package model

// AIScore AI 分析中单个维度的百分制打分与一句话点评。
type AIScore struct {
	Dimension string `json:"dimension"` // 盈利能力 / 偿债能力 / 现金获取能力 / 经营效率 / 成长能力
	Score     int    `json:"score"`     // 0-100
	Comment   string `json:"comment"`   // 一句话点评
}

// AIIndustry AI 输出的行业分析（结论简洁）。
type AIIndustry struct {
	Name        string `json:"name"`        // 行业名称
	Prospect    string `json:"prospect"`    // 前景
	Competition string `json:"competition"` // 竞争格局
	Application string `json:"application"` // 应用方向 / 需求
}

// AIBusiness AI 输出的业务板块分析（据主营构成 + 行业知识）。
type AIBusiness struct {
	Name         string `json:"name"`         // 业务板块名称
	Stage        string `json:"stage"`        // 发展阶段（起步 / 成长 / 成熟 / 衰退）
	Contribution string `json:"contribution"` // 营收 / 成长贡献占比（贴近实际主营构成）
	Prospect     string `json:"prospect"`     // 前景
	Risk         string `json:"risk"`         // 主要风险
}

// AIValuationParam 估值模型推荐参数，key 与估值接口（/valuation）参数对齐。
type AIValuationParam struct {
	Key   string  `json:"key"`   // g / g1 / n1 / g2 / n2 / g3（n1/n2 为年数）
	Label string  `json:"label"` // 中文名
	Value float64 `json:"value"` // 数值（增长率 %、年数为整数）
}

// AIValuation AI 推荐的估值模型与参数。
type AIValuation struct {
	Model        string             `json:"model"`         // zero / perpetual / two_stage / three_stage
	ModelName    string             `json:"model_name"`    // 中文名
	DiscountRate float64            `json:"discount_rate"` // 折现率 %
	Params       []AIValuationParam `json:"params"`        // 增长率 / 年数等参数
	Rationale    string             `json:"rationale"`     // 选择理由（简洁）
}

// AIAnalysisResult AI 分析结果（前端雷达图 + 文字结论 + 估值推荐）。
type AIAnalysisResult struct {
	Scores     []AIScore    `json:"scores"`
	Conclusion string       `json:"conclusion"` // 总体结论
	Industry   AIIndustry   `json:"industry"`   // 行业分析
	Businesses []AIBusiness `json:"businesses"` // 业务板块分析
	Valuation  AIValuation  `json:"valuation"`  // 估值模型推荐

	Usage *TokenUsage `json:"usage,omitempty"` // 本次大模型调用的 token 用量（服务端回填）
}

// TokenUsage 大模型调用的 token 用量。
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
