package api

// R18 外部增长快照缓存（24h TTL + 单飞）与快照汇合测试。

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"financial-report/internal/model"
)

func TestTTLCacheHitAndExpiry(t *testing.T) {
	now := time.Unix(0, 0)
	c := newTTLCache[int](time.Hour, 8)
	c.now = func() time.Time { return now }

	calls := 0
	fn := func() (int, error) { calls++; return calls, nil }

	if v, err := c.getOrCompute("k", fn); err != nil || v != 1 || calls != 1 {
		t.Fatalf("首次调用 = (%v,%v) calls=%d，期望 1", v, err, calls)
	}
	if v, err := c.getOrCompute("k", fn); err != nil || v != 1 || calls != 1 {
		t.Fatalf("TTL 内应命中缓存 = (%v,%v) calls=%d", v, err, calls)
	}
	now = now.Add(2 * time.Hour)
	if v, _ := c.getOrCompute("k", fn); v != 2 || calls != 2 {
		t.Fatalf("过期后应重算 = %v calls=%d", v, calls)
	}
}

func TestTTLCacheFailureNotCached(t *testing.T) {
	c := newTTLCache[int](time.Hour, 8)
	calls := 0
	fn := func() (int, error) {
		calls++
		if calls == 1 {
			return 0, errors.New("boom")
		}
		return 42, nil
	}
	if _, err := c.getOrCompute("k", fn); err == nil {
		t.Fatal("首次应返回错误")
	}
	if v, err := c.getOrCompute("k", fn); err != nil || v != 42 || calls != 2 {
		t.Fatalf("失败不应缓存：v=%v err=%v calls=%d", v, err, calls)
	}
}

func TestTTLCacheSingleFlight(t *testing.T) {
	c := newTTLCache[int](time.Hour, 8)
	var calls int32
	fn := func() (int, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(20 * time.Millisecond)
		return 7, nil
	}

	const n = 10
	var wg sync.WaitGroup
	results := make([]int, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = c.getOrCompute("same", fn)
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("同键并发应只执行一次，实际 %d 次", got)
	}
	for i := range results {
		if errs[i] != nil || results[i] != 7 {
			t.Errorf("第 %d 个等待者 = (%v,%v)", i, results[i], errs[i])
		}
	}
}

func TestTTLCacheEviction(t *testing.T) {
	c := newTTLCache[int](time.Hour, 2)
	for _, k := range []string{"a", "b", "c"} {
		k := k
		if _, err := c.getOrCompute(k, func() (int, error) { return 1, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.entries) > 2 {
		t.Errorf("容量上限 2，实际 %d 项", len(c.entries))
	}
}

func TestFetchGrowthSnapshotWith(t *testing.T) {
	h := &Handler{} // growth=nil：跳过缓存直接取数
	var consensusCalls, gdpCalls, dividendCalls int32

	snap := h.fetchGrowthSnapshotWith("600519",
		func(code string) (model.ConsensusForecast, error) {
			atomic.AddInt32(&consensusCalls, 1)
			return model.ConsensusForecast{Source: "profit_forecast", Years: []model.ConsensusYear{{Year: 2024, Mark: "A"}}}, nil
		},
		func() (model.GDPAnchor, error) {
			atomic.AddInt32(&gdpCalls, 1)
			return model.GDPAnchor{Available: true, Quarters: []model.GDPQuarter{{Year: 2024, Quarter: 4}}}, nil
		},
		func(code string) (model.DividendSnapshot, error) {
			atomic.AddInt32(&dividendCalls, 1)
			return model.DividendSnapshot{Available: true}, nil
		})

	if snap.RequestDate != time.Now().Format("2006-01-02") {
		t.Errorf("request_date = %q", snap.RequestDate)
	}
	if snap.Consensus.Source != "profit_forecast" || !snap.GDP.Available || !snap.Dividends.Available {
		t.Errorf("快照未正确汇合：%+v", snap)
	}
	if consensusCalls != 1 || gdpCalls != 1 || dividendCalls != 1 {
		t.Errorf("三路取数次数 = (%d,%d,%d)", consensusCalls, gdpCalls, dividendCalls)
	}

	// 取数失败只降级为空值，不阻塞其它两路。
	snap = h.fetchGrowthSnapshotWith("600519",
		func(string) (model.ConsensusForecast, error) { return model.ConsensusForecast{}, errors.New("x") },
		func() (model.GDPAnchor, error) { return model.GDPAnchor{Available: true}, nil },
		func(string) (model.DividendSnapshot, error) { return model.DividendSnapshot{}, errors.New("y") })
	if snap.Consensus.Source != "" || snap.Dividends.Available || !snap.GDP.Available {
		t.Errorf("失败降级不正确：%+v", snap)
	}
}

func TestFetchGrowthSnapshotUsesCache(t *testing.T) {
	h := &Handler{growth: newGrowthSnapshotCache()}
	var consensusCalls, gdpCalls, dividendCalls int32
	fetch := func() {
		h.fetchGrowthSnapshotWith("600519",
			func(string) (model.ConsensusForecast, error) {
				atomic.AddInt32(&consensusCalls, 1)
				return model.ConsensusForecast{Source: "profit_forecast"}, nil
			},
			func() (model.GDPAnchor, error) {
				atomic.AddInt32(&gdpCalls, 1)
				return model.GDPAnchor{Available: true}, nil
			},
			func(string) (model.DividendSnapshot, error) {
				atomic.AddInt32(&dividendCalls, 1)
				return model.DividendSnapshot{Available: true}, nil
			})
	}
	fetch()
	fetch()
	if consensusCalls != 1 || gdpCalls != 1 || dividendCalls != 1 {
		t.Errorf("24h 缓存内重复调用应只取数一次，实际 (%d,%d,%d)", consensusCalls, gdpCalls, dividendCalls)
	}
}
