package service

import (
	"math"
	"testing"

	"financial-report/internal/model"
)

// row 构造单期报表原始行（数值字段，报告期固定 2024-12-31）。
func row(fields map[string]float64) model.ReportRow {
	return rowAt("2024-12-31", fields)
}

// rowAt 构造指定报告期的单期报表原始行。
func rowAt(date string, fields map[string]float64) model.ReportRow {
	m := make(map[string]*float64, len(fields))
	for k, v := range fields {
		m[k] = fp(v)
	}
	return model.ReportRow{ReportDate: date, Fields: m}
}

// valuationFixture 构造估值用的三张表最新年报。
// 金融资产 = 1000（货币资金）；长期股权投资账面 = 200、收益 = 20（收益率 10%）；
// 有息债务 = 短期借款 300 + 长期借款 200 = 500；股东权益 = 1500。
// 经营现金流净额 = 500；保全性资本支出 = 资产减值 10 + 信用减值 5 + 折旧 80 + 使用权摊销 10 +
// 无形资产摊销 6 + 长期待摊摊销 4 + 处置损失 5 + 报废损失 2 = 122 → FCF = 378。
func valuationFixture() (balance, cashflow, income model.ReportRow) {
	balance = row(map[string]float64{
		"MONETARYFUNDS":      1000,
		"LONG_EQUITY_INVEST": 200,
		"SHORT_LOAN":         300,
		"LONG_LOAN":          200,
		"TOTAL_EQUITY":       1500,
		"TOTAL_ASSETS":       3000,
	})
	cashflow = row(map[string]float64{
		"NETCASH_OPERATE":         500,
		"FA_IR_DEPR":              80,
		"USERIGHT_ASSET_AMORTIZE": 10,
		"IA_AMORTIZE":             6,
		"LPE_AMORTIZE":            4,
		"DISPOSAL_LONGASSET_LOSS": 5,
		"FA_SCRAP_LOSS":           2,
	})
	income = row(map[string]float64{
		"INVEST_JOINT_INCOME":      20,
		"ASSET_IMPAIRMENT_INCOME":  -10, // 收益口径，负值 = 损失 10
		"CREDIT_IMPAIRMENT_INCOME": -5,  // 收益口径，负值 = 损失 5
	})
	return
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestOperatingFreeCashFlow(t *testing.T) {
	_, cf, inc := valuationFixture()
	got := operatingFreeCashFlow(inc, cf)
	if want := 378.0; !approx(got, want) {
		t.Errorf("经营资产自由现金流 = %v，期望 %v", got, want)
	}
}

// TestDCFZeroAndPerpetual 零增长与永续增长公式（含 g=0 退化为零增长）。
func TestDCFZeroAndPerpetual(t *testing.T) {
	if got := dcfZero(100, 0.10); !approx(got, 1000) {
		t.Errorf("dcfZero = %v，期望 1000", got)
	}
	if got := dcfPerpetual(100, 0.05, 0.10); !approx(got, 2100) {
		t.Errorf("dcfPerpetual(100,5%%,10%%) = %v，期望 2100", got)
	}
	if got := dcfPerpetual(100, 0, 0.10); !approx(got, 1000) {
		t.Errorf("dcfPerpetual g=0 应退化为零增长 = %v，期望 1000", got)
	}
}

// TestDCFTwoStage 两阶段模型：高增长 10% 两年后进入永续增长 5%。
func TestDCFTwoStage(t *testing.T) {
	// fcf=100, g1=10%, n1=2, g2=5%, r=10%：
	// 第1年 110/1.1=100；第2年 121/1.21=100；终值 121×1.05/0.05=2541，折现 2541/1.21=2100 → 合计 2300。
	if got := dcfTwoStage(100, 0.10, 2, 0.05, 0.10); !approx(got, 2300) {
		t.Errorf("dcfTwoStage = %v，期望 2300", got)
	}
	// g1=g2=0 时应退化为零增长永续 = 1000。
	if got := dcfTwoStage(100, 0, 2, 0, 0.10); !approx(got, 1000) {
		t.Errorf("dcfTwoStage g=0 = %v，期望 1000", got)
	}
}

// TestDCFThreeStage 三阶段模型：g1(1年) → g2(1年) → 永续 g3，全 0 增长应退化为零增长永续。
func TestDCFThreeStage(t *testing.T) {
	if got := dcfThreeStage(100, 0, 1, 0, 1, 0, 0.10); !approx(got, 1000) {
		t.Errorf("dcfThreeStage g=0 = %v，期望 1000", got)
	}
}

// TestComputeValuationZeroGrowth 零增长模型完整链路校验。
func TestComputeValuationZeroGrowth(t *testing.T) {
	b, cf, inc := valuationFixture()
	res, err := ComputeValuation([]model.ReportRow{b}, []model.ReportRow{cf}, []model.ReportRow{inc},
		model.ValuationParams{Model: "zero", DiscountRate: 10}, 1000)
	if err != nil {
		t.Fatalf("ComputeValuation 返回错误：%v", err)
	}
	if res.BaseFCF != 378 {
		t.Errorf("BaseFCF = %v，期望 378", res.BaseFCF)
	}
	if res.FinancialAssetValue != 1000 {
		t.Errorf("金融资产价值 = %v，期望 1000", res.FinancialAssetValue)
	}
	// 长投收益率 10% = 折现率 10% → 收益资本化 = 账面 200。
	if res.LongEquityValue != 200 {
		t.Errorf("长期股权投资价值 = %v，期望 200", res.LongEquityValue)
	}
	if res.OperatingAssetValue != 3780 {
		t.Errorf("经营资产价值 = %v，期望 3780", res.OperatingAssetValue)
	}
	if res.CompanyValue != 4980 {
		t.Errorf("公司价值 = %v，期望 4980", res.CompanyValue)
	}
	if res.DebtValue != 500 {
		t.Errorf("债务价值 = %v，期望 500", res.DebtValue)
	}
	if res.EquityValue != 4480 {
		t.Errorf("股权价值 = %v，期望 4480", res.EquityValue)
	}
	if !approx(res.EquityValuePerShare, 4.48) {
		t.Errorf("每股股权价值 = %v，期望 4.48", res.EquityValuePerShare)
	}
}

// TestComputeValuationPerpetual 永续增长模型校验。
func TestComputeValuationPerpetual(t *testing.T) {
	b, cf, inc := valuationFixture()
	res, err := ComputeValuation([]model.ReportRow{b}, []model.ReportRow{cf}, []model.ReportRow{inc},
		model.ValuationParams{Model: "perpetual", DiscountRate: 10, GrowthRate: 5}, 1000)
	if err != nil {
		t.Fatalf("ComputeValuation 返回错误：%v", err)
	}
	// 经营资产价值 = 378×1.05/(0.10−0.05) = 7938；公司价值 = 1000 + 200 + 7938 = 9138。
	if !approx(res.OperatingAssetValue, 7938) {
		t.Errorf("经营资产价值 = %v，期望 7938", res.OperatingAssetValue)
	}
	if !approx(res.CompanyValue, 9138) {
		t.Errorf("公司价值 = %v，期望 9138", res.CompanyValue)
	}
	if !approx(res.EquityValue, 9138-500) {
		t.Errorf("股权价值 = %v，期望 %v", res.EquityValue, 9138-500)
	}
}

// TestLongEquityValueCapitalization 长期股权投资价值收益资本化：折价/账面/溢价/负收益。
func TestLongEquityValueCapitalization(t *testing.T) {
	cases := []struct {
		name          string
		longInvest    float64 // 长期股权投资账面
		jointIncome   float64 // 长期股权投资收益
		discountRate  float64
		wantLongValue float64
	}{
		{"收益率=折现率→账面", 200, 20, 10, 200}, // 10% = 10%
		{"收益率>折现率→溢价", 200, 20, 5, 400},  // 10% > 5% → 200×(10/5)=400
		{"收益率<折现率→折价", 200, 20, 20, 100}, // 10% < 20% → 200×(10/20)=100
		{"收益率为负→0", 200, -10, 10, 0},     // 收益率 -5% → 0
		{"长投为0→0", 0, 0, 10, 0},          // 无长投
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := row(map[string]float64{
				"MONETARYFUNDS":      1000,
				"LONG_EQUITY_INVEST": c.longInvest,
				"SHORT_LOAN":         300,
				"LONG_LOAN":          200,
				"TOTAL_EQUITY":       1500,
				"TOTAL_ASSETS":       3000,
			})
			inc := row(map[string]float64{
				"INVEST_JOINT_INCOME":      c.jointIncome,
				"ASSET_IMPAIRMENT_INCOME":  -10,
				"CREDIT_IMPAIRMENT_INCOME": -5,
			})
			_, cf, _ := valuationFixture()
			res, err := ComputeValuation([]model.ReportRow{b}, []model.ReportRow{cf}, []model.ReportRow{inc},
				model.ValuationParams{Model: "zero", DiscountRate: c.discountRate}, 1000)
			if err != nil {
				t.Fatalf("ComputeValuation 返回错误：%v", err)
			}
			if !approx(res.LongEquityValue, c.wantLongValue) {
				t.Errorf("长期股权投资价值 = %v，期望 %v", res.LongEquityValue, c.wantLongValue)
			}
		})
	}
}

// TestComputeValuationErrors 非法参数与无数据边界。
func TestComputeValuationErrors(t *testing.T) {
	b, cf, inc := valuationFixture()
	rows := func() ([]model.ReportRow, []model.ReportRow, []model.ReportRow) {
		return []model.ReportRow{b}, []model.ReportRow{cf}, []model.ReportRow{inc}
	}

	cases := []struct {
		name   string
		params model.ValuationParams
	}{
		{"未知模型", model.ValuationParams{Model: "foo", DiscountRate: 10}},
		{"折现率<=0", model.ValuationParams{Model: "zero", DiscountRate: 0}},
		{"永续增长率>=折现率", model.ValuationParams{Model: "perpetual", DiscountRate: 10, GrowthRate: 12}},
		{"两阶段稳定期>=折现率", model.ValuationParams{Model: "two_stage", DiscountRate: 10, Stage1Growth: 15, Stage1Years: 5, Stage2Growth: 10}},
		{"两阶段年数<=0", model.ValuationParams{Model: "two_stage", DiscountRate: 10, Stage1Growth: 15, Stage1Years: 0, Stage2Growth: 5}},
		{"三阶段终值>=折现率", model.ValuationParams{Model: "three_stage", DiscountRate: 10, Stage1Growth: 15, Stage1Years: 5, Stage2Growth: 8, Stage2Years: 5, TerminalGrowth: 10}},
		{"三阶段年数<=0", model.ValuationParams{Model: "three_stage", DiscountRate: 10, Stage1Growth: 15, Stage1Years: 5, Stage2Growth: 8, Stage2Years: 0, TerminalGrowth: 3}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bb, ccf, iinc := rows()
			if _, err := ComputeValuation(bb, ccf, iinc, c.params, 1000); err == nil {
				t.Errorf("期望返回错误，实际 nil")
			}
		})
	}

	// 无年报数据。
	if _, err := ComputeValuation(nil, nil, nil, model.ValuationParams{Model: "zero", DiscountRate: 10}, 1000); err == nil {
		t.Errorf("无年报数据期望返回错误")
	}

	// 自由现金流 <= 0。
	negCf := row(map[string]float64{
		"NETCASH_OPERATE":         50,
		"FA_IR_DEPR":              80,
		"USERIGHT_ASSET_AMORTIZE": 10,
		"IA_AMORTIZE":             6,
		"LPE_AMORTIZE":            4,
		"DISPOSAL_LONGASSET_LOSS": 5,
		"FA_SCRAP_LOSS":           2,
	})
	if _, err := ComputeValuation([]model.ReportRow{b}, []model.ReportRow{negCf}, []model.ReportRow{inc},
		model.ValuationParams{Model: "zero", DiscountRate: 10}, 1000); err == nil {
		t.Errorf("自由现金流为负期望返回错误")
	}
}

// TestValuationLongEquityNote 长期股权投资占比过大时给出提示。
func TestValuationLongEquityNote(t *testing.T) {
	b := row(map[string]float64{
		"MONETARYFUNDS":      100,
		"LONG_EQUITY_INVEST": 800,
		"SHORT_LOAN":         100,
		"TOTAL_EQUITY":       800,
		"TOTAL_ASSETS":       1000,
	})
	cf := row(map[string]float64{
		"NETCASH_OPERATE":         500,
		"FA_IR_DEPR":              50,
		"USERIGHT_ASSET_AMORTIZE": 0,
		"IA_AMORTIZE":             0,
		"LPE_AMORTIZE":            0,
		"DISPOSAL_LONGASSET_LOSS": 0,
		"FA_SCRAP_LOSS":           0,
	})
	inc := row(map[string]float64{
		"INVEST_JOINT_INCOME":      40, // 收益率 5%
		"ASSET_IMPAIRMENT_INCOME":  0,
		"CREDIT_IMPAIRMENT_INCOME": 0,
	})
	res, err := ComputeValuation([]model.ReportRow{b}, []model.ReportRow{cf}, []model.ReportRow{inc},
		model.ValuationParams{Model: "zero", DiscountRate: 10}, 1000)
	if err != nil {
		t.Fatalf("ComputeValuation 返回错误：%v", err)
	}
	if len(res.Notes) == 0 {
		t.Errorf("长期股权投资占比 80%% 应给出提示，实际无提示")
	}
}

// TestSelectBaseFCF 基期自由现金流选取：最近一年 / 平均值 / 中位数。
func TestSelectBaseFCF(t *testing.T) {
	fcf := map[int]float64{2021: 100, 2022: 200, 2023: 300, 2024: 400}
	years := []int{2021, 2022, 2023, 2024}

	v, name, used, err := selectBaseFCF(fcf, years, "latest", 0)
	if err != nil || v != 400 || name != "最近一年" || len(used) != 1 || used[0] != 2024 {
		t.Errorf("latest: v=%v name=%v used=%v err=%v", v, name, used, err)
	}
	if v, _, used, _ = selectBaseFCF(fcf, years, "average", 3); !approx(v, 300) || len(used) != 3 {
		t.Errorf("average3: v=%v used=%v，期望 300 / 3年", v, used) // (200+300+400)/3
	}
	if v, _, _, _ = selectBaseFCF(fcf, years, "median", 3); !approx(v, 300) {
		t.Errorf("median3: v=%v，期望 300", v)
	}
	if v, _, _, _ = selectBaseFCF(fcf, years, "median", 4); !approx(v, 250) {
		t.Errorf("median4: v=%v，期望 250", v) // (200+300)/2
	}
	// 去极值平均：去掉最高最低后平均
	if v, _, _, _ = selectBaseFCF(fcf, years, "trim_mean", 3); !approx(v, 300) {
		t.Errorf("trim_mean3: v=%v，期望 300", v) // [200,300,400] 去极值 → [300]
	}
	if v, _, _, _ = selectBaseFCF(fcf, years, "trim_mean", 4); !approx(v, 250) {
		t.Errorf("trim_mean4: v=%v，期望 250", v) // [100,200,300,400] 去极值 → [200,300]
	}
	// 少于 3 年退化为普通平均值
	if v, _, _, _ = selectBaseFCF(fcf, years, "trim_mean", 2); !approx(v, 350) {
		t.Errorf("trim_mean2: v=%v，期望 350", v) // [300,400] 平均
	}
	// 年数超过可用年份 → 截断为全部可用年份
	if v, _, used, _ = selectBaseFCF(fcf, years, "average", 10); !approx(v, 250) || len(used) != 4 {
		t.Errorf("average10: v=%v used=%v，期望 250 / 4年", v, used)
	}
	// 空 mode 按最近一年处理
	if v, _, _, _ = selectBaseFCF(fcf, years, "", 0); v != 400 {
		t.Errorf("空 mode: v=%v，期望 400", v)
	}
	if _, _, _, err = selectBaseFCF(fcf, years, "foo", 0); err == nil {
		t.Errorf("未知方式应返回错误")
	}
	if _, _, _, err = selectBaseFCF(fcf, nil, "latest", 0); err == nil {
		t.Errorf("空年份应返回错误")
	}
}

// TestRdExpansion 研发投入扩张部分 = max(0, 本年度研发费用 − 上年度研发费用)。
func TestRdExpansion(t *testing.T) {
	income := map[int]model.ReportRow{
		2023: rowAt("2023-12-31", map[string]float64{"RESEARCH_EXPENSE": 100}),
		2024: rowAt("2024-12-31", map[string]float64{"RESEARCH_EXPENSE": 150}),
	}
	if v := rdExpansion(income, 2024); !approx(v, 50) {
		t.Errorf("扩张 = %v，期望 50", v)
	}
	if v := rdExpansion(income, 2023); v != 0 {
		t.Errorf("上年度缺失 = %v，期望 0", v)
	}
	income2 := map[int]model.ReportRow{
		2023: rowAt("2023-12-31", map[string]float64{"RESEARCH_EXPENSE": 100}),
		2024: rowAt("2024-12-31", map[string]float64{"RESEARCH_EXPENSE": 80}),
	}
	if v := rdExpansion(income2, 2024); v != 0 {
		t.Errorf("研发下降 = %v，期望 0", v)
	}
}

// TestComputeValuationBaseFCFMode 多年度基期自由现金流选取与研发调整完整链路。
func TestComputeValuationBaseFCFMode(t *testing.T) {
	balance := []model.ReportRow{row(map[string]float64{
		"MONETARYFUNDS": 1000, "LONG_EQUITY_INVEST": 200,
		"SHORT_LOAN": 300, "LONG_LOAN": 200, "TOTAL_EQUITY": 1500, "TOTAL_ASSETS": 3000,
	})}
	// 保全性资本支出（折旧摊销）固定为 100，FCF = NETCASH_OPERATE − 100。
	cf := []model.ReportRow{
		rowAt("2022-12-31", map[string]float64{"NETCASH_OPERATE": 200, "FA_IR_DEPR": 80, "USERIGHT_ASSET_AMORTIZE": 10, "IA_AMORTIZE": 6, "LPE_AMORTIZE": 4}),
		rowAt("2023-12-31", map[string]float64{"NETCASH_OPERATE": 300, "FA_IR_DEPR": 80, "USERIGHT_ASSET_AMORTIZE": 10, "IA_AMORTIZE": 6, "LPE_AMORTIZE": 4}),
		rowAt("2024-12-31", map[string]float64{"NETCASH_OPERATE": 400, "FA_IR_DEPR": 80, "USERIGHT_ASSET_AMORTIZE": 10, "IA_AMORTIZE": 6, "LPE_AMORTIZE": 4}),
	}
	income := []model.ReportRow{
		rowAt("2022-12-31", map[string]float64{"INVEST_JOINT_INCOME": 20}),
		rowAt("2023-12-31", map[string]float64{"INVEST_JOINT_INCOME": 20, "RESEARCH_EXPENSE": 100}),
		rowAt("2024-12-31", map[string]float64{"INVEST_JOINT_INCOME": 20, "RESEARCH_EXPENSE": 150}),
	}

	// FCF：2022=100、2023=200、2024=300。
	res, err := ComputeValuation(balance, cf, income, model.ValuationParams{Model: "zero", DiscountRate: 10, FCFMode: "latest"}, 1000)
	if err != nil {
		t.Fatalf("latest 返回错误：%v", err)
	}
	if res.BaseFCF != 300 || res.FCFModeName != "最近一年" {
		t.Errorf("latest BaseFCF=%v name=%v，期望 300 / 最近一年", res.BaseFCF, res.FCFModeName)
	}

	res, err = ComputeValuation(balance, cf, income, model.ValuationParams{Model: "zero", DiscountRate: 10, FCFMode: "average", FCFYears: 3}, 1000)
	if err != nil || !approx(res.BaseFCF, 200) || res.FCFYears != 3 {
		t.Errorf("average BaseFCF=%v years=%v err=%v，期望 200 / 3", res.BaseFCF, res.FCFYears, err)
	}

	res, err = ComputeValuation(balance, cf, income, model.ValuationParams{Model: "zero", DiscountRate: 10, FCFMode: "median", FCFYears: 3}, 1000)
	if err != nil || !approx(res.BaseFCF, 200) {
		t.Errorf("median BaseFCF=%v err=%v，期望 200", res.BaseFCF, err)
	}

	// 研发调整：2024 研发 150 − 2023 研发 100 = 50，加回后 latest FCF = 350。
	res, err = ComputeValuation(balance, cf, income, model.ValuationParams{Model: "zero", DiscountRate: 10, FCFMode: "latest", AdjustRD: true}, 1000)
	if err != nil || res.BaseFCF != 350 || res.RDAdjustment != 50 || !res.AdjustRD {
		t.Errorf("研发调整 BaseFCF=%v rd=%v adjust=%v err=%v，期望 350 / 50 / true", res.BaseFCF, res.RDAdjustment, res.AdjustRD, err)
	}
}
