package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"financial-report/internal/client"
)

// 新增接口只需离线可验证的部分：空/超长 q 的守卫，以及新静态路由与既有 :code 路由共存。
// 说明：包内其余 handler 依赖外部数据源（东财/腾讯），此处不覆盖。

func TestGetSearchEmptyAndLongQuery(t *testing.T) {
	r := NewRouter(NewHandler(client.New(), "", "", ""))

	cases := []struct {
		name string
		url  string
	}{
		{"缺省 q", "/api/stock/search"},
		{"空 q", "/api/stock/search?q="},
		{"纯空白 q", "/api/stock/search?q=%20%20"},
		{"超长 q", "/api/stock/search?q=" + strings.Repeat("a", maxSearchQueryRunes+1)},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.url, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: HTTP = %d, want 200", tc.name, rec.Code)
		}
		var body struct {
			Code int               `json:"code"`
			Data []json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: 响应非 JSON: %v (%s)", tc.name, err, rec.Body.String())
		}
		if body.Code != 0 {
			t.Errorf("%s: code = %d, want 0", tc.name, body.Code)
		}
		if len(body.Data) != 0 {
			t.Errorf("%s: data 应为空数组，实得 %s", tc.name, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"data":[]`) {
			t.Errorf("%s: data 应序列化为 []（非 null），实得 %s", tc.name, rec.Body.String())
		}
	}
}

// 静态段 /api/stock/search 与通配 /api/stock/:code/... 必须互不干扰（先静态、后通配）。
func TestRouterSearchCoexistsWithCodeRoute(t *testing.T) {
	r := NewRouter(NewHandler(client.New(), "", "", ""))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stock/search?q=", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/stock/search 被 :code 路由抢占：HTTP %d %s", rec.Code, rec.Body.String())
	}

	// 短代码仍走 :code 的参数校验（不发起任何外部请求）
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stock/123/indicators", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("/api/stock/123/indicators HTTP = %d, want 400（:code 校验保留）", rec.Code)
	}
}
