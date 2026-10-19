package service

// R19 一致性校验层：Capex 覆盖率与 FCF/净利润（同窗口、同聚合口径）。
// 全部断言取自《技术设计文档》§5.6 / §5.7 的硬编码期望值。

import (
	"math"
	"strings"
	"testing"

	"financial-report/internal/model"
)

func TestCommonBaseYear(t *testing.T) {
	cases := []struct {
		name       string
		income, cf []int
		want       int
	}{
		{"有交集取最新共同年", []int{2021, 2022, 2023}, []int{2022, 2023, 2024}, 2023},
		{"无交集", []int{2021, 2022}, []int{2023, 2024}, 0},
		{"现金流量表为空", []int{2021, 2022}, nil, 0},
		{"利润表为空", nil, []int{2021, 2022}, 0},
		{"交集非连续", []int{2018, 2020, 2022}, []int{2020, 2022, 2024}, 2022},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := commonBaseYear(c.income, c.cf); got != c.want {
				t.Errorf("commonBaseYear(%v,%v) = %d，期望 %d", c.income, c.cf, got, c.want)
			}
		})
	}
}

func TestCapexCoverageDetail(t *testing.T) {
	cf := map[int]model.ReportRow{
		2024: rowAt("2024-12-31", map[string]float64{
			"CONSTRUCT_LONG_ASSET": 150, "DISPOSAL_LONG_ASSET": 50, "FA_IR_DEPR": 100,
		}),
	}

	// 净资本支出 (150−50) ÷ 保全性支出 100 = 1.0。
	d := capexCoverageDetail(cf, []int{2024}, valFCFLatest)
	if !d.Available || math.Abs(d.Ratio-1.0) > 1e-9 || d.Numerator != 100 || d.Denominator != 100 {
		t.Errorf("覆盖率 = %+v，期望 1.0（100/100）", d)
	}
	if d.WindowStart != 2024 || d.WindowEnd != 2024 || d.Mode != valFCFLatest {
		t.Errorf("窗口/方式 = %+v", d)
	}
	if !strings.Contains(d.Note, "与财务指标分析同口径") || strings.Contains(d.Note, "信用减值") || strings.Contains(d.Note, "处置项") {
		t.Errorf("口径说明应为 R3 同口径且不含旧口径：%q", d.Note)
	}

	// 多年度按窗口合计。
	cf[2023] = rowAt("2023-12-31", map[string]float64{
		"CONSTRUCT_LONG_ASSET": 100, "FA_IR_DEPR": 100,
	})
	d = capexCoverageDetail(cf, []int{2023, 2024}, valFCFAverage)
	if !d.Available || math.Abs(d.Ratio-1.0) > 1e-9 || d.Denominator != 200 {
		t.Errorf("两年合计 = %+v，期望 200/200", d)
	}

	// 分母 ≤ 0 → 不可算。
	noCapex := capexCoverageDetail(
		map[int]model.ReportRow{2024: rowAt("2024-12-31", map[string]float64{"CONSTRUCT_LONG_ASSET": 150})},
		[]int{2024}, valFCFLatest)
	if noCapex.Available || noCapex.Reason == "" {
		t.Errorf("保全性支出为 0 应不可算：%+v", noCapex)
	}

	// CONSTRUCT_LONG_ASSET 缺失（nil）→ 不可算并给出年度。
	missing := capexCoverageDetail(
		map[int]model.ReportRow{2024: rowAt("2024-12-31", map[string]float64{"FA_IR_DEPR": 100})},
		[]int{2024}, valFCFLatest)
	if missing.Available || !strings.Contains(missing.Reason, "2024") {
		t.Errorf("购建长期资产缺失应不可算：%+v", missing)
	}

	// 空窗口 → 缺少现金流量表年度。
	if empty := capexCoverageDetail(cf, nil, valFCFLatest); empty.Available || !strings.Contains(empty.Reason, "缺少现金流量表年度") {
		t.Errorf("空窗口应不可算：%+v", empty)
	}
}

func TestEffectiveWindow(t *testing.T) {
	if got := effectiveWindow(valFCFAverage, []int{2022, 2023, 2024}, 2024); len(got) != 3 || got[0] != 2022 {
		t.Errorf("多年度应沿用实际窗口：%v", got)
	}
	if got := effectiveWindow(valFCFLatest, []int{2022, 2023, 2024}, 2024); len(got) != 1 || got[0] != 2024 {
		t.Errorf("latest 应取基期年：%v", got)
	}
	if got := effectiveWindow(valFCFCustom, []int{2022, 2023, 2024}, 2024); len(got) != 1 || got[0] != 2024 {
		t.Errorf("custom 应按最新年报校验：%v", got)
	}
}

func TestFCFProfitDetail(t *testing.T) {
	// R23：分母固定为重构净利润。仅填 TOTAL_OPERATE_INCOME 与 INCOME_TAX=0，
	// 其余科目缺省为 0 → netProfitReconstructed = TOTAL_OPERATE_INCOME。
	income := map[int]model.ReportRow{
		2022: rowAt("2022-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 300, "INCOME_TAX": 0}),
		2023: rowAt("2023-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 360, "INCOME_TAX": 0}),
		2024: rowAt("2024-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 432, "INCOME_TAX": 0}),
	}
	years := []int{2022, 2023, 2024}

	// 平均口径：600 / 364 = 1.65。
	d := fcfProfitDetail(600, false, 0, income, years, valFCFAverage)
	if !d.Available || math.Abs(d.Ratio-1.65) > 0.005 || d.ProfitBasis != "reconstructed" || d.Years != 3 {
		t.Errorf("FCF/净利润 = %+v，期望 1.65/reconstructed/3", d)
	}
	if !strings.Contains(d.Note, "未含研发调整") || !strings.Contains(d.Note, "2022–2024") ||
		!strings.Contains(d.Note, "重构净利润（含少数股东损益）") {
		t.Errorf("口径说明缺失：%q", d.Note)
	}

	// 归母字段存在但被忽略：PARENT_NETPROFIT=999 为诱饵。
	withParent := map[int]model.ReportRow{
		2022: rowAt("2022-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 300, "INCOME_TAX": 0, "PARENT_NETPROFIT": 999}),
		2023: rowAt("2023-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 360, "INCOME_TAX": 0, "PARENT_NETPROFIT": 999}),
		2024: rowAt("2024-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 432, "INCOME_TAX": 0, "PARENT_NETPROFIT": 999}),
	}
	if d := fcfProfitDetail(600, false, 0, withParent, years, valFCFAverage); !d.Available ||
		d.Profit != 364 || math.Abs(d.Ratio-1.65) > 0.005 || d.ProfitBasis != "reconstructed" {
		t.Errorf("归母字段应被忽略：%+v，期望 Profit=364 / 1.65", d)
	}

	// 含研发调整。
	if d := fcfProfitDetail(600, true, 10, income, years, valFCFAverage); !d.Available ||
		d.ProfitBasis != "reconstructed" || !strings.Contains(d.Note, "含研发调整") {
		t.Errorf("研发调整口径 = %+v", d)
	}

	// 亏损年不剔除：平均 (100−50+100)/3 = 50。
	loss := map[int]model.ReportRow{
		2022: rowAt("2022-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 100, "INCOME_TAX": 0}),
		2023: rowAt("2023-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": -50, "INCOME_TAX": 0}),
		2024: rowAt("2024-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 100, "INCOME_TAX": 0}),
	}
	d = fcfProfitDetail(100, false, 0, loss, years, valFCFAverage)
	if !d.Available || math.Abs(d.Ratio-2.0) > 1e-9 || d.Years != 3 {
		t.Errorf("亏损年不应剔除：%+v，期望 100/50=2", d)
	}

	// 基期 FCF≤0 → 不可算；不可用响应也输出 ProfitBasis=reconstructed。
	if d := fcfProfitDetail(0, false, 0, income, years, valFCFLatest); d.Available || d.Reason == "" ||
		d.ProfitBasis != "reconstructed" {
		t.Errorf("FCF≤0 应不可算且口径固定：%+v", d)
	}
	// 净利润≤0 → 不可算。
	allLoss := map[int]model.ReportRow{
		2022: rowAt("2022-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": -1, "INCOME_TAX": 0}),
		2023: rowAt("2023-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": -2, "INCOME_TAX": 0}),
		2024: rowAt("2024-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": -3, "INCOME_TAX": 0}),
	}
	if d := fcfProfitDetail(100, false, 0, allLoss, years, valFCFAverage); d.Available || d.Reason == "" ||
		d.ProfitBasis != "reconstructed" {
		t.Errorf("净利润≤0 应不可算：%+v", d)
	}
}

// TestRecommendValuationC7 现金流量表缺 2024 而利润表有 → 基期年回退 2023 并告警。
func TestRecommendValuationC7(t *testing.T) {
	income := []model.ReportRow{
		rowAt("2022-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 700}),
		rowAt("2023-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 1200, "OPERATE_COST": 840}),
		rowAt("2024-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 1400, "OPERATE_COST": 980}),
	}
	cashflow := []model.ReportRow{
		rowAt("2022-12-31", map[string]float64{"NETCASH_OPERATE": 500, "FA_IR_DEPR": 100}),
		rowAt("2023-12-31", map[string]float64{"NETCASH_OPERATE": 600, "FA_IR_DEPR": 100}),
	}
	balance := []model.ReportRow{
		rowAt("2023-12-31", map[string]float64{"TOTAL_EQUITY": 5000, "SHORT_LOAN": 100}),
		rowAt("2024-12-31", map[string]float64{"TOTAL_EQUITY": 5500, "SHORT_LOAN": 100}),
	}

	rec := RecommendValuation(ValuationRecommendInput{Balance: balance, Cashflow: cashflow, Income: income})
	if rec.BaseYear != 2023 {
		t.Errorf("BaseYear = %d，期望 2023（利润表 ∩ 现金流量表）", rec.BaseYear)
	}
	warns := strings.Join(rec.Warnings, "；")
	if !strings.Contains(warns, "最新共同年度为 2023") {
		t.Errorf("Warnings 应说明同源基期，实际 %q", rec.Warnings)
	}
	if rec.FCFWindowEnd != 2023 {
		t.Errorf("FCF 窗口应截止 2023，实际 %d", rec.FCFWindowEnd)
	}
}

// TestRecommendValuationChecks R19 关键校验：净 Capex/保全支出、FCF/净利润与告警。
func TestRecommendValuationChecks(t *testing.T) {
	years := []int{2022, 2023, 2024}
	income := make([]model.ReportRow, 0, len(years))
	cashflow := make([]model.ReportRow, 0, len(years))
	balance := make([]model.ReportRow, 0, len(years))
	for i, y := range years {
		date := ymd(y)
		rev := 1000.0 * math.Pow(1.2, float64(i))
		// 营业成本 = 0.8×营收 → 重构净利润 = 0.2×营收；TOTAL_PROFIT/PARENT_NETPROFIT 仅作展示诱饵。
		income = append(income, rowAt(date, map[string]float64{
			"TOTAL_OPERATE_INCOME": rev, "OPERATE_COST": rev * 0.8,
			"TOTAL_PROFIT": rev * 0.2, "PARENT_NETPROFIT": rev * 0.2, "INCOME_TAX": 0,
		}))
		cashflow = append(cashflow, rowAt(date, map[string]float64{
			"NETCASH_OPERATE": 700, "FA_IR_DEPR": 100,
			"CONSTRUCT_LONG_ASSET": 150, "DISPOSAL_LONG_ASSET": 0,
		}))
		balance = append(balance, rowAt(date, map[string]float64{
			"TOTAL_EQUITY": 5000, "SHORT_LOAN": 100,
		}))
	}

	rec := RecommendValuation(ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 100e8,
	})
	if !rec.CapexCheck.Available || math.Abs(rec.CapexCheck.Ratio-1.5) > 1e-9 {
		t.Errorf("CapexCheck = %+v，期望 1.5", rec.CapexCheck)
	}
	// 基期 FCF=600（latest 窗口 2024），重构净利润 288 → 2.08。
	if !rec.FCFProfitCheck.Available || math.Abs(rec.FCFProfitCheck.Ratio-2.08) > 0.005 {
		t.Errorf("FCFProfitCheck = %+v，期望 2.08", rec.FCFProfitCheck)
	}
	if rec.FCFProfitCheck.ProfitBasis != "reconstructed" {
		t.Errorf("R23 起分母固定重构口径：%+v", rec.FCFProfitCheck)
	}
	warns := strings.Join(rec.Warnings, "；")
	if !strings.Contains(warns, "资本支出") || !strings.Contains(warns, "FCF 为净利润的 2.08 倍") {
		t.Errorf("关键校验应进入 Warnings，实际 %q", rec.Warnings)
	}
	if len(rec.Warnings) > 6 {
		t.Errorf("Warnings 最多 6 条，实际 %d", len(rec.Warnings))
	}
}

// TestValuationCapexConsistency R23：估值链路与财务指标分析共用 maintenanceCapex，
// 「FCF/净利润」分母固定为重构净利润。
func TestValuationCapexConsistency(t *testing.T) {
	cashflow := []model.ReportRow{
		rowAt("2023-12-31", map[string]float64{
			"NETCASH_OPERATE": 400, "FA_IR_DEPR": 80, "IA_AMORTIZE": 6,
			"LPE_AMORTIZE": 4, "USERIGHT_ASSET_AMORTIZE": 10,
			"ASSET_IMPAIRMENT": 20, "FA_SCRAP_LOSS": 2,
			"DISPOSAL_LONGASSET_LOSS": 99, // 诱饵，不得计入
		}),
		rowAt("2024-12-31", map[string]float64{
			"NETCASH_OPERATE": 500, "CONSTRUCT_LONG_ASSET": 150, "DISPOSAL_LONG_ASSET": 50,
			"FA_IR_DEPR": 80, "IA_AMORTIZE": 6, "LPE_AMORTIZE": 4, "USERIGHT_ASSET_AMORTIZE": 10,
			"ASSET_IMPAIRMENT": 20, "FA_SCRAP_LOSS": 2,
			"DISPOSAL_LONGASSET_LOSS": 99, // 诱饵，不得计入
		}),
	}
	income := []model.ReportRow{
		rowAt("2023-12-31", map[string]float64{
			"TOTAL_OPERATE_INCOME": 360, "OPERATE_COST": 90, "INCOME_TAX": 0,
		}),
		rowAt("2024-12-31", map[string]float64{
			"TOTAL_OPERATE_INCOME": 400, "OPERATE_COST": 100, "INCOME_TAX": 0,
			"ASSET_IMPAIRMENT_INCOME": -10, "CREDIT_IMPAIRMENT_INCOME": -5, // 诱饵
			"PARENT_NETPROFIT": 999, // 诱饵
		}),
	}
	balance := []model.ReportRow{
		rowAt("2023-12-31", map[string]float64{"TOTAL_EQUITY": 5000, "SHORT_LOAN": 100}),
		rowAt("2024-12-31", map[string]float64{"TOTAL_EQUITY": 5000, "SHORT_LOAN": 100}),
	}

	cfByYear, _ := annualRows(cashflow)
	incomeByYear, _ := annualRows(income)

	// 1) 同一年度：maintenanceCapex = 财务指标分析 maintenance_capex 指标值。
	fa := ComputeAnalysis(nil, cashflow, income, 2023, 2024)
	it := findAnalysisIndicator(t, fa, "maintenance_capex")
	for i, y := range fa.Years {
		want := maintenanceCapex(cfByYear[y])
		if want != 122 {
			t.Fatalf("%d 年 maintenanceCapex = %v，期望 122", y, want)
		}
		if it.Values[i] == nil || *it.Values[i] != want {
			t.Errorf("%d 年财务指标分析保全性资本支出 = %v，maintenanceCapex = %v，期望严格相等",
				y, it.Values[i], want)
		}
	}

	// 2) operatingFreeCashFlow = NETCASH_OPERATE − maintenanceCapex（2024 = 378）。
	for y, cf := range cfByYear {
		got := operatingFreeCashFlow(cf)
		want := v0(cf, "NETCASH_OPERATE") - maintenanceCapex(cf)
		if got != want {
			t.Errorf("%d 年 FCF = %v，期望 %v", y, got, want)
		}
	}
	if got := operatingFreeCashFlow(cfByYear[2024]); got != 378 {
		t.Errorf("2024 年 FCF = %v，期望 378", got)
	}

	// 3) fcf_series 保全列 = maintenanceCapex，且 FCF = 经营现金流 − 保全支出。
	rec := RecommendValuation(ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 100e8,
	})
	if len(rec.FCFSeries) == 0 {
		t.Fatalf("fcf_series 为空")
	}
	for _, p := range rec.FCFSeries {
		if got, want := p.PreservationCapex, maintenanceCapex(cfByYear[p.Year]); got != want {
			t.Errorf("%d 年 fcf_series.preservation_capex = %v，maintenanceCapex = %v，期望相等", p.Year, got, want)
		}
		if p.FCF != p.OperatingCash-p.PreservationCapex {
			t.Errorf("%d 年 FCF(%v) ≠ 经营现金流(%v) − 保全支出(%v)",
				p.Year, p.FCF, p.OperatingCash, p.PreservationCapex)
		}
	}

	// 4) Capex 覆盖率分母 = 窗口内 maintenanceCapex 之和。
	d := capexCoverageDetail(cfByYear, []int{2024}, valFCFLatest)
	if !d.Available || d.Denominator != 122 {
		t.Errorf("Capex 覆盖率 = %+v，分母期望 122", d)
	}

	// 5) FCF/净利润分母 = 重构净利润（含少数股东损益），不受归母 999 诱饵影响。
	pd := fcfProfitDetail(378, false, 0, incomeByYear, []int{2024}, valFCFLatest)
	if !pd.Available || pd.Profit != 285 || pd.ProfitBasis != "reconstructed" {
		t.Errorf("FCF/净利润 = %+v，期望 Profit=285 / reconstructed", pd)
	}
	if pd.Profit != netProfitReconstructed(incomeByYear[2024]) {
		t.Errorf("Profit = %v，期望等于 netProfitReconstructed = %v",
			pd.Profit, netProfitReconstructed(incomeByYear[2024]))
	}

	// 6) ASSET_IMPAIRMENT 为 nil 按 0，且不因利润表减值回填。
	cfNoImpair := rowAt("2024-12-31", map[string]float64{
		"NETCASH_OPERATE": 500, "FA_IR_DEPR": 80, "IA_AMORTIZE": 6,
		"LPE_AMORTIZE": 4, "USERIGHT_ASSET_AMORTIZE": 10, "FA_SCRAP_LOSS": 2,
	})
	if got, want := maintenanceCapex(cfNoImpair), 102.0; got != want {
		t.Errorf("无 ASSET_IMPAIRMENT 时 maintenanceCapex = %v，期望 %v（仅折旧摊销）", got, want)
	}
	if got, want := operatingFreeCashFlow(cfNoImpair), 398.0; got != want {
		t.Errorf("无 ASSET_IMPAIRMENT 时 FCF = %v，期望 %v（不得回填利润表减值）", got, want)
	}
}

func ymd(year int) string {
	return strings.Join([]string{itoa(year), "12", "31"}, "-")
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
