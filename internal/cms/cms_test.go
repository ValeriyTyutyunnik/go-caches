package cms

import (
	"testing"
)

func TestCMS_NewPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("ожидалась паника при capacity <= 0")
		}
	}()
	NewSketch[string](0)
}

func TestCMS_BasicIncrementAndEstimate(t *testing.T) {
	cms := NewSketch[string](64)

	// Незнакомый ключ должен иметь оценку 0
	if got := cms.Estimate("unknown"); got != 0 {
		t.Fatalf("ожидалось 0 для нового ключа, получено %d", got)
	}

	// Увеличиваем частоту "apple" 5 раз
	for i := 1; i <= 5; i++ {
		cms.Increment("apple")
		if got := cms.Estimate("apple"); got != i {
			t.Errorf("шаг %d: ожидалось %d, получено %d", i, i, got)
		}
	}

	// Соседние ключи не должны пострадать (нет переполнения в соседние 4-битные слоты)
	if got := cms.Estimate("banana"); got != 0 {
		t.Errorf("ожидалось 0 для 'banana', получено %d", got)
	}
}

func TestCMS_SaturationAt15(t *testing.T) {
	// Берём большую capacity, чтобы не сработал автоматический Reset во время 30 инкрементов
	cms := NewSketch[string](100)

	for range 30 {
		cms.Increment("hot_key")
	}

	// Потолок 4-битного счётчика — строго 15
	if got := cms.Estimate("hot_key"); got != 15 {
		t.Errorf("ожидалось насыщение на 15, получено %d", got)
	}
}

func TestCMS_AgingReset(t *testing.T) {
	// capacity = 16 -> sampleSize = 160
	cms := NewSketch[string](16)

	// Набиваем ключу "star" максимальную частоту 15
	for range 15 {
		cms.Increment("star")
	}
	if got := cms.Estimate("star"); got != 15 {
		t.Fatalf("ожидалось 15 перед сбросом, получено %d", got)
	}

	// Добиваем счетчик additions до sampleSize = 160 одним ключом, не пересекающимся со "star"
	// Проверяем автоматический вызов Reset при достижении порога sampleSize:
	for cms.additions < cms.sampleSize-1 {
		cms.Increment("filler")
	}
	// Последний инкремент ровно на границе sampleSize (160) должен триггернуть Reset() -> additions станет 80
	cms.Increment("filler")
	if cms.additions != cms.sampleSize/2 {
		t.Errorf("после автоматического Reset additions ожидался %d, получено %d", cms.sampleSize/2, cms.additions)
	}

	// Проверяем точную математику битового сдвига (15 -> 7 -> 3 -> 1 -> 0) на чистом Reset()
	cms.Clear()
	for range 15 {
		cms.Increment("star")
	}

	expectedSequence := []int{7, 3, 1, 0}
	for step, expected := range expectedSequence {
		cms.Reset()
		if got := cms.Estimate("star"); got != expected {
			t.Errorf("шаг Reset #%d: ожидалось %d, получено %d", step+1, expected, got)
		}
	}
}

func TestCMS_Clear(t *testing.T) {
	cms := NewSketch[int](32)
	cms.Increment(42)
	cms.Increment(42)

	cms.Clear()

	if got := cms.Estimate(42); got != 0 {
		t.Errorf("после Clear() ожидалось 0, получено %d", got)
	}
	if cms.additions != 0 {
		t.Errorf("после Clear() additions должен быть 0, получено %d", cms.additions)
	}
}

func BenchmarkCMS_IncrementAndEstimate(b *testing.B) {
	cms := NewSketch[int](10000)
	b.ReportAllocs()

	for i := 0; b.Loop(); i++ {
		key := i & 16383
		cms.Increment(key)
		_ = cms.Estimate(key)
	}
}
