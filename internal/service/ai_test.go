package service

import (
	"strings"
	"testing"

	"financial-report/internal/model"
)

func f64(v float64) *float64 { return &v }

func sampleAnalysis() model.FinancialAnalysis {
	return model.FinancialAnalysis{
		Years: []int{2023, 2024},
		Dimensions: []model.AnalysisDimension{
			{
				Key: "equity_value_added", Name: "股权价值增加值", Status: "done",
				Sections: []model.AnalysisSection{
					{
						Title: "经营利润",
						Indicators: []model.AnalysisIndicator{
							{Key: "gross_margin", Name: "毛利率", Values: []*float64{f64(0.92), f64(0.91)}, Unit: "%", Interpretation: "衡量产品毛利水平。"},
							{Key: "net_profit", Name: "净利润", Values: []*float64{f64(8.6e10), f64(9.1e10)}, Unit: "元", Interpretation: "重构口径净利润。"},
							{Key: "fixed_asset_new_rate", Name: "固定资产成新率", Values: []*float64{nil, nil}, Unit: "%", Interpretation: "恒为空。"},
						},
					},
				},
			},
		},
	}
}

func TestParseAIResultValid(t *testing.T) {
	raw := `{"scores":[{"dimension":"盈利能力","score":88,"comment":"强"},{"dimension":"偿债能力","score":90,"comment":"强"},{"dimension":"现金获取能力","score":85,"comment":"强"},{"dimension":"经营效率","score":76,"comment":"中"},{"dimension":"成长能力","score":82,"comment":"强"}],"conclusion":"质地优秀","industry":{"name":"白酒","prospect":"稳健","competition":"集中","application":"消费"},"valuation":{"model":"two_stage","model_name":"两阶段模型","discount_rate":8,"params":[{"key":"g1","label":"高增长期增长率","value":10}],"rationale":"稳定成长"}}`
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(r.Scores) != 5 || r.Scores[0].Dimension != "盈利能力" || r.Scores[0].Score != 88 {
		t.Fatalf("评分解析不符: %+v", r.Scores)
	}
	if r.Valuation.Model != "two_stage" || r.Valuation.DiscountRate != 8 {
		t.Fatalf("估值解析不符: %+v", r.Valuation)
	}
}

func TestParseAIResultMarkdownFence(t *testing.T) {
	raw := "```json\n" + `{"scores":[{"dimension":"盈利能力","score":1,"comment":""},{"dimension":"偿债能力","score":2,"comment":""},{"dimension":"现金获取能力","score":3,"comment":""},{"dimension":"经营效率","score":4,"comment":""},{"dimension":"成长能力","score":5,"comment":""}],"conclusion":"c","industry":{"name":"n"},"valuation":{"model":"zero","model_name":"零增长模型","discount_rate":8}}` + "\n```"
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("带代码块解析失败: %v", err)
	}
	if r.Scores[4].Score != 5 {
		t.Fatalf("分数不符: %+v", r.Scores[4])
	}
}

func TestParseAIResultReorder(t *testing.T) {
	// 模型返回的顺序打乱，应被重排为规范顺序而非报错。
	raw := `{"scores":[{"dimension":"偿债能力","score":90,"comment":""},{"dimension":"盈利能力","score":88,"comment":""},{"dimension":"现金获取能力","score":85,"comment":""},{"dimension":"经营效率","score":76,"comment":""},{"dimension":"成长能力","score":82,"comment":""}]}`
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("顺序打乱应被重排而非报错: %v", err)
	}
	if r.Scores[0].Dimension != "盈利能力" || r.Scores[0].Score != 88 || r.Scores[1].Dimension != "偿债能力" || r.Scores[1].Score != 90 {
		t.Fatalf("重排结果不符: %+v", r.Scores)
	}
}

func TestParseAIResultFuzzyNames(t *testing.T) {
	// 维度名写法差异（获现能力/营运能力）应模糊匹配到规范名。
	raw := `{"scores":[{"dimension":"盈利能力","score":1,"comment":""},{"dimension":"偿债能力","score":2,"comment":""},{"dimension":"获现能力","score":3,"comment":""},{"dimension":"营运能力","score":4,"comment":""},{"dimension":"成长能力","score":5,"comment":""}]}`
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("模糊维度名解析失败: %v", err)
	}
	if r.Scores[2].Dimension != "现金获取能力" || r.Scores[2].Score != 3 || r.Scores[3].Dimension != "经营效率" || r.Scores[3].Score != 4 {
		t.Fatalf("模糊匹配结果不符: %+v", r.Scores)
	}
}

func TestParseAIResultFloatScore(t *testing.T) {
	// 模型返回浮点评分 88.5，应四舍五入为整数 89 而非报错。
	raw := `{"scores":[{"dimension":"盈利能力","score":88.5,"comment":""},{"dimension":"偿债能力","score":90.0,"comment":""},{"dimension":"现金获取能力","score":85,"comment":""},{"dimension":"经营效率","score":76,"comment":""},{"dimension":"成长能力","score":82,"comment":""}]}`
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("浮点评分解析失败: %v", err)
	}
	if r.Scores[0].Score != 89 {
		t.Fatalf("浮点评分四舍五入不符: %+v", r.Scores[0])
	}
}

func TestParseAIResultIncomplete(t *testing.T) {
	raw := `{"scores":[{"dimension":"盈利能力","score":88,"comment":""}]}`
	if _, err := ParseAIResult(raw); err == nil {
		t.Fatal("评分维度不完整应报错")
	}
}

func TestSerializeIndicators(t *testing.T) {
	a := sampleAnalysis()
	out := serializeIndicators(a)
	if !strings.Contains(out, "毛利率") || !strings.Contains(out, "净利润") {
		t.Fatalf("序列化缺少指标名: %s", out)
	}
	if !strings.Contains(out, "860.00亿") {
		t.Fatalf("金额未换算为亿: %s", out)
	}
	if strings.Contains(out, "2023=") {
		t.Fatalf("序列化不应重复年度标签: %s", out)
	}
	// 全 nil 的指标（固定资产成新率）应被跳过
	if strings.Contains(out, "固定资产成新率") {
		t.Fatalf("全 nil 指标不应被序列化: %s", out)
	}
	// 含义应随指标名内联
	if !strings.Contains(out, "衡量产品毛利水平") {
		t.Fatalf("含义未内联: %s", out)
	}
}

func TestBuildAIPrompt(t *testing.T) {
	a := sampleAnalysis()
	q := model.Quote{Name: "贵州茅台", Code: "600519", MarketCap: 1.8e12}
	system, user := BuildAIPrompt(a, q, "medium", []model.SegmentIncome{
		{Name: "茅台酒", RevenueRatio: 0.87, ProfitRatio: 0.89, GrossMargin: 0.94},
		{Name: "其他系列酒", RevenueRatio: 0.13, ProfitRatio: 0.11, GrossMargin: 0.76},
	}, sampleRecommendation())
	if !strings.Contains(system, "盈利能力") {
		t.Fatalf("系统提示词缺少框架: %s", system)
	}
	if !strings.Contains(system, "约200字") {
		t.Fatalf("系统提示词缺少结论详略要求: %s", system)
	}
	if strings.Contains(system, "指标含义字典") || strings.Contains(system, "衡量产品毛利水平") {
		t.Fatalf("系统提示词不应含指标含义字典（含义已并入用户消息）: %s", system)
	}
	if !strings.Contains(user, "贵州茅台") || !strings.Contains(user, "600519") {
		t.Fatalf("用户消息缺少公司信息: %s", user)
	}
	if !strings.Contains(user, "毛利率") || !strings.Contains(user, "衡量产品毛利水平") {
		t.Fatalf("用户消息缺少指标名/含义: %s", user)
	}
	if !strings.Contains(user, "年度：2023、2024") {
		t.Fatalf("用户消息缺少年度头部: %s", user)
	}
	if !strings.Contains(user, "主营构成") || !strings.Contains(user, "茅台酒（营收 87.00%、利润 89.00%、毛利率 94.00%）") {
		t.Fatalf("用户消息缺少主营构成: %s", user)
	}
}

func TestFormatAmount(t *testing.T) {
	if formatAmount(8.6e10) != "860.00亿" {
		t.Fatalf("亿元换算错误: %s", formatAmount(8.6e10))
	}
	if formatAmount(-3.5e4) != "-3.50万" {
		t.Fatalf("万元换算错误: %s", formatAmount(-3.5e4))
	}
	if formatAmount(0) != "0" {
		t.Fatalf("零值错误: %s", formatAmount(0))
	}
}
