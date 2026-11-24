package s3fifo

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

func TestS3FIFO_PromotionAndGhostResurrection(t *testing.T) {
	// capacity = 10 -> capS = 1, capM = 9
	c := New[string, int](10)

	// Добавляем 'a' в Small и заполняем кэш до capacity
	c.Put("a", 1)
	for i := range 9 {
		key := fmt.Sprintf("k-%d", i)
		c.Put(key, i)
	}

	// Вставляем 'b' в Small -> 'a' вытесняется в Ghost
	c.Put("b", 2)

	if _, ok := c.Get("a"); ok {
		t.Fatalf("не ожидалось возврата 'a'")
	}
	if c.Stats().GetEvict() != 1 {
		t.Fatalf("ожидалось Evict = 1, получено %d", c.Stats().GetEvict())
	}

	// Попадание в призрак 'a' (Ghost) переносит его в Main
	c.Put("a", 10)
	if val, ok := c.Get("a"); !ok || val != 10 {
		t.Fatalf("ожидалось ('a', 10, true), получено (%d, %v)", val, ok)
	}

	// Увеличиваем частоту 'b' (b.freq++) и вытесняем его из Small в Main вставкой 'c'
	if val, ok := c.Get("b"); !ok || val != 2 {
		t.Fatalf("ожидался возврат 'b'")
	}
	c.Put("c", 3)

	if val, ok := c.Get("b"); !ok || val != 2 {
		t.Fatalf("ожидалось, что 'b' остался в кэше")
	}
}

func TestS3FIFO_MainSecondChance(t *testing.T) {
	// capacity = 10 -> capS = 1, capM = 9
	c := New[string, int](10)

	// Прогреваем кэш до полного заполнения
	for i := range 10 {
		c.Put(fmt.Sprintf("init-%d", i), i)
	}

	// Вставляем "hot", делаем Get (freq=1 в Small) и следующей вставкой выталкиваем в Main
	c.Put("hot", 777)
	c.Get("hot")
	c.Put("push", 1) // теперь "hot" в Main с freq = 0

	// Набиваем "hot" частоту внутри Main (freq = 2)
	c.Get("hot")
	c.Get("hot")

	// Заполняем кэш новыми ключами (больше емкости Main = 9), чтобы заставить Main сделать полный оборот
	for i := 0; i < 15; i++ {
		c.Put(fmt.Sprintf("k-%d", i), i)
	}

	if val, ok := c.Get("hot"); !ok || val != 777 {
		t.Fatalf("ожидалось, что 'hot' останется в кэше благодаря freq > 0 в Main, получено (%d, %v)", val, ok)
	}
	if c.Len() != 10 {
		t.Fatalf("ожидалось Len() = 10, получено %d", c.Len())
	}
}

func TestS3FIFO_CapacityOneAndDelete(t *testing.T) {
	c := New[string, int](1)
	c.Put("x", 1)
	c.Put("y", 2)

	if _, ok := c.Get("x"); ok {
		t.Fatalf("'x' должен быть вытеснен")
	}
	if val, ok := c.Get("y"); !ok || val != 2 {
		t.Fatalf("ожидалось ('y', 2, true), получено (%d, %v)", val, ok)
	}
	if !c.DeleteKey("y") || c.DeleteKey("y") || c.Len() != 0 {
		t.Fatalf("ошибка удаления при capacity=1")
	}
}

func TestCache_S3FIFO_EvictMain(t *testing.T) {
	// capacity = 3 -> capS = max(1, 3/10) = 1, capM = 2
	c := New[string, int](3)

	// Заполняем кэш до capacity (3 элемента в Small)
	c.Put("m1", 10)
	c.Get("m1") // m1.freq = 1

	c.Put("m2", 20)
	c.Get("m2") // m2.freq = 1

	c.Put("s1", 30) // s1.freq = 0
	// Состояние: Small=[s1(0), m2(1), m1(1)], Main=[]

	// Вставляем "s2": кэш полон, listS.Len() (3) >= capS (1) -> m1 и m2 уходят в Main, s1 уходит в Ghost
	c.Put("s2", 40)
	// Состояние: Small=[s2(0)], Main=[m2(0), m1(0)], Ghost=[s1]

	if c.listS.Len() != 1 || c.listM.Len() != 2 {
		t.Fatalf("ожидалось Small=1, Main=2; получено Small=%d, Main=%d", c.listS.Len(), c.listM.Len())
	}

	// Повышаем частоту m1 в Main до 2, а m2 оставляем с freq = 0
	c.Get("m1")
	c.Get("m1") // m1.freq = 2, m2.freq = 0

	// Удаляем "s2" из Small (listS.Len() = 0 < capS = 1) и воскрешаем "s1" из Ghost в Main
	if !c.DeleteKey("s2") {
		t.Fatal("ожидалось удаление 's2'")
	}
	c.Put("s1", 300)
	// Состояние: Small=[], Main=[s1(0), m2(0), m1(2)] (кэш полон)

	if c.listS.Len() != 0 || c.listM.Len() != 3 {
		t.Fatalf("ожидалось Small=0, Main=3; получено Small=%d, Main=%d", c.listS.Len(), c.listM.Len())
	}

	// Вставляем "new_item": listS.Len() < capS -> evictMain() даст второй шанс m1 (freq=2->1) и вытеснит m2 (freq=0)
	c.Put("new_item", 999)

	if _, ok := c.Get("m2"); ok {
		t.Errorf("ожидалось вытеснение 'm2' (freq=0) из Main")
	}
	if val, ok := c.Get("m1"); !ok || val != 10 {
		t.Errorf("ожидалось, что 'm1' получит второй шанс и останется в кэше, получено (%v, %v)", val, ok)
	}
	if c.hash["m1"].Value.freq != 2 { // было 2 -> стало 1 в evictMain -> +1 от Get("m1") выше = 2
		t.Errorf("ожидалась частота m1 = 2, получено %d", c.hash["m1"].Value.freq)
	}
}

func TestS3FIFO_Concurrency(t *testing.T) {
	c := New[string, int](100)
	var wg sync.WaitGroup
	for g := range 16 {
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
