package lru

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func TestCache_NewPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("ожидалась паника при capacity <= 0")
		}
	}()
	New[string, int](0)
}

func TestCache_New(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("неожиданная паника %v", r)
		}
	}()
	capacity := 5
	c := New[string, int](capacity)

	if c.cap != capacity {
		t.Errorf("capacity ожидалось: %d; получено %d", capacity, c.cap)
	}
	if c.hash == nil {
		t.Errorf("hash мапа не инициализирована")
	}
	if c.list == nil {
		t.Errorf("список не инициализирован")
	}
}

func TestCache_BasicGetPutUpdate(t *testing.T) {
	c := New[string, int](2)

	// Промах на пустом кэше
	if val, ok := c.Get("a"); ok || val != 0 {
		t.Fatalf("ожидался промах для ключа 'a', получено (%v, %v)", val, ok)
	}

	// Вставка и попадание
	c.Put("a", 10)
	if val, ok := c.Get("a"); !ok || val != 10 {
		t.Fatalf("ожидалось (10, true), получено (%v, %v)", val, ok)
	}

	// Обновление существующего ключа
	c.Put("a", 20)
	if val, ok := c.Get("a"); !ok || val != 20 {
		t.Fatalf("ожидалось обновленное значение (20, true), получено (%v, %v)", val, ok)
	}

	expectedStat := "hit=2; miss=1; del=0; upd=1; evict=0; scans=0; HitRate=0.67; MissRate=0.33; AvgScans=0.00\n"
	if got := c.GetStatistic(); got != expectedStat {
		t.Errorf("неверная статистика: ожидалось: %q; получено:  %q", expectedStat, got)
	}
}

func TestCache_EvictionFIFO_NoHits(t *testing.T) {
	c := New[string, int](3)

	c.Put("k1", 1)
	c.Put("k2", 2)
	c.Put("k3", 3)

	// Вставляем k4: вытесняется k1
	c.Put("k4", 4)

	if _, ok := c.Get("k1"); ok {
		t.Errorf("'k1' должен был быть вытеснен")
	}
	for _, k := range []string{"k2", "k3", "k4"} {
		if _, ok := c.Get(k); !ok {
			t.Errorf("%q должен остаться в кэше", k)
		}
	}

	if c.stats.AvgScans() != 1.0 {
		t.Errorf("ожидался AvgScans = 1.0, получено %v", c.stats.AvgScans())
	}
}

func TestCache_LRU_StrictPromotionOrder(t *testing.T) {
	c := New[string, int](3)

	c.Put("k1", 1)
	c.Put("k2", 2)
	c.Put("k3", 3)
	// [k3, k2, k1]

	if _, ok := c.Get("k1"); !ok {
		t.Fatal("k1 должен быть в кэше")
	}
	// [k1, k3, k2]
	c.Put("k2", 20)
	// [k2, k1, k3]

	// Вставляем k4 вместо k3
	c.Put("k4", 4)
	if _, ok := c.Get("k3"); ok {
		t.Errorf("'k3' должен был быть вытеснен")
	}
	// [k4, k2, k1]

	// Вставляем k5 вместо k1
	c.Put("k5", 5)
	if _, ok := c.Get("k1"); ok {
		t.Errorf("'k1' должен был быть вытеснен вторым")
	}
	// [k5, k4, k2]

	if val, ok := c.Get("k2"); !ok || val != 20 {
		t.Errorf("'k2' должен остаться в кэше со значением 20")
	}
	if val, ok := c.Get("k4"); !ok || val != 4 {
		t.Errorf("'k4' должен остаться в кэше")
	}
	if val, ok := c.Get("k5"); !ok || val != 5 {
		t.Errorf("'k5' должен остаться в кэше")
	}

	if c.stats.AvgScans() != 1.0 {
		t.Errorf("ожидался AvgScans = 1.0, получено %v", c.stats.AvgScans())
	}
}

func TestCache_DeleteKey_Basic(t *testing.T) {
	c := New[string, int](3)

	if ok := c.DeleteKey("missing"); ok {
		t.Errorf("ожидалось false при удалении несуществующего ключа")
	}

	c.Put("a", 1)
	c.Put("b", 2)

	if ok := c.DeleteKey("a"); !ok {
		t.Fatalf("ожидалось true при удалении существующего ключа 'a'")
	}
	if _, ok := c.Get("a"); ok {
		t.Errorf("ключ 'a' не должен находиться после удаления")
	}
	if ok := c.DeleteKey("a"); ok {
		t.Errorf("ожидалось false при повторном удалении ключа 'a'")
	}

	c.Put("c", 3)
	c.Put("d", 4)

	for k, expectedVal := range map[string]int{"b": 2, "c": 3, "d": 4} {
		if val, ok := c.Get(k); !ok || val != expectedVal {
			t.Errorf("для ключа %q ожидалось (%d, true), получено (%d, %v)", k, expectedVal, val, ok)
		}
	}

	expectedStat := "hit=3; miss=1; del=1; upd=0; evict=0; scans=0; HitRate=0.75; MissRate=0.25; AvgScans=0.00\n"
	if got := c.GetStatistic(); got != expectedStat {
		t.Errorf("неверная статистика: ожидалось: %q; получено:  %q", expectedStat, got)
	}
}

func TestCache_DeleteKey_FullCacheEviction(t *testing.T) {
	c := New[string, int](3)

	c.Put("k1", 1)
	c.Put("k2", 2)
	c.Put("k3", 3)

	if !c.DeleteKey("k2") {
		t.Fatal("ожидалось успешное удаление k2")
	}

	// Вставляем k4 на освободившееся место
	c.Put("k4", 4)

	// Вставка k5 с вытеснением k1
	c.Put("k5", 5)

	size := c.Len()
	if size != 3 {
		t.Errorf("ожидался размер кэша 3, получено %d", size)
	}
	if _, ok := c.Get("k1"); ok {
		t.Errorf("ключ k1 должен был быть вытеснен")
	}
	if c.stats.AvgScans() != 1.0 {
		t.Errorf("ожидался AvgScans = 1.0, получено %v", c.stats.AvgScans())
	}
}

func TestCache_Concurrency(t *testing.T) {
	capacity := 100
	c := New[string, int](capacity)
	const goroutines = 32
	const opsPerGoroutine = 2000

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := range goroutines {
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for i := range opsPerGoroutine {
				key := fmt.Sprintf("key-%d", r.Intn(250))
				if r.Intn(2) == 0 {
					c.Put(key, i)
				} else {
					c.Get(key)
				}
				if i%capacity == 0 {
					_ = c.GetStatistic()
				}
			}
		}(g)
	}

	wg.Wait()

	size := c.Len()
	if size > capacity {
		t.Errorf("размер кэша превысил capacity: %d > %d", size, capacity)
	}
}

func TestCache_ConcurrencyWithDelete(t *testing.T) {
	capacity := 50
	c := New[string, int](capacity)
	const goroutines = 32
	const opsPerGoroutine = 2000

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := range goroutines {
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for i := range opsPerGoroutine {
				key := fmt.Sprintf("key-%d", r.Intn(100))
				switch r.Intn(3) {
				case 0:
					c.Put(key, i)
				case 1:
					c.Get(key)
				case 2:
					c.DeleteKey(key)
				}
			}
		}(g)
	}

	wg.Wait()

	size := c.Len()
	if size > capacity {
		t.Errorf("размер кэша превысил capacity: %d > %d", size, capacity)
	}
}
