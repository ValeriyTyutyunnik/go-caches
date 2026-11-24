package stats

import (
	"math"
	"sync"
	"testing"
)

func roundTo(val float64, precision int) float64 {
	ratio := math.Pow(10, float64(precision))
	return math.Round(val*ratio) / ratio
}

func TestStats_Empty(t *testing.T) {
	var s Stats

	if got := s.HitRate(); got != 0 {
		t.Errorf("ожидался HitRate = 0 для пустой статистики, получено %v", got)
	}
	if got := s.MissRate(); got != 0 {
		t.Errorf("ожидался MissRate = 0 для пустой статистики, получено %v", got)
	}
	if got := s.AvgScans(); got != 0 {
		t.Errorf("ожидался AvgScans = 0 для пустой статистики, получено %v", got)
	}

	expectedStr := "hit=0; miss=0; del=0; upd=0; evict=0; scans=0; HitRate=0.00; MissRate=0.00; AvgScans=0.00\n"
	if got := s.String(); got != expectedStr {
		t.Errorf("неверный вывод String():ожидалось: %q; получено:  %q", expectedStr, got)
	}
}

func TestStats_CountersAndRates(t *testing.T) {
	var s Stats

	// 2 попадания, 1 промах -> HitRate ≈ 0.67, MissRate ≈ 0.33
	s.Hit()
	s.Hit()
	s.Miss()
	s.Del()
	s.Del()
	s.Upd()

	// 3 вытеснения за 7 шагов -> AvgScans ≈ 2.33
	s.Evict()
	s.AddScans(2)
	s.Evict()
	s.AddScans(1)
	s.Evict()
	s.AddScans(4)

	if got := roundTo(s.HitRate(), 2); got != 0.67 {
		t.Errorf("ожидался HitRate = 0.67, получено %v", got)
	}
	if got := roundTo(s.MissRate(), 2); got != 0.33 {
		t.Errorf("ожидался MissRate = 0.33, получено %v", got)
	}
	if got := roundTo(s.AvgScans(), 2); got != 2.33 {
		t.Errorf("ожидался AvgScans = 2.33, получено %v", got)
	}

	expectedStr := "hit=2; miss=1; del=2; upd=1; evict=3; scans=7; HitRate=0.67; MissRate=0.33; AvgScans=2.33\n"
	if got := s.String(); got != expectedStr {
		t.Errorf("неверный вывод String():ожидалось: %q; получено:  %q", expectedStr, got)
	}
}

func TestStats_Reset(t *testing.T) {
	var s Stats

	s.Hit()
	s.Miss()
	s.Del()
	s.Upd()
	s.Evict()
	s.AddScans(5)

	s.Reset()

	if s.HitRate() != 0 || s.MissRate() != 0 || s.AvgScans() != 0 {
		t.Errorf("после Reset() HitRate, MissRate и AvgScans должны быть равны 0")
	}

	expectedStr := "hit=0; miss=0; del=0; upd=0; evict=0; scans=0; HitRate=0.00; MissRate=0.00; AvgScans=0.00\n"
	if got := s.String(); got != expectedStr {
		t.Errorf("после Reset() ожидалось %q, получено %q", expectedStr, got)
	}
}

func TestStats_Concurrency(t *testing.T) {
	var s Stats
	const goroutines = 50
	const iterations = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				s.Hit()
				s.Miss()
				s.Del()
				s.Upd()
				s.Evict()
				s.AddScans(3)
				_ = s.HitRate()
				_ = s.MissRate()
				_ = s.AvgScans()
				_ = s.String()
			}
		}()
	}

	wg.Wait()

	if got := roundTo(s.HitRate(), 2); got != 0.50 {
		t.Errorf("ожидался HitRate = 0.50, получено %v", got)
	}
	if got := roundTo(s.MissRate(), 2); got != 0.50 {
		t.Errorf("ожидался MissRate = 0.50, получено %v", got)
	}
	if got := roundTo(s.AvgScans(), 2); got != 3.00 {
		t.Errorf("ожидался AvgScans = 3.00, получено %v", got)
	}

	expectedStr := "hit=50000; miss=50000; del=50000; upd=50000; evict=50000; scans=150000; HitRate=0.50; MissRate=0.50; AvgScans=3.00\n"
	if got := s.String(); got != expectedStr {
		t.Errorf("неверный итог после конкурентной работы: ожидалось: %q; получено: %q", expectedStr, got)
	}
}
