package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"financial-report/internal/client"
	"financial-report/internal/model"
	"financial-report/internal/store"
)

type historyResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    *struct {
		Items []model.ValuationRecord `json:"items"`
	} `json:"data"`
}

func newHistoryRouter(t *testing.T) (*gin.Engine, *store.ValuationHistoryStore) {
	t.Helper()
	st := store.NewValuationHistoryStore(t.TempDir())
	return NewRouter(NewHandler(client.New(), "", "", "", st)), st
}

func doHistory(t *testing.T, r *gin.Engine, method, path, body string) (*httptest.ResponseRecorder, historyResp) {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var resp historyResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v (%s)", err, rec.Body.String())
	}
	return rec, resp
}

func TestGetValuationHistoryEmpty(t *testing.T) {
	r, _ := newHistoryRouter(t)
	rec, resp := doHistory(t, r, http.MethodGet, "/api/valuation-history", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP = %d, want 200", rec.Code)
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0（%s）", resp.Code, rec.Body.String())
	}
	if resp.Data == nil || resp.Data.Items == nil || len(resp.Data.Items) != 0 {
		t.Fatalf("data.items = %+v, want []", resp.Data)
	}
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("items 应序列化为 []，响应 = %s", rec.Body.String())
	}
}

func TestPostValuationHistory(t *testing.T) {
	r, _ := newHistoryRouter(t)
	body := `{"code":"300750","name":"宁德时代","source":"ai","year":2025,` +
		`"model":"two_stage","discountRate":9,"stage1Growth":15,"stage1Years":5,"stage2Growth":3,` +
		`"fcfMode":"average","fcfYears":3,"adjustRD":true,"baseFCF":45600000000,` +
		`"createdAt":"2000-01-01T00:00:00Z"}`
	rec, resp := doHistory(t, r, http.MethodPost, "/api/valuation-history", body)
	if rec.Code != http.StatusOK || resp.Code != 0 {
		t.Fatalf("HTTP=%d code=%d, want 200/0（%s）", rec.Code, resp.Code, rec.Body.String())
	}
	if resp.Data == nil || len(resp.Data.Items) != 1 {
		t.Fatalf("items = %+v, want 1 条", resp.Data)
	}
	it := resp.Data.Items[0]
	if it.CreatedAt == "2000-01-01T00:00:00Z" {
		t.Error("createdAt 应由服务端生成并忽略传入值")
	}
	if it.Market != "sz" {
		t.Errorf("market = %q, want 回填 sz", it.Market)
	}
	if it.FCFModeName != "近几年平均值" {
		t.Errorf("fcfModeName = %q, want 回填", it.FCFModeName)
	}
	if it.ModelName != "两阶段模型" {
		t.Errorf("modelName = %q, want 回填", it.ModelName)
	}
	if it.Source != "ai" {
		t.Errorf("source = %q, want ai", it.Source)
	}
}

func TestPostValuationHistoryInvalidCode(t *testing.T) {
	r, _ := newHistoryRouter(t)
	for _, body := range []string{
		`{"code":"12345"}`,
		`{"code":"abcdef"}`,
		`{"code":""}`,
	} {
		rec, resp := doHistory(t, r, http.MethodPost, "/api/valuation-history", body)
		if rec.Code != http.StatusBadRequest || resp.Code != 1 {
			t.Errorf("body=%s → HTTP=%d code=%d, want 400/1", body, rec.Code, resp.Code)
		}
	}
}

func TestPostValuationHistoryInvalidBody(t *testing.T) {
	r, _ := newHistoryRouter(t)
	rec, resp := doHistory(t, r, http.MethodPost, "/api/valuation-history", `not json`)
	if rec.Code != http.StatusBadRequest || resp.Code != 1 {
		t.Errorf("非法 JSON → HTTP=%d code=%d, want 400/1", rec.Code, resp.Code)
	}

	big := `{"code":"600519","name":"` + strings.Repeat("a", 70<<10) + `"}`
	rec, resp = doHistory(t, r, http.MethodPost, "/api/valuation-history", big)
	if rec.Code != http.StatusBadRequest || resp.Code != 1 {
		t.Errorf("超 64KiB → HTTP=%d code=%d, want 400/1", rec.Code, resp.Code)
	}
}

func TestPostValuationHistoryOverwrite(t *testing.T) {
	r, _ := newHistoryRouter(t)
	first := `{"code":"600519","name":"第一次","model":"zero","discountRate":8}`
	if rec, resp := doHistory(t, r, http.MethodPost, "/api/valuation-history", first); rec.Code != 200 || resp.Code != 0 {
		t.Fatalf("首次 POST 失败: HTTP=%d code=%d", rec.Code, resp.Code)
	}
	second := `{"code":"600519","name":"第二次","model":"zero","discountRate":9}`
	rec, resp := doHistory(t, r, http.MethodPost, "/api/valuation-history", second)
	if rec.Code != 200 || resp.Code != 0 {
		t.Fatalf("覆盖 POST 失败: HTTP=%d code=%d", rec.Code, resp.Code)
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("覆盖后 items = %d, want 1", len(resp.Data.Items))
	}
	if resp.Data.Items[0].Name != "第二次" {
		t.Errorf("name = %q, want 第二次", resp.Data.Items[0].Name)
	}
}

func TestDeleteValuationHistory(t *testing.T) {
	r, _ := newHistoryRouter(t)
	for _, body := range []string{
		`{"code":"600519","name":"贵州茅台"}`,
		`{"code":"000001","name":"平安银行"}`,
	} {
		if rec, resp := doHistory(t, r, http.MethodPost, "/api/valuation-history", body); rec.Code != 200 || resp.Code != 0 {
			t.Fatalf("预置 POST 失败: HTTP=%d code=%d", rec.Code, resp.Code)
		}
	}

	rec, resp := doHistory(t, r, http.MethodDelete, "/api/valuation-history/600519", "")
	if rec.Code != http.StatusOK || resp.Code != 0 {
		t.Fatalf("删除存在: HTTP=%d code=%d", rec.Code, resp.Code)
	}
	if len(resp.Data.Items) != 1 || resp.Data.Items[0].Code != "000001" {
		t.Fatalf("删除后 items = %+v, want 仅 000001", resp.Data.Items)
	}

	// 删除不存在的 code：幂等成功
	rec, resp = doHistory(t, r, http.MethodDelete, "/api/valuation-history/999999", "")
	if rec.Code != http.StatusOK || resp.Code != 0 || len(resp.Data.Items) != 1 {
		t.Fatalf("删除不存在: HTTP=%d code=%d items=%d", rec.Code, resp.Code, len(resp.Data.Items))
	}
}

func TestDeleteValuationHistoryInvalidCode(t *testing.T) {
	r, _ := newHistoryRouter(t)
	rec, resp := doHistory(t, r, http.MethodDelete, "/api/valuation-history/abc", "")
	if rec.Code != http.StatusBadRequest || resp.Code != 1 {
		t.Errorf("非法 code → HTTP=%d code=%d, want 400/1", rec.Code, resp.Code)
	}
}

func TestClearValuationHistory(t *testing.T) {
	r, st := newHistoryRouter(t)
	if rec, resp := doHistory(t, r, http.MethodPost, "/api/valuation-history", `{"code":"600519"}`); rec.Code != 200 || resp.Code != 0 {
		t.Fatalf("预置 POST 失败: HTTP=%d code=%d", rec.Code, resp.Code)
	}
	rec, resp := doHistory(t, r, http.MethodDelete, "/api/valuation-history", "")
	if rec.Code != http.StatusOK || resp.Code != 0 {
		t.Fatalf("清空: HTTP=%d code=%d", rec.Code, resp.Code)
	}
	if resp.Data == nil || resp.Data.Items == nil || len(resp.Data.Items) != 0 {
		t.Fatalf("清空后 items = %+v, want []", resp.Data)
	}
	raw, err := os.ReadFile(st.FilePath())
	if err != nil {
		t.Fatalf("读取文件: %v", err)
	}
	var env struct {
		Version int               `json:"version"`
		Items   []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("文件非合法信封: %v (%s)", err, raw)
	}
	if env.Version != store.ValuationHistoryVersion || env.Items == nil || len(env.Items) != 0 {
		t.Errorf("文件 version=%d items=%v, want 1/[]", env.Version, env.Items)
	}
}

func TestHistoryWriteFailure(t *testing.T) {
	dataDir := t.TempDir()
	st := store.NewValuationHistoryStore(dataDir)
	// 目标文件位置造成目录，写入必然失败
	if err := os.Mkdir(st.FilePath(), 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRouter(NewHandler(client.New(), "", "", "", st))

	rec, resp := doHistory(t, r, http.MethodPost, "/api/valuation-history", `{"code":"600519"}`)
	if rec.Code != http.StatusOK || resp.Code != 1 {
		t.Fatalf("POST 写失败 → HTTP=%d code=%d, want 200/1（%s）", rec.Code, resp.Code, rec.Body.String())
	}
	if !strings.Contains(resp.Message, "保存失败") {
		t.Errorf("message = %q, want 含「保存失败」", resp.Message)
	}
	if resp.Data != nil {
		t.Errorf("写失败 data 应为 null, got %+v", resp.Data)
	}

	rec, resp = doHistory(t, r, http.MethodDelete, "/api/valuation-history", "")
	if rec.Code != http.StatusOK || resp.Code != 1 || !strings.Contains(resp.Message, "保存失败") {
		t.Fatalf("清空写失败 → HTTP=%d code=%d message=%q, want 200/1/保存失败", rec.Code, resp.Code, resp.Message)
	}
}

func TestHistoryNilStore(t *testing.T) {
	r := NewRouter(NewHandler(client.New(), "", "", "", nil))
	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/api/valuation-history", ""},
		{http.MethodPost, "/api/valuation-history", `{"code":"600519"}`},
		{http.MethodDelete, "/api/valuation-history/600519", ""},
		{http.MethodDelete, "/api/valuation-history", ""},
	}
	for _, tc := range cases {
		rec, resp := doHistory(t, r, tc.method, tc.path, tc.body)
		if rec.Code != http.StatusOK || resp.Code != 1 {
			t.Errorf("%s %s → HTTP=%d code=%d, want 200/1", tc.method, tc.path, rec.Code, resp.Code)
		}
		if resp.Message != "估值记录存储未初始化" {
			t.Errorf("message = %q, want 估值记录存储未初始化", resp.Message)
		}
	}
}
