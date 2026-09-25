package model

// ValuationParams 估值模型输入参数（增长率为百分比、年数为整数）。
// 按模型取用不同字段：
//   - zero：仅 DiscountRate。
//   - perpetual：DiscountRate + GrowthRate。
//   - two_stage：DiscountRate + Stage1Growth + Stage1Years + Stage2Growth（Stage2Growth 为稳定期永续增长率）。
//   - three_stage：DiscountRate + Stage1Growth + Stage1Years + Stage2Growth + Stage2Years + TerminalGrowth。
type ValuationParams struct {
	Model          string  // zero / perpetual / two_stage / three_stage
	DiscountRate   float64 // 折现率 %
	GrowthRate     float64 // 永续增长率 %（perpetual）
	Stage1Growth   float64 // 第一阶段增长率 %（two_stage / three_stage）
	Stage1Years    int     // 第一阶段年数
	Stage2Growth   float64 // 第二阶段增长率 %（two_stage 的稳定期 / three_stage 的第二阶段）
	Stage2Years    int     // 第二阶段年数（three_stage）
	TerminalGrowth float64 // 终值永续增长率 %（three_stage 的第三阶段）

	FCFMode  string // 基期自由现金流选取方式：latest / average / median（缺省 latest）
	FCFYears int    // 基期自由现金流选取年数（average/median 用，缺省 3）
	AdjustRD bool   // 是否调整研发费用（成长科技股）
}

// ValuationResult 公司股票估值结果（现金流贴现法，基于最新年报）。
type ValuationResult struct {
	Model        string  `json:"model"`
	ModelName    string  `json:"model_name"`
	Year         int     `json:"year"`          // 基期年报年份
	DiscountRate float64 `json:"discount_rate"` // 折现率 %

	// 输入参数回显
	GrowthRate     float64 `json:"growth_rate,omitempty"`
	Stage1Growth   float64 `json:"stage1_growth,omitempty"`
	Stage1Years    int     `json:"stage1_years,omitempty"`
	Stage2Growth   float64 `json:"stage2_growth,omitempty"`
	Stage2Years    int     `json:"stage2_years,omitempty"`
	TerminalGrowth float64 `json:"terminal_growth,omitempty"`

	// 基期自由现金流选取与研发调整
	FCFMode      string  `json:"fcf_mode"`                // 基期自由现金流选取方式
	FCFModeName  string  `json:"fcf_mode_name"`           // 中文名
	FCFYears     int     `json:"fcf_years,omitempty"`     // 选取年数（average/median）
	AdjustRD     bool    `json:"adjust_rd"`               // 是否调整研发费用
	RDAdjustment float64 `json:"rd_adjustment,omitempty"` // 研发投入扩张部分加回金额（元）

	// 中间量
	BaseFCF          float64  `json:"base_fcf"`           // 基期经营资产自由现金流（元）
	LongEquityBook   float64  `json:"long_equity_book"`   // 长期股权投资账面（元）
	LongEquityReturn *float64 `json:"long_equity_return"` // 长期股权投资收益率 %
	DebtValue        float64  `json:"debt_value"`         // 债务价值 = 有息债务（元）

	// 估值结果
	FinancialAssetValue float64 `json:"financial_asset_value"`  // 金融资产价值（元）
	LongEquityValue     float64 `json:"long_equity_value"`      // 长期股权投资价值（元）
	OperatingAssetValue float64 `json:"operating_asset_value"`  // 经营资产价值（DCF，元）
	CompanyValue        float64 `json:"company_value"`          // 公司价值（元）
	EquityValue         float64 `json:"equity_value"`           // 股权价值（元）
	EquityValuePerShare float64 `json:"equity_value_per_share"` // 每股股权价值（元）

	Notes []string `json:"notes,omitempty"`
}
