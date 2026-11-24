package lru

// реализация кэша с алгоритмом LRU (Least Recently Used)
// Вытесняется элемент, к которому дольше всего не было обращений
// Элементы хранятся в двусвязном списке
// При каждом обращении (Get/Put) элемент перемещается в начало списка,
// При переполнении удаляется элемент из хвоста

import (
	"sync"

	"github.com/ValeriyTyutyunnik/go-caches/internal/genericList"
	"github.com/ValeriyTyutyunnik/go-caches/internal/stats"
)

type Cache[K comparable, V any] struct {
	mu    sync.Mutex
	hash  map[K]*genericList.Element[K, V]
	list  *genericList.List[K, V]
	stats stats.Stats
	cap   int
}

func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity <= 0 {
		panic("емкость должна быть > 0")
	}

	res := &Cache[K, V]{
		cap:  capacity,
		hash: make(map[K]*genericList.Element[K, V], capacity),
		list: genericList.NewList[K, V](),
	}

	return res
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

	c.list.ToFront(elem)
	val := elem.Value

	c.mu.Unlock()
	c.stats.Hit()
	return val, true
}

func (c *Cache[K, V]) Put(key K, value V) {
	c.mu.Lock()

	if elem, ok := c.hash[key]; ok {
		elem.Value = value
		c.list.ToFront(elem)

		c.mu.Unlock()
		c.stats.Upd()
		return
	}

	var (
		elem    *genericList.Element[K, V]
		evicted bool
	)

	if len(c.hash) >= c.cap {
		elem = c.list.Pop()
		if elem != nil {
			delete(c.hash, elem.Key)
			elem.Key = key
			elem.Value = value
			c.list.PushElement(elem)
			evicted = true
		}
	}

	if elem == nil {
		elem = c.list.Push(key, value)
	}
	c.hash[key] = elem

	c.mu.Unlock()
	if evicted {
		c.stats.Evict()
		c.stats.AddScans(1)
	}
}

func (c *Cache[K, V]) DeleteKey(key K) bool {
	c.mu.Lock()

	if elem, ok := c.hash[key]; ok {
		c.list.Remove(elem, true)
		delete(c.hash, key)

		c.mu.Unlock()
		c.stats.Del()
		return true
	}

	c.mu.Unlock()
	return false
}
