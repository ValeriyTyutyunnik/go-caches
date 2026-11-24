package sieve

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
		t.Errorf("список элементов не инициализирован")
	}

	if c.hand != nil {
		t.Errorf("hand должен быть nil")
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
		t.Errorf("неверная статистика: ожидалось: %q; получено: %q", expectedStat, got)
	}
}

func TestCache_SIEVE_EvictionFIFO_NoHits(t *testing.T) {
	c := New[string, int](3)

	c.Put("k1", 1)
	c.Put("k2", 2)
	c.Put("k3", 3)

	// Вставляем k4: hand увидит visited=false на k1 и вытеснит его
	c.Put("k4", 4)

	if _, ok := c.Get("k1"); ok {
		t.Errorf("'k1' должен был быть вытеснен")
	}
	for _, k := range []string{"k2", "k3", "k4"} {
		if _, ok := c.Get(k); !ok {
			t.Errorf("%q должен остаться в кэше", k)
		}
	}

	// Проверяем, что было 1 вытеснение и 1 шаг сканирования
	if c.stats.AvgScans() != 1.0 {
		t.Errorf("ожидался AvgScans = 1.0, получено %v", c.stats.AvgScans())
	}
}

func TestCache_SIEVE_SecondChance(t *testing.T) {
	c := New[string, int](3)

	c.Put("k1", 1)
	c.Put("k2", 2)
	c.Put("k3", 3)

	// Защищаем от вытеснения k1 и k2 через Get и Put соответственно
	if _, ok := c.Get("k1"); !ok {
		t.Fatal("k1 должен быть в кэше")
	}
	c.Put("k2", 20)

	// Вставляем k4: вытеснение должно проверить k1, k2 и вытеснить k3
	c.Put("k4", 4)

	if _, ok := c.Get("k3"); ok {
		t.Errorf("'k3' должен был быть вытеснен")
	}
	if val, ok := c.Get("k1"); !ok || val != 1 {
		t.Errorf("'k1' должен был выжить")
	}
	if val, ok := c.Get("k2"); !ok || val != 20 {
		t.Errorf("'k2' должен был выжить")
	}
	if val, ok := c.Get("k4"); !ok || val != 4 {
		t.Errorf("'k4' должен быть в кэше")
	}

	// На одно вытеснение должно уйти ровно 3 шага
	if c.stats.AvgScans() != 3.0 {
		t.Errorf("ожидался AvgScans = 3.0, получено %v", c.stats.AvgScans())
	}
}

func TestCache_SIEVE_FullPassAllVisited(t *testing.T) {
	c := New[int, int](3)

	c.Put(1, 10)
	c.Put(2, 20)
	c.Put(3, 30)

	c.Get(1)
	c.Get(2)
	c.Get(3)

	// Вставляем 4-й элемент: hand пройдет все 3 элемента и вытеснит k1 на новом круге
	c.Put(4, 40)

	if _, ok := c.Get(1); ok {
		t.Errorf("k1 должен был быть вытеснен")
	}
	if c.stats.AvgScans() != 4.0 {
		t.Errorf("ожидался AvgScans = 4.0, получено %v", c.stats.AvgScans())
	}
}

func TestCache_SIEVE_HandCheck(t *testing.T) {
	c := New[string, int](4)

	c.Put("k1", 1)
	c.Put("k2", 2)
	c.Put("k3", 3)
	c.Put("k4", 4)
	// [k4, k3, k2, k1]

	// Делаем k1 посещенным (visited)
	c.Get("k1")

	// k1 защищен от вытеснения, k2 вытесняется
	c.Put("k5", 5)
	if _, ok := c.Get("k2"); ok {
		t.Errorf("'k2' должен был быть вытеснен первым")
	}
	// [k5, k4, k3, k1]

	// Вытесняется k3
	c.Put("k6", 6)
	if _, ok := c.Get("k3"); ok {
		t.Errorf("'k3' должен был быть вытеснен вторым")
	}
	// [k6, k5, k4, k1]

	for _, k := range []string{"k1", "k4", "k5", "k6"} {
		if _, ok := c.Get(k); !ok {
			t.Errorf("ключ %q должен остаться в кэше", k)
		}
	}
}

func TestCache_DeleteKey_Basic(t *testing.T) {
	c := New[string, int](3)

	// Удаление несуществующего ключа
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

	// Повторное удаление того же ключа
	if ok := c.DeleteKey("a"); ok {
		t.Errorf("ожидалось false при повторном удалении ключа 'a'")
	}

	// "c" должен занять освободившийся первый слот
	c.Put("c", 3)
	// "d" должен занять свободный последний слот
	c.Put("d", 4)

	for k, expectedVal := range map[string]int{"b": 2, "c": 3, "d": 4} {
		if val, ok := c.Get(k); !ok || val != expectedVal {
			t.Errorf("для ключа %q ожидалось (%d, true), получено (%d, %v)", k, expectedVal, val, ok)
		}
	}

	expectedStat := "hit=3; miss=1; del=1; upd=0; evict=0; scans=0; HitRate=0.75; MissRate=0.25; AvgScans=0.00\n"
	if got := c.GetStatistic(); got != expectedStat {
		t.Errorf("неверная статистика: ожидалось: %q; получено: %q", expectedStat, got)
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

	// Вставляем k4 на место освободившегося k2
	c.Put("k4", 4)

	for k, expected := range map[string]int{"k1": 1, "k4": 4, "k3": 3} {
		if val, ok := c.Get(k); !ok || val != expected {
			t.Errorf("ключ %q потерян: получено (%d, %v)", k, val, ok)
		}
	}

	// Вставка k5 через вытеснение k1
	c.Put("k5", 5)

	size := c.Len()
	if size != 3 {
		t.Errorf("ожидался размер кэша 3, получено %d", size)
	}

	if _, ok := c.Get("k1"); ok {
		t.Errorf("k1 должен был быть вытеснен")
	}
	if c.stats.AvgScans() != 4.0 {
		t.Errorf("ожидался AvgScans = 4.0, получено %v", c.stats.AvgScans())
	}
}

func TestCache_SIEVE_DeleteElementUnderHand(t *testing.T) {
	c := New[string, int](3)

	c.Put("k1", 1)
	c.Put("k2", 2)
	c.Put("k3", 3)
	// [k3, k2, k1]

	c.Put("k4", 4)
	if c.hand == nil || c.hand.Key != "k2" {
		t.Fatalf("ожидалось, что hand указывает на 'k2'")
	}
	// [k4, k3, k2], hand=k2

	if !c.DeleteKey("k2") {
		t.Fatal("ожидалось успешное удаление k2")
	}
	if c.hand == nil || c.hand.Key != "k3" {
		t.Errorf("ожидалось, что после удаления k2 указатель hand сдвинется на 'k3'")
	}
	// [k4, k3], hand=k3

	// Вставка k5 и k6 вытеснит k3
	c.Put("k5", 5)
	c.Put("k6", 6)
	// [k6, k5, k4]

	if _, ok := c.Get("k3"); ok {
		t.Errorf("'k3' должен был быть вытеснен следующим")
	}

	for k, expected := range map[string]int{"k4": 4, "k5": 5, "k6": 6} {
		if val, ok := c.Get(k); !ok || val != expected {
			t.Errorf("ключ %q потерян: получено (%d, %v)", k, val, ok)
		}
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
	if size > 100 {
		t.Errorf("размер кэша превысил capacity: %d > 100", size)
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
