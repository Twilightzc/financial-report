package service

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"financial-report/internal/model"
)

// R25《基期自由现金流选取的复苏感知优化》测试：3–5 年短窗口、复苏门、RF 收缩与零回归。
// 样本金额单位均为亿元（×1e8 转元），容差 ±0.01 亿元（1e6 元）。

var r25SampleYears = []int{2016, 2017, 2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025}

// r25Years 取样本年份的末尾 n 年（与「最近 n 个年报」口径一致）。
func r25Years(n int) []int { return r25SampleYears[len(r25SampleYears)-n:] }

// r25Series 由亿元序列构造 FCF map 与年份（年份为样本年份末尾 len(vals) 个）。
func r25Series(vals []float64) (map[int]float64, []int) {
	years := r25Years(len(vals))
	fcf := make(map[int]float64, len(vals))
	for i, v := range vals {
		fcf[years[i]] = v * 1e8
	}
	return fcf, years
}

// r25Fixture 构造 10 年三表：FCF 通过 NETCASH_OPERATE 注入（maintenanceCapex=0），
// 营收/成本恒定以避开成长性否决，保证 detectFCFCycle 只由 FCF 序列决定。
func r25Fixture(fcf []float64) (balance, cashflow, income []model.ReportRow) {
	for i, y := range r25SampleYears {
		date := fmt.Sprintf("%d-12-31", y)
		cashflow = append(cashflow, rowAt(date, map[string]float64{
			"NETCASH_OPERATE":      fcf[i] * 1e8,
			"CONSTRUCT_LONG_ASSET": 0,
			"DISPOSAL_LONG_ASSET":  0,
		}))
		income = append(income, rowAt(date, map[string]float64{
			"TOTAL_OPERATE_INCOME": 100e8,
			"OPERATE_COST":         70e8,
			"SALE_EXPENSE":         10e8,
		}))
		balance = append(balance, rowAt(date, map[string]float64{
			"TOTAL_EQUITY":  100e8,
			"TOTAL_ASSETS":  200e8,
			"MONETARYFUNDS": 20e8,
			"SHORT_LOAN":    10e8,
		}))
	}
	return
}

// r25DetectCycle 按 RecommendValuation 的同一取数口径复算周期判定（隔离断言用）。
func r25DetectCycle(balance, cashflow, income []model.ReportRow) fcfCycle {
	incomeByYear, incomeYears := annualRows(income)
	cfByYear, cfYears := annualRows(cashflow)
	_, balanceYears := annualRows(balance)
	latest := commonBaseYear(incomeYears, cfYears)
	if latest == 0 {
		latest = latestYear(incomeYears, cfYears, balanceYears)
	}
	incomeWindow := yearWindow(incomeYears, latest, valGrowthWindowYears)
	fcfYears := yearWindow(commonYears(incomeYears, cfYears), latest, valFCFRecMaxYears)
	fcfByYear := make(map[int]float64, len(fcfYears))
	for _, y := range fcfYears {
		fcfByYear[y] = operatingFreeCashFlow(cfByYear[y])
	}
	gp := assessGrowthPattern(incomeByYear, incomeWindow)
	return detectFCFCycle(fcfByYear, fcfYears, gp)
}

// r25AssertRecomputable 推荐 (mode, years) 必须能被 selectBaseFCF 原样复算（AC-7）。
func r25AssertRecomputable(t *testing.T, fcfByYear map[int]float64, years []int, rec fcfRecommendation) {
	t.Helper()
	v, _, used, err := selectBaseFCF(fcfByYear, years, rec.Mode, rec.Years, rec.CustomYuan)
	if err != nil {
		t.Fatalf("selectBaseFCF 复算失败：%v", err)
	}
	if rec.Mode == valFCFCustom {
		if v != rec.CustomYuan || used != nil {
			t.Fatalf("custom 复算应返回录入值且无年份：v=%v used=%v", v, used)
		}
		return
	}
	if rec.Mode != valFCFLatest {
		if len(used) != rec.Years {
			t.Fatalf("复算年份数 = %d，期望 %d", len(used), rec.Years)
		}
	} else if len(used) != 1 {
		t.Fatalf("latest 复算应返回最近一年，实际 %v", used)
	}
	if v <= 0 && rec.Mode != valFCFLatest {
		t.Fatalf("多年度复算结果应为正：%v（%+v）", v, rec)
	}
}

// T1 AC-1/2/3/7：三只真实样本的选取方式、窗口与精确基期值。
func TestRecommendBaseFCFR25Samples(t *testing.T) {
	cases := []struct {
		name        string
		vals        []float64
		cycle       fcfCycle
		wantMode    string
		wantYears   int
		wantBaseYi  float64
		rationale   []string
		notRational string
		eligible    bool
		accel       bool
	}{
		{
			name: "天山铝业002532", vals: []float64{
				1.732885, 0.189006, 0.568057, 32.027325, 19.838092,
				6.476388, 16.568070, 22.652591, 34.990193, 61.153321},
			cycle:     fcfCycle{Evaluated: true, Cyclical: true},
			wantMode:  valFCFTrimMean,
			wantYears: 3, wantBaseYi: 34.990193,
			rationale: []string{"近 3 年", "复苏"}, accel: true,
		},
		{
			name: "芭田股份002170", vals: []float64{
				1.022872, 0.188060, -0.181025, 3.214166, 2.137896,
				-0.750174, -0.200855, 0.458847, 3.114723, 10.351931},
			cycle:     fcfCycle{Evaluated: true, Cyclical: true},
			wantMode:  valFCFTrimMean,
			wantYears: 3, wantBaseYi: 3.114723,
			rationale: []string{"近 3 年", "复苏"}, accel: true,
		},
		{
			name: "中孚实业600595", vals: []float64{
				1.220239, -3.187900, -18.873326, -0.648720, -11.027724,
				5.744461, 8.277560, 10.898857, 4.250200, 1.203208},
			cycle:     fcfCycle{Evaluated: false},
			wantMode:  valFCFTrimMean,
			wantYears: 5, wantBaseYi: 6.090740,
			rationale: []string{"近 5 年"}, notRational: "复苏", eligible: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fcf, years := r25Series(tc.vals)
			rec := RecommendBaseFCF(fcf, years, tc.cycle)
			if rec.Mode != tc.wantMode || rec.Years != tc.wantYears {
				t.Fatalf("选取 = %s/%d，期望 %s/%d（%+v）",
					rec.Mode, rec.Years, tc.wantMode, tc.wantYears, rec)
			}
			if rec.ModeName != valFCFModeNames[tc.wantMode] {
				t.Errorf("方式中文名 = %q，期望 %q", rec.ModeName, valFCFModeNames[tc.wantMode])
			}
			v, _, used, err := selectBaseFCF(fcf, years, rec.Mode, rec.Years, rec.CustomYuan)
			if err != nil {
				t.Fatalf("selectBaseFCF: %v", err)
			}
			if !approxEq(v, tc.wantBaseYi*1e8, 1e6) {
				t.Errorf("基期 FCF = %.6f 亿，期望 %.6f 亿", v/1e8, tc.wantBaseYi)
			}
			if len(used) != tc.wantYears {
				t.Errorf("实际使用 %d 年，期望 %d 年", len(used), tc.wantYears)
			}
			for _, frag := range tc.rationale {
				if !strings.Contains(rec.Rationale, frag) {
					t.Errorf("口径说明 %q 应包含 %q", rec.Rationale, frag)
				}
			}
			if tc.notRational != "" && strings.Contains(rec.Rationale, tc.notRational) {
				t.Errorf("5 年窗口口径说明不应包含 %q：%q", tc.notRational, rec.Rationale)
			}
			if got := fcfRecoveryEligible(fcf, years); got != tc.eligible {
				t.Errorf("fcfRecoveryEligible = %v，期望 %v", got, tc.eligible)
			}
			if got := fcfRecoveryAccelerating(fcf, years); got != tc.accel {
				t.Errorf("fcfRecoveryAccelerating = %v，期望 %v", got, tc.accel)
			}
			r25AssertRecomputable(t, fcf, years, rec)
		})
	}

	// 芭田额外防回归：若错误走 5 年窗口会得到 1.124238 亿（低于旧值），必须由 3 年路径避免。
	fcf, years := r25Series([]float64{
		1.022872, 0.188060, -0.181025, 3.214166, 2.137896,
		-0.750174, -0.200855, 0.458847, 3.114723, 10.351931})
	if v, _, _, err := selectBaseFCF(fcf, years, valFCFTrimMean, 5, 0); err != nil || !approxEq(v, 1.124238e8, 1e6) {
		t.Fatalf("芭田 5 年 trim_mean 应为 1.124238 亿：v=%v err=%v", v, err)
	}
}

// T2 AC-3/AC-9：复苏门四边界 + mean10 含 0 + 可用 5 年同窗。
func TestFCFRecoveryGateR25(t *testing.T) {
	cases := []struct {
		name string
		vals []float64
		want bool
	}{
		{"命中", []float64{-100, -100, -100, -100, -100, 10, 10, 10, 10, 10}, true},
		{"mean5<=0", []float64{5, 5, 5, 5, 5, -10, -10, -10, -10, 5}, false},
		{"latest<=0", []float64{-100, -100, -100, -100, -100, 10, 10, 10, 10, -5}, false},
		{"可用不足3年", []float64{1, 2}, false},
		{"mean10>0", []float64{10, 10, 10, 10, 10, 10, 10, 10, 10, 10}, false},
		{"mean10==0边界", []float64{-10, -10, -10, -10, -10, 10, 10, 10, 10, 10}, true},
		{"可用5年同窗", []float64{10, 10, 10, 10, 10}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fcf, years := r25Series(tc.vals)
			if got := fcfRecoveryEligible(fcf, years); got != tc.want {
				t.Errorf("fcfRecoveryEligible = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// T3 AC-1/2/3：复苏加速判定。
func TestFCFRecoveryAcceleratingR25(t *testing.T) {
	cases := []struct {
		name string
		vals []float64
		want bool
	}{
		{"加速", []float64{10, 10, 10, 10, 20}, true},
		{"prev<=0", []float64{10, 10, 10, -5, 20}, false},
		{"latest<=prev", []float64{10, 10, 10, 20, 15}, false},
		{"latest==positiveMedian5", []float64{10, 10, 10, 10, 10}, false},
		{"仅1年", []float64{10}, false},
		{"负值不参与中位数", []float64{-100, 10, 10, 10, 20}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fcf, years := r25Series(tc.vals)
			if got := fcfRecoveryAccelerating(fcf, years); got != tc.want {
				t.Errorf("fcfRecoveryAccelerating = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// T4 AC-4/9/10：全局 5 年封顶与非周期路径零回归。
func TestRecommendBaseFCRAutoCapR25(t *testing.T) {
	alt10 := []float64{1.1, 1.2, 1.1, 1.2, 1.1, 1.2, 1.1, 1.2, 1.1, 1.2}
	cases := []struct {
		name      string
		vals      []float64
		cycle     fcfCycle
		wantMode  string
		wantYears int
	}{
		{"10年未知", alt10, fcfCycle{}, valFCFAverage, 5},
		{"6年未知", alt10[4:], fcfCycle{}, valFCFAverage, 5},
		{"4年未知", alt10[6:], fcfCycle{}, valFCFAverage, 4},
		{"3年未知", []float64{1.1, 1.2, 1.1}, fcfCycle{}, valFCFAverage, 3},
		{"稳定10年", alt10, fcfCycle{Evaluated: true, CV: 0.1}, valFCFAverage, 3},
		{"温和10年", alt10, fcfCycle{Evaluated: true, CV: 0.4}, valFCFMedian, 5},
		{"成长否决10年", alt10, fcfCycle{Evaluated: true, RawCyclical: true, GrowthVeto: true, CV: 0.51}, valFCFMedian, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fcf, years := r25Series(tc.vals)
			rec := RecommendBaseFCF(fcf, years, tc.cycle)
			if rec.Mode != tc.wantMode || rec.Years != tc.wantYears {
				t.Fatalf("选取 = %s/%d，期望 %s/%d（%+v）",
					rec.Mode, rec.Years, tc.wantMode, tc.wantYears, rec)
			}
			if rec.Years > valFCFAutoMaxYears {
				t.Errorf("自动多年度窗口 %d 超过封顶 %d", rec.Years, valFCFAutoMaxYears)
			}
			r25AssertRecomputable(t, fcf, years, rec)
		})
	}
}

// T5 AC-5/9：RF（波动大）改为近期中位数，且只缩短不拉长。
func TestRecommendBaseFCFRFPolicyR25(t *testing.T) {
	cases := []struct {
		name      string
		vals      []float64
		cycle     fcfCycle
		wantMode  string
		wantYears int
		wantBase  float64 // 亿元
		rationale string
	}{
		{
			name:      "10年高极差近5年无非正",
			vals:      []float64{1, 1, 1, 1, 1, 10, 10, 1, 10, 10},
			cycle:     fcfCycle{},
			wantMode:  valFCFMedian,
			wantYears: 5, wantBase: 10,
			rationale: "取近 5 年中位数",
		},
		{
			name:      "5年含2个非正收缩到3年",
			vals:      []float64{10, 10, 10, 10, 10, 10, -1, -2, 5, 5.5},
			cycle:     fcfCycle{},
			wantMode:  valFCFMedian,
			wantYears: 3, wantBase: 5,
			rationale: "取近 3 年中位数",
		},
		{
			name:      "RF结果非正退回最近一年",
			vals:      []float64{10, 10, 10, 10, 10, 10, 10, -10, -1, 10},
			cycle:     fcfCycle{},
			wantMode:  valFCFLatest,
			wantYears: 0, wantBase: 10,
			rationale: "多年度结果非正",
		},
		{
			name:      "3年窗口不被拉长",
			vals:      []float64{1, 10, 5},
			cycle:     fcfCycle{},
			wantMode:  valFCFMedian,
			wantYears: 3, wantBase: 5,
			rationale: "取近 3 年中位数",
		},
		{
			name:      "稳定3年窗口不变",
			vals:      []float64{1, 10, 5},
			cycle:     fcfCycle{Evaluated: true, CV: 0.1},
			wantMode:  valFCFMedian,
			wantYears: 3, wantBase: 5,
			rationale: "取近 3 年中位数",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fcf, years := r25Series(tc.vals)
			rec := RecommendBaseFCF(fcf, years, tc.cycle)
			if rec.Mode != tc.wantMode || rec.Years != tc.wantYears {
				t.Fatalf("选取 = %s/%d，期望 %s/%d（%+v）",
					rec.Mode, rec.Years, tc.wantMode, tc.wantYears, rec)
			}
			if !strings.Contains(rec.Rationale, tc.rationale) {
				t.Errorf("口径说明 %q 应包含 %q", rec.Rationale, tc.rationale)
			}
			if rec.Mode != valFCFLatest {
				if v, _, _, err := selectBaseFCF(fcf, years, rec.Mode, rec.Years, 0); err != nil || !approxEq(v, tc.wantBase*1e8, 1e6) {
					t.Errorf("基期 FCF = %v，期望 %v 亿（err=%v）", v, tc.wantBase, err)
				}
			}
			r25AssertRecomputable(t, fcf, years, rec)
		})
	}
}

// T6 AC-1/2/9：周期/复苏 3–5 年自适应 trim_mean 与回退顺序。
func TestRecommendBaseFCFCyclicalR25(t *testing.T) {
	cyc := fcfCycle{Evaluated: true, Cyclical: true}
	cases := []struct {
		name      string
		vals      []float64
		wantMode  string
		wantYears int
		wantBase  float64
		rationale string
	}{
		{"4年加速", []float64{1, 5, 2, 6}, valFCFTrimMean, 3, 5, "复苏"},
		{"5年非加速", []float64{1, 2, 3, 5, 4}, valFCFTrimMean, 5, 3, "近 5 年去极值平均"},
		{"4年非加速", []float64{1, 2, 4, 3}, valFCFTrimMean, 4, 2.5, "近 4 年去极值平均"},
		{"prev非正", []float64{1, 2, 3, -1, 5}, valFCFTrimMean, 5, 2, "近 5 年去极值平均"},
		{"trimMean非正latest正", []float64{-100, -50, 10}, valFCFLatest, 0, 10, "多年度结果非正"},
		{"trimMean正latest非正", []float64{-100, 50, 60, 70, -10}, valFCFTrimMean, 5, 33.333333, "近 5 年去极值平均"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fcf, years := r25Series(tc.vals)
			rec := RecommendBaseFCF(fcf, years, cyc)
			if rec.Mode != tc.wantMode || rec.Years != tc.wantYears {
				t.Fatalf("选取 = %s/%d，期望 %s/%d（%+v）",
					rec.Mode, rec.Years, tc.wantMode, tc.wantYears, rec)
			}
			if !strings.Contains(rec.Rationale, tc.rationale) {
				t.Errorf("口径说明 %q 应包含 %q", rec.Rationale, tc.rationale)
			}
			if rec.Mode != valFCFLatest {
				if v, _, _, err := selectBaseFCF(fcf, years, rec.Mode, rec.Years, 0); err != nil || !approxEq(v, tc.wantBase*1e8, 1e6) {
					t.Errorf("基期 FCF = %v，期望 %v 亿（err=%v）", v, tc.wantBase, err)
				}
			}
			r25AssertRecomputable(t, fcf, years, rec)
		})
	}
}

// T7 AC-9：FR-5 第 4 条非正多年度结果回退最近一年。
func TestRecommendBaseFCFMultiYearGuardR25(t *testing.T) {
	cases := []struct {
		name      string
		vals      []float64
		wantMode  string
		wantYears int
		wantBase  float64
		rationale string
	}{
		{"average非正latest正", []float64{-100, -50, 10}, valFCFLatest, 0, 10, "多年度结果非正"},
		{"全非正", []float64{-1, -2, -3}, valFCFLatest, 0, -3, "均为非正"},
		{"多年度为正", []float64{1.2, 1.1, 1.2}, valFCFAverage, 3, 1.1666667, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fcf, years := r25Series(tc.vals)
			rec := RecommendBaseFCF(fcf, years, fcfCycle{})
			if rec.Mode != tc.wantMode || rec.Years != tc.wantYears {
				t.Fatalf("选取 = %s/%d，期望 %s/%d（%+v）",
					rec.Mode, rec.Years, tc.wantMode, tc.wantYears, rec)
			}
			if tc.rationale != "" && !strings.Contains(rec.Rationale, tc.rationale) {
				t.Errorf("口径说明 %q 应包含 %q", rec.Rationale, tc.rationale)
			}
			v, _, _, err := selectBaseFCF(fcf, years, rec.Mode, rec.Years, 0)
			if err != nil || !approxEq(v, tc.wantBase*1e8, 1e6) {
				t.Errorf("基期 FCF = %v，期望 %v 亿（err=%v）", v, tc.wantBase, err)
			}
		})
	}
}

// T8 AC-7/10：推荐 (mode, years) 可由 selectBaseFCF 复算；custom 直接取录入值。
func TestRecommendBaseFCFR25Recomputable(t *testing.T) {
	cases := []struct {
		name  string
		vals  []float64
		cycle fcfCycle
	}{
		{"天山3年", []float64{1.732885, 0.189006, 0.568057, 32.027325, 19.838092,
			6.476388, 16.568070, 22.652591, 34.990193, 61.153321}, fcfCycle{Evaluated: true, Cyclical: true}},
		{"中孚5年", []float64{1.220239, -3.187900, -18.873326, -0.648720, -11.027724,
			5.744461, 8.277560, 10.898857, 4.250200, 1.203208}, fcfCycle{Evaluated: false}},
		{"RF5年", []float64{1, 1, 1, 1, 1, 10, 10, 1, 10, 10}, fcfCycle{}},
		{"周期非加速", []float64{1, 2, 3, 5, 4}, fcfCycle{Evaluated: true, Cyclical: true}},
		{"最新一年", []float64{10, 20, 30}, fcfCycle{Evaluated: true, CV: 0.1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fcf, years := r25Series(tc.vals)
			rec := RecommendBaseFCF(fcf, years, tc.cycle)
			r25AssertRecomputable(t, fcf, years, rec)
		})
	}

	// custom：selectBaseFCF 直接返回录入值，不涉及年份。
	custom := fcfRecommendation{Mode: valFCFCustom, CustomYuan: 3.2e8}
	fcf, years := r25Series([]float64{1, 2, 3})
	v, _, used, err := selectBaseFCF(fcf, years, custom.Mode, custom.Years, custom.CustomYuan)
	if err != nil || v != custom.CustomYuan || used != nil {
		t.Fatalf("custom 复算 = %v/%v/%v，期望 3.2e8/nil", v, used, err)
	}
}

// T9 AC-1/2/3/6/12：三只样本端到端推荐与周期判定隔离。
func TestRecommendValuationR25Samples(t *testing.T) {
	cases := []struct {
		name       string
		vals       []float64
		wantYears  int
		wantBaseYi float64
		wantStart  int
		wantEnd    int
		cyclical   bool
		// 中孚为 Evaluated=false：CV/SignChanges 直接断言；天山/芭田仅断言周期标记，
		// CV/翻转由下方「与 detectFCFCycle 一致」的隔离断言覆盖。
		checkCycleDetail bool
		cv               float64
		signChange       int
	}{
		{
			name: "天山铝业002532", vals: []float64{
				1.732885, 0.189006, 0.568057, 32.027325, 19.838092,
				6.476388, 16.568070, 22.652591, 34.990193, 61.153321},
			wantYears: 3, wantBaseYi: 34.990193, wantStart: 2023, wantEnd: 2025, cyclical: true,
		},
		{
			name: "芭田股份002170", vals: []float64{
				1.022872, 0.188060, -0.181025, 3.214166, 2.137896,
				-0.750174, -0.200855, 0.458847, 3.114723, 10.351931},
			wantYears: 3, wantBaseYi: 3.114723, wantStart: 2023, wantEnd: 2025, cyclical: true,
		},
		{
			name: "中孚实业600595", vals: []float64{
				1.220239, -3.187900, -18.873326, -0.648720, -11.027724,
				5.744461, 8.277560, 10.898857, 4.250200, 1.203208},
			wantYears: 5, wantBaseYi: 6.090740, wantStart: 2021, wantEnd: 2025,
			cyclical: false, checkCycleDetail: true, cv: 0, signChange: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			balance, cashflow, income := r25Fixture(tc.vals)
			rec := RecommendValuation(ValuationRecommendInput{
				Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 1000e8,
			})

			if rec.FCFMode != valFCFTrimMean || rec.FCFYears != tc.wantYears {
				t.Fatalf("FCF 选取 = %s/%d，期望 trim_mean/%d", rec.FCFMode, rec.FCFYears, tc.wantYears)
			}
			if !approxEq(rec.FCFBaseValue, tc.wantBaseYi*1e8, 1e6) ||
				!approxEq(rec.FCFBaseBeforeRD, tc.wantBaseYi*1e8, 1e6) {
				t.Errorf("基期 FCF = %v（研发前 %v），期望 %.6f 亿",
					rec.FCFBaseValue, rec.FCFBaseBeforeRD, tc.wantBaseYi)
			}
			if rec.FCFWindowStart != tc.wantStart || rec.FCFWindowEnd != tc.wantEnd {
				t.Errorf("选取窗口 = %d–%d，期望 %d–%d",
					rec.FCFWindowStart, rec.FCFWindowEnd, tc.wantStart, tc.wantEnd)
			}
			if rec.FCFCyclical != tc.cyclical {
				t.Errorf("FCFCyclical = %v，期望 %v", rec.FCFCyclical, tc.cyclical)
			}
			if tc.checkCycleDetail && (!approxEq(rec.FCFCV, tc.cv, 1e-9) || rec.FCFSignChanges != tc.signChange) {
				t.Errorf("FCFCV/翻转 = %v/%d，期望 %v/%d",
					rec.FCFCV, rec.FCFSignChanges, tc.cv, tc.signChange)
			}
			// AC-12：下游校验窗口与基期 FCF 窗口一致。
			if rec.CapexCheck.WindowStart != rec.FCFWindowStart || rec.CapexCheck.WindowEnd != rec.FCFWindowEnd {
				t.Errorf("CapexCheck 窗口 %d–%d 与 FCF 窗口 %d–%d 不一致",
					rec.CapexCheck.WindowStart, rec.CapexCheck.WindowEnd,
					rec.FCFWindowStart, rec.FCFWindowEnd)
			}
			if rec.FCFProfitCheck.WindowStart != rec.FCFWindowStart || rec.FCFProfitCheck.WindowEnd != rec.FCFWindowEnd {
				t.Errorf("FCFProfitCheck 窗口 %d–%d 与 FCF 窗口 %d–%d 不一致",
					rec.FCFProfitCheck.WindowStart, rec.FCFProfitCheck.WindowEnd,
					rec.FCFWindowStart, rec.FCFWindowEnd)
			}
			// 历史展示不随选取窗口缩短。
			if rec.FCFWindowYears != 10 || len(rec.FCFSeries) != 10 {
				t.Errorf("FCFWindowYears/series = %d/%d，期望 10/10", rec.FCFWindowYears, len(rec.FCFSeries))
			}
			// AC-6 隔离：周期判定输出与直接调用 detectFCFCycle 完全一致。
			cyc := r25DetectCycle(balance, cashflow, income)
			if rec.FCFCyclical != cyc.Cyclical || rec.FCFCV != cyc.CV || rec.FCFSignChanges != cyc.SignChanges {
				t.Errorf("周期输出被污染：rec=%v/%v/%d cycle=%v/%v/%d",
					rec.FCFCyclical, rec.FCFCV, rec.FCFSignChanges,
					cyc.Cyclical, cyc.CV, cyc.SignChanges)
			}
			// AC-7：用推荐 (mode, years) 在 DCF 侧复算一致。
			cfByYear, cfYears := annualRows(cashflow)
			fcfByYear := make(map[int]float64, len(cfYears))
			for _, y := range cfYears {
				fcfByYear[y] = operatingFreeCashFlow(cfByYear[y])
			}
			v, _, _, err := selectBaseFCF(fcfByYear, cfYears, rec.FCFMode, rec.FCFYears, 0)
			if err != nil || !approxEq(v, rec.FCFBaseBeforeRD, 0.01) {
				t.Errorf("DCF 侧复算 = %v，期望 %v（err=%v）", v, rec.FCFBaseBeforeRD, err)
			}
		})
	}
}

// T10 AC-8：同一输入连续 3 次推荐结果逐字段一致。
func TestRecommendValuationR25Deterministic(t *testing.T) {
	vals := []float64{1.220239, -3.187900, -18.873326, -0.648720, -11.027724,
		5.744461, 8.277560, 10.898857, 4.250200, 1.203208}
	balance, cashflow, income := r25Fixture(vals)
	in := ValuationRecommendInput{Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 1000e8}
	first := RecommendValuation(in)
	for i := 0; i < 2; i++ {
		if got := RecommendValuation(in); !reflect.DeepEqual(got, first) {
			t.Fatalf("第 %d 次推荐结果与首次不一致", i+2)
		}
	}
}
