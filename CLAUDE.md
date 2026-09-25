# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

A股财报分析工具（MVP）：输入股票代码，从免费接口拉取财报 + 行情数据，计算指标并可视化展示。后端 Go + Gin，前端是 Go 内嵌的静态页（Vue 3 + Element Plus + ECharts，走 CDN）。产品/技术设计文档在 `docs/`。

## 常用命令

```bash
go run ./cmd/server   # 启动服务（默认 0.0.0.0:18080，可 -addr 指定 ip:port），含内嵌前端
go build ./...        # 编译
go test ./...         # 运行测试（暂无测试）
go vet ./...          # 静态检查
gofmt -l .            # 格式检查
```

## 环境限制（重要）

- 本机 Claude Code 沙箱**屏蔽对外 TCP**（DNS/ICMP 可用）。离线编译需加 `GOPROXY=off GOSUMDB=off`（gin 等依赖已缓存在 `~/go/pkg/mod`，go.sum 已完整）。
- 运行服务并拉取实时数据需 `dangerouslyDisableSandbox`，否则数据接口会超时/EOF。
- 沙箱内无法 `go get` 新增依赖（需联网）。

## 架构

单 Go 模块 `financial-report`（Go 1.25），分层单向依赖：

```
api（Gin handler/router）→ service（指标计算，纯函数）/ client（数据获取）→ model（标准化结构体）
```

- `internal/client/eastmoney.go` — 数据获取。**财报**走东财 datacenter，**行情**走腾讯（文件名含 eastmoney，但行情源已换成腾讯）。
- `internal/model` — 三张表 + 行情 + 指标的标准结构体，全项目通用。
- `internal/service/indicator.go` — 指标计算，纯函数、无 IO，当前为占位方案，待替换为用户指定分析方法。
- `internal/web/` — 前端静态资源，经 `//go:embed` 打进二进制、由 Gin 托管。不是独立 Vite 构建（技术设计文档里的 Vite 方案尚未落地）。

## 数据源字段映射（实测得出，易错）

东财 datacenter 用 `reportName` 区分三张表（`RPT_DMSK_FN_BALANCE` / `RPT_DMSK_FN_INCOME` / `RPT_DMSK_FN_CASHFLOW`），字段名与直觉不符：

- 利润表：营业总收入=`TOTAL_OPERATE_INCOME`、营业成本=`OPERATE_COST`、归母净利润=`PARENT_NETPROFIT`。**没有** `OPERATE_INCOME` 和 `NETPROFIT` 字段。
- 资产负债表：所有者权益合计=`TOTAL_EQUITY`。**没有**归母权益字段，PB/ROE/BPS 用 `TOTAL_EQUITY` 近似。

腾讯行情 `qt.gtimg.cn/q=sh600519`（GBK 编码，需 `simplifiedchinese.GBK` 解码），`~` 分隔，关键下标：`[1]`名称、`[3]`价格、`[32]`涨跌幅%、`[45]`总市值(亿)、`[73]`总股本。

## 关键约定

- 静态估值（PE/PB/ROE/EPS 等）只取**年报**（报告期以 `12-31` 结尾），避免用半年报累计值把 PE 算高一倍；过滤逻辑在 `internal/api/handler.go`。
- 前端经 `/api/stock/:code/indicators` 取数；统一返回 `{code, data, message}`，`code==0` 为成功。
- 已知限制：银行/金融股无「营业成本」科目，毛利率会显示 100%，待按行业区分处理。

## 迭代开发流水线

当用户说 **「开始迭代开发」**（附一句需求描述），按固定顺序串起五个 subagent（定义在 `.claude/agents/`），每步产出落盘到 `docs/`、衔接给下一步：

1. **prodexpert** — 需求分析：提炼需求重点、细化/完善、澄清不确定点，输出需求文档到 `docs/需求文档/<功能名>.md`（并更新其 `README.md` 索引）。
2. **techexpert** — 技术设计：按需求做架构与实现设计，落到 `docs/技术设计文档.md`。
3. **coder** — 实现：按技术设计落地代码，跑通 `go build/vet/test` + `gofmt -l` + `node --check`。
4. **tester** — 测试：按复杂度定方案，跑常见测试，发现问题用 `SendMessage` 回传 `coder` 修复并迭代到全部通过；大幅改动才输出测试报告到 `docs/测试报告/`。
5. **githelper** — 提交：把通过验证的改动按规范提交（署名 `Co-Authored-By: Claude Code <noreply@anthropic.com>`）。

要点：

- 各 agent 通过 `docs/` 与代码库共享上下文，可独立触发（`/prodexpert`、`/techexpert`、`/coder`、`/tester`、`/githelper`）。
- 每一步产出未稳定前不要跳步；tester 未通过前不进入 githelper。
- 步骤间如需用户拍板（prodexpert 的待确认问题、techexpert 的关键决策），先回主对话确认再继续。
