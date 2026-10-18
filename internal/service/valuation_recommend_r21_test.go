package service

import (
	"strings"
	"testing"
)

// R21《周期股终值基准与估值敏感性分析》专项测试：
// 周期/商品股终值上限由 2.0% 调整为 2.5%（长期通胀区间 2.0%–3.0% 的中点），
// 2.5% 仅为上限，SGR 或 r−1pp 更低时取更低者。

func TestTerminalGrowthCyclicalR21(t *testing.T) {
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
		{"AC-1 周期股 2.5% 生效", 4.5, 8.5, 3.0, true, true, 2.5, "cyclical", "长期通胀 2.5% 封顶", ""},
		{"AC-2 SGR=1.8% 向下取整", 4.5, 8.5, 1.8, true, true, 1.5, "sgr", "长期内生增速 1.8%", "2.5% 封顶"},
		{"AC-2 r−1=2.0% 更低", 4.5, 3.0, 8.0, true, true, 2.0, "discount", "折现率−1pp", "2.5% 封顶"},
		{"AC-2 SGR=2.8% 仍受 2.5% 约束", 4.5, 8.5, 2.8, true, true, 2.5, "cyclical", "2.5%", ""},
		{"AC-3 非周期仍取宏观锚 4.5%", 4.5, 8.5, 0, false, false, 4.5, "macro", "宏观锚", "2.5%"},
		{"周期股 r−1≤0", 4.5, 1.0, 0, false, true, 0, "zero", "终值按 0", ""},
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
			// 终值恒非负且严格低于折现率；周期股不超过 2.5%。
			if g < 0 || g >= c.discount {
				t.Errorf("g∞=%v 越界（r=%v）", g, c.discount)
			}
			if c.isCyclical && g > r20CyclicalTerminalPP+1e-9 {
				t.Errorf("周期股 g∞=%v 超过 %.1f%%", g, r20CyclicalTerminalPP)
			}
		})
	}
}

// 2.5% 在 0.5pp 网格上不得被 floorHalf 向下误取为 2.0%。
func TestTerminalCyclicalGridR21(t *testing.T) {
	if got := floorHalf(r20CyclicalTerminalPP); got != 2.5 {
		t.Errorf("floorHalf(2.5) = %v，期望 2.5", got)
	}
	if r20CyclicalTerminalPP != 2.5 {
		t.Errorf("r20CyclicalTerminalPP = %v，期望 2.5", r20CyclicalTerminalPP)
	}
}
