package genericRing

// Ring — кольцевой двусвязный список

type Element[K comparable, V any] struct {
	next, prev *Element[K, V]
	Key        K
	Value      V
}

func NewElem[K comparable, V any](key K, val V) *Element[K, V] {
	return &Element[K, V]{Key: key, Value: val}
}

func (e *Element[K, V]) Next() *Element[K, V] {
	return e.next
}

func (e *Element[K, V]) Prev() *Element[K, V] {
	return e.prev
}

// У каждого элемента в непустом кольце Next() и Prev() всегда не nil.
type Ring[K comparable, V any] struct {
	head  *Element[K, V]
	count int
}

func NewRing[K comparable, V any]() *Ring[K, V] {
	return &Ring[K, V]{}
}

func (r *Ring[K, V]) Head() *Element[K, V] {
	return r.head
}

func (r *Ring[K, V]) Len() int {
	return r.count
}

// InsertBefore вставляет отвязанный элемент e в кольцо прямо перед элементом mark.
// Если кольцо пустое или mark == nil, e становится единственным элементом кольца.
func (r *Ring[K, V]) InsertBefore(e, mark *Element[K, V]) {
	if e == nil {
		return
	}
	if r.count == 0 || mark == nil {
		e.next = e
		e.prev = e
		r.head = e
		r.count = 1
		return
	}

	e.prev = mark.prev
	e.next = mark
	mark.prev.next = e
	mark.prev = e
	r.count++
}

// Push добавляет новый элемент перед r.head (то есть в "хвост" кольца, если идти по Next()).
func (r *Ring[K, V]) Push(key K, val V) *Element[K, V] {
	e := NewElem(key, val)
	r.InsertBefore(e, r.head)
	return e
}

// Remove вырезает элемент e из кольца.
// fullClean - устанавлиет Key, Value в zero-vals
func (r *Ring[K, V]) Remove(e *Element[K, V], fullClean bool) {
	if e == nil || r.count == 0 {
		return
	}

	if r.count == 1 {
		r.head = nil
		r.count = 0
	} else {
		if r.head == e {
			r.head = e.next
		}
		e.prev.next = e.next
		e.next.prev = e.prev
		r.count--
	}

	e.prev = nil
	e.next = nil
	if fullClean {
		var zeroKey K
		var zeroVal V
		e.Key = zeroKey
		e.Value = zeroVal
	}
}
