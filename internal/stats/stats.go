package stats

import (
	"fmt"
	"sync/atomic"
)

type Stats struct {
	hit   atomic.Uint64
	miss  atomic.Uint64
	del   atomic.Uint64
	upd   atomic.Uint64
	evict atomic.Uint64
	scans atomic.Uint64
}

type Reader interface {
	GetStatistic() string
	GetHit() uint64
	GetMiss() uint64
	GetDel() uint64
	GetUpd() uint64
	GetEvict() uint64
	GetScans() uint64
	HitRate() float64
	MissRate() float64
	AvgScans() float64
}

func (s *Stats) GetHit() uint64       { return s.hit.Load() }
func (s *Stats) GetMiss() uint64      { return s.miss.Load() }
func (s *Stats) GetDel() uint64       { return s.del.Load() }
func (s *Stats) GetUpd() uint64       { return s.upd.Load() }
func (s *Stats) GetEvict() uint64     { return s.evict.Load() }
func (s *Stats) GetScans() uint64     { return s.scans.Load() }
func (s *Stats) GetStatistic() string { return s.String() }

func (s *Stats) Hit()              { s.hit.Add(1) }
func (s *Stats) Miss()             { s.miss.Add(1) }
func (s *Stats) Del()              { s.del.Add(1) }
func (s *Stats) Upd()              { s.upd.Add(1) }
func (s *Stats) Evict()            { s.evict.Add(1) }
func (s *Stats) AddScans(n uint64) { s.scans.Add(n) }

func (s *Stats) String() string {
	hit, miss := s.hit.Load(), s.miss.Load()
	evict, scans := s.evict.Load(), s.scans.Load()
	return fmt.Sprintf(
		"hit=%d; miss=%d; del=%d; upd=%d; evict=%d; scans=%d; HitRate=%.2f; MissRate=%.2f; AvgScans=%.2f\n",
		hit, miss, s.del.Load(), s.upd.Load(), evict, scans,
		s.hitRate(hit, miss), s.missRate(hit, miss), s.avgScans(evict, scans),
	)
}

func (s *Stats) hitRate(hit, miss uint64) float64 {
	if total := hit + miss; total > 0 {
		return float64(hit) / float64(total)
	}
	return 0
}

func (s *Stats) HitRate() float64 {
	hit, miss := s.hit.Load(), s.miss.Load()
	return s.hitRate(hit, miss)
}

func (s *Stats) missRate(hit, miss uint64) float64 {
	if total := hit + miss; total > 0 {
		return float64(miss) / float64(total)
	}
	return 0
}

func (s *Stats) MissRate() float64 {
	hit, miss := s.hit.Load(), s.miss.Load()
	return s.missRate(hit, miss)
}

func (s *Stats) avgScans(evict, scans uint64) float64 {
	if evict > 0 {
		return float64(scans) / float64(evict)
	}
	return 0
}

func (s *Stats) AvgScans() float64 {
	evict, scans := s.evict.Load(), s.scans.Load()
	return s.avgScans(evict, scans)
}

func (s *Stats) Reset() {
	s.hit.Store(0)
	s.miss.Store(0)
	s.del.Store(0)
	s.upd.Store(0)
	s.evict.Store(0)
	s.scans.Store(0)
}
