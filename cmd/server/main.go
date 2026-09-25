package main

import (
	"flag"
	"log"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"

	"financial-report/internal/api"
	"financial-report/internal/client"
	"financial-report/internal/web"
)

func main() {
	addr := flag.String("addr", "0.0.0.0:18080", "监听地址，格式 ip:port（默认 0.0.0.0:18080）")
	aiKey := flag.String("apikey", "", "DeepSeek API Key（AI 智能分析用；未填则读环境变量 DEEPSEEK_API_KEY）")
	aiModel := flag.String("model", "deepseek-v4-pro", "DeepSeek 模型名（AI 智能分析用）")
	aiBaseURL := flag.String("baseurl", "https://api.deepseek.com/v1", "DeepSeek 接口地址（OpenAI 兼容）")
	flag.Parse()

	c := client.New()
	h := api.NewHandler(c, *aiKey, *aiModel, *aiBaseURL)
	r := api.NewRouter(h)

	// 托管嵌入的前端静态资源
	r.StaticFS("/static", http.FS(web.FS))
	r.GET("/", func(c *gin.Context) {
		data, _ := web.FS.ReadFile("index.html")
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	})

	host, port, err := net.SplitHostPort(*addr)
	if err != nil {
		log.Fatalf("无效监听地址 %q：%v", *addr, err)
	}
	log.Printf("服务已启动: %s（本机 http://localhost:%s）", *addr, port)
	if host == "0.0.0.0" || host == "::" || host == "" {
		log.Printf("局域网访问 http://%s:%s", lanIP(), port)
	}
	if err := r.Run(*addr); err != nil {
		log.Fatal(err)
	}
}

// lanIP 返回本机局域网 IPv4 地址，优先私有网段（192.168/10/172.16-31），兜底返回任意非回环 IPv4。
func lanIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	var fallback string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil {
			continue
		}
		if fallback == "" {
			fallback = ip4.String()
		}
		if ip4[0] == 10 || ip4[0] == 192 && ip4[1] == 168 || ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
			return ip4.String()
		}
	}
	if fallback != "" {
		return fallback
	}
	return "127.0.0.1"
}
