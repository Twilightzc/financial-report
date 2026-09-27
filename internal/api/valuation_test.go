package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"financial-report/internal/client"
)

// R12 自定义基期现金流的参数校验在拉数之前 fail-fast（不发外部请求），故可离线验证。
// 成功路径依赖真实报表数据，此处不覆盖。
func TestGetValuationCustomParamErrors(t *testing.T) {
	r := NewRouter(NewHandler(client.New(), "", "", ""))

	cases := []struct {
		name    string
		url     string
		wantMsg string
	}{
		{"空自定义值", "/api/stock/600519/valuation?model=zero&r=10&fcf_mode=custom&fcf_custom_value=", "请输入基期现金流"},
		{"空自定义值（未传）", "/api/stock/600519/valuation?model=zero&r=10&fcf_mode=custom", "请输入基期现金流"},
		{"三位小数", "/api/stock/600519/valuation?model=zero&r=10&fcf_mode=custom&fcf_custom_value=1.234", "格式不正确"},
		{"负号", "/api/stock/600519/valuation?model=zero&r=10&fcf_mode=custom&fcf_custom_value=-1", "格式不正确"},
		{"零", "/api/stock/600519/valuation?model=zero&r=10&fcf_mode=custom&fcf_custom_value=0", "需大于 0"},
		{"超限", "/api/stock/600519/valuation?model=zero&r=10&fcf_mode=custom&fcf_custom_value=999999999&fcf_custom_unit=yi", "过大"},
		{"未知单位", "/api/stock/600519/valuation?model=zero&r=10&fcf_mode=custom&fcf_custom_value=1&fcf_custom_unit=foo", "未知的基期现金流单位"},
		{"缺折现率优先报错", "/api/stock/600519/valuation?model=zero&fcf_mode=custom&fcf_custom_value=1", "折现率需大于 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.url, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("HTTP = %d, want 200", rec.Code)
			}
			var body struct {
				Code    int             `json:"code"`
				Message string          `json:"message"`
				Data    json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("响应非 JSON: %v (%s)", err, rec.Body.String())
			}
			if body.Code != 1 {
				t.Errorf("code = %d, want 1（%s）", body.Code, rec.Body.String())
			}
			if !strings.Contains(body.Message, tc.wantMsg) {
				t.Errorf("message = %q, want 含 %q", body.Message, tc.wantMsg)
			}
		})
	}
}
