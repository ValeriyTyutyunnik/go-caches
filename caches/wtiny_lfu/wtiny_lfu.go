package wtiny_lfu

// реализация кэша с алгоритмом W-tinyLRU
// Состоит из входного окна Window LRU для новых элементов и основного сегмента SLRU
// Кандидат, вытесненный из Window, сравнивается по частоте с жертвой из Main SLRU с помощью вероятностного счётчика Count-Min Sketch
// Отбрасывается элемент с меньшей частотой

// window = 1%, минимум 1
// mainCap = 99% от оставшейся емкости
// Probationary = max(1, capMain / 5)
// Protected = mainCap - probationaryCap

import (
	"sync"

	"github.com/ValeriyTyutyunnik/go-caches/internal/cms"
	"github.com/ValeriyTyutyunnik/go-caches/internal/genericList"
	"github.com/ValeriyTyutyunnik/go-caches/internal/stats"
)

const (
	segWindow uint8 = iota
	segProbationary
	segProtected
)

type Node[V any] struct {
	val     V
	segment uint8
}

type Cache[K comparable, V any] struct {
	mu         sync.Mutex
	hash       map[K]*genericList.Element[K, Node[V]]
	listWindow *genericList.List[K, Node[V]]
	listProb   *genericList.List[K, Node[V]]
	listProt   *genericList.List[K, Node[V]]
	stats      stats.Stats
	sketch     *cms.CountMinSketch[K]
	spare      *genericList.Element[K, Node[V]]
	capInit    int
	capWindow  int
	capProb    int
	capProt    int
}

func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity <= 0 {
		panic("емкость должна быть > 0")
	}

	capWindow := max(1, capacity/100)
	capMain := capacity - capWindow

	var capProb, capProt int
	if capMain > 0 {
		capProb = max(1, capMain/5)
		capProt = capMain - capProb
	}

	res := &Cache[K, V]{
		capInit:    capacity,
		capWindow:  capWindow,
		capProb:    capProb,
		capProt:    capProt,
		hash:       make(map[K]*genericList.Element[K, Node[V]], capWindow+capMain),
		listWindow: genericList.NewList[K, Node[V]](),
		listProb:   genericList.NewList[K, Node[V]](),
		listProt:   genericList.NewList[K, Node[V]](),
		sketch:     cms.NewSketch[K](capacity),
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

	// инкрементим ключ по CMS
	c.sketch.Increment(key)

	elem, ok := c.hash[key]
	if !ok {
		c.mu.Unlock()
		c.stats.Miss()
		var zero V
		return zero, false
	}
	c.promoteElement(elem)
	val := elem.Value.val

	c.mu.Unlock()
	c.stats.Hit()
	return val, true
}

func (c *Cache[K, V]) Put(key K, value V) {
	c.mu.Lock()

	c.sketch.Increment(key)

	// Если ключ уже есть в кэше — обновляем значение и продвигаем
	if elem, ok := c.hash[key]; ok {
		c.promoteElement(elem)
		elem.Value.val = value
		c.mu.Unlock()
		c.stats.Upd()
		return
	}

	// В окне ещё есть место — создаём новый узел
	if c.listWindow.Len() < c.capWindow {
		elem := c.acquireElem(key, value, segWindow)
		c.listWindow.PushElement(elem)
		c.hash[key] = elem
		c.mu.Unlock()
		return
	}

	// Окно заполнено: сохраняем данные кандидата, а его узел сразу переиспользуем под новый ключ
	candidate := c.listWindow.Pop()
	candKey, candVal := candidate.Key, candidate.Value.val
	delete(c.hash, candKey)

	candidate.Key = key
	candidate.Value.val = value
	c.listWindow.PushElement(candidate)
	c.hash[key] = candidate

	// Если Main-сегмента нет (capacity == 1), кандидат окончательно вытеснен
	if c.capProb+c.capProt == 0 {
		c.mu.Unlock()
		c.stats.Evict()
		c.stats.AddScans(1)
		return
	}

	// Если в Main ещё есть свободные слоты — кандидат проходит без дуэли
	if c.listProb.Len()+c.listProt.Len() < c.capProb+c.capProt {
		c.admitNewToMain(candKey, candVal)
		c.mu.Unlock()
		return
	}

	// Main полон — проводим дуэль по частоте
	c.admitOrEvict(candKey, candVal)
	c.mu.Unlock()
	c.stats.Evict()
	c.stats.AddScans(1)
}

func (c *Cache[K, V]) acquireElem(key K, val V, seg uint8) *genericList.Element[K, Node[V]] {
	if c.spare != nil {
		elem := c.spare
		c.spare = nil
		elem.Key = key
		elem.Value = Node[V]{val: val, segment: seg}
		return elem
	}
	return genericList.NewElem(key, Node[V]{val: val, segment: seg})
}

// помещает новый элемент в главные очереди при наличии свободных слотов
func (c *Cache[K, V]) admitNewToMain(key K, val V) {
	// Новый элемент из Window ВСЕГДА должен начинать путь в Probationary.
	// Если Probationary полон, но в Protected есть место — освобождаем слот в Probationary,
	// переведя самый старый элемент из Probationary в Protected.
	if c.listProb.Len() >= c.capProb {
		promoted := c.listProb.Pop()
		promoted.Value.segment = segProtected
		c.listProt.PushElement(promoted)
	}
	elem := c.acquireElem(key, val, segProbationary)
	c.listProb.PushElement(elem)
	c.hash[key] = elem
}

func (c *Cache[K, V]) admitOrEvict(candKey K, candVal V) {
	victim := c.listProb.Back()
	if victim == nil {
		victim = c.listProt.Back()
	}

	if c.sketch.Estimate(candKey) > c.sketch.Estimate(victim.Key) {
		delete(c.hash, victim.Key)
		if victim.Value.segment == segProbationary {
			c.listProb.Remove(victim, false)
		} else {
			c.listProt.Remove(victim, false)
		}

		// Переиспользуем узел проигравшей жертвы под победившего кандидата
		victim.Key = candKey
		victim.Value = Node[V]{val: candVal, segment: segProbationary}
		c.listProb.PushElement(victim)
		c.hash[candKey] = victim
	}
}

// обновляет позицию элемента в зависимости от его текущего сегмента
func (c *Cache[K, V]) promoteElement(elem *genericList.Element[K, Node[V]]) {
	switch elem.Value.segment {
	case segProtected:
		c.listProt.ToFront(elem)
	case segWindow:
		c.listWindow.ToFront(elem)
	case segProbationary:
		if c.capProt == 0 {
			c.listProb.ToFront(elem)
			return
		}
		c.listProb.Remove(elem, false)
		if c.listProt.Len() >= c.capProt {
			demoted := c.listProt.Pop()
			demoted.Value.segment = segProbationary
			c.listProb.PushElement(demoted)
		}
		elem.Value.segment = segProtected
		c.listProt.PushElement(elem)
	}
}

func (c *Cache[K, V]) DeleteKey(key K) bool {
	c.mu.Lock()

	elem, ok := c.hash[key]
	if !ok {
		c.mu.Unlock()
		return false
	}

	switch elem.Value.segment {
	case segWindow:
		c.listWindow.Remove(elem, true)
	case segProbationary:
		c.listProb.Remove(elem, true)
	case segProtected:
		c.listProt.Remove(elem, true)
	}
	if c.spare == nil {
		c.spare = elem
	}

	delete(c.hash, key)

	c.mu.Unlock()
	c.stats.Del()
	return true
}
