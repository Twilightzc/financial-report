package model

// 三大报表「全科目」白名单：字段 key → 中文名。
// 顺序即前端展示顺序；仅列出真实财务科目，元数据（代码/名称/报告期等）
// 与派生比例字段（*_RATIO、*_CALCULATE）不在此列，由展示层自然排除。
//
// 字段名基于东财 datacenter（columns=ALL）实测得出；含银行/保险等行业专属科目，
// 非金融股对应科目返回 null，前端展示为「—」。

// BalanceFields 资产负债表全科目
var BalanceFields = []FieldDef{
	{"MONETARYFUNDS", "货币资金"},
	{"SETTLE_EXCESS_RESERVE", "结算备付金"},
	{"CASH_DEPOSIT_PBC", "存放中央银行款项"},
	{"BORROW_FUND", "拆出资金"},
	{"AVAILABLE_SALE_FINASSET", "可供出售金融资产"},
	{"ACCOUNTS_RECE", "应收账款"},
	{"ADVANCE_RECEIVABLES", "预收款项"},
	{"PREMIUM_RECE", "应收保费"},
	{"INVENTORY", "存货"},
	{"LOAN_ADVANCE", "发放贷款及垫款"},
	{"FIXED_ASSET", "固定资产"},
	{"TOTAL_ASSETS", "资产总计"},
	{"SHORT_LOAN", "短期借款"},
	{"LOAN_PBC", "向中央银行借款"},
	{"ACCEPT_DEPOSIT", "吸收存款及同业存放"},
	{"SELL_REPO_FINASSET", "卖出回购金融资产款"},
	{"AGENT_TRADE_SECURITY", "代理买卖证券款"},
	{"ADVANCE_PREMIUM", "预收保费"},
	{"ACCOUNTS_PAYABLE", "应付账款"},
	{"TOTAL_LIABILITIES", "负债合计"},
	{"TOTAL_EQUITY", "所有者权益合计"},
}

// IncomeFields 利润表全科目
var IncomeFields = []FieldDef{
	{"TOTAL_OPERATE_INCOME", "营业总收入"},
	{"OPERATE_INCOME", "营业收入"},
	{"INTEREST_NI", "利息净收入"},
	{"FEE_COMMISSION_NI", "手续费及佣金净收入"},
	{"EARNED_PREMIUM", "已赚保费"},
	{"TOTAL_OPERATE_COST", "营业总成本"},
	{"OPERATE_COST", "营业成本"},
	{"OPERATE_EXPENSE", "营业支出"},
	{"OPERATE_TAX_ADD", "营业税金及附加"},
	{"SALE_EXPENSE", "销售费用"},
	{"MANAGE_EXPENSE", "管理费用"},
	{"MANAGE_EXPENSE_BANK", "业务及管理费"},
	{"FINANCE_EXPENSE", "财务费用"},
	{"SURRENDER_VALUE", "退保金"},
	{"COMPENSATE_EXPENSE", "赔付支出"},
	{"INVEST_INCOME", "投资收益"},
	{"OPERATE_PROFIT", "营业利润"},
	{"TOTAL_PROFIT", "利润总额"},
	{"INCOME_TAX", "所得税费用"},
	{"PARENT_NETPROFIT", "归母净利润"},
	{"DEDUCT_PARENT_NETPROFIT", "扣非归母净利润"},
}

// CashflowGroups 现金流量表：按活动类型分组的全科目
var CashflowGroups = []FieldGroup{
	{
		Title: "经营活动现金流",
		Fields: []FieldDef{
			{"SALES_SERVICES", "销售商品、提供劳务收到的现金"},
			{"CUSTOMER_DEPOSIT_ADD", "客户存款和同业存放款项净增加额"},
			{"RECEIVE_ORIGIC_PREMIUM", "收到原保险合同保费取得的现金"},
			{"RECEIVE_INTEREST_COMMISSION", "收取利息、手续费及佣金的现金"},
			{"PAY_STAFF_CASH", "支付给职工以及为职工支付的现金"},
			{"PAY_ORIGIC_COMPENSATE", "支付原保险合同赔付款项的现金"},
			{"DEPOSIT_IOFI_OTHER", "存放中央银行和同业款项净增加额"},
			{"LOAN_ADVANCE_ADD", "客户贷款及垫款净增加额"},
			{"NETCASH_OPERATE", "经营活动产生的现金流量净额"},
		},
	},
	{
		Title: "投资活动现金流",
		Fields: []FieldDef{
			{"RECEIVE_INVEST_INCOME", "取得投资收益收到的现金"},
			{"CONSTRUCT_LONG_ASSET", "购建固定资产、无形资产和其他长期资产支付的现金"},
			{"INVEST_PAY_CASH", "投资支付的现金"},
			{"NETCASH_INVEST", "投资活动产生的现金流量净额"},
		},
	},
	{
		Title: "筹资活动现金流",
		Fields: []FieldDef{
			{"NETCASH_FINANCE", "筹资活动产生的现金流量净额"},
		},
	},
	{
		Title: "现金及现金等价物",
		Fields: []FieldDef{
			{"BEGIN_CCE", "期初现金及现金等价物余额"},
			{"CCE_ADD", "现金及现金等价物净增加额"},
			{"END_CCE", "期末现金及现金等价物余额"},
		},
	},
}
