# A股财报分析工具

输入 A 股股票代码，自动拉取财报与行情数据，一键完成多维财务指标分析、公司估值与 AI 智能诊断，并以图表可视化呈现。

> ⚠️ 本工具数据来自公开免费接口，分析结果与估值仅供参考，**不构成任何投资建议**。

## ✨ 功能特性

- **六维财务指标分析**：投资、筹资、资产资本、股权价值增加值、综合分析、经营现金流，逐层拆解派生指标并附解读。
- **公司估值**：现金流贴现法（DCF），支持零增长 / 永续增长 / 两阶段 / 三阶段四种模型，可调基期自由现金流、折现率、增长率与年数。
- **AI 智能分析**：接入 DeepSeek，输出盈利能力 / 偿债能力 / 现金获取能力 / 经营效率 / 成长能力五维评分，并给出估值模型推荐、行业分析与业务板块分析。
- **指标可视化**：柱状图展示指标值 + 折线图展示增速，涨红跌绿，移动端自适应。

## 🧱 技术栈

- 后端：Go 1.25 + Gin
- 前端：Vue 3 + Element Plus + ECharts（CDN，内嵌静态页）
- 数据源：东方财富 datacenter（财报）、腾讯行情（实时行情）
- AI：DeepSeek（OpenAI 兼容接口）

## 🚀 快速开始

### 环境要求

- Go 1.25+
- （可选）DeepSeek API Key，用于「AI 智能分析」

### 启动

```bash
go run ./cmd/server
```

默认监听 `0.0.0.0:18080`，启动后浏览器打开 <http://localhost:18080>。

### 启动参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-addr` | `0.0.0.0:18080` | 监听地址 `ip:port` |
| `-apikey` | 空 | DeepSeek API Key（未填则读环境变量 `DEEPSEEK_API_KEY`） |
| `-model` | `deepseek-v4-pro` | DeepSeek 模型名 |
| `-baseurl` | `https://api.deepseek.com/v1` | DeepSeek 接口地址 |

示例：

```bash
go run ./cmd/server -apikey sk-xxxx -addr 127.0.0.1:8080
```

> AI 智能分析需配置 `-apikey`；不配置时其余功能正常，AI 分析会提示未配置。

## 📡 API 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/health` | 健康检查 |
| GET | `/api/stock/:code/indicators` | 静态估值指标 |
| GET | `/api/stock/:code/financials` | 三大报表数据 |
| GET | `/api/stock/:code/analysis` | 六维财务指标分析 |
| GET | `/api/stock/:code/valuation` | 公司估值（DCF） |
| GET | `/api/stock/:code/ai-analysis` | AI 智能分析 |

统一返回 `{code, data, message}`，`code == 0` 表示成功。

## 📁 项目结构

```
financial-report/
├── cmd/server/         # 入口
├── internal/
│   ├── api/            # Gin handler / 路由
│   ├── client/         # 数据获取（东财 + 腾讯）
│   ├── model/          # 标准结构体
│   ├── service/        # 指标计算（纯函数）
│   └── web/            # 内嵌前端（Vue 3）
├── docs/               # 产品 / 技术设计文档
├── go.mod
└── go.sum
```

## 📚 文档

- `docs/产品设计文档.md` — 功能与产品设计
- `docs/技术设计文档.md` — 技术架构与实现设计
- `docs/公司股票估值.md` — 估值模型口径

## ⚠️ 免责声明

本工具仅供学习研究使用。所有数据来源于公开免费接口，可能存在延迟或误差；分析结果与估值仅供参考，不构成任何投资建议，据此操作风险自负。
