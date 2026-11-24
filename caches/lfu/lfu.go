package lfu

// реализация кэша с алгоритмом lfu (Least Frequently Used)
// Элементы группируются в двусвязные списки по частотам

// Вытесняется элемент с наименьшим количеством обращений за всё время жизни в кэше
// При попадании (Get) счётчик частоты элемента увеличивается на 1, а при равенстве частот вытесняется самый старый элемент

import (
	"math"
	"sync"

	"github.com/ValeriyTyutyunnik/go-caches/internal/genericList"
	"github.com/ValeriyTyutyunnik/go-caches/internal/stats"
)

type Node[V any] struct {
	val      V
	hitCount int
}

type Cache[K comparable, V any] struct {
	mu        sync.Mutex
	hash      map[K]*genericList.Element[K, Node[V]]
	freqList  map[int]*genericList.List[K, Node[V]]
	freeLists []*genericList.List[K, Node[V]]
	stats     stats.Stats
	baseList  *genericList.List[K, Node[V]]
	cap       int
	minFreq   int
}

func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity <= 0 {
		panic("емкость должна быть > 0")
	}
	c := &Cache[K, V]{
		cap:      capacity,
		hash:     make(map[K]*genericList.Element[K, Node[V]], capacity),
		freqList: make(map[int]*genericList.List[K, Node[V]], 16),
		minFreq:  1,
		baseList: genericList.NewList[K, Node[V]](),
	}
	// Создаём список для частоты 1 один раз на всё время жизни кэша
	c.freqList[1] = c.baseList
	return c
}

func (c *Cache[K, V]) GetStatistic() string { return c.stats.String() } // atomic operation
func (c *Cache[K, V]) Stats() stats.Reader  { return &c.stats }

func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	l := len(c.hash)
	c.mu.Unlock()
	return l
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()

	elem, ok := c.hash[key]
	if !ok {
		c.mu.Unlock()
		c.stats.Miss()
		var zero V
		return zero, false
	}
	// нашли - перемещаем элемент в новый список
	c.incrementFreq(elem)

	val := elem.Value.val

	c.mu.Unlock()
	c.stats.Hit()
	return val, true
}

func (c *Cache[K, V]) Put(key K, val V) {
	c.mu.Lock()

	// Если ключ существует
	if elem, ok := c.hash[key]; ok {
		elem.Value.val = val
		c.incrementFreq(elem)

		c.mu.Unlock()
		c.stats.Upd()
		return
	}

	// Получение элемента: переиспользуем вытесненный или создаём новый
	var (
		elem    *genericList.Element[K, Node[V]]
		evicted bool
	)
	if len(c.hash) >= c.cap {
		elem = c.evictOne()
		elem.Key = key
		elem.Value = Node[V]{val: val, hitCount: 1}
		c.baseList.PushElement(elem)
		evicted = true
	} else {
		elem = c.baseList.Push(key, Node[V]{val: val, hitCount: 1})
	}

	// складываем новый элемент в базовый список
	c.minFreq = 1
	c.hash[key] = elem
	c.mu.Unlock()
	if evicted {
		c.stats.Evict()
		c.stats.AddScans(1)
	}
}

// отвязка элемента от списка полная или частичная с сохранением
// clearData - флаг сохранения или обнуления key-val
func (c *Cache[K, V]) detachElement(elem *genericList.Element[K, Node[V]], freq int, clearData bool) bool {
	list := c.freqList[freq]
	list.Remove(elem, clearData)
	if list.Len() == 0 {
		if list != c.baseList {
			delete(c.freqList, freq)
			c.freeLists = append(c.freeLists, list)
		}
		return true
	}
	return false
}

// вытеснение наименее используемого элемента
func (c *Cache[K, V]) evictOne() *genericList.Element[K, Node[V]] {
	list := c.freqList[c.minFreq]
	evictElem := list.Pop()
	delete(c.hash, evictElem.Key)
	if list.Len() == 0 && list != c.baseList {
		delete(c.freqList, c.minFreq)
		c.freeLists = append(c.freeLists, list)
	}
	return evictElem
}

// перемещение элемента в список с большей частотой
func (c *Cache[K, V]) incrementFreq(elem *genericList.Element[K, Node[V]]) {
	oldFreq := elem.Value.hitCount
	if c.detachElement(elem, oldFreq, false) && c.minFreq == oldFreq {
		c.minFreq++
	}

	elem.Value.hitCount++
	c.getOrCreateList(elem.Value.hitCount).PushElement(elem)
}

// получить или создать список для указанной частоты
func (c *Cache[K, V]) getOrCreateList(freq int) *genericList.List[K, Node[V]] {
	list, ok := c.freqList[freq]
	if !ok {
		if n := len(c.freeLists); n > 0 {
			list = c.freeLists[n-1]
			c.freeLists = c.freeLists[:n-1]
		} else {
			list = genericList.NewList[K, Node[V]]()
		}
		c.freqList[freq] = list
	}
	return list
}

// переопеределение минимальной частоты
func (c *Cache[K, V]) recalcMinFreq() {
	if c.baseList.Len() > 0 || len(c.hash) == 0 {
		c.minFreq = 1
		return
	}
	c.minFreq = math.MaxInt
	for f, list := range c.freqList {
		if list.Len() > 0 && f < c.minFreq {
			c.minFreq = f
		}
	}
}

func (c *Cache[K, V]) DeleteKey(key K) bool {
	c.mu.Lock()

	elem, ok := c.hash[key]
	if !ok {
		c.mu.Unlock()
		return false
	}

	freq := elem.Value.hitCount
	delete(c.hash, key)

	// если при удалении оказалось, что это последний элемент в списке с минимальной частотой, то нужно получить новую минимальную частоту
	if c.detachElement(elem, freq, true) && c.minFreq == freq {
		c.recalcMinFreq()
	}

	c.mu.Unlock()
	c.stats.Del()
	return true
}
