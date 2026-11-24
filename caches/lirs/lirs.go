package lirs

// реализация кэша с алгоритмом LIRS (Low Inter-reference Recency Set)
// Использует расстояние повторного обращения (IRR — число уникальных элементов между двумя обращениями к ключу)
// Кэш делится на две группы: горячие блоки LIR (малый IRR) и холодные блоки HIR (большой IRR), при этом в памяти хранятся все LIR и только часть HIR
// Другая часть (non-resident HIR) находится в истории. При обращении повышается до LIR, а самый старый LIR понижается в HIR

// HIR-сегмент 1% от capacity, но минимум 1
// LIR-сегмент - capacity-capHIR
// стек из-за призраков может вырастать до capacity*2

import (
	"sync"

	"github.com/ValeriyTyutyunnik/go-caches/internal/genericList"
	"github.com/ValeriyTyutyunnik/go-caches/internal/stats"
)

const (
	statusResidentHIR    uint8 = iota // холодный элемент, лежит в обоих списках
	statusLIR                         // горячий элемент, лежит в listS
	statusNonResidentHIR              // элемент-призрак, лежит в listS без данных

)

type Entry[K comparable, V any] struct {
	key    K
	val    V
	elemS  *genericList.Element[K, *Entry[K, V]] // ссылка на узел в списке S
	elemQ  *genericList.Element[K, *Entry[K, V]] // ссылка на узел в списке Q
	status uint8
}

type Cache[K comparable, V any] struct {
	mu        sync.Mutex
	hash      map[K]*Entry[K, V]
	listS     *genericList.List[K, *Entry[K, V]]
	listQ     *genericList.List[K, *Entry[K, V]]
	spare     *Entry[K, V]
	stats     stats.Stats
	lirCount  int
	capInit   int
	capHIR    int
	capLIR    int
	maxStackS int
}

func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity <= 0 {
		panic("емкость должна быть > 0")
	}
	capHIR := max(1, capacity/100)
	capLIR := capacity - capHIR
	maxStackS := capacity * 2

	res := &Cache[K, V]{
		capInit:   capacity,
		capHIR:    capHIR,
		capLIR:    capLIR,
		maxStackS: maxStackS,
		hash:      make(map[K]*Entry[K, V], maxStackS),
		listS:     genericList.NewList[K, *Entry[K, V]](),
		listQ:     genericList.NewList[K, *Entry[K, V]](),
	}

	return res
}

func (c *Cache[K, V]) GetStatistic() string { return c.stats.String() } // atomic operation
func (c *Cache[K, V]) Stats() stats.Reader  { return &c.stats }

func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	l := c.lirCount + c.listQ.Len()
	c.mu.Unlock()
	return l
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()

	elem, ok := c.hash[key]
	if !ok || elem.status == statusNonResidentHIR {
		c.mu.Unlock()
		c.stats.Miss()
		var zero V
		return zero, false
	}
	c.onHit(elem)

	val := elem.val

	c.mu.Unlock()
	c.stats.Hit()
	return val, true
}

func (c *Cache[K, V]) onHit(e *Entry[K, V]) {
	switch e.status {
	case statusLIR:
		// перемещаем элемент с хвоста в голову
		wasBottom := c.listS.Back() == e.elemS
		c.listS.ToFront(e.elemS)
		// если элемент был в хвосте - чистим стек до LIR
		if wasBottom {
			c.pruneStack()
		}
	case statusResidentHIR:
		if c.capLIR == 0 {
			c.listQ.ToFront(e.elemQ)
		} else if e.elemS != nil {
			// раз элемент в S, то его IRR меньше, чем у блока LIR. Повышаем до LIR
			c.promoteLIR(e)
			// понижаем вместо него хвост
			c.depromoteLIR()
		} else {
			c.listQ.ToFront(e.elemQ)
			e.elemS = c.listS.Push(e.key, e)
		}
	}
}

func (c *Cache[K, V]) Put(key K, value V) {
	var evicted bool

	c.mu.Lock()

	entry, ok := c.hash[key]
	if ok && (entry.status == statusLIR || entry.status == statusResidentHIR) {
		entry.val = value
		c.onHit(entry)

		c.mu.Unlock()
		c.stats.Upd()
		return
	} else if ok && entry.status == statusNonResidentHIR {
		// если элемент еще есть как призрак - повышаем его сразу до LIR
		entry.val = value
		if c.lirCount+c.listQ.Len() >= c.capInit {
			c.evictOneHIR()
			evicted = true
		}
		c.promoteLIR(entry)
		// если LIR превышает размер - понижаем дно до HIR
		if c.lirCount > c.capLIR {
			c.depromoteLIR()
		}
		// upd не нужен - призрак это новый элемент
		c.mu.Unlock()
		if evicted {
			c.stats.Evict()
			c.stats.AddScans(1)
		}
		return
	}

	// новый ключ
	if c.spare != nil {
		entry, c.spare = c.spare, nil
		entry.key = key
		entry.val = value
	} else {
		entry = &Entry[K, V]{key: key, val: value}
	}
	c.hash[key] = entry

	// если в LIR сегменте есть свободные слоты - сразу записываем элемент в LIR
	if c.lirCount < c.capLIR && c.capLIR > 0 {
		entry.status = statusLIR
		c.lirCount++
		entry.elemS = c.listS.Push(entry.key, entry)
		c.mu.Unlock()
		return
	}

	if c.lirCount+c.listQ.Len() >= c.capInit {
		c.evictOneHIR()
		evicted = true
	}
	entry.status = statusResidentHIR
	// Если стек S переполнен сканированием — не пушим в S, чтобы не раздувать историю призраков
	if c.listS.Len() >= c.maxStackS {
		c.evictOldestGhostFromS()
	}
	entry.elemS = c.listS.Push(entry.key, entry)
	entry.elemQ = c.listQ.Push(entry.key, entry)
	c.pruneStack()
	c.mu.Unlock()
	if evicted {
		c.stats.Evict()
		c.stats.AddScans(1)
	}
}

func (c *Cache[K, V]) promoteLIR(e *Entry[K, V]) {
	c.listS.ToFront(e.elemS)
	if e.elemQ != nil {
		c.listQ.Remove(e.elemQ, true)
		e.elemQ = nil
	}
	e.status = statusLIR
	c.lirCount++
}

// Понижает самый старый LIR со дна S в ResidentHIR до statusResidendHIR
func (c *Cache[K, V]) depromoteLIR() {
	bottom := c.listS.Pop().Value // берем LIR элемент (он гарантированно находится хвосте)
	bottom.elemS = nil
	bottom.status = statusResidentHIR
	c.lirCount--
	bottom.elemQ = c.listQ.Push(bottom.key, bottom)
	c.pruneStack() // чистим стек до следующего LIR
}

// вытесняем HIR из listQ
func (c *Cache[K, V]) evictOneHIR() {
	victim := c.listQ.Pop().Value
	victim.elemQ = nil
	var zero V
	victim.val = zero

	if victim.elemS == nil {
		delete(c.hash, victim.key)
		var zeroK K
		victim.key = zeroK
		if c.spare == nil {
			c.spare = victim
		}
	} else {
		victim.status = statusNonResidentHIR
	}
}

func (c *Cache[K, V]) pruneStack() {
	for c.listS.Len() > 0 {
		bottom := c.listS.Back().Value
		if bottom.status == statusLIR {
			break
		}
		c.listS.Pop()
		bottom.elemS = nil
		if bottom.status == statusNonResidentHIR {
			delete(c.hash, bottom.key)
			var zeroK K
			bottom.key = zeroK
			if c.spare == nil {
				c.spare = bottom
			}
		}
	}
}

func (c *Cache[K, V]) evictOldestGhostFromS() {
	for curr := c.listS.Back(); curr != nil; curr = curr.Prev() {
		e := curr.Value
		if e.status != statusLIR {
			c.listS.Remove(curr, true)
			e.elemS = nil
			if e.status == statusNonResidentHIR {
				delete(c.hash, e.key)
				var zeroK K
				e.key = zeroK
				if c.spare == nil {
					c.spare = e
				}
			}
			return
		}
	}
}

func (c *Cache[K, V]) DeleteKey(key K) bool {
	c.mu.Lock()

	entry, ok := c.hash[key]
	if !ok {
		c.mu.Unlock()
		return false
	}

	if entry.elemQ != nil {
		c.listQ.Remove(entry.elemQ, true)
	}

	wasBottom := false
	if entry.elemS != nil {
		wasBottom = c.listS.Back() == entry.elemS
		c.listS.Remove(entry.elemS, true)
	}

	if entry.status == statusLIR {
		c.lirCount--
	}

	delete(c.hash, key)

	if wasBottom {
		c.pruneStack()
	}

	c.mu.Unlock()
	c.stats.Del()
	return true
}
