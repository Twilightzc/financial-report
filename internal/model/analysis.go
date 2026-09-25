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

	// R9 趋势信号灯（见技术设计 13.5/13.6）。
	// Direction 不设 omitempty：每个指标都必须带方向属性，缺字段会让「neutral」与「旧响应」不可区分。
	Direction   string `json:"direction"`              // higher_better / lower_better / neutral
	TrendSignal string `json:"trend_signal,omitempty"` // improving / worsening；无色（平稳/无信号/中性/维度非 done）时省略

	// ★决策更新 2 新增：首末相对变化率 R（FR-2 同一口径、同一次计算），**百分比口径 0–100**
	//（如 11.5 表示 +11.5%，与 model 内其余 unit=="%" 的字段同为 0–100，见 analysis.go 的 ratioPct ×100）。
	// R 不可计算（趋势 = trendNone）时为 nil；**必须是指针**：R=0（首末持平）是合法值，须输出 0，
	// 否则前端无法区分「持平」与「数据不足」。前端只把它用于措辞分级与文案，不参与信号判定（FR-16）。
	TrendRate *float64 `json:"trend_rate,omitempty"`
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
