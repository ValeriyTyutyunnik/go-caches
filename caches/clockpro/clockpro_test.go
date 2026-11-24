package clockpro

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

func TestCache_ClockPro_ColdPromotionAndHotDemotion(t *testing.T) {
	// capacity = 3 -> capCold = 1 (целевая квота Hot = 2, Cold = 1)
	c := New[string, int](3)

	c.Put("k1", 1)
	c.Put("k2", 2)
	c.Put("k3", 3)

	// Помечаем k1 и k2 как посещенные (visited)
	c.Get("k1")
	c.Get("k2")

	// Вставляем k4: handCold идет с k1, повышает k1 и k2 до Hot (hotCount=2), а k3 уходит в призраки (statusTest)
	c.Put("k4", 4)

	if _, ok := c.Get("k3"); ok {
		t.Errorf("k3 должен был перейти в призраки statusTest")
	}
	if entry, exists := c.hash["k3"]; !exists || entry.Value.status != statusTest {
		t.Fatalf("k3 должен оставаться в кольце как призрак statusTest")
	}
	if c.hotCount != 2 {
		t.Errorf("ожидалось hotCount = 2 (k1 и k2), получено %d", c.hotCount)
	}
	if c.Len() != 3 {
		t.Errorf("размер кэша должен быть 3, получено %d", c.Len())
	}
}

func TestCache_ClockPro_TestGhostResurrection(t *testing.T) {
	// capacity = 4 -> начальное capCold = 2
	c := New[string, int](4)

	c.Put("k1", 1)
	c.Put("k2", 2)
	c.Put("k3", 3)
	c.Put("k4", 4)

	// Вставляем k5: k1 переходит в statusTest
	c.Put("k5", 5)
	if c.hash["k1"].Value.status != statusTest {
		t.Fatalf("k1 должен был получить статус statusTest")
	}

	prevCapCold := c.capCold

	// Воскрешаем призрак: capCold уменьшается на 1, k1 становится statusHot, k2 уходит в statusTest
	c.Put("k1", 100)

	if c.capCold != prevCapCold-1 {
		t.Errorf("ожидалось уменьшение capCold до %d, получено %d", prevCapCold-1, c.capCold)
	}
	if c.hash["k1"].Value.status != statusHot {
		t.Errorf("воскрешенный k1 должен получить статус statusHot")
	}
	if val, ok := c.Get("k1"); !ok || val != 100 {
		t.Errorf("ожидалось (100, true) для воскрешенного k1")
	}
}

func TestCache_ClockPro_TestQueueOverflow(t *testing.T) {
	// capacity = 4 -> начальное capCold = 2, testCount не может превышать 4
	c := New[string, int](4)

	// Создаем призрак
	for i := 1; i <= 5; i++ {
		c.Put(fmt.Sprintf("init-%d", i), i)
	}
	c.Put("init-1", 10) // воскрешаем призрак, capCold становится 1

	// Заполняем одноразовыми ключами: при переполнении testCount сработает runHandTest() и вернет capCold
	for i := range 15 {
		c.Put(fmt.Sprintf("scan-%d", i), i)
	}

	if c.testCount > c.capacity {
		t.Errorf("число призраков превысило capacity: %d > %d", c.testCount, c.capacity)
	}
	if c.capCold <= 1 {
		t.Errorf("ожидалось увеличение capCold при вытеснении призраков, получено %d", c.capCold)
	}
}

func TestCache_CapacityOne(t *testing.T) {
	c := New[string, int](1)

	c.Put("a", 1)
	c.Get("a")
	c.Put("b", 2)
	c.Put("a", 3) // воскрешение призрака при capacity = 1

	if c.Len() != 1 {
		t.Errorf("при capacity=1 Len должен быть 1, получено %d", c.Len())
	}
	if val, ok := c.Get("a"); !ok || val != 3 {
		t.Errorf("ожидалось (3, true) для 'a'")
	}
}

func TestCache_DeleteKey(t *testing.T) {
	c := New[string, int](3)

	if c.DeleteKey("missing") {
		t.Errorf("ожидалось false при удалении несуществующего ключа")
	}

	c.Put("k1", 1)
	c.Put("k2", 2)
	c.Get("k1")    // visited = true
	c.Put("k3", 3) // кэш полон
	c.Put("k4", 4) // k1 станет Hot, k2 станет Test

	if !c.DeleteKey("k1") || c.DeleteKey("k1") {
		t.Errorf("ошибка при удалении Hot-ключа k1")
	}
	if !c.DeleteKey("k2") || c.DeleteKey("k2") {
		t.Errorf("ошибка при удалении Test-призрака k2")
	}
	if !c.DeleteKey("k3") || !c.DeleteKey("k4") {
		t.Errorf("ошибка при удалении оставшихся ключей")
	}
	if c.Len() != 0 || c.list.Len() != 0 || c.handHot != nil || c.handCold != nil || c.handTest != nil {
		t.Errorf("после удаления всех элементов кольцо и все стрелки должны быть пусты")
	}
}

func TestCache_ClockPro_ColdPromotionOverflow(t *testing.T) {
	// Адаптация квоты и понижение Hot при переполнении
	c := New[string, int](4)
	c.Put("k1", 1)
	c.Put("k2", 2)
	c.Put("k3", 3)
	c.Put("k4", 4)
	// Hot=[], Cold=[k1(0), k2(0), k3(0), k4(0)], Test=[], capCold=2

	c.Get("k1")
	c.Get("k2")
	// Hot=[], Cold=[k1(1), k2(1), k3(0), k4(0)], Test=[], capCold=2

	c.Put("k5", 5)
	// k1, k2 -> Hot(0), k3 -> Test; Hot=[k1(0), k2(0)], Cold=[k4(0), k5(0)], Test=[k3], capCold=2
	c.Put("k6", 6)
	// k4 -> Test; Hot=[k1(0), k2(0)], Cold=[k5(0), k6(0)], Test=[k3, k4], capCold=2

	if c.Len() != 4 {
		t.Fatalf("ожидался размер кэша 4, получено %d", c.Len())
	}
	if c.testCount != 2 {
		t.Errorf("ожидалось 2 тестовых элемента (k3, k4), получено %d", c.testCount)
	}
	if _, ok := c.Get("k3"); ok {
		t.Errorf("ключ k3 должен быть вытеснен в Test")
	}

	c.Put("k3", 30)
	// Попадание в Test(k3) -> capCold=1 (Hot quota=3), k5 -> Test; Hot=[k1(0), k2(0), k3(0)], Cold=[k6(0)], Test=[k4, k5]
	c.Put("k4", 40)
	// Попадание в Test(k4) -> capCold=1, k6 -> Test, k1 -> Cold(0); Hot=[k2(0), k3(0), k4(0)], Cold=[k1(0)], Test=[k5, k6]

	if c.Len() != 4 {
		t.Fatalf("ожидался размер кэша 4 после воскрешения из Test, получено %d", c.Len())
	}
	if val, ok := c.Get("k3"); !ok || val != 30 {
		t.Errorf("ожидалось (30, true) для k3, получено (%v, %v)", val, ok)
	}
	if val, ok := c.Get("k4"); !ok || val != 40 {
		t.Errorf("ожидалось (40, true) для k4, получено (%v, %v)", val, ok)
	}

	// Массовое повышение всех Cold-элементов с посещениями при заполненном кэше
	c2 := New[string, int](4)
	c2.Put("a", 1)
	c2.Put("b", 2)
	c2.Put("c", 3)
	c2.Put("d", 4)
	// Hot=[], Cold=[a(0), b(0), c(0), d(0)], Test=[], capCold=2

	c2.Get("a")
	c2.Get("b")
	c2.Get("c")
	c2.Get("d")
	// Hot=[], Cold=[a(1), b(1), c(1), d(1)], Test=[], capCold=2

	c2.Put("e", 5)
	// a, b -> Hot(0); c -> Hot(0) вытесняет a -> Cold(0) -> Test; d -> Hot(0) вытесняет b -> Cold(0)
	// Hot=[c(0), d(0)], Cold=[b(0), e(0)], Test=[a], capCold=2

	if c2.Len() != 4 {
		t.Fatalf("ожидался размер кэша 4, получено %d (hot=%d, cold=%d)", c2.Len(), c2.hotCount, c2.coldCount)
	}
	if c2.hotCount > c2.capacity-c2.capCold {
		t.Errorf("превышена квота Hot: %d > %d", c2.hotCount, c2.capacity-c2.capCold)
	}
	if _, ok := c2.Get("a"); ok {
		t.Errorf("элемент 'a' должен был понизиться из Hot в Cold и вытесниться")
	}
	if val, ok := c2.Get("e"); !ok || val != 5 {
		t.Errorf("новый ключ 'e' должен присутствовать в кэше, получено (%v, %v)", val, ok)
	}

	// Проверка размера при capacity = 2
	c3 := New[string, int](2)
	c3.Put("x", 1)
	c3.Put("y", 2)
	c3.Get("x")
	c3.Get("y")
	// Hot=[], Cold=[x(1), y(1)], Test=[], capCold=1

	c3.Put("z", 3)
	// x -> Hot(0), y -> Hot(0) понижает x -> Cold(0) -> Test; Hot=[y(0)], Cold=[z(0)], Test=[x]

	if c3.Len() != 2 {
		t.Fatalf("ожидался размер кэша 2, получено %d (hot=%d, cold=%d)", c3.Len(), c3.hotCount, c3.coldCount)
	}
	if _, ok := c3.Get("x"); ok {
		t.Errorf("элемент 'x' должен быть вытеснен")
	}
	if val, ok := c3.Get("z"); !ok || val != 3 {
		t.Errorf("ожидалось (3, true) для 'z', получено (%v, %v)", val, ok)
	}

	// Все Cold переходят в Hot, а квота Hot вмещает их всех
	c4 := New[string, int](2)
	c4.capCold = 0
	c4.Put("k1", 1)
	c4.Put("k2", 2)
	c4.Get("k1")
	c4.Get("k2")
	// Hot=[], Cold=[k1(1), k2(1)], Test=[], capCold=0 (Hot quota=2)

	c4.Put("k3", 3)
	// k1, k2 -> Hot(0) (coldCount=0) -> runHandHot понижает k1 -> Cold(0) -> Test
	// Итого: Hot=[k2(0)], Cold=[k3(0)], Test=[k1]

	if c4.Len() != 2 {
		t.Fatalf("кэш переполнился (%d > 2): evictCold не вытеснил элемент после повышения Cold->Hot", c4.Len())
	}
	if val, ok := c4.Get("k3"); !ok || val != 3 {
		t.Errorf("ожидалось (3, true) для 'k3', получено (%v, %v)", val, ok)
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
