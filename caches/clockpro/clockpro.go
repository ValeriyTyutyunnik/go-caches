package clockpro

// реализация кэша с алгоритмом CLOCK-Pro
// Продолжение алгоритма LIRS, но используется кольцевой список, вместо двухсязного

// Элементы делятся на горячие (Hot) и холодные (Cold), также хранятся призрачные метаданные для истории вытесненных ключей
// Использует три указателя (hand_hot, hand_cold, hand_test) и биты обращения (reference bits) для перевода холодных страниц в горячие и вытеснения неактивных
// Сохраняет устойчивость LIRS к сканированиям, но работает намного быстрее и дружелюбнее к многопоточности за счёт структуры кольца

import (
	"sync"

	"github.com/ValeriyTyutyunnik/go-caches/internal/genericRing"
	"github.com/ValeriyTyutyunnik/go-caches/internal/stats"
)

const (
	statusHot  uint8 = iota //  горячий элемент
	statusCold              // холодный элемент
	statusTest              // элемент-призрак без данных

)

type Node[V any] struct {
	val     V
	status  uint8
	visited bool
}

type Cache[K comparable, V any] struct {
	mu        sync.Mutex
	hash      map[K]*genericRing.Element[K, Node[V]]
	list      *genericRing.Ring[K, Node[V]]
	stats     stats.Stats
	handHot   *genericRing.Element[K, Node[V]]
	handCold  *genericRing.Element[K, Node[V]]
	handTest  *genericRing.Element[K, Node[V]]
	spare     *genericRing.Element[K, Node[V]] // элемент для переиспользования без лишней аллокации
	capacity  int
	capCold   int // адаптивная квота холодных елементов. Изначально max(1, capacity/2), меняется от 1 до capacity-1
	hotCount  int // hotCount + coldCount <= capacity
	coldCount int
	testCount int // testCount <= capacity
}

func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity <= 0 {
		panic("емкость должна быть > 0")
	}
	capCold := max(1, capacity/2)

	res := &Cache[K, V]{
		capacity: capacity,
		capCold:  capCold,
		hash:     make(map[K]*genericRing.Element[K, Node[V]], capacity*2),
		list:     genericRing.NewRing[K, Node[V]](),
	}

	return res
}

func (c *Cache[K, V]) GetStatistic() string { return c.stats.String() } // atomic operation
func (c *Cache[K, V]) Stats() stats.Reader  { return &c.stats }

func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	l := c.hotCount + c.coldCount
	c.mu.Unlock()
	return l
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()

	elem, ok := c.hash[key]
	if !ok || elem.Value.status == statusTest {
		c.mu.Unlock()
		c.stats.Miss()
		var zero V
		return zero, false
	}

	if !elem.Value.visited { // не инвалидируем зря L1 кэш процессора
		elem.Value.visited = true
	}
	val := elem.Value.val

	c.mu.Unlock()
	c.stats.Hit()
	return val, true
}

func (c *Cache[K, V]) Put(key K, value V) {
	c.mu.Lock()

	elem, ok := c.hash[key]
	if ok && (elem.Value.status == statusHot || elem.Value.status == statusCold) {
		elem.Value.val = value
		if !elem.Value.visited {
			elem.Value.visited = true
		}

		c.mu.Unlock()
		c.stats.Upd()
		return

	} else if ok && elem.Value.status == statusTest {
		// Сначала снимаем элемент с кольца, чтобы он не удалился в evictCold()=> runHandTest()
		c.removeFromRing(elem, false)
		c.testCount--

		// Адаптируем квоту и освобождаем слот
		c.capCold = max(1, c.capCold-1)
		if c.hotCount+c.coldCount >= c.capacity {
			c.evictCold()
		}

		// Воскрешаем элемент как Hot
		elem.Value.val = value
		elem.Value.visited = false
		elem.Value.status = statusHot

		c.list.InsertBefore(elem, c.handHot)
		c.initHands(elem)
		c.hotCount++

		if c.hotCount > c.capacity-c.capCold {
			c.runHandHot()
		}
		c.mu.Unlock()
		return
	}

	// если ключ новый
	// пытаемся получить переиспользуемый узел от вытесненного элемента
	var reused *genericRing.Element[K, Node[V]]
	if c.hotCount+c.coldCount >= c.capacity {
		reused = c.evictCold()
	}

	if reused == nil && c.spare != nil {
		reused, c.spare = c.spare, nil
	}

	if reused != nil {
		reused.Key = key
		reused.Value = Node[V]{val: value, status: statusCold, visited: false}
		elem = reused
	} else {
		elem = genericRing.NewElem(key, Node[V]{val: value, status: statusCold, visited: false})
	}

	c.list.InsertBefore(elem, c.handHot)
	c.hash[key] = elem
	c.coldCount++

	c.initHands(elem)
	c.mu.Unlock()
}

// инициализация рук при первом элементе кэша
func (c *Cache[K, V]) initHands(elem *genericRing.Element[K, Node[V]]) {
	if c.list.Len() == 1 {
		c.handHot, c.handCold, c.handTest = elem, elem, elem
	}
}

func (c *Cache[K, V]) evictCold() *genericRing.Element[K, Node[V]] {
	var steps uint64
	for {
		if c.coldCount == 0 {
			c.runHandHot()
			if c.coldCount == 0 {
				return nil
			}
		}
		steps++
		if c.handCold.Value.status == statusCold {
			if c.handCold.Value.visited {
				c.handCold.Value.status = statusHot
				c.handCold.Value.visited = false
				c.coldCount--
				c.hotCount++
				c.handCold = c.handCold.Next()
				if c.hotCount > c.capacity-c.capCold {
					c.runHandHot()
				}
			} else {
				c.handCold.Value.status = statusTest
				c.coldCount--
				c.testCount++

				var zero V
				c.handCold.Value.val = zero
				c.handCold = c.handCold.Next()

				var reused *genericRing.Element[K, Node[V]]
				if c.testCount > c.capacity {
					reused = c.runHandTest()
				}

				c.stats.AddScans(steps)
				c.stats.Evict()
				return reused
			}
		} else {
			c.handCold = c.handCold.Next()
		}
	}
}

func (c *Cache[K, V]) runHandHot() {
	if c.hotCount == 0 {
		return
	}
	var steps uint64
	for {
		steps++
		switch c.handHot.Value.status {
		case statusHot:
			if c.handHot.Value.visited {
				// снимаем метку посещения
				c.handHot.Value.visited = false
				c.handHot = c.handHot.Next()
			} else {
				// нашли жертву для понижения
				c.handHot.Value.status = statusCold
				c.hotCount--
				c.coldCount++
				c.handHot = c.handHot.Next()
				c.stats.AddScans(steps)
				return
			}
		case statusCold:
			if c.handHot.Value.visited {
				// снимаем метку посещения
				c.handHot.Value.visited = false
			}
			c.handHot = c.handHot.Next()
		case statusTest:
			// если попался тестовый элемент - удаляем
			evict := c.handHot
			delete(c.hash, evict.Key)
			c.removeFromRing(evict, true)
			c.testCount--
			if c.spare == nil {
				c.spare = evict
			}
		}
	}
}

func (c *Cache[K, V]) runHandTest() *genericRing.Element[K, Node[V]] {
	if c.testCount == 0 {
		return nil
	}
	var steps uint64
	for {
		steps++
		if c.handTest.Value.status == statusTest {
			// нашли жертву для очистки
			evict := c.handTest
			c.testCount--

			// увеличиваем квоту Cold
			c.capCold = max(1, min(c.capacity-1, c.capCold+1))

			delete(c.hash, evict.Key)
			// отвязываем без очистки чтобы переиспользовать узел
			c.removeFromRing(evict, false)
			c.stats.AddScans(steps)
			return evict
		}
		c.handTest = c.handTest.Next()
	}
}

// безопасное удаление элемента с валидацией указателей рук
func (c *Cache[K, V]) removeFromRing(elem *genericRing.Element[K, Node[V]], fullClean bool) {
	if c.list.Len() == 1 {
		c.handHot, c.handCold, c.handTest = nil, nil, nil
	} else {
		if c.handHot == elem {
			c.handHot = elem.Next()
		}
		if c.handCold == elem {
			c.handCold = elem.Next()
		}
		if c.handTest == elem {
			c.handTest = elem.Next()
		}
	}
	c.list.Remove(elem, fullClean)
}

func (c *Cache[K, V]) DeleteKey(key K) bool {
	c.mu.Lock()

	elem, ok := c.hash[key]
	if !ok {
		c.mu.Unlock()
		return false
	}

	switch elem.Value.status {
	case statusCold:
		c.coldCount--
	case statusHot:
		c.hotCount--
	case statusTest:
		c.testCount--
	}

	c.removeFromRing(elem, true)
	delete(c.hash, key)
	c.mu.Unlock()

	c.stats.Del()
	return true
}
