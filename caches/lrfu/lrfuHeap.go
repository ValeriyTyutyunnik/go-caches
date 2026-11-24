package lrfu

type entry[K comparable, V any] struct {
	key       K
	val       V
	logWeight float64 // log2(weight) + lambda * lastTime
	lastTime  uint64
	index     int
}

type lrfuHeap[K comparable, V any] struct {
	items   []*entry[K, V]
	now     uint64
	lambda  float64
	scanned uint64
}

func (h *lrfuHeap[K, V]) less(i, j int) bool {
	h.scanned++
	ei, ej := h.items[i], h.items[j]
	if ei.logWeight == ej.logWeight {
		return ei.lastTime < ej.lastTime
	}
	return ei.logWeight < ej.logWeight
}

func (h *lrfuHeap[K, V]) up(j int) {
	for {
		i := (j - 1) / 2 // родитель
		if i == j || !h.less(j, i) {
			break
		}
		h.items[i], h.items[j] = h.items[j], h.items[i]
		h.items[i].index = i
		h.items[j].index = j
		j = i
	}
}

func (h *lrfuHeap[K, V]) down(i0, n int) bool {
	i := i0
	for {
		j1 := 2*i + 1
		if j1 >= n || j1 < 0 { // j1 < 0 при переполнении int
			break
		}
		j := j1 // левый потомок
		if j2 := j1 + 1; j2 < n && h.less(j2, j1) {
			j = j2 // правый потомок меньше
		}
		if !h.less(j, i) {
			break
		}
		h.items[i], h.items[j] = h.items[j], h.items[i]
		h.items[i].index = i
		h.items[j].index = j
		i = j
	}
	return i > i0
}

func (h *lrfuHeap[K, V]) fix(i int) {
	if !h.down(i, len(h.items)) {
		h.up(i)
	}
}

func (h *lrfuHeap[K, V]) push(e *entry[K, V]) {
	e.index = len(h.items)
	h.items = append(h.items, e)
	h.up(len(h.items) - 1)
}

func (h *lrfuHeap[K, V]) remove(i int) *entry[K, V] {
	n := len(h.items) - 1
	e := h.items[i]
	if n != i {
		h.items[i] = h.items[n]
		h.items[i].index = i
		h.items[n] = nil
		h.items = h.items[:n]
		h.fix(i)
	} else {
		h.items[n] = nil
		h.items = h.items[:n]
	}
	e.index = -1
	return e
}

// заменяет минимальный элемент новым
func (h *lrfuHeap[K, V]) replaceRoot(newRoot *entry[K, V]) {
	newRoot.index = 0
	h.items[0] = newRoot
	h.down(0, len(h.items))
}

// renormalize сдвигает время и logWeight вниз, предотвращая потерю точности float64
func (h *lrfuHeap[K, V]) renormalize() {
	if len(h.items) == 0 {
		h.now = 0
		return
	}
	shiftLog := h.lambda * float64(h.now)
	minTime := h.items[0].lastTime
	for _, e := range h.items[1:] {
		if e.lastTime < minTime {
			minTime = e.lastTime
		}
	}
	for _, e := range h.items {
		e.logWeight -= shiftLog
		e.lastTime -= minTime
	}
	h.now -= minTime
}
