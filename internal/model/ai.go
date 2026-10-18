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

	// R17/D10：细分业务龙头（AI 推断，0–3 家；与 related/chain/partners 不合并、不跨列表去重）。
	Leaders []CompanyRef `json:"leaders,omitempty"`
	// R17/D8：板块相关公司（AI 推断，0–3 家；与 leaders/chain/partners 不合并、不跨列表去重）。
	Related []CompanyRef `json:"related,omitempty"`
}

// AIValuationParam 估值模型推荐参数，key 与估值接口（/valuation）参数对齐。
type AIValuationParam struct {
	Key   string  `json:"key"`   // g / g1 / n1 / g2 / n2 / g3（n1/n2 为年数）
	Label string  `json:"label"` // 中文名
	Value float64 `json:"value"` // 数值（增长率 %、年数为整数）
}

// GrowthContribution 复合锚构成表的一行。金额单位均为 %，权重为 0–1。
type GrowthContribution struct {
	Key          string  `json:"key"`          // history / forward / macro
	Name         string  `json:"name"`         // 历史锚（含周期修正）/ 前瞻锚 / 宏观锚
	Value        float64 `json:"value"`        // 锚值 %，2 位小数；不可用为 0
	Weight       float64 `json:"weight"`       // 权重 0–1，2 位小数；不可用为 0
	Contribution float64 `json:"contribution"` // 锚值×权重，2 位小数
	Available    bool    `json:"available"`    // 该锚是否可用
}

// GrowthAnchorTable 复合锚 A 的可复算构成。
// 恒满足：Σ(Value×Weight) = Raw；Raw + RoundingAdjustment + RangeAdjustment = Final。
type GrowthAnchorTable struct {
	Rows               []GrowthContribution `json:"rows"`
	WeightSum          float64              `json:"weight_sum"`          // 2 位小数；正常为 1.00
	WeightOK           bool                 `json:"weight_ok"`           // 权重合计校验
	Raw                float64              `json:"raw"`                 // 加权合计，2 位小数
	Rounded            float64              `json:"rounded"`             // 0.5pp 取整后，2 位小数
	Final              float64              `json:"final"`               // 区间约束后 = A，2 位小数
	RoundingAdjustment float64              `json:"rounding_adjustment"` // Rounded − Raw
	RangeAdjustment    float64              `json:"range_adjustment"`    // Final − Rounded
	Note               string               `json:"note,omitempty"`      // 仅材料性说明
}

// CompanyRiskItem 公司特定风险项。AI 只提供 Type/Severity/Note，Premium/Label 由后端回填。
type CompanyRiskItem struct {
	Type     string  `json:"type"`           // governance/litigation/customer_concentration/liquidity/accounting_quality
	Label    string  `json:"label"`          // 中文名，后端回填
	Severity string  `json:"severity"`       // medium/high
	Premium  float64 `json:"premium"`        // 0.5 / 1.0，后端回填；合计 ≤3.0
	Note     string  `json:"note,omitempty"` // ≤30 字，不含数字/百分比
}

// CapexCoverageDetail 净资本支出 ÷ 保全性资本支出。
type CapexCoverageDetail struct {
	Available   bool    `json:"available"`
	Ratio       float64 `json:"ratio"`       // 2 位小数
	Numerator   float64 `json:"numerator"`   // 元，同窗口合计
	Denominator float64 `json:"denominator"` // 元，同窗口合计
	WindowStart int     `json:"window_start"`
	WindowEnd   int     `json:"window_end"`
	Mode        string  `json:"mode"`             // 与基期 FCF 相同的选取方式
	Note        string  `json:"note"`             // 口径与窗口
	Reason      string  `json:"reason,omitempty"` // Available=false 时非空
}

// FCFProfitDetail 基期 FCF ÷ 同期净利润。
type FCFProfitDetail struct {
	Available   bool    `json:"available"`
	Ratio       float64 `json:"ratio"`        // 2 位小数
	FCF         float64 `json:"fcf"`          // 元，等于展示的 fcf_base_value
	Profit      float64 `json:"profit"`       // 元，同窗口同聚合
	ProfitBasis string  `json:"profit_basis"` // parent / reconstructed
	Mode        string  `json:"mode"`
	WindowStart int     `json:"window_start"`
	WindowEnd   int     `json:"window_end"`
	Years       int     `json:"years"` // 有效窗口年数
	AdjustRD    bool    `json:"adjust_rd"`
	RDExpansion float64 `json:"rd_expansion"` // 元
	Note        string  `json:"note"`
	Reason      string  `json:"reason,omitempty"`
}

// FCFYearPoint 基期 FCF 分年构成（FR-1；金额单位：元）。
type FCFYearPoint struct {
	Year              int     `json:"year"`
	OperatingCash     float64 `json:"operating_cash"`     // NETCASH_OPERATE
	PreservationCapex float64 `json:"preservation_capex"` // 保全性资本支出（正数）
	FCF               float64 `json:"fcf"`                // OperatingCash − PreservationCapex
}

// AIValuation AI 推荐的估值模型与参数（R19：单一适中基准，无三档情景）。
type AIValuation struct {
	// 一、模型
	Model              string             `json:"model"`        // zero / perpetual / two_stage / three_stage
	ModelName          string             `json:"model_name"`   // 中文名；降级时含「（已降级）」
	ModelReason        string             `json:"model_reason"` // 一句理由，≤40 字
	Applicable         bool               `json:"applicable"`   // 现金流贴现法是否适用
	InapplicableReason string             `json:"inapplicable_reason,omitempty"`
	Params             []AIValuationParam `json:"params"` // 与估值接口 key 对齐；零增长为空数组

	// 二、基期 FCF
	FCFBaseValue    float64        `json:"fcf_base_value"`             // 元，含研发调整，等于 DCF 实际使用值
	FCFBaseBeforeRD float64        `json:"fcf_base_before_rd"`         // 元，研发调整前
	FCFMode         string         `json:"fcf_mode"`                   // latest/average/median/trim_mean/custom
	FCFModeName     string         `json:"fcf_mode_name"`              // 中文名
	FCFYears        int            `json:"fcf_years,omitempty"`        // 多年度方式为 N；latest/custom 省略
	FCFWindowStart  int            `json:"fcf_window_start,omitempty"` // 选取窗口首年
	FCFWindowEnd    int            `json:"fcf_window_end,omitempty"`   // 选取窗口末年
	FCFCustomValue  float64        `json:"fcf_custom_value,omitempty"` // custom 展示值（单位见 fcf_custom_unit）
	FCFCustomUnit   string         `json:"fcf_custom_unit,omitempty"`  // custom 单位 key（本期固定 "yi"）
	AdjustRD        bool           `json:"adjust_rd"`                  // 是否建议开启研发调整
	RDExpansion     float64        `json:"rd_expansion,omitempty"`     // 研发投入扩张额（元）
	RDRatio         float64        `json:"rd_ratio,omitempty"`         // 最新年报研发费用率 %
	FCFNote         string         `json:"fcf_note"`                   // 选取方式/窗口/研发调整，一句
	FCFSeries       []FCFYearPoint `json:"fcf_series,omitempty"`       // 最近 ≤10 年，升序，元

	// 三、折现率
	DiscountRate               float64           `json:"discount_rate"`                // 最终 r，%
	RiskFreeRate               float64           `json:"risk_free_rate"`               // Rf，%
	EquityRiskPremium          float64           `json:"equity_risk_premium"`          // ERP，%
	Beta                       float64           `json:"beta"`                         // 0.8/1.0/1.2
	BetaBasis                  string            `json:"beta_basis"`                   // industry_classification/volatility_fallback/default
	IndustryType               string            `json:"industry_type"`                // cyclical/general/utility
	IndustryTypeName           string            `json:"industry_type_name"`           // 中文名
	CompanyRiskPremium         float64           `json:"company_risk_premium"`         // 0–3.0，只进 Re
	CompanyRiskItems           []CompanyRiskItem `json:"company_risk_items"`           // 逐项列示；无则空数组
	CompanyRiskNote            string            `json:"company_risk_note,omitempty"`  // 截断/去重说明
	EquityCost                 float64           `json:"equity_cost"`                  // Re，%，2 位小数
	EquityValue                float64           `json:"equity_value"`                 // E，元
	DebtValue                  float64           `json:"debt_value"`                   // D，元（负值已按 0）
	EquitySource               string            `json:"equity_source"`                // market_cap/book_equity/invalid
	EquitySourceNote           string            `json:"equity_source_note,omitempty"` // 回落说明
	EquityWeight               float64           `json:"equity_weight"`                // E/V，0–1，4 位小数
	DebtWeight                 float64           `json:"debt_weight"`                  // D/V，0–1，4 位小数
	WeightNote                 string            `json:"weight_note,omitempty"`
	DebtCostPretax             float64           `json:"debt_cost_pretax"`             // Rd，%
	AfterTaxDebtCost           float64           `json:"after_tax_debt_cost"`          // Rd×(1−t)，%
	EffectiveTaxRate           float64           `json:"effective_tax_rate"`           // t，%
	DebtCostEstimated          bool              `json:"debt_cost_estimated"`          // Rd 是否走估计
	DiscountBaseValue          float64           `json:"discount_base_value"`          // 取整前 WACC（或回落 Re），%
	DiscountBaseLabel          string            `json:"discount_base_label"`          // WACC / Re / default
	DiscountRounded            float64           `json:"discount_rounded"`             // 0.5pp 取整后，%
	DiscountRoundingAdjustment float64           `json:"discount_rounding_adjustment"` // pp
	DiscountRangeAdjustment    float64           `json:"discount_range_adjustment"`    // pp
	DiscountReconcileAvailable bool              `json:"discount_reconcile_available"` // 是否可展示对账等式
	DiscountNote               string            `json:"discount_note,omitempty"`      // 区间约束/回落/无债务等

	// 四、增长参数
	GrowthAnchor           GrowthAnchorTable `json:"growth_anchor"`
	GrowthHistoryBase      float64           `json:"growth_history_base"`     // 历史锚周期修正前，%
	GrowthHistoryAdjusted  float64           `json:"growth_history_adjusted"` // 历史锚周期修正后，%
	GrowthCycleAdjust      float64           `json:"growth_cycle_adjust"`     // pp，1 位小数
	GrowthCycleNote        string            `json:"growth_cycle_note,omitempty"`
	GrowthForward          float64           `json:"growth_forward"`            // %；不可用为 0
	GrowthForwardAvailable bool              `json:"growth_forward_available"`  // 前瞻锚是否可用
	GrowthMacro            float64           `json:"growth_macro"`              // %
	GrowthMacroBasis       string            `json:"growth_macro_basis"`        // dynamic_gdp/fallback_constant
	GDPAsOf                string            `json:"gdp_as_of,omitempty"`       // 动态 GDP 快照季末
	ConsensusAsOf          string            `json:"consensus_as_of,omitempty"` // 一致预期快照日期
	ConsensusInstitutions  int               `json:"consensus_institutions,omitempty"`
	ConsensusYears         int               `json:"consensus_years,omitempty"`
	ConsensusReliability   string            `json:"consensus_reliability,omitempty"` // high/medium/unavailable
	GrowthTerminal         float64           `json:"growth_terminal"`                 // g∞，%
	TerminalStability      float64           `json:"terminal_stability,omitempty"`    // 稳定度 0–1，2 位小数；新计算恒输出
	TerminalConstraint     string            `json:"terminal_constraint"`             // stability/sgr/discount/zero；cyclical/macro 仅历史兼容
	TerminalNote           string            `json:"terminal_note,omitempty"`
	SGR                    float64           `json:"sgr,omitempty"`                  // 仅在终值受 SGR 约束时输出；语义为近 N 年平均
	RetentionRate          float64           `json:"retention_rate,omitempty"`       // 平均留存率 0–1
	RetentionBasis         string            `json:"retention_basis,omitempty"`      // payout/equity_growth/mixed/unavailable
	SGRWindowYears         int               `json:"sgr_window_years,omitempty"`     // 实际有效年度数；回落单年=1，不可用省略
	NegativeGrowthType     string            `json:"negative_growth_type,omitempty"` // structural/cyclical
	NegativeGrowthNote     string            `json:"negative_growth_note,omitempty"`
	G1Constraint           string            `json:"g1_constraint,omitempty"` // none/upper/terminal/cap_below_terminal
	G1Note                 string            `json:"g1_note,omitempty"`

	// 五、关键校验
	CapexCheck     CapexCoverageDetail `json:"capex_check"`
	FCFProfitCheck FCFProfitDetail     `json:"fcf_profit_check"`

	// 六、结论与材料性警告
	BaseYear  int      `json:"base_year"`
	Warnings  []string `json:"warnings,omitempty"`
	Rationale string   `json:"rationale"` // 仅定性理由（AI 生成后清洗）

	// AI 输入通道：仅用于解析，Apply 后必须清空，不出现在最终响应。
	CompanyRisks []CompanyRiskItem `json:"company_risks,omitempty"`
}

// ValuationRecommendation 由财报数据确定性推导的估值参数推荐（纯计算结果，无 IO、无随机）。
type ValuationRecommendation struct {
	// 对外字段同名同义（见 ApplyValuationRecommendation 映射）。
	Model              string
	ModelName          string
	ModelReason        string
	DiscountRate       float64
	Params             []AIValuationParam
	Applicable         bool
	InapplicableReason string

	// 折现率内部量
	EquityCost                 float64 // Re %
	EquityWeight               float64 // wE 0–1
	DebtWeight                 float64 // wD 0–1
	DebtCostPretax             float64 // Rd_pre %
	AfterTaxDebtCost           float64 // Rd_after %
	EffectiveTaxRate           float64 // t，0–1；对外乘 100
	DebtCostEstimated          bool    // Rd 是否走保守估计
	DiscountBaseValue          float64 // 取整前 WACC（或回落 Re）%
	DiscountBaseLabel          string  // WACC / Re / default
	DiscountRounded            float64 // 0.5pp 取整后 %
	DiscountRoundingAdjustment float64 // pp
	DiscountRangeAdjustment    float64 // pp
	DiscountReconcileAvailable bool
	DiscountNote               string
	WeightNote                 string
	EquityValue                float64 // E，元
	DebtValue                  float64 // D，元（负值已按 0）
	EquitySource               string  // market_cap/book_equity/invalid
	EquitySourceNote           string
	CompanyRiskItems           []CompanyRiskItem
	CompanyRiskPremium         float64
	CompanyRiskNote            string
	Beta                       float64
	BetaTier                   string // 生效 β 档位（内部）
	BetaBasis                  string // industry_classification/volatility_fallback/default
	IndustryType               string // cyclical/general/utility
	IndustryTypeName           string

	// 增长内部量
	GrowthAnchor          GrowthAnchorTable
	GrowthHistoryBase     float64
	GrowthHistoryAdjusted float64
	GrowthCycleAdjust     float64
	GrowthCycleNote       string
	GrowthForward         float64
	GrowthForwardOK       bool
	GrowthMacro           float64
	GrowthMacroBasis      string
	GDPAsOf               string
	ConsensusAsOf         string
	ConsensusInstitutions int
	ConsensusYears        int
	ConsensusReliability  string
	GrowthTerminal        float64
	TerminalStability     float64 // 稳定度 0–1，完整精度；Apply 时 round2
	TerminalConstraint    string
	TerminalNote          string
	SGR                   float64
	RetentionRate         float64
	RetentionBasis        string
	SGRWindowYears        int // 有效年度数；SGR 不可用时为 0
	NegativeGrowthType    string
	NegativeGrowthNote    string
	G1Constraint          string
	G1Note                string

	// 仅内部：模型选择与日志
	ModelScore        float64
	ScoreGrowth       float64
	ScoreStability    float64
	ScoreROE          float64
	HasStabilityScore bool
	HasROEScore       bool
	ModelDegraded     bool
	GrowthRevenue     float64
	GrowthProfit      float64
	GrowthFCF         float64
	GrowthDispersion  float64
	GrowthRevenueOK   bool
	GrowthProfitOK    bool
	GrowthFCFOK       bool
	FCFCyclical       bool
	FCFCV             float64
	FCFSignChanges    int
	FCFWindowYears    int

	// FCF
	FCFBaseValue    float64
	FCFBaseBeforeRD float64
	RDExpansion     float64
	FCFMode         string
	FCFModeName     string
	FCFYears        int
	FCFCustomYuan   float64
	FCFCustomUnit   string
	AdjustRD        bool
	RDRatio         float64
	FCFSeries       []FCFYearPoint
	FCFWindowStart  int
	FCFWindowEnd    int
	FCFNote         string

	CapexCheck     CapexCoverageDetail
	FCFProfitCheck FCFProfitDetail
	BaseYear       int
	Warnings       []string
}

// LeaderCompany 行业龙头（R17：后端确定性回填，非 AI 输出）。
type LeaderCompany struct {
	Name         string  `json:"name"`                     // 证券简称（已 trim；为空的行在 client 层丢弃）
	Code         string  `json:"code"`                     // 6 位数字（非 6 位在 client 层丢弃）
	Market       string  `json:"market"`                   // sh / sz / bj（复用 client.quotePrefix）
	MarketLabel  string  `json:"market_label"`             // 沪 / 深 / 京（复用 client.marketLabel）
	TotalCap     float64 `json:"total_cap,omitempty"`      // 总市值（元）；缺失为 0 不输出
	TotalCapRank int     `json:"total_cap_rank,omitempty"` // 同行总市值排名（从 1 开始）；缺失时按排序下标+1
}

// LeaderBoard 行业龙头榜（leaders 容器）。
type LeaderBoard struct {
	IndustryCode string `json:"industry_code,omitempty"` // DMSK 原始值，实测为 "1277"（不是 "BK1277"）
	IndustryName string `json:"industry_name,omitempty"` // 实测 "白酒Ⅱ"
	// D10：自身位于去重排序榜前 3 时=有效排名（>0）；否则 0，omitempty 省略。
	SelfRank  int             `json:"self_rank,omitempty"`
	Companies []LeaderCompany `json:"companies"` // 0–3 家，按总市值降序，不含自身；非 nil（空数组）
}

// CompanyRef AI 推断的产业链/合作公司（名称是唯一展示主键，Code 仅作精确同名消歧）。
type CompanyRef struct {
	Name     string `json:"name"`               // 证券简称，≤30 字
	Code     string `json:"code,omitempty"`     // 可选；仅保留 6 位数字，前端不展示、不直接跳转
	Market   string `json:"market,omitempty"`   // A股 / 非A股；清洗后恒为二者之一
	Relation string `json:"relation,omitempty"` // 关系标签，≤20 字
	Note     string `json:"note,omitempty"`     // 一句话说明，≤30 字
}

// AIChain 产业链上下游（AI 推断）。
type AIChain struct {
	Upstream   []CompanyRef `json:"upstream,omitempty"`   // 上游供应商侧，0–3 家
	Downstream []CompanyRef `json:"downstream,omitempty"` // 下游客户/渠道侧，0–3 家
}

// AIAnalysisResult AI 分析结果（前端雷达图 + 文字结论 + 估值推荐）。
type AIAnalysisResult struct {
	Scores     []AIScore    `json:"scores"`
	Conclusion string       `json:"conclusion"` // 总体结论
	Industry   AIIndustry   `json:"industry"`   // 行业分析
	Businesses []AIBusiness `json:"businesses"` // 业务板块分析
	Valuation  AIValuation  `json:"valuation"`  // 估值模型推荐

	// R17：相关公司（只增不改；旧缓存/旧响应缺字段时前端按空处理）
	Leaders  *LeaderBoard `json:"leaders,omitempty"`  // 后端确定性回填；无有效龙头且自身不在前 3 时为 nil（整块不输出）
	Chain    *AIChain     `json:"chain,omitempty"`    // AI 输出；上下游都为空时为 nil
	Partners []CompanyRef `json:"partners,omitempty"` // AI 输出；为空时不输出

	Usage *TokenUsage `json:"usage,omitempty"` // 本次大模型调用的 token 用量（服务端回填）
}

// TokenUsage 大模型调用的 token 用量。
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
