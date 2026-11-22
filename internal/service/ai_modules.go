package service

import (
	"fmt"

	"financial-report/internal/model"
)

// AIModuleNeeds 模块对上游数据的依赖声明，用于一次请求内的并集取数。
type AIModuleNeeds struct {
	Quote         bool // 行情（模块公共）
	Reports       bool // 三张 F10 全量报表
	Segments      bool // 主营构成
	Growth        bool // 外部增长快照（一致预期/GDP/分红）
	Peers         bool // 行业同行市值榜
	Industry      bool // 行业分类（DMSK 或报表行）
	Analysis      bool // service.ComputeAnalysis（六维指标）
	ValuationBase bool // service.RecommendValuation 预推导
}

// UnionNeeds 逐项 OR 合并多个模块的数据依赖（all 取并集）。
func UnionNeeds(specs []AIModuleSpec) AIModuleNeeds {
	var n AIModuleNeeds
	for _, s := range specs {
		n.Quote = n.Quote || s.Needs.Quote
		n.Reports = n.Reports || s.Needs.Reports
		n.Segments = n.Segments || s.Needs.Segments
		n.Growth = n.Growth || s.Needs.Growth
		n.Peers = n.Peers || s.Needs.Peers
		n.Industry = n.Industry || s.Needs.Industry
		n.Analysis = n.Analysis || s.Needs.Analysis
		n.ValuationBase = n.ValuationBase || s.Needs.ValuationBase
	}
	return n
}

// AIModuleInput 一次请求内共享的只读事实，由 api 层构建、service 层消费。
type AIModuleInput struct {
	Quote        model.Quote
	Analysis     model.FinancialAnalysis
	Segments     []model.SegmentIncome
	Rec          model.ValuationRecommendation
	Leaders      model.LeaderBoard
	IndustryCode string
	IndustryName string

	// 仅供估值模块 finalize 两段式重算；其余模块不读。
	ValuationInput ValuationRecommendInput
}

// AIModuleSpec AI 分析模块注册项。
type AIModuleSpec struct {
	ID          string
	Title       string // 中文标题（仅展示/日志）
	Description string // 一句话职责（前端展示）
	Implemented bool
	Needs       AIModuleNeeds
	// BuildPrompt 只读取 AIModuleInput，不产生 IO。
	BuildPrompt func(in AIModuleInput) (system, user string)
	// Parse 只解析本模块 JSON；返回 *model.AIFinancialResult / *model.AIIndustryResult / *model.AIValuationResult。
	Parse func(raw, selfName string) (any, error)
	// Finalize 可选：确定性回填（行业龙头/行业名）或两段式重算（估值）。
	Finalize func(payload any, in AIModuleInput) (any, error)
}

// aiModuleSpecs 模块注册表，顺序即前端展示顺序。
var aiModuleSpecs = []AIModuleSpec{
	{
		ID: model.AIModuleFinancial, Title: "公司财务分析",
		Description: "财务健康与经营质量：五维评分与总体结论",
		Implemented: true,
		Needs:       AIModuleNeeds{Quote: true, Reports: true, Segments: true, Analysis: true},
		BuildPrompt: BuildFinancialPrompt,
		Parse:       func(raw, _ string) (any, error) { return ParseAIFinancialResult(raw) },
	},
	{
		ID: model.AIModuleIndustry, Title: "行业分析",
		Description: "所处行业、业务板块、产业链位置与相关公司",
		Implemented: true,
		// D1：单行业模块不拉三张 F10 报表，直接走 DMSK；all 中报表已存在时仍报表行优先。
		Needs:       AIModuleNeeds{Quote: true, Segments: true, Peers: true, Industry: true},
		BuildPrompt: BuildIndustryPrompt,
		Parse:       func(raw, selfName string) (any, error) { return ParseAIIndustryResult(raw, selfName) },
		Finalize:    FinalizeIndustryResult,
	},
	{
		ID: model.AIModuleValuation, Title: "估值参数分析",
		Description: "估值模型与确定性参数推荐",
		Implemented: true,
		Needs:       AIModuleNeeds{Quote: true, Reports: true, Segments: true, Growth: true, ValuationBase: true},
		BuildPrompt: BuildValuationPrompt,
		Parse:       func(raw, _ string) (any, error) { return ParseAIValuationResult(raw) },
		Finalize:    FinalizeValuationResult,
	},
	{
		ID: model.AIModuleBusinessModel, Title: "商业模式分析",
		Description: "九要素与商业模式评分（R27 上线）",
		Implemented: false, // R27 改为 true，并补齐 Needs/BuildPrompt/Parse/Finalize
	},
}

// ResolveAIModules 把 layer 参数解析为模块列表。
//
// "" 或 "all" → 全部模块且 all=true；合法模块 ID → 单个模块且 all=false；
// 其他 → error，由 handler 映射为 400。
func ResolveAIModules(module string) (specs []AIModuleSpec, all bool, err error) {
	if module == "" || module == model.AIModuleAll {
		out := make([]AIModuleSpec, len(aiModuleSpecs))
		copy(out, aiModuleSpecs)
		return out, true, nil
	}
	for _, s := range aiModuleSpecs {
		if s.ID == module {
			return []AIModuleSpec{s}, false, nil
		}
	}
	return nil, false, fmt.Errorf("module 参数无效（可选 financial、industry、valuation、business_model、all）")
}
