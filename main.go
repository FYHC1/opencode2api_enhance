package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/manager"
)

// frontendDistDir 返回前端构建产物目录（存在 dist/index.html 时）。
// 查找顺序：可执行文件旁 → 当前工作目录。
func frontendDistDir() string {
	cands := []string{}
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), "dist"))
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, filepath.Join(wd, "dist"))
	}
	for _, d := range cands {
		if _, err := os.Stat(filepath.Join(d, "index.html")); err == nil {
			return d
		}
	}
	return ""
}

var httpClient = &http.Client{
	Timeout: 300 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	},
}

var (
	version = "v0.3.0"
	commit  = "none"
	date    = "unknown"
)

func versionString() string {
	return fmt.Sprintf("opencode2api %s (commit=%s, date=%s)", version, commit, date)
}

func main() {
	var showVersion bool
	flag.StringVar(&port, "port", "8000", "服务端口")
	flag.StringVar(&configPath, "config", "config.json", "配置文件路径")
	flag.StringVar(&adminPassword, "password", "123456", "管理面板密码（留空则不启用登录验证）")
	flag.BoolVar(&debugMode, "debug", false, "启用调试日志")
	flag.BoolVar(&gatewayMode, "gateway", false, "统一网关模式（记录节点级统计）")
	flag.StringVar(&logLevel, "log-level", "info", "日志级别: debug/info/warn/error")
	flag.StringVar(&logFile, "log-file", "", "日志文件路径（留空输出到 stdout）")
	flag.BoolVar(&showVersion, "version", false, "显示版本信息")
	flag.Parse()

	initLogger()

	if showVersion {
		fmt.Println(versionString())
		return
	}

	cfg := loadConfig(configPath)
	applyConfig(cfg)
	if err := saveConfig(configPath, cfg); err != nil {
		slog.Warn("failed to save config", "path", configPath, "error", err)
	}
	startConfigWatcher(configPath)

	loadTokenStats()
	loadNodeStats()
	initCallLog()
	callLogEnabled = gatewayMode // 仅网关进程记录全流程日志（对齐 node_stats 语义）
	slog.Info("config loaded", "path", configPath)
	initOCSession()
	globalAgg = newAggregator()
	chatRouterVar = newChatRouter(globalAgg)
	refreshModelCatalog()
	modelMu.RLock()
	nLoaded := len(modelsCache)
	modelMu.RUnlock()
	if nLoaded > 0 {
		slog.Info("models loaded", "count", nLoaded)
	}
	startModelRefresh()
	slog.Info("server starting",
		"port", port,
		"log_level", logLevel,
		"models", len(getModelIDs()),
		"aliases", len(modelAlias),
	)
	if adminPassword != "" {
		slog.Info("admin panel enabled", "url", fmt.Sprintf("http://localhost:%s/", port))
	} else {
		slog.Info("admin panel disabled (no password)")
	}
	// P4: 管理域（实例/统计/日志/配置）并入 core，Web/桌面共用一份实现。
	managerInst := manager.New("")
	// P4-3：装配实例/探针接缝（clash 节点解析 + sing-box 配置 + opencode2api 配置生成）。
	managerInst.SetSeams(&manager.SeamFuncs{
		ResolveNode: func(name string) (manager.ClashNode, bool) {
			for _, n := range managerInst.ListNodesWithGroup() {
				if n.Name == name {
					return n, true
				}
			}
			return manager.ClashNode{}, false
		},
		BuildSingbox: func(node manager.ClashNode, port uint16) ([]byte, error) {
			return manager.BuildSingboxConfigFor(node, port)
		},
		BuildOpenCfg: managerInst.BuildOpenCodeCfgFor,
		ListNodes:    managerInst.ListNodesWithGroup,
	})
	http.HandleFunc("/v1/chat/completions", loggingMiddleware(apiKeyAuthMiddleware(chatCompletionsHandler)))
	http.HandleFunc("/v1/responses", loggingMiddleware(apiKeyAuthMiddleware(responsesHandler)))
	http.HandleFunc("/v1/messages", loggingMiddleware(apiKeyAuthMiddleware(claudeMessagesHandler)))
	http.HandleFunc("/v1/models", loggingMiddleware(apiKeyAuthMiddleware(listModelsHandler)))
	http.HandleFunc("/login", loggingMiddleware(loginHandler))
	http.HandleFunc("/logout", loggingMiddleware(logoutHandler))
	http.HandleFunc("/api/config", loggingMiddleware(requireAuth(adminConfigHandler)))
	http.HandleFunc("/api/stats", loggingMiddleware(requireAuth(adminStatsHandler)))
	http.HandleFunc("/api/reset-stats", loggingMiddleware(apiKeyAuthMiddleware(resetStatsHandler)))
	http.HandleFunc("/api/node-status", loggingMiddleware(apiKeyAuthMiddleware(nodeStatusHandler)))
	http.HandleFunc("/api/reload", loggingMiddleware(requireAuth(reloadHandler)))
	// P4: 管理域并入 core（/api/admin/*，鉴权与既有 /api/* 一致；由 core/manager 实现）
	http.HandleFunc("/api/admin/config", loggingMiddleware(requireAuth(managerInst.ConfigGetHandler())))
	http.HandleFunc("/api/admin/config/set", loggingMiddleware(requireAuth(managerInst.ConfigSetHandler())))
	http.HandleFunc("/api/admin/stats", loggingMiddleware(requireAuth(managerInst.StatsHandler())))
	http.HandleFunc("/api/admin/stats/reset", loggingMiddleware(apiKeyAuthMiddleware(managerInst.ResetStatsHandler())))
	http.HandleFunc("/api/admin/call-log", loggingMiddleware(requireAuth(managerInst.CallLogHandler())))
	http.HandleFunc("/api/admin/call-log/clear", loggingMiddleware(requireAuth(managerInst.ClearCallLogHandler())))
	http.HandleFunc("/api/admin/binaries", loggingMiddleware(apiKeyAuthMiddleware(managerInst.BinariesHandler())))
	http.HandleFunc("/api/admin/instances", loggingMiddleware(requireAuth(managerInst.InstancesHandler())))
	// P4-5：装配运行依赖（进程执行器 / 网关 / 扫描），HTTP 管理面用同一份核心。
	managerInst.SetDeps(manager.NewRealRunner(), manager.NewGateway(managerInst, 0), nil)
	// P4-5: 管理域操作面路由（/api/admin/*）。
	http.HandleFunc("/api/admin/nodes", loggingMiddleware(requireAuth(managerInst.NodesHandler())))
	http.HandleFunc("/api/admin/instances/add", loggingMiddleware(requireAuth(managerInst.InstancesAddHandler())))
	http.HandleFunc("/api/admin/instances/remove", loggingMiddleware(requireAuth(managerInst.InstancesRemoveHandler())))
	http.HandleFunc("/api/admin/instances/start", loggingMiddleware(requireAuth(managerInst.InstancesStartHandler())))
	http.HandleFunc("/api/admin/instances/stop", loggingMiddleware(requireAuth(managerInst.InstancesStopHandler())))
	http.HandleFunc("/api/admin/instances/refresh", loggingMiddleware(requireAuth(managerInst.InstancesRefreshHandler())))
	http.HandleFunc("/api/admin/instances/test", loggingMiddleware(requireAuth(managerInst.InstancesTestHandler())))
	http.HandleFunc("/api/admin/instances/batch/add", loggingMiddleware(requireAuth(managerInst.BatchAddHandler())))
	http.HandleFunc("/api/admin/instances/batch/start", loggingMiddleware(requireAuth(managerInst.BatchStartHandler())))
	http.HandleFunc("/api/admin/instances/batch/stop", loggingMiddleware(requireAuth(managerInst.BatchStopHandler())))
	http.HandleFunc("/api/admin/instances/batch/delete", loggingMiddleware(requireAuth(managerInst.BatchDeleteHandler())))
	http.HandleFunc("/api/admin/instances/join-gateway", loggingMiddleware(requireAuth(managerInst.JoinGatewayHandler())))
	http.HandleFunc("/api/admin/port/suggest", loggingMiddleware(requireAuth(managerInst.PortSuggestHandler())))
	http.HandleFunc("/api/admin/port/check", loggingMiddleware(requireAuth(managerInst.PortCheckHandler())))
	http.HandleFunc("/api/admin/scan/start", loggingMiddleware(requireAuth(managerInst.ScanStartHandler())))
	http.HandleFunc("/api/admin/scan/status", loggingMiddleware(requireAuth(managerInst.ScanStatusHandler())))
	http.HandleFunc("/api/admin/scan/stop", loggingMiddleware(requireAuth(managerInst.ScanStopHandler())))
	http.HandleFunc("/api/admin/autostart", loggingMiddleware(requireAuth(managerInst.AutostartGetHandler())))
	http.HandleFunc("/api/admin/autostart/set", loggingMiddleware(requireAuth(managerInst.AutostartSetHandler())))
	http.HandleFunc("/api/admin/data/clean", loggingMiddleware(requireAuth(managerInst.DataCleanHandler())))
	http.HandleFunc("/api/admin/gateway/status", loggingMiddleware(requireAuth(managerInst.GatewayStatusHandler())))
	http.HandleFunc("/api/admin/gateway/route-mode", loggingMiddleware(requireAuth(managerInst.GatewayRouteModeHandler())))
	http.HandleFunc("/api/admin/gateway/stop", loggingMiddleware(requireAuth(managerInst.GatewayStopHandler())))
	http.HandleFunc("/api/admin/pool/restart", loggingMiddleware(requireAuth(managerInst.RestartPoolHandler())))
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	// P4-5: 前端静态托管。仓库构建产物 dist/「存在」时托管 SPA（Web 版），否则退回内嵌管理面板。
	if distDir := frontendDistDir(); distDir != "" {
		http.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(filepath.Join(distDir, "assets")))))
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/", "/index.html":
				http.ServeFile(w, r, filepath.Join(distDir, "index.html"))
			default:
				http.NotFound(w, r)
			}
		})
		slog.Info("frontend dist served", "dir", distDir)
	} else {
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				requireAuth(adminPageHandler)(w, r)
				return
			}
			http.NotFound(w, r)
		})
	}
	addr := ":" + port
	slog.Info("listening", "addr", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		slog.Error("server terminated", "error", err)
		os.Exit(1)
	}
}
