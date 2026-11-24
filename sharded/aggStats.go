package sharded

// aggregatedStats суммирует метрики по всем шардам. Реализует интерфейс stats.Reader

import (
	"fmt"
)

func (s *ShardedCache[K, V]) GetStatistic() string {
	return s.aggStats.GetStatistic()
}

type aggregatedStats[K comparable, V any] struct {
	shards []paddedShard[K, V]
}

func (a *aggregatedStats[K, V]) GetHit() uint64 {
	var sum uint64
	for i := range a.shards {
		sum += a.shards[i].cache.Stats().GetHit()
	}
	return sum
}

func (a *aggregatedStats[K, V]) GetMiss() uint64 {
	var sum uint64
	for i := range a.shards {
		sum += a.shards[i].cache.Stats().GetMiss()
	}
	return sum
}

func (a *aggregatedStats[K, V]) GetDel() uint64 {
	var sum uint64
	for i := range a.shards {
		sum += a.shards[i].cache.Stats().GetDel()
	}
	return sum
}

func (a *aggregatedStats[K, V]) GetUpd() uint64 {
	var sum uint64
	for i := range a.shards {
		sum += a.shards[i].cache.Stats().GetUpd()
	}
	return sum
}

func (a *aggregatedStats[K, V]) GetEvict() uint64 {
	var sum uint64
	for i := range a.shards {
		sum += a.shards[i].cache.Stats().GetEvict()
	}
	return sum
}

func (a *aggregatedStats[K, V]) GetScans() uint64 {
	var sum uint64
	for i := range a.shards {
		sum += a.shards[i].cache.Stats().GetScans()
	}
	return sum
}

func (a *aggregatedStats[K, V]) HitRate() float64 {
	h := a.GetHit()
	total := h + a.GetMiss()
	if total == 0 {
		return 0
	}
	return float64(h) / float64(total)
}

func (a *aggregatedStats[K, V]) MissRate() float64 {
	m := a.GetMiss()
	total := a.GetHit() + m
	if total == 0 {
		return 0
	}
	return float64(m) / float64(total)
}

func (a *aggregatedStats[K, V]) AvgScans() float64 {
	e := a.GetEvict()
	if e == 0 {
		return 0
	}
	return float64(a.GetScans()) / float64(e)
}

func (a *aggregatedStats[K, V]) GetStatistic() string {
	return fmt.Sprintf(
		"hit=%d; miss=%d; del=%d; upd=%d; evict=%d; scans=%d; HitRate=%.2f; MissRate=%.2f; AvgScans=%.2f\n",
		a.GetHit(), a.GetMiss(), a.GetDel(), a.GetUpd(), a.GetEvict(), a.GetScans(),
		a.HitRate(), a.MissRate(), a.AvgScans(),
	)
}
