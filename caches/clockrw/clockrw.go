package clockrw

// реализация кэша с алгоритмом clock, но с RWmutex и atomic.Bool для Node.visited
// Из-за короткой операции чтения этот вариант оказывается менее производительным, чем реализация с обычным мьютексом

import (
	"sync"
	"sync/atomic"

	"github.com/ValeriyTyutyunnik/go-caches/internal/stats"
)

type Node[K, V any] struct {
	key     K
	val     V
	visited atomic.Bool
}

type Cache[K comparable, V any] struct {
	mu      sync.RWMutex
	hash    map[K]int
	cache   []Node[K, V]
	delList []int
	stats   stats.Stats
	cap     int
	hand    int
}

func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity <= 0 {
		panic("емкость должна быть > 0")
	}
	c := &Cache[K, V]{
		cap:     capacity,
		hash:    make(map[K]int, capacity),
		cache:   make([]Node[K, V], capacity),
		hand:    -1,
		delList: make([]int, 0, 8),
	}
	return c
}

func (c *Cache[K, V]) GetStatistic() string { return c.stats.String() } // atomic operation
func (c *Cache[K, V]) Stats() stats.Reader  { return &c.stats }

func (c *Cache[K, V]) Len() int {
	c.mu.RLock()
	l := len(c.hash)
	c.mu.RUnlock()
	return l
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.RLock()
	indx, ok := c.hash[key]

	if !ok {
		c.mu.RUnlock()
		c.stats.Miss()
		var zero V
		return zero, false
	}

	elem := &c.cache[indx]

	if !elem.visited.Load() { // не инвалидируем зря L1 кэш процессора
		elem.visited.Store(true)
	}

	val := elem.val
	c.mu.RUnlock()

	c.stats.Hit()
	return val, true
}

func (c *Cache[K, V]) Put(key K, val V) {
	c.mu.Lock()

	if idx, ok := c.hash[key]; ok {
		elem := &c.cache[idx]
		elem.val = val // Полностью безопасно!
		if !elem.visited.Load() {
			elem.visited.Store(true)
		}
		c.mu.Unlock()
		c.stats.Upd()
		return
	}

	var (
		freeIdx    int
		evictSteps uint64
	)
	if len(c.delList) > 0 {
		freeIdx = c.delList[len(c.delList)-1]
		c.delList = c.delList[:len(c.delList)-1]
	} else {
		freeIdx = len(c.hash)
		if freeIdx >= c.cap {
			freeIdx, evictSteps = c.freeSlot()
		}
	}

	// Переиспользуем слот из массива
	slot := &c.cache[freeIdx]
	slot.key = key
	slot.val = val
	slot.visited.Store(false)

	c.hash[key] = freeIdx
	c.mu.Unlock()
	if evictSteps > 0 {
		c.stats.Evict()
		c.stats.AddScans(evictSteps)
	}
}

// гарантированно очищает один слот. Возвращает его индекс и кол-во шагов затраченных на вытеснение
func (c *Cache[K, V]) freeSlot() (int, uint64) {
	var steps uint64
	for {
		steps++
		c.hand++
		if c.hand >= c.cap {
			c.hand = 0
		}
		elem := &c.cache[c.hand]
		if !elem.visited.Load() {
			delete(c.hash, elem.key)
			return c.hand, steps
		}
		elem.visited.Store(false)
	}
}

func (c *Cache[K, V]) DeleteKey(key K) bool {
	c.mu.Lock()

	if idx, ok := c.hash[key]; ok {
		elem := &c.cache[idx]
		var zeroV V
		var zeroK K
		elem.key = zeroK
		elem.val = zeroV
		elem.visited.Store(false)
		c.delList = append(c.delList, idx)
		delete(c.hash, key)
		c.mu.Unlock()
		c.stats.Del()
		return true
	}

	c.mu.Unlock()
	return false
}
