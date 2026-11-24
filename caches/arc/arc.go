package arc

// реализация кэша с алгоритмом ARC - Adaptive Replacement Cache (IBM, 2003)
// срок действия патента IBM (US6996676B2) истёк в 2023 году
// алгоритм свободен от лицензионных ограничений и доступен для коммерческого и open-source использования

// состоит из 4 списков, каждый работает как стандартный LRU
// T1 (Top 1 - недавние данные: все новые элементы, запрошенные впервые. Целевой размер списка "p"
// T2 (Top 2 - повторные запросы: все элементы из T1, к которым обратились повторно, и из B1/B2.  Целевой размер списка: "capacity - p"
// B1 (Bottom 1 - призраки из T1): хранит только ключи (без значений), недавно вытесненные из хвоста T1
// B2 (Bottom 2 - призраки из T2): хранит только ключи, недавно вытесненные из хвоста T2

// Ограничения:
// Живой кэш: T1.Len() + T2.Len() <= capacity
// История целиком: t1.Len() + t2.Len() + b1.Len() + b2.Len() <= capacity*2
// Первая половина истории: t1.Len() + b1.Len() <= capacity

import (
	"sync"

	"github.com/ValeriyTyutyunnik/go-caches/internal/genericList"
	"github.com/ValeriyTyutyunnik/go-caches/internal/stats"
)

const (
	listT1 uint8 = iota // недавние живые элементы
	listT2              // частые (повторные) живые элементы
	listB1              // призраки, вытесненные из T1
	listB2              // призраки, вытесненные из T2
)

type Node[V any] struct {
	val     V
	address uint8
}

type Cache[K comparable, V any] struct {
	mu       sync.Mutex
	hash     map[K]*genericList.Element[K, Node[V]]
	t1       *genericList.List[K, Node[V]] // Top 1 — недавние живые данные, LRU для новых ключей. Целевой размер "p"
	t2       *genericList.List[K, Node[V]] // Top 2 — живые данные, аналог LFU/LRU для повторных запросов. Целевой размер "capacity - p"
	b1       *genericList.List[K, Node[V]] // Bottom 1 — призраки из T1 без данных
	b2       *genericList.List[K, Node[V]] // Bottom 2 — призраки из T2 без данных
	stats    stats.Stats
	capacity int
	p        int // адаптивный размер listT1 (0 <= p <= capacity)
}

func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity <= 0 {
		panic("емкость должна быть > 0")
	}

	// p=0 на старте
	res := &Cache[K, V]{
		capacity: capacity,
		hash:     make(map[K]*genericList.Element[K, Node[V]], capacity*2),
		t1:       genericList.NewList[K, Node[V]](),
		t2:       genericList.NewList[K, Node[V]](),
		b1:       genericList.NewList[K, Node[V]](),
		b2:       genericList.NewList[K, Node[V]](),
	}

	return res
}

func (c *Cache[K, V]) GetStatistic() string { return c.stats.String() } // atomic operation
func (c *Cache[K, V]) Stats() stats.Reader  { return &c.stats }

func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	l := c.t1.Len() + c.t2.Len()
	c.mu.Unlock()
	return l
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()

	elem, ok := c.hash[key]
	if !ok || elem.Value.address >= listB1 {
		c.mu.Unlock()

		c.stats.Miss()
		var zero V
		return zero, false
	}

	c.onHit(elem) // listT1 || listT2

	val := elem.Value.val
	c.mu.Unlock()

	c.stats.Hit()
	return val, true
}

// действия с элементом при попадаении кэша
func (c *Cache[K, V]) onHit(elem *genericList.Element[K, Node[V]]) (evicted bool) {
	switch elem.Value.address {
	// если элемент в T1 - переносим в T2
	case listT1:
		c.t1.Remove(elem, false)
		elem.Value.address = listT2
		c.t2.PushElement(elem)
	case listT2:
		c.t2.ToFront(elem)
	case listB1:
		// шаг адаптации вверх
		delta := 1
		if c.b1.Len() > 0 && c.b2.Len() > c.b1.Len() {
			delta = c.b2.Len() / c.b1.Len()
		}
		c.p = min(c.capacity, c.p+delta)
		evicted = c.replace(false)
		c.b1.Remove(elem, false)
		elem.Value.address = listT2
		c.t2.PushElement(elem)
	case listB2:
		// шаг адаптации вниз
		delta := 1
		if c.b2.Len() > 0 && c.b1.Len() > c.b2.Len() {
			delta = c.b1.Len() / c.b2.Len()
		}
		c.p = max(0, c.p-delta)
		evicted = c.replace(true)
		c.b2.Remove(elem, false)
		elem.Value.address = listT2
		c.t2.PushElement(elem)
	}
	return
}

func (c *Cache[K, V]) Put(key K, value V) {
	c.mu.Lock()

	if elem, ok := c.hash[key]; ok {
		isUpdate := elem.Value.address == listT1 || elem.Value.address == listT2
		evicted := c.onHit(elem)
		elem.Value.val = value

		c.mu.Unlock()

		if isUpdate {
			c.stats.Upd()
		}
		if evicted {
			c.stats.Evict()
			c.stats.AddScans(1)
		}
		return
	}

	var (
		reused  *genericList.Element[K, Node[V]]
		evicted bool
	)
	if c.t1.Len()+c.b1.Len() == c.capacity {
		if c.t1.Len() < c.capacity {
			if c.b1.Len() > 0 {
				reused = c.b1.Pop()
				delete(c.hash, reused.Key)
			}
			evicted = c.replace(false)
		} else { // c.t1.Len() == c.cap, значит в b1 пусто, удаляем из t1
			reused = c.t1.Pop()
			delete(c.hash, reused.Key)
			evicted = true

		}
	} else {
		// t1 + b1 < c.capacity
		total := c.t1.Len() + c.t2.Len() + c.b1.Len() + c.b2.Len()
		if total >= c.capacity {
			if total >= 2*c.capacity && c.b2.Len() > 0 {
				reused = c.b2.Pop()
				delete(c.hash, reused.Key)
			}
			evicted = c.replace(false)
		}
	}
	if reused != nil {
		reused.Key = key
		reused.Value = Node[V]{val: value, address: listT1}
		c.t1.PushElement(reused)
	} else {
		reused = c.t1.Push(key, Node[V]{val: value, address: listT1})
	}
	c.hash[key] = reused

	c.mu.Unlock()
	if evicted {
		c.stats.Evict()
		c.stats.AddScans(1)
	}
}

// переносит один элемент из t1 или t2 в b1 или b2
// возвращает true если вытеснил
func (c *Cache[K, V]) replace(inB2 bool) (evicted bool) {
	if c.t1.Len()+c.t2.Len() < c.capacity {
		return false
	}

	// вытесняем из t1
	if c.t1.Len() > 0 && (c.t1.Len() > c.p || (inB2 && c.t1.Len() == c.p)) {
		victim := c.t1.Pop()
		var zero V
		victim.Value.val = zero
		victim.Value.address = listB1
		c.b1.PushElement(victim)
		return true
	}

	// вытесняем из t2
	if c.t2.Len() > 0 {
		victim := c.t2.Pop()
		var zero V
		victim.Value.val = zero
		victim.Value.address = listB2
		c.b2.PushElement(victim)
		return true
	}

	return false
}

func (c *Cache[K, V]) DeleteKey(key K) bool {
	c.mu.Lock()
	elem, ok := c.hash[key]
	if !ok {
		c.mu.Unlock()
		return false
	}

	switch elem.Value.address {
	case listT1:
		c.t1.Remove(elem, true)
	case listT2:
		c.t2.Remove(elem, true)
	case listB1:
		c.b1.Remove(elem, true)
	case listB2:
		c.b2.Remove(elem, true)
	}

	delete(c.hash, key)
	c.mu.Unlock()

	c.stats.Del()
	return true
}
