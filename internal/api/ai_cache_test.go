package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"financial-report/internal/client"
	"financial-report/internal/model"
)

func testAIResult(conclusion string) *model.AIAnalysisResult {
	return &model.AIAnalysisResult{Conclusion: conclusion}
}

// waitCh 等待测试信号，超时即失败（避免并发用例挂死）。
func waitCh(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("等待 %s 超时", what)
	}
}

// C1 命中：预置条目 → aiCacheHit，compute 不执行，返回同一结果对象。
func TestAICacheHitSkipsCompute(t *testing.T) {
	c := newAIResultCache()
	key := aiCacheKeyFor("600519", "low", 0, 0, false)
	want := testAIResult("预置结论")
	c.entries[key] = aiCacheEntry{result: want, expiresAt: c.now().Add(time.Hour)}

	calls := 0
	got, outcome, err := c.getOrCompute(key, func() (*model.AIAnalysisResult, error) {
		calls++
		return testAIResult("不应执行"), nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if outcome != aiCacheHit {
		t.Errorf("outcome = %v, want aiCacheHit", outcome)
	}
	if calls != 0 {
		t.Errorf("compute 调用 %d 次, want 0", calls)
	}
	if got != want {
		t.Errorf("返回结果不是缓存对象")
	}
}

// C2 未命中后命中：首次 miss 计算一次，第二次 hit 不再计算。
func TestAICacheMissThenHit(t *testing.T) {
	c := newAIResultCache()
	key := aiCacheKeyFor("600519", "low", 2020, 2024, true)
	want := testAIResult("首次结果")

	calls := 0
	compute := func() (*model.AIAnalysisResult, error) {
		calls++
		return want, nil
	}

	got, outcome, err := c.getOrCompute(key, compute)
	if err != nil || outcome != aiCacheMiss || got != want {
		t.Fatalf("首次调用 = (%v, %v, %v), want (want, aiCacheMiss, nil)", got, outcome, err)
	}
	got, outcome, err = c.getOrCompute(key, compute)
	if err != nil || outcome != aiCacheHit || got != want {
		t.Fatalf("第二次调用 = (%v, %v, %v), want (want, aiCacheHit, nil)", got, outcome, err)
	}
	if calls != 1 {
		t.Errorf("compute 调用 %d 次, want 1", calls)
	}
}

// C3 过期：推进 aiCacheTTL+1s 后重新 miss，旧条目被替换。
func TestAICacheExpires(t *testing.T) {
	c := newAIResultCache()
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	key := aiCacheKeyFor("600519", "low", 0, 0, false)
	first := testAIResult("第一版")
	second := testAIResult("第二版")
	calls := 0
	compute := func() (*model.AIAnalysisResult, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return second, nil
	}

	if _, outcome, _ := c.getOrCompute(key, compute); outcome != aiCacheMiss {
		t.Fatalf("首次 outcome = %v, want aiCacheMiss", outcome)
	}
	now = now.Add(aiCacheTTL + time.Second)
	got, outcome, err := c.getOrCompute(key, compute)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if outcome != aiCacheMiss {
		t.Errorf("过期后 outcome = %v, want aiCacheMiss", outcome)
	}
	if calls != 2 {
		t.Errorf("compute 调用 %d 次, want 2", calls)
	}
	if got != second {
		t.Errorf("过期后返回旧结果，want 新结果")
	}
	if e := c.entries[key]; e.result != second {
		t.Errorf("过期条目未被新结果替换")
	}
}

// C4 并发去重：leader 计算期间同 key 请求等待并复用，compute 只执行一次。
// 通过注入 now 在 waiter 查询缓存时发信号，确定性保证 waiter 先于 leader 完成进入等待。
func TestAICacheCoalescesConcurrent(t *testing.T) {
	c := newAIResultCache()
	key := aiCacheKeyFor("600519", "low", 0, 0, false)
	waiterEntered := make(chan struct{})
	var clockCalls atomic.Int32
	c.now = func() time.Time {
		if clockCalls.Add(1) == 2 { // 第 1 次为 leader 查询，第 2 次为 waiter 查询
			close(waiterEntered)
		}
		return time.Unix(0, 0)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	want := testAIResult("leader 结果")
	var calls atomic.Int32

	type callOut struct {
		res     *model.AIAnalysisResult
		outcome aiCacheOutcome
		err     error
	}
	leaderOut := make(chan callOut, 1)
	go func() {
		res, outcome, err := c.getOrCompute(key, func() (*model.AIAnalysisResult, error) {
			calls.Add(1)
			close(entered)
			<-release
			return want, nil
		})
		leaderOut <- callOut{res, outcome, err}
	}()
	waitCh(t, entered, "leader 进入 compute")

	waiterOut := make(chan callOut, 1)
	go func() {
		res, outcome, err := c.getOrCompute(key, func() (*model.AIAnalysisResult, error) {
			calls.Add(1)
			return testAIResult("waiter 不应计算"), nil
		})
		waiterOut <- callOut{res, outcome, err}
	}()
	waitCh(t, waiterEntered, "waiter 进入缓存查询")
	close(release)

	lo := <-leaderOut
	wo := <-waiterOut
	if lo.outcome != aiCacheMiss {
		t.Errorf("leader outcome = %v, want aiCacheMiss", lo.outcome)
	}
	if wo.outcome != aiCacheCoalesced {
		t.Errorf("waiter outcome = %v, want aiCacheCoalesced", wo.outcome)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("compute 调用 %d 次, want 1", got)
	}
	if lo.res != want || wo.res != want {
		t.Errorf("leader / waiter 结果不一致")
	}
	if lo.err != nil || wo.err != nil {
		t.Errorf("err = (%v, %v), want nil", lo.err, wo.err)
	}
}

// C5 不同 key 并行：两个 key 的 compute 可同时阻塞，互不串行。
func TestAICacheDifferentKeysParallel(t *testing.T) {
	c := newAIResultCache()
	keys := []aiCacheKey{
		aiCacheKeyFor("600519", "low", 0, 0, false),
		aiCacheKeyFor("000001", "high", 0, 0, false),
	}
	entered := []chan struct{}{make(chan struct{}), make(chan struct{})}
	release := make(chan struct{})

	type callOut struct {
		res     *model.AIAnalysisResult
		outcome aiCacheOutcome
		err     error
	}
	outs := make([]chan callOut, len(keys))
	for i := range keys {
		i := i
		outs[i] = make(chan callOut, 1)
		go func() {
			want := testAIResult(fmt.Sprintf("结果%d", i))
			res, outcome, err := c.getOrCompute(keys[i], func() (*model.AIAnalysisResult, error) {
				close(entered[i])
				<-release
				return want, nil
			})
			outs[i] <- callOut{res, outcome, err}
		}()
	}
	// 两个 compute 同时处于阻塞态，说明不同 key 未被全局锁串行化。
	waitCh(t, entered[0], "key0 进入 compute")
	waitCh(t, entered[1], "key1 进入 compute")
	close(release)

	for i := range keys {
		o := <-outs[i]
		if o.err != nil || o.outcome != aiCacheMiss {
			t.Errorf("key%d = (outcome=%v, err=%v), want (aiCacheMiss, nil)", i, o.outcome, o.err)
		}
	}
}

// C6 失败不缓存：compute 返回 error 不写 entries，下一次仍 miss 并重试。
func TestAICacheFailureNotCached(t *testing.T) {
	c := newAIResultCache()
	key := aiCacheKeyFor("600519", "low", 0, 0, false)
	calls := 0
	compute := func() (*model.AIAnalysisResult, error) {
		calls++
		return nil, errors.New("上游失败")
	}

	for i := 0; i < 2; i++ {
		_, outcome, err := c.getOrCompute(key, compute)
		if err == nil {
			t.Fatalf("第 %d 次 err = nil, want 上游错误", i+1)
		}
		if outcome != aiCacheMiss {
			t.Errorf("第 %d 次 outcome = %v, want aiCacheMiss", i+1, outcome)
		}
	}
	if calls != 2 {
		t.Errorf("compute 调用 %d 次, want 2", calls)
	}
	if len(c.entries) != 0 {
		t.Errorf("失败结果被写入缓存, entries = %d", len(c.entries))
	}
	if len(c.inflight) != 0 {
		t.Errorf("inflight 未清空, 剩余 %d", len(c.inflight))
	}
}

// C7 容量淘汰：写入 aiCacheMax+1 个 key 后条目不超过上限，最新 key 可命中。
func TestAICacheCapacityEviction(t *testing.T) {
	c := newAIResultCache()
	var lastKey aiCacheKey
	lastCalls := 0
	for i := 0; i <= aiCacheMax; i++ {
		key := aiCacheKeyFor(fmt.Sprintf("%06d", i), "low", 0, 0, false)
		want := testAIResult(fmt.Sprintf("结果%d", i))
		if _, outcome, err := c.getOrCompute(key, func() (*model.AIAnalysisResult, error) {
			return want, nil
		}); err != nil || outcome != aiCacheMiss {
			t.Fatalf("写入 key%d = (outcome=%v, err=%v), want (aiCacheMiss, nil)", i, outcome, err)
		}
		lastKey = key
	}
	if n := len(c.entries); n > aiCacheMax {
		t.Errorf("entries = %d, want <= %d", n, aiCacheMax)
	}
	if _, outcome, err := c.getOrCompute(lastKey, func() (*model.AIAnalysisResult, error) {
		lastCalls++
		return nil, nil
	}); err != nil || outcome != aiCacheHit {
		t.Errorf("最新 key = (outcome=%v, err=%v), want (aiCacheHit, nil)", outcome, err)
	}
	if lastCalls != 0 {
		t.Errorf("最新 key 命中时仍执行 compute")
	}
}

// C8 key 维度：reasoning / 年份任一不同即不同 key；未指定年份归一化为 0/0；
// 显式 0/0（RangeSpecified=true）与未指定（false）必须区分。
func TestAICacheKeyDimensions(t *testing.T) {
	base := aiCacheKeyFor("600519", "low", 0, 0, false)
	if base.StartYear != 0 || base.EndYear != 0 {
		t.Errorf("未指定年份 key = %+v, want StartYear=0 EndYear=0", base)
	}
	if base.RangeSpecified {
		t.Errorf("未指定年份 RangeSpecified = true, want false")
	}
	if aiCacheKeyFor("600519", "medium", 0, 0, false) == base {
		t.Errorf("不同 reasoning 生成了相同 key")
	}
	if aiCacheKeyFor("600519", "low", 2020, 2024, true) == base {
		t.Errorf("不同年份范围生成了相同 key")
	}
	if aiCacheKeyFor("000001", "low", 0, 0, false) == base {
		t.Errorf("不同代码生成了相同 key")
	}
	if aiCacheKeyFor("600519", "low", 0, 0, true) == base {
		t.Errorf("显式 0/0 与未指定年份生成了相同 key")
	}
}

// C8b 显式 0/0 与未指定年份互不影响：写入其中一个 key 不会命中另一个。
// 显式 0/0 在计算路径中返回 400（不缓存），未指定年份走「最新年报 - 4」默认（可缓存）。
func TestAICacheExplicitZeroRangeIsolated(t *testing.T) {
	c := newAIResultCache()
	defaultKey := aiCacheKeyFor("600519", "low", 0, 0, false)
	explicitKey := aiCacheKeyFor("600519", "low", 0, 0, true)

	// 预置默认 key，显式 0/0 必须仍为 miss（不被误命中）。
	wantDefault := testAIResult("默认年份结果")
	c.entries[defaultKey] = aiCacheEntry{result: wantDefault, expiresAt: c.now().Add(time.Hour)}

	explicitCalls := 0
	_, outcome, err := c.getOrCompute(explicitKey, func() (*model.AIAnalysisResult, error) {
		explicitCalls++
		return nil, errInvalidYearRange
	})
	if outcome != aiCacheMiss {
		t.Errorf("显式 0/0 outcome = %v, want aiCacheMiss（不得命中默认 key）", outcome)
	}
	if !errors.Is(err, errInvalidYearRange) {
		t.Errorf("显式 0/0 err = %v, want errInvalidYearRange", err)
	}
	if explicitCalls != 1 {
		t.Errorf("显式 0/0 compute 调用 %d 次, want 1", explicitCalls)
	}

	// 默认 key 不受影响，仍可命中且结果不变。
	got, outcome, err := c.getOrCompute(defaultKey, func() (*model.AIAnalysisResult, error) {
		return testAIResult("不应计算"), nil
	})
	if err != nil || outcome != aiCacheHit || got != wantDefault {
		t.Errorf("默认 key = (%v, %v, %v), want (预置结果, aiCacheHit, nil)", got, outcome, err)
	}
}

// C9 panic 释放等待者：leader panic 时等待者得到错误而非永久阻塞，inflight 清空且不写缓存。
func TestAICachePanicReleasesWaiters(t *testing.T) {
	c := newAIResultCache()
	key := aiCacheKeyFor("600519", "low", 0, 0, false)
	waiterEntered := make(chan struct{})
	var clockCalls atomic.Int32
	c.now = func() time.Time {
		if clockCalls.Add(1) == 2 {
			close(waiterEntered)
		}
		return time.Unix(0, 0)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	leaderDone := make(chan any, 1)
	go func() {
		defer func() { leaderDone <- recover() }()
		_, _, _ = c.getOrCompute(key, func() (*model.AIAnalysisResult, error) {
			close(entered)
			<-release
			panic("compute boom")
		})
	}()
	waitCh(t, entered, "leader 进入 compute")

	type callOut struct {
		res     *model.AIAnalysisResult
		outcome aiCacheOutcome
		err     error
	}
	waiterOut := make(chan callOut, 1)
	go func() {
		res, outcome, err := c.getOrCompute(key, func() (*model.AIAnalysisResult, error) {
			return testAIResult("waiter 不应计算"), nil
		})
		waiterOut <- callOut{res, outcome, err}
	}()
	waitCh(t, waiterEntered, "waiter 进入缓存查询")
	close(release)

	if recovered := <-leaderDone; recovered == nil {
		t.Fatalf("leader 未按预期 panic")
	}
	wo := <-waiterOut
	if wo.outcome != aiCacheCoalesced {
		t.Errorf("waiter outcome = %v, want aiCacheCoalesced", wo.outcome)
	}
	if wo.err == nil {
		t.Errorf("waiter err = nil, want 非空（leader panic 释放等待者）")
	}
	if len(c.inflight) != 0 {
		t.Errorf("inflight 未清空, 剩余 %d", len(c.inflight))
	}
	if len(c.entries) != 0 {
		t.Errorf("panic 路径写入了缓存, entries = %d", len(c.entries))
	}
}

// H1 缓存命中不取数：预置缓存后，handler 在任何取数之前直接返回。
// 若实现错误地把缓存放在取数之后，本用例会因外部请求失败而暴露。
func TestGetAIAnalysisCacheHitSkipsFetch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(client.New(), "test-key", "", "", nil)
	want := testAIResult("预置缓存结论")
	h.aiCache.entries[aiCacheKeyFor("600519", "low", 0, 0, false)] = aiCacheEntry{
		result:    want,
		expiresAt: time.Now().Add(time.Hour),
	}
	r := NewRouter(h)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stock/600519/ai-analysis", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Code int                    `json:"code"`
		Data model.AIAnalysisResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应非 JSON: %v (%s)", err, rec.Body.String())
	}
	if body.Code != 0 {
		t.Fatalf("code = %d, want 0 (%s)", body.Code, rec.Body.String())
	}
	if body.Data.Conclusion != "预置缓存结论" {
		t.Errorf("conclusion = %q, want 预置缓存结论", body.Data.Conclusion)
	}
}

// H2 code / apikey 校验优先：均在缓存之前，不触碰缓存、不发外部请求。
func TestGetAIAnalysisValidationBeforeCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DEEPSEEK_API_KEY", "")

	assertCacheEmpty := func(t *testing.T, h *Handler) {
		t.Helper()
		if len(h.aiCache.entries) != 0 || len(h.aiCache.inflight) != 0 {
			t.Errorf("缓存被触碰: entries=%d inflight=%d", len(h.aiCache.entries), len(h.aiCache.inflight))
		}
	}

	t.Run("非6位代码", func(t *testing.T) {
		h := NewHandler(client.New(), "test-key", "", "", nil)
		r := NewRouter(h)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stock/600/ai-analysis", nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("HTTP = %d, want 400 (%s)", rec.Code, rec.Body.String())
		}
		var body struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("响应非 JSON: %v (%s)", err, rec.Body.String())
		}
		if body.Code != 1 {
			t.Errorf("code = %d, want 1", body.Code)
		}
		assertCacheEmpty(t, h)
	})

	t.Run("apikey缺失", func(t *testing.T) {
		h := NewHandler(client.New(), "", "", "", nil)
		r := NewRouter(h)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stock/600519/ai-analysis", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP = %d, want 200 (%s)", rec.Code, rec.Body.String())
		}
		var body struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("响应非 JSON: %v (%s)", err, rec.Body.String())
		}
		if body.Code != 1 {
			t.Errorf("code = %d, want 1", body.Code)
		}
		assertCacheEmpty(t, h)
	})
}

// R25 AC-11：缓存 schema 升版，r24 条目不再命中。
func TestAICacheSchemaR25(t *testing.T) {
	if aiCacheSchema != "r25" {
		t.Fatalf("aiCacheSchema = %q，期望 r25", aiCacheSchema)
	}
}
