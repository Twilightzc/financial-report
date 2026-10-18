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

// ValuationScenario 单档情景参数（Q2：P0 只输出参数，不含股权价值）。
type ValuationScenario struct {
	Key                string             `json:"key"`                            // conservative / base / optimistic
	Name               string             `json:"name"`                           // 保守 / 基准 / 乐观
	DiscountRate       float64            `json:"discount_rate"`                  // r_s，%
	Model              string             `json:"model"`                          // zero / perpetual / two_stage / three_stage
	ModelName          string             `json:"model_name"`                     // 中文名
	GrowthAnchor       float64            `json:"growth_anchor"`                  // A_s，%
	TerminalGrowth     float64            `json:"terminal_growth"`                // g∞_s，%
	DeltaGrowth        float64            `json:"delta_growth"`                   // A_s − A_base，pp；不 omitempty
	DeltaDiscount      float64            `json:"delta_discount"`                 // r_s − r_base，pp；不 omitempty
	Params             []AIValuationParam `json:"params"`                         // 该档 g1/n1/g2/n2/g3
	Note               string             `json:"note,omitempty"`                 // 生效约束/退化说明
	CappedBy           []string           `json:"capped_by"`                      // gdp/sgr/discount/range；无约束为空数组
	ModelScore         float64            `json:"model_score"`                    // 该档得分（Q10/FR-32）
	ModelDegraded      bool               `json:"model_degraded"`                 // 该档是否发生降级
	GrowthFadeYears    int                `json:"growth_fade_years,omitempty"`    // n_total
	NegativeGrowthType string             `json:"negative_growth_type,omitempty"` // structural/cyclical；非负增长省略
	// R15/FR-38：三档基期 FCF（元，纯展示，不参与每档估值计算）。
	BaseFCF      float64 `json:"base_fcf"`       // 该档基期 FCF（保守=min(base,median)、乐观=max(base,latest)）
	BaseFCFBasis string  `json:"base_fcf_basis"` // 取值依据（当前推荐/近年中位数/最近一年/…）
}

// GrowthWeights 复合锚三权重（FR-19）。三者之和为 1，或数据不足时全 0。
type GrowthWeights struct {
	History float64 `json:"history"`
	Forward float64 `json:"forward"`
	Macro   float64 `json:"macro"`
}

// FCFYearPoint 基期 FCF 分年构成（FR-1；金额单位：元）。
type FCFYearPoint struct {
	Year              int     `json:"year"`
	OperatingCash     float64 `json:"operating_cash"`     // NETCASH_OPERATE
	PreservationCapex float64 `json:"preservation_capex"` // 保全性资本支出（正数）
	FCF               float64 `json:"fcf"`                // OperatingCash − PreservationCapex
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
	FCFCyclical    bool    `json:"fcf_cyclical,omitempty"`     // 生效强周期（原始判据 && !成长性否决）
	FCFCV          float64 `json:"fcf_cv,omitempty"`           // FCF 变异系数（不可判定为 0）
	FCFSignChanges int     `json:"fcf_sign_changes,omitempty"` // FCF 符号翻转次数
	FCFWindowYears int     `json:"fcf_window_years,omitempty"` // 判定/推荐窗口年数（3/5/10）
	RDExpansion    float64 `json:"rd_expansion,omitempty"`     // 研发投入同比扩张额（元；未扩张/上年缺失为 0）

	// R15/P4 新增：一致性校验（C4~C7）的可观测字段（服务端确定性回填，只读；全部 omitempty）。
	BaseYear       int     `json:"base_year,omitempty"`        // C7 基期年（0 表示无共同年度）
	CapexCoverage  float64 `json:"capex_coverage,omitempty"`   // C5 购建长期资产现金 ÷ 保全性资本支出
	DeltaNWCRatio  float64 `json:"delta_nwc_ratio,omitempty"`  // ΔNWC ÷ 基期 FCF（不可算/为负为 0）
	FCFProfitRatio float64 `json:"fcf_profit_ratio,omitempty"` // C6 FCF ÷ 净利润中位数

	// R15/P5 新增：成长型 FCF 噪声修复（服务端确定性回填，只读；全部 omitempty）。
	// GrowthFCFExcluded 用反向命名：Go 的 omitempty 会省略 false，反向字段才能让「未计入」显式出现。
	GrowthFCFExcluded        bool    `json:"growth_fcf_excluded,omitempty"`        // FCF 有增速但未计入 X
	GrowthFCFExcludeReason   string  `json:"growth_fcf_exclude_reason,omitempty"`  // 未计入原因
	GrowthDownsideDispersion float64 `json:"growth_downside_dispersion,omitempty"` // 下行离散度 pp（稳定性分输入）
	FCFCyclicalRaw           bool    `json:"fcf_cyclical_raw,omitempty"`           // 原始周期判据（被成长性否决时为 true）
	FCFGrowthVeto            bool    `json:"fcf_growth_veto,omitempty"`            // 成长性豁免标记
	GrowthVetoBasis          string  `json:"growth_veto_basis,omitempty"`          // 成长性判定依据

	// R18 新增：复合增长锚、周期位置、负增长归因、一致预期快照（只读，服务端确定性回填）。
	GrowthHistory         float64 `json:"growth_history,omitempty"`         // 历史锚 A_hist（周期修正前，%）
	GrowthForward         float64 `json:"growth_forward,omitempty"`         // 前瞻锚 A_fwd（%），不可用省略
	GrowthMacro           float64 `json:"growth_macro,omitempty"`           // 宏观锚 A_macro（%）
	GrowthMacroBasis      string  `json:"growth_macro_basis,omitempty"`     // dynamic_gdp / fallback_constant
	GDPAsOf               string  `json:"gdp_as_of,omitempty"`              // 动态 GDP 快照季末；回退常量时省略
	GrowthComposite       float64 `json:"growth_composite,omitempty"`       // 复合锚 A（%）
	GrowthNormalized      float64 `json:"growth_normalized,omitempty"`      // A_norm（=A，%）
	GrowthCycleAdjust     float64 `json:"growth_cycle_adjust,omitempty"`    // Δ_cycle（pp），0 省略
	CyclePosition         float64 `json:"cycle_position,omitempty"`         // [-0.5,0.5]
	CyclePositionLabel    string  `json:"cycle_position_label,omitempty"`   // 高位/中位/低位
	CycleBasis            string  `json:"cycle_basis,omitempty"`            // 周期判定与位置口径
	NegativeGrowthType    string  `json:"negative_growth_type,omitempty"`   // none/structural/cyclical
	NegativeGrowthBasis   string  `json:"negative_growth_basis,omitempty"`  // 归因判据
	ConsensusAsOf         string  `json:"consensus_as_of,omitempty"`        // 一致预期快照日期
	ConsensusInstitutions int     `json:"consensus_institutions,omitempty"` // 覆盖机构数
	ConsensusYears        int     `json:"consensus_years,omitempty"`        // 预测年数
	ConsensusReliability  string  `json:"consensus_reliability,omitempty"`  // high/medium/unavailable
	GrowthFadeYears       int     `json:"growth_fade_years,omitempty"`      // n_total（回归年限）
	SGRRole               string  `json:"sgr_role,omitempty"`               // long_run_reference
	RetentionBasis        string  `json:"retention_basis,omitempty"`        // payout/equity_growth/unavailable
	GrowthAdjustment      string  `json:"growth_adjustment,omitempty"`      // 实际生效的调整及原因

	// R15 整合版（2026-10-18）：增长可观测、折现率分步、基期 FCF 可解释、三档情景。
	// 不设 omitempty 的是契约字段：0 / 空 / 全 0 权重均为显式语义，缺字段会让前端无法区分。
	ScenarioMethod         string              `json:"scenario_method"`               // 带宽算法版本 anchor_disagreement_v1
	ScenarioBandGrowth     float64             `json:"scenario_band_growth"`          // 增长率带宽 pp（1 位小数，0 也输出）
	ScenarioBandDiscount   float64             `json:"scenario_band_discount"`        // 折现率带宽 pp（固定 1.0）
	ScenarioTerminalAdjust float64             `json:"scenario_terminal_adjust"`      // 终值增长率带宽 pp（固定 0.5）
	ScenarioBandSource     string              `json:"scenario_band_source"`          // disagreement / fallback / degenerate
	ScenarioBandRaw        float64             `json:"scenario_band_raw"`             // clamp 前的原始分歧/回退带宽（pp）
	Scenarios              []ValuationScenario `json:"scenarios"`                     // 恒 3 个：保守/基准/乐观
	GrowthWeights          GrowthWeights       `json:"growth_weights"`                // 复合锚三权重（全 0 表示数据不足）
	GrowthAnchorCount      int                 `json:"growth_anchor_count"`           // 有效独立锚数 0–3
	GrowthDamping          float64             `json:"growth_damping"`                // g1 实际折减 pp（0 表示未折减）
	GrowthCap              float64             `json:"growth_cap"`                    // g1 软上限 %
	GrowthEnvelope         float64             `json:"growth_envelope"`               // 包络 ±pp（5 或 3）
	GrowthTerminal         float64             `json:"growth_terminal"`               // 基准 g∞ %
	ScoreGrowth            float64             `json:"score_growth"`                  // 增速分 sg（0–100，2 位）
	ScoreStability         float64             `json:"score_stability"`               // 稳定性分 st（不可用时仍为 0）
	HasStabilityScore      bool                `json:"has_stability_score"`           // 稳定性信号是否可用
	ScoreROE               float64             `json:"score_roe"`                     // ROE 分 sr
	HasROEScore            bool                `json:"has_roe_score"`                 // ROE 信号是否可用
	EquityCost             float64             `json:"equity_cost,omitempty"`         // Re %
	DebtWeight             float64             `json:"debt_weight,omitempty"`         // wD 0–1（无债务时不输出）
	EquityWeight           float64             `json:"equity_weight,omitempty"`       // wE 0–1
	DebtCostPretax         float64             `json:"debt_cost_pretax,omitempty"`    // Rd_pre %
	AfterTaxDebtCost       float64             `json:"after_tax_debt_cost,omitempty"` // Rd_after %
	EffectiveTaxRate       float64             `json:"effective_tax_rate,omitempty"`  // t，百分数（0–100）
	DebtCostEstimated      bool                `json:"debt_cost_estimated,omitempty"` // Rd 是否走了 FR-12 保守估计
	FCFSeries              []FCFYearPoint      `json:"fcf_series,omitempty"`          // 最近 ≤10 年，升序，单位元
	FCFBaseValue           float64             `json:"fcf_base_value,omitempty"`      // 最终选中模式的基期 FCF（元）
	FCFFormulaNote         string              `json:"fcf_formula_note,omitempty"`    // 固定口径说明
	FCFWindowStart         int                 `json:"fcf_window_start,omitempty"`    // 推荐窗口首年
	FCFWindowEnd           int                 `json:"fcf_window_end,omitempty"`      // 推荐窗口末年
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
	FCFCyclical    bool    // 生效强周期（原始判据 && !成长性否决）
	FCFCV          float64 // FCF 变异系数（不可判定为 0）
	FCFSignChanges int     // FCF 符号翻转次数
	FCFWindowYears int     // 本次实际使用的窗口年数（3/5/10）
	RDExpansion    float64 // 研发投入同比扩张额（元；未扩张/上年缺失为 0）

	// R15/P4：一致性校验（C4~C7）的可观测字段（只读）。
	BaseYear       int     // C7 基期年（0 表示无共同年度）
	CapexCoverage  float64 // C5 购建长期资产现金 ÷ 保全性资本支出（不可算为 0）
	DeltaNWCRatio  float64 // ΔNWC ÷ 基期 FCF（不可算/为负为 0）
	FCFProfitRatio float64 // C6 FCF ÷ 净利润中位数（不可算为 0）

	// R15/P5：成长型 FCF 噪声修复（只读；前 5 项为内部字段，无 JSON tag）。
	GrowthFCFIncluded        bool    // FCF 分项是否计入 X（与 GrowthFCFOK 的「数据可算」区分）
	GrowthFCFExcludeReason   string  // 未计入原因（计入时为空）
	GrowthDownsideDispersion float64 // 下行离散度 pp（稳定性分输入）
	FCFRawCyclical           bool    // FCF 原始周期判据（CV/符号翻转）
	FCFGrowthVeto            bool    // 成长性否决是否命中
	GrowthVetoBasis          string  // 成长性判定口径说明

	// R18：复合增长锚与可观测字段（供 Apply / prompt / 日志使用；无 JSON tag）。
	GrowthHistory         float64
	GrowthForward         float64
	GrowthForwardOK       bool
	GrowthMacro           float64
	GrowthMacroBasis      string
	GDPAsOf               string
	GrowthComposite       float64
	GrowthNormalized      float64
	GrowthCycleAdjust     float64
	CyclePosition         float64
	CyclePositionLabel    string
	CycleBasis            string
	NegativeGrowthType    string
	NegativeGrowthBasis   string
	ConsensusFetched      bool
	ConsensusAsOf         string
	ConsensusInstitutions int
	ConsensusYears        int
	ConsensusReliability  string
	GrowthFadeYears       int
	SGRRole               string
	RetentionBasis        string
	GrowthAdjustment      string
	GrowthTerminal        float64 // g∞，仅内部/prompt 使用
	GrowthCap             float64 // g1_cap，仅内部/prompt/日志使用

	// R15 整合版（2026-10-18）：三档情景与新增可观测分量（无 JSON tag，经 Apply 回填）。
	Scenarios              []ValuationScenario
	ScenarioMethod         string  // 常量 anchor_disagreement_v1
	ScenarioBandGrowth     float64 // band_g，pp
	ScenarioBandDiscount   float64 // band_r，pp（1.0）
	ScenarioTerminalAdjust float64 // band_terminal，pp（0.5）
	ScenarioBandSource     string  // disagreement / fallback / degenerate
	ScenarioBandRaw        float64 // clamp 前的原始分歧/回退带宽（pp）
	GrowthWeights          GrowthWeights
	GrowthAnchorCount      int
	GrowthDamping          float64 // dampedCandidate 返回的实际折减 pp
	GrowthEnvelope         float64 // 5 或 3 pp
	EquityCost             float64 // Re %
	DebtWeight             float64 // wD 0–1
	EquityWeight           float64 // wE 0–1
	DebtCostPretax         float64 // Rd_pre %
	AfterTaxDebtCost       float64 // Rd_after %
	EffectiveTaxRate       float64 // t，0–1（对外乘 100）
	DebtCostEstimated      bool    // Rd 是否走保守估计
	FCFSeries              []FCFYearPoint
	FCFBaseValue           float64 // 最终选中模式下、套用后的基期 FCF（元）
	FCFFormulaNote         string
	FCFWindowStart         int
	FCFWindowEnd           int
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
