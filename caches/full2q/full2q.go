package full2q

// реализация кэша с алгоритмом Full 2Q
// Использует три структуры: входную FIFO-очередь A1in (здесь In), горячий LRU-сегмент Am (здесь Main) и FIFO-очередь призраков (хранит только ключи) A1out (здесь Out)
// Новые элементы попадают в In; позиция при обращении не меняется, при вытеснении из In ключ переходит в очередь призраков A1out
// Элемент попадает в Main только из Out

// Главная очередь capMain = 75% от capacity, но минимум 1
// Входная очередь capacity - capMain
// Очередь призраков 50% от размера capacity, но минимум 1
// т.е. при capacity=1 + 1 призрак

import (
	"sync"

	"github.com/ValeriyTyutyunnik/go-caches/internal/genericList"
	"github.com/ValeriyTyutyunnik/go-caches/internal/stats"
)

const (
	segMain uint8 = iota
	segIn
	segOut
)

type Node[V any] struct {
	val     V
	segment uint8
}

type Cache[K comparable, V any] struct {
	mu       sync.Mutex
	hash     map[K]*genericList.Element[K, Node[V]]
	listMain *genericList.List[K, Node[V]]
	listIn   *genericList.List[K, Node[V]]
	listOut  *genericList.List[K, Node[V]]
	stats    stats.Stats
	capInit  int
	capMain  int
	capIn    int
	capOut   int
}

func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity <= 0 {
		panic("емкость должна быть > 0")
	}

	var capIn, capMain int
	if capacity == 1 {
		capIn = 0
		capMain = 1
	} else {
		capIn = max(1, capacity/4)
		capMain = capacity - capIn
	}
	capOut := max(1, capacity/2)

	res := &Cache[K, V]{
		capInit:  capacity,
		capMain:  capMain,
		capIn:    capIn,
		capOut:   capOut,
		hash:     make(map[K]*genericList.Element[K, Node[V]], capMain+capIn+capOut),
		listMain: genericList.NewList[K, Node[V]](),
		listIn:   genericList.NewList[K, Node[V]](),
		listOut:  genericList.NewList[K, Node[V]](),
	}

	return res
}

func (c *Cache[K, V]) GetStatistic() string { return c.stats.String() } // atomic operation
func (c *Cache[K, V]) Stats() stats.Reader  { return &c.stats }

func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	l := c.listMain.Len() + c.listIn.Len()
	c.mu.Unlock()
	return l
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()

	elem, ok := c.hash[key]
	if !ok || elem.Value.segment == segOut {
		c.mu.Unlock()
		c.stats.Miss()
		var zero V
		return zero, false
	}

	// В главной очереди перемещается в голову. В IN никуда не перемещается (Full 2Q)
	if elem.Value.segment == segMain {
		c.listMain.ToFront(elem)
	}

	val := elem.Value.val

	c.mu.Unlock()
	c.stats.Hit()
	return val, true
}

func (c *Cache[K, V]) Put(key K, value V) {
	c.mu.Lock()

	if elem, ok := c.hash[key]; ok {
		// Живой элемент (Main или In)
		if elem.Value.segment != segOut {
			if elem.Value.segment == segMain {
				c.listMain.ToFront(elem)
			}
			elem.Value.val = value
			c.mu.Unlock()
			c.stats.Upd()
			return
		}

		// Элемент в очереди призраков (segOut) — воскрешаем и переносим в Main без аллокации
		c.listOut.Remove(elem, false)
		elem.Value.val = value
		elem.Value.segment = segMain

		evicted := c.pushMainElement(elem)
		c.mu.Unlock()

		if evicted {
			c.stats.Evict()
			c.stats.AddScans(1)
		}
		return
	}

	var evicted bool
	if c.capIn == 0 {
		// Случай capacity == 1
		evicted = c.pushMainCap1(key, value)
	} else {
		evicted = c.pushIn(key, value)
	}

	c.mu.Unlock()
	if evicted {
		c.stats.Evict()
		c.stats.AddScans(1)
	}
}

// вставляет новый ключ в Main (используется только при capacity == 1)
func (c *Cache[K, V]) pushMainCap1(key K, value V) (evicted bool) {
	if c.listMain.Len() < c.capMain {
		elem := genericList.NewElem(key, Node[V]{val: value, segment: segMain})
		c.listMain.PushElement(elem)
		c.hash[key] = elem
		return false
	}

	// если занято - отправляем новый ключ в призраки Out
	var elem *genericList.Element[K, Node[V]]
	if c.listOut.Len() >= c.capOut {
		elem = c.listOut.Pop()
		delete(c.hash, elem.Key)
		elem.Key = key
		elem.Value = Node[V]{segment: segOut}
	} else {
		elem = genericList.NewElem(key, Node[V]{segment: segOut})
	}
	c.listOut.PushElement(elem)
	c.hash[key] = elem
	return false
}

// pushMainElement переносит уже существующий узел (из Out) в Main
func (c *Cache[K, V]) pushMainElement(elem *genericList.Element[K, Node[V]]) (evicted bool) {
	if c.listMain.Len() >= c.capMain {
		victim := c.listMain.Pop()
		delete(c.hash, victim.Key)
		var zero V
		victim.Value.val = zero
		evicted = true
	}
	c.listMain.PushElement(elem)
	return evicted
}

// pushIn вставляет новый ключ в In, вытесняя старый из In в Out
func (c *Cache[K, V]) pushIn(key K, value V) (evicted bool) {
	var elem *genericList.Element[K, Node[V]]

	if c.listIn.Len() >= c.capIn {
		// Вытесняем самый старый из In и переводим его в призраки Out без аллокации
		victim := c.listIn.Pop()
		var zero V
		victim.Value.val = zero
		victim.Value.segment = segOut
		evicted = true

		// Если очередь призраков Out полна — забираем её хвост для переиспользования под новый ключ
		if c.listOut.Len() >= c.capOut {
			elem = c.listOut.Pop()
			delete(c.hash, elem.Key)
			elem.Key = key
			elem.Value = Node[V]{val: value, segment: segIn}
		} else {
			elem = genericList.NewElem(key, Node[V]{val: value, segment: segIn})
		}
		c.listOut.PushElement(victim)
	} else {
		elem = genericList.NewElem(key, Node[V]{val: value, segment: segIn})
	}

	c.listIn.PushElement(elem)
	c.hash[key] = elem
	return evicted
}

func (c *Cache[K, V]) DeleteKey(key K) bool {
	c.mu.Lock()

	elem, ok := c.hash[key]
	if !ok {
		c.mu.Unlock()
		return false
	}

	switch elem.Value.segment {
	case segMain:
		c.listMain.Remove(elem, true)
	case segIn:
		c.listIn.Remove(elem, true)
	case segOut:
		c.listOut.Remove(elem, true)
	}

	delete(c.hash, key)

	c.mu.Unlock()
	c.stats.Del()
	return true
}
