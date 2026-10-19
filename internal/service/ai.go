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
  "valuation": {"industry_type": "cyclical|general|utility", "company_risks": [{"type": "governance|litigation|customer_concentration|liquidity|accounting_quality", "severity": "medium|high", "note": "一句话说明(≤30字，不含数字)"}], "rationale": "一句话定性理由(≤3句，不含任何数字或百分号)"}
}

说明：scores 必须恰好 5 项，维度名只能用「盈利能力、偿债能力、现金获取能力、经营效率、成长能力」这 5 个精确写法；score 为 0-100 的整数。

## 五个评分维度（百分制，0-100 整数）
评分结合行业特性（不同行业标杆不同），主要依据最近一个年报、辅以多年趋势。评分区间参考：90+ 优秀 / 75-89 良好 / 60-74 一般 / 45-59 偏弱 / <45 较差。

1. 盈利能力：看毛利率、营业成本率、总费用率、息税前经营利润率、息前税后营业收入利润率、股东权益回报率(ROE)、息税前资产回报率、息税前经营资产回报率、净利润、股权价值增加值。
2. 偿债能力：看财务杠杆倍数、债务对股东权益比率、利息保障倍数、有息债务占比、股权占比、财务成本负担率、债务资本成本率、长期/短期融资净额。（有息负债低、利息覆盖高、财务负担轻 → 高分）
3. 现金获取能力：看经营活动现金流量净额、营业收入现金含量、成本费用付现率、息前税后经营利润现金含量、净利润现金含量、现金自给率。（利润含金量高、现金自给 → 高分）
4. 经营效率：看资产周转率、经营资产周转率、长期经营资产/固定资产/周转性经营投入周转率、存货/应收账款/应付账款周转率与周转天数、营业周期、现金周期。
5. 成长能力：看营业收入、净利润、经营活动现金流量净额的同比增速，长期经营资产扩张性资本支出及比例、战略投资活动总体规模扩张、并购活动净合并额、研发费用率趋势。

## 估值参数推荐（只做定性判断）
- 估值参数（模型、基期 FCF、折现率、增长率与年数）已由系统按实际财报数据**确定性计算**，用户消息中会给出事实块，只读、不得改动。
- 你只需输出三件事：industry_type（三选一 ASCII key）、company_risks（非财务风险项数组）、rationale（定性理由）。
- industry_type：依据主营构成与行业知识判定，cyclical（钢铁/煤炭/化工/航运/养殖/券商/有色等随宏观或商品周期大幅波动的行业）、general（消费/制造/医药/科技等多数行业）、utility（电力/燃气/水务/高速公路/港口等需求与现金流稳定、受价格管制的行业）；不确定时输出 general，不得输出其它取值。
- company_risks：只列**非财务**风险，可用 type 仅 governance（治理结构）、litigation（诉讼与合规）、customer_concentration（客户集中）、liquidity（流动性）、accounting_quality（会计质量）；severity 仅 medium 或 high。**不得**把杠杆、偿债能力、利息保障倍数、资产负债率、盈利能力、估值高低等财务维度写入；同一 type 只输出一次；无把握或无非财务风险时输出空数组 []。
- note 为一句定性说明，≤30 字，**不得包含任何阿拉伯数字或百分号**；无把握可留空。
- rationale：≤3 句、≤180 字，只做定性解释；**不得出现任何阿拉伯数字或百分号**，不得建议其它模型、折现率、增长率、基期 FCF 或目标价；不得出现「后端、确定性、系统校验、锚、软上限、包络、三档、保守、乐观、基准档、带宽」等过程或内部用语。
- 系统收到你的 industry_type 与 company_risks 后重算全部参数；rationale 中若出现数字或过程话术，整句会被丢弃。

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

// valuationPromptBlock 把确定性推导结果压缩为只读事实块（大模型只据此写定性理由）。
func valuationPromptBlock(rec model.ValuationRecommendation) string {
	var b strings.Builder
	b.WriteString("【估值参数事实（只读，不得改动）】\n")
	b.WriteString(fmt.Sprintf("- 模型：%s（%s）\n", rec.ModelName, rec.ModelReason))

	head := fmt.Sprintf("%.2f 亿元；%s", rec.FCFBaseValue/1e8, fcfWindowText(rec))
	if rec.AdjustRD && rec.RDExpansion > 0 {
		head += fmt.Sprintf("；已加回研发扩张 %.2f 亿元", rec.RDExpansion/1e8)
	}
	b.WriteString("- 基期 FCF：" + head + "\n")

	line := fmt.Sprintf("- 折现率：%.1f%%；Re %.2f%%；", rec.DiscountRate, rec.EquityCost)
	if rec.DebtWeight > 0 {
		line += fmt.Sprintf("Rd（税后）%.2f%%；E/V %.2f%%；D/V %.2f%%",
			rec.AfterTaxDebtCost, rec.EquityWeight*100, rec.DebtWeight*100)
	} else {
		line += "无有息债务；E/V 100%"
	}
	b.WriteString(line + "\n")

	b.WriteString("- 增长参数：" + growthParamsText(rec) + "\n")

	checks := make([]string, 0, 2)
	if rec.CapexCheck.Available {
		checks = append(checks, fmt.Sprintf("净资本支出/保全性资本支出 %.2f（%d–%d）",
			rec.CapexCheck.Ratio, rec.CapexCheck.WindowStart, rec.CapexCheck.WindowEnd))
	}
	if rec.FCFProfitCheck.Available {
		checks = append(checks, fmt.Sprintf("FCF/净利润（重构口径，含少数股东损益） %.2f（%d–%d）",
			rec.FCFProfitCheck.Ratio, rec.FCFProfitCheck.WindowStart, rec.FCFProfitCheck.WindowEnd))
	}
	if len(checks) > 0 {
		b.WriteString("- 关键校验：" + strings.Join(checks, "；") + "\n")
	}

	b.WriteString("- 行业分类：请判定并输出 cyclical / general / utility\n")
	b.WriteString("- 公司特定风险：如无可列示的非财务风险，输出空数组\n")
	if !rec.Applicable {
		b.WriteString("- 现金流贴现法不适用：" + rec.InapplicableReason + "\n")
	}
	return b.String()
}

// fcfWindowText 基期 FCF 窗口的提示词文案（与展示口径一致）。
func fcfWindowText(rec model.ValuationRecommendation) string {
	if rec.FCFMode == valFCFCustom {
		return "自定义基期值"
	}
	if rec.FCFMode != valFCFLatest && rec.FCFWindowStart > 0 && rec.FCFWindowEnd > 0 {
		return fmt.Sprintf("近 %d 年%s（%d–%d）", rec.FCFYears, rec.FCFModeName,
			rec.FCFWindowStart, rec.FCFWindowEnd)
	}
	if rec.FCFWindowEnd > 0 {
		return fmt.Sprintf("最近一年（%d）", rec.FCFWindowEnd)
	}
	return rec.FCFModeName
}

// growthParamsText 增长参数事实：A、g1/n1、g2/n2、g∞ 按模型裁剪。
func growthParamsText(rec model.ValuationRecommendation) string {
	parts := []string{fmt.Sprintf("A %.1f%%", rec.GrowthAnchor.Final)}
	switch rec.Model {
	case valModelThreeStage:
		parts = append(parts,
			fmt.Sprintf("g1 %.1f%%", paramValueOf(rec.Params, "g1")),
			fmt.Sprintf("n1 %.0f 年", paramValueOf(rec.Params, "n1")),
			fmt.Sprintf("g2 %.1f%%", paramValueOf(rec.Params, "g2")),
			fmt.Sprintf("n2 %.0f 年", paramValueOf(rec.Params, "n2")))
	case valModelTwoStage:
		parts = append(parts,
			fmt.Sprintf("g1 %.1f%%", paramValueOf(rec.Params, "g1")),
			fmt.Sprintf("n1 %.0f 年", paramValueOf(rec.Params, "n1")),
			fmt.Sprintf("g2 %.1f%%", paramValueOf(rec.Params, "g2")))
	case valModelPerpetual:
		parts = append(parts, fmt.Sprintf("g %.1f%%", paramValueOf(rec.Params, "g")))
	}
	if rec.Model != valModelZero {
		parts = append(parts, fmt.Sprintf("g∞ %.1f%%", rec.GrowthTerminal))
	}
	return strings.Join(parts, "；")
}

// paramValue 从参数表取 key 对应值（缺失按 0）。
func paramValueOf(params []model.AIValuationParam, key string) float64 {
	for _, p := range params {
		if p.Key == key {
			return p.Value
		}
	}
	return 0
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
		Conclusion string           `json:"conclusion"`
		Industry   model.AIIndustry `json:"industry"`

		// R19：valuation 内部脏类型（company_risks 非数组等）不应使整体解析失败。
		Valuation json.RawMessage `json:"valuation"`

		// R17/D8：RawMessage 容忍 null / 错误类型；未知字段（含 AI 返回的 leaders）自动忽略。
		// businesses 顶层类型异常也不使整体解析失败。
		Businesses json.RawMessage `json:"businesses"`
		Chain      json.RawMessage `json:"chain"`
		Partners   json.RawMessage `json:"partners"`
	}
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return model.AIAnalysisResult{}, fmt.Errorf("解析 AI 返回失败: %w（原始返回：%s）", err, clip(s, 200))
	}
	valuation := parseAIValuation(parsed.Valuation)

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
		Valuation:  valuation,
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

// parseAIValuation 宽松解析 valuation：缺失/null/非对象 → 空结构；
// company_risks 缺失、null 或类型错误 → 空数组，不影响其余字段。
func parseAIValuation(raw json.RawMessage) model.AIValuation {
	var out model.AIValuation
	if len(raw) == 0 || string(raw) == "null" {
		return out
	}
	var v struct {
		IndustryType string          `json:"industry_type"`
		CompanyRisks json.RawMessage `json:"company_risks"`
		Rationale    string          `json:"rationale"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return out // valuation 不是对象（数组/字符串等）：整块按空
	}
	out.IndustryType = v.IndustryType
	out.CompanyRisks = parseCompanyRisks(v.CompanyRisks)
	out.Rationale = sanitizeRationale(v.Rationale)
	return out
}

// parseCompanyRisks 逐条宽松解析 AI 风险项；只取 type/severity/note，其余字段（label/premium）忽略。
func parseCompanyRisks(raw json.RawMessage) []model.CompanyRiskItem {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil // 非数组：按空处理
	}
	out := make([]model.CompanyRiskItem, 0, len(items))
	for _, item := range items {
		var it struct {
			Type     string `json:"type"`
			Severity string `json:"severity"`
			Note     string `json:"note"`
		}
		if err := json.Unmarshal(item, &it); err != nil {
			continue // 单条脏类型只丢该条
		}
		out = append(out, model.CompanyRiskItem{
			Type: it.Type, Severity: it.Severity, Note: it.Note,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// rationaleBannedPhrases 含过程/内部话术的句子整句丢弃。
var rationaleBannedPhrases = []string{
	"后端", "确定性", "不改数", "不得改写", "系统校验", "锚数", "A_raw",
	"软上限", "包络", "离散度折减", "回退", "退化", "三档", "保守",
	"乐观", "基准档", "带宽", "收窄", "不与基准档", "上限 40", "下限 7",
}

// sanitizeRationale 清洗 AI 的定性理由：按句切分，含数字/百分号/过程话术的句子整句丢弃，
// 最多保留 3 句、总长 ≤180 字。全部被丢弃时返回空串（由调用方按模型补默认句）。
func sanitizeRationale(s string) string {
	text := strings.TrimSpace(s)
	if text == "" {
		return ""
	}
	parts := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '。', '！', '？', '!', '?', '\n':
			return true
		}
		return false
	})
	kept := make([]string, 0, 3)
	total := 0
	for _, part := range parts {
		sentence := strings.TrimSpace(part)
		if sentence == "" {
			continue
		}
		if containsDigitOrPercent(sentence) {
			continue
		}
		banned := false
		for _, b := range rationaleBannedPhrases {
			if strings.Contains(sentence, b) {
				banned = true
				break
			}
		}
		if banned {
			continue
		}
		r := []rune(sentence)
		if len(r) == 0 {
			continue
		}
		if total+len(r) > 180 {
			if total == 0 {
				sentence = string(r[:180])
			} else {
				break
			}
		}
		kept = append(kept, sentence+"。")
		total += utf8.RuneCountInString(sentence) + 1
		if len(kept) == 3 {
			break
		}
	}
	return strings.Join(kept, "")
}

// containsDigitOrPercent 是否含阿拉伯数字或百分号（数字一律交给后端，AI 不得复述）。
func containsDigitOrPercent(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' || r == '%' || r == '％' {
			return true
		}
	}
	return false
}

// rationaleFallback 按模型给出不含数字的默认定性理由。
func rationaleFallback(modelKey string) string {
	switch modelKey {
	case valModelZero:
		return "当前增长与现金流不支持高增长假设，采用零增长模型。"
	case valModelPerpetual:
		return "公司增长已接近长期水平，采用永续增长模型。"
	case valModelThreeStage:
		return "公司增长阶段分层明显，采用三阶段模型。"
	default:
		return "公司仍有一段高于长期水平的增长期，采用两阶段模型。"
	}
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

// ApplyValuationRecommendation 用确定性推导结果整体覆盖估值参数，只保留清洗后的定性 rationale。
// 大模型返回的 model / discount_rate / params / company_risks 一律丢弃（参数以后端确定性结果为准）。
func ApplyValuationRecommendation(r *model.AIAnalysisResult, rec model.ValuationRecommendation) {
	params := rec.Params
	if params == nil {
		params = []model.AIValuationParam{} // 零增长模型输出空数组而非 null
	}
	riskItems := rec.CompanyRiskItems
	if riskItems == nil {
		riskItems = []model.CompanyRiskItem{}
	}

	v := model.AIValuation{
		// 一、模型
		Model: rec.Model, ModelName: rec.ModelName, ModelReason: rec.ModelReason,
		Applicable: rec.Applicable, InapplicableReason: rec.InapplicableReason,
		Params: params,

		// 二、基期 FCF（金额保持元值；展示层按亿元格式化）
		FCFBaseValue: round2(rec.FCFBaseValue), FCFBaseBeforeRD: round2(rec.FCFBaseBeforeRD),
		FCFMode: rec.FCFMode, FCFModeName: rec.FCFModeName, FCFYears: rec.FCFYears,
		FCFWindowStart: rec.FCFWindowStart, FCFWindowEnd: rec.FCFWindowEnd,
		AdjustRD: rec.AdjustRD, RDExpansion: round2(rec.RDExpansion), RDRatio: rec.RDRatio,
		FCFNote: rec.FCFNote, FCFSeries: rec.FCFSeries,

		// 三、折现率
		DiscountRate: round1(rec.DiscountRate),
		RiskFreeRate: valRiskFreeRate, EquityRiskPremium: valEquityRiskPrem,
		Beta: rec.Beta, BetaBasis: rec.BetaBasis,
		IndustryType: rec.IndustryType, IndustryTypeName: rec.IndustryTypeName,
		CompanyRiskPremium: round2(rec.CompanyRiskPremium), CompanyRiskItems: riskItems,
		CompanyRiskNote: rec.CompanyRiskNote,
		EquityCost:      round2(rec.EquityCost),
		EquityValue:     round2(rec.EquityValue), DebtValue: round2(rec.DebtValue),
		EquitySource: rec.EquitySource, EquitySourceNote: rec.EquitySourceNote,
		EquityWeight: rec.EquityWeight, DebtWeight: rec.DebtWeight, WeightNote: rec.WeightNote,
		DebtCostPretax: round2(rec.DebtCostPretax), AfterTaxDebtCost: round2(rec.AfterTaxDebtCost),
		EffectiveTaxRate: round2(rec.EffectiveTaxRate * 100), DebtCostEstimated: rec.DebtCostEstimated,
		DiscountBaseValue: round2(rec.DiscountBaseValue), DiscountBaseLabel: rec.DiscountBaseLabel,
		DiscountRounded:            round2(rec.DiscountRounded),
		DiscountRoundingAdjustment: round2(rec.DiscountRoundingAdjustment),
		DiscountRangeAdjustment:    round2(rec.DiscountRangeAdjustment),
		DiscountReconcileAvailable: rec.DiscountReconcileAvailable,
		DiscountNote:               rec.DiscountNote,

		// 四、增长参数
		GrowthAnchor:          rec.GrowthAnchor,
		GrowthHistoryBase:     round2(rec.GrowthHistoryBase),
		GrowthHistoryAdjusted: round2(rec.GrowthHistoryAdjusted),
		GrowthCycleAdjust:     round1(rec.GrowthCycleAdjust), GrowthCycleNote: rec.GrowthCycleNote,
		GrowthForward: round2(rec.GrowthForward), GrowthForwardAvailable: rec.GrowthForwardOK,
		GrowthMacro: round2(rec.GrowthMacro), GrowthMacroBasis: rec.GrowthMacroBasis,
		GDPAsOf:       rec.GDPAsOf,
		ConsensusAsOf: rec.ConsensusAsOf, ConsensusInstitutions: rec.ConsensusInstitutions,
		ConsensusYears: rec.ConsensusYears, ConsensusReliability: rec.ConsensusReliability,
		GrowthTerminal:     round1(rec.GrowthTerminal),
		TerminalStability:  round2(rec.TerminalStability),
		TerminalConstraint: rec.TerminalConstraint,
		TerminalNote:       rec.TerminalNote,
		SGR:                round2(rec.SGR), RetentionRate: round4(rec.RetentionRate),
		RetentionBasis: rec.RetentionBasis, SGRWindowYears: rec.SGRWindowYears,
		NegativeGrowthType: rec.NegativeGrowthType, NegativeGrowthNote: rec.NegativeGrowthNote,
		G1Constraint: rec.G1Constraint, G1Note: rec.G1Note,

		// 五、关键校验
		CapexCheck: rec.CapexCheck, FCFProfitCheck: rec.FCFProfitCheck,

		// 六、结论与材料性警告
		BaseYear: rec.BaseYear, Warnings: rec.Warnings,

		// 大模型仅可提供定性理由；输入通道清空，不出现在最终响应。
		CompanyRisks: nil,
	}

	// custom 展示值 = 元值 ÷ 单位系数；非 custom 恒为空，杜绝脏值泄漏。
	if rec.FCFMode == valFCFCustom && rec.FCFCustomUnit != "" {
		if factor, ok := valFCFCustomUnits[rec.FCFCustomUnit]; ok {
			v.FCFCustomUnit = rec.FCFCustomUnit
			v.FCFCustomValue = round2(rec.FCFCustomYuan / float64(factor))
		}
	}

	// rationale 二次清洗（大模型可能未走解析入口）；全部丢弃时按模型补默认句。
	rationale := sanitizeRationale(r.Valuation.Rationale)
	if rationale == "" {
		rationale = rationaleFallback(rec.Model)
	}
	v.Rationale = rationale

	r.Valuation = v
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
