package service

import (
	"strings"
	"testing"
)

// R21《周期股终值基准与估值敏感性分析》专项测试（R22 起连续化）：
// 深度周期稳定度 0.2 对应终值上限 2.5%（宏观锚 4.5% 下 2% + 2.5% × 0.2），
// 2.5% 仅为上限，SGR 或 r−1pp 更低时取更低者。

func TestTerminalGrowthCyclicalR21(t *testing.T) {
	cases := []struct {
		name       string
		macro      float64
		discount   float64
		sgr        float64
		sgrOK      bool
		stability  float64
		want       float64
		constraint string
		contains   string
		excludes   string
	}{
		{"AC-1 深度周期 2.5% 生效", 4.5, 8.5, 3.0, true, 0.2, 2.5, "stability", "稳定度 0.20", ""},
		{"AC-2 SGR=1.8% 向下取整", 4.5, 8.5, 1.8, true, 0.2, 1.5, "sgr", "长期内生增速 1.8%", "稳定度"},
		{"AC-2 r−1=2.0% 更低", 4.5, 3.0, 8.0, true, 0.2, 2.0, "discount", "折现率−1pp", "稳定度"},
		{"AC-2 SGR=2.8% 仍受 2.5% 约束", 4.5, 8.5, 2.8, true, 0.2, 2.5, "stability", "稳定度 0.20", ""},
		{"AC-3 稳定消费约 4.0%", 4.5, 8.5, 0, false, 0.925, 4.0, "stability", "稳定度 0.93", ""},
		{"周期股 r−1≤0", 4.5, 1.0, 0, false, 0.2, 0, "zero", "终值按 0", ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			g, constraint, note := terminalGrowth(c.macro, c.discount, c.sgr, c.sgrOK, c.stability, 5)
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
			// 终值恒非负且严格低于折现率；稳定度 0.2 时不超过 2.5%。
			if g < 0 || g >= c.discount {
				t.Errorf("g∞=%v 越界（r=%v）", g, c.discount)
			}
			if c.stability <= 0.2+1e-9 && c.macro == 4.5 && g > 2.5+1e-9 {
				t.Errorf("稳定度 0.2 时 g∞=%v 超过 2.5%%", g)
			}
		})
	}
}

// 稳定度 0.2 对应的连续上限为 2.5%，且在 0.5pp 网格上不得被 floorHalf 向下误取为 2.0%。
func TestTerminalCyclicalGridR21(t *testing.T) {
	capValue := r22TerminalInflationPP + (r18GDPFallbackPP-r22TerminalInflationPP)*0.2
	if !approxEq(capValue, 2.5, 1e-9) {
		t.Errorf("稳定度 0.2 的上限 = %v，期望 2.5", capValue)
	}
	if got := floorHalf(capValue); got != 2.5 {
		t.Errorf("floorHalf(2.5) = %v，期望 2.5", got)
	}
}
