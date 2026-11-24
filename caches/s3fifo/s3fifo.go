package s3fifo

// реализация кэша с алгоритмом S3_FIFO Simple, Scalable, Space-efficient FIFO — SOSP 2023
// Кэш строится на трёх FIFO-очередях (Small - 10% capacity, Main 90% capacity, Ghost 90% capacity)
// и 2-битных счётчиках обращений (0..3) без перестановок узлов при чтении
// В Ghost значения не хранятся
// при первоначальном заполнении очередь small заполняется полностью на 100% capacity, и только после начнет вытеснять элементы в Main

import (
	"sync"

	"github.com/ValeriyTyutyunnik/go-caches/internal/genericList"
	"github.com/ValeriyTyutyunnik/go-caches/internal/stats"
)

const (
	listSmall uint8 = iota // Small queue
	listMain               // Main queue
	listGhost              // Ghost queue
)

type Node[V any] struct {
	val     V
	freq    uint8
	address uint8
}

type Cache[K comparable, V any] struct {
	mu                          sync.Mutex
	hash                        map[K]*genericList.Element[K, Node[V]]
	listS                       *genericList.List[K, Node[V]]
	listM                       *genericList.List[K, Node[V]]
	listG                       *genericList.List[K, Node[V]]
	spare                       *genericList.Element[K, Node[V]] // элемент для переиспользования без лишней аллокации
	stats                       stats.Stats
	pendingScans, pendingEvicts uint64
	capS, capM                  int
}

func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity <= 0 {
		panic("емкость должна быть > 0")
	}
	capS := max(1, capacity/10)
	capM := capacity - capS
	c := &Cache[K, V]{
		capS:  capS,
		capM:  capM,
		hash:  make(map[K]*genericList.Element[K, Node[V]], capacity+capM),
		listS: genericList.NewList[K, Node[V]](),
		listM: genericList.NewList[K, Node[V]](),
		listG: genericList.NewList[K, Node[V]](),
	}
	return c
}

func (c *Cache[K, V]) GetStatistic() string { return c.stats.String() } // atomic operation
func (c *Cache[K, V]) Stats() stats.Reader  { return &c.stats }

func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	res := c.listS.Len() + c.listM.Len()
	c.mu.Unlock()
	return res
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()

	elem, ok := c.hash[key]
	if !ok || elem.Value.address == listGhost {
		c.mu.Unlock()
		c.stats.Miss()
		var zero V
		return zero, false
	}

	if elem.Value.freq < 3 {
		elem.Value.freq++ // инкремент счетчика максимум до 3
	}
	val := elem.Value.val

	c.mu.Unlock()
	c.stats.Hit()
	return val, true
}

func (c *Cache[K, V]) Put(key K, val V) {
	c.mu.Lock()

	elem, ok := c.hash[key]
	// есть живой элемент
	if ok && (elem.Value.address == listSmall || elem.Value.address == listMain) {
		elem.Value.val = val
		if elem.Value.freq < 3 {
			elem.Value.freq++
		}
		c.mu.Unlock()
		c.stats.Upd()
		return
	}

	// попали в призрак, воскрешаем в Main
	if ok && elem.Value.address == listGhost {
		c.listG.Remove(elem, false)
		if evicted := c.evictOne(); evicted != nil {
			var zeroK K
			var zeroV V
			evicted.Key = zeroK
			evicted.Value.val = zeroV
			if c.spare == nil {
				c.spare = evicted // сохраняем для переиспользования
			}
		}
		elem.Value = Node[V]{val: val, address: listMain, freq: 0}
		c.listM.PushElement(elem)
		scans, evicts := c.pendingScans, c.pendingEvicts
		c.pendingScans, c.pendingEvicts = 0, 0
		c.mu.Unlock()
		c.addPendingStats(scans, evicts)
		return
	}

	// добавляем совсем новый элемент в Small
	reused := c.evictOne()
	if reused == nil && c.spare != nil {
		reused = c.spare
		c.spare = nil
	}

	if reused == nil {
		reused = genericList.NewElem(key, Node[V]{val: val, address: listSmall, freq: 0})
	} else {
		reused.Value = Node[V]{val: val, address: listSmall, freq: 0}
		reused.Key = key
	}
	c.listS.PushElement(reused)
	c.hash[key] = reused
	scans, evicts := c.pendingScans, c.pendingEvicts
	c.pendingScans, c.pendingEvicts = 0, 0
	c.mu.Unlock()
	c.addPendingStats(scans, evicts)
}

func (c *Cache[K, V]) addPendingStats(scans, evicts uint64) {
	for range evicts {
		c.stats.Evict()
	}
	if scans > 0 {
		c.stats.AddScans(scans)
	}
}

func (c *Cache[K, V]) evictGhost() *genericList.Element[K, Node[V]] {
	if c.listG.Len() < c.capM {
		return nil
	}
	evicted := c.listG.Back()
	c.listG.Remove(evicted, false)
	delete(c.hash, evicted.Key)
	return evicted
}

func (c *Cache[K, V]) evictMain() *genericList.Element[K, Node[V]] {
	var steps uint64
	for {
		steps++
		elem := c.listM.Back()
		if elem.Value.freq == 0 {
			c.listM.Remove(elem, false)
			delete(c.hash, elem.Key)
			c.pendingEvicts++
			c.pendingScans += steps
			return elem
		}
		elem.Value.freq--
		c.listM.ToFront(elem)
	}
}

// гарантированно вытеснит 1 элемент если кэш заполнен
func (c *Cache[K, V]) evictOne() *genericList.Element[K, Node[V]] {
	if c.listS.Len()+c.listM.Len() < c.capS+c.capM {
		return nil
	}
	// если кэш без Main
	if c.capM == 0 {
		elem := c.listS.Back()
		c.listS.Remove(elem, false)
		delete(c.hash, elem.Key)
		c.pendingEvicts++
		c.pendingScans++
		return elem
	}

	for {
		// Если Small достиг или превысил целевой порог capS или Main пуст, вытесняем из Small
		if c.listS.Len() >= c.capS || c.listM.Len() == 0 {
			elem := c.listS.Back()
			c.listS.Remove(elem, false)
			c.pendingScans++

			// элемент не запрашивался - вытесняем в призраки
			if elem.Value.freq == 0 {
				evicted := c.evictGhost()
				elem.Value.address = listGhost
				var zero V
				elem.Value.val = zero
				c.listG.PushElement(elem)
				c.pendingEvicts++
				return evicted
			}

			// Переносим в Main
			elem.Value.address = listMain
			elem.Value.freq = 0
			c.listM.PushElement(elem)
			continue // цикл на следующей итерации гарантированно вытеснит 1 элемент
		}

		// Иначе Small меньше capS, а в Main что-то есть - вытесняем из Main
		return c.evictMain()
	}
}

func (c *Cache[K, V]) DeleteKey(key K) bool {
	c.mu.Lock()

	if elem, ok := c.hash[key]; ok {
		switch elem.Value.address {
		case listSmall:
			c.listS.Remove(elem, true)
		case listMain:
			c.listM.Remove(elem, true)
		case listGhost:
			c.listG.Remove(elem, true)
		}
		delete(c.hash, key)

		c.mu.Unlock()
		c.stats.Del()
		return true
	}

	c.mu.Unlock()
	return false
}
