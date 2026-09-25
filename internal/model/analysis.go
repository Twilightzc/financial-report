package model

// AnalysisIndicator 单个分析指标（跨年度数值，与 FinancialAnalysis.Years 对齐，nil=无数据）。
type AnalysisIndicator struct {
	Key            string     `json:"key"`
	Name           string     `json:"name"`
	Values         []*float64 `json:"values"`
	Unit           string     `json:"unit"`                     // 元/%/倍
	Kind           string     `json:"kind,omitempty"`           // ""=普通 / "sub"=子项 / "subtotal"=小计 / "net"=净额
	Interpretation string     `json:"interpretation,omitempty"` // 指标解读
	Note           string     `json:"note,omitempty"`           // 异常标注（如极端值被截断/回落的提示）
}

// AnalysisSection 分析维度下的小节（如投资活动现金流入/流出、长期经营资产、并购活动等）。
type AnalysisSection struct {
	Title      string              `json:"title"`
	Indicators []AnalysisIndicator `json:"indicators"`
}

// AnalysisDimension 一个分析维度（六大维度之一）。
// Status：done=已实现；pending=待补充（指标口径待用户给出）；no_data=已实现但无年报数据。
type AnalysisDimension struct {
	Key      string            `json:"key"`
	Name     string            `json:"name"`
	Status   string            `json:"status"`
	Sections []AnalysisSection `json:"sections"`
	Notes    []string          `json:"notes,omitempty"` // 重分类口径提示（如自由裁量科目占比过大被排除）
}

// FinancialAnalysis 六维财务指标分析聚合返回（年份升序）。
type FinancialAnalysis struct {
	Years      []int               `json:"years"`
	Dimensions []AnalysisDimension `json:"dimensions"`
}
