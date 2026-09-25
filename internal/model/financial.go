package model

// Quote 行情快照
type Quote struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Price       float64 `json:"price"`
	ChangePct   float64 `json:"change_pct"`
	MarketCap   float64 `json:"market_cap"`   // 总市值（元）
	TotalShares float64 `json:"total_shares"` // 总股本（股）
}

// BalanceSheet 资产负债表（标准化）
type BalanceSheet struct {
	ReportDate       string  `json:"report_date"`
	TotalAssets      float64 `json:"total_assets"`      // 资产总计
	TotalLiabilities float64 `json:"total_liabilities"` // 负债合计
	TotalEquity      float64 `json:"total_equity"`      // 所有者权益合计
}

// IncomeStatement 利润表（标准化）
type IncomeStatement struct {
	ReportDate      string  `json:"report_date"`
	Revenue         float64 `json:"revenue"`           // 营业总收入
	OperateCost     float64 `json:"operate_cost"`      // 营业成本
	ParentNetProfit float64 `json:"parent_net_profit"` // 归母净利润
}

// CashFlowStatement 现金流量表（标准化）
type CashFlowStatement struct {
	ReportDate        string  `json:"report_date"`
	OperatingCashFlow float64 `json:"operating_cash_flow"` // 经营活动现金流量净额
}

// Indicator 单个指标
type Indicator struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

// SegmentIncome 主营构成中的一项业务板块（名称 + 营收/利润占比 + 毛利率）。
type SegmentIncome struct {
	Name         string  `json:"name"`          // 板块名称
	RevenueRatio float64 `json:"revenue_ratio"` // 营收占比（0-1）
	ProfitRatio  float64 `json:"profit_ratio"`  // 主营利润占比（0-1）
	GrossMargin  float64 `json:"gross_margin"`  // 毛利率（0-1）
}

// Stock 股票基础信息
type Stock struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// StockOverview 单股分析总览（聚合返回）
type StockOverview struct {
	Stock      Stock       `json:"stock"`
	Quote      Quote       `json:"quote"`
	Indicators []Indicator `json:"indicators"`
}

// FieldDef 科目字典条目：字段 key → 中文名
type FieldDef struct {
	Key  string
	Name string
}

// FieldGroup 分组科目（用于现金流量表按活动类型拆分展示）
type FieldGroup struct {
	Title  string
	Fields []FieldDef
}

// ReportRow 单期报表原始行（仅数值字段，nil 表示该科目本期无数据）
type ReportRow struct {
	ReportDate string
	Fields     map[string]*float64
}

// StatementItem 单个科目的跨年度数值（与 Statement.Years 对齐，nil=无数据）
type StatementItem struct {
	Field  string     `json:"field"`
	Name   string     `json:"name"`
	Values []*float64 `json:"values"`
}

// StatementGroup 分组（现金流量表按经营/投资/筹资/现金等价物拆分）
type StatementGroup struct {
	Title string          `json:"title"`
	Items []StatementItem `json:"items"`
}

// Statement 单张报表：年份正序 + 全科目（平铺 Items 或分组 Groups）
type Statement struct {
	Years  []int            `json:"years"`
	Items  []StatementItem  `json:"items"`
	Groups []StatementGroup `json:"groups,omitempty"`
}

// Financials 三张表聚合返回
type Financials struct {
	Balance  Statement `json:"balance"`
	Income   Statement `json:"income"`
	Cashflow Statement `json:"cashflow"`
}
