package service

import (
	"encoding/json"
	"testing"

	"financial-report/internal/model"
)

// R8《估值表单与 AI 分析界面打磨》的 tester 独立交叉验证。
// 与 coder 的用例互补：coder 覆盖 rdAdjustSuggestion 的分支；此处覆盖
// ①「年份口径必须取利润表自身末位年报（而非三表 latestYear）」这条设计 12.4 明确点出的易错点；
// ② 响应 JSON 契约（adjust_rd 恒输出 / rd_ratio 0 值省略）在「整个 AIAnalysisResult」上的表现；
// ③ 输入行序（真实接口按日期倒序返回）不影响结论；
// ④ 阈值浮点边界与「判定用未取整值、展示用取整值」的联合行为。
// 均为对公开/包内函数的黑盒断言，不改动被测代码。

// r8Inc 构造利润表行（仅营业总收入 + 可选研发费用）。
func r8Inc(date string, rev float64, rd *float64) model.ReportRow {
	f := map[string]float64{"TOTAL_OPERATE_INCOME": rev}
	if rd != nil {
		f["RESEARCH_EXPENSE"] = *rd
	}
	return rowAt(date, f)
}

func r8Ptr(v float64) *float64 { return &v }

// 设计 12.4 易错点 1：研发费用率两个字段都在利润表内，必须取「利润表最后一个年报年份」。
// 这里让资产负债表/现金流量表出现更晚的年报年份（2025），若实现误用 latestYear，
// incomeByYear[2025] 是零值行 → RESEARCH_EXPENSE 缺失 → 误判 false。
func TestVerifyRDIncomeYearNotThreeTableLatest(t *testing.T) {
	income := []model.ReportRow{
		r8Inc("2023-12-31", 1000, r8Ptr(10)),
		r8Inc("2024-12-31", 1000, r8Ptr(80)), // 最新利润表年报：研发费用率 8% > 5%
	}
	balance := []model.ReportRow{rowAt("2025-12-31", map[string]float64{"TOTAL_ASSETS": 5000, "TOTAL_EQUITY": 3000})}
	cashflow := []model.ReportRow{rowAt("2025-12-31", map[string]float64{"NETCASH_OPERATE": 800})}

	// 前提：三表最新年报确为 2025（比利润表末位 2024 更晚），确保本用例真的能区分两种口径。
	if latest := latestYear([]int{2023, 2024}, []int{2025}, []int{2025}); latest != 2025 {
		t.Fatalf("用例前提不成立：latestYear = %d，期望 2025", latest)
	}

	rec := RecommendValuation(balance, cashflow, income)
	if !rec.AdjustRD {
		t.Errorf("应取利润表末位年报 2024（研发费用率 8%%）→ AdjustRD=true，实际 false（疑似误用了三表 latestYear=2025 的零值行）")
	}
	if rec.RDRatio != 8 {
		t.Errorf("RDRatio = %v，期望 8（利润表 2024 年报口径）", rec.RDRatio)
	}
}

// 真实接口按日期倒序返回数据行；口径必须与行序无关（annualRows 排序后取末位）。
func TestVerifyRDRowOrderIndependent(t *testing.T) {
	desc := []model.ReportRow{
		r8Inc("2024-12-31", 1000, r8Ptr(80)), // 倒序：最新在前
		r8Inc("2023-12-31", 1000, r8Ptr(10)),
		r8Inc("2022-12-31", 1000, r8Ptr(10)),
	}
	asc := []model.ReportRow{desc[2], desc[1], desc[0]}
	gotDesc, ratioDesc := rdSuggestion(desc)
	gotAsc, ratioAsc := rdSuggestion(asc)
	if !gotDesc || ratioDesc == nil || *ratioDesc != 8 {
		t.Errorf("倒序输入应取 2024 年报 8%%：got=%v ratio=%v", gotDesc, ratioDesc)
	}
	if gotDesc != gotAsc || ratioDesc == nil || ratioAsc == nil || *ratioDesc != *ratioAsc {
		t.Errorf("行序不应影响结论：倒序 (%v, %v) vs 升序 (%v, %v)", gotDesc, ratioDesc, gotAsc, ratioAsc)
	}
}

// 响应 JSON 契约：在整个 AIAnalysisResult（handler 实际序列化的对象）上核对字段的出现/省略，
// 用 map 判定「键是否存在」，避免字符串包含带来的假阳性。
func TestVerifyRDResponseJSONContract(t *testing.T) {
	raw := `{"scores":[{"dimension":"盈利能力","score":88},{"dimension":"偿债能力","score":90},{"dimension":"现金获取能力","score":85},{"dimension":"经营效率","score":76},{"dimension":"成长能力","score":82}],"conclusion":"c","industry":{"name":"n"},"valuation":{"model":"zero","rationale":"r"}}`

	cases := []struct {
		name        string
		rec         model.ValuationRecommendation
		wantRDKey   bool
		wantRDValue bool
		wantRatio   *float64
	}{
		{"建议开启且有比率", model.ValuationRecommendation{Model: valModelZero, AdjustRD: true, RDRatio: 12.35}, true, true, r8Ptr(12.35)},
		{"不建议但有比率", model.ValuationRecommendation{Model: valModelZero, AdjustRD: false, RDRatio: 3.2}, true, false, r8Ptr(3.2)},
		{"无法判定（0 值）", model.ValuationRecommendation{Model: valModelZero, AdjustRD: false, RDRatio: 0}, true, false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := ParseAIResult(raw)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			ApplyValuationRecommendation(&res, c.rec)
			blob, err := json.Marshal(res)
			if err != nil {
				t.Fatalf("序列化失败: %v", err)
			}
			var out struct {
				Valuation map[string]json.RawMessage `json:"valuation"`
			}
			if err := json.Unmarshal(blob, &out); err != nil {
				t.Fatalf("反序列化失败: %v", err)
			}
			v, ok := out.Valuation["adjust_rd"]
			if !ok || !c.wantRDKey {
				t.Fatalf("valuation.adjust_rd 应始终出现：ok=%v want=%v，JSON=%s", ok, c.wantRDKey, blob)
			}
			var rd bool
			if err := json.Unmarshal(v, &rd); err != nil {
				t.Fatalf("adjust_rd 非布尔：%s", v)
			}
			if rd != c.wantRDValue {
				t.Errorf("adjust_rd = %v，期望 %v", rd, c.wantRDValue)
			}
			if rv, ok := out.Valuation["rd_ratio"]; c.wantRatio == nil {
				if ok {
					t.Errorf("rd_ratio 为 0 时不应输出，实际 %s", rv)
				}
			} else {
				if !ok {
					t.Fatalf("rd_ratio 缺失，期望 %v，JSON=%s", *c.wantRatio, blob)
				}
				var got float64
				if err := json.Unmarshal(rv, &got); err != nil {
					t.Fatalf("rd_ratio 非数值：%s", rv)
				}
				if got != *c.wantRatio {
					t.Errorf("rd_ratio = %v，期望 %v", got, *c.wantRatio)
				}
			}
			// 回归：既有字段语义不变。
			var model string
			if err := json.Unmarshal(out.Valuation["model"], &model); err != nil || model != "zero" {
				t.Errorf("覆盖后 model 应为确定性结果 zero，实际 %q (err=%v)", model, err)
			}
			var rationale string
			if err := json.Unmarshal(out.Valuation["rationale"], &rationale); err != nil || rationale != "r" {
				t.Errorf("rationale 应保留大模型原文，实际 %q", rationale)
			}
		})
	}
}

// 阈值浮点边界：恰 5% → false；刚过 5% → true；判定与展示取整的联合行为如实断言。
func TestVerifyRDThresholdFloatBoundary(t *testing.T) {
	if rdAdjustThreshold*100 != 5.0 {
		t.Fatalf("常量折算异常：rdAdjustThreshold*100 = %v", rdAdjustThreshold*100)
	}
	exact := 50.0 / 1000.0 * 100
	if exact != 5.0 {
		t.Logf("注意：50/1000×100 = %v（非精确 5.0），浮点边界依赖该值", exact)
	}

	cases := []struct {
		name       string
		rev, rd    float64
		want       bool
		wantRatio  float64
		expectNote string
	}{
		{"恰 5%", 1000, 50, false, 5, "严格大于才开启"},
		{"刚过 5%（5.0001%）", 1000, 50.001, true, 5.0, "判定用未取整的原始比率（设计 12.11 #13）"},
		{"稍高于 5% 但展示取整为 5", 1000, 50.049, true, 5.0, "展示值取整后与阈值同值，属设计取舍（前端文案会显示「研发费用率 5%」）"},
		{"极小营收但比率高", 0.001, 0.001, true, 100, "营收 > 0 即参与判定"},
		{"大数营收 10%", 1e12, 1e11, true, 10, "大数下比率仍精确"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ratio := rdSuggestion([]model.ReportRow{r8Inc("2024-12-31", c.rev, r8Ptr(c.rd))})
			if got != c.want {
				t.Errorf("suggest = %v，期望 %v（%s）", got, c.want, c.expectNote)
			}
			if ratio == nil {
				t.Fatalf("ratioPct 不应为 nil")
			}
			if rec := RecommendValuation(nil, nil, []model.ReportRow{r8Inc("2024-12-31", c.rev, r8Ptr(c.rd))}); rec.RDRatio != c.wantRatio {
				t.Errorf("展示值 RDRatio = %v，期望 %v（%s）", rec.RDRatio, c.wantRatio, c.expectNote)
			}
		})
	}
}

// 防御性：incomeYears 为空但 map 非空时，必须先被长度守卫拦下（不 panic、不读数）。
func TestVerifyRDEmptyYearsNonEmptyMap(t *testing.T) {
	byYear := map[int]model.ReportRow{2024: r8Inc("2024-12-31", 1000, r8Ptr(80))}
	got, ratio := rdAdjustSuggestion(byYear, nil)
	if got || ratio != nil {
		t.Errorf("年份切片为空应返回 (false, nil)，实际 (%v, %v)", got, ratio)
	}
}

// 确定性（R7 幂等的延伸）：同一份含研发费用的报表多次推导，adjust_rd / rd_ratio 稳定。
func TestVerifyRDDeterministic(t *testing.T) {
	income := []model.ReportRow{
		r8Inc("2022-12-31", 900, r8Ptr(30)),
		r8Inc("2023-12-31", 950, r8Ptr(60)),
		r8Inc("2024-12-31", 1000, r8Ptr(80)),
	}
	first := RecommendValuation(nil, nil, income)
	for i := 0; i < 5; i++ {
		got := RecommendValuation(nil, nil, income)
		if got.AdjustRD != first.AdjustRD || got.RDRatio != first.RDRatio {
			t.Fatalf("第 %d 次推导不一致：%v/%v vs %v/%v", i+2, got.AdjustRD, got.RDRatio, first.AdjustRD, first.RDRatio)
		}
	}
	if !first.AdjustRD || first.RDRatio != 8 {
		t.Errorf("期望 (true, 8)，实际 (%v, %v)", first.AdjustRD, first.RDRatio)
	}
}
