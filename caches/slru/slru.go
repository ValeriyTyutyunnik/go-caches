package slru

// реализация кэша с алгоритмом SLRU (Segmented LRU)
// кэш делится на два сегмента — испытательный (Probationary) и защищённый (Protected)
// При повторном запросе элемент из испытательного сегмента и переносится в защищённый сегмент
// При переполнении Protected элемент вытесняется обратно в Probationary

// Главная очередь capMain (Protected) = 80% от capacity, минимум 1
// Входная очередь capIn (Probationary) - capacity-capMain

import (
	"sync"

	"github.com/ValeriyTyutyunnik/go-caches/internal/genericList"
	"github.com/ValeriyTyutyunnik/go-caches/internal/stats"
)

const (
	segMain uint8 = iota
	segIn
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
	spare    *genericList.Element[K, Node[V]]
	stats    stats.Stats
	capInit  int
	capMain  int
	capIn    int
}

func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity <= 0 {
		panic("емкость должна быть > 0")
	}
	capIn := capacity / 5
	capMain := capacity - capIn

	res := &Cache[K, V]{
		capInit:  capacity,
		capMain:  capMain,
		capIn:    capIn,
		hash:     make(map[K]*genericList.Element[K, Node[V]], capMain+capIn),
		listMain: genericList.NewList[K, Node[V]](),
		listIn:   genericList.NewList[K, Node[V]](),
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

	// если в главной очереди - двигаем элемент в голову
	if elem.Value.segment == segMain {
		c.listMain.ToFront(elem)
		val := elem.Value.val

		c.mu.Unlock()
		c.stats.Hit()
		return val, true
	}

	// элемент в IN очереди, перемещаем в Main
	c.promoteToMain(elem)
	val := elem.Value.val

	c.mu.Unlock()
	c.stats.Hit()
	return val, true
}

func (c *Cache[K, V]) promoteToMain(elem *genericList.Element[K, Node[V]]) {
	c.listIn.Remove(elem, false)
	if c.listMain.Len() >= c.capMain {
		evict := c.listMain.Pop()
		evict.Value.segment = segIn
		c.listIn.PushElement(evict)
	}
	elem.Value.segment = segMain
	c.listMain.PushElement(elem)
}

func (c *Cache[K, V]) Put(key K, value V) {
	c.mu.Lock()

	if elem, ok := c.hash[key]; ok {
		elem.Value.val = value
		if elem.Value.segment == segMain {
			c.listMain.ToFront(elem)
		} else if c.capMain > 0 {

			c.promoteToMain(elem)
		}

		c.mu.Unlock()
		c.stats.Upd()
		return
	}

	var (
		elem    *genericList.Element[K, Node[V]]
		evicted bool
	)

	if c.capIn == 0 {
		// Случай capacity < 5
		if c.listMain.Len() >= c.capMain {
			elem = c.listMain.Pop()
			delete(c.hash, elem.Key)
			evicted = true
		} else if c.spare != nil {
			elem, c.spare = c.spare, nil
		}
		if elem != nil {
			elem.Key = key
			elem.Value = Node[V]{val: value, segment: segMain}
		} else {
			elem = genericList.NewElem(key, Node[V]{val: value, segment: segMain})
		}
		c.listMain.PushElement(elem)
		c.hash[key] = elem
		c.mu.Unlock()
		if evicted {
			c.stats.Evict()
			c.stats.AddScans(1)
		}
		return
	}

	// Вытесняем из In только когда кэш достиг общей ёмкости capInit (или In переполнен при полном Main)
	if len(c.hash) >= c.capInit && c.listIn.Len() > 0 {
		elem = c.listIn.Pop()
		delete(c.hash, elem.Key)
		evicted = true
	} else if c.spare != nil {
		elem, c.spare = c.spare, nil
	}

	if elem != nil {
		elem.Key = key
		elem.Value = Node[V]{val: value, segment: segIn}
	} else {
		elem = genericList.NewElem(key, Node[V]{val: value, segment: segIn})
	}
	c.listIn.PushElement(elem)
	c.hash[key] = elem

	c.mu.Unlock()
	if evicted {
		c.stats.Evict()
		c.stats.AddScans(1)
	}
}

func (c *Cache[K, V]) DeleteKey(key K) bool {
	c.mu.Lock()

	elem, ok := c.hash[key]
	if !ok {
		c.mu.Unlock()
		return false
	}

	if elem.Value.segment == segMain {
		c.listMain.Remove(elem, true)
	} else {
		c.listIn.Remove(elem, true)
	}
	if c.spare == nil {
		c.spare = elem
	}
	delete(c.hash, key)

	c.mu.Unlock()
	c.stats.Del()
	return true
}
