package service

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"financial-report/internal/model"
)

// AI 分析的系统提示词（skill），只含静态框架：
// 角色、任务、输出 JSON 结构、五个评分维度与评分标准、估值模型推荐（只解释、不改数）、行业分析规则。
// 指标含义随指标数值一起放进用户消息（避免「字典」与「数值」重复发送指标名）。
const aiSystemFramework = `你是资深财务分析师兼行业研究员。你将收到一家 A 股上市公司的财务指标数据（指标名、单位、含义与按年度顺序的数值），请据此完成财务诊断，并**只输出一个 JSON 对象**（不要输出任何解释，不要用 markdown 代码块包裹）。

## 输出 JSON 结构（严格遵守，字段名与类型固定）
{
  "scores": [
    {"dimension": "盈利能力", "score": 88, "comment": "一句话点评"},
    {"dimension": "偿债能力", "score": 90, "comment": "一句话点评"},
    {"dimension": "现金获取能力", "score": 85, "comment": "一句话点评"},
    {"dimension": "经营效率", "score": 76, "comment": "一句话点评"},
    {"dimension": "成长能力", "score": 82, "comment": "一句话点评"}
  ],
  "conclusion": "总体结论（{conclusion}，分段分点、用换行分隔，覆盖：①公司质地与商业模式，②核心竞争优势/护城河，③财务健康与盈利质量（结合盈利能力/偿债能力/现金获取能力三个维度），④主要风险与隐忧，⑤综合评级与是否值得关注）",
  "industry": {"name": "行业名称", "prospect": "前景(1-2句)", "competition": "竞争格局(1-2句)", "application": "应用方向/需求(1-2句)"},
  "businesses": [{"name": "业务板块", "stage": "发展阶段", "contribution": "贡献占比", "prospect": "前景(1句)", "risk": "风险(1句)", "leaders": [{"name": "证券简称", "code": "可选6位代码", "market": "A股|非A股", "relation": "该细分行业的头部公司/行业地位", "note": "一句话说明"}], "related": [{"name": "证券简称", "code": "可选6位代码", "market": "A股|非A股", "relation": "与该板块的关系", "note": "一句话说明"}]}],
  "chain": {
    "upstream": [{"name": "证券简称", "code": "可选6位代码", "market": "A股|非A股", "relation": "上游角色", "note": "一句话说明"}],
    "downstream": [{"name": "证券简称", "code": "可选6位代码", "market": "A股|非A股", "relation": "下游角色", "note": "一句话说明"}]
  },
  "partners": [{"name": "证券简称", "code": "可选6位代码", "market": "A股|非A股", "relation": "合作类型", "note": "一句话说明"}],
  "valuation": {"industry_type": "cyclical|general|utility", "rationale": "选择理由(2-3句，只能解释、不能改数)"}
}

说明：scores 必须恰好 5 项，维度名只能用「盈利能力、偿债能力、现金获取能力、经营效率、成长能力」这 5 个精确写法；score 为 0-100 的整数。

## 五个评分维度（百分制，0-100 整数）
评分结合行业特性（不同行业标杆不同），主要依据最近一个年报、辅以多年趋势。评分区间参考：90+ 优秀 / 75-89 良好 / 60-74 一般 / 45-59 偏弱 / <45 较差。

1. 盈利能力：看毛利率、营业成本率、总费用率、息税前经营利润率、息前税后营业收入利润率、股东权益回报率(ROE)、息税前资产回报率、息税前经营资产回报率、净利润、股权价值增加值。
2. 偿债能力：看财务杠杆倍数、债务对股东权益比率、利息保障倍数、有息债务占比、股权占比、财务成本负担率、债务资本成本率、长期/短期融资净额。（有息负债低、利息覆盖高、财务负担轻 → 高分）
3. 现金获取能力：看经营活动现金流量净额、营业收入现金含量、成本费用付现率、息前税后经营利润现金含量、净利润现金含量、现金自给率。（利润含金量高、现金自给 → 高分）
4. 经营效率：看资产周转率、经营资产周转率、长期经营资产/固定资产/周转性经营投入周转率、存货/应收账款/应付账款周转率与周转天数、营业周期、现金周期。
5. 成长能力：看营业收入、净利润、经营活动现金流量净额的同比增速，长期经营资产扩张性资本支出及比例、战略投资活动总体规模扩张、并购活动净合并额、研发费用率趋势。

## 估值模型推荐（只解释、不改数）
- 估值参数已由系统按该公司实际财报数据**确定性推导**，并会在用户消息中给出（模型、折现率、各阶段增长率与年数、基准增速 X% 及其口径）。同一家公司每次分析结果完全相同。
- 你的任务**仅为这些参数写 rationale（2-3 句）**：说明为什么该模型合适、该折现率与增长率反映了哪些财务与行业因素（可结合行业前景/竞争格局做定性解释）。
- 硬性要求：rationale 中出现的任何数字必须与给定参数完全一致；**不得**建议或写出其它模型/折现率/增长率取值。
- 系统还会给出「基期自由现金流」的确定性选取（选取方式、年数或自定义值及其口径）。请**结合公司发展**（业绩增速与自由现金流增速是否可持续）在 rationale 中解释为何选该基期自由现金流；**不得**建议其它选取方式或自定义值，出现的一切数字必须与给定值一致。
- 折现率由「WACC 基线 + 公司特定风险溢价」确定性推导；其中 β 由你输出的 industry_type 按常量表确定（cyclical 1.2 / general 1.0 / utility 0.8）。系统会在收到你的 industry_type 后据此**重算**折现率与永续增长率，故 rationale 请解释口径与依据（WACC 基线 + 公司风险溢价 + 行业 β 档位），**不要写出折现率 / 永续增长率的具体数值**。
- 输出 JSON 中 valuation 需返回 {"industry_type": "...", "rationale": "..."}；除 industry_type 外的其余字段由系统回填，即使填写也会被忽略。

## 行业类型判定规则
- 依据主营构成（板块营收/利润占比与毛利率）与行业知识，从以下三选一中判定公司所属行业类型，并在 valuation.industry_type 字段**原样输出 ASCII key**：cyclical（周期性行业：钢铁/煤炭/化工/航运/养殖/券商/有色等，业绩随宏观或商品周期大幅波动）、general（一般行业：消费/制造/医药/科技/公用事业以外的多数行业）、utility（公用事业：电力/燃气/水务/高速公路/港口等，需求与现金流稳定、受价格管制）。
- **不确定时输出 general**；不得输出其它取值。

## 行业分析规则
- 行业 name 由公司名称推断；prospect（前景）、competition（竞争格局）、application（应用方向/需求）各写 1-2 句，结论简洁，不堆砌。

## 业务板块分析规则
- 依据主营构成数据（各板块营收占比、利润占比、毛利率）与行业知识，列出公司主要业务板块（2-4 个）。
- 每个板块：name（板块名）、stage（所处阶段：起步/成长/成熟/衰退）、contribution（贡献占比，同时看营收占比与利润占比，如「营收占 87%、利润占 89%」；若利润占比明显低于营收占比，说明该板块盈利质量偏弱，要在前景/风险里点出）、prospect（前景 1 句）、risk（主要风险 1 句）。
- 占比必须贴近主营构成数据给出的实际数值，不要凭空编造；结合毛利率判断该板块是否真正赚钱。
- 每个板块可输出 leaders（0–3 家）：只列该板块所属细分行业的头部/龙头公司（同行竞争者），
  以证券简称为主；不得把公司级全局行业龙头榜单复制到每个板块；不同板块允许同一家公司，
  不做跨列表去重；同一板块内不重复、不得包含当前公司自身；无把握输出空数组 []。
- 每个板块可输出 related（0–3 家）：只列与该板块强相关的客户/供应商/渠道/合作方；
  不得把全局行业龙头、businesses[].leaders 或 chain/partners 整段复制到各板块；
  不同板块允许同一家公司，不做跨列表去重；同一板块内不重复、不含当前公司自身；无把握输出空数组 []。
- leaders 与 related 语义不同、分别输出，禁止互相替代、合并或去重；
  businesses 内仅允许新增 leaders / related，name/stage/contribution/prospect/risk 保持既有要求。

## 相关公司规则（产业链上下游 / 合作公司）
- 全局行业龙头由系统确定性获取，不要输出顶层 leaders / competitors 字段；
  businesses[].leaders 是细分业务板块龙头，按业务板块规则输出，不在此禁止范围内。
- 必须输出 chain.upstream、chain.downstream、partners 三组字段；无内容时输出空数组 []。
- 产业链上下游围绕公司营收占比最大的主营板块展开；上游=原材料/零部件/设备/能源等供应商侧，
  下游=客户/渠道/终端应用等需求侧。同业公司不得放入上游或下游。
- 公司统一使用证券简称（如「贵州茅台」「宁德时代」），不得使用「XX股份有限公司」等法定全称；优先 A 股上市公司。
- 上游、下游、合作公司各最多 3 家；不得包含当前公司自身；同一列表内不得重复。
- code 为可选项，只在确信时填写 6 位数字；不确定就留空，不得猜测或编造。
- 合作关系只列公开可查、较高把握的内容；不确定时输出空数组，不得编造合作方。
- relation ≤ 20 字，note ≤ 30 字；只描述产业链角色或合作内容，不写买卖建议、评级、目标价。
- 不得改动既有 scores / conclusion / industry / valuation 的任何内容；
  businesses 中除新增 leaders / related 外，name/stage/contribution/prospect/risk 保持既有要求，
  不得因 leaders / related 改写成公司级龙头名单。
`

// aiScoreDimensions 五个评分维度的固定顺序与名称。
var aiScoreDimensions = []string{"盈利能力", "偿债能力", "现金获取能力", "经营效率", "成长能力"}

// aiConclusionSpec 各分析模式（推理强度）的结论详略要求。简洁→短、快；详细→长、深入。
func aiConclusionSpec(reasoning string) string {
	switch reasoning {
	case "low":
		return "约100字，简洁精炼"
	case "high":
		return "约300字，详尽深入"
	default: // medium
		return "约200字，简明扼要"
	}
}

// BuildAIPrompt 组装 AI 分析的系统提示词（skill）与用户消息（公司信息 + 主营构成 + 指标数值与含义 + 确定性估值参数）。
// reasoning 为 low/medium/high，控制结论详略（简洁/标准/详细），进而影响速度与详细度。
// rec 为后端按财报数据确定性推导的估值参数（R7）：作为事实喂给大模型，供其撰写与此自洽的 rationale。
func BuildAIPrompt(a model.FinancialAnalysis, q model.Quote, reasoning string, segments []model.SegmentIncome, rec model.ValuationRecommendation) (system, user string) {
	system = strings.Replace(aiSystemFramework, "{conclusion}", aiConclusionSpec(reasoning), 1)
	user = buildUserMessage(a, q, segments, rec)
	return system, user
}

// buildUserMessage 用户消息：公司信息 + 主营构成 + 年度 + 各维度指标（每行含单位与含义）+ 确定性估值参数。
func buildUserMessage(a model.FinancialAnalysis, q model.Quote, segments []model.SegmentIncome, rec model.ValuationRecommendation) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("公司：%s（代码 %s），总市值约 %.0f 亿元。\n", q.Name, q.Code, q.MarketCap/1e8))
	if summary := segmentSummary(segments); summary != "" {
		b.WriteString(fmt.Sprintf("主营构成（最新年报）：%s。\n", summary))
	}
	b.WriteString(fmt.Sprintf("年度：%s。\n", joinYears(a.Years)))
	b.WriteString("以下为各维度财务指标，每行格式为「指标名（单位；含义）：按年度顺序的数值」（— 表示无数据）：\n")
	b.WriteString(serializeIndicators(a))
	b.WriteString("\n")
	b.WriteString(valuationPromptBlock(rec))
	return b.String()
}

// valuationPromptBlock 把确定性推导的估值参数序列化为用户消息中的一段（大模型只据此写 rationale）。
func valuationPromptBlock(rec model.ValuationRecommendation) string {
	var b strings.Builder
	b.WriteString("估值参数（系统按实际财报数据确定性推导，请据实解释、不得改动）：\n")
	b.WriteString(fmt.Sprintf("- 复合增长锚 A：%.2f%%；历史锚 A_hist：%.2f%%；前瞻锚 A_fwd：%s；宏观锚 A_macro：%.2f%%（%s）。\n",
		rec.GrowthComposite, rec.GrowthHistory, forwardPromptValue(rec),
		rec.GrowthMacro, macroBasisPromptText(rec)))
	b.WriteString(fmt.Sprintf("- 历史锚构成：营收 %s / 净利润 %s / FCF %s（%s）；%s\n",
		growthPromptValue(rec.GrowthRevenue, rec.GrowthRevenueOK),
		growthPromptValue(rec.GrowthProfit, rec.GrowthProfitOK),
		growthPromptValue(rec.GrowthFCF, rec.GrowthFCFOK),
		baseGrowthWeightText(rec), rec.BaseGrowthNote))
	b.WriteString(fmt.Sprintf("- 模型：%s（%s）；折现率：%.1f%%\n", rec.ModelName, rec.Model, rec.DiscountRate))
	// R15/P1：行业类型判定要求（恒输出，与 hasBalance 无关——行业判定不吃财务数据）。
	b.WriteString("- 行业类型：请判定并在 industry_type 输出（cyclical 周期性行业 / general 一般行业 / utility 公用事业，三选一，不确定取 general）。\n")
	// R15/P1：折现率构成（预推导口径；终态折现率与永续增长率将按 AI 判定的行业类型重算）。
	if rec.WaccBaseline > 0 {
		// FR-10/FR-15：Re → 资本结构 → 税后 Rd → WACC → 风险溢价 → 收口的可复算链路。
		line := fmt.Sprintf("- 折现率构成（WACC 分步）：WACC 基线 %.2f%%；", rec.WaccBaseline)
		if rec.EquityCost > 0 {
			line += fmt.Sprintf("Re = Rf %.2f%% + β %.2f（%s）× ERP %.2f%% = %.2f%%；",
				rec.RiskFreeRate, rec.Beta, rec.BetaTier, rec.EquityRiskPremium, rec.EquityCost)
		} else {
			line += fmt.Sprintf("β %.2f（%s）；", rec.Beta, rec.BetaTier)
		}
		if rec.DebtWeight > 0 {
			rdText := fmt.Sprintf("Rd_pre %.2f%% × (1−t %.2f%%) = Rd_after %.2f%%",
				rec.DebtCostPretax, rec.EffectiveTaxRate*100, rec.AfterTaxDebtCost)
			if rec.DebtCostEstimated {
				rdText += "（债务成本不可用，按 Rf+2.0pp 保守估计）"
			}
			line += fmt.Sprintf("wD %.2f / wE %.2f；%s；", rec.DebtWeight, rec.EquityWeight, rdText)
		} else {
			line += "无有息债务（wD=0，WACC=Re）；"
		}
		line += fmt.Sprintf("+公司风险溢价 %.2f%% → %.2f%% → 收口 %.1f%%（[8,15] 0.5 网格）；β 将按你判定的 industry_type 重算（cyclical 1.2 / general 1.0 / utility 0.8），折现率与永续增长率随之重算。",
			rec.RiskPremium, rec.WaccBaseline+rec.RiskPremium, rec.DiscountRate)
		b.WriteString(line + "\n")
	} else {
		b.WriteString("- 折现率构成：缺少资产负债表，折现率回落保守默认值；β 将按你判定的 industry_type 重算（cyclical 1.2 / general 1.0 / utility 0.8），折现率与永续增长率随之重算。\n")
	}
	if len(rec.Params) > 0 {
		parts := make([]string, 0, len(rec.Params))
		for _, p := range rec.Params {
			parts = append(parts, fmt.Sprintf("%s %s=%s", p.Label, p.Key, formatParamValue(p.Value)))
		}
		b.WriteString("- 参数：" + strings.Join(parts, "；") + "\n")
	}
	if rec.Warning != "" {
		b.WriteString("- 提示：" + rec.Warning + "\n")
	}
	// R13：基期自由现金流选取推荐（数值与方式名严格取自 rec，大模型只据此解释）。
	if rec.FCFRationale != "" {
		b.WriteString("- 基期自由现金流：" + baseFCFPromptText(rec) + "\n")
	}
	// R15 整合版：FCF 公式、窗口与 capex/现金含量提示（FR-1/FR-7/FR-9）。
	if rec.FCFFormulaNote != "" {
		line := "- 基期 FCF 公式：" + rec.FCFFormulaNote
		if rec.FCFWindowStart > 0 && rec.FCFWindowEnd > 0 {
			line += fmt.Sprintf(" 推荐窗口 %d–%d 年", rec.FCFWindowStart, rec.FCFWindowEnd)
		}
		line += fmt.Sprintf("；推荐值 %.2f 亿元。", rec.FCFBaseValue/1e8)
		b.WriteString(line + "\n")
	}
	if rec.CapexCoverage > valCapexCoverageHigh {
		b.WriteString(fmt.Sprintf("- capex 提示：购建长期资产现金/保全性资本支出 %.2f 倍（> 1.3），保全口径未扣扩张性资本开支，基期 FCF 可能偏乐观。\n",
			rec.CapexCoverage))
	}
	if rec.FCFProfitRatio > valFCFProfitHigh {
		b.WriteString(fmt.Sprintf("- 现金含量提示：FCF 中位数为净利润的 %.2f 倍（> 1.5），只解释折旧摊销/减值/营运资本差异，不得改写数字。\n",
			rec.FCFProfitRatio))
	}
	// R18：三层锚、周期位置、负增长归因与模型分档（只解释口径，不得改写数字）。
	// 缺失分项标记「不可用」而不是写成 0%，避免向大模型编造数据点（A3）。
	downsideText := "不可用"
	if rec.HasStabilityScore {
		downsideText = fmt.Sprintf("%.2f pp（稳定性分按此计算）", rec.GrowthDownsideDispersion)
	}
	b.WriteString(fmt.Sprintf("- 盈利波动：全幅极差 %s；下行波动 %s。\n",
		dispersionPromptValue(rec.GrowthDispersion, rec.HasStabilityScore), downsideText))
	if rec.CycleBasis != "" {
		b.WriteString(fmt.Sprintf("- 周期位置：%s（%+.2f）；历史锚修正 Δ_cycle %+.1fpp，已内含于 A（只作用于历史锚）；%s。\n",
			rec.CyclePositionLabel, rec.CyclePosition, rec.GrowthCycleAdjust, rec.CycleBasis))
	}
	b.WriteString(fmt.Sprintf("- 负增长归因：%s（%s）；正常化 A_norm = %.2f%%。\n",
		rec.NegativeGrowthType, rec.NegativeGrowthBasis, rec.GrowthNormalized))
	b.WriteString(fmt.Sprintf("- 模型分档：得分 %.2f（%s）→ %s（权重 0.6×Sg + 0.2×Sst + 0.2×Sroe，缺项按剩余权重归一化；阈值 70/45；A<0 时结构性→零增长、周期性→低永续；不得再把 A<3%% 说成零增长）。\n",
		rec.ModelScore, modelScoreParts(rec), rec.ModelName))
	// R15 整合版：复合锚权重与贡献（只读 rec，禁止大模型自行加权）。
	if rec.GrowthAnchorCount > 0 {
		histValue := rec.GrowthHistory + rec.GrowthCycleAdjust
		fwdValue := 0.0
		if rec.GrowthForwardOK {
			fwdValue = rec.GrowthForward
		}
		b.WriteString(fmt.Sprintf("- 复合锚贡献：历史（周期修正后）%.2f%%×%.2f=%.2fpp；前瞻 %.2f%%×%.2f=%.2fpp；宏观 %.2f%%×%.2f=%.2fpp；合计 A_raw %.2fpp → A %.2f%%（锚数 %d）。\n",
			histValue, rec.GrowthWeights.History, histValue*rec.GrowthWeights.History,
			fwdValue, rec.GrowthWeights.Forward, fwdValue*rec.GrowthWeights.Forward,
			rec.GrowthMacro, rec.GrowthWeights.Macro, rec.GrowthMacro*rec.GrowthWeights.Macro,
			histValue*rec.GrowthWeights.History+fwdValue*rec.GrowthWeights.Forward+rec.GrowthMacro*rec.GrowthWeights.Macro,
			rec.GrowthComposite, rec.GrowthAnchorCount))
	}
	// R15 整合版：g1 向下调整的可观测分量（未触发也要写明）。
	g1Text := "不可用"
	if v, ok := paramValueFromParams(rec.Params, "g1"); ok {
		g1Text = fmt.Sprintf("%.2f%%", v)
	} else if v, ok := paramValueFromParams(rec.Params, "g"); ok {
		g1Text = fmt.Sprintf("%.2f%%（永续/零增长模型无 g1，取 g）", v)
	}
	dampingText := "未触发"
	if rec.GrowthDamping > 0 {
		dampingText = fmt.Sprintf("折减 %.2fpp", rec.GrowthDamping)
	}
	b.WriteString(fmt.Sprintf("- g1 向下调整：离散度%s；软上限 %.2f%%；包络 A±%.0fpp；生效 g1 %s。\n",
		dampingText, rec.GrowthCap, rec.GrowthEnvelope, g1Text))
	if rec.SGR > 0 {
		b.WriteString(fmt.Sprintf("- 长期内生增速参考：SGR %.2f%%（留存率 %.1f%%，口径 %s）；仅作终值与软上限参考，不硬压 g1；g1 超过 SGR 只告警、不降级。\n",
			rec.SGR, rec.RetentionRate*100, rec.RetentionBasis))
	} else {
		b.WriteString("- 长期内生增速参考：SGR 不可用（留存率/ROE 数据不足），终值未施加 SGR 约束。\n")
	}
	b.WriteString(fmt.Sprintf("- 终值 g∞：%.2f%%；回归年限 n_total：%d；增长调整：%s。\n",
		rec.GrowthTerminal, rec.GrowthFadeYears, rec.GrowthAdjustment))
	b.WriteString("- 参数已由服务端确定性计算并回填到输出，你只能在 rationale 中解释上述结果，不得给出不同的增长率、模型或归因；最终参数会按你判定的 industry_type 重算，不要复述预推导的具体数字。\n")
	// R15/P3：FCF 周期与研发双条件；R15/P5：同时给出原始判据、成长性豁免与生效判据。
	if rec.FCFWindowYears > 0 {
		vetoBasis := rec.GrowthVetoBasis
		if vetoBasis == "" {
			vetoBasis = "营收数据不足，未判定"
		}
		b.WriteString(fmt.Sprintf("- 周期性：FCF 变异系数 %.2f（符号翻转 %d 次）→ 原始判据%s；成长性豁免%s（%s）→ 生效判据%s；基期 FCF 判定窗口 %d 年、方式 %s。\n",
			rec.FCFCV, rec.FCFSignChanges,
			boolText(rec.FCFRawCyclical, "强周期", "非强周期"),
			boolText(rec.FCFGrowthVeto, "命中", "未命中"), vetoBasis,
			boolText(rec.FCFCyclical, "强周期", "非强周期"),
			rec.FCFWindowYears, rec.FCFModeName))
	}
	if rec.RDRatio > 0 {
		b.WriteString(fmt.Sprintf("- 研发：费用率 %.2f%%，同比扩张 %.2f 亿元 → %s研发调整（需同时满足费用率 > 5%% 且同比扩张 > 0）。\n",
			rec.RDRatio, rec.RDExpansion/1e8, boolText(rec.AdjustRD, "建议开启", "不建议开启")))
	}
	// R15/P4：一致性校验结果（只解释口径，不得改写数字）。
	if rec.BaseYear > 0 {
		line := fmt.Sprintf("- 一致性：基期年 %d（利润表 ∩ 现金流量表）", rec.BaseYear)
		if rec.CapexCoverage > 0 {
			line += fmt.Sprintf("，购建长期资产现金/保全性资本支出 %.2f", rec.CapexCoverage)
		}
		if rec.DeltaNWCRatio > 0 {
			line += fmt.Sprintf("，ΔNWC/基期 FCF %.2f", rec.DeltaNWCRatio)
		}
		if rec.FCFProfitRatio > 0 {
			line += fmt.Sprintf("，FCF/净利润 %.2f", rec.FCFProfitRatio)
		}
		b.WriteString(line + "。以上均为系统校验结果，只解释口径，不得改写数字。\n")
	}
	// R15 整合版：三档参数摘要（固定顺序；即使 applicable=false 也输出，只禁用逐档套用）。
	if len(rec.Scenarios) > 0 {
		if rec.ScenarioBandSource != "" {
			source := map[string]string{
				"disagreement": "历史与前瞻锚分歧",
				"fallback":     "历史/前瞻无有效分歧，按 0.2×A 回退",
				"degenerate":   "负增长/零增长，增长带宽置 0",
			}[rec.ScenarioBandSource]
			if source == "" {
				source = rec.ScenarioBandSource
			}
			b.WriteString(fmt.Sprintf(
				"- 带宽来源：%s；原始分歧/回退 %.1fpp → 增长带宽 %.1fpp（折现率 %.1fpp、终值 %.1fpp）。\n",
				source, rec.ScenarioBandRaw, rec.ScenarioBandGrowth,
				rec.ScenarioBandDiscount, rec.ScenarioTerminalAdjust))
		}
		b.WriteString("三档参数（后端确定性计算，只能解释，不得生成或修改任何数字）：\n")
		for _, s := range rec.Scenarios {
			g1Text := "—"
			if v, ok := paramValueFromParams(s.Params, "g1"); ok {
				g1Text = fmt.Sprintf("%.1f%%", v)
			} else if v, ok := paramValueFromParams(s.Params, "g"); ok {
				g1Text = fmt.Sprintf("%.1f%%", v)
			}
			if s.Key == "base" {
				b.WriteString(fmt.Sprintf("- %s：r=%.1f%%，A=%.1f%%，g1=%s，g∞=%.1f%%，模型=%s，基期FCF=%.2f亿元（%s）（与单点输出一致）\n",
					s.Name, s.DiscountRate, s.GrowthAnchor, g1Text, s.TerminalGrowth, s.ModelName,
					s.BaseFCF/1e8, s.BaseFCFBasis))
				continue
			}
			b.WriteString(fmt.Sprintf("- %s：r=%.1f%%，A=%.1f%%，g1=%s，g∞=%.1f%%，模型=%s，基期FCF=%.2f亿元（%s），Δ增长=%+.1fpp，Δ折现=%+.1fpp，说明=%s\n",
				s.Name, s.DiscountRate, s.GrowthAnchor, g1Text, s.TerminalGrowth, s.ModelName,
				s.BaseFCF/1e8, s.BaseFCFBasis, s.DeltaGrowth, s.DeltaDiscount, s.Note))
		}
	}
	b.WriteString("- 三档由服务端确定性计算；你只能在 rationale 中解释基准与差异，不得输出不同数值、概率或目标价。\n")
	return b.String()
}

// boolText 二值文案辅助（yes/no 依 cond 选择）。
func boolText(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

// growthPromptValue 分项增速的提示词文本：不可用 → 「不可用」，避免把缺失项写成 0%。
func growthPromptValue(v float64, ok bool) string {
	if !ok {
		return "不可用"
	}
	return fmt.Sprintf("%.2f%%", v)
}

// forwardPromptValue 前瞻锚的提示词文本：不可用时显式标注，附机构数/日期/可靠度。
func forwardPromptValue(rec model.ValuationRecommendation) string {
	if !rec.GrowthForwardOK {
		return "不可用"
	}
	text := fmt.Sprintf("%.2f%%", rec.GrowthForward)
	if rec.ConsensusFetched {
		text += fmt.Sprintf("（%d 家机构，截至 %s，可靠度 %s）",
			rec.ConsensusInstitutions, rec.ConsensusAsOf, rec.ConsensusReliability)
	}
	return text
}

// macroBasisPromptText 宏观锚口径文案（动态 GDP / 回退常量）。
func macroBasisPromptText(rec model.ValuationRecommendation) string {
	if rec.GrowthMacroBasis == "dynamic_gdp" {
		if rec.GDPAsOf != "" {
			return "动态 GDP TTM 同比，截至 " + rec.GDPAsOf
		}
		return "动态 GDP TTM 同比"
	}
	return "回退常量 4.5%"
}

// baseGrowthWeightText 历史锚构成权重文案（缺失分项不补 0）。
func baseGrowthWeightText(rec model.ValuationRecommendation) string {
	terms := 0
	for _, ok := range []bool{rec.GrowthRevenueOK, rec.GrowthProfitOK, rec.GrowthFCFOK && rec.GrowthFCFIncluded} {
		if ok {
			terms++
		}
	}
	if terms == 0 {
		return "无可用分项，基准增速按 0 计"
	}
	text := fmt.Sprintf("按 %d 项等权", terms)
	if rec.GrowthFCFOK && !rec.GrowthFCFIncluded {
		text += "；FCF 未计入：" + rec.GrowthFCFExcludeReason
	}
	return text
}

// dispersionPromptValue 盈利增速极差的提示词文本（稳定性信号不可用时标注）。
func dispersionPromptValue(dispersion float64, available bool) string {
	if !available {
		return "不可用"
	}
	return fmt.Sprintf("%.2f pp", dispersion)
}

// modelScoreParts 模型分档的分项得分文本（不可用信号显式标注，不伪装成 0 分）。
func modelScoreParts(rec model.ValuationRecommendation) string {
	parts := []string{fmt.Sprintf("增速分 %.2f", rec.ScoreGrowth)}
	if rec.HasStabilityScore {
		parts = append(parts, fmt.Sprintf("稳定性分 %.2f", rec.ScoreStability))
	} else {
		parts = append(parts, "稳定性分 不可用")
	}
	if rec.HasROEScore {
		parts = append(parts, fmt.Sprintf("ROE 分 %.2f", rec.ScoreROE))
	} else {
		parts = append(parts, "ROE 分 不可用")
	}
	return strings.Join(parts, " / ")
}

// baseFCFPromptText 把基期自由现金流推荐序列化为一行文本：方式名 +（多年度方式）年数 /（custom）推荐值 + 口径。
func baseFCFPromptText(rec model.ValuationRecommendation) string {
	label := rec.FCFModeName
	switch {
	case rec.FCFMode == valFCFCustom && rec.FCFCustomUnit != "":
		if factor, ok := valFCFCustomUnits[rec.FCFCustomUnit]; ok {
			label = fmt.Sprintf("%s，推荐值 %.0f 亿元", rec.FCFModeName, rec.FCFCustomYuan/float64(factor))
		}
	case rec.FCFYears > 0:
		label = fmt.Sprintf("%s（%d 年）", rec.FCFModeName, rec.FCFYears)
	}
	return fmt.Sprintf("%s（口径：%s）", label, rec.FCFRationale)
}

// formatParamValue 格式化估值参数字面值：整数不带小数，其余保留原始精度（如 13.2）。
func formatParamValue(v float64) string {
	if v == math.Trunc(v) {
		return strconv.Itoa(int(v))
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// paramValueFromParams 从模型参数中取指定 key 的值（提示词只读 rec，不做二次推导）。
func paramValueFromParams(params []model.AIValuationParam, key string) (float64, bool) {
	for _, p := range params {
		if p.Key == key {
			return p.Value, true
		}
	}
	return 0, false
}

// segmentSummary 把主营构成序列化为「板块名（营收/利润/毛利率）、…」的紧凑文本，供大模型阅读。
func segmentSummary(segments []model.SegmentIncome) string {
	if len(segments) == 0 {
		return ""
	}
	parts := make([]string, 0, len(segments))
	for _, s := range segments {
		parts = append(parts, fmt.Sprintf("%s（营收 %.2f%%、利润 %.2f%%、毛利率 %.2f%%）", s.Name, s.RevenueRatio*100, s.ProfitRatio*100, s.GrossMargin*100))
	}
	return strings.Join(parts, "、")
}

// serializeIndicators 把已计算出的六维指标序列化为紧凑文本，供大模型阅读。
// 仅保留 done 维度、至少有一年数据的指标；数值不重复年度标签（年度已在头部统一给出）。
func serializeIndicators(a model.FinancialAnalysis) string {
	var b strings.Builder
	for _, dim := range a.Dimensions {
		if dim.Status != "done" {
			continue
		}
		for _, sec := range dim.Sections {
			var sb strings.Builder
			for _, it := range sec.Indicators {
				if !hasData(it.Values) {
					continue
				}
				sb.WriteString(fmt.Sprintf("- %s：%s\n", indicatorLabel(it), formatSeriesCompact(it.Values, it.Unit)))
			}
			if sb.Len() == 0 {
				continue
			}
			b.WriteString(fmt.Sprintf("\n## %s · %s\n", dim.Name, sec.Title))
			b.WriteString(sb.String())
		}
	}
	return b.String()
}

// indicatorLabel 指标名 + 单位 + 含义（无含义则省略）。
func indicatorLabel(it model.AnalysisIndicator) string {
	if it.Interpretation != "" {
		return fmt.Sprintf("%s（%s；%s）", it.Name, unitName(it.Unit), it.Interpretation)
	}
	return fmt.Sprintf("%s（%s）", it.Name, unitName(it.Unit))
}

// hasData 指标是否至少有一年数据（全 nil 视为无数据，不发送）。
func hasData(values []*float64) bool {
	for _, v := range values {
		if v != nil {
			return true
		}
	}
	return false
}

// formatSeriesCompact 把跨年度数值按顺序拼接（无年度标签，nil → 「—」）。
func formatSeriesCompact(values []*float64, unit string) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		if v == nil {
			parts = append(parts, "—")
		} else {
			parts = append(parts, formatValue(*v, unit))
		}
	}
	return strings.Join(parts, "；")
}

// unitName 单位（元/%/倍 原样返回，其余兜底）。
func unitName(unit string) string {
	return unit
}

// formatValue 按单位格式化单个数值（元 → 亿/万，% / 倍保留两位小数）。
func formatValue(v float64, unit string) string {
	switch unit {
	case "元":
		return formatAmount(v)
	case "%":
		return fmt.Sprintf("%.2f%%", v)
	case "倍":
		return fmt.Sprintf("%.2f倍", v)
	default:
		return fmt.Sprintf("%.2f", v)
	}
}

// formatAmount 把金额（元）转换为易读的亿/万。
func formatAmount(v float64) string {
	if v == 0 {
		return "0"
	}
	abs := v
	if abs < 0 {
		abs = -abs
	}
	if abs >= 1e8 {
		return fmt.Sprintf("%.2f亿", v/1e8)
	}
	if abs >= 1e4 {
		return fmt.Sprintf("%.2f万", v/1e4)
	}
	return fmt.Sprintf("%.2f元", v)
}

// ParseAIResult 解析大模型返回的 JSON 文本为 AIAnalysisResult。
// 容忍 markdown 代码块包裹、浮点评分（88.0/88.5 → 四舍五入为整数）、
// 维度名写法差异（模糊匹配到规范名并按规范顺序重排）。
//
// selfName 可选（R17）：传入当前公司证券简称，用于在「截取前 3 家」之前排除自身；
// 既有 ParseAIResult(raw) 调用与测试无需修改（缺省为空串，不做自身排除）。
func ParseAIResult(raw string, selfName ...string) (model.AIAnalysisResult, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	var parsed struct {
		Scores []struct {
			Dimension string      `json:"dimension"`
			Score     json.Number `json:"score"`
			Comment   string      `json:"comment"`
		} `json:"scores"`
		Conclusion string            `json:"conclusion"`
		Industry   model.AIIndustry  `json:"industry"`
		Valuation  model.AIValuation `json:"valuation"`

		// R17/D8：RawMessage 容忍 null / 错误类型；未知字段（含 AI 返回的 leaders）自动忽略。
		// businesses 顶层类型异常也不使整体解析失败。
		Businesses json.RawMessage `json:"businesses"`
		Chain      json.RawMessage `json:"chain"`
		Partners   json.RawMessage `json:"partners"`
	}
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return model.AIAnalysisResult{}, fmt.Errorf("解析 AI 返回失败: %w（原始返回：%s）", err, clip(s, 200))
	}

	byDim := make(map[string]model.AIScore)
	for _, sc := range parsed.Scores {
		dim := normalizeDimension(sc.Dimension)
		if dim == "" {
			continue // 无法识别的维度名，忽略
		}
		f, err := sc.Score.Float64()
		if err != nil {
			return model.AIAnalysisResult{}, fmt.Errorf("AI 返回的评分非数值（%s=%s）", sc.Dimension, sc.Score.String())
		}
		score := int(math.Round(f))
		if score < 0 || score > 100 {
			return model.AIAnalysisResult{}, fmt.Errorf("AI 返回的评分超出 0-100 范围（%s=%d）", sc.Dimension, score)
		}
		byDim[dim] = model.AIScore{Dimension: dim, Score: score, Comment: sc.Comment}
	}

	self := ""
	if len(selfName) > 0 {
		self = selfName[0]
	}
	r := model.AIAnalysisResult{
		Conclusion: parsed.Conclusion,
		Industry:   parsed.Industry,
		Businesses: parseBusinesses(parsed.Businesses, self),
		Valuation:  parsed.Valuation,
		Chain:      parseChain(parsed.Chain, self),
		Partners:   sanitizeCompanyRefs(parseCompanyRefs(parsed.Partners), self),
	}
	for _, dim := range aiScoreDimensions {
		sc, ok := byDim[dim]
		if !ok {
			return model.AIAnalysisResult{}, fmt.Errorf("AI 返回缺少评分维度「%s」，请重试", dim)
		}
		r.Scores = append(r.Scores, sc)
	}
	return r, nil
}

// ===================== R17 产业链 / 合作公司解析 =====================

const (
	aiCompanyMax       = 3  // 上游/下游/合作公司各自上限
	aiCompanyNameMax   = 30 // 公司名最大字符数
	aiRelationMaxRunes = 20 // 关系标签最大字符数
	aiNoteMaxRunes     = 30 // 说明最大字符数
)

// parseBusinesses 逐条宽松解析业务板块；顶层不是数组 → nil；单条异常只丢该条。
// 每条 leaders / related 各自复用 parseCompanyRefs + sanitizeCompanyRefs：
// 先排除自身、再去重、再截断到 3 家；两个字段互相独立，不同板块之间也不去重。
func parseBusinesses(raw json.RawMessage, selfName string) []model.AIBusiness {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil // businesses 为对象/字符串等脏类型：整体按空，不影响其余字段
	}
	out := make([]model.AIBusiness, 0, len(items))
	for _, item := range items {
		var b struct {
			Name         string          `json:"name"`
			Stage        string          `json:"stage"`
			Contribution string          `json:"contribution"`
			Prospect     string          `json:"prospect"`
			Risk         string          `json:"risk"`
			Leaders      json.RawMessage `json:"leaders"` // D10：类型异常只影响本板块 leaders
			Related      json.RawMessage `json:"related"` // D8：类型异常只影响本板块 related
		}
		if err := json.Unmarshal(item, &b); err != nil {
			continue // 单条板块字段类型异常：丢该板块，保留其余板块
		}
		if strings.TrimSpace(b.Name) == "" {
			continue // null / 空对象 / 缺板块名：无法展示，直接丢弃
		}
		leaders := sanitizeCompanyRefs(parseCompanyRefs(b.Leaders), selfName)
		related := sanitizeCompanyRefs(parseCompanyRefs(b.Related), selfName)
		if len(leaders) == 0 {
			leaders = nil // omitempty：无细分业务龙头不输出空数组
		}
		if len(related) == 0 {
			related = nil // omitempty：无相关公司不输出空数组
		}
		out = append(out, model.AIBusiness{
			Name:         b.Name,
			Stage:        b.Stage,
			Contribution: b.Contribution,
			Prospect:     b.Prospect,
			Risk:         b.Risk,
			Leaders:      leaders,
			Related:      related,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseChain 解析 chain 对象；整体缺失/类型错误/上下游均空 → nil。
// selfName 透传给 sanitizeCompanyRefs，确保自身在截断前被排除。
func parseChain(raw json.RawMessage, selfName string) *model.AIChain {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var c struct {
		Upstream   json.RawMessage `json:"upstream"`
		Downstream json.RawMessage `json:"downstream"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil
	}
	up := sanitizeCompanyRefs(parseCompanyRefs(c.Upstream), selfName)
	down := sanitizeCompanyRefs(parseCompanyRefs(c.Downstream), selfName)
	if len(up) == 0 && len(down) == 0 {
		return nil
	}
	return &model.AIChain{Upstream: up, Downstream: down}
}

// parseCompanyRefs 逐条宽松解析：顶层不是数组 → nil；单条类型异常只丢该条/该字段。
func parseCompanyRefs(raw json.RawMessage) []model.CompanyRef {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	out := make([]model.CompanyRef, 0, len(items))
	for _, item := range items {
		var m map[string]any
		if err := json.Unmarshal(item, &m); err != nil {
			continue
		}
		out = append(out, model.CompanyRef{
			Name:     stringField(m, "name"),
			Code:     codeField(m, "code"),
			Market:   stringField(m, "market"),
			Relation: stringField(m, "relation"),
			Note:     stringField(m, "note"),
		})
	}
	return out
}

// stringField 取字符串字段；非字符串一律按空处理（脏类型不使整体解析失败）。
func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// codeField 兼容 AI 把代码写成数字；数字 000858 会退化为 858，随后因非 6 位被丢弃（可接受）。
func codeField(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

// sanitizeCompanyRefs 清洗、去重、排除自身、截断、上限。
// selfName 非空时，同名条目在「记入 seen / 计入上限」之前直接丢弃——
// 避免自身占用 3 个名额导致最终只剩 2 家（必须先排除再截断）。
func sanitizeCompanyRefs(in []model.CompanyRef, selfName string) []model.CompanyRef {
	seen := make(map[string]bool, len(in))
	selfKey := NormalizeQuery(selfName)
	out := make([]model.CompanyRef, 0, aiCompanyMax)
	for _, c := range in {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		key := NormalizeQuery(name) // 与 R11 同口径：去全部空白 + 大写
		if key == "" || key == selfKey || seen[key] {
			continue
		}
		code := strings.TrimSpace(c.Code)
		if !isSixDigitCode(code) {
			code = "" // 代码非法只丢字段，不丢整条
		}
		market := strings.TrimSpace(c.Market)
		if market != "非A股" {
			market = "A股" // 缺省/非法一律按 A股
		}
		seen[key] = true
		out = append(out, model.CompanyRef{
			Name:     truncateRunes(name, aiCompanyNameMax),
			Code:     code,
			Market:   market,
			Relation: truncateRunes(strings.TrimSpace(c.Relation), aiRelationMaxRunes),
			Note:     truncateRunes(strings.TrimSpace(c.Note), aiNoteMaxRunes),
		})
		if len(out) == aiCompanyMax {
			break
		}
	}
	return out
}

// truncateRunes 按字符截断并加省略号，保证总长度 ≤ max。
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

// isSixDigitCode 是否为 6 位纯数字股票代码（与 client 同口径，service 包内独立实现）。
func isSixDigitCode(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// IndustryFromReportRows 从已拉取的报表行中找最近一期带行业字段的行（行已按报告期倒序）。
// F10 全量报表当前没有 INDUSTRY_*，返回空，由 client.FetchIndustry 回退。
func IndustryFromReportRows(lists ...[]model.ReportRow) (code, name string) {
	for _, rows := range lists {
		for _, r := range rows {
			if r.IndustryName != "" || r.IndustryCode != "" {
				return r.IndustryCode, r.IndustryName
			}
		}
	}
	return "", ""
}

// ApplyValuationRecommendation 用确定性推导结果覆盖大模型返回的估值参数字段，只保留其 rationale。
// 大模型返回的 model / discount_rate / params 一律丢弃（FR-5：参数以后端确定性结果为准）。
func ApplyValuationRecommendation(r *model.AIAnalysisResult, rec model.ValuationRecommendation) {
	r.Valuation.Model = rec.Model
	r.Valuation.ModelName = rec.ModelName
	r.Valuation.DiscountRate = rec.DiscountRate
	r.Valuation.Params = rec.Params
	r.Valuation.BaseGrowth = rec.BaseGrowth
	r.Valuation.BaseGrowthNote = rec.BaseGrowthNote
	r.Valuation.Volatile = rec.Volatile
	r.Valuation.Warning = rec.Warning
	r.Valuation.AdjustRD = rec.AdjustRD // R8：研发调整建议（确定性回填，大模型即便返回也会被覆盖）
	r.Valuation.RDRatio = rec.RDRatio
	// R13：基期自由现金流推荐（确定性回填，大模型即便返回也会被覆盖）。
	r.Valuation.FCFMode = rec.FCFMode
	r.Valuation.FCFModeName = rec.FCFModeName
	r.Valuation.FCFYears = rec.FCFYears
	r.Valuation.FCFRationale = rec.FCFRationale
	// 非 custom 一律清空自定义值/单位，杜绝大模型脏值泄漏（FR-2 互斥）。
	r.Valuation.FCFCustomValue = 0
	r.Valuation.FCFCustomUnit = ""
	if rec.FCFMode == valFCFCustom && rec.FCFCustomUnit != "" {
		r.Valuation.FCFCustomUnit = rec.FCFCustomUnit
		// 展示值 = 元值 ÷ 单位系数（与前端口径一致，便于 applyAIValuation 直填表单）。
		r.Valuation.FCFCustomValue = rec.FCFCustomYuan / float64(valFCFCustomUnits[rec.FCFCustomUnit])
	}
	// R15/P1：折现率分量 + 行业类型 + 适用性（确定性回填，大模型即便返回也被丢弃）。
	r.Valuation.RiskFreeRate = rec.RiskFreeRate
	r.Valuation.EquityRiskPremium = rec.EquityRiskPremium
	r.Valuation.Beta = rec.Beta
	r.Valuation.BetaTier = rec.BetaTier
	r.Valuation.WaccBaseline = rec.WaccBaseline
	r.Valuation.RiskPremium = rec.RiskPremium
	r.Valuation.Applicable = rec.Applicable // 恒覆盖（大模型若返回也被丢弃）
	r.Valuation.InapplicableReason = rec.InapplicableReason
	// 行业类型：归一化后的 key 覆盖大模型原始值，脏值不外泄（须在 api 侧读取过原始值之后调用）。
	r.Valuation.IndustryType = rec.IndustryType
	r.Valuation.IndustryTypeName = rec.IndustryTypeName
	// R15/P2：基准增速构成、多信号分档与 SGR（确定性回填）。
	r.Valuation.GrowthRevenue = rec.GrowthRevenue
	r.Valuation.GrowthProfit = rec.GrowthProfit
	r.Valuation.GrowthFCF = rec.GrowthFCF
	r.Valuation.GrowthDispersion = rec.GrowthDispersion
	r.Valuation.ModelScore = rec.ModelScore
	r.Valuation.SGR = rec.SGR
	r.Valuation.RetentionRate = rec.RetentionRate
	r.Valuation.ModelDegraded = rec.ModelDegraded
	// R15/P3：FCF 周期识别与研发扩张（确定性回填）。
	r.Valuation.FCFCyclical = rec.FCFCyclical
	r.Valuation.FCFCV = rec.FCFCV
	r.Valuation.FCFSignChanges = rec.FCFSignChanges
	r.Valuation.FCFWindowYears = rec.FCFWindowYears
	r.Valuation.RDExpansion = rec.RDExpansion
	// R15/P4：一致性校验的可观测字段（确定性回填）。
	r.Valuation.BaseYear = rec.BaseYear
	r.Valuation.CapexCoverage = rec.CapexCoverage
	r.Valuation.DeltaNWCRatio = rec.DeltaNWCRatio
	r.Valuation.FCFProfitRatio = rec.FCFProfitRatio
	// R15/P5：成长型 FCF 噪声修复（确定性回填；GrowthFCFExcluded 只在「有增速但未计入」时为 true）。
	r.Valuation.GrowthFCFExcluded = rec.GrowthFCFOK && !rec.GrowthFCFIncluded
	r.Valuation.GrowthFCFExcludeReason = rec.GrowthFCFExcludeReason
	r.Valuation.GrowthDownsideDispersion = rec.GrowthDownsideDispersion
	r.Valuation.FCFCyclicalRaw = rec.FCFRawCyclical
	r.Valuation.FCFGrowthVeto = rec.FCFGrowthVeto
	r.Valuation.GrowthVetoBasis = rec.GrowthVetoBasis
	// R18：三层锚 / 周期位置 / 负增长归因 / 一致预期快照（确定性回填，不接受大模型返回值）。
	r.Valuation.GrowthHistory = rec.GrowthHistory
	// growth_forward 只在可用时输出（不可用时 omitempty 省略，与「0%」区分）。
	r.Valuation.GrowthForward = 0
	if rec.GrowthForwardOK {
		r.Valuation.GrowthForward = rec.GrowthForward
	}
	r.Valuation.GrowthMacro = rec.GrowthMacro
	r.Valuation.GrowthMacroBasis = rec.GrowthMacroBasis
	r.Valuation.GDPAsOf = rec.GDPAsOf
	r.Valuation.GrowthComposite = rec.GrowthComposite
	r.Valuation.GrowthNormalized = rec.GrowthNormalized
	r.Valuation.GrowthCycleAdjust = rec.GrowthCycleAdjust
	r.Valuation.CyclePosition = rec.CyclePosition
	r.Valuation.CyclePositionLabel = rec.CyclePositionLabel
	r.Valuation.CycleBasis = rec.CycleBasis
	r.Valuation.NegativeGrowthType = rec.NegativeGrowthType
	r.Valuation.NegativeGrowthBasis = rec.NegativeGrowthBasis
	r.Valuation.GrowthFadeYears = rec.GrowthFadeYears
	r.Valuation.SGRRole = rec.SGRRole
	r.Valuation.RetentionBasis = rec.RetentionBasis
	r.Valuation.GrowthAdjustment = rec.GrowthAdjustment
	// R15 整合版：三档情景、增长可观测、折现率分步、基期 FCF 可解释。
	// 精度按 20.3.3：率/pp/权重/得分 1~2 位，金额保持元值（计算链仍用未舍入值）。
	r.Valuation.ScenarioMethod = rec.ScenarioMethod
	r.Valuation.ScenarioBandGrowth = round2(rec.ScenarioBandGrowth)
	r.Valuation.ScenarioBandDiscount = round2(rec.ScenarioBandDiscount)
	r.Valuation.ScenarioTerminalAdjust = round2(rec.ScenarioTerminalAdjust)
	r.Valuation.ScenarioBandSource = rec.ScenarioBandSource
	r.Valuation.ScenarioBandRaw = round2(rec.ScenarioBandRaw)
	// Scenarios 整体拷贝，含 FR-38 的 base_fcf / base_fcf_basis（确定性回填，丢弃大模型值）。
	r.Valuation.Scenarios = rec.Scenarios
	r.Valuation.GrowthWeights = rec.GrowthWeights
	r.Valuation.GrowthAnchorCount = rec.GrowthAnchorCount
	r.Valuation.GrowthDamping = round1(rec.GrowthDamping)
	r.Valuation.GrowthCap = round1(rec.GrowthCap)
	r.Valuation.GrowthEnvelope = rec.GrowthEnvelope
	r.Valuation.ScoreGrowth = round2(rec.ScoreGrowth)
	r.Valuation.ScoreStability = round2(rec.ScoreStability)
	r.Valuation.HasStabilityScore = rec.HasStabilityScore
	r.Valuation.ScoreROE = round2(rec.ScoreROE)
	r.Valuation.HasROEScore = rec.HasROEScore
	r.Valuation.GrowthTerminal = round2(rec.GrowthTerminal)
	r.Valuation.EquityCost = round2(rec.EquityCost)
	r.Valuation.DebtWeight = round2(rec.DebtWeight)
	r.Valuation.EquityWeight = round2(rec.EquityWeight)
	r.Valuation.DebtCostPretax = round2(rec.DebtCostPretax)
	r.Valuation.AfterTaxDebtCost = round2(rec.AfterTaxDebtCost)
	r.Valuation.EffectiveTaxRate = round2(rec.EffectiveTaxRate * 100)
	r.Valuation.DebtCostEstimated = rec.DebtCostEstimated
	r.Valuation.FCFSeries = rec.FCFSeries
	r.Valuation.FCFBaseValue = rec.FCFBaseValue
	r.Valuation.FCFFormulaNote = rec.FCFFormulaNote
	r.Valuation.FCFWindowStart = rec.FCFWindowStart
	r.Valuation.FCFWindowEnd = rec.FCFWindowEnd
	// 一致预期未取到/不可用时清空快照字段，避免旧缓存或大模型脏值透传。
	if rec.ConsensusFetched {
		r.Valuation.ConsensusAsOf = rec.ConsensusAsOf
		r.Valuation.ConsensusInstitutions = rec.ConsensusInstitutions
		r.Valuation.ConsensusYears = rec.ConsensusYears
		r.Valuation.ConsensusReliability = rec.ConsensusReliability
	} else {
		r.Valuation.ConsensusAsOf = ""
		r.Valuation.ConsensusInstitutions = 0
		r.Valuation.ConsensusYears = 0
		r.Valuation.ConsensusReliability = ""
	}
	// Rationale 保持大模型返回值，不覆盖
}

// normalizeDimension 把大模型返回的维度名模糊匹配到规范名，无法识别返回空串。
func normalizeDimension(name string) string {
	n := strings.TrimSpace(name)
	switch {
	case strings.Contains(n, "盈利"):
		return "盈利能力"
	case strings.Contains(n, "偿债"):
		return "偿债能力"
	case strings.Contains(n, "现金") || strings.Contains(n, "获现"):
		return "现金获取能力"
	case strings.Contains(n, "效率") || strings.Contains(n, "营运") || strings.Contains(n, "运营") || strings.Contains(n, "经营"):
		return "经营效率"
	case strings.Contains(n, "成长"):
		return "成长能力"
	}
	return ""
}

// clip 截断过长的文本，避免把整段响应塞进错误提示。
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
