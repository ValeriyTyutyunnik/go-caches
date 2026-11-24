package full2q

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
	if c.capIn != 2 || c.capMain != 8 || c.capOut != 5 {
		t.Errorf("неверное распределение емкостей: in=%d, main=%d, out=%d", c.capIn, c.capMain, c.capOut)
	}
	if c.hash == nil {
		t.Errorf("мапа не инициализирована")
	}
	if c.listMain == nil || c.listIn == nil || c.listOut == nil {
		t.Errorf("один из списков не инициализирован")
	}
}

func TestCache_BasicGetPutUpdate(t *testing.T) {
	c := New[string, int](4) // capIn = 1, capMain = 3, capOut = 2

	// Промах на пустом кэше
	if val, ok := c.Get("a"); ok || val != 0 {
		t.Fatalf("ожидался промах для ключа 'a', получено (%v, %v)", val, ok)
	}

	// Вставка в listIn и попадание
	c.Put("a", 10)
	if val, ok := c.Get("a"); !ok || val != 10 {
		t.Fatalf("ожидалось (10, true), получено (%v, %v)", val, ok)
	}

	// Обновление существующего ключа в listIn
	c.Put("a", 20)
	if val, ok := c.Get("a"); !ok || val != 20 {
		t.Fatalf("ожидалось обновленное значение (20, true), получено (%v, %v)", val, ok)
	}

	expectedStat := "hit=2; miss=1; del=0; upd=1; evict=0; scans=0; HitRate=0.67; MissRate=0.33; AvgScans=0.00\n"
	if got := c.GetStatistic(); got != expectedStat {
		t.Errorf("неверная статистика: ожидалось: %q; получено: %q", expectedStat, got)
	}
}

func TestCache_Cap1(t *testing.T) {
	c := New[string, int](1) // capIn = 0, capMain = 1, capOut = 1

	// Вставка в Main
	c.Put("a", 10)
	if elem, exists := c.hash["a"]; !exists || elem.Value.segment != segMain {
		t.Fatalf("a должен находиться в Main-очереди")
	}
	if val, ok := c.Get("a"); !ok || val != 10 {
		t.Fatalf("ожидалось (10, true), получено (%v, %v)", val, ok)
	}

	c.Put("b", 10)

	if elem, exists := c.hash["a"]; !exists || elem.Value.segment != segMain {
		t.Fatalf("a должен находиться в Main-очереди")
	}
	if elem, exists := c.hash["b"]; !exists || elem.Value.segment != segOut {
		t.Fatalf("b должен находиться в Out-очереди")
	}

	c.Put("b", 20)
	if elem, exists := c.hash["b"]; !exists || elem.Value.segment != segMain {
		t.Fatalf("b должен находиться в Main-очереди")
	}
	if val, ok := c.Get("b"); !ok || val != 20 {
		t.Fatalf("ожидалось (20, true), получено (%v, %v)", val, ok)
	}
	if _, exists := c.hash["a"]; exists {
		t.Fatalf("'a' должен быть удален")
	}
}

func TestCache_2Q_PromotionThroughGhostQueue(t *testing.T) {
	// capacity = 8 -> capIn = 2, capMain = 6, capOut = 4
	c := New[string, int](8)

	// Добавляем k1 и k2 во входную очередь listIn
	c.Put("k1", 1)
	c.Put("k2", 2)

	if val, ok := c.Get("k1"); !ok || val != 1 {
		t.Fatal("k1 не найден")
	}

	// Добавляем k3: входная очередь переполнена, самый старый k1 вытесняется в Out-очередь
	c.Put("k3", 3)

	if _, ok := c.Get("k1"); ok {
		t.Errorf("k1 не должен быть найден")
	}
	if elem, existsInOut := c.hash["k1"]; !existsInOut || elem.Value.segment != segOut {
		t.Fatalf("k1 должен находиться в Out-очереди")
	}

	// Повторно записываем k1: из Out-очереди он попадает в Main
	c.Put("k1", 100)

	if e, ok := c.hash["k1"]; !ok || e.Value.segment != segMain {
		t.Errorf("k1 должен находиться в Main")
	}
	if val, ok := c.Get("k1"); !ok || val != 100 {
		t.Errorf("ожидалось (100, true), получено (%v, %v)", val, ok)
	}
}

func TestCache_2Q_GhostQueueOverflow(t *testing.T) {
	// capacity = 4 -> capIn = 1, capMain = 3, capOut = 2
	c := New[string, int](4)

	c.Put("k1", 1) // listIn: [k1]
	c.Put("k2", 2) // listIn: [k2], listOut: [k1]
	c.Put("k3", 3) // listIn: [k3], listOut: [k2, k1]
	c.Put("k4", 4) // listIn: [k4], listOut: [k3, k2] -> k1 вытеснен

	// Вставляем k1 заново: как новый элемент он пойдет в listIn
	c.Put("k1", 10)

	if e, ok := c.hash["k1"]; !ok || e.Value.segment != segIn {
		t.Errorf("k1 должен был попасть во входную очередь")
	}
}

func TestCache_2Q_MainQueueLRU(t *testing.T) {
	// capacity = 4 -> capIn = 1, capMain = 3, capOut = 2
	c := New[string, int](4)

	// Прогоняем m1, m2, m3 через призраков в горячую очередь listMain
	for _, k := range []string{"m1", "m2", "m3"} {
		c.Put(k, 1)            // в listIn
		c.Put("evictor", 0)    // выталкивает k в listOut
		c.DeleteKey("evictor") // очищаем кэш от лишнего ключа
		c.Put(k, 10)           // переносит k в listMain
	}
	// listMain: [m3, m2, m1]

	// Обращаемся к самому старому m1 -> новый порядок [m1, m3, m2]
	if val, ok := c.Get("m1"); !ok || val != 10 {
		t.Fatal("m1 должен быть в Main")
	}

	// Переводим 4-й элемент m4 в listMain -> вытесняем m2
	c.Put("m4", 1)
	c.Put("evictor", 0)
	c.Put("m4", 40)

	if _, ok := c.Get("m2"); ok {
		t.Errorf("m2 не должен быть найден")
	}
	for _, k := range []string{"m1", "m3", "m4"} {
		if _, ok := c.Get(k); !ok {
			t.Errorf("ключ %q должен остаться в Main", k)
		}
	}
}

func TestCache_DeleteKey_AllQueues(t *testing.T) {
	// capacity = 4 -> capIn = 1, capMain = 3, capOut = 2
	c := New[string, int](4)

	if c.DeleteKey("missing") {
		t.Errorf("ожидалось false при удалении несуществующего ключа")
	}

	// Помещаем "mainKey" в listMain
	c.Put("mainKey", 1)  // "mainKey" -> listIn
	c.Put("ghostKey", 2) // "mainKey" -> listOut, "ghostKey" -> listIn
	c.Put("mainKey", 10) // "mainKey" -> listMain
	c.Put("inKey", 3)    // "ghostKey" -> listOut, "inKey" -> listIn

	// Проверяем удаление и повторное удаление того же элемента
	if !c.DeleteKey("mainKey") || c.DeleteKey("mainKey") {
		t.Errorf("ошибка при удалении mainKey из listMain")
	}
	if !c.DeleteKey("inKey") || c.DeleteKey("inKey") {
		t.Errorf("ошибка при удалении inKey из listIn")
	}
	if !c.DeleteKey("ghostKey") || c.DeleteKey("ghostKey") {
		t.Errorf("ошибка при удалении ghostKey из listOut")
	}

	if c.Len() != 0 {
		t.Errorf("очереди должны быть пусты")
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
