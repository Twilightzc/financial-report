package model

// StockCandidate 股票检索候选（A 股：沪/深/京）。
type StockCandidate struct {
	Code        string `json:"code"`        // 6 位证券代码，如 "600519"、"920185"
	Name        string `json:"name"`        // 证券简称，原样保留 ST/*ST/XD 前缀
	Market      string `json:"market"`      // 交易所前缀：sh / sz / bj
	MarketLabel string `json:"marketLabel"` // 市场徽标文案：沪 / 深 / 京
	PinYin      string `json:"-"`           // 名称首字母（大写），仅服务端命中判定用，不进 API 返回
}
