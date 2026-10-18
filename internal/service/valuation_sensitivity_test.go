package service

import (
	"reflect"
	"testing"

	"financial-report/internal/model"
)

// R21 估值敏感性矩阵专项测试。
// 固定 fixture（valuationFixture）：基期 FCF=378、金融资产=1000、长投账面=200/收益率=10%、
// 有息债务=500、总股本=1000。

func sensitivityFixtureResult(t *testing.T, p model.ValuationParams, shares float64) model.ValuationResult {
	t.Helper()
	b, cf, inc := valuationFixture()
	res, err := ComputeValuation([]model.ReportRow{b}, []model.ReportRow{cf}, []model.ReportRow{inc}, p, shares)
	if err != nil {
		t.Fatalf("ComputeValuation 返回错误：%v", err)
	}
	return res
}

func assertFloats(t *testing.T, name string, got, want []float64) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v，期望 %v", name, got, want)
	}
}

func TestSensitivityGridShapePerpetual(t *testing.T) {
	res := sensitivityFixtureResult(t, model.ValuationParams{Model: "perpetual", DiscountRate: 10, GrowthRate: 5}, 1000)
	g := res.Sensitivity
	if g == nil {
		t.Fatal("Sensitivity 为 nil")
	}
	if !g.TerminalApplicable {
		t.Errorf("perpetual 应 TerminalApplicable=true")
	}
	if g.Unit != "元" {
		t.Errorf("Unit = %q，期望 元", g.Unit)
	}
	assertFloats(t, "rows", g.Rows, []float64{4.0, 4.5, 5.0, 5.5, 6.0})
	assertFloats(t, "cols", g.Cols, []float64{9.5, 10.0, 10.5})
	if g.BaseRow != 2 || g.BaseCol != 1 {
		t.Errorf("基准格 = (%d,%d)，期望 (2,1)", g.BaseRow, g.BaseCol)
	}
	if len(g.Cells) != 5 {
		t.Fatalf("cells 行数 = %d，期望 5", len(g.Cells))
	}
	for i, row := range g.Cells {
		if len(row) != 3 {
			t.Errorf("cells[%d] 列数 = %d，期望 3", i, len(row))
		}
	}
}

func TestSensitivityBaseCellEqualsSinglePoint(t *testing.T) {
	res := sensitivityFixtureResult(t, model.ValuationParams{Model: "perpetual", DiscountRate: 10, GrowthRate: 5}, 1000)
	if !approx(res.EquityValuePerShare, 8.638) {
		t.Fatalf("单点每股 = %v，期望 8.638", res.EquityValuePerShare)
	}
	cell := res.Sensitivity.Cells[res.Sensitivity.BaseRow][res.Sensitivity.BaseCol]
	if cell == nil {
		t.Fatal("基准格为 nil")
	}
	if !approx(*cell, res.EquityValuePerShare) {
		t.Errorf("基准格 = %v，单点 = %v", *cell, res.EquityValuePerShare)
	}
}

// TestSensitivityCellRecompute 逐格复算：经营资产 = 378×(1+g)/(r−g)，长投 = 200×10%/r，
// 股权 = max(0, 1000 + 长投 + 经营 − 500)，每股 = 股权/1000。
func TestSensitivityCellRecompute(t *testing.T) {
	res := sensitivityFixtureResult(t, model.ValuationParams{Model: "perpetual", DiscountRate: 10, GrowthRate: 5}, 1000)
	g := res.Sensitivity
	for i, gPct := range g.Rows {
		for j, rPct := range g.Cols {
			r := rPct / 100
			op := 378 * (1 + gPct/100) / (r - gPct/100)
			long := 200 * 0.10 / r
			want := (1000 + long + op - 500) / 1000
			cell := g.Cells[i][j]
			if cell == nil {
				t.Fatalf("cells[%d][%d] 不应为 nil", i, j)
			}
			if !approx(*cell, want) {
				t.Errorf("cells[%d][%d] = %v，手算 = %v", i, j, *cell, want)
			}
		}
	}
	// 基准行的 r=9.5% 列：长投按 9.5% 重算为 210.5263…，每股 9.5305…
	if !approx(*g.Cells[2][0], 9.530526315789474) {
		t.Errorf("cells[2][0] = %v，期望 9.530526315789474", *g.Cells[2][0])
	}
}

func TestSensitivityInvalidCells(t *testing.T) {
	res := sensitivityFixtureResult(t, model.ValuationParams{Model: "perpetual", DiscountRate: 6, GrowthRate: 5}, 1000)
	g := res.Sensitivity
	assertFloats(t, "rows", g.Rows, []float64{4, 4.5, 5, 5.5, 6})
	assertFloats(t, "cols", g.Cols, []float64{5.5, 6, 6.5})
	for _, pos := range [][2]int{{3, 0}, {4, 0}, {4, 1}} {
		if g.Cells[pos[0]][pos[1]] != nil {
			t.Errorf("cells[%d][%d] 应为 nil（r ≤ g）", pos[0], pos[1])
		}
	}
	if g.Cells[4][2] == nil {
		t.Errorf("cells[4][2] 不应为 nil（r=6.5 > g=6）")
	}
	if g.Cells[3][1] == nil {
		t.Errorf("cells[3][1] 不应为 nil（r=6 > g=5.5）")
	}
}

func TestSensitivityZeroModel(t *testing.T) {
	res := sensitivityFixtureResult(t, model.ValuationParams{Model: "zero", DiscountRate: 10}, 1000)
	g := res.Sensitivity
	if g == nil {
		t.Fatal("Sensitivity 为 nil")
	}
	if g.TerminalApplicable {
		t.Errorf("zero 应 TerminalApplicable=false")
	}
	if len(g.Rows) != 0 {
		t.Errorf("rows = %v，期望空数组", g.Rows)
	}
	if len(g.Cells) != 1 || len(g.Cells[0]) != 3 {
		t.Fatalf("cells 形状 = %d×%d，期望 1×3", len(g.Cells), len(g.Cells[0]))
	}
	if g.BaseRow != 0 || g.BaseCol != 1 {
		t.Errorf("基准格 = (%d,%d)，期望 (0,1)", g.BaseRow, g.BaseCol)
	}
	assertFloats(t, "cols", g.Cols, []float64{9.5, 10, 10.5})
	if cell := g.Cells[0][1]; cell == nil || !approx(*cell, 4.48) {
		t.Errorf("零增长基准格 = %v，期望 4.48", cell)
	}
}

func TestSensitivityInvalidRateZeroModel(t *testing.T) {
	res := sensitivityFixtureResult(t, model.ValuationParams{Model: "zero", DiscountRate: 0.5}, 1000)
	g := res.Sensitivity
	assertFloats(t, "cols", g.Cols, []float64{0, 0.5, 1})
	if g.Cells[0][0] != nil {
		t.Errorf("cells[0][0] 应为 nil（r=0）")
	}
	if g.Cells[0][1] == nil || g.Cells[0][2] == nil {
		t.Errorf("r>0 的格子不应为 nil")
	}
}

func TestSensitivityNoTotalShares(t *testing.T) {
	res := sensitivityFixtureResult(t, model.ValuationParams{Model: "perpetual", DiscountRate: 10, GrowthRate: 5}, 0)
	if res.Sensitivity != nil {
		t.Errorf("总股本为 0 时不应生成敏感性矩阵")
	}
	if res.EquityValuePerShare != 0 {
		t.Errorf("EquityValuePerShare = %v，期望 0", res.EquityValuePerShare)
	}
}

func TestSensitivityNegativeBaseGrowth(t *testing.T) {
	res := sensitivityFixtureResult(t, model.ValuationParams{Model: "perpetual", DiscountRate: 10, GrowthRate: 0}, 1000)
	g := res.Sensitivity
	assertFloats(t, "rows", g.Rows, []float64{-1, -0.5, 0, 0.5, 1})
	for i := range g.Rows {
		for j := range g.Cols {
			if g.Cells[i][j] == nil {
				t.Errorf("cells[%d][%d] 不应为 nil（r=10%% > g）", i, j)
			}
		}
	}
}

func TestSensitivityThreeStageUsesTerminal(t *testing.T) {
	p := model.ValuationParams{
		Model: "three_stage", DiscountRate: 10,
		Stage1Growth: 15, Stage1Years: 2, Stage2Growth: 5, Stage2Years: 2, TerminalGrowth: 3,
	}
	res := sensitivityFixtureResult(t, p, 1000)
	g := res.Sensitivity
	assertFloats(t, "rows", g.Rows, []float64{2, 2.5, 3, 3.5, 4})
	if g.Cells[2][1] == nil || !approx(*g.Cells[2][1], res.EquityValuePerShare) {
		t.Errorf("三阶段基准格 = %v，单点 = %v", g.Cells[2][1], res.EquityValuePerShare)
	}
	// 只替换 g3：基准格复算仍使用 g1=15%/n1=2、g2=5%/n2=2。
	want := dcfThreeStage(378, 0.15, 2, 0.05, 2, 0.03, 0.10)
	want = (1000 + 200 + want - 500) / 1000
	if !approx(*g.Cells[2][1], want) {
		t.Errorf("三阶段基准格 = %v，复算 = %v", *g.Cells[2][1], want)
	}
	// 相邻行只改终值段 g3（g1/g2 固定），与手算一致。
	row0 := dcfThreeStage(378, 0.15, 2, 0.05, 2, 0.02, 0.10)
	row0want := (1000 + 200 + row0 - 500) / 1000
	if !approx(*g.Cells[0][1], row0want) {
		t.Errorf("三阶段 rows[0] 格 = %v，复算 = %v", *g.Cells[0][1], row0want)
	}
}

func TestSensitivityLongEquityNilReturn(t *testing.T) {
	bal := row(map[string]float64{
		"MONETARYFUNDS": 1000,
		"SHORT_LOAN":    300,
		"LONG_LOAN":     200,
		"TOTAL_EQUITY":  1500,
		"TOTAL_ASSETS":  3000,
	})
	cf := row(map[string]float64{"NETCASH_OPERATE": 500}) // 保全性支出 0 → FCF=500
	inc := row(map[string]float64{})
	res, err := ComputeValuation([]model.ReportRow{bal}, []model.ReportRow{cf}, []model.ReportRow{inc},
		model.ValuationParams{Model: "perpetual", DiscountRate: 10, GrowthRate: 5}, 1000)
	if err != nil {
		t.Fatalf("ComputeValuation 返回错误：%v", err)
	}
	g := res.Sensitivity
	if g == nil {
		t.Fatal("Sensitivity 为 nil")
	}
	if res.LongEquityReturn != nil || res.LongEquityBook != 0 {
		t.Errorf("长投应为 0/nil，实际 %v/%v", res.LongEquityBook, res.LongEquityReturn)
	}
	// 长投价值按 0：经营资产 = 500×1.05/0.05 = 10500；每股 = (1000+10500−500)/1000 = 11。
	if !approx(*g.Cells[2][1], 11) {
		t.Errorf("基准格 = %v，期望 11", *g.Cells[2][1])
	}
}

func TestSensitivityNoCapReapplied(t *testing.T) {
	res := sensitivityFixtureResult(t, model.ValuationParams{Model: "perpetual", DiscountRate: 8.5, GrowthRate: 4.5}, 1000)
	g := res.Sensitivity
	assertFloats(t, "rows", g.Rows, []float64{3.5, 4.0, 4.5, 5.0, 5.5})
	assertFloats(t, "cols", g.Cols, []float64{8.0, 8.5, 9.0})
	// 行 3.5/5.0/5.5 不被 2.5% 上限或 SGR 钳制，全部有效。
	for i := range g.Rows {
		for j := range g.Cols {
			if g.Cells[i][j] == nil {
				t.Errorf("cells[%d][%d] 不应为 nil（矩阵不重新套用终值上限）", i, j)
			}
		}
	}
}

func TestSensitivityDeterministic(t *testing.T) {
	p := model.ValuationParams{Model: "two_stage", DiscountRate: 9, Stage1Growth: 12, Stage1Years: 3, Stage2Growth: 4}
	a := sensitivityFixtureResult(t, p, 1000)
	b := sensitivityFixtureResult(t, p, 1000)
	if !reflect.DeepEqual(a.Sensitivity, b.Sensitivity) {
		t.Errorf("同输入两次计算的敏感性矩阵不一致")
	}
}
