package api

import (
	"errors"
	"sync"
	"time"

	"financial-report/internal/model"
)

// AI 分析结果内存缓存（技术设计 §19.15）。
// 同一「股票 + reasoning 档位 + 年份范围」在 TTL 内只完整计算一次（含取数与 LLM 调用）；
// 并发同 key 请求只触发一次计算，等待者复用 leader 结果；失败不缓存。
const (
	// aiCacheTTL 固定 10 分钟（从写入时刻起算的绝对过期）：只用于挡住短时间内的
	// 重复点击 / 页面刷新（单次 LLM 调用数十秒且消耗 token），同时保证报表与行情
	// 变化在 10 分钟内自然反映，不跨交易日返回旧结果。
	aiCacheTTL = 10 * time.Minute
	aiCacheMax = 64 // 条目上限，满则先清过期、仍满整体清空

	// aiCacheSchema 结果结构版本：R24 起周期股历史锚改用全窗口 CAGR，旧缓存不再复用。
	aiCacheSchema = "r24"
)

// aiCacheKey 缓存 key：三个维度均会改变最终分析结果，必须全部参与。
// RangeSpecified 区分「未显式指定年份」（StartYear/EndYear=0，实际范围由「最新年报 - 4」
// 在计算时确定）与「显式指定 0/0」（rangeOK=true，随后按非法年份返回 400）——两者语义与
// 结果不同，若共用 key 会互相污染缓存。
type aiCacheKey struct {
	Schema         string // 结果结构版本（R24：Schema=r24）
	Code           string // 6 位股票代码
	Reasoning      string // low / medium / high（已归一化）
	StartYear      int
	EndYear        int
	RangeSpecified bool // 是否显式传入且可解析的年份范围
}

type aiCacheEntry struct {
	result    *model.AIAnalysisResult
	expiresAt time.Time
}

// aiFlight 同 key 在途请求；close(done) 后 result / err 只读。
type aiFlight struct {
	done   chan struct{}
	result *model.AIAnalysisResult
	err    error
}

type aiCacheOutcome int

const (
	aiCacheMiss      aiCacheOutcome = iota // 本请求执行了计算
	aiCacheHit                             // 命中未过期条目
	aiCacheCoalesced                       // 等待并复用了同 key 在途请求
)

type aiResultCache struct {
	mu       sync.Mutex
	entries  map[aiCacheKey]aiCacheEntry
	inflight map[aiCacheKey]*aiFlight
	now      func() time.Time // 默认 time.Now；测试注入固定时钟
}

func newAIResultCache() *aiResultCache {
	return &aiResultCache{
		entries:  make(map[aiCacheKey]aiCacheEntry),
		inflight: make(map[aiCacheKey]*aiFlight),
		now:      time.Now,
	}
}

// aiCacheKeyFor 只做参数映射，不做 IO。
func aiCacheKeyFor(code, reasoning string, startYear, endYear int, rangeSpecified bool) aiCacheKey {
	return aiCacheKey{
		Schema:         aiCacheSchema,
		Code:           code,
		Reasoning:      reasoning,
		StartYear:      startYear,
		EndYear:        endYear,
		RangeSpecified: rangeSpecified,
	}
}

// outcomeLabel 缓存结果类型的日志文案。
func outcomeLabel(outcome aiCacheOutcome) string {
	switch outcome {
	case aiCacheHit:
		return "命中"
	case aiCacheCoalesced:
		return "合并"
	default:
		return "未命中"
	}
}

// getOrCompute 是唯一读写入口：命中直接返回；未命中由本请求计算并写缓存；
// 同 key 已有在途请求时等待并复用它，避免重复 LLM 调用。
func (c *aiResultCache) getOrCompute(
	key aiCacheKey,
	compute func() (*model.AIAnalysisResult, error),
) (*model.AIAnalysisResult, aiCacheOutcome, error) {
	c.mu.Lock()
	now := c.now()
	if e, ok := c.entries[key]; ok {
		if now.Before(e.expiresAt) {
			c.mu.Unlock()
			return e.result, aiCacheHit, nil
		}
		delete(c.entries, key) // 过期即删，不主动全表扫描
	}
	if f, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		<-f.done // 等待 leader；done 关闭后 result / err 只读
		return f.result, aiCacheCoalesced, f.err
	}
	f := &aiFlight{done: make(chan struct{})}
	c.inflight[key] = f
	c.mu.Unlock()

	res, err := c.run(key, f, compute)
	return res, aiCacheMiss, err
}

func (c *aiResultCache) run(
	key aiCacheKey,
	f *aiFlight,
	compute func() (*model.AIAnalysisResult, error),
) (res *model.AIAnalysisResult, err error) {
	finished := false
	defer func() {
		if finished {
			return
		}
		// compute panic：先释放等待者，避免同 key 请求永久阻塞；panic 继续上抛，
		// 由 gin.Default() 的 Recovery 中间件处理。此路径不写缓存。
		c.finish(key, f, nil, errors.New("AI 分析执行异常"))
	}()

	res, err = compute()
	finished = true
	c.finish(key, f, res, err)
	return res, err
}

// finish 负责：成功才写 entries；无论成败都删除 inflight 并唤醒等待者。
func (c *aiResultCache) finish(key aiCacheKey, f *aiFlight, res *model.AIAnalysisResult, err error) {
	c.mu.Lock()
	if err == nil && res != nil {
		c.storeLocked(key, res)
	}
	delete(c.inflight, key)
	f.result, f.err = res, err
	close(f.done) // 在持锁期间关闭，保证等待者能看到已写入的 result / err
	c.mu.Unlock()
}

func (c *aiResultCache) storeLocked(key aiCacheKey, res *model.AIAnalysisResult) {
	now := c.now()
	if len(c.entries) >= aiCacheMax {
		// 先清过期，再决定是否整体清空（§19.15.7）
		for k, e := range c.entries {
			if !now.Before(e.expiresAt) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= aiCacheMax {
			c.entries = make(map[aiCacheKey]aiCacheEntry, aiCacheMax)
		}
	}
	c.entries[key] = aiCacheEntry{result: res, expiresAt: now.Add(aiCacheTTL)}
}
