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

	// R13 新增：基期自由现金流选取推荐（只读，服务端确定性回填，见《技术设计文档》17.3）。
	// FCFMode / FCFModeName 不设 omitempty：算法恒产出，前端需稳定显示（镜像 model/model_name）。
	FCFMode        string  `json:"fcf_mode"`                   // latest/average/median/trim_mean/custom（恒为五选一）
	FCFModeName    string  `json:"fcf_mode_name"`              // 中文名
	FCFYears       int     `json:"fcf_years,omitempty"`        // 多年度方式的推荐年数（>0）；latest/custom 为 0 不输出
	FCFCustomValue float64 `json:"fcf_custom_value,omitempty"` // custom 展示值（单位见 fcf_custom_unit，取整到整亿）
	FCFCustomUnit  string  `json:"fcf_custom_unit,omitempty"`  // custom 单位 key（本期固定 "yi"）
	FCFRationale   string  `json:"fcf_rationale,omitempty"`    // 确定性口径说明

	// R15/P1 新增：折现率 WACC 基线化的可观测分量（服务端确定性回填，只读）。
	// 均设 omitempty：wacc_baseline / risk_premium 为 0 时省略属正常语义（如 riskPoints=0 → 无 risk_premium）。
	RiskFreeRate      float64 `json:"risk_free_rate,omitempty"`      // Rf 无风险利率 %
	EquityRiskPremium float64 `json:"equity_risk_premium,omitempty"` // ERP 股权风险溢价 %
	Beta              float64 `json:"beta,omitempty"`                // β（由行业类型常量表映射；恒为非零 → 必输出）
	BetaTier          string  `json:"beta_tier,omitempty"`           // 生效 β 档位中文名
	WaccBaseline      float64 `json:"wacc_baseline,omitempty"`       // WACC 基线 %（缺资产负债表时为 0 → 省略）
	RiskPremium       float64 `json:"risk_premium,omitempty"`        // 公司特定风险溢价 %

	// R15/P1 新增：不适用判定（Q6）。Applicable 不设 omitempty：true/false 均须出现，
	// 前端用 === false 守卫，缺字段的旧响应不会被误禁用。
	Applicable         bool   `json:"applicable"`                    // 现金流贴现法是否适用
	InapplicableReason string `json:"inapplicable_reason,omitempty"` // 不适用原因（Applicable=false 时非空）

	// R15/P1（2026-10-16 修订）：行业类型。IndustryType 双向——AI 输出时=原始分类，
	// 服务端回填时=归一化 key（cyclical/general/utility）；IndustryTypeName 为中文名（只出）。
	IndustryType     string `json:"industry_type,omitempty"`      // 归一化行业 key（服务端回填）
	IndustryTypeName string `json:"industry_type_name,omitempty"` // 行业类型中文名

	// R15/P2 新增：基准增速构成、多信号模型分档与 SGR（服务端确定性回填，只读；全部 omitempty）。
	GrowthRevenue    float64 `json:"growth_revenue,omitempty"`    // 营收稳健增速 %
	GrowthProfit     float64 `json:"growth_profit,omitempty"`     // 重构净利润稳健增速 %
	GrowthFCF        float64 `json:"growth_fcf,omitempty"`        // 基期 FCF 稳健增速 %
	GrowthDispersion float64 `json:"growth_dispersion,omitempty"` // 营收/净利润各自同比增速极差的最大值 pp
	ModelScore       float64 `json:"model_score,omitempty"`       // 多信号得分 0–100（落档前）
	SGR              float64 `json:"sgr,omitempty"`               // 可持续增长率 %（不可用为 0）
	RetentionRate    float64 `json:"retention_rate,omitempty"`    // 留存率 0–1（不可用为 0）
	ModelDegraded    bool    `json:"model_degraded,omitempty"`    // 是否发生模型降级

	// R15/P3 新增：FCF 周期识别与研发扩张（服务端确定性回填，只读；全部 omitempty）。
	FCFCyclical    bool    `json:"fcf_cyclical,omitempty"`     // 强周期（CV>0.5 或符号翻转≥2）
	FCFCV          float64 `json:"fcf_cv,omitempty"`           // FCF 变异系数（不可判定为 0）
	FCFSignChanges int     `json:"fcf_sign_changes,omitempty"` // FCF 符号翻转次数
	FCFWindowYears int     `json:"fcf_window_years,omitempty"` // 判定/推荐窗口年数（3/5/7）
	RDExpansion    float64 `json:"rd_expansion,omitempty"`     // 研发投入同比扩张额（元；未扩张/上年缺失为 0）

	// R15/P4 新增：一致性校验（C4~C7）的可观测字段（服务端确定性回填，只读；全部 omitempty）。
	BaseYear       int     `json:"base_year,omitempty"`        // C7 基期年（0 表示无共同年度）
	CapexCoverage  float64 `json:"capex_coverage,omitempty"`   // C5 购建长期资产现金 ÷ 保全性资本支出
	DeltaNWCRatio  float64 `json:"delta_nwc_ratio,omitempty"`  // ΔNWC ÷ 基期 FCF（不可算/为负为 0）
	FCFProfitRatio float64 `json:"fcf_profit_ratio,omitempty"` // C6 FCF ÷ 净利润中位数
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

	// R13：基期自由现金流选取推荐（与 R7 同源，确定性推导，无 IO / 随机 / 时间依赖）。
	FCFMode       string  // latest / average / median / trim_mean / custom（复用 service.valFCF* 常量）
	FCFModeName   string  // 中文名（复用 service.valFCFModeNames）
	FCFYears      int     // average/median/trim_mean 的推荐年数；latest/custom 为 0
	FCFCustomYuan float64 // custom 推荐值（元，整亿倍数）；非 custom 为 0
	FCFCustomUnit string  // custom 单位 key（本期固定 "yi"）；非 custom 为 ""
	FCFRationale  string  // 确定性口径说明（供展示与喂给大模型写理由）

	// R15/P1：折现率 WACC 基线化的可观测分量（只读，供展示、日志与喂给大模型写理由）。
	RiskFreeRate      float64 // Rf 无风险利率 %（常量 2.5）
	EquityRiskPremium float64 // ERP 股权风险溢价 %（常量 5.5）
	Beta              float64 // β（由 IndustryType 经常量表映射；AI 未分类时由 volatile 兜底）
	BetaTier          string  // 生效 β 档位中文名（「周期性行业」/「一般行业」/「公用事业」）
	WaccBaseline      float64 // WACC 基线 %（含债务加权，未加公司特定风险溢价；缺资产负债表时为 0）
	RiskPremium       float64 // 公司特定风险溢价 %（= riskPoints，0~3）

	// R15/P1（2026-10-16 修订）：行业类型（AI 定性分类，受约束枚举；服务端归一化后回填）。
	IndustryType     string // 归一化枚举 key：cyclical / general / utility（AI 缺省/非法时回落 general）
	IndustryTypeName string // 中文名（「周期性行业」/「一般行业」/「公用事业」）
	BetaBasis        string // β 取值依据（纯内部，无 JSON tag；供日志与单测断言 β 来源）

	// R15/P1：不适用判定（Q6）。
	Applicable         bool   // 现金流贴现法是否适用（false 时前端禁用「套用到估值」）
	InapplicableReason string // 不适用原因（Applicable=false 时非空）

	// R15/P2：基准增速构成、多信号模型分档与 SGR（只读）。
	GrowthRevenue    float64 // 营收稳健增速 %
	GrowthProfit     float64 // 重构净利润稳健增速 %
	GrowthFCF        float64 // 基期 FCF 稳健增速 %
	GrowthDispersion float64 // 营收/净利润各自同比增速极差的最大值 pp（稳定性信号）
	// 分项可用性（仅内部使用，不序列化）：提示词/日志据此区分「增速 0%」与「样本不可用」。
	GrowthRevenueOK bool
	GrowthProfitOK  bool
	GrowthFCFOK     bool
	ModelScore      float64 // 多信号得分 0–100（落档前）
	// 分项得分（仅内部使用，不序列化；供提示词解释「为何是该模型」）。
	ScoreGrowth       float64 // 增速分 sg
	ScoreStability    float64 // 稳定性分 st
	HasStabilityScore bool    // 稳定性信号是否可用
	ScoreROE          float64 // ROE 分 sr
	HasROEScore       bool    // ROE 信号是否可用
	SGR               float64 // 可持续增长率 %（不可用为 0）
	RetentionRate     float64 // 留存率 0–1（不可用为 0）
	ModelDegraded     bool    // 是否发生 three_stage→two_stage→perpetual→zero 降级

	// R15/P3：FCF 周期识别与研发扩张（只读）。
	FCFCyclical    bool    // 强周期（CV>0.5 或符号翻转≥2）
	FCFCV          float64 // FCF 变异系数（不可判定为 0）
	FCFSignChanges int     // FCF 符号翻转次数
	FCFWindowYears int     // 本次实际使用的窗口年数（3/5/7）
	RDExpansion    float64 // 研发投入同比扩张额（元；未扩张/上年缺失为 0）

	// R15/P4：一致性校验（C4~C7）的可观测字段（只读）。
	BaseYear       int     // C7 基期年（0 表示无共同年度）
	CapexCoverage  float64 // C5 购建长期资产现金 ÷ 保全性资本支出（不可算为 0）
	DeltaNWCRatio  float64 // ΔNWC ÷ 基期 FCF（不可算/为负为 0）
	FCFProfitRatio float64 // C6 FCF ÷ 净利润中位数（不可算为 0）
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
