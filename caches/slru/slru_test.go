package slru

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
	capacity := 10
	c := New[string, int](capacity)

	if c.capInit != capacity {
		t.Errorf("capInit ожидалось: %d; получено %d", capacity, c.capInit)
	}
	if c.capIn != 2 || c.capMain != 8 {
		t.Errorf("неверное распределение емкостей: in=%d, main=%d", c.capIn, c.capMain)
	}
	if c.hash == nil || c.listMain == nil || c.listIn == nil {
		t.Errorf("структуры данных кэша не инициализированы")
	}
}

func TestCache_BasicGetPutUpdate(t *testing.T) {
	c := New[string, int](5) // capIn = 1, capMain = 4

	if val, ok := c.Get("a"); ok || val != 0 {
		t.Fatalf("ожидался промах для ключа 'a', получено (%v, %v)", val, ok)
	}

	// Вставка в listIn
	c.Put("a", 10)
	// Первый Get переводит 'a' из listIn в listMain
	if val, ok := c.Get("a"); !ok || val != 10 {
		t.Fatalf("ожидалось (10, true), получено (%v, %v)", val, ok)
	}

	// Обновление в listMain
	c.Put("a", 20)
	if val, ok := c.Get("a"); !ok || val != 20 {
		t.Fatalf("ожидалось обновленное значение (20, true), получено (%v, %v)", val, ok)
	}

	expectedStat := "hit=2; miss=1; del=0; upd=1; evict=0; scans=0; HitRate=0.67; MissRate=0.33; AvgScans=0.00\n"
	if got := c.GetStatistic(); got != expectedStat {
		t.Errorf("неверная статистика: ожидалось: %q; получено: %q", expectedStat, got)
	}
}

func TestCache_SLRU_PromotionAndDemotion(t *testing.T) {
	// capacity = 5 -> capIn = 1, capMain = 4
	c := New[string, int](5)

	for _, k := range []string{"m1", "m2", "m3", "m4"} {
		c.Put(k, 1) // попадает в listIn
		c.Get(k)    // переходит в listMain (listIn снова пуст)
	}
	// listMain: [m4, m3, m2, m1]

	if c.listMain.Len() != 4 || c.listIn.Len() != 0 {
		t.Fatalf("ожидалось Main=4, In=0; получено Main=%d, In=%d", c.listMain.Len(), c.listIn.Len())
	}

	c.Put("p1", 100)

	// Обращаемся к 'p1': он должен повыситься в Main, а самый старый из Main ('m1') - понизиться в In
	if val, ok := c.Get("p1"); !ok || val != 100 {
		t.Fatal("p1 должен быть в кэше")
	}

	if e, ok := c.hash["p1"]; !ok || e.Value.segment != segMain {
		t.Errorf("'p1' должен был перейти в segMain")
	}
	if e, ok := c.hash["m1"]; !ok || e.Value.segment != segIn {
		t.Errorf("вытесненный из Main 'm1' должен был понизиться в segIn")
	}

	// 'm1' все еще доступен через listIn, повторный Get вернет его обратно в Main (вытолкнув 'm2' в In)
	if val, ok := c.Get("m1"); !ok || val != 1 {
		t.Errorf("пониженный 'm1' должен был остаться доступным в кэше")
	}
	if e, ok := c.hash["m1"]; !ok || e.Value.segment != segMain {
		t.Errorf("'m1' должен был вернуться в segMain после повторного Get")
	}
	if e, ok := c.hash["m2"]; !ok || e.Value.segment != segIn {
		t.Errorf("'m2' должен был понизиться в segIn")
	}
}

func TestCache_SLRU_OneHitWonderEviction(t *testing.T) {
	// capacity = 5 -> capIn = 1, capMain = 4
	c := New[string, int](5)

	// Переводим ключи в Main
	c.Put("m1", 1)
	c.Put("m2", 2)
	c.Put("m3", 3)
	c.Put("m4", 4)
	c.Put("m5", 5)

	c.Get("m1")
	c.Get("m2")
	c.Get("m3")
	c.Get("m4")
	c.Get("m5") // вытесняем m1 в In

	// Поток одноразовых ключей в listIn не должен затронуть Main
	c.Put("one1", 10)
	c.Put("one2", 20) // вытесняет one1

	if _, ok := c.Get("one1"); ok {
		t.Errorf("'one1' должен был вытесниться")
	}
	if _, ok := c.Get("m1"); ok {
		t.Errorf("'m1' должен вытесниться")
	}
	if _, ok := c.Get("m2"); !ok {
		t.Errorf("'m2' должен оставаться в Main")
	}
	if _, ok := c.Get("m3"); !ok {
		t.Errorf("'m3' должен оставаться в Main")
	}
	if _, ok := c.Get("m4"); !ok {
		t.Errorf("'m4' должен оставаться в Main")
	}
	if _, ok := c.Get("m5"); !ok {
		t.Errorf("'m5' должен оставаться в Main")
	}
}

func TestCache_DeleteKey_BothSegments(t *testing.T) {
	c := New[string, int](5) // capIn = 1, capMain = 4

	if c.DeleteKey("missing") {
		t.Errorf("ожидалось false при удалении несуществующего ключа")
	}

	c.Put("mainKey", 1)
	c.Get("mainKey") // перевели в Main

	c.Put("inKey", 2) // остался в In

	if !c.DeleteKey("mainKey") || c.DeleteKey("mainKey") {
		t.Errorf("ошибка при удалении mainKey")
	}
	if !c.DeleteKey("inKey") || c.DeleteKey("inKey") {
		t.Errorf("ошибка при удалении inKey")
	}
	if c.Len() != 0 {
		t.Errorf("ожидался пустой кэш, получено Len=%d", c.Len())
	}
}

func TestCache_SLRU_CapacityOne(t *testing.T) {
	c := New[string, int](1)

	if c.capIn != 0 || c.capMain != 1 {
		t.Fatalf("ожидались квоты capIn=0, capMain=1; получено capIn=%d, capMain=%d", c.capIn, c.capMain)
	}

	// Вставка первого элемента
	c.Put("k1", 10)
	if c.Len() != 1 {
		t.Fatalf("ожидался размер кэша 1, получено %d", c.Len())
	}
	if val, ok := c.Get("k1"); !ok || val != 10 {
		t.Fatalf("ожидалось (10, true) для 'k1', получено (%v, %v)", val, ok)
	}

	// Обновление существующего элемента не должно вызывать вытеснение
	c.Put("k1", 15)
	if c.Len() != 1 {
		t.Fatalf("ожидался размер кэша 1 после обновления, получено %d", c.Len())
	}
	if val, ok := c.Get("k1"); !ok || val != 15 {
		t.Fatalf("ожидалось обновленное значение (15, true), получено (%v, %v)", val, ok)
	}

	// Вставка второго ключа должна вытеснить первый без превышения capacity = 1
	c.Put("k2", 20)
	if c.Len() != 1 {
		t.Fatalf("размер кэша превысил capacity=1: получено %d", c.Len())
	}
	if _, ok := c.Get("k1"); ok {
		t.Errorf("ключ 'k1' должен был быть вытеснен")
	}
	if val, ok := c.Get("k2"); !ok || val != 20 {
		t.Errorf("ожидалось (20, true) для 'k2', получено (%v, %v)", val, ok)
	}

	// Удаление и повторная вставка (проверка переиспользования c.spare)
	if !c.DeleteKey("k2") {
		t.Fatal("ожидалось успешное удаление 'k2'")
	}
	if c.Len() != 0 {
		t.Fatalf("ожидался пустой кэш после удаления, получено %d", c.Len())
	}

	c.Put("k3", 30)
	if val, ok := c.Get("k3"); !ok || val != 30 {
		t.Errorf("ожидалось (30, true) для 'k3' после переиспользования слота, получено (%v, %v)", val, ok)
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
