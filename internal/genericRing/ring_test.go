package genericRing

import "testing"

func checkRing[K comparable, V any](t *testing.T, r *Ring[K, V], expectedKeys []K) {
	t.Helper()
	if r.Len() != len(expectedKeys) {
		t.Fatalf("ожидалась длина %d, получено %d", len(expectedKeys), r.Len())
	}
	if len(expectedKeys) == 0 {
		if r.Head() != nil {
			t.Fatalf("у пустого кольца Head должен быть nil")
		}
		return
	}

	curr := r.Head()
	for i, exp := range expectedKeys {
		if curr.Key != exp {
			t.Errorf("индекс %d: ожидался ключ %v, получен %v", i, exp, curr.Key)
		}
		if curr.Next().Prev() != curr || curr.Prev().Next() != curr {
			t.Errorf("нарушена кольцевая связность на ключе %v", curr.Key)
		}
		curr = curr.Next()
	}
	if curr != r.Head() {
		t.Errorf("круг не замкнулся на Head")
	}
}

func TestRing_BasicOperations(t *testing.T) {
	r := NewRing[int, string]()
	checkRing(t, r, []int{})

	e1 := r.Push(1, "one")
	checkRing(t, r, []int{1})
	if e1.Next() != e1 || e1.Prev() != e1 {
		t.Fatalf("единственный элемент должен ссылаться сам на себя")
	}

	e2 := r.Push(2, "two")
	e3 := r.Push(3, "three")
	checkRing(t, r, []int{1, 2, 3})

	// добавляем в середину [1, 2, 4, 3]
	e4 := NewElem(4, "four")
	r.InsertBefore(e4, e3)
	checkRing(t, r, []int{1, 2, 4, 3})

	// замена Head (e1) на e2: [2, 4, 3]
	r.Remove(e1, true)
	checkRing(t, r, []int{2, 4, 3})
	if e1.Key != 0 || e1.Value != "" {
		t.Errorf("fullClean не очистил поля удалённого элемента")
	}

	// Удаление элементов
	r.Remove(e4, false)
	r.Remove(e3, false)
	r.Remove(e2, true)
	checkRing(t, r, []int{})
}
