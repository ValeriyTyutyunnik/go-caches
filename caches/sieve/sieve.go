package sieve

// реализация кэша с алгоритмом SIEVE

// Элементы хранятся в одной очереди FIFO. Новые записи добавляются в голову, а при попадании элементы не перемещаются, им ставится бит visited = true
// Указатель hand движется от хвоста к голове: если у элемента visited==true сбрасывается в false для второй жизни; элемент с visited==false удаляется

import (
	"sync"

	"github.com/ValeriyTyutyunnik/go-caches/internal/genericList"
	"github.com/ValeriyTyutyunnik/go-caches/internal/stats"
)

type Node[V any] struct {
	val     V
	visited bool
}

type Cache[K comparable, V any] struct {
	mu    sync.Mutex
	hash  map[K]*genericList.Element[K, Node[V]]
	list  *genericList.List[K, Node[V]]
	stats stats.Stats
	hand  *genericList.Element[K, Node[V]]
	spare *genericList.Element[K, Node[V]] // переиспользование удаленного ключа
	cap   int
}

func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity <= 0 {
		panic("емкость должна быть > 0")
	}
	c := &Cache[K, V]{
		cap:  capacity,
		hash: make(map[K]*genericList.Element[K, Node[V]], capacity),
		list: genericList.NewList[K, Node[V]](),
	}
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

	if !elem.Value.visited {
		elem.Value.visited = true
	}
	val := elem.Value.val

	c.mu.Unlock()
	c.stats.Hit()
	return val, true
}

func (c *Cache[K, V]) Put(key K, val V) {
	c.mu.Lock()

	if elem, ok := c.hash[key]; ok {
		elem.Value.val = val
		if !elem.Value.visited {
			elem.Value.visited = true
		}

		c.mu.Unlock()
		c.stats.Upd()
		return
	}

	var (
		e          *genericList.Element[K, Node[V]]
		evictSteps uint64
	)
	if len(c.hash) >= c.cap {
		e, evictSteps = c.evictOne()
	} else if c.spare != nil {
		e, c.spare = c.spare, nil
	}
	if e != nil {
		e.Key = key
		e.Value = Node[V]{val: val, visited: false}
		c.list.PushElement(e)
	} else {
		e = c.list.Push(key, Node[V]{val: val, visited: false})
	}
	c.hash[key] = e
	c.mu.Unlock()
	if evictSteps > 0 {
		c.stats.AddScans(evictSteps)
		c.stats.Evict()
	}
}

func (c *Cache[K, V]) evictOne() (*genericList.Element[K, Node[V]], uint64) {
	var steps uint64

	for {
		if c.hand == nil {
			c.hand = c.list.Back()
		}
		steps++
		if !c.hand.Value.visited {
			evicted := c.hand
			c.hand = c.hand.Prev()
			delete(c.hash, evicted.Key)
			c.list.Remove(evicted, false)
			return evicted, steps
		}
		c.hand.Value.visited = false
		c.hand = c.hand.Prev()
	}
}

func (c *Cache[K, V]) DeleteKey(key K) bool {
	c.mu.Lock()

	if elem, ok := c.hash[key]; ok {
		if c.hand == elem {
			c.hand = elem.Prev()
		}
		delete(c.hash, key)
		c.list.Remove(elem, true)
		if c.spare == nil {
			c.spare = elem
		}

		c.mu.Unlock()
		c.stats.Del()
		return true
	}

	c.mu.Unlock()
	return false
}
