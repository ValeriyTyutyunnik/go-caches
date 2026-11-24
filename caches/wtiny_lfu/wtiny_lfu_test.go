package wtiny_lfu

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

func TestCache_BasicGetPutUpdate(t *testing.T) {
	c := New[string, int](10) // capWindow = 1, capMain = 9 (capProb = 1, capProt = 8)

	if val, ok := c.Get("a"); ok || val != 0 {
		t.Fatalf("ожидался промах для ключа 'a', получено (%v, %v)", val, ok)
	}

	c.Put("a", 10)
	if val, ok := c.Get("a"); !ok || val != 10 {
		t.Fatalf("ожидалось (10, true), получено (%v, %v)", val, ok)
	}

	c.Put("a", 20)
	if val, ok := c.Get("a"); !ok || val != 20 {
		t.Fatalf("ожидалось обновленное значение (20, true), получено (%v, %v)", val, ok)
	}

	expectedStat := "hit=2; miss=1; del=0; upd=1; evict=0; scans=0; HitRate=0.67; MissRate=0.33; AvgScans=0.00\n"
	if got := c.GetStatistic(); got != expectedStat {
		t.Errorf("неверная статистика: ожидалось: %q; получено: %q", expectedStat, got)
	}
}

// Проверка, что одноразовые ключи не вытесняют горячий ключ из основной очереди
func TestCache_WTinyLFU_AdmissionRejects(t *testing.T) {
	// capacity = 3 -> capWindow = 1, capMain = 2 (capProb = 1, capProt = 1)
	c := New[string, int](3)

	// Прогреваем "hot"
	for range 4 {
		c.Get("hot")
	}
	c.Put("hot", 100)

	// Проталкиваем "hot" из Window в Main через вставку "warm"
	c.Get("warm")
	c.Get("warm")
	c.Put("warm", 200)

	// Проталкиваем "warm" в Main через вставку "filler"
	// Window: [filler (freq=1)], Main: [warm (freq=3), hot (freq=5)]
	c.Put("filler", 300)

	if c.Len() != 3 {
		t.Fatalf("ожидался полный кэш размером 3, получено %d", c.Len())
	}

	// запускаем волну одноразовых ключей
	// Каждый новый ключ должен проиграть в дуэли с жертвой из главных очередей
	for i := range 20 {
		c.Put(fmt.Sprintf("one-hit-%d", i), i)
	}

	if val, ok := c.Get("hot"); !ok || val != 100 {
		t.Errorf("горячий ключ 'hot' не должен был быть вытеснен одноразовым трафиком")
	}
	if val, ok := c.Get("warm"); !ok || val != 200 {
		t.Errorf("теплый ключ 'warm' не должен был быть вытеснен одноразовым трафиком")
	}
}

// Проверка победы горячего новичка в дуэли
func TestCache_WTinyLFU_AdmissionAcceptsHotCandidate(t *testing.T) {
	// capacity = 3 -> capWindow = 1, capMain = 2 (capProb = 1, capProt = 1)
	c := New[string, int](3)

	// одноразовые ключи
	c.Put("k1", 1)
	c.Put("k2", 2)
	c.Put("k3", 3)

	// греем кандидата
	for range 4 {
		c.Get("rising_star")
	}
	c.Put("rising_star", 500)
	// Выталкиваем "rising_star" из Window в дуэль против жертвы из Main (freq=1)
	c.Put("pusher", 600) // толкаем кандидата на дуэль

	if val, ok := c.Get("rising_star"); !ok || val != 500 {
		t.Errorf("'rising_star' с высокой частотой должен был выиграть")
	}

}

func TestCache_DeleteKey(t *testing.T) {
	c := New[string, int](10)

	if c.DeleteKey("missing") {
		t.Errorf("ожидалось false при удалении несуществующего ключа")
	}

	c.Put("k1", 1)
	c.Put("k2", 2) // вытолкнет k1 в Main
	c.Get("k1")    // переведет k1 в Protected

	if !c.DeleteKey("k1") || c.DeleteKey("k1") {
		t.Errorf("ошибка при удалении k1 из Protected")
	}
	if !c.DeleteKey("k2") || c.DeleteKey("k2") {
		t.Errorf("ошибка при удалении k2 из Window")
	}
	if c.Len() != 0 {
		t.Errorf("ожидался пустой кэш, получено Len=%d", c.Len())
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
				switch r.Intn(3) {
				case 0:
					c.Put(key, i)
				case 1:
					c.Get(key)
				case 2:
					c.DeleteKey(key)
				}
				if i%capacity == 0 {
					_ = c.GetStatistic()
				}
			}
		}(g)
	}

	wg.Wait()

	if c.Len() > capacity {
		t.Errorf("размер кэша превысил capacity: %d > %d", c.Len(), capacity)
	}
}
