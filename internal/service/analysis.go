package service

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"financial-report/internal/model"
)

// 六大分析维度（顺序即展示顺序）。Key 为稳定标识。
var analysisDimensionDefs = []struct{ Key, Name string }{
	{"investing", "投资活动现金流"},
	{"financing", "筹资活动现金流"},
	{"asset_capital", "资产资本"},
	{"equity_value_added", "股权价值增加值"},
	{"comprehensive", "股东权益回报"},
	{"operating", "经营活动现金流"},
}

// 筹资活动现金流与资产资本共用的重分类科目（对应《公司财务指标分析》文档「各类资产与资本的重新归类划分」）。

// financialAssetFields 金融资产（19 科目，用于「期初金融资产」）。
// 含财务公司类科目（拆出资金、存放中央银行款项、结算备付金），供带财务子公司的非金融股使用。
// 交易性金融资产新准则字段名为 TRADE_FINASSET_NOTFVTPL（旧 TRADE_FINASSET 已为空，两者互斥）。
// 注：文档中「金融资产递延所得税资产（减金融负债递延所得税负债）」需附注明细，暂不计入。
var financialAssetFields = []string{
	"MONETARYFUNDS", "TRADE_FINASSET", "TRADE_FINASSET_NOTFVTPL", "DERIVE_FINASSET", "LOAN_ADVANCE",
	"AVAILABLE_SALE_FINASSET", "HOLD_MATURITY_INVEST", "INVEST_REALESTATE",
	"INTEREST_RECE", "DIVIDEND_RECE", "BUY_RESALE_FINASSET", "CREDITOR_INVEST",
	"OTHER_CREDITOR_INVEST", "OTHER_EQUITY_INVEST", "HOLDSALE_ASSET",
	"OTHER_NONCURRENT_FINASSET", "LEND_FUND", "CASH_DEPOSIT_PBC", "SETTLE_EXCESS_RESERVE",
}

// shortDebtFields 短期债务（12 科目，含财务公司类科目：吸收存款、拆入资金、卖出回购、向央行借款）。
var shortDebtFields = []string{
	"SHORT_LOAN", "TRADE_FINLIAB", "TRADE_FINLIAB_NOTFVTPL", "DERIVE_FINLIAB", "HOLDSALE_LIAB",
	"INTEREST_PAYABLE", "SHORT_BOND_PAYABLE", "NONCURRENT_LIAB_1YEAR",
	"ACCEPT_DEPOSIT_INTERBANK", "BORROW_FUND", "SELL_REPO_FINASSET", "LOAN_PBC",
}

// longDebtFields 长期债务（5 科目，含文档第 50 行「其他非流动负债→长期债务」）。
var longDebtFields = []string{
	"LONG_LOAN", "BOND_PAYABLE", "LEASE_LIAB", "LONG_PAYABLE", "OTHER_NONCURRENT_LIAB",
}

// 资产资本所需的重分类科目（对应《公司财务指标分析》文档「标准资产负债表的重新归类划分」）。

// operatingAssetFields 营运资产核心科目（文档归类表中明确列出）。
// 存货含消耗性生物资产（CONSUMPTIVE_BIOLOGICAL_ASSET），故一并计入。
var operatingAssetFields = []string{
	"NOTE_RECE", "ACCOUNTS_RECE", "FINANCE_RECE", "PREPAYMENT", "INVENTORY",
	"CONSUMPTIVE_BIOLOGICAL_ASSET", "CONTRACT_ASSET", "OTHER_CURRENT_ASSET", "LONG_RECE",
}

// operatingLiabilityFields 营运负债核心科目（文档归类表中明确列出）。
// 递延收益含流动（DEFER_INCOME_1YEAR）与非流动（DEFER_INCOME）；递延所得税负债按文档计入营运负债。
var operatingLiabilityFields = []string{
	"NOTE_PAYABLE", "ACCOUNTS_PAYABLE", "ADVANCE_RECEIVABLES", "CONTRACT_LIAB",
	"FEE_COMMISSION_PAYABLE", "STAFF_SALARY_PAYABLE", "LONG_STAFFSALARY_PAYABLE",
	"TAX_PAYABLE", "DEFER_INCOME", "DEFER_INCOME_1YEAR", "OTHER_CURRENT_LIAB", "DEFER_TAX_LIAB",
}

// discretionaryField 需按是否与营运相关具体分析的科目（文档标注，免费接口无法逐公司判断）。
type discretionaryField struct {
	Key  string
	Name string
}

// discretionaryAssetFields 自由裁量资产科目：无法区分时一律归营运，占比过大则提示核实。
// 东财全量接口中其他应收款为合计字段 TOTAL_OTHER_RECE（OTHER_RECE 为空）。
var discretionaryAssetFields = []discretionaryField{
	{"TOTAL_OTHER_RECE", "其他应收款"},
	{"NONCURRENT_ASSET_1YEAR", "一年内到期的非流动资产"},
}

// discretionaryLiabilityFields 自由裁量负债科目：无法区分时一律归营运，占比过大则提示核实。
// 东财全量接口中其他应付款为合计字段 TOTAL_OTHER_PAYABLE（OTHER_PAYABLE 为空）。
var discretionaryLiabilityFields = []discretionaryField{
	{"TOTAL_OTHER_PAYABLE", "其他应付款"},
	{"PREDICT_LIAB", "预计负债"},
}

// longOperatingAssetFields 长期经营资产（13 科目，用于长期经营资产合计与期初净额）。
var longOperatingAssetFields = []string{
	"FIXED_ASSET", "CIP", "PROJECT_MATERIAL", "FIXED_ASSET_DISPOSAL",
	"PRODUCTIVE_BIOLOGY_ASSET", "OIL_GAS_ASSET", "USERIGHT_ASSET", "INTANGIBLE_ASSET",
	"DEVELOP_EXPENSE", "GOODWILL", "LONG_PREPAID_EXPENSE", "OTHER_NONCURRENT_ASSET",
	"DEFER_TAX_ASSET",
}

// discretionaryThreshold 自由裁量科目占资产比例超过该阈值（默认 10%）时提示人工核实（科目仍计入营运，不排除）。
const discretionaryThreshold = 0.10

// ComputeAnalysis 计算六维财务指标分析（跨年度，仅年报，年份升序）。
// 六个维度均已实现：投资活动现金流、筹资活动现金流、资产资本、股权价值增加值、
// 股东权益回报、经营活动现金流。
// 入参为三张全量报表（balance、cashflow、income）与现金分红事件（dividends）。
func ComputeAnalysis(balance, cashflow, income []model.ReportRow, dividends []model.DividendEvent, startYear, endYear int) model.FinancialAnalysis {
	cfByYear, cfYears := annualRows(cashflow)
	balanceByYear, _ := annualRows(balance)
	incomeByYear, _ := annualRows(income)
	divByYear := dividendByYear(dividends)
	years := yearRange(cfYears, startYear, endYear)

	dimensions := make([]model.AnalysisDimension, 0, len(analysisDimensionDefs))
	for _, d := range analysisDimensionDefs {
		dim := model.AnalysisDimension{Key: d.Key, Name: d.Name, Status: "pending", Sections: []model.AnalysisSection{}}
		switch d.Key {
		case "investing":
			dim = buildInvestingAnalysis(balanceByYear, cfByYear, years)
		case "financing":
			dim = buildFinancingAnalysis(balanceByYear, cfByYear, incomeByYear, divByYear, years)
		case "asset_capital":
			dim = buildAssetCapitalAnalysis(balanceByYear, years)
		case "equity_value_added":
			dim = buildEquityValueAddedAnalysis(balanceByYear, incomeByYear, years)
		case "comprehensive":
			dim = buildComprehensiveAnalysis(balanceByYear, incomeByYear, years)
		case "operating":
			dim = buildOperatingAnalysis(cfByYear, incomeByYear, years)
		}
		dimensions = append(dimensions, dim)
	}

	analysis := model.FinancialAnalysis{Years: years, Dimensions: dimensions}
	applyTrendSignals(&analysis) // R9：回填方向属性与趋势信号（纯数值运算，不新增 IO）
	return analysis
}

// dividendByYear 按除权除息日年份汇总母公司股东现金分红总额。
func dividendByYear(events []model.DividendEvent) map[int]float64 {
	byYear := make(map[int]float64)
	for _, e := range events {
		if len(e.ExDividendDate) < 4 {
			continue
		}
		y, err := strconv.Atoi(e.ExDividendDate[:4])
		if err != nil {
			continue
		}
		byYear[y] += e.TotalAmount
	}
	return byYear
}

// yearRange 从升序年报年份中截取 [startYear, endYear] 范围。
func yearRange(years []int, startYear, endYear int) []int {
	out := make([]int, 0, len(years))
	for _, y := range years {
		if y >= startYear && y <= endYear {
			out = append(out, y)
		}
	}
	return out
}

// buildInvestingAnalysis 计算「投资活动现金流」维度。
//
// 指标口径（对应《公司财务指标分析》文档）：
//   - 长期经营资产净投资额 = 购建长期经营资产支付的现金 − 处置长期经营资产收回的现金。
//   - 保全性资本支出：为保持长期经营资产规模不变而必须支出的费用，暂以现金流量表补充资料的
//     折旧 + 摊销（含使用权资产摊销）+ 资产减值准备 + 固定资产报废损失近似。
//   - 长期经营资产扩张性资本支出 = 长期经营资产净投资额 − 保全性资本支出。
//   - 长期经营资产扩张性资本支出比例 = 扩张性资本支出 ÷ 长期经营资产期初净额。
//   - 长期经营资产期初净额：期初经营规模与生产能力，取上期末长期经营资产合计
//     （固定资产+在建工程+工程物资+固定资产清理+生产性生物资产+油气资产+使用权资产+无形资产+
//     开发支出+商誉+长期待摊费用+其他非流动资产+递延所得税资产）。
//   - 并购活动净合并额 = 取得子公司支付的现金 − 处置子公司收回的现金。
//   - 战略投资活动总体规模扩张 = 扩张性资本支出 + 并购活动净合并额。
func buildInvestingAnalysis(balanceByYear, cfByYear map[int]model.ReportRow, years []int) model.AnalysisDimension {
	dim := model.AnalysisDimension{Key: "investing", Name: "投资活动现金流", Status: "done"}
	if len(years) == 0 {
		dim.Status = "no_data" // 已实现但无年报数据
		return dim
	}

	dim.Sections = []model.AnalysisSection{
		buildLongAssetSection(balanceByYear, cfByYear, years),
		buildMergerSection(cfByYear, years),
		buildStrategySection(cfByYear, years),
	}
	return dim
}

// buildLongAssetSection 购建和处置长期经营资产的投资决策和活动。
func buildLongAssetSection(balanceByYear, cfByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "购建和处置长期经营资产的投资决策和活动",
		Indicators: []model.AnalysisIndicator{
			ind("net_long_asset", "长期经营资产净投资额", derivedValues(cfByYear, years, constructNet), "元", "",
				"反映公司长期经营资产的净投入规模。"),
			ind("maintenance_capex", "保全性资本支出", derivedValues(cfByYear, years, maintenanceCapex), "元", "",
				"为保持长期经营资产规模不变而必须支出的费用。"),
			ind("expansion_capex", "长期经营资产扩张性资本支出", derivedValues(cfByYear, years, expansionCapex), "元", "",
				"大于零表示扩张战略，小于零表示收缩战略，接近零表示维持战略。"),
			indN("expansion_capex_ratio", "长期经营资产扩张性资本支出比例", ratioValues(balanceByYear, cfByYear, years), years, "%", "",
				"扩张性资本支出占期初长期经营资产的比重，越大表示扩张力度越强。", "期初长期经营资产缺失或为0"),
		},
	}
}

// buildMergerSection 取得和处置子公司的投资决策和活动（并购）。
func buildMergerSection(cfByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "取得和处置子公司的投资决策和活动",
		Indicators: []model.AnalysisIndicator{
			ind("net_merger", "并购活动净合并额", derivedValues(cfByYear, years, mergerNet), "元", "",
				"大于零表示通过并购扩张，小于零表示出售子公司收缩，接近零表示无并购活动。"),
		},
	}
}

// buildStrategySection 战略投资活动的规模扩张。
func buildStrategySection(cfByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "战略投资活动的规模扩张",
		Indicators: []model.AnalysisIndicator{
			ind("strategy_expansion", "战略投资活动的总体规模扩张", derivedValues(cfByYear, years, strategyExpansion), "元", "",
				"反映公司通过内部扩张与外部并购实现的总扩张规模。"),
		},
	}
}

// buildFinancingAnalysis 计算「筹资活动现金流」维度。
//
// 指标口径（对应《公司财务指标分析》文档「二 筹资活动现金流」）：
//   - 战略投资活动综合现金需求 = 长期经营资产净投资额 + 并购活动净合并额。
//   - 现金自给率 = 经营活动现金流量净额 ÷ 战略投资活动综合现金需求 × 100%。
//   - 筹资需求 = 期初金融资产 + 经营活动现金流量净额 − 战略投资活动综合现金需求。
//   - 股东筹资净额 = 吸收投资收到的现金 − 分配股利、利润支付的现金。
//   - 债务筹资净额 = 借款+发债收到的现金 − 偿还债务+偿付利息支付的现金。
//   - 偿付利息支付现金 = 分配股利、利润或偿付利息支付的现金 − 母公司股东股利 − 少数股东股利（文档方法一）。
//   - 债务资本成本 = 偿付利息支付现金 ÷ 平均有息债务余额 × 100%（现金利息含资本化利息，近似总利息支出）。
//   - 加权平均资本成本 = (有息债务/投入资本)×税后债务资本成本 + (股东权益/投入资本)×股权资本成本(8%)。
//   - 实际所得税税率 = 所得税费用 ÷ (利润总额 − 长期股权投资收益)。
func buildFinancingAnalysis(balanceByYear, cfByYear, incomeByYear map[int]model.ReportRow, divByYear map[int]float64, years []int) model.AnalysisDimension {
	dim := model.AnalysisDimension{Key: "financing", Name: "筹资活动现金流", Status: "done"}
	if len(years) == 0 {
		dim.Status = "no_data"
		return dim
	}

	dim.Sections = []model.AnalysisSection{
		{
			Title: "现金供给及投资需求",
			Indicators: []model.AnalysisIndicator{
				ind("strategic_cash_demand", "战略投资活动综合现金需求", derivedValues(cfByYear, years, strategicCashDemand), "元", "",
					"长期经营资产净投资额（含保全性＋扩张性资本支出）与并购净合并额之和，即战略投资活动的全部现金需求。"),
				indN("cash_self_sufficiency", "现金自给率", yearValues(years, func(y int) *float64 {
					return cashSelfSufficiency(cfByYear[y])
				}), years, "%", "",
					"经营活动现金流覆盖战略投资需求的能力，高于100%表示靠自身造血，低于100%表示需动用存量资金或外源筹资。", "战略投资需求≤0"),
			},
		},
		{
			Title: "外源性筹资",
			Indicators: []model.AnalysisIndicator{
				indN("financing_gap", "筹资需求", yearValues(years, func(y int) *float64 {
					return financingGap(balanceByYear, cfByYear, y)
				}), years, "元", "",
					"期初金融资产与经营现金流之和减去战略投资需求后的资金盈余，正值表示资金富余，负值表示资金缺口。", "上期数据缺失"),
				ind("equity_financing_net", "股东筹资净额", yearValues(years, func(y int) *float64 {
					return fp(equityFinancingNet(cfByYear[y], divByYear[y]))
				}), "元", "",
					"吸收投资收到的现金减去分配股利利润支付的现金，为正表示股东追加投入，为负表示分红回报或减资。"),
				ind("debt_financing_net", "债务筹资净额", yearValues(years, func(y int) *float64 {
					return fp(debtFinancingNet(cfByYear[y], divByYear[y]))
				}), "元", "",
					"借款与发债收到的现金减去偿还债务与偿付利息支付的现金，为正表示举债投入，为负表示去杠杆偿债。"),
			},
		},
		{
			Title: "资本成本",
			Indicators: []model.AnalysisIndicator{
				indN("debt_capital_cost", "债务资本成本", yearValues(years, func(y int) *float64 {
					return debtCapitalCost(balanceByYear, cfByYear, divByYear, y)
				}), years, "%", "",
					"以偿付利息（现金利息，含资本化利息）除以平均有息债务余额，衡量债务融资成本。", "无有息债务"),
				indN("wacc", "加权平均资本成本", yearValues(years, func(y int) *float64 {
					return wacc(balanceByYear, cfByYear, incomeByYear, divByYear, y)
				}), years, "%", "",
					"有息债务与股东权益按占比加权的综合资本成本，股权资本成本默认按8%估算。", "投入资本为0"),
			},
		},
	}
	return dim
}

// buildOperatingAnalysis 计算「经营活动现金流」维度。
//
// 指标口径（对应《公司财务指标分析》文档「五 经营活动现金流量分析」）：
//   - 营业收入现金含量 = 销售商品、提供劳务收到的现金 ÷ 营业收入 × 100%。
//   - 成本费用付现率 =（采购商品、接受劳务支付的现金 + 支付给职工及为职工支付的现金）÷
//     （营业成本 + 税金及附加 + 销售费用 + 管理费用 + 研发费用）× 100%。
//   - 息前税后经营利润现金含量 = 经营活动现金流量净额 ÷ 息前税后经营利润。
//   - 净利润现金含量 = 经营活动现金流量净额 ÷ 净利润。
//   - 经营活动现金流量净额：直接取现金流量表净额，按文档五档解读。
//
// 增值税销项/进项税率需税务明细、免费接口不可得，故采用未剔除增值税的简化口径（文档许可）。
func buildOperatingAnalysis(cfByYear, incomeByYear map[int]model.ReportRow, years []int) model.AnalysisDimension {
	dim := model.AnalysisDimension{Key: "operating", Name: "经营活动现金流", Status: "done"}
	if len(years) == 0 {
		dim.Status = "no_data"
		return dim
	}

	dim.Sections = []model.AnalysisSection{
		{
			Title: "营业收入现金含量",
			Indicators: []model.AnalysisIndicator{
				indN("revenue_cash_ratio", "营业收入现金含量", yearValues(years, func(y int) *float64 {
					return revenueCashRatio(cfByYear[y], incomeByYear[y])
				}), years, "%", "",
					"销售商品、提供劳务收到的现金÷营业收入，反映营业收入中实际收到现金的比例；平稳期约100%，明显偏低说明营收含金量不足。", "营业收入为0"),
			},
		},
		{
			Title: "成本费用付现率",
			Indicators: []model.AnalysisIndicator{
				indN("cost_cash_ratio", "成本费用付现率", yearValues(years, func(y int) *float64 {
					return costCashRatio(cfByYear[y], incomeByYear[y])
				}), years, "%", "",
					"（采购付现+支付职工现金）÷（营业成本+税金及附加+销售费用+管理费用+研发费用），反映成本费用中需付现的比例；平稳期应低于100%（含折旧摊销不需付现），扩张期可高于100%。", "成本费用为0"),
			},
		},
		{
			Title: "经营利润现金含量",
			Indicators: []model.AnalysisIndicator{
				indN("nopat_cash_ratio", "息前税后经营利润现金含量", yearValues(years, func(y int) *float64 {
					return cashToProfit(cfByYear[y], afterTaxOperatingProfit(incomeByYear[y]))
				}), years, "倍", "",
					"经营活动现金流量净额÷息前税后经营利润，衡量经营利润是否是真金白银；成熟稳定公司应大于1。", "息前税后经营利润≤0"),
				indN("net_profit_cash_ratio", "净利润现金含量", yearValues(years, func(y int) *float64 {
					return cashToProfit(cfByYear[y], netProfitReconstructed(incomeByYear[y]))
				}), years, "倍", "",
					"经营活动现金流量净额÷净利润，衡量净利润是否是真金白银；成熟稳定公司应大于1。", "净利润≤0"),
			},
		},
		{
			Title: "经营活动现金流量净额",
			Indicators: []model.AnalysisIndicator{
				ind("operating_cash_flow", "经营活动现金流量净额", derivedValues(cfByYear, years, func(cf model.ReportRow) float64 {
					return v0(cf, "NETCASH_OPERATE")
				}), "元", "",
					"正常经营现金造血能力：<0 经营陷入危机；恰能补偿非付现成本仅维持现有规模；补偿后仍有剩余才能支撑投资与发展。"),
			},
		},
	}
	return dim
}

// revenueCashRatio 营业收入现金含量 = 销售商品、提供劳务收到的现金 ÷ 营业收入 × 100%。
func revenueCashRatio(cf, income model.ReportRow) *float64 {
	return ratioPct(v0(cf, "SALES_SERVICES"), revenue(income))
}

// costCashRatio 成本费用付现率 =（采购付现 + 支付职工现金）÷ 成本费用合计 × 100%。
// 成本费用合计 = 营业成本 + 税金及附加 + 销售费用 + 管理费用 + 研发费用。
func costCashRatio(cf, income model.ReportRow) *float64 {
	num := v0(cf, "BUY_SERVICES") + v0(cf, "PAY_STAFF_CASH")
	den := operateCost(income) + operateTaxAdd(income) + saleExpense(income) + manageExpense(income) + researchExpense(income)
	return ratioPct(num, den)
}

// cashToProfit 经营利润现金含量 = 经营活动现金流量净额 ÷ 利润（息前税后经营利润 / 净利润），利润 ≤ 0 返回 nil。
func cashToProfit(cf model.ReportRow, profit float64) *float64 {
	if profit <= 0 {
		return nil
	}
	v := v0(cf, "NETCASH_OPERATE") / profit
	return &v
}

// buildAssetCapitalAnalysis 计算「资产资本」维度。
//
// 指标口径（对应《公司财务指标分析》文档「三 资产和资本分析」与「标准资产负债表的重新归类划分」）：
//   - 资产端重分类为金融资产、营运资产、营运负债、周转性经营投入（=营运资产−营运负债）、
//     长期经营资产（13 科目）、长期股权投资；资本端为短期/长期债务、有息债务、股东权益、资本合计。
//   - 占比 = 各类资产（资本）÷ 资产合计（资本合计）× 100%。
//   - 财务杠杆倍数、长期/短期融资净额、周转性经营长期化率反映资产资本期限匹配与流动性。
//   - 约束：资产合计 = 资本合计（有息债务 + 股东权益）。为保证成立，自由裁量科目一律归入营运，
//     并将未分类科目差额（资产总计 − 已分类资产、负债总计 − 已分类负债）作为「其他」并入营运资产/营运负债。
func buildAssetCapitalAnalysis(balanceByYear map[int]model.ReportRow, years []int) model.AnalysisDimension {
	dim := model.AnalysisDimension{Key: "asset_capital", Name: "资产资本", Status: "done"}
	if len(years) == 0 {
		dim.Status = "no_data"
		return dim
	}

	dim.Sections = []model.AnalysisSection{
		buildAssetStructureSection(balanceByYear, years),
		buildCapitalStructureSection(balanceByYear, years),
		buildAssetCapitalRatioSection(balanceByYear, years),
		buildFinancingStructureSection(balanceByYear, years),
	}
	dim.Notes = reclassificationNotes(balanceByYear, years)
	return dim
}

// buildAssetStructureSection 资产端重分类。
// 指标按「父项在前、子项缩进在后」排序；kind 标识层级：net=净额（由子项计算）、sub=子项、subtotal=合计。
func buildAssetStructureSection(balanceByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "资产结构",
		Indicators: []model.AnalysisIndicator{
			ind("financial_assets", "金融资产", absValues(balanceByYear, years, financialAssets), "元", "",
				"为提高资产收益率而开展的理财型投资形成的资产，非金融公司占比不宜过高。"),
			ind("operating_assets_total", "经营资产", absValues(balanceByYear, years, operatingAssetsTotal), "元", "subtotal",
				"周转性经营投入+长期经营资产，经营活动中用于生产产品或提供劳务的全部资产。"),
			ind("working_capital", "周转性经营投入", absValues(balanceByYear, years, workingCapital), "元", "net",
				"营运资产减营运负债，反映经营占用的净周转资金。"),
			ind("operating_assets", "营运资产", absValues(balanceByYear, years, operatingAssetsValue), "元", "sub",
				"经营活动中需投入的周转性资产。"),
			ind("operating_liabilities", "营运负债", absValues(balanceByYear, years, operatingLiabilitiesValue), "元", "sub",
				"经营活动中产生的周转性负债。"),
			ind("long_operating_assets", "长期经营资产", absValues(balanceByYear, years, longOperatingAssets), "元", "",
				"通过生产产品或提供劳务获得回报的长期经营资产（含商誉等）。"),
			ind("long_equity_invest", "长期股权投资", absValues(balanceByYear, years, longEquityInvest), "元", "",
				"权益法核算的长期股权投资。"),
			ind("total_assets", "资产合计", absValues(balanceByYear, years, reclassifiedTotalAssets), "元", "subtotal",
				"金融资产+周转性经营投入+长期经营资产+长期股权投资，与资本合计相等。"),
		},
	}
}

// buildCapitalStructureSection 资本端重分类。
// 指标按「父项在前、子项缩进在后」排序；有息债务为合计（subtotal），其子项短期/长期债务缩进。
func buildCapitalStructureSection(balanceByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "资本结构",
		Indicators: []model.AnalysisIndicator{
			ind("interest_bearing_debt", "有息债务", absValues(balanceByYear, years, interestBearingDebt), "元", "subtotal",
				"短期债务加长期债务。"),
			ind("short_debt", "短期债务", absValues(balanceByYear, years, shortDebtValue), "元", "sub",
				"一年内到期的有息债务。"),
			ind("long_debt", "长期债务", absValues(balanceByYear, years, longDebtValue), "元", "sub",
				"一年以上到期的有息债务。"),
			ind("equity", "股东权益", absValues(balanceByYear, years, equityValue), "元", "",
				"所有者权益合计（含少数股东）。"),
			ind("total_capital", "资本合计", absValues(balanceByYear, years, totalCapital), "元", "subtotal",
				"有息债务加股东权益，与资产合计相等。"),
		},
	}
}

// buildAssetCapitalRatioSection 资产资本占比。
// 资产占比分母为「资产合计」（重分类），资本占比分母为「资本合计」（有息债务+股东权益）；二者相等。
func buildAssetCapitalRatioSection(balanceByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "资产资本占比",
		Indicators: []model.AnalysisIndicator{
			ind("financial_assets_ratio", "金融资产占比", ratioOf(balanceByYear, years, financialAssets, reclassifiedTotalAssets), "%", "",
				"金融资产占资产合计比重，非金融公司过高提示资金配置低效。"),
			ind("working_capital_ratio", "周转性经营投入占比", ratioOf(balanceByYear, years, workingCapital, reclassifiedTotalAssets), "%", "net",
				"周转性经营投入占资产合计比重。"),
			ind("operating_assets_ratio", "营运资产占比", ratioOf(balanceByYear, years, operatingAssetsValue, reclassifiedTotalAssets), "%", "sub",
				"营运资产占资产合计比重。"),
			ind("operating_liabilities_ratio", "营运负债占比", ratioOf(balanceByYear, years, operatingLiabilitiesValue, reclassifiedTotalAssets), "%", "sub",
				"营运负债占资产合计比重。"),
			ind("long_operating_assets_ratio", "长期经营资产占比", ratioOf(balanceByYear, years, longOperatingAssets, reclassifiedTotalAssets), "%", "",
				"长期经营资产占资产合计比重，用于判断重资产/轻资产。"),
			ind("long_equity_invest_ratio", "长期股权投资占比", ratioOf(balanceByYear, years, longEquityInvest, reclassifiedTotalAssets), "%", "",
				"长期股权投资占资产合计比重。"),
			ind("short_assets_ratio", "短期资产占比", ratioOf(balanceByYear, years, func(b model.ReportRow) float64 {
				return financialAssets(b) + workingCapital(b)
			}, reclassifiedTotalAssets), "%", "",
				"金融资产占比加周转性经营投入占比，反映短期资产规模。"),
			ind("long_assets_ratio", "长期资产占比", ratioOf(balanceByYear, years, func(b model.ReportRow) float64 {
				return longOperatingAssets(b) + longEquityInvest(b)
			}, reclassifiedTotalAssets), "%", "",
				"长期经营资产占比加长期股权投资占比，反映长期资产规模。"),
			ind("short_capital_ratio", "短期资本占比", ratioOf(balanceByYear, years, shortDebtValue, totalCapital), "%", "",
				"短期债务占资本合计比重。"),
			ind("long_capital_ratio", "长期资本占比", ratioOf(balanceByYear, years, func(b model.ReportRow) float64 {
				return longDebtValue(b) + equityValue(b)
			}, totalCapital), "%", "",
				"长期债务加股东权益占资本合计比重。"),
			ind("debt_ratio", "有息债务占比", ratioOf(balanceByYear, years, interestBearingDebt, totalCapital), "%", "",
				"有息债务占资本合计比重，反映债务筹资比例。"),
			ind("equity_ratio", "股权占比", ratioOf(balanceByYear, years, equityValue, totalCapital), "%", "",
				"股东权益占资本合计比重，反映股权筹资比例。"),
		},
	}
}

// buildFinancingStructureSection 融资结构与流动性。
func buildFinancingStructureSection(balanceByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "融资结构与流动性",
		Indicators: []model.AnalysisIndicator{
			indN("leverage", "财务杠杆倍数", yearValues(years, func(y int) *float64 {
				return leverage(balanceByYear[y])
			}), years, "倍", "",
				"资本合计除以股东权益，越大杠杆越高、财务风险越大。", "股东权益为0"),
			ind("long_financing_net", "长期融资净额", absValues(balanceByYear, years, longFinancingNet), "元", "",
				"长期债务+股东权益−长期经营资产−长期股权投资；为负表示短融长投（激进），接近零为匹配，为正为稳健。"),
			ind("short_financing_net", "短期融资净额", absValues(balanceByYear, years, shortFinancingNet), "元", "",
				"短期债务−金融资产−周转性经营投入，衡量短期资金缺口。"),
			indN("working_capital_longterm_ratio", "周转性经营长期化率", yearValues(years, func(y int) *float64 {
				return workingCapitalLongtermRatio(balanceByYear[y])
			}), years, "%", "",
				"周转性经营投入中由长期资本支撑的比重，越高流动能力越强；周转性经营投入≤0（营运负债≥营运资产）时无意义，显示为—。", "周转性经营投入≤0"),
		},
	}
}

// reclassificationNotes 汇总金额较大的自由裁量科目与未分类差额，生成口径提示（科目仍计入营运，仅提示人工核实）。
func reclassificationNotes(balanceByYear map[int]model.ReportRow, years []int) []string {
	var notes []string
	for _, y := range years {
		b := balanceByYear[y]
		ta := rawTotalAssets(b)
		if ta <= 0 {
			continue
		}
		for _, f := range discretionaryAssetFields {
			val := v0(b, f.Key)
			if val/ta > discretionaryThreshold {
				notes = append(notes, fmt.Sprintf("%d年「%s」占总资产 %.1f%%，金额较大，已按营运资产归入，请核实其是否应为金融资产", y, f.Name, val/ta*100))
			}
		}
		for _, f := range discretionaryLiabilityFields {
			val := v0(b, f.Key)
			if val/ta > discretionaryThreshold {
				notes = append(notes, fmt.Sprintf("%d年「%s」占总资产 %.1f%%，金额较大，已按营运负债归入，请核实其是否应为有息债务", y, f.Name, val/ta*100))
			}
		}
		if ra := residualAssets(b); ra/ta > discretionaryThreshold {
			notes = append(notes, fmt.Sprintf("%d年未分类资产占总资产 %.1f%%，已并入营运资产，请核实分类完整性", y, ra/ta*100))
		} else if ra/ta < -discretionaryThreshold {
			notes = append(notes, fmt.Sprintf("%d年已分类资产超出资产总计 %.1f%%，可能存在科目口径错误，请核实", y, -ra/ta*100))
		}
		if rl := residualLiabilities(b); rl/ta > discretionaryThreshold {
			notes = append(notes, fmt.Sprintf("%d年未分类负债占总资产 %.1f%%，已并入营运负债，请核实分类完整性", y, rl/ta*100))
		} else if rl/ta < -discretionaryThreshold {
			notes = append(notes, fmt.Sprintf("%d年已分类负债超出负债总计 %.1f%%，可能存在科目口径错误，请核实", y, -rl/ta*100))
		}
	}
	return notes
}

// rawTotalAssets 资产总计（原始值，用于未分类差额与阈值判定）。
func rawTotalAssets(b model.ReportRow) float64 { return v0(b, "TOTAL_ASSETS") }

// rawTotalLiabilities 负债总计（原始值，用于未分类差额）。
func rawTotalLiabilities(b model.ReportRow) float64 { return v0(b, "TOTAL_LIABILITIES") }

// equityValue 股东权益（所有者权益合计，含少数股东）。
func equityValue(b model.ReportRow) float64 { return v0(b, "TOTAL_EQUITY") }

// longEquityInvest 长期股权投资（权益法核算）。
func longEquityInvest(b model.ReportRow) float64 { return v0(b, "LONG_EQUITY_INVEST") }

// shortDebtValue 短期债务（7 科目）。
func shortDebtValue(b model.ReportRow) float64 { return sum0Fields(b, shortDebtFields) }

// longDebtValue 长期债务（4 科目）。
func longDebtValue(b model.ReportRow) float64 { return sum0Fields(b, longDebtFields) }

// longOperatingAssets 长期经营资产（13 科目）。
func longOperatingAssets(b model.ReportRow) float64 { return sum0Fields(b, longOperatingAssetFields) }

// operatingAssetsValue 营运资产 = 核心科目 + 自由裁量科目 + 未分类资产差额。
func operatingAssetsValue(b model.ReportRow) float64 {
	return sum0Fields(b, operatingAssetFields) + discretionaryAssets(b) + residualAssets(b)
}

// operatingLiabilitiesValue 营运负债 = 核心科目 + 自由裁量科目 + 未分类负债差额。
func operatingLiabilitiesValue(b model.ReportRow) float64 {
	return sum0Fields(b, operatingLiabilityFields) + discretionaryLiabilities(b) + residualLiabilities(b)
}

// discretionaryAssets 自由裁量资产科目之和（其他应收款 + 一年内到期的非流动资产），一律归入营运。
func discretionaryAssets(b model.ReportRow) float64 {
	return sumDiscretionary(b, discretionaryAssetFields)
}

// discretionaryLiabilities 自由裁量负债科目之和（其他应付款 + 预计负债），一律归入营运。
func discretionaryLiabilities(b model.ReportRow) float64 {
	return sumDiscretionary(b, discretionaryLiabilityFields)
}

// sumDiscretionary 对自由裁量科目求和（nil 视为 0）。
func sumDiscretionary(b model.ReportRow, fields []discretionaryField) float64 {
	var v float64
	for _, f := range fields {
		v += v0(b, f.Key)
	}
	return v
}

// residualAssets 未分类资产差额 = 资产总计 − 已分类资产（金融 + 营运核心 + 自由裁量 + 长期经营 + 长期股权投资）。
func residualAssets(b model.ReportRow) float64 {
	classified := financialAssets(b) + sum0Fields(b, operatingAssetFields) + discretionaryAssets(b) +
		longOperatingAssets(b) + longEquityInvest(b)
	return rawTotalAssets(b) - classified
}

// residualLiabilities 未分类负债差额 = 负债总计 − 已分类负债（营运核心 + 自由裁量 + 有息债务）。
func residualLiabilities(b model.ReportRow) float64 {
	classified := sum0Fields(b, operatingLiabilityFields) + discretionaryLiabilities(b) + interestBearingDebt(b)
	return rawTotalLiabilities(b) - classified
}

// reclassifiedTotalAssets 资产合计（重分类）= 金融资产 + 周转性经营投入 + 长期经营资产 + 长期股权投资。
// 因未分类差额已并入营运资产/营运负债，该值恒等于资本合计（有息债务 + 股东权益）。
func reclassifiedTotalAssets(b model.ReportRow) float64 {
	return financialAssets(b) + workingCapital(b) + longOperatingAssets(b) + longEquityInvest(b)
}

// workingCapital 周转性经营投入 = 营运资产 − 营运负债。
func workingCapital(b model.ReportRow) float64 {
	return operatingAssetsValue(b) - operatingLiabilitiesValue(b)
}

// totalCapital 资本合计 = 有息债务 + 股东权益。
func totalCapital(b model.ReportRow) float64 {
	return interestBearingDebt(b) + equityValue(b)
}

// leverage 财务杠杆倍数 = 资本合计 / 股东权益；股东权益为 0 返回 nil。
func leverage(b model.ReportRow) *float64 {
	e := equityValue(b)
	if e == 0 {
		return nil
	}
	v := totalCapital(b) / e
	return &v
}

// longFinancingNet 长期融资净额 = 长期债务 + 股东权益 − 长期经营资产 − 长期股权投资。
func longFinancingNet(b model.ReportRow) float64 {
	return longDebtValue(b) + equityValue(b) - longOperatingAssets(b) - longEquityInvest(b)
}

// shortFinancingNet 短期融资净额 = 短期债务 − 金融资产 − 周转性经营投入。
func shortFinancingNet(b model.ReportRow) float64 {
	return shortDebtValue(b) - financialAssets(b) - workingCapital(b)
}

// workingCapitalLongtermRatio 周转性经营长期化率 = 长期融资净额 ÷ 周转性经营投入 × 100。
// 周转性经营投入 ≤ 0（营运负债 ≥ 营运资产，无营运资金缺口）时该比率失去意义，返回 nil。
func workingCapitalLongtermRatio(b model.ReportRow) *float64 {
	wc := workingCapital(b)
	if wc <= 0 {
		return nil
	}
	v := longFinancingNet(b) / wc * 100
	return &v
}

// equityCostRate 股东预期回报率（股权资本成本率），文档默认 8%。
const equityCostRate = 0.08

// buildEquityValueAddedAnalysis 计算「股权价值增加值」维度。
//
// 指标口径（对应《公司财务指标分析》文档「四 股权价值增加分析」）：
// 把利润表按资产/资本来源重构为股权价值增加表：经营利润、金融资产收益、长期股权投资收益、
// 真实财务费用、净利润，再以「净利润 − 股权资本成本（股东权益×8%）」得到股权价值增加值。
// 简化（文档特别说明许可）：资产减值损失/信用减值损失全部归经营（不细分金融/长投）；
// 「其他综合收益」「净敞口套期收益」非净利润项目且需附注明细，暂不计入金融资产收益；
// 利息收入取财务费用明细的利息收入（FE_INTEREST_INCOME），并用于还原真实财务费用。
func buildEquityValueAddedAnalysis(balanceByYear, incomeByYear map[int]model.ReportRow, years []int) model.AnalysisDimension {
	dim := model.AnalysisDimension{Key: "equity_value_added", Name: "股权价值增加值", Status: "done"}
	if len(years) == 0 {
		dim.Status = "no_data"
		return dim
	}

	dim.Sections = []model.AnalysisSection{
		buildOperatingProfitSection(incomeByYear, years),
		buildFinancialAssetIncomeSection(incomeByYear, years),
		buildEquityInvestmentSection(incomeByYear, years),
		buildTotalProfitSection(incomeByYear, years),
		buildFinanceExpenseSection(balanceByYear, incomeByYear, years),
		buildProfitAndTaxSection(incomeByYear, years),
		buildEquityValueAddedSection(balanceByYear, incomeByYear, years),
	}
	return dim
}

// buildOperatingProfitSection 经营利润（营业收入 → 息税前经营利润 → 税后经营利润）。
// 息税前经营利润为净额（net），构成科目按文档股权价值增加表顺序列于其后，费用类科目后附对应比率；
// 末段附经营利润所得税、息前税后经营利润及息前税后营业收入利润率。
func buildOperatingProfitSection(incomeByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "经营利润",
		Indicators: []model.AnalysisIndicator{
			ind("revenue", "营业收入", absValues(incomeByYear, years, revenue), "元", "",
				"营业收入逐年快速增长为成长型，逐年下降为衰退型，稳定为成熟型。"),
			ind("operate_cost", "营业成本", absValues(incomeByYear, years, operateCost), "元", "",
				"成本费用很大一部分由资产结构决定；成本增幅低于营业收入才能实现增收增利。"),
			ind("operate_cost_ratio", "营业成本率", revenueRatioValues(incomeByYear, years, operateCost), "%", "",
				"营业成本占营业收入比重，越低成本控制越强。"),
			ind("gross_profit", "毛利", absValues(incomeByYear, years, grossProfit), "元", "",
				"营业收入减营业成本。"),
			ind("gross_margin", "毛利率", revenueRatioValues(incomeByYear, years, grossProfit), "%", "",
				"毛利率反映护城河：越高越稳定护城河越深；长期稳定高于25%通常具备核心竞争力。"),
			ind("operate_tax_add", "税金及附加", absValues(incomeByYear, years, operateTaxAdd), "元", "",
				"反映营运环节税费负担，随行业特征而异。"),
			ind("operate_tax_add_ratio", "税金及附加率", revenueRatioValues(incomeByYear, years, operateTaxAdd), "%", "",
				"营运环节税费占营业收入比重，一般应与行业法定税负相当。"),
			ind("sale_expense", "销售费用", absValues(incomeByYear, years, saleExpense), "元", "",
				"优秀公司销售费用率稳定或略降；显著上升需分析销售效率或竞争加剧。"),
			ind("sale_expense_ratio", "销售费用率", revenueRatioValues(incomeByYear, years, saleExpense), "%", "",
				"销售费用占营业收入比重，稳定或略降为佳。"),
			ind("manage_expense", "管理费用", absValues(incomeByYear, years, manageExpense), "元", "",
				"好公司管理费用率稳定；上升需分析原因（优秀管理人员增加可能是积极信号）。"),
			ind("manage_expense_ratio", "管理费用率", revenueRatioValues(incomeByYear, years, manageExpense), "%", "",
				"管理费用占营业收入比重，应保持稳定。"),
			ind("research_expense", "研发费用", absValues(incomeByYear, years, researchExpense), "元", "",
				"研发投入反映创新与长期竞争力。"),
			ind("research_expense_ratio", "研发费用率", revenueRatioValues(incomeByYear, years, researchExpense), "%", "",
				"研发投入强度，反映创新与长期竞争力。"),
			ind("asset_impairment_loss", "资产减值损失", absValues(incomeByYear, years, assetImpairmentLoss), "元", "",
				"经营资产减值损失作为营业费用（简化处理，未细分金融/长投减值）。"),
			ind("credit_impairment_loss", "信用减值损失", absValues(incomeByYear, years, creditImpairmentLoss), "元", "",
				"坏账等信用减值，通常源于客户资信管理。"),
			ind("impairment_loss_ratio", "资产减值损失率", revenueRatioValues(incomeByYear, years, impairmentLossTotal), "%", "",
				"（资产减值损失+信用减值损失）占营业收入比重，反映减值侵蚀利润的程度。"),
			ind("asset_disposal_income", "资产处置收益", absValues(incomeByYear, years, assetDisposalIncome), "元", "",
				"资产处置为企业经营决策的结果。"),
			ind("other_income", "其他收益", absValues(incomeByYear, years, otherIncome), "元", "",
				"主要为政府补助，具有经营相关性，作为经营利润处理。"),
			ind("nonbusiness_income", "营业外收入", absValues(incomeByYear, years, nonbusinessIncome), "元", "",
				"与经营相关，多数公司占比很低。"),
			ind("nonbusiness_expense", "营业外支出", absValues(incomeByYear, years, nonbusinessExpense), "元", "",
				"经营管理良好的公司不必要支出较少。"),
			ind("nonbusiness_other_ratio", "营业外收支及其他占营业收入比例", revenueRatioValues(incomeByYear, years, nonbusinessOtherNet), "%", "",
				"营业外收支净额及其他占营业收入比重，多数公司应很低；过高需分析是否合理且持续。"),
			ind("total_expense_ratio", "总费用率", revenueRatioValues(incomeByYear, years, totalExpense), "%", "",
				"（税金及附加+销售+管理+研发+减值损失−营业外收支及其他）÷营业收入，反映综合费用负担。"),
			ind("ebit_operating", "息税前经营利润", absValues(incomeByYear, years, ebitOperatingProfit), "元", "net",
				"扣除债务利息与所得税前的经营利润，反映经营资产创造利润的能力。"),
			ind("operating_profit_tax", "经营利润所得税", absValues(incomeByYear, years, operatingProfitTax), "元", "",
				"息税前经营利润×实际所得税税率，经营利润承担的所得税。"),
			ind("after_tax_operating_profit", "息前税后经营利润", absValues(incomeByYear, years, afterTaxOperatingProfit), "元", "",
				"息税前经营利润×(1−实际所得税税率)，经营资产税后收益。"),
			ind("after_tax_operating_margin", "息前税后营业收入利润率", revenueRatioValues(incomeByYear, years, afterTaxOperatingProfit), "%", "",
				"息前税后经营利润÷营业收入，反映经营获利能力，稳定或略升为佳。"),
		},
	}
}

// buildFinancialAssetIncomeSection 金融资产收益（理财型投资，含所得税拆分与息前税后金融资产收益）。
func buildFinancialAssetIncomeSection(incomeByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "金融资产收益",
		Indicators: []model.AnalysisIndicator{
			ind("ebit_financial_asset_income", "息税前金融资产收益", absValues(incomeByYear, years, ebitFinancialAssetIncome), "元", "net",
				"金融资产（理财型投资）创造的税前收益。"),
			ind("short_term_invest_income", "短期投资收益", absValues(incomeByYear, years, shortTermInvestIncome), "元", "sub",
				"投资收益扣除长期股权投资收益后的短期理财收益。"),
			ind("interest_income", "利息收入", absValues(incomeByYear, years, interestIncome), "元", "sub",
				"金融资产的利息收益，从财务费用中还原。"),
			ind("net_exposure_income", "净敞口套期收益", absValues(incomeByYear, years, netExposureIncome), "元", "sub",
				"净敞口套期的损益。"),
			ind("fair_value_change_income", "公允价值变动净收益", absValues(incomeByYear, years, fairValueChangeIncome), "元", "sub",
				"金融资产公允价值变动损益。"),
			ind("exchange_income", "汇兑净收益", absValues(incomeByYear, years, exchangeIncome), "元", "sub",
				"外币金融资产的汇兑损益。"),
			ind("other_comprehensive_income", "其他综合收益", absValues(incomeByYear, years, preTaxOtherComprehensiveIncome), "元", "sub",
				"税后其他综合收益按统一实际所得税税率还原为税前（近似，各分项实际税负可能不同）。"),
			ind("financial_asset_income_tax", "金融资产收益所得税", absValues(incomeByYear, years, financialAssetIncomeTax), "元", "",
				"息税前金融资产收益×实际所得税税率，金融资产收益承担的所得税。"),
			ind("after_tax_financial_asset_income", "息前税后金融资产收益", absValues(incomeByYear, years, afterTaxFinancialAssetIncome), "元", "",
				"息税前金融资产收益×(1−实际所得税税率)，金融资产税后收益。"),
		},
	}
}

// buildEquityInvestmentSection 长期股权投资收益。
func buildEquityInvestmentSection(incomeByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "长期股权投资收益",
		Indicators: []model.AnalysisIndicator{
			ind("long_equity_invest_income", "长期股权投资收益", absValues(incomeByYear, years, longEquityInvestmentIncome), "元", "",
				"对联营企业和合营企业的投资收益（权益法，被投资企业已缴所得税，不再计入所得税）。"),
		},
	}
}

// buildTotalProfitSection 息税前与息前税后利润总额（经营+金融资产+长投收益的合计，税前/税后两种口径）。
func buildTotalProfitSection(incomeByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "息税前与息前税后利润总额",
		Indicators: []model.AnalysisIndicator{
			ind("ebit_total", "息税前利润总额", absValues(incomeByYear, years, ebitTotal), "元", "net",
				"息税前经营利润+息税前金融资产收益+长期股权投资收益。"),
			ind("after_tax_total_profit", "息前税后利润总额", absValues(incomeByYear, years, afterTaxTotalProfit), "元", "net",
				"息前税后经营利润+息前税后金融资产收益+长期股权投资收益，与资本成本比较判断是否创造价值。"),
		},
	}
}

// buildFinanceExpenseSection 财务费用与财务成本（真实财务费用的税前/抵税/税后，及财务成本负担率、债务资本成本率）。
func buildFinanceExpenseSection(balanceByYear, incomeByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "财务费用与财务成本",
		Indicators: []model.AnalysisIndicator{
			ind("real_finance_expense", "真实财务费用", absValues(incomeByYear, years, realFinanceExpense), "元", "",
				"财务费用+利息收入，反映真实债务成本（利息收入已还原为金融资产收益）。"),
			ind("finance_expense_tax_shield", "财务费用抵税效应", absValues(incomeByYear, years, financeExpenseTaxShield), "元", "",
				"真实财务费用×实际所得税税率，债务利息的抵税金额，降低税后债务成本。"),
			ind("after_tax_finance_expense", "税后真实财务费用", absValues(incomeByYear, years, afterTaxRealFinanceExpense), "元", "",
				"真实财务费用−财务费用抵税效应，反映税后实际承担的债务成本。"),
			indN("finance_cost_burden", "财务成本负担率", yearValues(years, func(y int) *float64 {
				return ratioPct(realFinanceExpense(incomeByYear[y]), ebitTotal(incomeByYear[y]))
			}), years, "%", "",
				"真实财务费用÷息税前利润总额，反映利润中用于支付利息的比例，越低负担越轻。", "息税前利润总额为0"),
			indN("debt_capital_cost_rate", "债务资本成本率", yearValues(years, func(y int) *float64 {
				return debtCapitalCostRate(balanceByYear, incomeByYear, y)
			}), years, "%", "",
				"真实财务费用÷平均有息债务余额，反映债务资本的平均成本；无有息债务或净利息收入时显示「—」。", "无有息债务或净利息收入"),
		},
	}
}

// buildProfitAndTaxSection 税前利润与所得税（税前利润 → 所得税 → 净利润）。
func buildProfitAndTaxSection(incomeByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	etr := ind("effective_tax_rate", "实际所得税税率", yearValues(years, func(y int) *float64 {
		return effectiveTaxRateDisplay(incomeByYear[y])
	}), "%", "",
		"所得税费用÷(税前利润−长期股权投资收益)×100%，反映实际税负。")
	etr.Note = effectiveTaxRateNote(incomeByYear, years)
	return model.AnalysisSection{
		Title: "税前利润与所得税",
		Indicators: []model.AnalysisIndicator{
			ind("pre_tax_profit", "税前利润", absValues(incomeByYear, years, preTaxProfit), "元", "net",
				"息税前利润总额−真实财务费用，≈ 利润表利润总额。"),
			ind("income_tax", "所得税费用", absValues(incomeByYear, years, incomeTax), "元", "",
				"当期企业所得税费用。"),
			etr,
			ind("net_profit", "净利润", absValues(incomeByYear, years, netProfitReconstructed), "元", "net",
				"税前利润−所得税；亦=息前税后利润总额−税后真实财务费用。"),
		},
	}
}

// buildEquityValueAddedSection 股权价值增加值。
func buildEquityValueAddedSection(balanceByYear, incomeByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "股权价值增加值",
		Indicators: []model.AnalysisIndicator{
			ind("equity_capital_cost", "股权资本成本", yearValues(years, func(y int) *float64 {
				return fp(equityCapitalCost(balanceByYear[y]))
			}), "元", "",
				"股东权益×股东预期回报率（默认8%），反映股东要求的回报。"),
			ind("equity_value_added", "股权价值增加值", yearValues(years, func(y int) *float64 {
				return fp(equityValueAdded(balanceByYear[y], incomeByYear[y]))
			}), "元", "net",
				"净利润−股权资本成本；为正创造超预期回报，为负损毁股东价值。"),
		},
	}
}

// —— 股权价值增加表科目取值（利润表全量接口，nil 视为 0）——

// revenue 营业收入（营业总收入）。
func revenue(income model.ReportRow) float64 { return v0(income, "TOTAL_OPERATE_INCOME") }

// operateCost 营业成本。
func operateCost(income model.ReportRow) float64 { return v0(income, "OPERATE_COST") }

// operateTaxAdd 税金及附加。
func operateTaxAdd(income model.ReportRow) float64 { return v0(income, "OPERATE_TAX_ADD") }

// saleExpense 销售费用。
func saleExpense(income model.ReportRow) float64 { return v0(income, "SALE_EXPENSE") }

// manageExpense 管理费用。
func manageExpense(income model.ReportRow) float64 { return v0(income, "MANAGE_EXPENSE") }

// researchExpense 研发费用。
func researchExpense(income model.ReportRow) float64 { return v0(income, "RESEARCH_EXPENSE") }

// assetImpairmentLoss 资产减值损失（正数=损失）。
// 非金融股用 ASSET_IMPAIRMENT_INCOME（收益口径，负值=损失，需取反）；金融股（银行等）用 ASSET_IMPAIRMENT_LOSS（损失口径，正数=损失），两者互斥。
func assetImpairmentLoss(income model.ReportRow) float64 {
	if v := income.Fields["ASSET_IMPAIRMENT_LOSS"]; v != nil {
		return *v
	}
	return -v0(income, "ASSET_IMPAIRMENT_INCOME")
}

// creditImpairmentLoss 信用减值损失（正数=损失），口径同 assetImpairmentLoss。
func creditImpairmentLoss(income model.ReportRow) float64 {
	if v := income.Fields["CREDIT_IMPAIRMENT_LOSS"]; v != nil {
		return *v
	}
	return -v0(income, "CREDIT_IMPAIRMENT_INCOME")
}

// assetDisposalIncome 资产处置收益。
func assetDisposalIncome(income model.ReportRow) float64 { return v0(income, "ASSET_DISPOSAL_INCOME") }

// otherIncome 其他收益（政府补助等）。
func otherIncome(income model.ReportRow) float64 { return v0(income, "OTHER_INCOME") }

// nonbusinessIncome 营业外收入。
func nonbusinessIncome(income model.ReportRow) float64 { return v0(income, "NONBUSINESS_INCOME") }

// nonbusinessExpense 营业外支出。
func nonbusinessExpense(income model.ReportRow) float64 { return v0(income, "NONBUSINESS_EXPENSE") }

// grossProfit 毛利 = 营业收入 − 营业成本。
func grossProfit(income model.ReportRow) float64 { return revenue(income) - operateCost(income) }

// impairmentLossTotal 资产减值损失 + 信用减值损失（资产减值损失率的分子，文档第 16 行）。
func impairmentLossTotal(income model.ReportRow) float64 {
	return assetImpairmentLoss(income) + creditImpairmentLoss(income)
}

// nonbusinessOtherNet 营业外收支及其他 = 资产处置收益 + 其他收益 + 营业外收入 − 营业外支出（文档第 21 行）。
func nonbusinessOtherNet(income model.ReportRow) float64 {
	return assetDisposalIncome(income) + otherIncome(income) + nonbusinessIncome(income) - nonbusinessExpense(income)
}

// totalExpense 总费用 = 税金及附加 + 销售费用 + 管理费用 + 研发费用 + 资产减值损失 + 信用减值损失 − 营业外收支及其他（文档营业费用分析「总费用率」）。
func totalExpense(income model.ReportRow) float64 {
	return operateTaxAdd(income) + saleExpense(income) + manageExpense(income) + researchExpense(income) +
		impairmentLossTotal(income) - nonbusinessOtherNet(income)
}

// ebitOperatingProfit 息税前经营利润 = 营业收入 − 营业成本 − 税金及附加 − 销售费用 − 管理费用 − 研发费用
// − 资产减值损失 − 信用减值损失 + 资产处置收益 + 其他收益 + 营业外收入 − 营业外支出。
func ebitOperatingProfit(income model.ReportRow) float64 {
	return revenue(income) - operateCost(income) - operateTaxAdd(income) -
		saleExpense(income) - manageExpense(income) - researchExpense(income) -
		assetImpairmentLoss(income) - creditImpairmentLoss(income) +
		assetDisposalIncome(income) + otherIncome(income) +
		nonbusinessIncome(income) - nonbusinessExpense(income)
}

// shortTermInvestIncome 短期投资收益 = 投资收益 − 长期股权投资收益。
func shortTermInvestIncome(income model.ReportRow) float64 {
	return v0(income, "INVEST_INCOME") - v0(income, "INVEST_JOINT_INCOME")
}

// interestIncome 利息收入（财务费用明细中的利息收入，用于还原真实财务费用）。
// 注：独立「利息收入」科目 INTEREST_INCOME 为财务公司放贷利息、已含于营业总收入，故此处取 FE_INTEREST_INCOME。
func interestIncome(income model.ReportRow) float64 { return v0(income, "FE_INTEREST_INCOME") }

// fairValueChangeIncome 公允价值变动净收益。
func fairValueChangeIncome(income model.ReportRow) float64 {
	return v0(income, "FAIRVALUE_CHANGE_INCOME")
}

// exchangeIncome 汇兑净收益。
func exchangeIncome(income model.ReportRow) float64 { return v0(income, "EXCHANGE_INCOME") }

// netExposureIncome 净敞口套期收益。
func netExposureIncome(income model.ReportRow) float64 { return v0(income, "NET_EXPOSURE_INCOME") }

// otherComprehensiveIncome 其他综合收益（税后净额，财报披露口径）。
func otherComprehensiveIncome(income model.ReportRow) float64 {
	return v0(income, "OTHER_COMPRE_INCOME")
}

// preTaxOtherComprehensiveIncome 税前其他综合收益 = 其他综合收益 ÷ (1 − 实际所得税税率)。
// 其他综合收益在财报中按税后净额披露，此处用统一实际所得税税率近似还原为税前，
// 与息税前金融资产收益其余各项（均为税前）口径一致。税率 ≥ 1 时无法还原，返回 0。
func preTaxOtherComprehensiveIncome(income model.ReportRow) float64 {
	oci := otherComprehensiveIncome(income)
	t := effectiveTaxRate(income)
	if t >= 1 {
		return 0
	}
	return oci / (1 - t)
}

// ebitFinancialAssetIncome 息税前金融资产收益 = 短期投资收益 + 利息收入 + 净敞口套期收益 + 公允价值变动净收益 + 汇兑净收益 + 税前其他综合收益。
// 对应文档股权价值增加表 (26)+(27)+(28)+(29)+(30)+(31)；其他综合收益按统一税率还原为税前。
func ebitFinancialAssetIncome(income model.ReportRow) float64 {
	return shortTermInvestIncome(income) + interestIncome(income) + netExposureIncome(income) +
		fairValueChangeIncome(income) + exchangeIncome(income) + preTaxOtherComprehensiveIncome(income)
}

// longEquityInvestmentIncome 长期股权投资收益 = 对联营企业和合营企业的投资收益。
func longEquityInvestmentIncome(income model.ReportRow) float64 {
	return v0(income, "INVEST_JOINT_INCOME")
}

// ebitTotal 息税前利润总额 = 息税前经营利润 + 息税前金融资产收益 + 长期股权投资收益。
func ebitTotal(income model.ReportRow) float64 {
	return ebitOperatingProfit(income) + ebitFinancialAssetIncome(income) + longEquityInvestmentIncome(income)
}

// realFinanceExpense 真实财务费用 = 财务费用 + 利息收入（把财务费用中被抵减的利息收入还原）。
func realFinanceExpense(income model.ReportRow) float64 {
	return v0(income, "FINANCE_EXPENSE") + interestIncome(income)
}

// preTaxProfit 税前利润 = 息税前利润总额 − 真实财务费用。
func preTaxProfit(income model.ReportRow) float64 {
	return ebitTotal(income) - realFinanceExpense(income)
}

// incomeTax 所得税费用。
func incomeTax(income model.ReportRow) float64 { return v0(income, "INCOME_TAX") }

// netProfitReconstructed 净利润 = 税前利润 − 所得税费用（重构，≈ 利润表净利润）。
func netProfitReconstructed(income model.ReportRow) float64 {
	return preTaxProfit(income) - incomeTax(income)
}

// afterTaxOperatingProfit 息前税后经营利润 = 息税前经营利润 × (1 − 实际所得税税率)。
func afterTaxOperatingProfit(income model.ReportRow) float64 {
	return ebitOperatingProfit(income) * (1 - effectiveTaxRate(income))
}

// afterTaxFinancialAssetIncome 息前税后金融资产收益 = 息税前金融资产收益 × (1 − 实际所得税税率)。
func afterTaxFinancialAssetIncome(income model.ReportRow) float64 {
	return ebitFinancialAssetIncome(income) * (1 - effectiveTaxRate(income))
}

// afterTaxTotalProfit 息前税后利润总额 = 息前税后经营利润 + 息前税后金融资产收益 + 长期股权投资收益。
// 长期股权投资收益被投资企业已缴所得税，故不再乘 (1−税率)。
func afterTaxTotalProfit(income model.ReportRow) float64 {
	return afterTaxOperatingProfit(income) + afterTaxFinancialAssetIncome(income) + longEquityInvestmentIncome(income)
}

// operatingProfitTax 经营利润所得税 = 息税前经营利润 × 实际所得税税率。
func operatingProfitTax(income model.ReportRow) float64 {
	return ebitOperatingProfit(income) * effectiveTaxRate(income)
}

// financialAssetIncomeTax 金融资产收益所得税 = 息税前金融资产收益 × 实际所得税税率。
func financialAssetIncomeTax(income model.ReportRow) float64 {
	return ebitFinancialAssetIncome(income) * effectiveTaxRate(income)
}

// financeExpenseTaxShield 财务费用抵税效应 = 真实财务费用 × 实际所得税税率（债务利息的抵税金额）。
func financeExpenseTaxShield(income model.ReportRow) float64 {
	return realFinanceExpense(income) * effectiveTaxRate(income)
}

// afterTaxRealFinanceExpense 税后真实财务费用 = 真实财务费用 − 财务费用抵税效应 = 真实财务费用 × (1 − 实际所得税税率)。
func afterTaxRealFinanceExpense(income model.ReportRow) float64 {
	return realFinanceExpense(income) - financeExpenseTaxShield(income)
}

// debtCapitalCostRate 债务资本成本率 = 真实财务费用 ÷ 平均有息债务余额 × 100%。
// 平均有息债务余额 = (期初有息债务 + 期末有息债务) ÷ 2；上期缺失、无有息债务或真实财务费用 ≤ 0（净利息收入）时返回 nil。
func debtCapitalCostRate(balanceByYear, incomeByYear map[int]model.ReportRow, year int) *float64 {
	prev, ok := balanceByYear[year-1]
	if !ok {
		return nil
	}
	avg := (interestBearingDebt(balanceByYear[year]) + interestBearingDebt(prev)) / 2
	if avg <= 0 {
		return nil
	}
	rfe := realFinanceExpense(incomeByYear[year])
	if rfe <= 0 {
		return nil
	}
	v := rfe / avg * 100
	return &v
}

// equityCapitalCost 股权资本成本 = 股东权益 × 股东预期回报率（默认 8%）。
// 股东权益取期末所有者权益合计（含少数股东），与净利润（含少数股东损益）口径一致。
func equityCapitalCost(balance model.ReportRow) float64 {
	return v0(balance, "TOTAL_EQUITY") * equityCostRate
}

// equityValueAdded 股权价值增加值 = 净利润 − 股权资本成本。
func equityValueAdded(balance, income model.ReportRow) float64 {
	return netProfitReconstructed(income) - equityCapitalCost(balance)
}

// buildComprehensiveAnalysis 计算「股东权益回报」维度（对应文档「五 资产资本表和股权价值增加表分析」）。
//
// 指标口径：
//   - 股东权益回报率（ROE）= 净利润 ÷ 股东权益 × 100%，按杜邦分解为四个因素：
//     息税前资产回报率（息税前利润÷资产总额）× 财务成本效应比率（税前利润÷息税前利润）×
//     财务杠杆倍数（资产总额÷股东权益）× 企业所得税效应比率（净利润÷税前利润）。
//     资产总额取资产负债表总计（TOTAL_ASSETS，含全部负债）。
//   - 经营资产 = 周转性经营投入 + 长期经营资产（文档：经营资产由二者构成）。
//   - 周转率分母为对应资产（经营资产/长期经营资产/周转性经营投入/固定资产/金融资产/长期股权投资）；
//     固定资产、存货、应收/应付账款用「(期初+期末)÷2」平均余额，上期缺失时无法计算。
//   - 应收账款净额 = 应收账款 + 应收票据 − 预收账款；应付账款净额 = 应付账款 + 应付票据 − 预付账款（文档口径）。
//   - 周转天数 = 365 ÷ 周转率；营业周期 = 存货周转天数 + 应收账款周转天数；
//     现金周期 = 营业周期 − 应付账款平均付账期。
//   - 采购成本/销货成本用营业成本近似；赊销收入净额用营业收入近似（文档特别说明许可）。
//   - 利息保障倍数 = 息税前利润 ÷ 真实财务费用（利息费用取真实财务费用，用户确认，与财务成本负担率口径一致）。
//   - 固定资产成新率需固定资产原值（财报附注），免费接口不可得，指标保留但恒为空。
func buildComprehensiveAnalysis(balanceByYear, incomeByYear map[int]model.ReportRow, years []int) model.AnalysisDimension {
	dim := model.AnalysisDimension{Key: "comprehensive", Name: "股东权益回报", Status: "done"}
	if len(years) == 0 {
		dim.Status = "no_data"
		return dim
	}

	dim.Sections = []model.AnalysisSection{
		buildROESection(balanceByYear, incomeByYear, years),
		buildOperatingEfficiencySection(balanceByYear, incomeByYear, years),
		buildLeverageEffectSection(balanceByYear, incomeByYear, years),
		buildCreditorProtectionSection(balanceByYear, incomeByYear, years),
		buildTaxEffectSection(incomeByYear, years),
	}
	return dim
}

// buildROESection 股东权益回报率（杜邦分解）。
func buildROESection(balanceByYear, incomeByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "股东权益回报率",
		Indicators: []model.AnalysisIndicator{
			indN("roe", "股东权益回报率", yearValues(years, func(y int) *float64 {
				return roe(balanceByYear[y], incomeByYear[y])
			}), years, "%", "net",
				"净利润÷股东权益；=息税前资产回报率×财务成本效应比率×财务杠杆倍数×企业所得税效应比率（杜邦四因素）。高于股东预期回报（8%）才创造超预期回报。", "股东权益≤0"),
		},
	}
}

// buildOperatingEfficiencySection 投资和经营活动的管理效率及盈利能力。
func buildOperatingEfficiencySection(balanceByYear, incomeByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "投资和经营活动的管理效率及盈利能力",
		Indicators: []model.AnalysisIndicator{
			// —— 盈利能力：回报率/收益率 ——
			indN("ebit_asset_return", "息税前资产回报率", yearValues(years, func(y int) *float64 {
				return ebitAssetReturn(balanceByYear[y], incomeByYear[y])
			}), years, "%", "",
				"息税前利润÷资产总额，反映公司全部资产的整体获利能力（杜邦四因素分解的首项）。", "资产总额为0"),
			indN("ebit_operating_asset_return", "息税前经营资产回报率", yearValues(years, func(y int) *float64 {
				return ebitOperatingAssetReturn(balanceByYear[y], incomeByYear[y])
			}), years, "%", "net",
				"息税前经营利润÷经营资产总额；=息税前经营利润率×经营资产周转率，反映经营资产的盈利能力。", "经营资产≤0"),
			indN("ebit_operating_margin", "息税前经营利润率", yearValues(years, func(y int) *float64 {
				return ebitOperatingMargin(incomeByYear[y])
			}), years, "%", "sub",
				"息税前经营利润÷营业收入，反映经营业务本身的盈利能力（纯经营口径，区别于含金融/长投收益的息税前利润率）。", "营业收入为0"),
			indN("long_equity_return", "长期股权投资收益率", yearValues(years, func(y int) *float64 {
				return longEquityReturn(balanceByYear[y], incomeByYear[y])
			}), years, "%", "",
				"长期股权投资收益÷长期股权投资；高于加权平均资本成本才实现保值增值。", "长期股权投资≤0"),
			indN("financial_asset_return", "金融资产收益率", yearValues(years, func(y int) *float64 {
				return financialAssetReturn(balanceByYear[y], incomeByYear[y])
			}), years, "%", "",
				"息税前金融资产收益÷金融资产，反映理财型投资的回报水平。", "金融资产≤0"),
			// —— 管理效率：资产/经营周转率 ——
			indN("asset_turnover", "资产周转率", yearValues(years, func(y int) *float64 {
				return assetTurnover(balanceByYear[y], incomeByYear[y])
			}), years, "次", "",
				"营业收入÷资产总额，反映资产整体周转效率。", "资产总额≤0"),
			indN("operating_asset_turnover", "经营资产周转率", yearValues(years, func(y int) *float64 {
				return operatingAssetTurnover(balanceByYear[y], incomeByYear[y])
			}), years, "次", "",
				"营业收入÷经营资产（周转性经营投入+长期经营资产），反映经营资产管理效率。", "经营资产≤0"),
			indN("long_operating_asset_turnover", "长期经营资产周转率", yearValues(years, func(y int) *float64 {
				return longOperatingAssetTurnover(balanceByYear[y], incomeByYear[y])
			}), years, "次", "sub",
				"营业收入÷长期经营资产，经营资产周转率的子项。", "长期经营资产≤0"),
			indN("fixed_asset_turnover", "固定资产周转率", yearValues(years, func(y int) *float64 {
				return fixedAssetTurnover(balanceByYear, incomeByYear, y)
			}), years, "次", "sub",
				"营业收入÷平均固定资产净值，长期经营资产周转率的子项（需结合成新率辅助判断）。", "平均固定资产净值≤0或上期缺失"),
			indN("fixed_asset_new_rate", "固定资产成新率", make([]*float64, len(years)), years, "%", "sub",
				"平均固定资产净值÷平均固定资产原值；固定资产原值需财报附注数据、免费接口不可得，暂无法计算。", "固定资产原值不可得"),
			indN("working_capital_turnover", "周转性经营投入周转率", yearValues(years, func(y int) *float64 {
				return workingCapitalTurnover(balanceByYear[y], incomeByYear[y])
			}), years, "次", "sub",
				"营业收入÷周转性经营投入，经营资产周转率的子项。", "周转性经营投入≤0"),
			indN("receivable_turnover", "应收账款周转率", yearValues(years, func(y int) *float64 {
				return receivableTurnover(balanceByYear, incomeByYear, y)
			}), years, "次", "",
				"销售收入÷平均应收账款（用销售收入替代赊销净额），越高收账越快。", "平均应收账款≤0或上期缺失"),
			indN("inventory_turnover", "存货周转率", yearValues(years, func(y int) *float64 {
				return inventoryTurnover(balanceByYear, incomeByYear, y)
			}), years, "次", "",
				"销货成本÷平均存货余额（销货成本用营业成本替代），越高存货变现越快。", "平均存货≤0或上期缺失"),
			indN("payable_turnover", "应付账款周转率", yearValues(years, func(y int) *float64 {
				return payableTurnover(balanceByYear, incomeByYear, y)
			}), years, "次", "",
				"采购成本÷平均应付账款（采购成本用营业成本替代），越低越能占用供应商货款。", "平均应付账款≤0或上期缺失"),
			// —— 管理效率：周转周期/天数 ——
			indN("operating_cycle", "营业周期", yearValues(years, func(y int) *float64 {
				return operatingCycle(balanceByYear, incomeByYear, y)
			}), years, "天", "net",
				"存货周转天数+应收账款周转天数，越短周转效率越高。", "存货或应收账款周转天数缺失"),
			indN("receivable_days", "应收账款周转天数", yearValues(years, func(y int) *float64 {
				return receivableDays(balanceByYear, incomeByYear, y)
			}), years, "天", "sub",
				"计算期天数÷应收账款周转率，收账越快越好。", "应收账款周转率不可得"),
			indN("inventory_days", "存货周转天数", yearValues(years, func(y int) *float64 {
				return inventoryDays(balanceByYear, incomeByYear, y)
			}), years, "天", "sub",
				"计算期天数÷存货周转率，存货占用越短越好。", "存货周转率不可得"),
			indN("payable_days", "应付账款平均付账期", yearValues(years, func(y int) *float64 {
				return payableDays(balanceByYear, incomeByYear, y)
			}), years, "天", "",
				"计算期天数÷应付账款周转率，越长占用供应商资金越多、还款压力也越大。", "应付账款周转率不可得"),
			indN("cash_cycle", "现金周期", yearValues(years, func(y int) *float64 {
				return cashCycle(balanceByYear, incomeByYear, y)
			}), years, "天", "net",
				"营业周期−应付账款平均付账期，越短现金回笼越快。", "营业周期或应付付账期缺失"),
		},
	}
}

// buildLeverageEffectSection 财务杠杆效应。
func buildLeverageEffectSection(balanceByYear, incomeByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "财务杠杆效应",
		Indicators: []model.AnalysisIndicator{
			indN("finance_cost_effect_ratio", "财务成本效应比率", yearValues(years, func(y int) *float64 {
				return financeCostEffectRatio(incomeByYear[y])
			}), years, "%", "",
				"税前利润÷息税前利润=1−财务成本负担率，越高财务成本负担越轻。", "息税前利润为0"),
			indN("dupont_leverage", "财务杠杆倍数", yearValues(years, func(y int) *float64 {
				return dupontLeverage(balanceByYear[y])
			}), years, "倍", "",
				"资产总额÷股东权益（杜邦口径，含全部负债；区别于资产资本维度「资本合计÷股东权益」的有息债务口径）。", "股东权益≤0"),
			indN("financial_leverage_effect", "财务杠杆效应", yearValues(years, func(y int) *float64 {
				return financialLeverageEffect(balanceByYear[y], incomeByYear[y])
			}), years, "倍", "net",
				"财务成本效应比率×财务杠杆倍数，债务筹资对股东权益回报率的综合影响。", "息税前利润为0或股东权益≤0"),
		},
	}
}

// buildCreditorProtectionSection 债权人保障程度。
func buildCreditorProtectionSection(balanceByYear, incomeByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "债权人保障程度",
		Indicators: []model.AnalysisIndicator{
			indN("debt_equity_ratio", "债务对股东权益比率", yearValues(years, func(y int) *float64 {
				return debtEquityRatio(balanceByYear[y])
			}), years, "倍", "",
				"（短期借款+长期借款）÷股东权益，股东每投入1元对应多少债务融资，越低债权人越安全。", "股东权益≤0"),
			indN("interest_coverage", "利息保障倍数", yearValues(years, func(y int) *float64 {
				return interestCoverage(incomeByYear[y])
			}), years, "倍", "",
				"息税前利润÷真实财务费用，最低应高于1，越高利息保障越充分。", "真实财务费用≤0"),
		},
	}
}

// buildTaxEffectSection 企业所得税效应。
func buildTaxEffectSection(incomeByYear map[int]model.ReportRow, years []int) model.AnalysisSection {
	return model.AnalysisSection{
		Title: "企业所得税效应",
		Indicators: []model.AnalysisIndicator{
			indN("tax_effect_ratio", "企业所得税效应比率", yearValues(years, func(y int) *float64 {
				return taxEffectRatio(incomeByYear[y])
			}), years, "%", "",
				"净利润÷税前利润=1−实际所得税税率，越高税负越轻、股东权益回报率越高。", "税前利润为0"),
		},
	}
}

// —— 综合分析指标取值（利润表 + 资产负债表，nil 视为 0 / 缺失返回 nil）——

// daysPerYear 计算期天数（文档未指定，取 365）。
const daysPerYear = 365.0

// operatingAssetsTotal 经营资产 = 周转性经营投入 + 长期经营资产（文档：经营资产由长期经营资产和周转性经营投入构成）。
func operatingAssetsTotal(b model.ReportRow) float64 {
	return workingCapital(b) + longOperatingAssets(b)
}

// roe 股东权益回报率 = 净利润 ÷ 股东权益 × 100；股东权益 ≤ 0（资不抵债）时比率无意义，返回 nil。
func roe(balance, income model.ReportRow) *float64 {
	e := equityValue(balance)
	if e <= 0 {
		return nil
	}
	v := netProfitReconstructed(income) / e * 100
	return &v
}

// ebitAssetReturn 息税前资产回报率 = 息税前利润 ÷ 资产总额 × 100。
func ebitAssetReturn(balance, income model.ReportRow) *float64 {
	return ratioPct(ebitTotal(income), rawTotalAssets(balance))
}

// ebitOperatingMargin 息税前经营利润率 = 息税前经营利润 ÷ 营业收入 × 100（纯经营口径）。
func ebitOperatingMargin(income model.ReportRow) *float64 {
	return ratioPct(ebitOperatingProfit(income), revenue(income))
}

// assetTurnover 资产周转率 = 营业收入 ÷ 资产总额（次）。
func assetTurnover(balance, income model.ReportRow) *float64 {
	return turnover(revenue(income), rawTotalAssets(balance))
}

// operatingAssetTurnover 经营资产周转率 = 营业收入 ÷ 经营资产。
func operatingAssetTurnover(balance, income model.ReportRow) *float64 {
	return turnover(revenue(income), operatingAssetsTotal(balance))
}

// longOperatingAssetTurnover 长期经营资产周转率 = 营业收入 ÷ 长期经营资产。
func longOperatingAssetTurnover(balance, income model.ReportRow) *float64 {
	return turnover(revenue(income), longOperatingAssets(balance))
}

// workingCapitalTurnover 周转性经营投入周转率 = 营业收入 ÷ 周转性经营投入。
func workingCapitalTurnover(balance, income model.ReportRow) *float64 {
	return turnover(revenue(income), workingCapital(balance))
}

// ebitOperatingAssetReturn 息税前经营资产回报率 = 息税前经营利润 ÷ 经营资产 × 100。
func ebitOperatingAssetReturn(balance, income model.ReportRow) *float64 {
	den := operatingAssetsTotal(balance)
	if den <= 0 {
		return nil
	}
	v := ebitOperatingProfit(income) / den * 100
	return &v
}

// avgFixedAsset 平均固定资产净值 = (期初 + 期末) ÷ 2；上期缺失返回 nil。
func avgFixedAsset(balanceByYear map[int]model.ReportRow, year int) *float64 {
	cur, ok := balanceByYear[year]
	if !ok {
		return nil
	}
	prev, ok := balanceByYear[year-1]
	if !ok {
		return nil
	}
	v := (v0(prev, "FIXED_ASSET") + v0(cur, "FIXED_ASSET")) / 2
	return &v
}

// accountsReceivableNet 应收账款净额 = 应收账款 + 应收票据 − 预收账款（文档口径）。
func accountsReceivableNet(b model.ReportRow) float64 {
	return v0(b, "ACCOUNTS_RECE") + v0(b, "NOTE_RECE") - v0(b, "ADVANCE_RECEIVABLES")
}

// accountsPayableNet 应付账款净额 = 应付账款 + 应付票据 − 预付账款（文档口径）。
func accountsPayableNet(b model.ReportRow) float64 {
	return v0(b, "ACCOUNTS_PAYABLE") + v0(b, "NOTE_PAYABLE") - v0(b, "PREPAYMENT")
}

// avgAccountsReceivable 平均应收账款净额 = (期初 + 期末) ÷ 2。
func avgAccountsReceivable(balanceByYear map[int]model.ReportRow, year int) *float64 {
	cur, ok := balanceByYear[year]
	if !ok {
		return nil
	}
	prev, ok := balanceByYear[year-1]
	if !ok {
		return nil
	}
	v := (accountsReceivableNet(prev) + accountsReceivableNet(cur)) / 2
	return &v
}

// avgInventory 平均存货余额 = (期初 + 期末) ÷ 2。
func avgInventory(balanceByYear map[int]model.ReportRow, year int) *float64 {
	cur, ok := balanceByYear[year]
	if !ok {
		return nil
	}
	prev, ok := balanceByYear[year-1]
	if !ok {
		return nil
	}
	v := (v0(prev, "INVENTORY") + v0(cur, "INVENTORY")) / 2
	return &v
}

// avgAccountsPayable 平均应付账款净额 = (期初 + 期末) ÷ 2。
func avgAccountsPayable(balanceByYear map[int]model.ReportRow, year int) *float64 {
	cur, ok := balanceByYear[year]
	if !ok {
		return nil
	}
	prev, ok := balanceByYear[year-1]
	if !ok {
		return nil
	}
	v := (accountsPayableNet(prev) + accountsPayableNet(cur)) / 2
	return &v
}

// fixedAssetTurnover 固定资产周转率 = 销售收入 ÷ 平均固定资产净值（销售收入用营业收入近似）。
func fixedAssetTurnover(balanceByYear, incomeByYear map[int]model.ReportRow, year int) *float64 {
	avg := avgFixedAsset(balanceByYear, year)
	if avg == nil {
		return nil
	}
	return turnover(revenue(incomeByYear[year]), *avg)
}

// receivableTurnover 应收账款周转率 = 销售收入 ÷ 平均应收账款净额（用销售收入替代赊销净额）。
func receivableTurnover(balanceByYear, incomeByYear map[int]model.ReportRow, year int) *float64 {
	avg := avgAccountsReceivable(balanceByYear, year)
	if avg == nil {
		return nil
	}
	return turnover(revenue(incomeByYear[year]), *avg)
}

// receivableDays 应收账款周转天数 = 计算期天数 ÷ 应收账款周转率。
func receivableDays(balanceByYear, incomeByYear map[int]model.ReportRow, year int) *float64 {
	t := receivableTurnover(balanceByYear, incomeByYear, year)
	if t == nil || *t <= 0 {
		return nil
	}
	v := daysPerYear / *t
	return &v
}

// inventoryTurnover 存货周转率 = 销货成本 ÷ 平均存货余额（销货成本用营业成本替代）。
func inventoryTurnover(balanceByYear, incomeByYear map[int]model.ReportRow, year int) *float64 {
	avg := avgInventory(balanceByYear, year)
	if avg == nil {
		return nil
	}
	return turnover(operateCost(incomeByYear[year]), *avg)
}

// inventoryDays 存货周转天数 = 计算期天数 ÷ 存货周转率。
func inventoryDays(balanceByYear, incomeByYear map[int]model.ReportRow, year int) *float64 {
	t := inventoryTurnover(balanceByYear, incomeByYear, year)
	if t == nil || *t <= 0 {
		return nil
	}
	v := daysPerYear / *t
	return &v
}

// payableTurnover 应付账款周转率 = 采购成本 ÷ 平均应付账款净额（采购成本用营业成本替代）。
func payableTurnover(balanceByYear, incomeByYear map[int]model.ReportRow, year int) *float64 {
	avg := avgAccountsPayable(balanceByYear, year)
	if avg == nil {
		return nil
	}
	return turnover(operateCost(incomeByYear[year]), *avg)
}

// payableDays 应付账款平均付账期 = 计算期天数 ÷ 应付账款周转率。
func payableDays(balanceByYear, incomeByYear map[int]model.ReportRow, year int) *float64 {
	t := payableTurnover(balanceByYear, incomeByYear, year)
	if t == nil || *t <= 0 {
		return nil
	}
	v := daysPerYear / *t
	return &v
}

// operatingCycle 营业周期 = 存货周转天数 + 应收账款周转天数。
func operatingCycle(balanceByYear, incomeByYear map[int]model.ReportRow, year int) *float64 {
	id := inventoryDays(balanceByYear, incomeByYear, year)
	rd := receivableDays(balanceByYear, incomeByYear, year)
	if id == nil || rd == nil {
		return nil
	}
	v := *id + *rd
	return &v
}

// cashCycle 现金周期 = 存货周转天数 + 应收账款周转天数 − 应付账款平均付账期。
func cashCycle(balanceByYear, incomeByYear map[int]model.ReportRow, year int) *float64 {
	id := inventoryDays(balanceByYear, incomeByYear, year)
	rd := receivableDays(balanceByYear, incomeByYear, year)
	pd := payableDays(balanceByYear, incomeByYear, year)
	if id == nil || rd == nil || pd == nil {
		return nil
	}
	v := *id + *rd - *pd
	return &v
}

// longEquityReturn 长期股权投资收益率 = 长期股权投资收益 ÷ 长期股权投资 × 100。
func longEquityReturn(balance, income model.ReportRow) *float64 {
	den := longEquityInvest(balance)
	if den <= 0 {
		return nil
	}
	v := longEquityInvestmentIncome(income) / den * 100
	return &v
}

// financialAssetReturn 金融资产收益率 = 息税前金融资产收益 ÷ 金融资产 × 100。
func financialAssetReturn(balance, income model.ReportRow) *float64 {
	den := financialAssets(balance)
	if den <= 0 {
		return nil
	}
	v := ebitFinancialAssetIncome(income) / den * 100
	return &v
}

// financeCostEffectRatio 财务成本效应比率 = 税前利润 ÷ 息税前利润 × 100 = 1 − 财务成本负担率。
func financeCostEffectRatio(income model.ReportRow) *float64 {
	return ratioPct(preTaxProfit(income), ebitTotal(income))
}

// dupontLeverage 财务杠杆倍数（杜邦口径）= 资产总额 ÷ 股东权益；股东权益 ≤ 0 返回 nil。
func dupontLeverage(balance model.ReportRow) *float64 {
	e := equityValue(balance)
	if e <= 0 {
		return nil
	}
	v := rawTotalAssets(balance) / e
	return &v
}

// financialLeverageEffect 财务杠杆效应 = 财务成本效应比率 × 财务杠杆倍数（=税前利润÷息税前利润 × 资产总额÷股东权益）。
func financialLeverageEffect(balance, income model.ReportRow) *float64 {
	ebit := ebitTotal(income)
	if ebit == 0 {
		return nil
	}
	lev := dupontLeverage(balance)
	if lev == nil {
		return nil
	}
	v := (preTaxProfit(income) / ebit) * *lev
	return &v
}

// interestCoverage 利息保障倍数 = 息税前利润 ÷ 真实财务费用（利息费用取真实财务费用，用户确认）。
func interestCoverage(income model.ReportRow) *float64 {
	rfe := realFinanceExpense(income)
	if rfe <= 0 {
		return nil
	}
	v := ebitTotal(income) / rfe
	return &v
}

// debtEquityRatio 债务对股东权益比率 = (短期借款 + 长期借款) ÷ 股东权益。
func debtEquityRatio(balance model.ReportRow) *float64 {
	e := equityValue(balance)
	if e <= 0 {
		return nil
	}
	v := (v0(balance, "SHORT_LOAN") + v0(balance, "LONG_LOAN")) / e
	return &v
}

// taxEffectRatio 企业所得税效应比率 = 净利润 ÷ 税前利润 × 100 = 1 − 实际所得税税率。
func taxEffectRatio(income model.ReportRow) *float64 {
	return ratioPct(netProfitReconstructed(income), preTaxProfit(income))
}

// turnover 周转率 = num ÷ den（次），den ≤ 0 返回 nil（零/负资产无法周转）。
func turnover(num, den float64) *float64 {
	if den <= 0 {
		return nil
	}
	v := num / den
	return &v
}

// revenueRatioValues 对每年计算分子(元) ÷ 营业收入 × 100（%），营业收入为 0 返回 nil。
// 用于毛利率、息前税后营业收入利润率等以营业收入为分母的比率。
func revenueRatioValues(incomeByYear map[int]model.ReportRow, years []int, num func(model.ReportRow) float64) []*float64 {
	out := make([]*float64, len(years))
	for i, y := range years {
		out[i] = ratioPct(num(incomeByYear[y]), revenue(incomeByYear[y]))
	}
	return out
}

// absValues 对每年应用 fn 生成绝对额数值（资产负债表科目 nil 视为 0）。
func absValues(balanceByYear map[int]model.ReportRow, years []int, fn func(model.ReportRow) float64) []*float64 {
	out := make([]*float64, len(years))
	for i, y := range years {
		out[i] = fp(fn(balanceByYear[y]))
	}
	return out
}

// ratioOf 对每年计算 num/den×100（%），分母为 0 返回 nil。
func ratioOf(balanceByYear map[int]model.ReportRow, years []int, num, den func(model.ReportRow) float64) []*float64 {
	out := make([]*float64, len(years))
	for i, y := range years {
		out[i] = ratioPct(num(balanceByYear[y]), den(balanceByYear[y]))
	}
	return out
}

// ratioPct num/den×100，den 为 0 返回 nil。
func ratioPct(num, den float64) *float64 {
	if den == 0 {
		return nil
	}
	v := num / den * 100
	return &v
}

// strategicCashDemand 战略投资活动综合现金需求 = 长期经营资产净投资额 + 并购活动净合并额。
func strategicCashDemand(cf model.ReportRow) float64 {
	return constructNet(cf) + mergerNet(cf)
}

// cashSelfSufficiency 现金自给率 = 经营活动现金流量净额 ÷ 战略投资活动综合现金需求 × 100。
// 需求 ≤ 0（无战略投资需求或处于收缩处置）时该指标无意义，返回 nil。
func cashSelfSufficiency(cf model.ReportRow) *float64 {
	demand := strategicCashDemand(cf)
	if demand <= 0 {
		return nil
	}
	v := v0(cf, "NETCASH_OPERATE") / demand * 100
	return &v
}

// financialAssets 金融资产合计（15 科目，nil 视为 0：不持有即 0）。
func financialAssets(b model.ReportRow) float64 {
	return sum0Fields(b, financialAssetFields)
}

// openingFinancialAssets 期初金融资产（上期末金融资产合计），上期末缺失返回 nil。
func openingFinancialAssets(balanceByYear map[int]model.ReportRow, year int) *float64 {
	prev, ok := balanceByYear[year-1]
	if !ok {
		return nil
	}
	v := financialAssets(prev)
	return &v
}

// financingGap 筹资需求 = 期初金融资产 + 经营活动现金流量净额 − 战略投资活动综合现金需求。
func financingGap(balanceByYear, cfByYear map[int]model.ReportRow, year int) *float64 {
	open := openingFinancialAssets(balanceByYear, year)
	if open == nil {
		return nil
	}
	cf := cfByYear[year]
	v := *open + v0(cf, "NETCASH_OPERATE") - strategicCashDemand(cf)
	return &v
}

// interestPaidCash 偿付利息支付现金（文档方法一）= 分配股利、利润或偿付利息支付的现金 − 母公司股东股利 − 子公司支付给少数股东的股利。
// 该方法得到的现金利息包含资本化利息，比利润表「费用化利息支出」更接近总利息成本。
func interestPaidCash(cf model.ReportRow, parentDividend float64) float64 {
	return v0(cf, "ASSIGN_DIVIDEND_PORFIT") - parentDividend - v0(cf, "SUBSIDIARY_PAY_DIVIDEND")
}

// equityFinancingNet 股东筹资净额 = 吸收投资收到的现金 − 分配股利、利润支付的现金。
// 分配股利、利润支付的现金 = 分配股利、利润或偿付利息支付的现金 − 偿付利息支付现金。
func equityFinancingNet(cf model.ReportRow, parentDividend float64) float64 {
	dividend := v0(cf, "ASSIGN_DIVIDEND_PORFIT") - interestPaidCash(cf, parentDividend)
	return v0(cf, "ACCEPT_INVEST_CASH") - dividend
}

// debtFinancingNet 债务筹资净额 = 借款+发债收到的现金 − 偿还债务+偿付利息支付的现金。
func debtFinancingNet(cf model.ReportRow, parentDividend float64) float64 {
	return v0(cf, "RECEIVE_LOAN_CASH") + v0(cf, "ISSUE_BOND") -
		v0(cf, "PAY_DEBT_CASH") - interestPaidCash(cf, parentDividend)
}

// interestBearingDebt 有息债务 = 短期债务 + 长期债务（nil 视为 0）。
func interestBearingDebt(b model.ReportRow) float64 {
	return sum0Fields(b, shortDebtFields) + sum0Fields(b, longDebtFields)
}

// debtCapitalCost 债务资本成本 = 偿付利息支付现金 ÷ 平均有息债务余额 × 100。
// 用现金利息（含资本化利息）近似总利息支出，见 interestPaidCash 说明。
// 平均有息债务余额 = (年初有息债务 + 年末有息债务) / 2；为 0 时无法计算，返回 nil。
func debtCapitalCost(balanceByYear, cfByYear map[int]model.ReportRow, divByYear map[int]float64, year int) *float64 {
	cur := interestBearingDebt(balanceByYear[year])
	prev := interestBearingDebt(balanceByYear[year-1])
	avg := (cur + prev) / 2
	if avg == 0 {
		return nil
	}
	v := interestPaidCash(cfByYear[year], divByYear[year]) / avg * 100
	return &v
}

// effectiveTaxRate 实际所得税税率 = 所得税费用 ÷ (利润总额 − 长期股权投资收益)。
// 分母 ≤ 0 或所得税费用缺失时回落 25%；结果 clamp 到 [0,1]。
func effectiveTaxRate(income model.ReportRow) float64 {
	tax := v0(income, "INCOME_TAX")
	base := v0(income, "TOTAL_PROFIT") - v0(income, "INVEST_JOINT_INCOME")
	if base <= 0 {
		return 0.25
	}
	r := tax / base
	if r < 0 {
		return 0
	}
	if r > 1 {
		return 1
	}
	return r
}

// effectiveTaxRateDisplay 展示用的实际所得税税率（%）。公式同 effectiveTaxRate，但当基数
// （利润总额−长期股权投资收益）≤0、所得税为负、或所得税超过基数（比率>100%）时，该比率无实际
// 经济含义，返回 nil（前端显示「—」）。区别于 effectiveTaxRate：后者供内部计算使用，需始终有界。
func effectiveTaxRateDisplay(income model.ReportRow) *float64 {
	tax := v0(income, "INCOME_TAX")
	base := v0(income, "TOTAL_PROFIT") - v0(income, "INVEST_JOINT_INCOME")
	if base <= 0 || tax < 0 || tax > base {
		return nil
	}
	v := tax / base * 100
	return &v
}

// effectiveTaxRateNote 实际所得税税率的异常标注。当某年基数（利润总额−长期股权投资收益）≤0、
// 所得税为负、或所得税超过基数（比率>100%）时，该年税率为失真值，界面隐藏数值并在此标注说明。
// 典型如威孚高科等利润主要由联营/合营企业投资收益构成的公司，基数极小导致比率虚高。
func effectiveTaxRateNote(incomeByYear map[int]model.ReportRow, years []int) string {
	var parts []string
	for _, y := range years {
		inc := incomeByYear[y]
		tax := v0(inc, "INCOME_TAX")
		base := v0(inc, "TOTAL_PROFIT") - v0(inc, "INVEST_JOINT_INCOME")
		switch {
		case base <= 0:
			parts = append(parts, fmt.Sprintf("%d 年基数≤0（利润主要由长期股权投资收益构成），实际税率失真", y))
		case tax < 0:
			parts = append(parts, fmt.Sprintf("%d 年所得税为负，实际税率失真", y))
		case tax > base:
			parts = append(parts, fmt.Sprintf("%d 年基数过小、比率>100%%，实际税率失真", y))
		}
	}
	return strings.Join(parts, "；")
}

// wacc 加权平均资本成本 = (有息债务/投入资本)×税后债务资本成本 + (股东权益/投入资本)×股权资本成本。
// 投入资本 = 有息债务 + 股东权益；股权资本成本默认 8%；税后债务资本成本 = 债务资本成本×(1−实际所得税税率)。
// 无有息债务时债务成本项为 0，退化为纯股权成本 8%。投入资本为 0 时返回 nil。
func wacc(balanceByYear, cfByYear, incomeByYear map[int]model.ReportRow, divByYear map[int]float64, year int) *float64 {
	b := balanceByYear[year]
	debt := interestBearingDebt(b)
	equity := v0(b, "TOTAL_EQUITY")
	invested := debt + equity
	if invested == 0 {
		return nil
	}
	equityCost := 8.0 // %，文档默认股权资本成本
	afterTaxDebtCost := 0.0
	if dc := debtCapitalCost(balanceByYear, cfByYear, divByYear, year); dc != nil {
		afterTaxDebtCost = *dc * (1 - effectiveTaxRate(incomeByYear[year]))
	}
	v := (debt/invested)*afterTaxDebtCost + (equity/invested)*equityCost
	return &v
}

// yearValues 对范围内每个年份应用 fn 生成跨年度数值（fn 返回 nil 表示无法计算）。
// 用于需要跨年度上下文（上期末资产负债、利润表）的指标，区别于单一现金流行的 derivedValues。
func yearValues(years []int, fn func(int) *float64) []*float64 {
	out := make([]*float64, len(years))
	for i, y := range years {
		out[i] = fn(y)
	}
	return out
}

// sum0Fields 对 ReportRow 中给定字段求和，nil 视为 0。
func sum0Fields(b model.ReportRow, keys []string) float64 {
	var total float64
	for _, k := range keys {
		if v := b.Fields[k]; v != nil {
			total += *v
		}
	}
	return total
}

// constructNet 长期经营资产净投资额 = 购建长期经营资产支付的现金 − 处置长期经营资产收回的现金。
func constructNet(cf model.ReportRow) float64 {
	return v0(cf, "CONSTRUCT_LONG_ASSET") - v0(cf, "DISPOSAL_LONG_ASSET")
}

// mergerNet 并购活动净合并额 = 取得子公司支付的现金 − 处置子公司收回的现金。
func mergerNet(cf model.ReportRow) float64 {
	return v0(cf, "OBTAIN_SUBSIDIARY_OTHER") - v0(cf, "DISPOSAL_SUBSIDIARY_OTHER")
}

// maintenanceCapex 保全性资本支出近似值：折旧 + 摊销（含使用权资产摊销）+ 资产减值准备 + 固定资产报废损失。
// 对应《公司财务指标分析》文档「保全性资本支出」定义，暂用现金流量表补充资料科目近似。
func maintenanceCapex(cf model.ReportRow) float64 {
	return v0(cf, "FA_IR_DEPR") + v0(cf, "IA_AMORTIZE") + v0(cf, "LPE_AMORTIZE") +
		v0(cf, "USERIGHT_ASSET_AMORTIZE") + v0(cf, "ASSET_IMPAIRMENT") + v0(cf, "FA_SCRAP_LOSS")
}

// expansionCapex 长期经营资产扩张性资本支出 = 净投资额 − 保全性资本支出。
func expansionCapex(cf model.ReportRow) float64 {
	return constructNet(cf) - maintenanceCapex(cf)
}

// strategyExpansion 战略投资活动总体规模扩张 = 扩张性资本支出 + 并购活动净合并额。
func strategyExpansion(cf model.ReportRow) float64 {
	return expansionCapex(cf) + mergerNet(cf)
}

// v0 取科目值，nil 视为 0。用于现金流量表主表必有科目：无该交易即为 0，
// 而非「无数据」，避免派生指标因 nil 传播而错误显示「—」。
func v0(cf model.ReportRow, key string) float64 {
	if v := cf.Fields[key]; v != nil {
		return *v
	}
	return 0
}

// fp 将 float64 装箱为指针（供 nil 语义的 *float64 指标使用）。
func fp(v float64) *float64 { return &v }

// derivedValues 对每年应用 fn 生成跨年度数值（fn 返回 float64，主表科目 nil 视为 0）。
func derivedValues(cfByYear map[int]model.ReportRow, years []int, fn func(model.ReportRow) float64) []*float64 {
	out := make([]*float64, len(years))
	for i, y := range years {
		out[i] = fp(fn(cfByYear[y]))
	}
	return out
}

// ratioValues 扩张性资本支出比例：需上期资产负债表（期初净额），缺失年份为 nil。
func ratioValues(balanceByYear, cfByYear map[int]model.ReportRow, years []int) []*float64 {
	out := make([]*float64, len(years))
	for i, y := range years {
		expansion := fp(expansionCapex(cfByYear[y]))
		out[i] = divPct(expansion, openingLongAssets(balanceByYear, y))
	}
	return out
}

// openingLongAssets 长期经营资产期初净额（上期末长期经营资产合计）。
// 口径对应《公司财务指标分析》文档「长期经营资产」定义，共 13 个科目（见 longOperatingAssetFields）。
func openingLongAssets(balanceByYear map[int]model.ReportRow, year int) *float64 {
	prev, ok := balanceByYear[year-1]
	if !ok {
		return nil
	}
	v := longOperatingAssets(prev)
	return &v
}

// ind 构造单个分析指标。
func ind(key, name string, values []*float64, unit, kind, interpretation string) model.AnalysisIndicator {
	return model.AnalysisIndicator{
		Key: key, Name: name, Values: values, Unit: unit, Kind: kind,
		Interpretation: interpretation,
	}
}

// nilNote 生成「无意义未显示」标注：values 中为 nil 的年份视为无意义（前端显示「—」），
// 列出这些年份并说明原因；无 nil 年份返回 ""（不标注）。
func nilNote(values []*float64, years []int, reason string) string {
	var ys []int
	for i, v := range values {
		if v == nil {
			ys = append(ys, years[i])
		}
	}
	if len(ys) == 0 {
		return ""
	}
	return fmt.Sprintf("%s 年%s，未显示", joinYears(ys), reason)
}

// indN 构造带「无意义标注」的指标：values 存在 nil（前端显示「—」）时，按 reason 生成 Note 说明原因。
func indN(key, name string, values []*float64, years []int, unit, kind, interpretation, reason string) model.AnalysisIndicator {
	it := ind(key, name, values, unit, kind, interpretation)
	it.Note = nilNote(values, years, reason)
	return it
}

// divPct a/b*100，任一为 nil 或 b==0 返回 nil。
func divPct(a, b *float64) *float64 {
	if a == nil || b == nil || *b == 0 {
		return nil
	}
	v := *a / *b * 100
	return &v
}

// annualRows 过滤年报，返回 year→row 与升序年份列表。
func annualRows(rows []model.ReportRow) (map[int]model.ReportRow, []int) {
	byYear := make(map[int]model.ReportRow)
	for _, r := range rows {
		y, ok := model.AnnualYear(r.ReportDate)
		if !ok {
			continue
		}
		byYear[y] = r
	}
	years := make([]int, 0, len(byYear))
	for y := range byYear {
		years = append(years, y)
	}
	sort.Ints(years)
	return byYear, years
}
