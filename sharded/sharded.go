package sharded

import (
	"hash/maphash"
	"math/bits"

	"github.com/ValeriyTyutyunnik/go-caches/internal/stats"
)

type Cache[K comparable, V any] interface {
	Get(key K) (V, bool)
	Put(key K, value V)
	DeleteKey(key K) bool
	Len() int
	GetStatistic() string
	Stats() stats.Reader
}

// защита указателя на шард от False Sharing между кэш-линиями ядер процессора
type paddedShard[K comparable, V any] struct {
	cache Cache[K, V] // 8 байт интерфейс
	_     [56]byte    // дополняем до 64 байт
}

type ShardedCache[K comparable, V any] struct {
	shards    []paddedShard[K, V]
	seed      maphash.Seed
	shardMask uint64
	aggStats  aggregatedStats[K, V]
}

// Конструктор шардированного кэша
// totalCapacity — общая ёмкость всего кэша.
// shardsCount — число шардов - округляется вверх до степени двойки, но меньше totalCapacity
// shardFactory — конструктор кэша для шарда
func NewSharded[K comparable, V any](
	totalCapacity int,
	shardsCount int,
	shardFactory func(shardCapacity int) Cache[K, V],
) *ShardedCache[K, V] {
	if totalCapacity <= 0 {
		panic("общая емкость должна быть > 0")
	}

	if shardsCount <= 0 {
		shardsCount = 16
	}

	numShards := nextPowerOfTwo(shardsCount)
	// шардов должно быть <= capacity
	if numShards > totalCapacity {
		numShards = prevPowerOfTwo(totalCapacity)
	}

	baseCap := totalCapacity / numShards
	remainder := totalCapacity % numShards

	sc := &ShardedCache[K, V]{
		shards:    make([]paddedShard[K, V], numShards),
		seed:      maphash.MakeSeed(),
		shardMask: uint64(numShards - 1),
	}
	sc.aggStats.shards = sc.shards

	for i := 0; i < numShards; i++ {
		shardCap := baseCap
		// Распределяем остаток от capacity по первым шардам
		if i < remainder {
			shardCap++
		}
		sc.shards[i].cache = shardFactory(shardCap)
	}

	return sc
}

func NewShardedExt[K comparable, V any](
	totalCapacity int,
	shardsCount int,
	shardFactory func(shardCapacity, totalCapacity, shardsCount int) Cache[K, V],
) *ShardedCache[K, V] {
	if totalCapacity <= 0 {
		panic("общая емкость должна быть > 0")
	}

	if shardsCount <= 0 {
		shardsCount = 16
	}

	numShards := nextPowerOfTwo(shardsCount)
	// шардов должно быть <= capacity
	if numShards > totalCapacity {
		numShards = prevPowerOfTwo(totalCapacity)
	}

	baseCap := totalCapacity / numShards
	remainder := totalCapacity % numShards

	sc := &ShardedCache[K, V]{
		shards:    make([]paddedShard[K, V], numShards),
		seed:      maphash.MakeSeed(),
		shardMask: uint64(numShards - 1),
	}
	sc.aggStats.shards = sc.shards

	for i := 0; i < numShards; i++ {
		shardCap := baseCap
		// Распределяем остаток от capacity по первым шардам
		if i < remainder {
			shardCap++
		}
		sc.shards[i].cache = shardFactory(shardCap, totalCapacity, shardsCount)
	}

	return sc
}

func (s *ShardedCache[K, V]) getShard(key K) Cache[K, V] {
	h := maphash.Comparable(s.seed, key)
	return s.shards[h&s.shardMask].cache
}

func (s *ShardedCache[K, V]) Get(key K) (V, bool) {
	return s.getShard(key).Get(key)
}

func (s *ShardedCache[K, V]) Put(key K, value V) {
	s.getShard(key).Put(key, value)
}

func (s *ShardedCache[K, V]) DeleteKey(key K) bool {
	return s.getShard(key).DeleteKey(key)
}

func (s *ShardedCache[K, V]) Len() int {
	total := 0
	for i := range s.shards {
		total += s.shards[i].cache.Len()
	}
	return total
}

func (s *ShardedCache[K, V]) Stats() stats.Reader {
	return &s.aggStats
}

func nextPowerOfTwo(n int) int {
	if n <= 1 {
		return 1
	}
	return 1 << bits.Len(uint(n-1))
}

func prevPowerOfTwo(n int) int {
	if n <= 1 {
		return 1
	}
	return 1 << (bits.Len(uint(n)) - 1)
}
