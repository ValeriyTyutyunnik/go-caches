package genericList

import (
	"testing"
)

// Проверка целостности связей списка
func checkList[K comparable, V any](t *testing.T, l *List[K, V], expectedKeys []K) {
	t.Helper()

	if len(expectedKeys) == 0 {
		if l.Front() != nil || l.Back() != nil {
			t.Fatalf("ожидался пустой список, получено: front=%v, back=%v", l.Front(), l.Back())
		}
		return
	}

	if l.Front() == nil || l.Back() == nil {
		t.Fatalf("список не должен быть пустым: front=%v, back=%v", l.Front(), l.Back())
	}

	if l.Front().prev != nil {
		t.Errorf("у первого элемента prev должен быть nil, получено %v", l.Front().prev)
	}
	if l.Back().next != nil {
		t.Errorf("у последнего элемента next должен быть nil, получено %v", l.Back().next)
	}

	// проход от front к back
	curr := l.Front()
	for i, expKey := range expectedKeys {
		if curr == nil {
			t.Fatalf("список неожиданно закончился на индексе %d", i)
		}
		if curr.Key != expKey {
			t.Errorf("индекс %d: ожидался ключ %v, получен %v", i, expKey, curr.Key)
		}
		if curr.next != nil && curr.next.prev != curr {
			t.Errorf("нарушена обратная ссылка у элемента с ключом %v", curr.next.Key)
		}
		if i == len(expectedKeys)-1 && curr != l.Back() {
			t.Errorf("последний элемент при проходе не совпадает с l.Back()")
		}
		curr = curr.next
	}
	if curr != nil {
		t.Errorf("в списке остались лишние элементы после ожидаемого конца")
	}
}

func TestNewAndEmptyList(t *testing.T) {
	l := NewList[int, int]()

	checkList(t, l, []int{})

	// При работе с пустым списком не нужно паниковать
	l.Pop()
	l.Remove(nil, false)
	l.ToFront(nil)

	checkList(t, l, []int{})
}

func TestPushFrontBack(t *testing.T) {
	l := NewList[int, string]()

	e1 := l.Push(1, "one")
	if e1.Key != 1 || e1.Value != "one" {
		t.Errorf("неверные данные элемента: %+v", e1)
	}
	checkList(t, l, []int{1})
	if l.Front() != e1 || l.Back() != e1 {
		t.Errorf("Front и Back должны указывать на единственный элемент")
	}

	e2 := l.Push(2, "two")
	e3 := l.Push(3, "three")

	checkList(t, l, []int{3, 2, 1})
	if l.Front() != e3 {
		t.Errorf("ожидался Front = e3, получен %v", l.Front())
	}
	if l.Back() != e1 {
		t.Errorf("ожидался Back = e1, получен %v", l.Back())
	}
	_ = e2
}

func TestToFront(t *testing.T) {
	l := NewList[int, string]()
	e1 := l.Push(1, "one")
	e2 := l.Push(2, "two")
	e3 := l.Push(3, "three")

	// 1. Перемещаем элемент, который уже первый (ничего не должно измениться)
	l.ToFront(e3)
	checkList(t, l, []int{3, 2, 1})

	// 2. Перемещаем элемент из середины
	l.ToFront(e2)
	checkList(t, l, []int{2, 3, 1})

	// 3. Перемещаем последний элемент (должен обновиться Back)
	l.ToFront(e1)
	checkList(t, l, []int{1, 2, 3})
	if l.Back() != e3 {
		t.Errorf("ожидался новый Back = e3, получен %v", l.Back())
	}
}

func TestRemove(t *testing.T) {
	l := NewList[string, string]()
	e1 := l.Push("1", "one")
	e2 := l.Push("2", "two")
	e3 := l.Push("3", "three")
	e4 := l.Push("4", "four")

	// 1. Удаляем из середины
	l.Remove(e3, true)
	checkList(t, l, []string{"4", "2", "1"})
	if e3.Key == "3" {
		t.Errorf("не ожидалось сохранения Key в e3, получено %v", e3.Key)
	}
	if e3.Value == "three" {
		t.Errorf("не ожидалось сохранения Value в e3, получено %v", e3.Value)
	}

	// 2. Удаляем первый элемент
	l.Remove(e4, true)
	checkList(t, l, []string{"2", "1"})

	// 3. Удаляем последний элемент с сохранением полей в элементе
	l.Remove(e1, false)
	checkList(t, l, []string{"2"})
	if e1.Key != "1" {
		t.Errorf("ожидалось сохранения Key в e1, получено %v", e1.Key)
	}
	if e1.Value != "one" {
		t.Errorf("ожидалось сохранения Value в e1, получено %v", e1.Value)
	}

	// 4. Удаляем единственный оставшийся элемент
	l.Remove(e2, true)
	checkList(t, l, []string{})
}

func TestPop(t *testing.T) {
	l := NewList[int, string]()
	l.Push(1, "one")
	l.Push(2, "two")
	l.Push(3, "three")
	// Текущий порядок: 3 -> 2 -> 1

	l.Pop()
	checkList(t, l, []int{3, 2})

	l.Pop()
	checkList(t, l, []int{3})

	l.Pop()
	checkList(t, l, []int{})

	// Повторный Pop на пустом списке
	l.Pop()
	checkList(t, l, []int{})
}
