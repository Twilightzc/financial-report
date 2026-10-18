package service

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"financial-report/internal/model"
)

// R20《估值增长与终值保守化》专项测试：
//   g1 上限 = min(30%, max(前瞻锚, SGR) + 1pp)（前瞻锚参与，取较高者）；
//   周期/商品股终值增长率上限（R21 起为长期通胀 2.5%）。

func TestTerminalGrowthR20(t *testing.T) {
	cases := []struct {
		name       string
		macro      float64
		discount   float64
		sgr        float64
		sgrOK      bool
		isCyclical bool
		want       float64
		constraint string
		contains   string
		excludes   string
	}{
		{"周期股 2.5% 封顶", 4.5, 8.0, 8.0, true, true, 2.5, "cyclical", "周期股长期通胀 2.5%", ""},
		{"周期股 SGR 更低", 4.5, 8.0, 1.6, true, true, 1.5, "sgr", "长期内生增速 1.6%", "2.5% 封顶"},
		{"周期股 r−1 更低", 4.5, 2.8, 8.0, true, true, 1.5, "discount", "折现率−1pp", "2.5% 封顶"},
		{"周期股 SGR 不可用", 4.5, 8.0, 0, false, true, 2.5, "cyclical", "2.5%", ""},
		{"周期股 r−1≤0", 4.5, 1.0, 0, false, true, 0, "zero", "终值按 0", ""},
		{"非周期宏观锚", 4.5, 8.0, 0, false, false, 4.5, "macro", "宏观锚", "周期股长期通胀 2.5%"},
		{"非周期 SGR 生效", 7.0, 8.0, 3.2, true, false, 3.0, "sgr", "长期内生增速 3.2%", "周期股长期通胀 2.5%"},
		{"非周期 r−1 生效", 4.5, 4.2, 0, false, false, 3.0, "discount", "折现率−1pp", "周期股长期通胀 2.5%"},
		{"非周期不被压到 2.5%", 4.5, 8.0, 0, false, false, 4.5, "macro", "宏观锚", "周期股长期通胀 2.5%"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			g, constraint, note := terminalGrowth(c.macro, c.discount, c.sgr, c.sgrOK, c.isCyclical)
			if g != c.want {
				t.Errorf("g∞ = %v，期望 %v", g, c.want)
			}
			if constraint != c.constraint {
				t.Errorf("constraint = %q，期望 %q", constraint, c.constraint)
			}
			if c.contains != "" && !strings.Contains(note, c.contains) {
				t.Errorf("note = %q 应含 %q", note, c.contains)
			}
			if c.excludes != "" && strings.Contains(note, c.excludes) {
				t.Errorf("note = %q 不应含 %q", note, c.excludes)
			}
			// g∞ 恒非负且严格低于折现率。
			if g < 0 || g >= c.discount {
				t.Errorf("g∞=%v 越界（r=%v）", g, c.discount)
			}
			// 周期股终值不得高于长期通胀上限（R21 起 2.5%）。
			if c.isCyclical && g > r20CyclicalTerminalPP+1e-9 {
				t.Errorf("周期股 g∞=%v 超过 %.1f%%", g, r20CyclicalTerminalPP)
			}
		})
	}
}

func TestGrowthG1R20(t *testing.T) {
	cases := []struct {
		name       string
		A          float64
		gInf       float64
		fwd        float64
		sgr        float64
		fwdOK      bool
		sgrOK      bool
		want       float64
		constraint string
		contains   string
		excludes   string
	}{
		{"AC-1 SGR 更高时绑定 SGR", 20, 2.0, 5, 8.0, true, true, 9.0, "upper", "8.0% + 1pp", "前瞻一致预期"},
		{"AC-2a 不被无谓压低", 7, 2.0, 5, 8.0, true, true, 7.0, "none", "", ""},
		{"AC-2b 网格取整", 12, 2.0, 5, 8.2, true, true, 9.0, "upper", "8.2%", ""},
		{"前瞻锚参与（高于 SGR）", 20, 2.0, 12, 8.0, true, true, 13.0, "upper", "前瞻一致预期 12.0% + 1pp", ""},
		{"前瞻锚远高按 +1pp", 25, 2.0, 20, 8.0, true, true, 21.0, "upper", "20.0% + 1pp", "2pp"},
		{"SGR=8 无前瞻", 20, 2.0, 0, 8.0, false, true, 9.0, "upper", "8.0% + 1pp", ""},
		{"Q1 前瞻兜底", 20, 4.5, 12, 0, true, false, 13.0, "upper", "前瞻一致预期 12.0% + 1pp", ""},
		{"前瞻≤-1 按 0 收口", 20, 0, -1.2, 0, true, false, 0.0, "upper", "按 0 收口", "1pp 上限"},
		{"Q1 仅 30%", 40, 2.0, 0, 0, false, false, 30.0, "upper", "仅受 30%", ""},
		{"SGR=0", 20, 0, 0, 0, false, true, 1.0, "upper", "非正", ""},
		{"-1<SGR<0", 20, 0, 0, -0.5, false, true, 0.5, "upper", "-0.5%", ""},
		{"SGR≤-1", 20, 0, 0, -1.2, false, true, 0.0, "upper", "按 0 收口", ""},
		{"上限低于终值", 20, 4.5, -20, 0, true, false, 4.5, "cap_below_terminal", "上限低于终值", ""},
		{"A 低于终值", 3, 4.5, 12, 11, true, true, 4.5, "terminal", "终值收口", ""},
		{"纯网格取整不误报", 0.2, 0, 0, 8.0, false, true, 0.0, "none", "", ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			g1, constraint, note := growthG1(c.A, c.gInf, c.fwd, c.sgr, c.fwdOK, c.sgrOK)
			if g1 != c.want {
				t.Errorf("g1 = %v，期望 %v", g1, c.want)
			}
			if constraint != c.constraint {
				t.Errorf("constraint = %q，期望 %q", constraint, c.constraint)
			}
			if c.contains != "" && !strings.Contains(note, c.contains) {
				t.Errorf("note = %q 应含 %q", note, c.contains)
			}
			if c.excludes != "" && strings.Contains(note, c.excludes) {
				t.Errorf("note = %q 不应含 %q", note, c.excludes)
			}
			if g1 < 0 || g1+1e-9 < c.gInf || g1 > 30 {
				t.Errorf("g1=%v 越界（g∞=%v）", g1, c.gInf)
			}
			// 复算上限：前瞻锚与 SGR 均可用时取较高者 + 1pp。
			capG1 := 30.0
			switch {
			case c.fwdOK && c.sgrOK:
				capG1 = math.Min(30, math.Max(0, math.Max(c.fwd, c.sgr)+g1CapBufferPP))
			case c.sgrOK:
				capG1 = math.Min(30, math.Max(0, c.sgr+g1CapBufferPP))
			case c.fwdOK:
				capG1 = math.Min(30, math.Max(0, c.fwd+g1CapBufferPP))
			}
			if c.constraint != "cap_below_terminal" && g1 > capG1+1e-9 {
				t.Errorf("g1=%v 超过上限 %v（fwd=%v, sgr=%v）", g1, capG1, c.fwd, c.sgr)
			}
		})
	}
}

func TestTerminalNoFalseCyclicalNote(t *testing.T) {
	cases := []struct {
		name       string
		macro      float64
		discount   float64
		sgr        float64
		sgrOK      bool
		isCyclical bool
	}{
		{"非周期宏观锚", 4.5, 8.0, 0, false, false},
		{"非周期 SGR 约束", 7.0, 8.0, 3.2, true, false},
		{"周期股 SGR 更低", 4.5, 8.0, 1.6, true, true},
		{"周期股折现率更低", 4.5, 2.8, 8.0, true, true},
	}
	for _, c := range cases {
		if _, _, note := terminalGrowth(c.macro, c.discount, c.sgr, c.sgrOK, c.isCyclical); strings.Contains(note, "周期股长期通胀 2.5%") {
			t.Errorf("%s：2.5%% 未生效却输出封顶依据：%q", c.name, note)
		}
	}
}

func TestVerifyC4bBuffer(t *testing.T) {
	if warn := verifyC4b(8.9, 8.0, true); warn != "" {
		t.Errorf("1pp 缓冲内不应告警：%q", warn)
	}
	if warn := verifyC4b(9.2, 8.0, true); !strings.Contains(warn, "+ 1pp") {
		t.Errorf("超过 SGR+1pp 应告警且注明 + 1pp：%q", warn)
	}
}

// r20CyclicalFixture 构造确定性周期股样本：营收/经营现金流年增 20%，权益年增 10%，
// 2024 年现金分红为归母净利润的 20%（归母缺失 → 重构净利润），留存率 0.8。
func r20CyclicalFixture() (balance, cashflow, income []model.ReportRow, div model.DividendSnapshot) {
	years := []int{2021, 2022, 2023, 2024}
	rev, op, eq := 1000.0, 400.0, 2000.0
	for _, y := range years {
		income = append(income, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{
			"TOTAL_OPERATE_INCOME": rev,
			"OPERATE_COST":         rev * 0.7,
		}))
		cashflow = append(cashflow, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{
			"NETCASH_OPERATE": op,
			"FA_IR_DEPR":      100,
		}))
		balance = append(balance, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{
			"MONETARYFUNDS": 500,
			"SHORT_LOAN":    100,
			"LONG_LOAN":     200,
			"TOTAL_EQUITY":  eq,
			"TOTAL_ASSETS":  5000,
		}))
		rev *= 1.2
		op *= 1.2
		eq *= 1.1
	}
	// 2024 重构净利润 = 1728 × 0.3 = 518.4；分红 20% → 留存率 0.8。
	np := 1728.0 * 0.3
	div = model.DividendSnapshot{
		Available: true,
		Records:   []model.DividendRecord{{ReportYear: 2024, CashYuan: np * 0.2}},
	}
	return balance, cashflow, income, div
}

func TestRecommendCyclicalTerminalR20(t *testing.T) {
	balance, cashflow, income, div := r20CyclicalFixture()
	input := ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income,
		IndustryType: valIndustryCyclical,
		Snapshot:     model.GrowthSnapshot{Dividends: div},
		MarketCap:    100e8,
	}
	rec := RecommendValuation(input)

	if rec.GrowthTerminal > r20CyclicalTerminalPP+1e-9 {
		t.Errorf("周期股 g∞ = %v，应 ≤ %.1f", rec.GrowthTerminal, r20CyclicalTerminalPP)
	}
	if rec.TerminalConstraint != "cyclical" {
		t.Errorf("terminal_constraint = %q，期望 cyclical", rec.TerminalConstraint)
	}
	if !strings.Contains(rec.TerminalNote, "周期股长期通胀 2.5%") {
		t.Errorf("terminal_note = %q，应含周期股 2.5%% 依据", rec.TerminalNote)
	}

	// 复算 SGR = ROE × 留存率，验证 g1 ≤ SGR + 1pp。
	years := []int{2021, 2022, 2023, 2024}
	balanceByYear := make(map[int]model.ReportRow, len(years))
	incomeByYear := make(map[int]model.ReportRow, len(years))
	for i, y := range years {
		balanceByYear[y] = balance[i]
		incomeByYear[y] = income[i]
	}
	sgr, _, _, sgrOK, _ := sustainableGrowthR18(balanceByYear, incomeByYear, 2024, div)
	if !sgrOK || sgr <= 0 {
		t.Fatalf("样本 SGR 应可用且为正，实际 %v/%v", sgr, sgrOK)
	}
	if rec.Model == valModelTwoStage || rec.Model == valModelThreeStage {
		g1 := paramValue(t, rec.Params, "g1")
		if g1 > sgr+g1CapBufferPP+1e-9 {
			t.Errorf("g1 = %v 超过 SGR+1pp = %v", g1, sgr+g1CapBufferPP)
		}
		if g1 < rec.GrowthTerminal-1e-9 {
			t.Errorf("g1 = %v 低于终值 %v", g1, rec.GrowthTerminal)
		}
		if rec.G1Note == "" {
			t.Errorf("g1 被上限压低时应输出依据")
		}
	} else {
		t.Errorf("周期股高增长样本应为两阶段/三阶段，实际 %s", rec.Model)
	}

	// 模型可由 A − g∞ 与 R19 阈值复算（降级链可能导致实际模型更低，需标注已降级）。
	gap := rec.GrowthAnchor.Final - rec.GrowthTerminal
	classified, _, _ := classifyGrowthModel(rec.GrowthAnchor.Final, gap, rec.ModelScore, false, rec.NegativeGrowthType)
	if rec.Model != classified && !rec.ModelDegraded {
		t.Errorf("模型 %s 不可由 gap=%.2f 复算（分类结果 %s）", rec.Model, gap, classified)
	}

	// 依据句洁净：不出现内部键名与已废除字样。
	for _, text := range []string{rec.G1Note, rec.TerminalNote} {
		for _, banned := range []string{"sgr", "macro", "capBase", "SGR", "三档", "保守", "乐观", "带宽", "收窄"} {
			if strings.Contains(text, banned) {
				t.Errorf("依据句不应含 %q：%q", banned, text)
			}
		}
	}

	// 同一输入多次调用完全一致。
	if again := RecommendValuation(input); !reflect.DeepEqual(again, rec) {
		t.Errorf("同输入两次调用结果不一致")
	}
}
