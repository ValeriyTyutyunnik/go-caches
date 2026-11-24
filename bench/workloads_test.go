package bench

import (
	"fmt"
	"math/rand"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/ValeriyTyutyunnik/go-caches/caches/arc"
	"github.com/ValeriyTyutyunnik/go-caches/caches/clock"
	"github.com/ValeriyTyutyunnik/go-caches/caches/clockpro"
	"github.com/ValeriyTyutyunnik/go-caches/caches/clockrw"
	"github.com/ValeriyTyutyunnik/go-caches/caches/full2q"
	"github.com/ValeriyTyutyunnik/go-caches/caches/lfu"
	"github.com/ValeriyTyutyunnik/go-caches/caches/lirs"
	"github.com/ValeriyTyutyunnik/go-caches/caches/lrfu"
	"github.com/ValeriyTyutyunnik/go-caches/caches/lru"
	"github.com/ValeriyTyutyunnik/go-caches/caches/s3fifo"
	"github.com/ValeriyTyutyunnik/go-caches/caches/sieve"
	"github.com/ValeriyTyutyunnik/go-caches/caches/slru"
	"github.com/ValeriyTyutyunnik/go-caches/caches/wtiny_lfu"
	"github.com/ValeriyTyutyunnik/go-caches/sharded"
)

const (
	cacheCapacity    = 10000               // общий размер кэша
	cacheShards      = 16                  // количество шардов в шардированном кэше
	maxKey           = cacheCapacity * 2   // Создаем дефицит места, чтобы провоцировать вытеснение
	trendPhaseZone   = cacheCapacity * 100 // в сценарии "сдвиг тренда" кол-во операций для сдвига фазы
	trendPhaseOffset = cacheCapacity * 5   // шаг горячей зоны
)

// Типы распределения данных
type DistType int

const (
	DistUniform        DistType = iota // 1. Равномерное
	DistPareto80_20                    // 2. Классическое 80/20 (Парето)
	DistZipfian                        // 3. Распределение Ципфа (Реалистичный веб-трафик)
	DistOneHitWonders                  // 4. Одноразовые ключи (One-Hit Wonders)
	DistThunderingHerd                 // 5. Битва за один ключ (Hotspot Contention)
	DistSequential                     // 6. Сканирование диапазона (Sequential Scan)
	DistMovingHotspot                  // 7. Сдвиг тренда (Moving Hotspot)
)

type BenchScenario struct {
	Name       string
	WriteRatio float32  // Доля операций записи (например, 0.20 = 20% Put, 80% Get)
	Dist       DistType // Тип распределения генерации ключей
}

func getBenchScenarios() []BenchScenario {
	return []BenchScenario{
		{Name: "Uniform_ReadHeavy_10_90", WriteRatio: 0.1, Dist: DistUniform},
		{Name: "Pareto_80_20_Standard", WriteRatio: 0.2, Dist: DistPareto80_20},
		{Name: "Zipfian_Web_Traffic", WriteRatio: 0.1, Dist: DistZipfian},
		{Name: "OneHitWonders_CacheWash", WriteRatio: 0.5, Dist: DistOneHitWonders},
		{Name: "ThunderingHerd_LockContention", WriteRatio: 0.05, Dist: DistThunderingHerd},
		{Name: "SequentialScan_BackupImitation", WriteRatio: 0.0, Dist: DistSequential},
		{Name: "MovingHotspot_TrendShift", WriteRatio: 0.1, Dist: DistMovingHotspot},
	}
}

func getShardScaling() []int {
	return []int{1, 4, 8, 16, 32, 64}
}

func runUltimateSuite(b *testing.B, factory func(capacity int) sharded.Cache[string, string]) {
	scenarios := getBenchScenarios()
	var globalCounter atomic.Uint64

	for _, s := range scenarios {
		b.Run(s.Name, func(b *testing.B) {
			// Вызываем фабрику. Каждый сценарий получает чистый экземпляр кэша.
			cache := factory(cacheCapacity)

			globalCounter.Store(0)
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				// Локальный генератор на горутину (Повторяемый Seed + защита от гонки за блокировки rand)
				routineSeed := int64(1337 + rand.Intn(100000))
				src := rand.NewSource(routineSeed)
				r := rand.New(src)

				// Инициализация генератора Ципфа
				zipf := rand.NewZipf(r, 1.1, 1.0, uint64(maxKey-1))

				// Границы для Парето 80/20 (пул ключей в 5 раз больше кэша -> 20% горячих = 100% ёмкости кэша)
				paretoMaxKey := cacheCapacity * 5
				hotKeyBoundary := int(float64(paretoMaxKey) * 0.2)

				// Параметры для сдвига тренда (10 непересекающихся зон размером с кэш)
				trendMaxKey := cacheCapacity * 10
				trendWindow := int(float64(cacheCapacity) * 0.8)

				for pb.Next() {
					var key int
					isScanRead := false

					// --- ВЫБОР КЛЮЧА СОГЛАСНО СЦЕНАРИЮ ---
					switch s.Dist {
					case DistUniform:
						key = r.Intn(maxKey)

					case DistPareto80_20:
						if r.Float32() < 0.8 { // 80% запросов бьют в 20% ключей (ровно размер кэша)
							key = r.Intn(hotKeyBoundary)
						} else {
							key = hotKeyBoundary + r.Intn(paretoMaxKey-hotKeyBoundary)
						}

					case DistZipfian:
						key = int(zipf.Uint64())

					case DistOneHitWonders:
						if r.Float32() < 0.7 {
							// 70% ключей — уникальные, никогда не повторятся
							key = int(globalCounter.Add(1)) + maxKey
						} else {
							// 30% — стабильные ключи из базового пула
							key = r.Intn(cacheCapacity)
						}

					case DistThunderingHerd:
						if r.Float32() < 0.99 {
							key = 42 // 99% запросов по одному ключу
						} else {
							key = r.Intn(maxKey)
						}

					case DistSequential:
						// Реалистичный бэкап: 75% времени обычный трафик по горячему ядру,
						// 25% времени — залповое последовательное чтение холодных одноразовых записей
						op := globalCounter.Add(1)
						phasePos := op % uint64(cacheCapacity*4)
						if phasePos < uint64(cacheCapacity*3) {
							// Обычная работа: горячее ядро размером 80% от кэша
							key = r.Intn(int(float64(cacheCapacity) * 0.8))
						} else {
							// Залп бэкапа: сплошной поток уникальных ключей, которые никогда не повторятся
							key = int(op) + trendMaxKey
							isScanRead = true
						}

					case DistMovingHotspot:
						// Сдвиг тренда: каждые trendPhaseZone операций горячее окно размером 80% кэша
						// перепрыгивает на новую непересекающуюся область
						op := globalCounter.Add(1)
						currentPhase := int(op / uint64(trendPhaseZone))
						offset := (currentPhase * cacheCapacity) % trendMaxKey

						if r.Float32() < 0.8 {
							key = (offset + r.Intn(trendWindow)) % trendMaxKey
						} else {
							key = r.Intn(trendMaxKey)
						}
					}

					// --- ВЫБОР ОПЕРАЦИИ (READ / WRITE) ---
					keyS := fmt.Sprintf("cached_key_%d", key)
					if !isScanRead && r.Float32() < s.WriteRatio {
						cache.Put(keyS, "value")
					} else {
						_, ok := cache.Get(keyS)
						// В режиме cache-aside (DistSequential) любой промах подгружает запись из БД в кэш
						if !ok && s.Dist == DistSequential {
							cache.Put(keyS, "value")
						}
					}
				}
			})

			stats := cache.Stats()
			b.ReportMetric(float64(stats.GetHit()), "hits")
			b.ReportMetric(float64(stats.GetMiss()), "miss")
			b.ReportMetric(float64(stats.GetEvict()), "evict")
			b.ReportMetric(float64(stats.GetUpd()), "upd")
			b.ReportMetric(stats.AvgScans(), "scans/evict")
			b.ReportMetric(stats.HitRate(), "hitRate")
			b.ReportMetric(stats.MissRate(), "missRate")

		})
	}
}

// фабрики кэшей
func fabricLRU[K comparable, V any](capacity int) sharded.Cache[K, V] {
	return lru.New[K, V](capacity)
}
func fabricLFU[K comparable, V any](capacity int) sharded.Cache[K, V] {
	return lfu.New[K, V](capacity)
}
func fabricClock[K comparable, V any](capacity int) sharded.Cache[K, V] {
	return clock.New[K, V](capacity)
}
func fabricClockRW[K comparable, V any](capacity int) sharded.Cache[K, V] {
	return clockrw.New[K, V](capacity)
}
func fabricSieve[K comparable, V any](capacity int) sharded.Cache[K, V] {
	return sieve.New[K, V](capacity)
}
func fabricFull2Q[K comparable, V any](capacity int) sharded.Cache[K, V] {
	return full2q.New[K, V](capacity)
}
func fabricSLRU[K comparable, V any](capacity int) sharded.Cache[K, V] {
	return slru.New[K, V](capacity)
}
func fabricWTinyLFU[K comparable, V any](capacity int) sharded.Cache[K, V] {
	return wtiny_lfu.New[K, V](capacity)
}
func fabricLIRS[K comparable, V any](capacity int) sharded.Cache[K, V] {
	return lirs.New[K, V](capacity)
}
func fabricClockPro[K comparable, V any](capacity int) sharded.Cache[K, V] {
	return clockpro.New[K, V](capacity)
}
func fabricARC[K comparable, V any](capacity int) sharded.Cache[K, V] {
	return arc.New[K, V](capacity)
}
func fabricS3FIFO[K comparable, V any](capacity int) sharded.Cache[K, V] {
	return s3fifo.New[K, V](capacity)
}
func fabricLRFU[K comparable, V any](capacity int) sharded.Cache[K, V] {
	return lrfu.New[K, V](capacity)
}

type cacheFabric[K comparable, V any] struct {
	name   string
	fabric func(cap int) sharded.Cache[K, V]
}

func getCacheFactories[K comparable, V any]() []cacheFabric[K, V] {
	return []cacheFabric[K, V]{
		{"LRU", func(c int) sharded.Cache[K, V] { return lru.New[K, V](c) }},
		{"LFU", func(c int) sharded.Cache[K, V] { return lfu.New[K, V](c) }},
		{"LRFU", func(c int) sharded.Cache[K, V] { return lrfu.New[K, V](c) }},
		{"Clock", func(c int) sharded.Cache[K, V] { return clock.New[K, V](c) }},
		{"Clock-PRO", func(c int) sharded.Cache[K, V] { return clockpro.New[K, V](c) }},
		{"SIEVE", func(c int) sharded.Cache[K, V] { return sieve.New[K, V](c) }},
		{"S3-FIFO", func(c int) sharded.Cache[K, V] { return s3fifo.New[K, V](c) }},
		{"SLRU", func(c int) sharded.Cache[K, V] { return slru.New[K, V](c) }},
		{"Full-2Q", func(c int) sharded.Cache[K, V] { return full2q.New[K, V](c) }},
		{"W-TinyLFU", func(c int) sharded.Cache[K, V] { return wtiny_lfu.New[K, V](c) }},
		{"ARC", func(c int) sharded.Cache[K, V] { return arc.New[K, V](c) }},
		{"LIRS", func(c int) sharded.Cache[K, V] { return lirs.New[K, V](c) }},
	}
}

// Обёртка для шардированных кэшей
func withShards(shards int, f func(capacity int) sharded.Cache[string, string]) func(capacity int) sharded.Cache[string, string] {
	return func(capacity int) sharded.Cache[string, string] {
		return sharded.NewSharded(capacity, shards, f)
	}
}

//  ---------------------- Бэнчи одиночных кэшей ----------------------

func BenchmarkLRU_NonSharded(b *testing.B) {
	runUltimateSuite(b, fabricLRU[string, string])
}

func BenchmarkLFU_NonSharded(b *testing.B) {
	runUltimateSuite(b, fabricLFU)
}

func BenchmarkClock_NonSharded(b *testing.B) {
	runUltimateSuite(b, fabricClock)
}

func BenchmarkClockRW_NonSharded(b *testing.B) {
	runUltimateSuite(b, fabricClockRW)
}

func BenchmarkSieve_NonSharded(b *testing.B) {
	runUltimateSuite(b, fabricSieve)
}

func BenchmarkFull2Q_NonSharded(b *testing.B) {
	runUltimateSuite(b, fabricFull2Q)
}

func BenchmarkSLRU_NonSharded(b *testing.B) {
	runUltimateSuite(b, fabricSLRU)
}

func BenchmarkWTinyLFU_NonSharded(b *testing.B) {
	runUltimateSuite(b, fabricWTinyLFU)
}

func BenchmarkLirs_NonSharded(b *testing.B) {
	runUltimateSuite(b, fabricLIRS)
}

func BenchmarkClockPRO_NonSharded(b *testing.B) {
	runUltimateSuite(b, fabricClockPro)
}

func BenchmarkARC_NonSharded(b *testing.B) {
	runUltimateSuite(b, fabricARC)
}

func BenchmarkS3FIFO_NonSharded(b *testing.B) {
	runUltimateSuite(b, fabricS3FIFO)
}

func BenchmarkLRFU_NonSharded(b *testing.B) {
	runUltimateSuite(b, fabricLRFU)
}

func BenchmarkLRFU_asLRU_NonSharded(b *testing.B) {
	lru := func(capacity int) sharded.Cache[string, string] {
		return lrfu.NewWithLambda[string, string](capacity, 0)
	}
	runUltimateSuite(b, lru)
}

func BenchmarkLRFU_asLFU_NonSharded(b *testing.B) {
	lru := func(capacity int) sharded.Cache[string, string] {
		return lrfu.NewWithLambda[string, string](capacity, 1)
	}
	runUltimateSuite(b, lru)
}

//  ---------------------- Бэнчи шардированных кэшей ----------------------

func BenchmarkLRU_ShardsScaling(b *testing.B) {
	for _, shards := range getShardScaling() {
		if shards == 1 {
			b.Run("NonSharded", func(b *testing.B) {
				runUltimateSuite(b, fabricLRU[string, string])
			})
		} else {
			b.Run(fmt.Sprintf("Sharded-%d", shards), func(b *testing.B) {
				runUltimateSuite(b, withShards(shards, fabricLRU[string, string]))
			})
		}
	}
}

func BenchmarkLFU_ShardsScaling(b *testing.B) {
	for _, shards := range getShardScaling() {
		if shards == 1 {
			b.Run("NonSharded", func(b *testing.B) {
				runUltimateSuite(b, fabricLFU)
			})
		} else {
			b.Run(fmt.Sprintf("Sharded-%d", shards), func(b *testing.B) {
				runUltimateSuite(b, withShards(shards, fabricLFU))
			})
		}
	}
}

func BenchmarkClock_ShardsScaling(b *testing.B) {
	for _, shards := range getShardScaling() {
		if shards == 1 {
			b.Run("NonSharded", func(b *testing.B) {
				runUltimateSuite(b, fabricClock)
			})
		} else {
			b.Run(fmt.Sprintf("Sharded-%d", shards), func(b *testing.B) {
				runUltimateSuite(b, withShards(shards, fabricClock))
			})
		}
	}
}

func BenchmarkClockRW_ShardsScaling(b *testing.B) {
	for _, shards := range getShardScaling() {
		if shards == 1 {
			b.Run("NonSharded", func(b *testing.B) {
				runUltimateSuite(b, fabricClockRW)
			})
		} else {
			b.Run(fmt.Sprintf("Sharded-%d", shards), func(b *testing.B) {
				runUltimateSuite(b, withShards(shards, fabricClockRW))
			})
		}
	}
}

func BenchmarkSIEVE_ShardsScaling(b *testing.B) {
	for _, shards := range getShardScaling() {
		if shards == 1 {
			b.Run("NonSharded", func(b *testing.B) {
				runUltimateSuite(b, fabricSieve)
			})
		} else {
			b.Run(fmt.Sprintf("Sharded-%d", shards), func(b *testing.B) {
				runUltimateSuite(b, withShards(shards, fabricSieve))
			})
		}
	}
}

func BenchmarkFull2Q_ShardsScaling(b *testing.B) {
	for _, shards := range getShardScaling() {
		if shards == 1 {
			b.Run("NonSharded", func(b *testing.B) {
				runUltimateSuite(b, fabricFull2Q)
			})
		} else {
			b.Run(fmt.Sprintf("Sharded-%d", shards), func(b *testing.B) {
				runUltimateSuite(b, withShards(shards, fabricFull2Q))
			})
		}
	}
}

func BenchmarkSLRU_ShardsScaling(b *testing.B) {
	for _, shards := range getShardScaling() {
		if shards == 1 {
			b.Run("NonSharded", func(b *testing.B) {
				runUltimateSuite(b, fabricSLRU)
			})
		} else {
			b.Run(fmt.Sprintf("Sharded-%d", shards), func(b *testing.B) {
				runUltimateSuite(b, withShards(shards, fabricSLRU))
			})
		}
	}
}

func BenchmarkWTinyLFU_ShardsScaling(b *testing.B) {
	for _, shards := range getShardScaling() {
		if shards == 1 {
			b.Run("NonSharded", func(b *testing.B) {
				runUltimateSuite(b, fabricWTinyLFU)
			})
		} else {
			b.Run(fmt.Sprintf("Sharded-%d", shards), func(b *testing.B) {
				runUltimateSuite(b, withShards(shards, fabricWTinyLFU))
			})
		}
	}
}

func BenchmarkLIRS_ShardsScaling(b *testing.B) {
	for _, shards := range getShardScaling() {
		if shards == 1 {
			b.Run("NonSharded", func(b *testing.B) {
				runUltimateSuite(b, fabricLIRS)
			})
		} else {
			b.Run(fmt.Sprintf("Sharded-%d", shards), func(b *testing.B) {
				runUltimateSuite(b, withShards(shards, fabricLIRS))
			})
		}
	}
}

func BenchmarkClockPRO_ShardsScaling(b *testing.B) {
	for _, shards := range getShardScaling() {
		if shards == 1 {
			b.Run("NonSharded", func(b *testing.B) {
				runUltimateSuite(b, fabricClockPro)
			})
		} else {
			b.Run(fmt.Sprintf("Sharded-%d", shards), func(b *testing.B) {
				runUltimateSuite(b, withShards(shards, fabricClockPro))
			})
		}
	}
}

func BenchmarkARC_ShardsScaling(b *testing.B) {
	for _, shards := range getShardScaling() {
		if shards == 1 {
			b.Run("NonSharded", func(b *testing.B) {
				runUltimateSuite(b, fabricARC)
			})
		} else {
			b.Run(fmt.Sprintf("Sharded-%d", shards), func(b *testing.B) {
				runUltimateSuite(b, withShards(shards, fabricARC))
			})
		}
	}
}

func BenchmarkS3FIFO_ShardsScaling(b *testing.B) {
	for _, shards := range getShardScaling() {
		if shards == 1 {
			b.Run("NonSharded", func(b *testing.B) {
				runUltimateSuite(b, fabricS3FIFO)
			})
		} else {
			b.Run(fmt.Sprintf("Sharded-%d", shards), func(b *testing.B) {
				runUltimateSuite(b, withShards(shards, fabricS3FIFO))
			})
		}
	}
}

func BenchmarkLRFU_ShardsScaling(b *testing.B) {
	for _, shards := range getShardScaling() {
		if shards == 1 {
			b.Run("NonSharded", func(b *testing.B) {
				runUltimateSuite(b, fabricLRFU)
			})
		} else {
			b.Run(fmt.Sprintf("Sharded-%d", shards), func(b *testing.B) {
				runUltimateSuite(b, withShards(shards, fabricLRFU))
			})
		}
	}
}

// Memory Test
func TestMemoryOverhead(t *testing.T) {
	const capacity = 100_000
	// Полезная нагрузка: key (uint64 = 8B) + val (uint64 = 8B) = 16 байт
	const payloadBytes = 16.0

	measureHeapBytes := func() uint64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}

	factories := getCacheFactories[uint64, uint64]()

	fmt.Printf("\n%-12s | %-12s | %-15s | %-18s\n", "Algorithm", "Total (MB)", "Bytes / Entry", "Overhead / Entry")
	fmt.Println("-------------------------------------------------------------------")

	for _, f := range factories {
		before := measureHeapBytes()
		cache := f.fabric(capacity)

		// Вставляем 2.5 * capacity элементов, чтобы заполнились и основной кэш и все теневые очереди (Ghost, B1/B2, A1out)
		for i := uint64(1); i <= capacity*2+capacity/2; i++ {
			cache.Put(i, i)
			if i%3 == 0 {
				cache.Get(i - 1)
			}
		}

		after := measureHeapBytes()
		runtime.KeepAlive(cache) // Удерживаем ссылку на cache до замера от GC

		diff := float64(after - before)
		bytesPerEntry := diff / float64(capacity)
		overhead := bytesPerEntry - payloadBytes
		totalMB := diff / (1024 * 1024)

		fmt.Printf("%-12s | %9.2f MB | %12.1f B | %15.1f B\n", f.name, totalMB, bytesPerEntry, overhead)
	}
}
