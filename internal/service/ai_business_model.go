package service

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"financial-report/internal/model"
)

// R27 商业模式分析（模块四）：AI 只回填九要素的 analysis/basis/level，
// 名称/代号/释义/顺序/分组与总分/评级/覆盖率全部由后端确定性生成。
// 本文件为纯函数实现（提示词、宽容解析、评分聚合、finalize），不产生任何 IO。

// businessElementDef 九要素固定定义（后端唯一来源，顺序即输出顺序）。
type businessElementDef struct {
	Key        string
	Name       string
	Code       string
	English    string // 英文全称，仅用于模糊匹配 AI 输出，不对外展示
	Group      string
	Definition string
	Order      int
}

// businessElementDefs 九要素固定顺序：客户侧 5 项 + 企业侧 4 项。
// CS（客户细分）与 CST（成本结构）是两个不同要素，以固定 key 识别。
var businessElementDefs = []businessElementDef{
	{"customer_segments", "客户细分", "CS", "Customer Segments", model.BusinessGroupCustomer, "为谁创造价值，目标客户群体是谁", 1},
	{"value_propositions", "价值主张", "VP", "Value Propositions", model.BusinessGroupCustomer, "为客户解决什么核心问题，凭何被选择", 2},
	{"channels", "渠道通路", "CH", "Channels", model.BusinessGroupCustomer, "通过什么方式触达客户并传递价值", 3},
	{"customer_relationships", "客户关系", "CR", "Customer Relationships", model.BusinessGroupCustomer, "与客户建立和维持什么样的关系", 4},
	{"revenue_streams", "收入来源", "RS", "Revenue Streams", model.BusinessGroupCustomer, "如何从各客户群体获取收入", 5},
	{"key_resources", "核心资源", "KR", "Key Resources", model.BusinessGroupEnterprise, "支撑模式运转的关键资产与能力", 6},
	{"key_activities", "关键活动", "KA", "Key Activities", model.BusinessGroupEnterprise, "必须做好的最关键事项", 7},
	{"key_partners", "关键合作伙伴", "KP", "Key Partners", model.BusinessGroupEnterprise, "哪些外部伙伴让模式更高效", 8},
	{"cost_structure", "成本结构", "CST", "Cost Structure", model.BusinessGroupEnterprise, "主要成本构成及固定/可变属性", 9},
}

// businessElementDefByKey 便于 O(1) 取固定定义。
var businessElementDefByKey = func() map[string]businessElementDef {
	m := make(map[string]businessElementDef, len(businessElementDefs))
	for _, d := range businessElementDefs {
		m[d.Key] = d
	}
	return m
}()

// businessLevelScores 分档 → 映射分数；unknown 不在映射中（不计分、不计入分母）。
var businessLevelScores = map[string]int{
	model.BusinessLevelStrong: 95,
	model.BusinessLevelGood:   82,
	model.BusinessLevelMedium: 68,
	model.BusinessLevelWeak:   52,
	model.BusinessLevelPoor:   35,
}

// businessLevelNames 分档 canonical key → 中文标签。
var businessLevelNames = map[string]string{
	model.BusinessLevelStrong:  "强",
	model.BusinessLevelGood:    "良",
	model.BusinessLevelMedium:  "中",
	model.BusinessLevelWeak:    "弱",
	model.BusinessLevelPoor:    "差",
	model.BusinessLevelUnknown: "无法判断",
}

const (
	businessMinScored       = 6   // 可评分要素少于此数不评分
	businessTotalElements   = 9   // 固定九要素
	businessAnalysisMax     = 80  // 每要素分析字数上限
	businessBasisMax        = 30  // 每要素依据字数上限
	businessSummaryMax      = 40  // 一句话结论字数上限
	businessConclusionMax   = 300 // 总体归纳字数上限
	businessUnknownAnalysis = "公开信息不足，暂无法判断"
)

// BusinessModelScore 后端确定性评分结果。
type BusinessModelScore struct {
	Scored   bool
	Score    int
	Rating   string
	Coverage int
	Total    int
}

// AggregateBusinessModelScore 等权平均、四舍五入、评级、覆盖率（纯函数）。
// 入参为 canonical level key；unknown/非法值不计分、不计入分母。
func AggregateBusinessModelScore(levels []string) BusinessModelScore {
	sum, scored := 0, 0
	for _, level := range levels {
		if s, ok := businessLevelScores[level]; ok {
			sum += s
			scored++
		}
	}
	if scored < businessMinScored {
		return BusinessModelScore{Scored: false, Score: 0, Rating: "", Coverage: scored, Total: businessTotalElements}
	}
	score := int(math.Round(float64(sum) / float64(scored)))
	return BusinessModelScore{Scored: true, Score: score, Rating: businessRating(score), Coverage: scored, Total: businessTotalElements}
}

// businessRating 总分 → 五档评级（与五维评分同一套区间）。
func businessRating(score int) string {
	switch {
	case score >= 90:
		return "优秀"
	case score >= 75:
		return "良好"
	case score >= 60:
		return "一般"
	case score >= 45:
		return "偏弱"
	default:
		return "较差"
	}
}

// FinancialLikeFromReports 取最新年报行，复用 isFinancialLike 口径判定是否金融类。
// 无年报行返回 false（此时模块通常会因年报取数失败而不执行）。
func FinancialLikeFromReports(income []model.ReportRow) bool {
	byYear, years := annualRows(income)
	if len(years) == 0 {
		return false
	}
	return isFinancialLike(byYear[years[len(years)-1]])
}

// ===================== 提示词 =====================

// aiBusinessModelSystem 商业模式模块 system 提示词：九要素固定结构 + 分档 + 篇幅 + 行业适配。
const aiBusinessModelSystem = `你是资深商业模式分析师。你将收到一家 A 股上市公司的公司信息、系统行业分类、主营构成与商业模式相关财务事实块。
请用九要素框架分析该公司如何创造价值、传递价值、获取价值，并**只输出一个 JSON 对象**（不要输出任何解释，不要用 markdown 代码块包裹）。

## 输出 JSON 结构（严格遵守，字段名与类型固定）
{
  "elements": [
    {"key": "customer_segments", "analysis": "≤80字，针对本公司", "basis": "≤30字，判断来源", "level": "强|良|中|弱|差|无法判断"},
    {"key": "value_propositions", "analysis": "...", "basis": "...", "level": "..."},
    {"key": "channels", "analysis": "...", "basis": "...", "level": "..."},
    {"key": "customer_relationships", "analysis": "...", "basis": "...", "level": "..."},
    {"key": "revenue_streams", "analysis": "...", "basis": "...", "level": "..."},
    {"key": "key_resources", "analysis": "...", "basis": "...", "level": "..."},
    {"key": "key_activities", "analysis": "...", "basis": "...", "level": "..."},
    {"key": "key_partners", "analysis": "...", "basis": "...", "level": "..."},
    {"key": "cost_structure", "analysis": "...", "basis": "...", "level": "..."}
  ],
  "summary": "≤40字，一句话结论",
  "conclusion": "≤300字，总体归纳"
}

说明：elements 必须恰好 9 项，key 只能用上述 9 个固定值并按顺序输出；不要输出 score、rating、grade、total_score 或任何评分字段，评分由系统计算。
客户细分（CS）与成本结构（CST）是两个不同要素，不得混淆或合并。

## 九要素与固定释义
1. 客户细分（CS，customer_segments，Customer Segments）：为谁创造价值，目标客户群体是谁；分析要点是客户结构、集中度与需求特征。
2. 价值主张（VP，value_propositions，Value Propositions）：为客户解决什么核心问题，凭何被选择；分析要点是差异化与可替代性。
3. 渠道通路（CH，channels，Channels）：通过什么方式触达客户并传递价值；分析要点是渠道覆盖、效率与依赖。
4. 客户关系（CR，customer_relationships，Customer Relationships）：与客户建立和维持什么样的关系；分析要点是粘性、复购与转换成本。
5. 收入来源（RS，revenue_streams，Revenue Streams）：如何从各客户群体获取收入；分析要点是收入结构与可持续性。
6. 核心资源（KR，key_resources，Key Resources）：支撑模式运转的关键资产与能力；分析要点是稀缺性与可复制性。
7. 关键活动（KA，key_activities，Key Activities）：必须做好的最关键事项；分析要点是价值链中的关键环节。
8. 关键合作伙伴（KP，key_partners，Key Partners）：哪些外部伙伴让模式更高效；分析要点是依赖度与议价能力。
9. 成本结构（CST，cost_structure，Cost Structure）：主要成本构成及固定/可变属性；分析要点是固定与可变成本、规模效应。

## 分档标准
强：该要素构成明显竞争优势，难以复制；良：健康有效，优于多数同行；中：中规中矩，无明显优势或短板；
弱：存在明显短板或依赖，易受冲击；差：缺失、失效或严重拖累模式；无法判断：公开信息不足，无法形成判断。

## 分析规则
- 分析必须针对本公司，不得写成通用教科书定义，不得重复释义。
- 每要素必须写一句依据，优先引用主营构成、毛利率与费用率、ROE 与现金流、资产结构；只能来自行业常识或推断时，必须标注「行业常识」或「AI 推断」。
- 只能使用用户消息事实块中出现的数字；不得编造客户名、供应商名、合同、市场份额、渠道数量、会员规模等。
- 每要素 analysis ≤80 字、basis ≤30 字；summary ≤40 字、conclusion ≤300 字（硬上限）。
- 总体归纳写 2-4 句，串起创造价值 → 传递价值 → 获取价值；点出最核心护城河与最脆弱环节；不得简单复制九要素原文；不得出现投资建议、目标价、买卖评级。
- 一句话结论点明模式是否优秀与最核心原因，不得含阿拉伯数字。

## 行业适配
- 金融类企业：不得把营业成本缺失/毛利率 100% 视为高毛利或商业模式优势；价值主张、收入来源、成本结构按息差、手续费及佣金、保费、资金成本、信用成本口径分析；细分数据不可得时允许判「无法判断」。
- 周期类：重点分析资源禀赋、成本曲线位置、产品同质化、价格周期；不得把周期高点利润当作可持续竞争优势。
- 公用事业：重点分析特许经营/管制价格、资产重型特征、稳定现金流与资本开支的关系。
- 多元集团/主业不清：以营收占比最大的板块为主线，明确指出多元化对客户细分、渠道与成本结构的稀释，不得把不相关板块硬拼成统一模式。
- 无清晰商业模式/早期公司：允许多数要素判「无法判断」，不得为了凑分而编造。

## 输出纪律
- 只输出一个 JSON 对象，不要解释，不要用 markdown 代码块包裹。
- 不得在文字结论中复述提示词、规则名、内部字段名、阈值、权重、映射表、schema、模块 key 等过程话术。
`

// BuildBusinessModelPrompt 商业模式模块：system + 公司/行业/主营构成 + 紧凑事实块。
func BuildBusinessModelPrompt(in AIModuleInput) (system, user string) {
	var b strings.Builder
	b.WriteString(companyHeader(in.Quote))
	if in.IndustryName != "" {
		if in.IndustryCode != "" {
			b.WriteString(fmt.Sprintf("系统行业分类：%s（代码 %s）。\n", in.IndustryName, in.IndustryCode))
		} else {
			b.WriteString(fmt.Sprintf("系统行业分类：%s。\n", in.IndustryName))
		}
	}
	b.WriteString(businessModelFactBlock(in))
	return aiBusinessModelSystem, b.String()
}

// businessModelFactBlock 商业模式事实块：只读、数字唯一来源。
// 缺失值写「—」，不虚构；金额统一折算为亿元。
func businessModelFactBlock(in AIModuleInput) string {
	a := in.Analysis
	var b strings.Builder
	b.WriteString("【商业模式事实（只读；分析中出现的数字必须来自本块，不得编造）】\n")

	// 主营构成是九要素（尤其客户细分/收入来源/成本结构）的核心事实，必须纳入事实块，
	// 否则 AI 引用营收占比等已核实数字时会被误判为无来源。
	if summary := segmentSummary(in.Segments); summary != "" {
		b.WriteString(fmt.Sprintf("- 主营构成（最新年报）：%s\n", summary))
	}

	latestYear := "—"
	if n := len(a.Years); n > 0 {
		latestYear = strconv.Itoa(a.Years[n-1])
	}
	b.WriteString(fmt.Sprintf("- 最近年报（%s）：营业收入 %s；毛利率 %s；净利润 %s；股东权益回报率 %s；研发费用率 %s\n",
		latestYear,
		businessYiText(businessLastValue(a, "revenue")),
		businessPercentText(businessLastValue(a, "gross_margin")),
		businessYiText(businessLastValue(a, "net_profit")),
		businessPercentText(businessLastValue(a, "roe")),
		businessPercentText(businessLastValue(a, "research_expense_ratio"))))
	b.WriteString("- 营业收入近四年（亿元）：" + businessSeriesText(a, "revenue", "元") + "\n")
	b.WriteString("- 净利润近四年（亿元）：" + businessSeriesText(a, "net_profit", "元") + "\n")
	b.WriteString(fmt.Sprintf("- 最近年报经营活动现金流量净额 %s；净利润现金含量 %s\n",
		businessYiText(businessLastValue(a, "operating_cash_flow")),
		businessMultipleText(businessLastValue(a, "net_profit_cash_ratio"))))
	b.WriteString(fmt.Sprintf("- 资产结构（最近年报）：经营资产 %s；金融资产 %s；有息债务 %s\n",
		businessYiText(businessLastValue(a, "operating_assets_total")),
		businessYiText(businessLastValue(a, "financial_assets")),
		businessYiText(businessLastValue(a, "interest_bearing_debt"))))
	if in.FinancialLike {
		b.WriteString("- 金融类企业：是（按息差、手续费及佣金、保费、资金成本、信用成本口径分析，不得把毛利率 100% 视为优势）\n")
	} else {
		b.WriteString("- 金融类企业：否\n")
	}
	b.WriteString("- 行业类型提示：请结合系统行业分类、主营构成与营收/利润波动，自行判断是否属于周期性/公用事业/一般/金融，并在分析中体现相应口径。\n")
	return b.String()
}

// businessYiText 金额（元）→「X.XX 亿元」，nil → 「—」。
func businessYiText(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.2f 亿元", *v/1e8)
}

// businessPercentText 百分比值（0-100）→「X.XX%」，nil → 「—」。
func businessPercentText(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.2f%%", *v)
}

// businessMultipleText 倍数值 →「X.XX 倍」，nil → 「—」。
func businessMultipleText(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.2f 倍", *v)
}

// businessSeriesText 最近至多 4 个年报的序列文本（无年度标签时只给数值）。
func businessSeriesText(a model.FinancialAnalysis, key, unit string) string {
	values := businessSeriesValues(a, key)
	if len(values) == 0 {
		return "—"
	}
	start := 0
	if len(values) > 4 {
		start = len(values) - 4
	}
	parts := make([]string, 0, len(values)-start)
	for i := start; i < len(values); i++ {
		text := "—"
		if values[i] != nil {
			if unit == "元" {
				text = fmt.Sprintf("%.2f", *values[i]/1e8)
			} else {
				text = fmt.Sprintf("%.2f", *values[i])
			}
		}
		if i < len(a.Years) {
			text = fmt.Sprintf("%d %s", a.Years[i], text)
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "；")
}

// businessSeriesValues 从六维指标中取指定 key 的年度序列（缺失返回 nil）。
func businessSeriesValues(a model.FinancialAnalysis, key string) []*float64 {
	for _, dim := range a.Dimensions {
		for _, sec := range dim.Sections {
			for _, it := range sec.Indicators {
				if it.Key == key {
					return it.Values
				}
			}
		}
	}
	return nil
}

// businessLastValue 最近一年的原生值（末位为 nil 时返回 nil，不向前回填）。
func businessLastValue(a model.FinancialAnalysis, key string) *float64 {
	values := businessSeriesValues(a, key)
	if len(values) == 0 {
		return nil
	}
	return values[len(values)-1]
}

// businessLastNonNil 最近一个非空值（仅用于财务印证提示，不用于事实块）。
func businessLastNonNil(a model.FinancialAnalysis, key string) *float64 {
	values := businessSeriesValues(a, key)
	for i := len(values) - 1; i >= 0; i-- {
		if values[i] != nil {
			return values[i]
		}
	}
	return nil
}

// ===================== 解析 =====================

// ParseAIBusinessModelResult 宽容解析商业模式模块。
// 仅顶层 JSON 非法时返回 error；其余缺失/脏类型/非法分档按「无法判断」占位。
// AI 返回的 score/rating/grade/total_score 不进入解析结构（评分由 finalize 重算）。
func ParseAIBusinessModelResult(raw string) (*model.AIBusinessModelResult, error) {
	s := stripJSONFence(raw)
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &top); err != nil {
		return nil, fmt.Errorf("解析 AI 返回失败: %w（原始返回：%s）", err, clip(s, 200))
	}
	// D9：容忍 AI 多包一层 business_model 对象。
	if _, ok := top["elements"]; !ok {
		if inner, ok := top["business_model"]; ok && len(inner) > 0 && string(inner) != "null" {
			var wrapped map[string]json.RawMessage
			if err := json.Unmarshal(inner, &wrapped); err == nil {
				top = wrapped
			}
		}
	}

	entries := parseBusinessElementEntries(top["elements"])
	byKey := make(map[string]model.AIBusinessElement, len(entries))
	for _, entry := range entries {
		key := normalizeBusinessElementKey(entry.keyHint, stringField(entry.obj, "name"), stringField(entry.obj, "code"))
		if key == "" {
			continue // 无法识别：丢弃，不按下标强配，避免漏项后错位
		}
		if _, exists := byKey[key]; exists {
			continue // 重复 key：第一条生效
		}
		byKey[key] = newBusinessElement(key, stringField(entry.obj, "level"),
			stringField(entry.obj, "analysis"), stringField(entry.obj, "basis"))
	}
	return &model.AIBusinessModelResult{
		Elements:   businessElementsOrdered(byKey),
		Summary:    truncateRunes(strings.TrimSpace(rawString(top, "summary")), businessSummaryMax),
		Conclusion: truncateRunes(strings.TrimSpace(rawString(top, "conclusion")), businessConclusionMax),
		Total:      businessTotalElements,
	}, nil
}

// businessElementEntry 一条 AI 要素原文（keyHint 来自数组元素的 key 或对象形式的键名）。
type businessElementEntry struct {
	keyHint string
	obj     map[string]any
}

// parseBusinessElementEntries 优先按数组解析，失败按对象 map 兜底；其余类型按空。
func parseBusinessElementEntries(raw json.RawMessage) []businessElementEntry {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err == nil {
		out := make([]businessElementEntry, 0, len(items))
		for _, item := range items {
			var obj map[string]any
			if err := json.Unmarshal(item, &obj); err != nil {
				continue // 单条脏类型只丢该条
			}
			out = append(out, businessElementEntry{keyHint: stringField(obj, "key"), obj: obj})
		}
		return out
	}
	var objMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &objMap); err != nil {
		return nil // 非数组/非对象：全部占位
	}
	keys := make([]string, 0, len(objMap))
	for k := range objMap {
		keys = append(keys, k)
	}
	sort.Strings(keys) // 对象形式的迭代顺序不稳定，排序保证重复 key 时「第一条」确定
	out := make([]businessElementEntry, 0, len(keys))
	for _, k := range keys {
		var obj map[string]any
		if err := json.Unmarshal(objMap[k], &obj); err != nil {
			continue
		}
		out = append(out, businessElementEntry{keyHint: k, obj: obj})
	}
	return out
}

// normalizeBusinessElementKey 以固定 key 优先，其次代号与中文/英文名模糊匹配。
// CS 代号有歧义：名称含「成本」时按成本结构，否则按客户细分；CST 直接映射成本结构。
func normalizeBusinessElementKey(rawKey, rawName, rawCode string) string {
	token := normalizeBusinessToken(rawKey)
	if token != "" {
		for _, d := range businessElementDefs {
			if normalizeBusinessToken(d.Key) == token {
				return d.Key
			}
		}
	}
	name := strings.TrimSpace(rawName)
	// 代号优先取 code 字段；AI 有时直接把 CS/CST 写进 key，此时用 key 兜底消歧。
	code := strings.ToUpper(normalizeBusinessToken(rawCode))
	if code == "" {
		code = strings.ToUpper(normalizeBusinessToken(rawKey))
	}
	switch code {
	case "CST":
		return "cost_structure"
	case "CS":
		if strings.Contains(name, "成本") {
			return "cost_structure"
		}
		return "customer_segments"
	}

	// 中文名模糊匹配（固定顺序，先匹配更具体的名称）。
	chinese := []struct {
		key     string
		keyword string
	}{
		{"cost_structure", "成本结构"},
		{"customer_segments", "客户细分"},
		{"customer_relationships", "客户关系"},
		{"value_propositions", "价值主张"},
		{"key_resources", "核心资源"},
		{"key_activities", "关键活动"},
		{"key_partners", "关键合作"},
		{"channels", "渠道"},
		{"revenue_streams", "收入来源"},
	}
	for _, c := range chinese {
		if strings.Contains(name, c.keyword) {
			return c.key
		}
	}
	// 英文全称模糊匹配（含 rawKey，兼容 CustomerSegments / customer segments）。
	lower := strings.ToLower(name + " " + rawKey + " " + rawCode)
	for _, d := range businessElementDefs {
		if d.English != "" && strings.Contains(lower, strings.ToLower(d.English)) {
			return d.Key
		}
	}
	return ""
}

// normalizeBusinessToken 仅保留字母与数字并转小写（去空格/下划线/连字符/括号）。
func normalizeBusinessToken(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// normalizeBusinessLevel 把 AI 分档归一化为 canonical key；无法识别按 unknown。
func normalizeBusinessLevel(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.Trim(s, "（）()[]【】 ")
	switch {
	case s == "", s == "unknown", s == "n/a", s == "na", s == "none", s == "-", s == "—":
		return model.BusinessLevelUnknown
	case strings.Contains(s, "无法判断"), strings.Contains(s, "信息不足"),
		strings.Contains(s, "不适用"), strings.Contains(s, "待补充"), strings.Contains(s, "暂无"),
		strings.Contains(s, "unknown"):
		return model.BusinessLevelUnknown
	case strings.Contains(s, "强"), strings.Contains(s, "优势"), strings.Contains(s, "优秀"),
		strings.Contains(s, "strong"), strings.Contains(s, "high"):
		return model.BusinessLevelStrong
	case strings.Contains(s, "良"), strings.Contains(s, "较好"), strings.Contains(s, "good"):
		return model.BusinessLevelGood
	case strings.Contains(s, "中"), strings.Contains(s, "一般"), strings.Contains(s, "medium"),
		strings.Contains(s, "mid"), strings.Contains(s, "normal"):
		return model.BusinessLevelMedium
	case strings.Contains(s, "弱"), strings.Contains(s, "weak"), strings.Contains(s, "low"):
		return model.BusinessLevelWeak
	case strings.Contains(s, "差"), strings.Contains(s, "糟糕"), strings.Contains(s, "poor"),
		strings.Contains(s, "bad"):
		return model.BusinessLevelPoor
	}
	return model.BusinessLevelUnknown
}

// newBusinessElement 由固定 key 与 AI 回填内容构造一个规范要素。
// 分档已知但分析为空 → 强制 unknown（避免「强档 + 信息不足」自相矛盾）。
func newBusinessElement(key, level, analysis, basis string) model.AIBusinessElement {
	def := businessElementDefByKey[key]
	level = normalizeBusinessLevel(level)
	analysis = strings.TrimSpace(analysis)
	basis = strings.TrimSpace(basis)
	if level == model.BusinessLevelUnknown || analysis == "" {
		level = model.BusinessLevelUnknown
		analysis = businessUnknownAnalysis
		basis = ""
	} else {
		analysis = truncateRunes(analysis, businessAnalysisMax)
		if basis == "" {
			basis = "AI 推断（未注明来源）"
		} else {
			basis = truncateRunes(basis, businessBasisMax)
		}
	}
	return model.AIBusinessElement{
		Key:        key,
		Name:       def.Name,
		Code:       def.Code,
		Group:      def.Group,
		Definition: def.Definition,
		Order:      def.Order,
		Level:      level,
		LevelName:  businessLevelNames[level],
		Analysis:   analysis,
		Basis:      basis,
	}
}

// businessElementsOrdered 按固定定义表重排并补齐缺失项，保证恰好 9 项。
func businessElementsOrdered(byKey map[string]model.AIBusinessElement) []model.AIBusinessElement {
	out := make([]model.AIBusinessElement, 0, len(businessElementDefs))
	for _, def := range businessElementDefs {
		e, ok := byKey[def.Key]
		if !ok {
			e = newBusinessElement(def.Key, "", "", "")
		}
		out = append(out, e)
	}
	return out
}

// ===================== Finalize =====================

// FinalizeBusinessModelResult 后端确定性汇总：评分/评级/覆盖率、数字守卫、结论兜底、财务印证。
// AI 返回的任何评分字段都不参与计算。
func FinalizeBusinessModelResult(payload any, in AIModuleInput) (any, error) {
	res, ok := payload.(*model.AIBusinessModelResult)
	if !ok || res == nil {
		return nil, fmt.Errorf("商业模式模块结果类型无效")
	}

	// 1) 结构归一：按固定定义表重排，补齐缺失项（防御手工构造的载荷）。
	byKey := make(map[string]model.AIBusinessElement, len(res.Elements))
	for _, e := range res.Elements {
		key := e.Key
		if _, exists := businessElementDefByKey[key]; !exists {
			key = normalizeBusinessElementKey("", e.Name, e.Code)
		}
		if _, exists := businessElementDefByKey[key]; !exists {
			continue
		}
		if _, exists := byKey[key]; exists {
			continue
		}
		byKey[key] = newBusinessElement(key, e.Level, e.Analysis, e.Basis)
	}
	res.Elements = businessElementsOrdered(byKey)

	// 2) 评分聚合（总分/评级/覆盖率由同一函数一次算出）。
	levels := make([]string, 0, len(res.Elements))
	for _, e := range res.Elements {
		levels = append(levels, e.Level)
	}
	score := AggregateBusinessModelScore(levels)
	res.Scored, res.Score, res.Rating = score.Scored, score.Score, score.Rating
	res.Coverage, res.Total = score.Coverage, score.Total

	// 3) 数字白名单（唯一来源为事实块）+ 要素依据/分析的数字守卫（只降级，不失败）。
	allowed := businessNumberWhitelist(businessModelFactBlock(in))
	for i := range res.Elements {
		e := &res.Elements[i]
		if e.Level == model.BusinessLevelUnknown {
			continue
		}
		if !businessTextNumbersAllowed(e.Analysis, allowed) || !businessTextNumbersAllowed(e.Basis, allowed) {
			e.Basis = "AI 推断（含未核实数据）"
		}
	}

	// 4) 一句话结论：未评分/空/超长/含未提供数字/与评级矛盾 → 默认句。
	if !res.Scored || !businessSummaryValid(res.Summary, res.Rating, allowed) {
		res.Summary = businessDefaultSummary(res.Rating)
	}

	// 5) 总体归纳：未评分/空/超长/含未提供数字/投资建议话术/与评级矛盾 → 确定性兜底。
	if !res.Scored || !businessConclusionValid(res.Conclusion, res.Rating, allowed) {
		res.Conclusion = businessConclusionFallback(*res)
	}

	// 6) 轻量财务印证提示（不改变分数）。
	res.Evidence = businessFinancialEvidence(score, in.Analysis)

	log.Printf("AI 商业模式: scored=%v score=%d rating=%s coverage=%d/%d",
		res.Scored, res.Score, res.Rating, res.Coverage, res.Total)
	return res, nil
}

// ===================== 数字守卫 =====================

// businessNumberPattern 匹配阿拉伯数字 token（含小数）。
var businessNumberPattern = regexp.MustCompile(`\d+(?:\.\d+)?`)

// businessNumberWhitelist 从事实来源文本提取所有数字 token，并为小数补充整数部分。
func businessNumberWhitelist(text string) map[string]bool {
	allowed := make(map[string]bool)
	for _, tok := range businessNumberPattern.FindAllString(text, -1) {
		allowed[tok] = true
		if i := strings.IndexByte(tok, '.'); i > 0 {
			allowed[tok[:i]] = true
		}
	}
	return allowed
}

// businessTextNumbersAllowed 文本中出现的所有数字 token 都必须在白名单内。
// 中文数字与无数字文本天然通过。
func businessTextNumbersAllowed(text string, allowed map[string]bool) bool {
	for _, tok := range businessNumberPattern.FindAllString(text, -1) {
		if !allowed[tok] {
			return false
		}
	}
	return true
}

// ===================== 一句话结论 / 总体归纳 =====================

// businessStrongPositive 强正向断言（评级一般及以下时不得出现）。
var businessStrongPositive = []string{
	"非常优秀", "极其优秀", "极为优秀", "卓越", "极佳", "顶级", "完美",
	"无懈可击", "无与伦比", "优秀", "顶尖", "一流",
}

// businessStrongNegative 强负向断言（评级良好及以上时不得出现）。
var businessStrongNegative = []string{
	"较差", "糟糕", "失败", "不可持续", "毫无竞争力", "缺乏竞争力", "没有壁垒", "毫无壁垒",
}

// businessInvestmentBanned 总体归纳中禁止出现的投资建议话术。
var businessInvestmentBanned = []string{
	"目标价", "买入", "卖出", "增持", "减持", "投资建议", "评级",
}

// businessRatingContradicts 文案是否与后端评级明显矛盾。
func businessRatingContradicts(text, rating string) bool {
	switch rating {
	case "一般", "偏弱", "较差":
		for _, w := range businessStrongPositive {
			if strings.Contains(text, w) {
				return true
			}
		}
	case "优秀", "良好":
		for _, w := range businessStrongNegative {
			if strings.Contains(text, w) {
				return true
			}
		}
	}
	return false
}

// businessSummaryValid 一句话结论校验：评级非空、≤40 字、无未提供数字、不与评级矛盾。
func businessSummaryValid(summary, rating string, allowed map[string]bool) bool {
	s := strings.TrimSpace(summary)
	if rating == "" || s == "" {
		return false
	}
	if utf8.RuneCountInString(s) > businessSummaryMax {
		return false
	}
	if !businessTextNumbersAllowed(s, allowed) {
		return false
	}
	return !businessRatingContradicts(s, rating)
}

// businessDefaultSummary 按评级给出不含数字的默认一句话结论。
func businessDefaultSummary(rating string) string {
	switch rating {
	case "优秀":
		return "模式优秀，核心优势突出且难以复制。"
	case "良好":
		return "模式清晰，核心优势可持续，但仍有改进空间。"
	case "一般":
		return "模式中规中矩，壁垒不明显，需持续观察。"
	case "偏弱":
		return "模式存在明显短板，抗风险与复制能力偏弱。"
	case "较差":
		return "模式缺乏可持续竞争力，价值获取能力较弱。"
	default:
		return "可判断的信息不足，暂不评分。"
	}
}

// businessConclusionValid 总体归纳校验：≤300 字、无未提供数字、无投资建议话术、不与评级矛盾。
func businessConclusionValid(conclusion, rating string, allowed map[string]bool) bool {
	s := strings.TrimSpace(conclusion)
	if s == "" || utf8.RuneCountInString(s) > businessConclusionMax {
		return false
	}
	if !businessTextNumbersAllowed(s, allowed) {
		return false
	}
	for _, banned := range businessInvestmentBanned {
		if strings.Contains(s, banned) {
			return false
		}
	}
	return !businessRatingContradicts(s, rating)
}

// businessStrongWeak 找出最强、次强与最弱要素；全部可评分要素同档视为均衡。
func businessStrongWeak(elements []model.AIBusinessElement) (top, second, bottom string, balanced bool) {
	topScore, secondScore, bottomScore := -1, -1, 101
	for _, e := range elements {
		s, ok := businessLevelScores[e.Level]
		if !ok {
			continue
		}
		if s > topScore {
			secondScore, second = topScore, top
			topScore, top = s, e.Name
		} else if s > secondScore && e.Name != top {
			secondScore, second = s, e.Name
		}
		if s < bottomScore {
			bottomScore, bottom = s, e.Name
		}
	}
	return top, second, bottom, topScore == bottomScore
}

// businessConclusionFallback 未评分或 AI 归纳不合格时的确定性兜底（不含阿拉伯数字）。
func businessConclusionFallback(res model.AIBusinessModelResult) string {
	if !res.Scored {
		return "公开信息不足，仅能对部分要素形成判断，暂不给出总体评分；已展示可判断要素的分析供参考。"
	}
	top, second, bottom, balanced := businessStrongWeak(res.Elements)
	switch {
	case top == "":
		return fmt.Sprintf("整体商业模式评级为「%s」。", res.Rating)
	case balanced:
		return fmt.Sprintf("九要素表现较为均衡，整体商业模式评级为「%s」。", res.Rating)
	case second == "":
		return fmt.Sprintf("九要素中，%s相对突出；%s相对薄弱。整体商业模式评级为「%s」。", top, bottom, res.Rating)
	default:
		return fmt.Sprintf("九要素中，%s、%s相对突出；%s相对薄弱。整体商业模式评级为「%s」。", top, second, bottom, res.Rating)
	}
}

// ===================== 财务印证提示（Q10） =====================

// businessFinancialEvidence 只用已有六维指标生成一句提示，不改变分数。
func businessFinancialEvidence(score BusinessModelScore, a model.FinancialAnalysis) string {
	if !score.Scored {
		return ""
	}
	roe := businessLastNonNil(a, "roe")
	cash := businessLastNonNil(a, "operating_cash_flow")
	revenueDeclining := businessRevenueDeclining(a)
	if score.Score >= 75 && ((roe != nil && *roe < 8) || revenueDeclining || (cash != nil && *cash <= 0)) {
		return "模式判断偏乐观，建议结合 ROE 与经营现金流复核。"
	}
	if score.Score < 60 && roe != nil && *roe >= 15 && cash != nil && *cash > 0 {
		return "财务表现优于模式判断，需结合行业与竞争格局理解差异。"
	}
	return "财务数据与模式判断基本一致。"
}

// businessRevenueDeclining 最近至多 4 个年报的营业收入是否下降（首末非空值比较）。
func businessRevenueDeclining(a model.FinancialAnalysis) bool {
	values := businessSeriesValues(a, "revenue")
	if len(values) == 0 {
		return false
	}
	start := 0
	if len(values) > 4 {
		start = len(values) - 4
	}
	var first, last *float64
	for i := start; i < len(values); i++ {
		if values[i] == nil {
			continue
		}
		if first == nil {
			first = values[i]
		}
		last = values[i]
	}
	return first != nil && last != nil && *last < *first
}

// ===================== JSON 读取辅助 =====================

// rawString 从 RawMessage map 中读取字符串字段；非字符串按空处理。
func rawString(m map[string]json.RawMessage, key string) string {
	raw, ok := m[key]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}
