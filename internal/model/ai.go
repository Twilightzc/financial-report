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

	// R7 新增：由财报数据确定性回填的只读信息（omitempty 保证旧客户端不因缺字段报错）。
	// BaseGrowth 不设 omitempty：X=0（数据不足或增长持平时）也须输出，前端才能显示「基准增速 0%」。
	BaseGrowth     float64 `json:"base_growth"`                // 基准增速 X%
	BaseGrowthNote string  `json:"base_growth_note,omitempty"` // 基准增速口径说明
	Volatile       bool    `json:"volatile,omitempty"`         // 是否剧烈波动（g1 按基准增速 50% 取）
	Warning        string  `json:"warning,omitempty"`          // 套用提示（如基期 FCF ≤ 0）

	// R8 新增：研发调整建议（只读，服务端确定性回填）。
	// AdjustRD 不设 omitempty：true/false 均须出现，缺字段会让客户端无法与「旧响应」区分。
	AdjustRD bool    `json:"adjust_rd"`          // 是否建议开启研发调整
	RDRatio  float64 `json:"rd_ratio,omitempty"` // 最新年报研发费用率 %（无法判定 / 恰为 0 时省略）
}

// ValuationRecommendation 由财报数据确定性推导的估值参数推荐（纯计算结果，无 IO、无随机）。
type ValuationRecommendation struct {
	Model          string             // zero / perpetual / two_stage / three_stage
	ModelName      string             // 中文名（复用 service.valuationModelNames）
	DiscountRate   float64            // 折现率 %（8.0–12.0，0.5 一档）
	Params         []AIValuationParam // 模型对应的增长率/年数参数（zero 为空切片）
	BaseGrowth     float64            // 基准增速 X%（%）
	BaseGrowthNote string             // 基准增速口径说明（FR-7）
	Volatile       bool               // 近三年同比增速极差是否 > 40 个百分点
	RiskPoints     int                // 折现率风险点计数 0–3（可观测用）
	Warning        string             // 套用后估值可能不适用的提示（FR-8）
	AdjustRD       bool               // R8：是否建议开启研发调整（最新年报研发费用率 > rdAdjustThreshold）
	RDRatio        float64            // R8：最新年报研发费用率 %（与接口字段同值，无法判定时为 0）
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
