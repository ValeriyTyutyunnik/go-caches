package lrfu

// реализация кэша с алгоритмом LRFU - Least Recently/Frequently Used
// последнее ображщние (LRU) и частота (LFU) вычисляются через единый вес CRF (Combined Recency and Frequency)
// CRF экспоненциально затухает со временем: CRF(t) = 1 + 2^(-lambda * dt) * CRF(t_last)
// Параметр lambda (0..1) регулирует баланс: при lambda=0 алгоритм ведёт себя как чистый LFU, при lambda=1 - как чистый LRU
// Элементы хранятся в бинарной мин-куче по текущему весу CRF, вытесняется элемент с наименьшим весом

import (
	"math"
	"sync"

	"github.com/ValeriyTyutyunnik/go-caches/internal/stats"
)

// Порог переполнения logWeight (~10^15 операций до ренормализации)
const maxLogWeight = 1e12

type Cache[K comparable, V any] struct {
	mu       sync.Mutex
	capacity int
	hash     map[K]*entry[K, V]
	spare    *entry[K, V] // переиспользование удаленного элемента
	h        lrfuHeap[K, V]
	stats    stats.Stats
}

func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity <= 0 {
		panic("емкость должна быть > 0")
	}
	return NewWithLambda[K, V](capacity, 1.0/float64(capacity))
}

func NewWithLambda[K comparable, V any](capacity int, lambda float64) *Cache[K, V] {
	if capacity <= 0 {
		panic("емкость должна быть > 0")
	}
	if lambda < 0 || lambda > 1 || math.IsNaN(lambda) {
		panic("lambda должна быть в диапазоне [0, 1]")
	}
	return &Cache[K, V]{
		capacity: capacity,
		hash:     make(map[K]*entry[K, V], capacity),
		h: lrfuHeap[K, V]{
			items:  make([]*entry[K, V], 0, capacity),
			lambda: lambda,
		},
	}
}

func (c *Cache[K, V]) GetStatistic() string { return c.stats.String() }
func (c *Cache[K, V]) Stats() stats.Reader  { return &c.stats }

func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	l := len(c.hash)
	c.mu.Unlock()
	return l
}

func (c *Cache[K, V]) touch(e *entry[K, V]) {
	c.h.now++
	nowLog := c.h.lambda * float64(c.h.now)

	// Вычисляем новый вес через log2(2^nowLog + 2^e.logWeight) без переполнения float64:
	// - при diff < -53.0 старый вес меньше машинного эпсилона float64 относительно 1.0 (2^0), поэтому он обнуляется;
	// - при diff > 53.0 прибавление 1.0 (2^0) меньше машинного эпсилона float64 и не меняет e.logWeight.
	diff := e.logWeight - nowLog
	if diff < -53.0 {
		e.logWeight = nowLog
	} else if diff <= 53.0 {
		e.logWeight = nowLog + math.Log1p(math.Exp2(diff))*math.Log2E
	}
	e.lastTime = c.h.now

	// Вес при touch() ВСЕГДА только растёт (в min-heap элемент может двигаться только вниз).
	// Поэтому вместо fix() вызываем сразу down(), а для листьев (e.index >= len/2) не делаем вообще ничего!
	if e.index < len(c.h.items)/2 {
		c.h.down(e.index, len(c.h.items))
	}

	if nowLog > maxLogWeight {
		c.h.renormalize()
	}
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

	c.touch(elem)
	val := elem.val

	c.mu.Unlock()
	c.stats.Hit()
	return val, true
}

func (c *Cache[K, V]) Put(key K, val V) {
	c.mu.Lock()

	if elem, ok := c.hash[key]; ok {
		elem.val = val
		c.touch(elem)

		c.mu.Unlock()
		c.stats.Upd()
		return
	}

	c.h.now++
	nowLog := c.h.lambda * float64(c.h.now) // т.к. weight = 1.0, log2(1.0) = 0

	// При заполнении кэша вытесняем корень кучи и сразу ставим новый элемент на его место
	if len(c.hash) >= c.capacity {
		c.h.scanned = 0
		victim := c.h.items[0]
		delete(c.hash, victim.key)

		// Переиспользуем структуру victim
		victim.key = key
		victim.val = val
		victim.logWeight = nowLog
		victim.lastTime = c.h.now
		c.hash[key] = victim

		c.h.replaceRoot(victim)

		if nowLog > maxLogWeight {
			c.h.renormalize()
		}

		c.mu.Unlock()
		c.stats.Evict()
		c.stats.AddScans(max(1, c.h.scanned))
		return
	}

	var e *entry[K, V]
	if c.spare != nil {
		e, c.spare = c.spare, nil
		e.key = key
		e.val = val
		e.logWeight = nowLog
		e.lastTime = c.h.now
	} else {
		e = &entry[K, V]{
			key:       key,
			val:       val,
			logWeight: nowLog,
			lastTime:  c.h.now,
		}
	}
	c.hash[key] = e
	c.h.push(e)
	c.mu.Unlock()
}

func (c *Cache[K, V]) DeleteKey(key K) bool {
	c.mu.Lock()

	elem, ok := c.hash[key]
	if !ok {
		c.mu.Unlock()
		return false
	}

	c.h.remove(elem.index)
	delete(c.hash, key)
	var zeroK K
	var zeroV V
	elem.key = zeroK
	elem.val = zeroV
	if c.spare == nil {
		c.spare = elem
	}

	c.mu.Unlock()
	c.stats.Del()
	return true
}
