package lrfu

import (
	"fmt"
	"math"
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

func TestCache_NewWithLambdaPanicCapacity(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("ожидалась паника при capacity <= 0")
		}
	}()
	NewWithLambda[string, int](0, 1)
}

func TestCache_NewWithLambdaPanicLambda(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("ожидалась паника при lambda > 1")
		}
	}()
	NewWithLambda[string, int](10, 2)
}

func TestLRFU_CombinedRecencyAndFrequency(t *testing.T) {
	// lambda = 0.1: частый ключ "freq" должен пережить одиночные свежие вставки, но со временем протухнуть
	c := NewWithLambda[string, int](3, 0.1)

	c.Put("freq", 1)
	c.Get("freq")
	c.Get("freq") // weight ~2.80

	c.Put("a", 2)
	c.Put("b", 3)

	// Вставляем "c": вытеснится "a" (наименьший вес), а "freq" выживет
	c.Put("c", 4)

	if _, ok := c.Get("a"); ok {
		t.Fatalf("ожидалось вытеснение 'a'")
	}
	if val, ok := c.Get("freq"); !ok || val != 1 {
		t.Fatalf("ожидалось, что 'freq' останется в кэше, получено (%d, %v)", val, ok)
	}

	// Поднимаем веса "b" и "c", чтобы "freq" стал самым легким
	for range 2 {
		c.Get("b")
		c.Get("c")
	}

	c.Put("d", 4)
	if _, ok := c.Get("freq"); ok {
		t.Fatalf("ожидалось вытеснение 'freq'")
	}
}

func TestLRFU_HighLambdaBehavesLikeLRU(t *testing.T) {
	// При lambda = 1.0 алгоритм работает как обычный LRU
	c := NewWithLambda[string, int](2, 1.0)

	c.Put("k1", 1)
	c.Put("k2", 2)
	c.Get("k1")    // k1 становится самым свежим
	c.Put("k3", 3) // должен вытесниться k2

	if _, ok := c.Get("k2"); ok {
		t.Fatalf("при lambda=1.0 ожидалось вытеснение самого старого 'k2'")
	}
	if val, ok := c.Get("k1"); !ok || val != 1 {
		t.Fatalf("ожидалось сохранение 'k1'")
	}
}

func TestLRFU_DeleteAndStats(t *testing.T) {
	c := New[string, int](3)
	c.Put("x", 10)
	c.Put("y", 20)
	c.Get("x")
	c.Get("missing")
	c.Put("x", 11)

	if !c.DeleteKey("y") || c.DeleteKey("y") {
		t.Fatalf("ошибка при удалении 'y'")
	}

	st := c.Stats()
	if st.GetHit() != 1 || st.GetMiss() != 1 || st.GetUpd() != 1 || st.GetDel() != 1 || c.Len() != 1 {
		t.Fatalf("неверная статистика: hit=%d miss=%d upd=%d del=%d len=%d",
			st.GetHit(), st.GetMiss(), st.GetUpd(), st.GetDel(), c.Len())
	}
}

func TestLRFU_EqualWeightsTieBreaker(t *testing.T) {
	// При lambda = 0 веса не затухают (чистый LFU), при равном весе вытесняется более старый элемент
	c := NewWithLambda[string, int](2, 0)

	c.Put("first", 1)  // lastTime = 1, weight = 1.0
	c.Put("second", 2) // lastTime = 2, weight = 1.0

	c.Get("first")  // lastTime = 3, weight = 2.0
	c.Get("second") // lastTime = 4, weight = 2.0

	// У обоих вес 2.0 -> вытеснится "first", так как его lastTime (3) меньше, чем у "second" (4)
	c.Put("third", 3)

	if _, ok := c.Get("first"); ok {
		t.Fatalf("при равных весах ожидалось вытеснение более старого элемента 'first'")
	}
	if val, ok := c.Get("second"); !ok || val != 2 {
		t.Fatalf("ожидалось сохранение 'second', получено (%d, %v)", val, ok)
	}
	if val, ok := c.Get("third"); !ok || val != 3 {
		t.Fatalf("ожидалось сохранение 'third', получено (%d, %v)", val, ok)
	}
}

func TestCache_Renormalize(t *testing.T) {
	t.Run("renormalize on touch preserves heap order and relative weights", func(t *testing.T) {
		c := NewWithLambda[string, int](3, 0.5)

		c.Put("cold", 1)
		c.Put("warm", 2)
		c.Put("hot", 3)

		c.Get("warm")
		c.Get("hot")
		c.Get("hot")

		// Перематываем время за порог maxLogWeight
		shift := uint64(maxLogWeight/c.h.lambda) + 10
		shiftLog := c.h.lambda * float64(shift)

		c.h.now += shift
		for _, e := range c.h.items {
			e.logWeight += shiftLog
			e.lastTime += shift
		}

		// Вызов Get("hot") должен запустить renormalize()
		val, ok := c.Get("hot")
		if !ok || val != 3 {
			t.Fatalf("ожидалось hot=3, получено %v (ok=%v)", val, ok)
		}

		// Проверяем, что время и веса сбросились в нормальный диапазон
		if c.h.now > 10 {
			t.Errorf("ожидалось h.now <= 10 после ренормализации, получено %d", c.h.now)
		}
		for _, e := range c.h.items {
			if e.lastTime > c.h.now {
				t.Errorf("ожидалось lastTime (%d) <= h.now (%d) для ключа %q", e.lastTime, c.h.now, e.key)
			}
			if e.logWeight > 100 || math.IsNaN(e.logWeight) || math.IsInf(e.logWeight, 0) {
				t.Errorf("некорректный logWeight %v для ключа %q после ренормализации", e.logWeight, e.key)
			}
		}

		// Проверяем, что порядок вытеснения сохранился: cold -> warm -> hot
		c.Put("new1", 4)
		if _, exists := c.Get("cold"); exists {
			t.Error("ожидалось, что 'cold' будет вытеснен первым")
		}

		c.Put("new2", 5)
		if _, exists := c.Get("warm"); exists {
			t.Error("ожидалось, что 'warm' будет вытеснен вторым")
		}

		if _, exists := c.Get("hot"); !exists {
			t.Error("ожидалось, что 'hot' останется в кэше")
		}
	})

	t.Run("renormalize on Put eviction", func(t *testing.T) {
		c := NewWithLambda[string, int](2, 0.5)

		c.Put("k1", 1)
		c.Put("k2", 2)
		c.Get("k2") // k2 тяжелее k1

		// Перематываем время к порогу maxLogWeight
		shift := uint64(maxLogWeight/c.h.lambda) + 10
		shiftLog := c.h.lambda * float64(shift)

		c.h.now += shift
		for _, e := range c.h.items {
			e.logWeight += shiftLog
			e.lastTime += shift
		}

		// Вставка при полном кэше должна вытеснить k1 и вызвать renormalize()
		c.Put("k3", 3)

		if c.h.now > 10 {
			t.Errorf("ожидалось h.now <= 10 после ренормализации в Put, получено %d", c.h.now)
		}
		if _, exists := c.Get("k1"); exists {
			t.Error("ожидалось, что 'k1' будет вытеснен")
		}
		if _, exists := c.Get("k2"); !exists {
			t.Error("ожидалось, что 'k2' останется в кэше")
		}
		if _, exists := c.Get("k3"); !exists {
			t.Error("ожидалось, что 'k3' будет в кэше")
		}
	})
}

func TestCache_TouchHotLeafFastPath(t *testing.T) {
	t.Run("fast path skips weight recalculation for ultra-hot leaf", func(t *testing.T) {
		// Емкость 4: индексы 2 и 3 являются листьями кучи
		c := NewWithLambda[string, int](4, 0.001)

		c.Put("k1", 1)
		c.Put("k2", 2)
		c.Put("k3", 3)
		c.Put("hot", 4)

		hotEntry := c.hash["hot"]
		c.Get("hot") // опускаем "hot" в листья
		if hotEntry.index < len(c.h.items)/2 {
			t.Fatalf("ожидалось, что 'hot' станет листом (index >= %d), получено index %d", len(c.h.items)/2, hotEntry.index)
		}

		// Задаем diff = logWeight - nowLog > 30
		nowLog := c.h.lambda * float64(c.h.now)
		hotEntry.logWeight = nowLog + 35.0
		hotEntry.lastTime = c.h.now
		savedLogWeight := hotEntry.logWeight

		// Повторный Get("hot") при dt == 1 должен пойти по fast-path
		val, ok := c.Get("hot")
		if !ok || val != 4 {
			t.Fatalf("ожидалось hot=4, получено %v (ok=%v)", val, ok)
		}

		// Проверяем, что logWeight не пересчитывался, а lastTime обновился
		const eps = 1e-6
		if math.Abs(hotEntry.logWeight-savedLogWeight) > eps {
			t.Errorf("ожидалось сохранение logWeight %v на fast-path, получено %v", savedLogWeight, hotEntry.logWeight)
		}
		if hotEntry.lastTime != c.h.now {
			t.Errorf("ожидалось обновление lastTime до %d, получено %d", c.h.now, hotEntry.lastTime)
		}
	})

	t.Run("fast path triggers renormalize when exceeding maxLogWeight", func(t *testing.T) {
		c := NewWithLambda[string, int](3, 0.5)

		c.Put("cold1", 1)
		c.Put("cold2", 2)
		c.Put("hot", 3)

		hotEntry := c.hash["hot"]
		c.Get("hot") // отправляем в листья (index >= 1)

		// Перематываем время за порог maxLogWeight
		shift := uint64(maxLogWeight/c.h.lambda) + 10
		shiftLog := c.h.lambda * float64(shift)

		c.h.now += shift
		for _, e := range c.h.items {
			e.logWeight += shiftLog
			e.lastTime += shift
		}

		// Делаем diff > 30 и dt == 1 для следующего обращения
		nowLog := c.h.lambda * float64(c.h.now)
		hotEntry.logWeight = nowLog + 35.0
		hotEntry.lastTime = c.h.now

		// Вызов Get("hot") пойдет по fast-path и вызовет renormalize()
		val, ok := c.Get("hot")
		if !ok || val != 3 {
			t.Fatalf("ожидалось hot=3, получено %v (ok=%v)", val, ok)
		}

		if c.h.now > 10 {
			t.Errorf("ожидалось, что ренормализация сбросит h.now <= 10, получено %d", c.h.now)
		}

		// Проверяем, что "hot" остался самым тяжелым и не вытесняется первым
		c.Put("new1", 4)
		c.Put("new2", 5)
		if _, exists := c.Get("hot"); !exists {
			t.Error("ожидалось, что 'hot' останется в кэше после ренормализации на fast-path")
		}
	})
}

func TestLRFU_Concurrency(t *testing.T) {
	c := New[string, int](100)
	var wg sync.WaitGroup
	for g := range 12 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for i := range 2000 {
				k := fmt.Sprintf("key-%d", r.Intn(200))
				switch r.Intn(3) {
				case 0:
					c.Put(k, i)
				case 1:
					c.Get(k)
				case 2:
					c.DeleteKey(k)
				}
			}
		}(g)
	}
	wg.Wait()

	if c.Len() > 100 {
		t.Fatalf("превышена емкость кэша: %d > 100", c.Len())
	}
}
