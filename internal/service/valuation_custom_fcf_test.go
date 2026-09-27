package service

import (
	"encoding/json"
	"strings"
	"testing"

	"financial-report/internal/model"
)

// TestParseCustomFCFExactConversion 自定义基期现金流换算的精确性（FR-3「换算过程不产生额外四舍五入误差」）。
// 用例特意选取「float64 直接相乘会出错」的组合（如 8.7×1e4、1.15×1e8、0.07×1e8），故断言用完全相等
// （`==`）而非 approx —— 朴素 float 实现会在此失败，用于锁定 big.Rat 精确换算不被回退。
func TestParseCustomFCFExactConversion(t *testing.T) {
	cases := []struct {
		name  string
		value string
		unit  string
		want  float64
	}{
		{"6.5亿", "6.5", "yi", 650000000},
		{"8.7万（8.7*1e4 浮点不安全）", "8.7", "wan", 87000},
		{"1.15亿（1.15*1e8 浮点不安全）", "1.15", "yi", 115000000},
		{"0.07亿（0.07*1e8 浮点不安全）", "0.07", "yi", 7000000},
		{"2.03千（2.03*1e3 浮点不安全）", "2.03", "qian", 2030},
		{"0.01亿 = 100万元", "0.01", "yi", 1000000},
		{"不选单位（元）不换算", "1.1", "yuan", 1.1},
		{"上限恰好 1e14 元", "1000000", "yi", 1e14},
		{"上限 + 两位小数", "1000000.00", "yi", 1e14},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseCustomFCF(c.value, c.unit)
			if err != nil {
				t.Fatalf("ParseCustomFCF(%q,%q) 返回错误：%v", c.value, c.unit, err)
			}
			if got != c.want {
				t.Errorf("ParseCustomFCF(%q,%q) = %v，期望精确值 %v（差 %v）", c.value, c.unit, got, c.want, got-c.want)
			}
		})
	}

	// 空 unit 与显式 "yi" 等价（缺省单位「亿」，Q2）。
	withDefault, err := ParseCustomFCF("6.5", "")
	if err != nil {
		t.Fatalf("空 unit 应合法：%v", err)
	}
	explicit, _ := ParseCustomFCF("6.5", "yi")
	if withDefault != explicit {
		t.Errorf("空 unit=%v 与 yi=%v 不等", withDefault, explicit)
	}

	// 超长原文（30 位）不得 panic/挂起，应走上限错误。
	if _, err := ParseCustomFCF("999999999999999999999999999999", "yuan"); err == nil || !strings.Contains(err.Error(), "过大") {
		t.Errorf("30 位原文应返回上限错误，实际 %v", err)
	}
	// 单位大小写敏感：不做宽松匹配，未知单位必须报错（防前后端字面量漂移被静默吞掉）。
	if _, err := ParseCustomFCF("6.5", "YI"); err == nil || !strings.Contains(err.Error(), "未知的基期现金流单位") {
		t.Errorf("大写 YI 应返回未知单位错误，实际 %v", err)
	}
}

// TestComputeValuationCustomNoCashflowTable custom 分支早于空年份守卫（设计 16.5.4）：
// 现金流量表缺失但资产负债表/利润表有年报时仍可估值，且不产生「基期自由现金流取 X 年」提示。
func TestComputeValuationCustomNoCashflowTable(t *testing.T) {
	balance := []model.ReportRow{row(map[string]float64{
		"MONETARYFUNDS": 1000, "LONG_EQUITY_INVEST": 200,
		"SHORT_LOAN": 300, "LONG_LOAN": 200, "TOTAL_EQUITY": 1500, "TOTAL_ASSETS": 3000,
	})}
	income := []model.ReportRow{row(map[string]float64{"INVEST_JOINT_INCOME": 20})}

	res, err := ComputeValuation(balance, nil, income,
		model.ValuationParams{Model: "zero", DiscountRate: 10, FCFMode: "custom", FCFCustomValue: 1e9, FCFCustomUnit: "yi"}, 1000)
	if err != nil {
		t.Fatalf("custom + 无现金流量表：期望成功，实际 err=%v", err)
	}
	if res.BaseFCF != 1e9 || res.FCFModeName != "自定义" || res.FCFYears != 0 {
		t.Errorf("BaseFCF=%v name=%q years=%d，期望 1e9/自定义/0", res.BaseFCF, res.FCFModeName, res.FCFYears)
	}
	for _, n := range res.Notes {
		if strings.HasPrefix(n, "基期自由现金流取") {
			t.Errorf("custom 不应产生「取 X 年」提示：%q", n)
		}
	}
}

// TestValuationCustomJSONFields 响应字段的向后兼容：非 custom 不得出现 fcf_custom_*（omitempty），
// custom 回显 value/unit 且不带 fcf_years（FR-6/FR-7、设计 16.3）。
func TestValuationCustomJSONFields(t *testing.T) {
	b, cf, inc := valuationFixture()
	rawOf := func(p model.ValuationParams) map[string]any {
		res, err := ComputeValuation([]model.ReportRow{b}, []model.ReportRow{cf}, []model.ReportRow{inc}, p, 1000)
		if err != nil {
			t.Fatalf("ComputeValuation(%+v) err=%v", p, err)
		}
		raw, _ := json.Marshal(res)
		m := map[string]any{}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	latest := rawOf(model.ValuationParams{Model: "zero", DiscountRate: 10, FCFMode: "latest"})
	for _, k := range []string{"fcf_custom_value", "fcf_custom_unit", "fcf_years"} {
		if _, ok := latest[k]; ok {
			t.Errorf("latest 响应不应含 %s：%v", k, latest)
		}
	}

	custom := rawOf(model.ValuationParams{Model: "zero", DiscountRate: 10, FCFMode: "custom", FCFCustomValue: 6.5e8, FCFCustomUnit: "yi"})
	if custom["fcf_custom_value"] != 6.5e8 || custom["fcf_custom_unit"] != "yi" || custom["fcf_mode_name"] != "自定义" {
		t.Errorf("custom 回显异常：%v", custom)
	}
	if _, ok := custom["fcf_years"]; ok {
		t.Errorf("custom 响应不应含 fcf_years：%v", custom)
	}

	// 非 custom 模式下误传 FCFCustomValue 不得影响基期 FCF（该参数只在 custom 分支被读取）。
	plain := rawOf(model.ValuationParams{Model: "zero", DiscountRate: 10, FCFMode: "latest"})
	polluted := rawOf(model.ValuationParams{Model: "zero", DiscountRate: 10, FCFMode: "latest", FCFCustomValue: 9.9e9})
	if plain["base_fcf"] != polluted["base_fcf"] {
		t.Errorf("latest 基期 FCF 被 FCFCustomValue 污染：%v vs %v", plain["base_fcf"], polluted["base_fcf"])
	}
	// 非 custom 模式即使误传非零 FCFCustomValue/FCFCustomUnit，响应也不得回显（设计 16.5.3「其余留零值」）。
	for _, k := range []string{"fcf_custom_value", "fcf_custom_unit"} {
		if _, ok := polluted[k]; ok {
			t.Errorf("latest + 脏 FCFCustom* 不应回显 %s：%v", k, polluted)
		}
	}
}
