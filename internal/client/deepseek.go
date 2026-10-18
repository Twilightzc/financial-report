package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"financial-report/internal/model"
)

// DeepSeek 接入 DeepSeek 大模型（OpenAI 兼容 Chat Completions 协议）。
// apikey 由请求参数传入；baseURL / model / reasoningEffort 可配置，缺省指向 DeepSeek 官方与快速模型 deepseek-flash。
type DeepSeek struct {
	hc              *http.Client
	apiKey          string
	baseURL         string
	model           string
	reasoningEffort string
}

// NewDeepSeek 构造 DeepSeek 客户端。baseURL 空则用官方地址，model 空则用快速模型 deepseek-flash，
// reasoningEffort 空则用 low（仅推理模型发送，flash 不发送该字段）。
func NewDeepSeek(apiKey, baseURL, model, reasoningEffort string) *DeepSeek {
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}
	if model == "" {
		model = "deepseek-flash"
	}
	if reasoningEffort == "" {
		reasoningEffort = "low"
	}
	return &DeepSeek{
		hc:              &http.Client{Timeout: 180 * time.Second}, // LLM 分析耗时长，超时给足
		apiKey:          apiKey,
		baseURL:         baseURL,
		model:           model,
		reasoningEffort: reasoningEffort,
	}
}

type chatMessage struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content"` // 推理模型的思考过程（仅用于诊断，不作为结果）
}

type responseFormat struct {
	Type string `json:"type"` // json_object
}

type chatRequest struct {
	Model           string          `json:"model"`
	Messages        []chatMessage   `json:"messages"`
	ResponseFormat  *responseFormat `json:"response_format,omitempty"`
	Temperature     float64         `json:"temperature"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"` // 仅推理模型发送（low/medium/high）
	MaxTokens       int             `json:"max_tokens,omitempty"`       // 输出 token 上限
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// chatMaxAttempts 大模型调用的最大尝试次数（限流/网关抖动会自动重试）。
const chatMaxAttempts = 5

// isReasoningModel 推理模型白名单：只有这些模型接受 reasoning_effort。
// 采用前缀匹配，兼容官方滚动版本（如 deepseek-v4-pro-xxxx）；未知模型一律不发送，避免参数错误。
func isReasoningModel(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(n, "deepseek-v4-pro") || strings.HasPrefix(n, "deepseek-reasoner")
}

// Chat 发送一轮对话（system + user），返回助手回复文本与 token 用量。
// 对限流(429)/5xx/网关 HTML 等临时失败自动重试（指数退避 2s、4s、8s、16s）。
func (d *DeepSeek) Chat(system, user string) (string, model.TokenUsage, error) {
	req := chatRequest{
		Model: d.model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		ResponseFormat: &responseFormat{Type: "json_object"},
		Temperature:    0.0,
		// 8192 为输出 token 上限（flash 为 CED 非推理模型，不发送 reasoning_effort）。
		MaxTokens: 8192,
	}
	// reasoning_effort 只对白名单内的推理模型发送；未知/快速模型一律不带，避免参数错误。
	if isReasoningModel(d.model) {
		req.ReasoningEffort = d.reasoningEffort
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", model.TokenUsage{}, err
	}

	var lastErr error
	for attempt := 0; attempt < chatMaxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<(attempt-1)) * 2 * time.Second) // 2s、4s、8s、16s 指数退避
		}
		content, usage, retryable, err := d.chatOnce(body)
		if err == nil {
			return content, usage, nil
		}
		lastErr = err
		if !retryable {
			break
		}
	}
	return "", model.TokenUsage{}, lastErr
}

// chatOnce 单次调用。retryable 表示是否为可重试的临时失败（限流/5xx/网关 HTML/网络抖动）。
func (d *DeepSeek) chatOnce(body []byte) (string, model.TokenUsage, bool, error) {
	httpReq, err := http.NewRequest(http.MethodPost, d.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", model.TokenUsage{}, false, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+d.apiKey)

	resp, err := d.hc.Do(httpReq)
	if err != nil {
		// 网络抖动（超时/连接重置）可重试
		return "", model.TokenUsage{}, true, fmt.Errorf("请求大模型失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", model.TokenUsage{}, true, err
	}

	if resp.StatusCode != http.StatusOK {
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return "", model.TokenUsage{}, retryable, fmt.Errorf("大模型接口返回状态码 %d（model=%s）: %s",
			resp.StatusCode, d.model, truncate(string(respBody), 300))
	}

	// 200 但内容不是 JSON（网关/代理返回的 HTML 页面）→ 视为临时失败，可重试。
	// 注意：限流通常返回 429 + JSON 错误；这里返回 HTML 通常是上游网关/代理/网络拦截，与调用频率无关。
	trimmed := strings.TrimSpace(string(respBody))
	if !strings.HasPrefix(trimmed, "{") {
		return "", model.TokenUsage{}, true, fmt.Errorf("大模型接口返回了非 JSON 内容（状态码 %d，疑似网关/代理/网络拦截，通常不是限流），响应片段：%s", resp.StatusCode, truncate(trimmed, 300))
	}

	var cr chatResponse
	if err := json.Unmarshal(respBody, &cr); err != nil {
		return "", model.TokenUsage{}, false, fmt.Errorf("解析大模型响应失败: %w", err)
	}
	if cr.Error != nil && cr.Error.Message != "" {
		return "", model.TokenUsage{}, false, fmt.Errorf("大模型返回错误: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", model.TokenUsage{}, false, fmt.Errorf("大模型返回内容为空（model=%s）", d.model)
	}

	var usage model.TokenUsage
	if cr.Usage != nil {
		usage = model.TokenUsage{
			PromptTokens:     cr.Usage.PromptTokens,
			CompletionTokens: cr.Usage.CompletionTokens,
			TotalTokens:      cr.Usage.TotalTokens,
		}
	}
	choice := cr.Choices[0]
	if choice.Message.Content == "" || choice.FinishReason == "length" {
		log.Printf("大模型输出可能被截断: model=%s finish_reason=%s completion_tokens=%d",
			d.model, choice.FinishReason, usage.CompletionTokens)
	}
	if choice.Message.Content == "" {
		if choice.Message.ReasoningContent != "" {
			return "", usage, false, fmt.Errorf("大模型只返回了推理过程、未返回最终结果（model=%s finish_reason=%s，可能被输出额度截断）",
				d.model, choice.FinishReason)
		}
		return "", usage, false, fmt.Errorf("大模型返回内容为空（model=%s finish_reason=%s）", d.model, choice.FinishReason)
	}
	return choice.Message.Content, usage, false, nil
}

// truncate 截断过长的错误信息，避免把整段响应塞进错误提示。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
