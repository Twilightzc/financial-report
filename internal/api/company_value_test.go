package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"financial-report/internal/client"
)

// R14 公司价值接口：代码位校验在拉数之前 fail-fast（不发外部请求），故可离线验证。
// 成功路径（年份范围、数值）依赖真实报表数据，归端到端人工核验。
func TestGetCompanyValueCodeInvalid(t *testing.T) {
	r := NewRouter(NewHandler(client.New(), "", "", ""))

	// 仅校验长度（非 6 位即 400），长度达 6 位会继续拉数，故此处只取长度异常值。
	for _, code := range []string{"12345", "1234567"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stock/"+code+"/company-value", nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("code=%q HTTP = %d, want 400", code, rec.Code)
		}
		var body struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("code=%q 响应非 JSON: %v (%s)", code, err, rec.Body.String())
		}
		if body.Code == 0 {
			t.Errorf("code=%q 业务码 = 0, want 非 0", code)
		}
	}
}
