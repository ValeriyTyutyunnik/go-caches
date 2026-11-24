package lirs

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
	c := New[string, int](10) // capHIR = 1, capLIR = 9, maxStackS = 20

	if c.capInit != 10 || c.capHIR != 1 || c.capLIR != 9 || c.maxStackS != 20 {
		t.Errorf("неверные параметры инициализации: capInit=%d, capHIR=%d, capLIR=%d, maxStackS=%d",
			c.capInit, c.capHIR, c.capLIR, c.maxStackS)
	}
	if c.Len() != 0 {
		t.Errorf("ожидался пустой кэш, получено Len=%d", c.Len())
	}
}

func TestCache_BasicGetPutUpdate(t *testing.T) {
	c := New[string, int](3) // capHIR = 1, capLIR = 2

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

func TestCache_LIRS_PromotionAndStackPruning(t *testing.T) {
	// capacity = 3 -> capHIR = 1, capLIR = 2
	c := New[string, int](3)

	c.Put("lir1", 1)
	c.Put("lir2", 2)
	// listS: [lir2(LIR), lir1(LIR)]

	c.Put("hir1", 3)
	// listS: [hir1(HIR), lir2(LIR), lir1(LIR)], listQ: [hir1]

	// Обращаемся к hir1: так как он есть в S, он повышается до LIR,
	// а самый старый LIR понижается
	if val, ok := c.Get("hir1"); !ok || val != 3 {
		t.Fatal("hir1 должен быть в кэше")
	}

	if c.hash["hir1"].status != statusLIR {
		t.Errorf("hir1 должен был получить статус LIR")
	}
	if c.hash["lir1"].status != statusResidentHIR {
		t.Errorf("lir1 должен был понизиться до ResidentHIR")
	}
	if bottom := c.listS.Back().Value; bottom.status != statusLIR || bottom.key != "lir2" {
		t.Errorf("на дне стека S ожидался lir2(LIR), получено %v (status=%d)", bottom.key, bottom.status)
	}
}

func TestCache_LIRS_GhostResurrection(t *testing.T) {
	// capacity = 3 -> capHIR = 1, capLIR = 2
	c := New[string, int](3)

	c.Put("lir1", 1)
	c.Put("lir2", 2)
	c.Put("hir1", 3)
	// listS: [hir1(HIR), lir2(LIR), lir1(LIR)], listQ: [hir1]

	// Вставляем hir2 -> hir1 вытесняется из Q, но остаётся как призрак
	c.Put("hir2", 4)

	if _, ok := c.Get("hir1"); ok {
		t.Errorf("hir1 должен был вытесниться из активного кэша")
	}
	if entry, exists := c.hash["hir1"]; !exists || entry.status != statusNonResidentHIR {
		t.Fatalf("hir1 должен оставаться в стеке S как призрак")
	}

	// Повторно записываем hir1, он сразу LIR
	// вытесняя hir2 из Q, и понижая lir1 со дна S в ResidentHIR
	c.Put("hir1", 30)

	if val, ok := c.Get("hir1"); !ok || val != 30 {
		t.Errorf("воскрешённый hir1 должен быть доступен со значением 30")
	}
	if c.hash["hir1"].status != statusLIR {
		t.Errorf("воскрешённый hir1 должен получить статус LIR")
	}
	if c.Len() != 3 {
		t.Errorf("размер кэша должен остаться 3, получено %d", c.Len())
	}
}

func TestCache_LIRS_ScanResistanceAndMaxStackS(t *testing.T) {
	// capacity = 4 -> capHIR = 1, capLIR = 3, maxStackS = 8
	c := New[string, int](4)

	c.Put("hot1", 1)
	c.Put("hot2", 2)
	c.Put("hot3", 3)

	// Прогоняем одноразовые ключи: все они должны пройти через 1-слотовый HIR-буфер,
	// не вытеснив ни одного горячего LIR-ключа, а размер стека S не должен превысить maxStackS
	for i := range 50 {
		c.Put(fmt.Sprintf("scan-%d", i), i)
	}

	for _, hot := range []string{"hot1", "hot2", "hot3"} {
		if _, ok := c.Get(hot); !ok {
			t.Errorf("горячий ключ %q был вымыт одноразовым сканированием", hot)
		}
	}

	if c.listS.Len() > c.maxStackS {
		t.Errorf("стек S превысил ограничение maxStackS: %d > %d", c.listS.Len(), c.maxStackS)
	}
}

func TestCache_CapacityOne(t *testing.T) {
	c := New[string, int](1) // capHIR = 1, capLIR = 0

	c.Put("a", 1)
	c.Get("a")
	c.Put("b", 2)

	if c.Len() != 1 {
		t.Errorf("при capacity=1 Len должен быть 1, получено %d", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Errorf("ключ 'a' должен был вытесниться")
	}
	if val, ok := c.Get("b"); !ok || val != 2 {
		t.Errorf("ожидалось (2, true) для 'b'")
	}
}

func TestCache_DeleteKey(t *testing.T) {
	c := New[string, int](3) // capHIR = 1, capLIR = 2

	if c.DeleteKey("missing") {
		t.Errorf("ожидалось false при удалении несуществующего ключа")
	}

	c.Put("lir1", 1)
	c.Put("lir2", 2)
	c.Put("hir1", 3)
	c.Put("hir2", 4)

	// Удаляем оба LIR-блока со дна S (lir1 и lir2): теперь на дне оказывается призрак hir1, и pruneStack должен его удалить
	if !c.DeleteKey("lir1") || !c.DeleteKey("lir2") {
		t.Fatal("ожидалось успешное удаление lir1 и lir2")
	}
	if _, ghostExists := c.hash["hir1"]; ghostExists {
		t.Errorf("призрак hir1 со дна стека должен был удалиться")
	}
	if !c.DeleteKey("hir2") {
		t.Errorf("ошибка при удалении hir2")
	}
	if c.Len() != 0 {
		t.Errorf("кэш должен быть пустым, получено Len=%d", c.Len())
	}
}

func TestCache_LIRS_PruneStackLIRLeak(t *testing.T) {
	// capacity = 2 -> capHIR = 1, capLIR = 1, maxStackS = 4
	c := New[string, int](2)

	c.Put("lir1", 1) // LIR (дно S)
	c.Put("hir1", 2) // HIR (голова S и Q)
	c.Put("hir2", 3) // HIR (hir1 -> Ghost, len(S) = 3)
	c.Put("hir3", 4) // HIR (hir2 -> Ghost, len(S) = 4)

	// Вставка "hir4" достигает лимита maxStackS (4).
	// evictOldestGhostFromS() удаляет самый старый призрак (hir1) из S, не трогая "lir1" на дне стека.
	c.Put("hir4", 5)

	if c.listS.Len() > c.maxStackS {
		t.Fatalf("размер стека S превысил maxStackS: %d > %d", c.listS.Len(), c.maxStackS)
	}
	if _, ok := c.Get("lir1"); !ok {
		t.Fatalf("ожидалось, что горячий 'lir1' останется в кэше при одноразовом сканировании")
	}

	// Теперь проверяем, что "lir1" не застрял навечно и корректно вытесняется новым горячим ключом:
	c.Put("new1", 10) // Входит как ResidentHIR в голову S и Q
	c.Put("new1", 11) // Повторное обращение (e.elemS != nil) -> повышается до LIR, а "lir1" понижается в HIR (listQ)

	if c.hash["lir1"].status != statusResidentHIR {
		t.Errorf("ожидалось понижение 'lir1' до statusResidentHIR, получено %v", c.hash["lir1"].status)
	}

	// Вставляем одноразовый "new2" в Q: он вытесняет пониженный "lir1" из Q в NonResidentHIR
	c.Put("new2", 20)
	if _, ok := c.Get("lir1"); ok {
		t.Errorf("ожидалось вытеснение 'lir1' из кэша после понижения до HIR")
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
