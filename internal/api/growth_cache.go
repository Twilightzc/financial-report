package api

import (
	"log"
	"sync"
	"time"

	"financial-report/internal/model"
)

// R18：外部增长快照的 24h 进程内缓存（一致预期 / GDP / 分红）。
// 放在 api 层而不是 client 的 15min 报表缓存：需要单飞并发去重、缓存解析后的快照、失败不缓存。
const (
	growthCacheTTL = 24 * time.Hour
	growthCacheMax = 256
)

// growthSnapshotCache 三个独立键空间的 TTL 缓存。GDP 为全局键，可跨股票复用。
type growthSnapshotCache struct {
	consensus *ttlCache[model.ConsensusForecast]
	gdp       *ttlCache[model.GDPAnchor]
	dividend  *ttlCache[model.DividendSnapshot]
}

func newGrowthSnapshotCache() *growthSnapshotCache {
	return &growthSnapshotCache{
		consensus: newTTLCache[model.ConsensusForecast](growthCacheTTL, growthCacheMax),
		gdp:       newTTLCache[model.GDPAnchor](growthCacheTTL, growthCacheMax),
		dividend:  newTTLCache[model.DividendSnapshot](growthCacheTTL, growthCacheMax),
	}
}

type ttlEntry[T any] struct {
	value     T
	expiresAt time.Time
}

// ttlFlight 同键在途请求；close(done) 后 value / err 只读。
type ttlFlight[T any] struct {
	done  chan struct{}
	value T
	err   error
}

// ttlCache 通用 TTL + 单飞缓存：同键并发只执行一次 fn，失败不缓存。
type ttlCache[T any] struct {
	mu      sync.Mutex
	ttl     time.Duration
	max     int
	entries map[string]ttlEntry[T]
	flights map[string]*ttlFlight[T]
	now     func() time.Time // 默认 time.Now；测试可注入固定时钟
}

func newTTLCache[T any](ttl time.Duration, max int) *ttlCache[T] {
	return &ttlCache[T]{
		ttl:     ttl,
		max:     max,
		entries: make(map[string]ttlEntry[T]),
		flights: make(map[string]*ttlFlight[T]),
		now:     time.Now,
	}
}

// getOrCompute 命中未过期条目直接返回；否则由本请求执行 fn 并写缓存；
// 同键已有在途请求时等待并复用其结果（成功或失败）。
func (c *ttlCache[T]) getOrCompute(key string, fn func() (T, error)) (T, error) {
	c.mu.Lock()
	now := c.now()
	if e, ok := c.entries[key]; ok {
		if now.Before(e.expiresAt) {
			c.mu.Unlock()
			return e.value, nil
		}
		delete(c.entries, key)
	}
	if f, ok := c.flights[key]; ok {
		c.mu.Unlock()
		<-f.done
		return f.value, f.err
	}
	f := &ttlFlight[T]{done: make(chan struct{})}
	c.flights[key] = f
	c.mu.Unlock()

	value, err := c.run(key, f, fn)
	return value, err
}

func (c *ttlCache[T]) run(key string, f *ttlFlight[T], fn func() (T, error)) (value T, err error) {
	finished := false
	defer func() {
		if finished {
			return
		}
		// fn panic：先唤醒等待者，避免同键请求永久阻塞；panic 继续上抛。
		var zero T
		c.finish(key, f, zero, errPanic)
	}()
	value, err = fn()
	finished = true
	c.finish(key, f, value, err)
	return value, err
}

// finish 成功才写缓存；无论成败都删除在途并唤醒等待者。
func (c *ttlCache[T]) finish(key string, f *ttlFlight[T], value T, err error) {
	c.mu.Lock()
	if err == nil {
		c.storeLocked(key, value)
	}
	delete(c.flights, key)
	f.value, f.err = value, err
	close(f.done)
	c.mu.Unlock()
}

func (c *ttlCache[T]) storeLocked(key string, value T) {
	now := c.now()
	if len(c.entries) >= c.max {
		for k, e := range c.entries { // 先删过期项
			if !now.Before(e.expiresAt) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= c.max { // 仍满则删最早到期的一项
			earliestKey, earliest := "", time.Time{}
			for k, e := range c.entries {
				if earliestKey == "" || e.expiresAt.Before(earliest) {
					earliestKey, earliest = k, e.expiresAt
				}
			}
			delete(c.entries, earliestKey)
		}
	}
	c.entries[key] = ttlEntry[T]{value: value, expiresAt: now.Add(c.ttl)}
}

// errPanic 单飞计算 panic 时返回给等待者的错误（panic 本身继续上抛给 Recovery）。
var errPanic = ttlPanicError{}

type ttlPanicError struct{}

func (ttlPanicError) Error() string { return "增长快照执行异常" }

// fetchGrowthSnapshot 取本次 AI 分析使用的外部增长快照（三路并发，任一失败只降级不阻塞）。
// h.growth 为 nil（测试直接构造 Handler{}）时跳过缓存直接取数，不 panic。
func (h *Handler) fetchGrowthSnapshot(code string) model.GrowthSnapshot {
	return h.fetchGrowthSnapshotWith(code,
		h.c.FetchConsensusForecast, h.c.FetchGDPAnchor, h.c.FetchDividendSnapshot)
}

// fetchGrowthSnapshotWith 便于单测注入取数函数：三个 fetch 并发执行并汇合为快照。
func (h *Handler) fetchGrowthSnapshotWith(code string,
	fetchConsensus func(string) (model.ConsensusForecast, error),
	fetchGDP func() (model.GDPAnchor, error),
	fetchDividend func(string) (model.DividendSnapshot, error)) model.GrowthSnapshot {
	snap := model.GrowthSnapshot{RequestDate: time.Now().Format("2006-01-02")}

	consensusFn := func() (model.ConsensusForecast, error) { return fetchConsensus(code) }
	gdpFn := func() (model.GDPAnchor, error) { return fetchGDP() }
	dividendFn := func() (model.DividendSnapshot, error) { return fetchDividend(code) }

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		var (
			v   model.ConsensusForecast
			err error
		)
		if h.growth != nil {
			v, err = h.growth.consensus.getOrCompute("consensus|"+code, consensusFn)
		} else {
			v, err = consensusFn()
		}
		if err != nil {
			log.Printf("[growth] 一致预期取数失败 code=%s: %v", code, err)
			return
		}
		snap.Consensus = v
	}()
	go func() {
		defer wg.Done()
		var (
			v   model.GDPAnchor
			err error
		)
		if h.growth != nil {
			v, err = h.growth.gdp.getOrCompute("gdp|CN", gdpFn)
		} else {
			v, err = gdpFn()
		}
		if err != nil {
			log.Printf("[growth] 宏观 GDP 取数失败: %v", err)
			return
		}
		snap.GDP = v
	}()
	go func() {
		defer wg.Done()
		var (
			v   model.DividendSnapshot
			err error
		)
		if h.growth != nil {
			v, err = h.growth.dividend.getOrCompute("dividend|"+code, dividendFn)
		} else {
			v, err = dividendFn()
		}
		if err != nil {
			log.Printf("[growth] 分红取数失败 code=%s: %v", code, err)
			return
		}
		snap.Dividends = v
	}()
	wg.Wait()
	return snap
}
