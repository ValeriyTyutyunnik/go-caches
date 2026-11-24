package sharded

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/ValeriyTyutyunnik/go-caches/caches/sieve"
)

func TestCache_NewPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("ожидалась паника при capacity <= 0")
		}
	}()
	NewSharded(0, 16, func(cap int) Cache[string, int] {
		return sieve.New[string, int](cap)
	})
}

func TestShardedCache_InitShards(t *testing.T) {
	// capacity 1 => shards=1
	c := NewSharded(1, 16, func(cap int) Cache[string, int] {
		return sieve.New[string, int](cap)
	})
	if len(c.shards) != 1 {
		t.Fatalf("ожидался 1 шард, получено %d", len(c.shards))
	}

	// capacity 5 => shards=4
	c = NewSharded(5, 16, func(cap int) Cache[string, int] {
		return sieve.New[string, int](cap)
	})
	if len(c.shards) != 4 {
		t.Fatalf("ожидалось 4 шарда, получено %d", len(c.shards))
	}
}

func TestShardedCache_BasicAndDistribution(t *testing.T) {
	// Берём ёмкость 1000 чтобы на 1 шард было по 62-63 слота, и 50 точно влезло
	c := NewSharded(1000, 16, func(cap int) Cache[string, int] {
		return sieve.New[string, int](cap)
	})

	if len(c.shards) != 16 {
		t.Fatalf("ожидалось 16 шардов, получено %d", len(c.shards))
	}

	// 1. Вставляем 50 уникальных ключей (0 попаданий, 0 промахов, 0 обновлений)
	for i := range 50 {
		c.Put(fmt.Sprintf("key-%d", i), i*10)
	}

	if c.Len() != 50 {
		t.Errorf("ожидалось Len() = 50, получено %d", c.Len())
	}

	// 50 Hit
	for i := range 50 {
		key := fmt.Sprintf("key-%d", i)
		if val, ok := c.Get(key); !ok || val != i*10 {
			t.Errorf("для %q ожидалось (%d, true), получено (%d, %v)", key, i*10, val, ok)
		}
	}

	// 10 Miss
	for i := 50; i < 60; i++ {
		if _, ok := c.Get(fmt.Sprintf("key-%d", i)); ok {
			t.Errorf("не ожидалось попадания для key-%d", i)
		}
	}

	// 5 Upd
	for i := range 5 {
		c.Put(fmt.Sprintf("key-%d", i), i*100)
	}

	// 1 Del
	if !c.DeleteKey("key-10") || c.DeleteKey("key-10") {
		t.Errorf("ошибка при удалении key-10")
	}
	if c.Len() != 49 {
		t.Errorf("после удаления ожидалось Len() = 49, получено %d", c.Len())
	}

	// Проверяем аггрегацию статистики
	st := c.Stats()
	if st.GetHit() != 50 || st.GetMiss() != 10 || st.GetUpd() != 5 || st.GetDel() != 1 || st.GetEvict() != 0 || st.GetScans() != 0 {
		t.Errorf("неверные счётчики статистики: hit=%d, miss=%d, upd=%d, del=%d, evict=%d, scans=%d",
			st.GetHit(), st.GetMiss(), st.GetUpd(), st.GetDel(), st.GetEvict(), st.GetScans())
	}

	expectedStat := "hit=50; miss=10; del=1; upd=5; evict=0; scans=0; HitRate=0.83; MissRate=0.17; AvgScans=0.00\n"
	if got := c.GetStatistic(); got != expectedStat {
		t.Errorf("неверная строка статистики. ожидалось: %q; получено: %q", expectedStat, got)
	}
}

func TestShardedCache_Capacity(t *testing.T) {
	// при некратной ёмкости (100 / 16) общая capacity шардов равна 100
	c := NewSharded(100, 16, func(cap int) Cache[int, int] {
		return sieve.New[int, int](cap)
	})

	for i := range 500 {
		c.Put(i, i)
	}

	if c.Len() != 100 {
		t.Errorf("ожидалась общая ёмкость 100, получено %d", c.Len())
	}
}

func TestShardedCache_HighConcurrency(t *testing.T) {
	capacity := 1000
	c := NewSharded(capacity, 32, func(cap int) Cache[string, int] {
		return sieve.New[string, int](cap)
	})

	const goroutines = 64
	const opsPerGoroutine = 10000

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for i := range opsPerGoroutine {
				key := fmt.Sprintf("key-%d", r.Intn(2000))
				switch r.Intn(3) {
				case 0:
					c.Put(key, i)
				case 1:
					c.Get(key)
				case 2:
					c.DeleteKey(key)
				}
				if i%500 == 0 {
					_ = c.GetStatistic()
				}
			}
		}(g)
	}

	wg.Wait()

	if c.Len() > capacity {
		t.Errorf("размер шардированного кэша превысил capacity: %d > %d", c.Len(), capacity)
	}
}
