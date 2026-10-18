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
	income := map[int]model.ReportRow{
		2024: rowAt("2024-12-31", map[string]float64{}),
	}

	// 净资本支出 (150−50) ÷ 保全性支出 100 = 1.0。
	d := capexCoverageDetail(cf, income, []int{2024}, valFCFLatest)
	if !d.Available || math.Abs(d.Ratio-1.0) > 1e-9 || d.Numerator != 100 || d.Denominator != 100 {
		t.Errorf("覆盖率 = %+v，期望 1.0（100/100）", d)
	}
	if d.WindowStart != 2024 || d.WindowEnd != 2024 || d.Mode != valFCFLatest {
		t.Errorf("窗口/方式 = %+v", d)
	}

	// 多年度按窗口合计。
	cf[2023] = rowAt("2023-12-31", map[string]float64{
		"CONSTRUCT_LONG_ASSET": 100, "FA_IR_DEPR": 100,
	})
	income[2023] = rowAt("2023-12-31", map[string]float64{})
	d = capexCoverageDetail(cf, income, []int{2023, 2024}, valFCFAverage)
	if !d.Available || math.Abs(d.Ratio-1.0) > 1e-9 || d.Denominator != 200 {
		t.Errorf("两年合计 = %+v，期望 200/200", d)
	}

	// 分母 ≤ 0 → 不可算。
	noCapex := capexCoverageDetail(
		map[int]model.ReportRow{2024: rowAt("2024-12-31", map[string]float64{"CONSTRUCT_LONG_ASSET": 150})},
		income, []int{2024}, valFCFLatest)
	if noCapex.Available || noCapex.Reason == "" {
		t.Errorf("保全性支出为 0 应不可算：%+v", noCapex)
	}

	// CONSTRUCT_LONG_ASSET 缺失（nil）→ 不可算并给出年度。
	missing := capexCoverageDetail(
		map[int]model.ReportRow{2024: rowAt("2024-12-31", map[string]float64{"FA_IR_DEPR": 100})},
		income, []int{2024}, valFCFLatest)
	if missing.Available || !strings.Contains(missing.Reason, "2024") {
		t.Errorf("购建长期资产缺失应不可算：%+v", missing)
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
	income := map[int]model.ReportRow{
		2022: rowAt("2022-12-31", map[string]float64{"PARENT_NETPROFIT": 300}),
		2023: rowAt("2023-12-31", map[string]float64{"PARENT_NETPROFIT": 360}),
		2024: rowAt("2024-12-31", map[string]float64{"PARENT_NETPROFIT": 432}),
	}
	years := []int{2022, 2023, 2024}

	// 平均口径：600 / 364 = 1.65。
	d := fcfProfitDetail(600, false, 0, income, years, valFCFAverage)
	if !d.Available || math.Abs(d.Ratio-1.65) > 0.005 || d.ProfitBasis != "parent" || d.Years != 3 {
		t.Errorf("FCF/净利润 = %+v，期望 1.65/parent/3", d)
	}
	if !strings.Contains(d.Note, "未含研发调整") || !strings.Contains(d.Note, "2022–2024") {
		t.Errorf("口径说明缺失：%q", d.Note)
	}

	// 归母字段缺一年 → 整窗口回落重构净利润。
	noParent := map[int]model.ReportRow{
		2022: rowAt("2022-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 300}),
		2023: rowAt("2023-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 360}),
		2024: rowAt("2024-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 432}),
	}
	if d := fcfProfitDetail(600, true, 10, noParent, years, valFCFAverage); !d.Available ||
		d.ProfitBasis != "reconstructed" || !strings.Contains(d.Note, "含研发调整") {
		t.Errorf("重构口径 = %+v", d)
	}

	// 亏损年不剔除：平均 (100−50+100)/3 = 50。
	loss := map[int]model.ReportRow{
		2022: rowAt("2022-12-31", map[string]float64{"PARENT_NETPROFIT": 100}),
		2023: rowAt("2023-12-31", map[string]float64{"PARENT_NETPROFIT": -50}),
		2024: rowAt("2024-12-31", map[string]float64{"PARENT_NETPROFIT": 100}),
	}
	d = fcfProfitDetail(100, false, 0, loss, years, valFCFAverage)
	if !d.Available || math.Abs(d.Ratio-2.0) > 1e-9 || d.Years != 3 {
		t.Errorf("亏损年不应剔除：%+v，期望 100/50=2", d)
	}

	// 基期 FCF≤0 → 不可算。
	if d := fcfProfitDetail(0, false, 0, income, years, valFCFLatest); d.Available || d.Reason == "" {
		t.Errorf("FCF≤0 应不可算：%+v", d)
	}
	// 净利润≤0 → 不可算。
	allLoss := map[int]model.ReportRow{
		2022: rowAt("2022-12-31", map[string]float64{"PARENT_NETPROFIT": -1}),
		2023: rowAt("2023-12-31", map[string]float64{"PARENT_NETPROFIT": -2}),
		2024: rowAt("2024-12-31", map[string]float64{"PARENT_NETPROFIT": -3}),
	}
	if d := fcfProfitDetail(100, false, 0, allLoss, years, valFCFAverage); d.Available || d.Reason == "" {
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
		income = append(income, rowAt(date, map[string]float64{
			"TOTAL_OPERATE_INCOME": rev, "OPERATE_COST": rev * 0.7,
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
	// 基期 FCF=600（latest 窗口 2024），归母净利润 288 → 2.08。
	if !rec.FCFProfitCheck.Available || math.Abs(rec.FCFProfitCheck.Ratio-2.08) > 0.005 {
		t.Errorf("FCFProfitCheck = %+v，期望 2.08", rec.FCFProfitCheck)
	}
	if rec.FCFProfitCheck.ProfitBasis != "parent" {
		t.Errorf("有归母字段时应取归母口径：%+v", rec.FCFProfitCheck)
	}
	warns := strings.Join(rec.Warnings, "；")
	if !strings.Contains(warns, "资本支出") || !strings.Contains(warns, "FCF 为净利润的 2.08 倍") {
		t.Errorf("关键校验应进入 Warnings，实际 %q", rec.Warnings)
	}
	if len(rec.Warnings) > 6 {
		t.Errorf("Warnings 最多 6 条，实际 %d", len(rec.Warnings))
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
