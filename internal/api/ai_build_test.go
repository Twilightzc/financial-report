package api

import (
	"errors"
	"strings"
	"testing"

	"financial-report/internal/model"
	"financial-report/internal/service"
)

// 取数编排回归（P1）：三表/无年报失败只应影响财务与估值模块，
// 行业模块仍须拿到已成功取得的 Segments / Leaders / 行业分类。

// aiBuildTestHandler 构造只含 AI 取数注入的 Handler（不触碰真实 client）。
func aiBuildTestHandler(reportsErr error, reports []model.ReportRow) *Handler {
	return &Handler{aiSource: aiDataSource{
		quote: func(string) (*model.Quote, error) {
			return &model.Quote{Name: "贵州茅台", Code: "600519", MarketCap: 1.8e12}, nil
		},
		reports: func(string, int, int) (cashflow, balance, income []model.ReportRow, err error) {
			if reportsErr != nil {
				return nil, nil, nil, reportsErr
			}
			return reports, reports, reports, nil
		},
		segments: func(string) ([]model.SegmentIncome, error) {
			return []model.SegmentIncome{{Name: "茅台酒", RevenueRatio: 0.87}}, nil
		},
		peers: func(string) (model.LeaderBoard, error) {
			return model.LeaderBoard{Companies: []model.LeaderCompany{{Name: "五粮液", Code: "000858"}}}, nil
		},
		industry: func(string) (string, string, error) {
			return "1277", "白酒Ⅱ", nil
		},
		growth: func(string) model.GrowthSnapshot { return model.GrowthSnapshot{} },
	}}
}

// aiAllImplementedNeeds all 请求的依赖并集（含行业所需 Segments/Peers/Industry）。
func aiAllImplementedNeeds(t *testing.T) service.AIModuleNeeds {
	t.Helper()
	specs, _, err := service.ResolveAIModules(model.AIModuleAll)
	if err != nil {
		t.Fatalf("ResolveAIModules(all) 失败: %v", err)
	}
	return service.UnionNeeds(specs)
}

func mustModuleSpec(t *testing.T, id string) service.AIModuleSpec {
	t.Helper()
	specs, _, err := service.ResolveAIModules(id)
	if err != nil || len(specs) != 1 {
		t.Fatalf("ResolveAIModules(%q) 失败: %v", id, err)
	}
	return specs[0]
}

// assertIndustryOptionalData 断言行业模块所需的三项可选/回退数据均已汇合。
func assertIndustryOptionalData(t *testing.T, in service.AIModuleInput) {
	t.Helper()
	if in.Quote.Name != "贵州茅台" {
		t.Errorf("Quote 不应丢失: %+v", in.Quote)
	}
	if len(in.Segments) != 1 || in.Segments[0].Name != "茅台酒" {
		t.Errorf("Segments 应保留已成功的取数: %+v", in.Segments)
	}
	if len(in.Leaders.Companies) != 1 || in.Leaders.Companies[0].Name != "五粮液" {
		t.Errorf("Leaders 应保留已成功的取数: %+v", in.Leaders)
	}
	if in.IndustryCode != "1277" || in.IndustryName != "白酒Ⅱ" {
		t.Errorf("报表为空时应回退 DMSK 行业分类: code=%q name=%q", in.IndustryCode, in.IndustryName)
	}
	if len(in.Analysis.Years) != 0 {
		t.Errorf("取数失败时不应计算六维指标: %+v", in.Analysis)
	}
	if in.Rec.Model != "" {
		t.Errorf("取数失败时不应预推导估值: %+v", in.Rec)
	}
}

// P1-a 三表失败：返回 reports 错误，但行业数据完整，且错误不影响行业模块。
func TestBuildAIModuleDataReportsFailureKeepsIndustryData(t *testing.T) {
	h := aiBuildTestHandler(errors.New("上游超时"), nil)
	in, _, _, err := h.buildAIModuleData("600519", aiAllImplementedNeeds(t), 0, 0, false)
	if err == nil {
		t.Fatal("三表失败应返回错误")
	}
	var be *aiBuildError
	if !errors.As(err, &be) || !be.reports || be.year || be.quote {
		t.Fatalf("错误类型应为 reports: %#v", err)
	}
	if aiFetchAffects(err, mustModuleSpec(t, model.AIModuleIndustry)) {
		t.Error("三表失败不应影响行业模块")
	}
	if !aiFetchAffects(err, mustModuleSpec(t, model.AIModuleFinancial)) {
		t.Error("三表失败应影响财务模块")
	}
	if !aiFetchAffects(err, mustModuleSpec(t, model.AIModuleValuation)) {
		t.Error("三表失败应影响估值模块")
	}
	if !aiFetchAffects(err, mustModuleSpec(t, model.AIModuleBusinessModel)) {
		t.Error("三表失败应影响商业模式模块")
	}
	assertIndustryOptionalData(t, in)
}

// P1-b 无年报行：返回 year 错误，但行业数据完整，且错误不影响行业模块。
func TestBuildAIModuleDataYearFailureKeepsIndustryData(t *testing.T) {
	h := aiBuildTestHandler(nil, nil) // 三表请求成功但没有任何年报行
	in, _, _, err := h.buildAIModuleData("600519", aiAllImplementedNeeds(t), 0, 0, false)
	if err == nil {
		t.Fatal("无年报数据应返回错误")
	}
	var be *aiBuildError
	if !errors.As(err, &be) || !be.year {
		t.Fatalf("错误类型应为 year: %#v", err)
	}
	if aiFetchAffects(err, mustModuleSpec(t, model.AIModuleIndustry)) {
		t.Error("无年报数据不应影响行业模块")
	}
	if !aiFetchAffects(err, mustModuleSpec(t, model.AIModuleBusinessModel)) {
		t.Error("无年报数据应影响商业模式模块")
	}
	assertIndustryOptionalData(t, in)
}

// T16 FinancialLike 由已取利润表回填：银行型（有收入、无营业成本）→ true 且写入提示词。
func TestBuildAIModuleDataFinancialLike(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	bank := []model.ReportRow{{
		ReportDate: "2024-12-31",
		Fields: map[string]*float64{
			"TOTAL_OPERATE_INCOME": f(100e8),
			"NETCASH_OPERATE":      f(20e8),
			"TOTAL_ASSETS":         f(2000e8),
			"TOTAL_EQUITY":         f(200e8),
		},
	}}
	h := aiBuildTestHandler(nil, bank)
	spec := mustModuleSpec(t, model.AIModuleBusinessModel)
	in, _, _, err := h.buildAIModuleData("600519", spec.Needs, 0, 0, false)
	if err != nil {
		t.Fatalf("取数失败: %v", err)
	}
	if !in.FinancialLike {
		t.Errorf("银行型公司（无营业成本）FinancialLike 应为 true")
	}
	_, user := service.BuildBusinessModelPrompt(in)
	if !strings.Contains(user, "金融类企业：是") {
		t.Errorf("金融类公司提示词应标注「金融类企业：是」: %s", user)
	}
}

// T16b 普通公司（有营业成本）FinancialLike 为 false，提示词标注「否」。
func TestBuildAIModuleDataNotFinancialLike(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	normal := []model.ReportRow{{
		ReportDate: "2024-12-31",
		Fields: map[string]*float64{
			"TOTAL_OPERATE_INCOME": f(100e8),
			"OPERATE_COST":         f(40e8),
			"NETCASH_OPERATE":      f(20e8),
			"TOTAL_ASSETS":         f(200e8),
			"TOTAL_EQUITY":         f(120e8),
		},
	}}
	h := aiBuildTestHandler(nil, normal)
	spec := mustModuleSpec(t, model.AIModuleBusinessModel)
	in, _, _, err := h.buildAIModuleData("600519", spec.Needs, 0, 0, false)
	if err != nil {
		t.Fatalf("取数失败: %v", err)
	}
	if in.FinancialLike {
		t.Errorf("普通公司 FinancialLike 应为 false")
	}
	_, user := service.BuildBusinessModelPrompt(in)
	if !strings.Contains(user, "金融类企业：否") {
		t.Errorf("普通公司提示词应标注「金融类企业：否」: %s", user)
	}
}
