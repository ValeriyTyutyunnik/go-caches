package cms

import (
	"hash/maphash"
	"math/bits"
)

const (
	cmsDepth      = 4
	maxCounterVal = 15
	// Маска для одновременного побитового сдвига (деления на 2) шестнадцати 4-битных счётчиков внутри одного uint64
	resetMask uint64 = 0x7777777777777777
)

type CountMinSketch[K comparable] struct {
	seed       maphash.Seed
	table      [cmsDepth][]uint64
	widthMask  uint32
	additions  int
	sampleSize int
}

func NewSketch[K comparable](capacity int) *CountMinSketch[K] {
	if capacity <= 0 {
		panic("емкость должна быть > 0")
	}

	// Выбираем ширину как ближайшую степень двойки >= capacity (минимум 16, чтобы уместиться в один uint64)
	width := nextPowerOfTwo(max(16, capacity))
	wordsPerRow := width / 16

	cms := &CountMinSketch[K]{
		seed:       maphash.MakeSeed(),
		widthMask:  uint32(width - 1),
		sampleSize: capacity * 10,
	}

	for i := range cmsDepth {
		cms.table[i] = make([]uint64, wordsPerRow)
	}

	return cms
}

// Increment увеличивает частоту ключа на 1 (максимум до 15) и запускает старение при достижении sampleSize
func (c *CountMinSketch[K]) Increment(key K) {
	h := maphash.Comparable(c.seed, key)
	h1 := uint32(h)
	h2 := uint32(h >> 32)

	for i := range cmsDepth {
		idx := (h1 + uint32(i)*h2) & c.widthMask
		c.incrementAt(i, idx)
	}

	c.additions++
	if c.additions >= c.sampleSize {
		c.Reset()
	}
}

// Estimate возвращает приблизительную частоту обращений к ключу (минимум по 4 строкам, от 0 до 15).
func (c *CountMinSketch[K]) Estimate(key K) int {
	h := maphash.Comparable(c.seed, key)
	h1 := uint32(h)
	h2 := uint32(h >> 32)

	minCount := maxCounterVal
	for i := range cmsDepth {
		idx := (h1 + uint32(i)*h2) & c.widthMask
		count := c.getAt(i, idx)
		if count == 0 {
			return 0 // ниже не будет
		}
		if count < minCount {
			minCount = count
		}
	}

	return minCount
}

// Reset делит все 4-битные счётчики в матрице пополам (старение).
func (c *CountMinSketch[K]) Reset() {
	for i := 0; i < cmsDepth; i++ {
		row := c.table[i]
		for j := range row {
			row[j] = (row[j] >> 1) & resetMask
		}
	}
	c.additions /= 2
}

// Clear полностью обнуляет счётчики
func (c *CountMinSketch[K]) Clear() {
	for i := 0; i < cmsDepth; i++ {
		clear(c.table[i])
	}
	c.additions = 0
}

func (c *CountMinSketch[K]) incrementAt(row int, idx uint32) {
	wordIdx := idx >> 4          // idx / 16
	bitOffset := (idx & 15) << 2 // (idx % 16) * 4

	word := c.table[row][wordIdx]
	val := (word >> bitOffset) & maxCounterVal
	if val < maxCounterVal {
		// Прибавляем 1 к нужному 4-битному слоту без затрагивания соседних слотов
		c.table[row][wordIdx] = word + (1 << bitOffset)
	}
}

func (c *CountMinSketch[K]) getAt(row int, idx uint32) int {
	wordIdx := idx >> 4
	bitOffset := (idx & 15) << 2
	return int((c.table[row][wordIdx] >> bitOffset) & maxCounterVal)
}

func nextPowerOfTwo(n int) int {
	if n <= 1 {
		return 1
	}
	return 1 << bits.Len(uint(n-1))
}
