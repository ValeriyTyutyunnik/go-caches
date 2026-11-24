package genericList

// Двухсвязный список
// чуть проще по функционалу, чем container/list

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

type List[K comparable, V any] struct {
	front, back *Element[K, V]
	count       int
}

func NewList[K comparable, V any]() *List[K, V] {
	return &List[K, V]{}
}

func (l *List[K, V]) Front() *Element[K, V] {
	return l.front
}

func (l *List[K, V]) Back() *Element[K, V] {
	return l.back
}

func (l *List[K, V]) Len() int {
	return l.count
}

// Перемещает элемент вперед списка
func (l *List[K, V]) ToFront(e *Element[K, V]) {
	if e == nil || l.front == e {
		return
	}

	// если элемент был хвостом - переносим хвост
	if l.back == e {
		l.back = e.prev
	}

	// предыдущий элемент линкуем со следующим (следующим может быть nil)
	if e.prev != nil {
		e.prev.next = e.next
	}

	// следующий элемент линкуем с предыдущим (предыдущим может быть nil)
	if e.next != nil {
		e.next.prev = e.prev
	}

	e.prev = nil
	e.next = l.front

	// у прошлого фронта ставим ссылку на новый элемент
	if l.front != nil {
		l.front.prev = e
	}
	l.front = e

	// если хвоста нет, значит этот элемент и есть хвост
	if l.back == nil {
		l.back = e
	}

}

// Добавляет новый элемент на первое место
func (l *List[K, V]) PushElement(e *Element[K, V]) {
	if e == nil {
		return
	}
	l.count++

	// у прошлого фронта ставим ссылку на новый элемент
	if l.front != nil {
		l.front.prev = e
		e.next = l.front
	}
	l.front = e
	e.prev = nil

	// если хвоста нет, значит этот элемент и есть хвост
	if l.back == nil {
		l.back = e
	}
}

func (l *List[K, V]) Push(key K, value V) *Element[K, V] {
	e := NewElem(key, value)
	l.PushElement(e)
	return e
}

func (l *List[K, V]) Remove(e *Element[K, V], fullClean bool) {
	if e == nil {
		return
	}

	// если элемент был хвостом, меняем хвост
	if l.back == e {
		l.back = e.prev
	}

	// если элемент был фронтом, меняем фронт
	if l.front == e {
		l.front = e.next
	}

	// связываем следующий с предыдущим
	if e.next != nil {
		e.next.prev = e.prev
	}

	// связываем предыдущий с последующим
	if e.prev != nil {
		e.prev.next = e.next
	}

	e.prev = nil
	e.next = nil
	l.count--
	if fullClean {
		var zeroKey K
		var zeroVal V
		e.Key = zeroKey
		e.Value = zeroVal
	}
}

// Удаляет последний элемент и возвращает его с сохраненем ключа и значения
func (l *List[K, V]) Pop() *Element[K, V] {
	e := l.Back()
	l.Remove(e, false)
	return e
}
