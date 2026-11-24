package arc

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
	c := New[string, int](4)

	if val, ok := c.Get("a"); ok || val != 0 {
		t.Fatalf("ожидался промах для ключа 'a', получено (%v, %v)", val, ok)
	}

	// Вставка в T1
	c.Put("a", 10)
	if c.t1.Len() != 1 || c.t2.Len() != 0 {
		t.Fatalf("после первой вставки 'a' должен лежать в T1")
	}

	// Попадание в T1 переносит элемент в T2
	if val, ok := c.Get("a"); !ok || val != 10 {
		t.Fatalf("ожидалось (10, true), получено (%v, %v)", val, ok)
	}
	if c.t1.Len() != 0 || c.t2.Len() != 1 {
		t.Fatalf("после Get 'a' должен перейти в T2")
	}

	// Обновление в T2
	c.Put("a", 20)
	if val, ok := c.Get("a"); !ok || val != 20 {
		t.Fatalf("ожидалось обновленное значение (20, true), получено (%v, %v)", val, ok)
	}

	expectedStat := "hit=2; miss=1; del=0; upd=1; evict=0; scans=0; HitRate=0.67; MissRate=0.33; AvgScans=0.00\n"
	if got := c.GetStatistic(); got != expectedStat {
		t.Errorf("неверная статистика: ожидалось: %q; получено: %q", expectedStat, got)
	}
}

func TestCache_ARC_AdaptationP_UpAndDown(t *testing.T) {
	// capacity = 4, изначально p = 0
	c := New[string, int](4)

	// k0 переводим в T2, а k1, k2, k3 оставляем в T1 (итого T1=3, T2=1)
	c.Put("k0", 0)
	c.Get("k0")
	c.Put("k1", 1)
	c.Put("k2", 2)
	c.Put("k3", 3)

	// Вставляем k4: T1+B1 = 3 < capacity, кэш полон, при p=0 replace вытесняет k1 из T1 в B1
	c.Put("k4", 4)
	if c.b1.Len() != 1 || c.hash["k1"].Value.address != listB1 {
		t.Fatalf("k1 должен был переместиться в B1")
	}

	// Попадание в призрак B1 увеличивает p и переносит k1 в T2
	c.Put("k1", 10)
	if c.p <= 0 {
		t.Errorf("ожидался рост параметра p после попадания в B1, получено p=%d", c.p)
	}
	if c.hash["k1"].Value.address != listT2 {
		t.Errorf("k1 должен попасть в T2")
	}

	// Переносим k3 и k4 в T2 (в T2 лежат все элементы, кроме k2 из B1)
	c.Get("k3")
	c.Get("k4")

	// Вставляем новый ключ: T1=0 и p=1, replace вытеснит последний из T2 (k0) в B2
	c.Put("new1", 50)
	if c.hash["k0"].Value.address != listB2 {
		t.Fatalf("k0 должен был вытесниться из T2 в призраки B2")
	}

	prevP := c.p
	// Попадание в призрак B2 уменьшает параметр p
	c.Put("k0", 100)
	if c.p >= prevP {
		t.Errorf("ожидалось уменьшение параметра p после попадания в B2: было %d, стало %d", prevP, c.p)
	}
}

func TestCache_DeleteKey_AllLists(t *testing.T) {
	c := New[string, int](3)

	if c.DeleteKey("missing") {
		t.Errorf("ожидалось false при удалении несуществующего ключа")
	}

	c.Put("t1Key", 1)
	c.Put("t2Key", 2)
	c.Get("t2Key") // перевели в T2

	c.Put("b1Key", 3) // в T1: [b1Key, t1Key], T2: [t2Key], кэш полон
	c.Put("extra", 4) // t1Key вытеснен в B1

	if !c.DeleteKey("t2Key") || c.DeleteKey("t2Key") {
		t.Errorf("ошибка при удалении t2Key из T2")
	}
	if !c.DeleteKey("b1Key") || c.DeleteKey("b1Key") {
		t.Errorf("ошибка при удалении b1Key из T1")
	}
	if !c.DeleteKey("t1Key") || c.DeleteKey("t1Key") {
		t.Errorf("ошибка при удалении призрака t1Key из B1")
	}
	if !c.DeleteKey("extra") {
		t.Errorf("ошибка при удалении extra")
	}

	if c.Len() != 0 || len(c.hash) != 0 {
		t.Errorf("после удаления всех ключей кэш и мапа должны быть пусты")
	}
}

func TestCache_Put_Reused(t *testing.T) {
	t.Run("T1 == capacity (B1 empty)", func(t *testing.T) {
		c := New[string, int](3)

		c.Put("k1", 1)
		c.Put("k2", 2)
		c.Put("k3", 3)
		// T1: [k3, k2, k1], T2: [], B1: [], B2: []

		victimPtr := c.hash["k1"]

		// t1.Len() + b1.Len() == 3 и t1.Len() == 3 -> вытесняем хвост T1
		c.Put("k4", 4)

		if _, exists := c.hash["k1"]; exists {
			t.Fatalf("ключ 'k1' должен был быть окончательно удален из hash")
		}
		if gotPtr := c.hash["k4"]; gotPtr != victimPtr {
			t.Errorf("ожидалось переиспользование узла %p для ключа 'k4', получено %p", victimPtr, gotPtr)
		}
		if c.t1.Len() != 3 || c.b1.Len() != 0 {
			t.Errorf("ожидалось T1=3, B1=0; получено T1=%d, B1=%d", c.t1.Len(), c.b1.Len())
		}
	})

	t.Run("T1 + B1 == capacity (T1 < capacity)", func(t *testing.T) {
		c := New[string, int](3)

		c.Put("t2_1", 10)
		c.Get("t2_1")
		c.Put("t1_1", 1)
		c.Put("t1_2", 2)
		// T1=[t1_2, t1_1] (2), T2=[t2_1] (1), B1=[], B2=[]

		// T1+B1 = 2 < 3, кэш полон (T1+T2=3), при p=0 replace вытеснит t1_1 из T1 в B1
		c.Put("t1_3", 3)

		victimB1Ptr := c.hash["t1_1"]

		// t1.Len()+b1.Len() == c.capacity и t1.Len() < c.capacity -> выкидываем t1_1 из B1
		c.Put("new_key", 99)

		if _, exists := c.hash["t1_1"]; exists {
			t.Fatalf("призрак 't1_1' должен был быть удален")
		}
		if gotPtr := c.hash["new_key"]; gotPtr != victimB1Ptr {
			t.Errorf("ожидалось переиспользование узла %p, получено %p", victimB1Ptr, gotPtr)
		}
		if c.hash["t1_2"].Value.address != listB1 {
			t.Errorf("ожидалось, что 't1_2' переместится в B1")
		}
		if val, ok := c.Get("new_key"); !ok || val != 99 {
			t.Errorf("ожидалось (99, true) для 'new_key', получено (%v, %v)", val, ok)
		}
	})

	t.Run("total == 2*capacity", func(t *testing.T) {
		c := New[string, int](2)

		c.Put("g1", 1)
		c.Put("g2", 2)
		// T1=[g2, g1] (2), T2=[], B1=[], B2=[]

		c.Get("g2")      // T1=[g1] (1), T2=[g2] (1)
		c.Put("x1", 10)  // p=0 -> replace вытеснит g1 в B1: T1=[x1] (1), T2=[g2] (1), B1=[g1] (1)
		c.Put("g1", 100) // попадание в B1 -> p=1, g2 в B2, g1 в T2: T1=[x1] (1), T2=[g1] (1), B1=[], B2=[g2] (1)

		// Наполняем B2 до 2 элементов
		c.Get("x1")     // T1=[] (0), T2=[x1, g1] (2), B1=[], B2=[g2] (1)
		c.Put("y1", 20) // T1=0 <= p=1 -> replace вытеснит g1 из T2 в B2
		// T1=[y1] (1), T2=[x1] (1), B1=[], B2=[g1, g2] (2)

		total := c.t1.Len() + c.t2.Len() + c.b1.Len() + c.b2.Len()
		if total != 2*c.capacity {
			t.Fatalf("ожидался total == %d, получено %d", 2*c.capacity, total)
		}

		victimB2Ptr := c.hash["g2"]

		// T1+B1 = 1 < 2 -> вытесняем хвост B2 (g2)
		c.Put("x2", 777)

		if _, exists := c.hash["g2"]; exists {
			t.Fatalf("призрак 'g2' должен был быть удален")
		}
		if gotPtr := c.hash["x2"]; gotPtr != victimB2Ptr {
			t.Errorf("ожидалось переиспользование узла %p для 'x2', получено %p", victimB2Ptr, gotPtr)
		}
		if c.b2.Len() != 2 {
			t.Errorf("длина очереди B2 ожидалась 2, получено %d", c.b2.Len())
		}
	})
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
