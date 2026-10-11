package service

import (
	"testing"

	"financial-report/internal/model"
)

// cvDates 2020–2024 五个年报报告期，供公司价值卡片测试共用。
var cvDates = []string{"2020-12-31", "2021-12-31", "2022-12-31", "2023-12-31", "2024-12-31"}

// cvFixture 构造 2020–2024 五年的三张表：
// 经营现金流净额逐年上升 {500,520,540,560,580}，保全性资本支出恒 122
//
//	（减值 10+5 + 折旧 80 + 使用权摊销 10 + 无形资产摊销 6 + 长期待摊摊销 4 + 处置损失 5 + 报废损失 2）
//	→ 经营资产自由现金流 = {378,398,418,438,458}（逐年上升）；
//
// 金融资产（货币资金）= {600,610,620,630,640}；长期股权投资账面 = {200,201,202,203,204}。
func cvFixture() (balance, cashflow, income []model.ReportRow) {
	netOperate := []float64{500, 520, 540, 560, 580}
	monetary := []float64{600, 610, 620, 630, 640}
	longEquity := []float64{200, 201, 202, 203, 204}
	for i, d := range cvDates {
		balance = append(balance, rowWith(d, map[string]float64{
			"MONETARYFUNDS":      monetary[i],
			"LONG_EQUITY_INVEST": longEquity[i],
		}))
		cashflow = append(cashflow, rowWith(d, map[string]float64{
			"NETCASH_OPERATE":         netOperate[i],
			"FA_IR_DEPR":              80,
			"USERIGHT_ASSET_AMORTIZE": 10,
			"IA_AMORTIZE":             6,
			"LPE_AMORTIZE":            4,
			"DISPOSAL_LONGASSET_LOSS": 5,
			"FA_SCRAP_LOSS":           2,
		}))
		income = append(income, rowWith(d, map[string]float64{
			"ASSET_IMPAIRMENT_INCOME":  -10, // 收益口径：负值=损失 10
			"CREDIT_IMPAIRMENT_INCOME": -5,  // 负值=损失 5
		}))
	}
	return balance, cashflow, income
}

// cvIndicatorByKey 按 key 取公司价值卡片指标。
func cvIndicatorByKey(t *testing.T, cv model.CompanyValue, key string) model.AnalysisIndicator {
	t.Helper()
	for _, it := range cv.Indicators {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("公司价值卡片缺少指标 %q", key)
	return model.AnalysisIndicator{}
}

// findAnalysisIndicator 在六维分析结果中按 key 查找指标（用于一致性核对）。
func findAnalysisIndicator(t *testing.T, fa model.FinancialAnalysis, key string) model.AnalysisIndicator {
	t.Helper()
	for _, d := range fa.Dimensions {
		for _, s := range d.Sections {
			for _, it := range s.Indicators {
				if it.Key == key {
					return it
				}
			}
		}
	}
	t.Fatalf("六维分析中未找到指标 %q", key)
	return model.AnalysisIndicator{}
}

// TestComputeCompanyValueBasic 三指标基本结构：数量、key 顺序、单位、年份对齐、status。
func TestComputeCompanyValueBasic(t *testing.T) {
	balance, cashflow, income := cvFixture()
	cv := ComputeCompanyValue(balance, cashflow, income, 2020, 2024)

	if cv.Status != "done" {
		t.Errorf("status = %q, want done", cv.Status)
	}
	if len(cv.Indicators) != 3 {
		t.Fatalf("指标数 = %d, want 3", len(cv.Indicators))
	}
	wantKeys := []string{"operating_fcf", "financial_assets_value", "long_equity_value"}
	for i, k := range wantKeys {
		if cv.Indicators[i].Key != k {
			t.Errorf("指标[%d].key = %q, want %q", i, cv.Indicators[i].Key, k)
		}
	}
	if len(cv.Years) != 5 || cv.Years[0] != 2020 || cv.Years[4] != 2024 {
		t.Fatalf("years = %v, want [2020 2021 2022 2023 2024]", cv.Years)
	}
	for _, it := range cv.Indicators {
		if it.Unit != "元" {
			t.Errorf("指标 %q 单位 = %q, want 元", it.Key, it.Unit)
		}
		if it.Kind != "" {
			t.Errorf("指标 %q kind = %q, want 空（无合计/子项层级）", it.Key, it.Kind)
		}
		if len(it.Values) != len(cv.Years) {
			t.Errorf("指标 %q 值数 = %d, want %d", it.Key, len(it.Values), len(cv.Years))
		}
	}
	if len(cv.Notes) == 0 {
		t.Errorf("卡片级 Notes 为空，期望含「流量与存量不可相加」提示")
	}
}

// TestComputeCompanyValueFCF 经营资产自由现金流口径逐项核对：NETCASH_OPERATE − 保全性资本支出。
func TestComputeCompanyValueFCF(t *testing.T) {
	balance, cashflow, income := cvFixture()
	cv := ComputeCompanyValue(balance, cashflow, income, 2020, 2024)
	it := cvIndicatorByKey(t, cv, "operating_fcf")

	want := []float64{378, 398, 418, 438, 458} // 500-122 ... 580-122
	for i, v := range it.Values {
		if v == nil {
			t.Fatalf("operating_fcf[%d] = nil, want %v", i, want[i])
		}
		if *v != want[i] {
			t.Errorf("operating_fcf[%d] = %v, want %v", i, *v, want[i])
		}
	}
}

// TestComputeCompanyValueConsistentWithAssetCapital 金融资产价值/长期股权投资价值与资产资本维度同年值一致。
func TestComputeCompanyValueConsistentWithAssetCapital(t *testing.T) {
	balance, cashflow, income := cvFixture()
	cv := ComputeCompanyValue(balance, cashflow, income, 2020, 2024)
	fa := ComputeAnalysis(balance, cashflow, income, 2020, 2024)

	pairs := []struct{ cvKey, dimKey string }{
		{"financial_assets_value", "financial_assets"},
		{"long_equity_value", "long_equity_invest"},
	}
	for _, p := range pairs {
		cvIt := cvIndicatorByKey(t, cv, p.cvKey)
		dimIt := findAnalysisIndicator(t, fa, p.dimKey)
		for i := range cvIt.Values {
			if cvIt.Values[i] == nil || dimIt.Values[i] == nil {
				t.Fatalf("%s 年 %d 存在 nil（cv=%v, dim=%v）", p.cvKey, cv.Years[i], cvIt.Values[i], dimIt.Values[i])
			}
			if *cvIt.Values[i] != *dimIt.Values[i] {
				t.Errorf("%s 年 %d = %v, 资产资本维度 %s = %v，期望一致",
					p.cvKey, cv.Years[i], *cvIt.Values[i], p.dimKey, *dimIt.Values[i])
			}
		}
	}
}

// TestComputeCompanyValueBalanceYearMissing 整年资产负债表缺失 → 存量指标为 nil（非 0）。
func TestComputeCompanyValueBalanceYearMissing(t *testing.T) {
	balance, cashflow, income := cvFixture()
	// 删除 2022 年资产负债表行（该年仍在现金流量表年份范围内）
	filtered := balance[:0:0]
	for _, r := range balance {
		if r.ReportDate == "2022-12-31" {
			continue
		}
		filtered = append(filtered, r)
	}
	cv := ComputeCompanyValue(filtered, cashflow, income, 2020, 2024)

	idx := -1
	for i, y := range cv.Years {
		if y == 2022 {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("2022 年应仍在年份范围内，years = %v", cv.Years)
	}
	for _, k := range []string{"financial_assets_value", "long_equity_value"} {
		if v := cvIndicatorByKey(t, cv, k).Values[idx]; v != nil {
			t.Errorf("%s 2022 = %v, want nil（整年资产负债表缺失须与「科目缺=0」区分）", k, *v)
		}
	}
	// 经营资产自由现金流仍可算（年份来自现金流量表）
	if v := cvIndicatorByKey(t, cv, "operating_fcf").Values[idx]; v == nil {
		t.Errorf("operating_fcf 2022 = nil, want 非 nil")
	}
}

// TestComputeCompanyValueBalanceFieldMissing 资产负债表存在但无金融资产科目 → 0（合法 0，非 nil）。
func TestComputeCompanyValueBalanceFieldMissing(t *testing.T) {
	balance, cashflow, income := cvFixture()
	// 2023 年资产负债表行存在但为空科目 → 金融资产/长投均计 0
	for i := range balance {
		if balance[i].ReportDate == "2023-12-31" {
			balance[i] = rowWith("2023-12-31", map[string]float64{})
		}
	}
	cv := ComputeCompanyValue(balance, cashflow, income, 2020, 2024)

	idx := -1
	for i, y := range cv.Years {
		if y == 2023 {
			idx = i
		}
	}
	for _, k := range []string{"financial_assets_value", "long_equity_value"} {
		v := cvIndicatorByKey(t, cv, k).Values[idx]
		if v == nil {
			t.Errorf("%s 2023 = nil, want 0（表在科目缺须计 0）", k)
		} else if *v != 0 {
			t.Errorf("%s 2023 = %v, want 0", k, *v)
		}
	}
}

// TestComputeCompanyValueIncomeYearMissing 利润表缺失年份：FCF 仍算（减值计 0），Note 标注该年。
func TestComputeCompanyValueIncomeYearMissing(t *testing.T) {
	balance, cashflow, income := cvFixture()
	filtered := income[:0:0]
	for _, r := range income {
		if r.ReportDate == "2023-12-31" {
			continue
		}
		filtered = append(filtered, r)
	}
	cv := ComputeCompanyValue(balance, cashflow, filtered, 2020, 2024)
	it := cvIndicatorByKey(t, cv, "operating_fcf")

	idx := -1
	for i, y := range cv.Years {
		if y == 2023 {
			idx = i
		}
	}
	// 2023 减值计 0 → FCF = NETCASH_OPERATE(560) − (折旧 80+使用权 10+无形 6+长期待摊 4+处置 5+报废 2 = 107)
	if v := it.Values[idx]; v == nil {
		t.Fatalf("operating_fcf 2023 = nil, want 非 nil（利润表缺失仍计算）")
	} else if *v != 453 {
		t.Errorf("operating_fcf 2023 = %v, want 453（减值计 0）", *v)
	}
	if it.Note == "" {
		t.Errorf("operating_fcf.Note 为空，期望标注 2023 年利润表缺失")
	}
	// 其余年份有利润表，不应误报
	for i, y := range cv.Years {
		if y != 2023 && it.Values[i] == nil {
			t.Errorf("operating_fcf %d = nil, want 有值", y)
		}
	}
}

// TestComputeCompanyValueFCFNegative 经营现金流小于保全性资本支出 → 负值原样保留。
func TestComputeCompanyValueFCFNegative(t *testing.T) {
	balance, cashflow, income := cvFixture()
	for i := range cashflow {
		if cashflow[i].ReportDate == "2024-12-31" {
			cashflow[i] = rowWith("2024-12-31", map[string]float64{
				"NETCASH_OPERATE":         100,
				"FA_IR_DEPR":              80,
				"IA_AMORTIZE":             6,
				"LPE_AMORTIZE":            4,
				"USERIGHT_ASSET_AMORTIZE": 10,
				"DISPOSAL_LONGASSET_LOSS": 5,
				"FA_SCRAP_LOSS":           2,
			})
		}
	}
	cv := ComputeCompanyValue(balance, cashflow, income, 2020, 2024)
	it := cvIndicatorByKey(t, cv, "operating_fcf")
	last := it.Values[len(it.Values)-1]
	if last == nil || *last != -22 { // 100 − 122
		t.Errorf("operating_fcf 末值 = %v, want -22（负值原样保留）", last)
	}
}

// TestComputeCompanyValueYearRange 年份范围收窄：years 与各指标值同长度、仅含范围内年份。
func TestComputeCompanyValueYearRange(t *testing.T) {
	balance, cashflow, income := cvFixture()
	cv := ComputeCompanyValue(balance, cashflow, income, 2022, 2024)

	if len(cv.Years) != 3 || cv.Years[0] != 2022 || cv.Years[2] != 2024 {
		t.Fatalf("years = %v, want [2022 2023 2024]", cv.Years)
	}
	for _, it := range cv.Indicators {
		if len(it.Values) != 3 {
			t.Errorf("指标 %q 值数 = %d, want 3", it.Key, len(it.Values))
		}
	}
}

// TestComputeCompanyValueNoAnnualData 无年报：status=no_data、years 空，但仍产出 3 个 key。
func TestComputeCompanyValueNoAnnualData(t *testing.T) {
	cv := ComputeCompanyValue(nil, nil, nil, 2020, 2024)

	if cv.Status != "no_data" {
		t.Errorf("status = %q, want no_data", cv.Status)
	}
	if len(cv.Years) != 0 {
		t.Errorf("years = %v, want 空", cv.Years)
	}
	if len(cv.Indicators) != 3 {
		t.Fatalf("指标数 = %d, want 3（无数据时 key 仍须稳定产出）", len(cv.Indicators))
	}
	wantKeys := []string{"operating_fcf", "financial_assets_value", "long_equity_value"}
	for i, k := range wantKeys {
		if cv.Indicators[i].Key != k {
			t.Errorf("指标[%d].key = %q, want %q", i, cv.Indicators[i].Key, k)
		}
	}
}

// TestComputeCompanyValueTrendSignals 趋势信号：自由现金流 higher_better 上行 improving，另两者 neutral 无信号。
func TestComputeCompanyValueTrendSignals(t *testing.T) {
	balance, cashflow, income := cvFixture()
	cv := ComputeCompanyValue(balance, cashflow, income, 2020, 2024)

	fcf := cvIndicatorByKey(t, cv, "operating_fcf")
	if fcf.Direction != dirHigherBetter {
		t.Errorf("operating_fcf.direction = %q, want %q", fcf.Direction, dirHigherBetter)
	}
	if fcf.TrendSignal != signalImproving {
		t.Errorf("operating_fcf.trend_signal = %q, want %q（FCF 逐年上升）", fcf.TrendSignal, signalImproving)
	}
	if fcf.TrendRate == nil {
		t.Errorf("operating_fcf.trend_rate = nil, want 非 nil")
	}
	for _, k := range []string{"financial_assets_value", "long_equity_value"} {
		it := cvIndicatorByKey(t, cv, k)
		if it.Direction != dirNeutral {
			t.Errorf("%s.direction = %q, want %q", k, it.Direction, dirNeutral)
		}
		if it.TrendSignal != signalNone {
			t.Errorf("%s.trend_signal = %q, want 空（中性无信号）", k, it.TrendSignal)
		}
	}
}

// TestComputeCompanyValueTrendInsufficient 范围仅 1 年：三指标无趋势信号、trend_rate 为 nil。
func TestComputeCompanyValueTrendInsufficient(t *testing.T) {
	balance, cashflow, income := cvFixture()
	cv := ComputeCompanyValue(balance, cashflow, income, 2024, 2024)

	if len(cv.Years) != 1 {
		t.Fatalf("years = %v, want 长度 1", cv.Years)
	}
	for _, it := range cv.Indicators {
		if it.TrendSignal != signalNone {
			t.Errorf("指标 %q trend_signal = %q, want 空（数据不足）", it.Key, it.TrendSignal)
		}
		if it.TrendRate != nil {
			t.Errorf("指标 %q trend_rate = %v, want nil（数据不足）", it.Key, *it.TrendRate)
		}
	}
	// 方向属性与年份范围无关，仍须回填
	if cvIndicatorByKey(t, cv, "operating_fcf").Direction != dirHigherBetter {
		t.Errorf("operating_fcf.direction 未回填 higher_better")
	}
}
