package web

import "embed"

// FS 嵌入前端静态资源（MVP 阶段直接由 Go 托管，后续可替换为独立 Vite 前端）
//
//go:embed index.html app.js style.css
var FS embed.FS
