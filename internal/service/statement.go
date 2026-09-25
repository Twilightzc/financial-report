package service

import (
	"sort"

	"financial-report/internal/model"
)

// BuildStatement 将单张报表的原始行按白名单组装为跨年度视图（平铺）。
// 仅保留年报（报告期以 12-31 结尾）且年份落在 [startYear, endYear]；
// 年份升序；全无数据的科目（所有年份均为 nil）被隐藏。
func BuildStatement(rows []model.ReportRow, fields []model.FieldDef, startYear, endYear int) model.Statement {
	byYear, years := filterRows(rows, startYear, endYear)
	return model.Statement{Years: years, Items: buildItems(byYear, years, fields)}
}

// BuildGroupedStatement 同上，但按分组输出（用于现金流量表按活动类型拆分展示）。
func BuildGroupedStatement(rows []model.ReportRow, groups []model.FieldGroup, startYear, endYear int) model.Statement {
	byYear, years := filterRows(rows, startYear, endYear)
	out := make([]model.StatementGroup, 0, len(groups))
	for _, g := range groups {
		items := buildItems(byYear, years, g.Fields)
		if len(items) == 0 {
			continue // 整组全无数据时隐藏该组
		}
		out = append(out, model.StatementGroup{Title: g.Title, Items: items})
	}
	return model.Statement{Years: years, Groups: out}
}

// filterRows 过滤年报 + 年份范围，返回 year→row 索引与升序年份列表。
func filterRows(rows []model.ReportRow, startYear, endYear int) (map[int]model.ReportRow, []int) {
	byYear := make(map[int]model.ReportRow)
	for _, r := range rows {
		y, ok := model.AnnualYear(r.ReportDate)
		if !ok || y < startYear || y > endYear {
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

// buildItems 按 fields 顺序生成科目列表，与 years 对齐；全无数据的科目被隐藏。
func buildItems(byYear map[int]model.ReportRow, years []int, fields []model.FieldDef) []model.StatementItem {
	items := make([]model.StatementItem, 0, len(fields))
	for _, fd := range fields {
		it := model.StatementItem{
			Field:  fd.Key,
			Name:   fd.Name,
			Values: make([]*float64, len(years)),
		}
		hasValue := false
		for i, y := range years {
			v := byYear[y].Fields[fd.Key] // 缺失为 nil
			it.Values[i] = v
			if v != nil {
				hasValue = true
			}
		}
		if !hasValue {
			continue // 所有年份均无数据，隐藏该科目
		}
		items = append(items, it)
	}
	return items
}
