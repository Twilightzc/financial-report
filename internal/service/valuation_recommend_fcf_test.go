package service

import (
	"math"
	"strings"
	"testing"
)

// TestRecommendBaseFCF 表驱动测试 R13 纯函数 RecommendBaseFCF（《技术设计文档》17.5 / 17.11）。
// 固定输入断言固定输出：方式、年数、custom 值与单位、口径关键措辞。
func TestRecommendBaseFCF(t *testing.T) {
	cases := []struct {
		name          string
		fcf           map[int]float64
		years         []int
		wantMode      string
		wantYears     int
		wantUnit      string
		wantCustom    float64 // custom 元值（0 表示不校验/非 custom）
		wantRationale string  // 口径说明须包含的片段（空表示不校验）
	}{
		{
			name:      "稳定增长",
			fcf:       map[int]float64{2022: 1e8, 2023: 1.1e8, 2024: 1.21e8},
			years:     []int{2022, 2023, 2024},
			wantMode:  valFCFLatest,
			wantYears: 0,
		},
		{
			name:      "剧烈波动",
			fcf:       map[int]float64{2022: 1e8, 2023: 3e8, 2024: 2e8},
			years:     []int{2022, 2023, 2024},
			wantMode:  valFCFMedian,
			wantYears: 3,
		},
		{
			name:          "最近一年偏高不可持续",
			fcf:           map[int]float64{2022: 1e8, 2023: 1e8, 2024: 5e8},
			years:         []int{2022, 2023, 2024},
			wantMode:      valFCFCustom,
			wantYears:     0,
			wantUnit:      "yi",
			wantCustom:    1e8,
			wantRationale: "可持续中枢",
		},
		{
			name:      "最近一年偏低",
			fcf:       map[int]float64{2022: 2e8, 2023: 5e8, 2024: 3e8, 2025: 1e8},
			years:     []int{2022, 2023, 2024, 2025},
			wantMode:  valFCFMedian,
			wantYears: 4,
		},
		{
			name:      "逐年下滑",
			fcf:       map[int]float64{2022: 3e8, 2023: 2e8, 2024: 1e8},
			years:     []int{2022, 2023, 2024},
			wantMode:  valFCFLatest,
			wantYears: 0,
		},
		{
			name:      "平稳",
			fcf:       map[int]float64{2022: 1e8, 2023: 1.2e8, 2024: 1.1e8},
			years:     []int{2022, 2023, 2024},
			wantMode:  valFCFAverage,
			wantYears: 3,
		},
		{
			name:          "全非正",
			fcf:           map[int]float64{2022: -1e8, 2023: -2e8, 2024: -3e8},
			years:         []int{2022, 2023, 2024},
			wantMode:      valFCFLatest,
			wantYears:     0,
			wantRationale: "均为非正",
		},
		{
			name:      "最近一年非正",
			fcf:       map[int]float64{2022: 1e8, 2023: 2e8, 2024: -1e8},
			years:     []int{2022, 2023, 2024},
			wantMode:  valFCFLatest,
			wantYears: 0,
		},
		{
			name:          "数据不足三年",
			fcf:           map[int]float64{2023: 1e8, 2024: 2e8},
			years:         []int{2023, 2024},
			wantMode:      valFCFLatest,
			wantYears:     0,
			wantRationale: "不足 3 年",
		},
		{
			name:      "无数据",
			fcf:       nil,
			years:     nil,
			wantMode:  valFCFLatest,
			wantYears: 0,
		},
		{
			name:      "custom 取整到整亿",
			fcf:       map[int]float64{2021: 1e8, 2022: 5e8, 2023: 2e8, 2024: 6e8},
			years:     []int{2021, 2022, 2023, 2024},
			wantMode:  valFCFCustom,
			wantYears: 0,
			wantUnit:  "yi",
			// latest 6 亿 > 1.3×中位数 3.5 亿，且增速极差 460pp > 40 → R6：
			// center 3.5 亿 ×（1 + 15%）= 4.025 亿 → round = 4 亿 = 4e8。
			wantCustom:    4e8,
			wantRationale: "可持续中枢",
		},
		{
			name:      "custom 取整溢出回落中位数",
			fcf:       map[int]float64{2022: 0.2e8, 2023: 0.3e8, 2024: 1e8},
			years:     []int{2022, 2023, 2024},
			wantMode:  valFCFMedian, // 取整后 < 1 亿 → 不产出 custom，退回 median
			wantYears: 3,
		},
		{
			name:  "阈值边界 gSpread 恰 40pp",
			fcf:   map[int]float64{2022: 1e8, 2023: 1.1e8, 2024: 1.65e8}, // +10% / +50% → 极差恰 40
			years: []int{2022, 2023, 2024},
			// gSpread <= 40 且 latest > 1.3×median → 不命中 R4/R6，兜底 average。
			wantMode:  valFCFAverage,
			wantYears: 3,
		},
		{
			name:      "阈值边界 latest 恰 1.3×median",
			fcf:       map[int]float64{2022: 1e8, 2023: 1e8, 2024: 1.3e8},
			years:     []int{2022, 2023, 2024},
			wantMode:  valFCFLatest, // 严格 > 1.3× 才算偏高 → 恰等于命中 R4
			wantYears: 0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RecommendBaseFCF(c.fcf, c.years, fcfCycle{})
			if got.Mode != c.wantMode {
				t.Fatalf("Mode = %s，期望 %s（Rationale：%s）", got.Mode, c.wantMode, got.Rationale)
			}
			if got.Years != c.wantYears {
				t.Errorf("Years = %d，期望 %d", got.Years, c.wantYears)
			}
			if got.ModeName != valFCFModeNames[got.Mode] {
				t.Errorf("ModeName = %q，与 %s 的中文名不一致", got.ModeName, got.Mode)
			}
			if got.Mode == valFCFCustom {
				if got.CustomUnit != c.wantUnit {
					t.Errorf("CustomUnit = %q，期望 %q", got.CustomUnit, c.wantUnit)
				}
				if math.Abs(got.CustomYuan-c.wantCustom) > 1e-6 {
					t.Errorf("CustomYuan = %v，期望 %v", got.CustomYuan, c.wantCustom)
				}
				if got.CustomYuan <= 0 {
					t.Errorf("custom 推荐值须 > 0，实际 %v", got.CustomYuan)
				}
				if got.CustomYuan/1e8 != math.Trunc(got.CustomYuan/1e8) {
					t.Errorf("custom 推荐值须为整亿倍数，实际 %v 元", got.CustomYuan)
				}
			} else if got.CustomYuan != 0 || got.CustomUnit != "" {
				t.Errorf("非 custom 不应有自定义值/单位：%+v", got)
			}
			if c.wantRationale != "" && !strings.Contains(got.Rationale, c.wantRationale) {
				t.Errorf("Rationale 应包含 %q，实际 %q", c.wantRationale, got.Rationale)
			}
		})
	}
}

// TestRecommendBaseFCFLowRatioBoundary 校验 R7 的「明显偏低」判据 `latest < 0.7 × median` 为严格小于：
// 恰等于 0.7×median 不触发 R7，略低才命中 R7（按 R7 专属措辞「明显低于多年中枢」区分）。
func TestRecommendBaseFCFLowRatioBoundary(t *testing.T) {
	const lowWording = "明显低于多年中枢"

	t.Run("恰等于 0.7×median 不触发 R7", func(t *testing.T) {
		// vals=[3e8,10e8,2.1e8]：median=3e8，latest=2.1e8 恰等于 0.7×median=2.1e8。
		// 因增速极差 > 40pp 走 R8 取中位数，但不得出现 R7 的「明显低于多年中枢」措辞。
		got := RecommendBaseFCF(map[int]float64{2022: 3e8, 2023: 10e8, 2024: 2.1e8}, []int{2022, 2023, 2024}, fcfCycle{})
		if got.Mode != valFCFMedian {
			t.Fatalf("Mode = %s，期望 median", got.Mode)
		}
		if strings.Contains(got.Rationale, lowWording) {
			t.Errorf("latest 恰等于 0.7×median 不应触发 R7（严格 <），实际 Rationale：%s", got.Rationale)
		}
	})

	t.Run("略低于 0.7×median 命中 R7", func(t *testing.T) {
		// vals=[3e8,10e8,1.4e8]：median=3e8，latest=1.4e8 < 0.7×median=2.1e8 → R7 取中位数。
		got := RecommendBaseFCF(map[int]float64{2022: 3e8, 2023: 10e8, 2024: 1.4e8}, []int{2022, 2023, 2024}, fcfCycle{})
		if got.Mode != valFCFMedian {
			t.Fatalf("Mode = %s，期望 median", got.Mode)
		}
		if !strings.Contains(got.Rationale, lowWording) {
			t.Errorf("latest 明显低于 0.7×median 应命中 R7，实际 Rationale：%s", got.Rationale)
		}
	})
}

// TestRecommendBaseFCFDeterministic 幂等：同一输入连续 3 次调用逐字段完全相同（FR-1 / FR-7）。
func TestRecommendBaseFCFDeterministic(t *testing.T) {
	fcf := map[int]float64{2022: 1e8, 2023: 1e8, 2024: 5e8}
	years := []int{2022, 2023, 2024}
	first := RecommendBaseFCF(fcf, years, fcfCycle{})
	for i := 0; i < 2; i++ {
		got := RecommendBaseFCF(fcf, years, fcfCycle{})
		if got != first {
			t.Fatalf("第 %d 次调用结果与首次不一致：\n首次 %+v\n本次 %+v", i+2, first, got)
		}
	}
	if first.Mode != valFCFCustom {
		t.Fatalf("前置用例应命中 custom，实际 %s", first.Mode)
	}
}

// TestRecommendBaseFCFDynamicWindow G9：窗口按 FCF 周期动态取 3/5/7 年，且不超过可用年数。
func TestRecommendBaseFCFDynamicWindow(t *testing.T) {
	years := []int{2017, 2018, 2019, 2020, 2021, 2022, 2023, 2024}
	fcf := make(map[int]float64, len(years))
	for i, y := range years {
		fcf[y] = float64(100+i) * 1e8 // 单调递增 → 无周期时命中 R4 latest
	}

	// 稳定（CV ≤ 0.3）→ 3 年窗口。
	got := RecommendBaseFCF(fcf, years, fcfCycle{Evaluated: true, CV: 0.1, WindowYears: 8})
	if got.Mode != valFCFLatest {
		t.Errorf("稳定序列应命中 latest，实际 %+v", got)
	}
	// 中等波动（0.3 < CV ≤ 0.5）→ 5 年；不强制 trim_mean。
	if got := RecommendBaseFCF(fcf, years, fcfCycle{Evaluated: true, CV: 0.4, WindowYears: 8}); got.Mode != valFCFLatest {
		t.Errorf("中等波动 + 单调增应命中 latest，实际 %+v", got)
	}
	// 强周期 → 7 年，且强制去极值平均。
	got = RecommendBaseFCF(fcf, years, fcfCycle{Evaluated: true, Cyclical: true, CV: 0.6, WindowYears: 8})
	if got.Mode != valFCFTrimMean || got.Years != valFCFYearsCyclical {
		t.Errorf("强周期应取 %d 年去极值平均，实际 %+v", valFCFYearsCyclical, got)
	}

	// 可用年数不足窗口时按可用年数：6 年且不可判定 → m=6。
	short := []int{2019, 2020, 2021, 2022, 2023, 2024}
	same := map[int]float64{2019: 1.1e8, 2020: 1.2e8, 2021: 1.1e8, 2022: 1.2e8, 2023: 1.1e8, 2024: 1.2e8}
	if got := RecommendBaseFCF(same, short, fcfCycle{}); got.Mode != valFCFAverage || got.Years != 6 {
		t.Errorf("6 年可用时应取 6 年，实际 %+v", got)
	}
}

// TestDetectFCFCycle G8：总体标准差 CV、符号翻转、年数不足与窗口截断。
func TestDetectFCFCycle(t *testing.T) {
	// 稳定增长：CV 小、无翻转。
	up := map[int]float64{2022: 1e8, 2023: 1.1e8, 2024: 1.21e8}
	if c := detectFCFCycle(up, []int{2022, 2023, 2024}, growthPattern{}); c.RawCyclical || c.Cyclical || !c.Evaluated || c.SignChanges != 0 {
		t.Errorf("稳定序列不应判为周期：%+v", c)
	}

	// 正负交替 2 次（+,-,+）→ 原始与生效判据均为强周期（无成长性否决）；0 值跳过、不计翻转。
	sign := map[int]float64{2020: 1e8, 2021: -1e8, 2022: 0, 2023: 2e8}
	if c := detectFCFCycle(sign, []int{2020, 2021, 2022, 2023}, growthPattern{}); !c.RawCyclical || !c.Cyclical || c.SignChanges != 2 {
		t.Errorf("正负交替 2 次应判强周期：%+v", c)
	}

	// 均值 ≤ 0 → 不做判定（CV 无意义）。
	neg := map[int]float64{2022: -1e8, 2023: 1e8, 2024: -1e8}
	if c := detectFCFCycle(neg, []int{2022, 2023, 2024}, growthPattern{}); c.Evaluated {
		t.Errorf("均值 ≤ 0 不应判定周期：%+v", c)
	}

	// 年数不足 3 → Evaluated=false。
	if c := detectFCFCycle(map[int]float64{2023: 1e8, 2024: 2e8}, []int{2023, 2024}, growthPattern{}); c.Evaluated || c.WindowYears != 2 {
		t.Errorf("不足 3 年不应判定：%+v", c)
	}

	// 窗口截断到 7 年（只吃最近 7 年）。
	years := []int{2017, 2018, 2019, 2020, 2021, 2022, 2023, 2024}
	fcf := make(map[int]float64, len(years))
	for _, y := range years {
		fcf[y] = 1e8
	}
	if c := detectFCFCycle(fcf, years, growthPattern{}); c.WindowYears != valCycleWindowYears {
		t.Errorf("窗口应截断到 %d 年，实际 %d", valCycleWindowYears, c.WindowYears)
	}
}

// TestFCFWindowYears 周期状态 → 窗口年数。
func TestFCFWindowYears(t *testing.T) {
	cases := []struct {
		c    fcfCycle
		want int
	}{
		{fcfCycle{Evaluated: false}, valFCFYearsCyclical}, // 不可判定不额外缩短（交给 R1）
		{fcfCycle{Evaluated: true, CV: 0.29}, valFCFYearsStable},
		{fcfCycle{Evaluated: true, CV: 0.31}, valFCFYearsMild},
		// CV 高但未判周期（如成长性否决后 Cyclical=false）不再单独触发 7 年：
		// 7 年只由生效判据 Cyclical 决定（R15/P5 修复点 1 的回归守卫）。
		{fcfCycle{Evaluated: true, CV: 0.51}, valFCFYearsMild},
		{fcfCycle{Evaluated: true, Cyclical: true, CV: 0.51}, valFCFYearsCyclical},
		{fcfCycle{Evaluated: true, Cyclical: true, CV: 0.1}, valFCFYearsCyclical},
		{fcfCycle{Evaluated: true, RawCyclical: true, GrowthVeto: true, CV: 0.51}, valFCFYearsGrowth},
	}
	for _, c := range cases {
		if got := fcfWindowYears(c.c); got != c.want {
			t.Errorf("fcfWindowYears(%+v) = %d，期望 %d", c.c, got, c.want)
		}
	}
}

// TestParseCustomFCFWordingNoResidual FR-9：自定义基期自由现金流校验串统一用「基期自由现金流」，
// 不得残留「基期现金流」。防回退。
func TestParseCustomFCFWordingNoResidual(t *testing.T) {
	cases := []struct{ value, unit string }{
		{"", "yi"}, {"0", "yi"}, {"1.234", "yi"}, {"abc", "yi"}, {"1", "foo"}, {"1000000000", "yi"},
	}
	for _, c := range cases {
		_, err := ParseCustomFCF(c.value, c.unit)
		if err == nil {
			t.Fatalf("ParseCustomFCF(%q,%q) 应报错", c.value, c.unit)
		}
		msg := err.Error()
		if strings.Contains(msg, "基期现金流") {
			t.Errorf("错误串残留「基期现金流」：%q", msg)
		}
		if !strings.Contains(msg, "基期自由现金流") {
			t.Errorf("错误串应使用「基期自由现金流」：%q", msg)
		}
	}
}
