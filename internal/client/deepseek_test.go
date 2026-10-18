package client

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// testDeepSeek 构造一个走内存 RoundTripper 的客户端；handler 返回状态码与响应体。
// roundTripFunc 复用 industry_test.go 中的定义（沙箱内 httptest 无法监听端口）。
func testDeepSeek(model string, handler func(*http.Request) (int, string)) *DeepSeek {
	return &DeepSeek{
		hc: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			status, body := handler(r)
			return &http.Response{
				StatusCode: status,
				Status:     http.StatusText(status),
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    r,
			}, nil
		})},
		apiKey:          "sk-secret",
		baseURL:         "http://mock",
		model:           model,
		reasoningEffort: "low",
	}
}

// captureChat 从请求体解析 JSON，返回捕获的请求体与调用结果。
func captureChat(t *testing.T, model string) (map[string]any, string, error) {
	t.Helper()
	var body map[string]any
	d := testDeepSeek(model, func(r *http.Request) (int, string) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		return http.StatusOK, `{"choices":[{"message":{"content":"{\"ok\":true}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
	})
	content, _, err := d.Chat("system", "user")
	return body, content, err
}

// TestChatRequestReasoningEffort flash 为 CED 非推理模型，不发送 reasoning_effort；
// 推理模型发送 low；两者 max_tokens 均为 8192（输出上限）。（设计 §20.10.3）
func TestChatRequestReasoningEffort(t *testing.T) {
	flash, content, err := captureChat(t, "deepseek-flash")
	if err != nil {
		t.Fatalf("flash 调用失败：%v", err)
	}
	if content == "" {
		t.Error("flash 应返回内容")
	}
	if _, ok := flash["reasoning_effort"]; ok {
		t.Errorf("deepseek-flash 不应发送 reasoning_effort：%+v", flash)
	}
	if got := flash["max_tokens"]; got != float64(8192) {
		t.Errorf("max_tokens=%v，期望 8192", got)
	}

	pro, _, err := captureChat(t, "deepseek-v4-pro")
	if err != nil {
		t.Fatalf("pro 调用失败：%v", err)
	}
	if got := pro["reasoning_effort"]; got != "low" {
		t.Errorf("推理模型 reasoning_effort=%v，期望 low", got)
	}
	if got := pro["max_tokens"]; got != float64(8192) {
		t.Errorf("max_tokens=%v，期望 8192", got)
	}
}

// TestIsReasoningModel 推理模型前缀白名单（§20.10.3）。
func TestIsReasoningModel(t *testing.T) {
	cases := map[string]bool{
		"deepseek-v4-pro":      true,
		"deepseek-v4-pro-1017": true,
		"deepseek-reasoner":    true,
		"deepseek-flash":       false,
		"":                     false,
		"gpt-4":                false,
	}
	for name, want := range cases {
		if got := isReasoningModel(name); got != want {
			t.Errorf("isReasoningModel(%q)=%v，期望 %v", name, got, want)
		}
	}
}

// TestChatFinishReasonLength 输出被截断且无内容时，错误信息带 model 与 finish_reason。
func TestChatFinishReasonLength(t *testing.T) {
	d := testDeepSeek("deepseek-flash", func(r *http.Request) (int, string) {
		return http.StatusOK, `{"choices":[{"message":{"content":""},"finish_reason":"length"}],"usage":{"completion_tokens":8192}}`
	})
	_, _, err := d.Chat("s", "u")
	if err == nil {
		t.Fatal("空内容应返回错误")
	}
	if !strings.Contains(err.Error(), "finish_reason=length") || !strings.Contains(err.Error(), "deepseek-flash") {
		t.Errorf("错误信息应包含模型与 finish_reason：%v", err)
	}
}

// TestChatHTTPErrorHidesKey 400 错误信息包含模型名，但不泄漏 apikey。
func TestChatHTTPErrorHidesKey(t *testing.T) {
	d := testDeepSeek("deepseek-flash", func(r *http.Request) (int, string) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-secret" {
			t.Errorf("Authorization=%q", got)
		}
		return http.StatusBadRequest, `{"error":{"message":"invalid model"}}`
	})
	_, _, err := d.Chat("s", "u")
	if err == nil {
		t.Fatal("400 应返回错误")
	}
	if !strings.Contains(err.Error(), "deepseek-flash") {
		t.Errorf("错误信息应包含模型名：%v", err)
	}
	if strings.Contains(err.Error(), "sk-secret") {
		t.Errorf("错误信息不得泄漏 apikey：%v", err)
	}
}
