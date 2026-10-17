package model

// ValuationRecord 一条估值历史记录（计算时点快照）。
// 金额单位一律为元；不适用的可选字段为 nil（JSON null）。
type ValuationRecord struct {
	Code      string `json:"code"`      // 6 位代码，唯一键
	Name      string `json:"name"`      // 公司名，缺失为空串
	Market    string `json:"market"`    // sh / sz / bj
	CreatedAt string `json:"createdAt"` // 服务端写入时刻，ISO 8601 UTC（RFC3339）
	Source    string `json:"source"`    // manual / ai
	Year      *int   `json:"year"`      // 基期年报年份

	Model     string `json:"model"`     // zero/perpetual/two_stage/three_stage
	ModelName string `json:"modelName"` // 模型中文名

	DiscountRate   *float64 `json:"discountRate"`   // 折现率 %
	GrowthRate     *float64 `json:"growthRate"`     // 永续增长率 %（perpetual）
	Stage1Growth   *float64 `json:"stage1Growth"`   // 第一阶段增长率 %
	Stage1Years    *int     `json:"stage1Years"`    // 第一阶段年数
	Stage2Growth   *float64 `json:"stage2Growth"`   // 第二阶段增长率 %
	Stage2Years    *int     `json:"stage2Years"`    // 第二阶段年数
	TerminalGrowth *float64 `json:"terminalGrowth"` // 三阶段终值增长率 %

	FCFMode     string `json:"fcfMode"`     // latest/average/median/trim_mean/custom
	FCFModeName string `json:"fcfModeName"` // 中文名
	FCFYears    *int   `json:"fcfYears"`    // 选取年数（平均值/中位数/去极值）
	AdjustRD    bool   `json:"adjustRD"`    // 研发调整开关

	RDAdjustment   *float64 `json:"rdAdjustment"`   // 研发扩张加回金额（元）
	FCFCustomValue *float64 `json:"fcfCustomValue"` // 自定义基期 FCF 的元值
	FCFCustomUnit  *string  `json:"fcfCustomUnit"`  // yuan/qian/wan/yi

	BaseFCF             *float64 `json:"baseFCF"`             // 基期经营资产自由现金流（元）
	FinancialAssetValue *float64 `json:"financialAssetValue"` // 金融资产价值（元）
	LongEquityValue     *float64 `json:"longEquityValue"`     // 长期股权投资价值（元）
	OperatingAssetValue *float64 `json:"operatingAssetValue"` // 经营资产价值 DCF（元）
	CompanyValue        *float64 `json:"companyValue"`        // 公司价值（元）
	DebtValue           *float64 `json:"debtValue"`           // 债务价值/有息债务（元）
	EquityValue         *float64 `json:"equityValue"`         // 股权价值（元）
	EquityValuePerShare *float64 `json:"equityValuePerShare"` // 每股股权价值（元）
}
