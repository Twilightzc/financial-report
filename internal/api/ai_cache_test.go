package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"financial-report/internal/client"
	"financial-report/internal/model"
	"financial-report/internal/service"
)

func testOutcome(conclusion string) *aiModuleOutcome {
	return &aiModuleOutcome{Payload: &model.AIFinancialResult{Conclusion: conclusion}}
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
	key := aiCacheKeyFor("600519", model.AIModuleFinancial, 0, 0, false)
	want := testOutcome("预置结论")
	c.entries[key] = aiCacheEntry{outcome: want, expiresAt: c.now().Add(time.Hour)}

	calls := 0
	got, outcome, err := c.getOrCompute(key, func() (*aiModuleOutcome, error) {
		calls++
		return testOutcome("不应执行"), nil
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
	key := aiCacheKeyFor("600519", model.AIModuleFinancial, 2020, 2024, true)
	want := testOutcome("首次结果")

	calls := 0
	compute := func() (*aiModuleOutcome, error) {
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

	key := aiCacheKeyFor("600519", model.AIModuleFinancial, 0, 0, false)
	first := testOutcome("第一版")
	second := testOutcome("第二版")
	calls := 0
	compute := func() (*aiModuleOutcome, error) {
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
	if e := c.entries[key]; e.outcome != second {
		t.Errorf("过期条目未被新结果替换")
	}
}

// C4 并发去重：leader 计算期间同 key 请求等待并复用，compute 只执行一次。
func TestAICacheCoalescesConcurrent(t *testing.T) {
	c := newAIResultCache()
	key := aiCacheKeyFor("600519", model.AIModuleFinancial, 0, 0, false)
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
	want := testOutcome("leader 结果")
	var calls atomic.Int32

	type callOut struct {
		res     *aiModuleOutcome
		outcome aiCacheOutcome
		err     error
	}
	leaderOut := make(chan callOut, 1)
	go func() {
		res, outcome, err := c.getOrCompute(key, func() (*aiModuleOutcome, error) {
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
		res, outcome, err := c.getOrCompute(key, func() (*aiModuleOutcome, error) {
			calls.Add(1)
			return testOutcome("waiter 不应计算"), nil
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
		aiCacheKeyFor("600519", model.AIModuleFinancial, 0, 0, false),
		aiCacheKeyFor("600519", model.AIModuleIndustry, 0, 0, false),
	}
	entered := []chan struct{}{make(chan struct{}), make(chan struct{})}
	release := make(chan struct{})

	type callOut struct {
		res     *aiModuleOutcome
		outcome aiCacheOutcome
		err     error
	}
	outs := make([]chan callOut, len(keys))
	for i := range keys {
		i := i
		outs[i] = make(chan callOut, 1)
		go func() {
			want := testOutcome(fmt.Sprintf("结果%d", i))
			res, outcome, err := c.getOrCompute(keys[i], func() (*aiModuleOutcome, error) {
				close(entered[i])
				<-release
				return want, nil
			})
			outs[i] <- callOut{res, outcome, err}
		}()
	}
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
	key := aiCacheKeyFor("600519", model.AIModuleFinancial, 0, 0, false)
	calls := 0
	compute := func() (*aiModuleOutcome, error) {
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
		key := aiCacheKeyFor(fmt.Sprintf("%06d", i%1000000), model.AIModuleFinancial, 0, 0, false)
		want := testOutcome(fmt.Sprintf("结果%d", i))
		if _, outcome, err := c.getOrCompute(key, func() (*aiModuleOutcome, error) {
			return want, nil
		}); err != nil || outcome != aiCacheMiss {
			t.Fatalf("写入 key%d = (outcome=%v, err=%v), want (aiCacheMiss, nil)", i, outcome, err)
		}
		lastKey = key
	}
	if n := len(c.entries); n > aiCacheMax {
		t.Errorf("entries = %d, want <= %d", n, aiCacheMax)
	}
	if _, outcome, err := c.getOrCompute(lastKey, func() (*aiModuleOutcome, error) {
		lastCalls++
		return nil, nil
	}); err != nil || outcome != aiCacheHit {
		t.Errorf("最新 key = (outcome=%v, err=%v), want (aiCacheHit, nil)", outcome, err)
	}
	if lastCalls != 0 {
		t.Errorf("最新 key 命中时仍执行 compute")
	}
}

// C8 key 维度：module / 年份 / 代码任一不同即不同 key；未指定年份归一化为 0/0；
// 显式 0/0（RangeSpecified=true）与未指定（false）必须区分；不再含 reasoning 维度。
func TestAICacheKeyDimensions(t *testing.T) {
	base := aiCacheKeyFor("600519", model.AIModuleFinancial, 0, 0, false)
	if base.StartYear != 0 || base.EndYear != 0 {
		t.Errorf("未指定年份 key = %+v, want StartYear=0 EndYear=0", base)
	}
	if base.RangeSpecified {
		t.Errorf("未指定年份 RangeSpecified = true, want false")
	}
	if aiCacheKeyFor("600519", model.AIModuleIndustry, 0, 0, false) == base {
		t.Errorf("不同 module 生成了相同 key")
	}
	if aiCacheKeyFor("600519", model.AIModuleFinancial, 2020, 2024, true) == base {
		t.Errorf("不同年份范围生成了相同 key")
	}
	if aiCacheKeyFor("000001", model.AIModuleFinancial, 0, 0, false) == base {
		t.Errorf("不同代码生成了相同 key")
	}
	if aiCacheKeyFor("600519", model.AIModuleFinancial, 0, 0, true) == base {
		t.Errorf("显式 0/0 与未指定年份生成了相同 key")
	}
}

// C8b 显式 0/0 与未指定年份互不影响。
func TestAICacheExplicitZeroRangeIsolated(t *testing.T) {
	c := newAIResultCache()
	defaultKey := aiCacheKeyFor("600519", model.AIModuleFinancial, 0, 0, false)
	explicitKey := aiCacheKeyFor("600519", model.AIModuleFinancial, 0, 0, true)

	wantDefault := testOutcome("默认年份结果")
	c.entries[defaultKey] = aiCacheEntry{outcome: wantDefault, expiresAt: c.now().Add(time.Hour)}

	explicitCalls := 0
	_, outcome, err := c.getOrCompute(explicitKey, func() (*aiModuleOutcome, error) {
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

	got, outcome, err := c.getOrCompute(defaultKey, func() (*aiModuleOutcome, error) {
		return testOutcome("不应计算"), nil
	})
	if err != nil || outcome != aiCacheHit || got != wantDefault {
		t.Errorf("默认 key = (%v, %v, %v), want (预置结果, aiCacheHit, nil)", got, outcome, err)
	}
}

// C9 panic 释放等待者：leader panic 时等待者得到错误而非永久阻塞，inflight 清空且不写缓存。
func TestAICachePanicReleasesWaiters(t *testing.T) {
	c := newAIResultCache()
	key := aiCacheKeyFor("600519", model.AIModuleFinancial, 0, 0, false)
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
		_, _, _ = c.getOrCompute(key, func() (*aiModuleOutcome, error) {
			close(entered)
			<-release
			panic("compute boom")
		})
	}()
	waitCh(t, entered, "leader 进入 compute")

	type callOut struct {
		res     *aiModuleOutcome
		outcome aiCacheOutcome
		err     error
	}
	waiterOut := make(chan callOut, 1)
	go func() {
		res, outcome, err := c.getOrCompute(key, func() (*aiModuleOutcome, error) {
			return testOutcome("waiter 不应计算"), nil
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

// seedModule 预置某模块缓存（模拟同一股票同年份已分析过）。
func seedModule(h *Handler, module string, out *aiModuleOutcome) {
	h.aiCache.entries[aiCacheKeyFor("600519", module, 0, 0, false)] = aiCacheEntry{
		outcome:   out,
		expiresAt: time.Now().Add(time.Hour),
	}
}

// H1/T15 全命中不取数：四个已上线模块预置缓存后，all 请求零上游请求直接返回。
// 若实现错误地把缓存放在取数之后，本用例会因外部请求失败而暴露。
func TestGetAIAnalysisAllHitSkipsFetch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(client.New(), "test-key", "", "", nil)
	seedModule(h, model.AIModuleFinancial, &aiModuleOutcome{Payload: &model.AIFinancialResult{Conclusion: "预置财务结论"}})
	seedModule(h, model.AIModuleIndustry, &aiModuleOutcome{Payload: &model.AIIndustryResult{Industry: model.AIIndustry{Name: "预置行业"}}})
	seedModule(h, model.AIModuleValuation, &aiModuleOutcome{Payload: &model.AIValuationResult{Valuation: model.AIValuation{ModelName: "预置估值"}}})
	seedModule(h, model.AIModuleBusinessModel, &aiModuleOutcome{Payload: &model.AIBusinessModelResult{Score: 82, Rating: "良好", Scored: true}})
	r := NewRouter(h)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stock/600519/ai-analysis", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Code int                        `json:"code"`
		Data model.AIAllModulesResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应非 JSON: %v (%s)", err, rec.Body.String())
	}
	if body.Code != 0 {
		t.Fatalf("code = %d, want 0 (%s)", body.Code, rec.Body.String())
	}
	f := body.Data.Modules[model.AIModuleFinancial]
	if f.Status != model.AIModuleStatusOK || !f.Cached || f.ElapsedMS != 0 {
		t.Errorf("财务模块缓存响应错误: %+v", f)
	}
	if body.Data.Modules[model.AIModuleIndustry].Status != model.AIModuleStatusOK ||
		body.Data.Modules[model.AIModuleValuation].Status != model.AIModuleStatusOK {
		t.Errorf("行业/估值模块应命中缓存: %+v", body.Data.Modules)
	}
	bm := body.Data.Modules[model.AIModuleBusinessModel]
	if bm.Status != model.AIModuleStatusOK || !bm.Cached {
		t.Errorf("商业模式模块应命中缓存: %+v", bm)
	}
	if len(body.Data.Modules) != 4 {
		t.Errorf("all 应返回 4 个模块，实际 %d: %+v", len(body.Data.Modules), body.Data.Modules)
	}
}

// H1b 单模块命中：只返回该模块，cached=true。
func TestGetAIAnalysisSingleModuleHit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(client.New(), "test-key", "", "", nil)
	seedModule(h, model.AIModuleFinancial, &aiModuleOutcome{Payload: &model.AIFinancialResult{Conclusion: "预置财务结论"}})
	r := NewRouter(h)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stock/600519/ai-analysis?module=financial", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Module string                   `json:"module"`
			Status string                   `json:"status"`
			Cached bool                     `json:"cached"`
			Result *model.AIFinancialResult `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应非 JSON: %v (%s)", err, rec.Body.String())
	}
	if body.Code != 0 || body.Data.Module != model.AIModuleFinancial || body.Data.Status != model.AIModuleStatusOK || !body.Data.Cached {
		t.Fatalf("单模块响应错误: %+v", body)
	}
	if body.Data.Result == nil || body.Data.Result.Conclusion != "预置财务结论" {
		t.Errorf("result 载荷错误: %+v", body.Data.Result)
	}
}

// H2 code / module / apikey 校验优先：均在缓存与取数之前，不触碰缓存、不发外部请求。
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

	t.Run("非法module", func(t *testing.T) {
		h := NewHandler(client.New(), "test-key", "", "", nil)
		r := NewRouter(h)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stock/600519/ai-analysis?module=bad", nil))
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

	t.Run("年度非法", func(t *testing.T) {
		h := NewHandler(client.New(), "test-key", "", "", nil)
		r := NewRouter(h)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stock/600519/ai-analysis?startYear=2024&endYear=2020", nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("HTTP = %d, want 400 (%s)", rec.Code, rec.Body.String())
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

// businessModelFakeJSON 九要素全「中」，并故意携带 total_score/rating/grade
// 验证 AI 评分被丢弃（最终应为 68 / 一般）。
const businessModelFakeJSON = `{
  "total_score": 99,
  "rating": "优秀",
  "grade": "A",
  "elements": [
    {"key":"customer_segments","analysis":"客户基础广。","basis":"行业常识","level":"中"},
    {"key":"value_propositions","analysis":"产品差异化有限。","basis":"主营构成","level":"中"},
    {"key":"channels","analysis":"渠道以经销为主。","basis":"行业常识","level":"中"},
    {"key":"customer_relationships","analysis":"关系维护常规。","basis":"AI 推断","level":"中"},
    {"key":"revenue_streams","analysis":"收入来源集中。","basis":"主营构成","level":"中"},
    {"key":"key_resources","analysis":"资源禀赋一般。","basis":"行业常识","level":"中"},
    {"key":"key_activities","analysis":"关键活动常规。","basis":"AI 推断","level":"中"},
    {"key":"key_partners","analysis":"伙伴关系稳定。","basis":"AI 推断","level":"中"},
    {"key":"cost_structure","analysis":"成本结构普通。","basis":"行业常识","level":"中"}
  ],
  "summary": "模式优秀，核心优势突出。",
  "conclusion": "公司以品牌创造价值，通过经销渠道传递价值，最终获取稳定回报。"
}`

// businessModelTestRows 最小三表年报行（普通公司：有收入、有营业成本）。
func businessModelTestRows() []model.ReportRow {
	f := func(v float64) *float64 { return &v }
	return []model.ReportRow{{
		ReportDate: "2024-12-31",
		Fields: map[string]*float64{
			"TOTAL_OPERATE_INCOME": f(100e8),
			"OPERATE_COST":         f(40e8),
			"NETCASH_OPERATE":      f(20e8),
			"TOTAL_ASSETS":         f(200e8),
			"TOTAL_EQUITY":         f(120e8),
		},
	}}
}

// H3 business_model 单模块已上线：取数一次、LLM 调用一次，AI 总分被后端重算覆盖。
func TestGetAIAnalysisBusinessModelSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(client.New(), "test-key", "", "", nil)
	rows := businessModelTestRows()
	h.aiSource = aiDataSource{
		quote: func(string) (*model.Quote, error) {
			return &model.Quote{Name: "测试公司", Code: "600519", MarketCap: 1e11}, nil
		},
		reports: func(string, int, int) (cashflow, balance, income []model.ReportRow, err error) {
			return rows, rows, rows, nil
		},
		segments: func(string) ([]model.SegmentIncome, error) {
			return []model.SegmentIncome{{Name: "主业", RevenueRatio: 0.9}}, nil
		},
		industry: func(string) (string, string, error) { return "1277", "测试行业", nil },
	}
	called := 0
	h.aiChat = func(apiKey, baseURL, modelName, system, user string) (string, model.TokenUsage, error) {
		called++
		if !strings.Contains(system, "customer_segments") || !strings.Contains(system, "成本结构（CST）") {
			t.Errorf("商业模式 system 提示词缺少九要素结构")
		}
		if !strings.Contains(user, "【商业模式事实") {
			t.Errorf("商业模式用户消息缺少事实块: %s", user)
		}
		return businessModelFakeJSON, model.TokenUsage{TotalTokens: 100}, nil
	}
	r := NewRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stock/600519/ai-analysis?module=business_model", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Module string                       `json:"module"`
			Status string                       `json:"status"`
			Result *model.AIBusinessModelResult `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应非 JSON: %v (%s)", err, rec.Body.String())
	}
	if body.Code != 0 || body.Data.Status != model.AIModuleStatusOK || body.Data.Result == nil {
		t.Fatalf("business_model 响应错误: %+v (%s)", body, rec.Body.String())
	}
	res := body.Data.Result
	if !res.Scored || res.Score != 68 || res.Rating != "一般" {
		t.Errorf("AI 总分应被丢弃并按九要素重算为 68/一般，实际 scored=%v score=%d rating=%q", res.Scored, res.Score, res.Rating)
	}
	if res.Coverage != 9 || res.Total != 9 || len(res.Elements) != 9 {
		t.Errorf("覆盖率/要素数错误: coverage=%d total=%d elements=%d", res.Coverage, res.Total, len(res.Elements))
	}
	if called != 1 {
		t.Errorf("LLM 调用 %d 次，want 1", called)
	}
	if _, ok := h.aiCache.peek(aiCacheKeyFor("600519", model.AIModuleBusinessModel, 0, 0, false)); !ok {
		t.Error("成功模块应写入缓存")
	}
}

// H4 旧 reasoning 参数被忽略：不报错、不影响缓存 key、不取数（单模块命中）。
func TestGetAIAnalysisReasoningIgnored(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(client.New(), "test-key", "", "", nil)
	seedModule(h, model.AIModuleFinancial, &aiModuleOutcome{Payload: &model.AIFinancialResult{Conclusion: "详细档结论"}})
	r := NewRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stock/600519/ai-analysis?module=financial&reasoning=low", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data struct {
			Cached bool                     `json:"cached"`
			Result *model.AIFinancialResult `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	if body.Code != 0 || !body.Data.Cached || body.Data.Result.Conclusion != "详细档结论" {
		t.Errorf("reasoning 参数应被忽略: %+v", body)
	}
}

// T11 单模块执行：runAIModule 只调用一次 LLM 并解析出本模块载荷。
func TestRunAIModuleCallsChatOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(client.New(), "test-key", "", "", nil)
	calls := 0
	h.aiChat = func(apiKey, baseURL, modelName, system, user string) (string, model.TokenUsage, error) {
		calls++
		if !strings.Contains(system, "scores") {
			t.Errorf("财务模块 system 提示词异常")
		}
		return `{"scores":[
			{"dimension":"盈利能力","score":88,"comment":"a"},
			{"dimension":"偿债能力","score":90,"comment":"b"},
			{"dimension":"现金获取能力","score":85,"comment":"c"},
			{"dimension":"经营效率","score":76,"comment":"d"},
			{"dimension":"成长能力","score":82,"comment":"e"}],"conclusion":"详细结论"}`, model.TokenUsage{TotalTokens: 123}, nil
	}
	specs, _, _ := service.ResolveAIModules(model.AIModuleFinancial)
	out, err := h.runAIModule(specs[0], service.AIModuleInput{Quote: model.Quote{Name: "贵州茅台", Code: "600519"}}, "k", "m", "u")
	if err != nil {
		t.Fatalf("runAIModule 失败: %v", err)
	}
	if calls != 1 {
		t.Errorf("LLM 调用 %d 次, want 1", calls)
	}
	payload, ok := out.Payload.(*model.AIFinancialResult)
	if !ok || payload.Conclusion != "详细结论" || len(payload.Scores) != 5 {
		t.Errorf("载荷解析错误: %+v", out.Payload)
	}
	if out.Usage == nil || out.Usage.TotalTokens != 123 {
		t.Errorf("usage 未回填: %+v", out.Usage)
	}
}

// T12 模块级失败边界：估值 LLM 失败不影响财务成功，失败模块不写缓存。
func TestExecuteAIModulePartialFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(client.New(), "test-key", "", "", nil)
	h.aiChat = func(apiKey, baseURL, modelName, system, user string) (string, model.TokenUsage, error) {
		if strings.Contains(user, "估值参数事实") {
			return "", model.TokenUsage{}, errors.New("估值上游失败")
		}
		return `{"scores":[
			{"dimension":"盈利能力","score":88,"comment":"a"},
			{"dimension":"偿债能力","score":90,"comment":"b"},
			{"dimension":"现金获取能力","score":85,"comment":"c"},
			{"dimension":"经营效率","score":76,"comment":"d"},
			{"dimension":"成长能力","score":82,"comment":"e"}],"conclusion":"ok"}`, model.TokenUsage{TotalTokens: 10}, nil
	}

	specs, _, _ := service.ResolveAIModules(model.AIModuleAll)
	byID := map[string]service.AIModuleSpec{}
	for _, s := range specs {
		byID[s.ID] = s
	}
	keyFor := func(id string) aiCacheKey { return aiCacheKeyFor("600519", id, 0, 0, false) }

	fin := h.executeAIModule("600519", pendingModule{spec: byID[model.AIModuleFinancial], key: keyFor(model.AIModuleFinancial)},
		service.AIModuleInput{Quote: model.Quote{Name: "贵州茅台", Code: "600519"}}, "k", "m", "u")
	if fin.Status != model.AIModuleStatusOK {
		t.Errorf("财务模块应成功: %+v", fin)
	}
	val := h.executeAIModule("600519", pendingModule{spec: byID[model.AIModuleValuation], key: keyFor(model.AIModuleValuation)},
		service.AIModuleInput{Quote: model.Quote{Name: "贵州茅台", Code: "600519"}}, "k", "m", "u")
	if val.Status != model.AIModuleStatusError || val.Message != "分析失败，请重试" {
		t.Errorf("估值模块应失败且文案通用: %+v", val)
	}
	if _, ok := h.aiCache.peek(keyFor(model.AIModuleFinancial)); !ok {
		t.Error("成功模块应写入缓存")
	}
	if _, ok := h.aiCache.peek(keyFor(model.AIModuleValuation)); ok {
		t.Error("失败模块不应写入缓存")
	}
}

// T12b aiBuildError 影响范围：三表失败只影响依赖报表的模块，行情失败影响全部。
func TestAIBuildErrorAffects(t *testing.T) {
	specs, _, _ := service.ResolveAIModules(model.AIModuleAll)
	byID := map[string]service.AIModuleSpec{}
	for _, s := range specs {
		byID[s.ID] = s
	}
	reports := &aiBuildError{reports: true}
	for _, id := range []string{model.AIModuleFinancial, model.AIModuleValuation, model.AIModuleBusinessModel} {
		if !reports.affects(byID[id]) {
			t.Errorf("三表失败应影响 %s", id)
		}
	}
	if reports.affects(byID[model.AIModuleIndustry]) {
		t.Error("三表失败不应影响行业")
	}
	quote := &aiBuildError{quote: true}
	for _, id := range []string{model.AIModuleFinancial, model.AIModuleIndustry, model.AIModuleValuation, model.AIModuleBusinessModel} {
		if !quote.affects(byID[id]) {
			t.Errorf("行情失败应影响 %s", id)
		}
	}
	year := &aiBuildError{year: true}
	if !year.affects(byID[model.AIModuleBusinessModel]) {
		t.Error("年份失败应影响商业模式（依赖年报行）")
	}
}

// aiAllFakeHandler 构造 all 请求的注入式 Handler：四个模块均由 fake chat 返回合法结构。
// failBusiness=true 时商业模式模块返回非法 JSON，用于验证模块级失败隔离。
func aiAllFakeHandler(t *testing.T, failBusiness bool) (*Handler, *int32) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewHandler(client.New(), "test-key", "", "", nil)
	f := func(v float64) *float64 { return &v }
	rows := []model.ReportRow{{
		ReportDate: "2024-12-31",
		Fields: map[string]*float64{
			"TOTAL_OPERATE_INCOME": f(100e8), "OPERATE_COST": f(40e8),
			"NONCASH_INVEST_LOSS": f(0), "NETCASH_OPERATE": f(20e8),
			"TOTAL_ASSETS": f(200e8), "TOTAL_EQUITY": f(120e8),
		},
	}}
	h.aiSource = aiDataSource{
		quote: func(string) (*model.Quote, error) {
			return &model.Quote{Name: "测试公司", Code: "600519", MarketCap: 1e11}, nil
		},
		reports: func(string, int, int) ([]model.ReportRow, []model.ReportRow, []model.ReportRow, error) {
			return rows, rows, rows, nil
		},
		segments: func(string) ([]model.SegmentIncome, error) {
			return []model.SegmentIncome{{Name: "主业", RevenueRatio: 0.9}}, nil
		},
		peers:    func(string) (model.LeaderBoard, error) { return model.LeaderBoard{}, nil },
		industry: func(string) (string, string, error) { return "1277", "测试行业", nil },
		growth:   func(string) model.GrowthSnapshot { return model.GrowthSnapshot{} },
	}
	var calls int32
	h.aiChat = func(_, _, _, _ string, user string) (string, model.TokenUsage, error) {
		atomic.AddInt32(&calls, 1)
		switch {
		case strings.Contains(user, "【商业模式事实"):
			if failBusiness {
				return "not-json", model.TokenUsage{TotalTokens: 1}, nil
			}
			return businessModelFakeJSON, model.TokenUsage{TotalTokens: 10}, nil
		case strings.Contains(user, "【估值参数事实"):
			return `{"valuation":{}}`, model.TokenUsage{TotalTokens: 10}, nil
		case strings.Contains(user, "同行市值"):
			return `{"industry":{"name":"测试行业"},"businesses":[],"chain":null,"partners":[]}`, model.TokenUsage{TotalTokens: 10}, nil
		default:
			return `{"scores":[
				{"dimension":"盈利能力","score":88,"comment":"a"},
				{"dimension":"偿债能力","score":90,"comment":"b"},
				{"dimension":"现金获取能力","score":85,"comment":"c"},
				{"dimension":"经营效率","score":76,"comment":"d"},
				{"dimension":"成长能力","score":82,"comment":"e"}],"conclusion":"ok"}`, model.TokenUsage{TotalTokens: 10}, nil
		}
	}
	return h, &calls
}

// T15 all 成功：4 个模块各调用一次 LLM，business_model 成功且评分由后端重算。
func TestGetAIAnalysisAllModulesCallsEachOnce(t *testing.T) {
	h, calls := aiAllFakeHandler(t, false)
	r := NewRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stock/600519/ai-analysis?module=all", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Code int                        `json:"code"`
		Data model.AIAllModulesResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应非 JSON: %v (%s)", err, rec.Body.String())
	}
	if body.Code != 0 {
		t.Fatalf("code = %d, want 0 (%s)", body.Code, rec.Body.String())
	}
	if got := atomic.LoadInt32(calls); got != 4 {
		t.Errorf("LLM 调用 %d 次，want 4（每模块一次）", got)
	}
	if len(body.Data.Modules) != 4 {
		t.Errorf("all 应返回 4 个模块，实际 %d", len(body.Data.Modules))
	}
	for _, id := range []string{model.AIModuleFinancial, model.AIModuleIndustry, model.AIModuleValuation, model.AIModuleBusinessModel} {
		if m := body.Data.Modules[id]; m.Status != model.AIModuleStatusOK {
			t.Errorf("模块 %s 应成功: %+v", id, m)
		}
	}
	bm, ok := body.Data.Modules[model.AIModuleBusinessModel].Result.(map[string]any)
	if !ok {
		t.Fatalf("business_model 载荷类型错误: %T", body.Data.Modules[model.AIModuleBusinessModel].Result)
	}
	if bm["scored"] != true || bm["score"] != float64(68) || bm["rating"] != "一般" {
		t.Errorf("AI 总分应被丢弃重算为 68/一般: %+v", bm)
	}
}

// AC-6 模块级失败隔离：business_model 解析失败时 all 仍 code=0，其余三模块 ok。
func TestGetAIAnalysisBusinessModelFailureIsolated(t *testing.T) {
	h, _ := aiAllFakeHandler(t, true)
	r := NewRouter(h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stock/600519/ai-analysis?module=all", nil))
	var body struct {
		Code int                        `json:"code"`
		Data model.AIAllModulesResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应非 JSON: %v (%s)", err, rec.Body.String())
	}
	if body.Code != 0 {
		t.Errorf("单模块失败不应使 all 失败，code = %d (%s)", body.Code, rec.Body.String())
	}
	if bm := body.Data.Modules[model.AIModuleBusinessModel]; bm.Status != model.AIModuleStatusError {
		t.Errorf("business_model 应为 error，实际 %+v", bm)
	}
	for _, id := range []string{model.AIModuleFinancial, model.AIModuleIndustry, model.AIModuleValuation} {
		if m := body.Data.Modules[id]; m.Status != model.AIModuleStatusOK {
			t.Errorf("模块 %s 不应受商业模式失败影响: %+v", id, m)
		}
	}
}

// R27：缓存 schema 升版，r26 条目不再命中（新增商业模式结果结构）。
func TestAICacheSchemaR27(t *testing.T) {
	if aiCacheSchema != "r27" {
		t.Fatalf("aiCacheSchema = %q，期望 r27", aiCacheSchema)
	}
	// r26 生成的 key 与当前 key 不同，旧缓存不会被读取。
	legacy := aiCacheKey{Schema: "r26", Code: "600519", Module: model.AIModuleFinancial}
	if legacy == aiCacheKeyFor("600519", model.AIModuleFinancial, 0, 0, false) {
		t.Fatal("r26 缓存 key 不应与 r27 相同")
	}
}
