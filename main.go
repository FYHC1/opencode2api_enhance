package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/6Kmfi6HP/opencode2api/core/manager"
)

// frontendDistDir 返回前端构建产物目录（存在 dist/index.html 时）。
// 查找顺序：exe 旁 → 当前工作目录；dev（tauri dev，cwd=src-tauri）额外向上找仓库根 dist。
func frontendDistDir() string {
	var cands []string
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		cands = append(cands,
			filepath.Join(exeDir, "dist"),                // 便携包：exe 旁
			filepath.Join(exeDir, "..", "..", "..", "dist"), // dev：target/debug/bin → 仓库根
		)
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands,
			filepath.Join(wd, "dist"),   // 常规：cwd/dist
			filepath.Join(wd, "..", "dist"), // dev：cwd=src-tauri → 仓库根
		)
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
	version = "v1.2.0"
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


	mux := http.NewServeMux()
	registerHTTPRoutes(mux, managerInst)
	addr := ":" + port
	slog.Info("listening", "addr", addr)
	if err := http.ListenAndServe(addr, withRecover(mux)); err != nil {
		slog.Error("server terminated", "error", err)
		os.Exit(1)
	}
}

// withRecover 全局 panic 兜底：任何 handler panic 都会记录堆栈并返回 500 JSON，
// 而不是 net/http 默认的空 500（前端无法定位、表现为"按钮无效"）。
func withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("handler panic",
					"method", r.Method,
					"path", r.URL.Path,
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal error: handler panic"}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// registerHTTPRoutes 注册全部 HTTP 路由（/v1、/api、/api/admin、/health、静态 SPA）。
func registerHTTPRoutes(mux *http.ServeMux, managerInst *manager.Manager) {
	mux.HandleFunc("/v1/chat/completions", loggingMiddleware(apiKeyAuthMiddleware(chatCompletionsHandler)))
	mux.HandleFunc("/v1/responses", loggingMiddleware(apiKeyAuthMiddleware(responsesHandler)))
	mux.HandleFunc("/v1/messages", loggingMiddleware(apiKeyAuthMiddleware(claudeMessagesHandler)))
	mux.HandleFunc("/v1/models", loggingMiddleware(apiKeyAuthMiddleware(listModelsHandler)))
	mux.HandleFunc("/login", loggingMiddleware(loginHandler))
	mux.HandleFunc("/logout", loggingMiddleware(logoutHandler))
	mux.HandleFunc("/api/config", loggingMiddleware(requireAuth(adminConfigHandler)))
	mux.HandleFunc("/api/stats", loggingMiddleware(requireAuth(adminStatsHandler)))
	mux.HandleFunc("/api/reset-stats", loggingMiddleware(apiKeyAuthMiddleware(resetStatsHandler)))
	mux.HandleFunc("/api/node-status", loggingMiddleware(apiKeyAuthMiddleware(nodeStatusHandler)))
	mux.HandleFunc("/api/reload", loggingMiddleware(requireAuth(reloadHandler)))
	// P4: 管理域并入 core（/api/admin/*，鉴权与既有 /api/* 一致；由 core/manager 实现）
	mux.HandleFunc("/api/admin/config", loggingMiddleware(requireAuth(managerInst.ConfigGetHandler())))
	mux.HandleFunc("/api/admin/config/set", loggingMiddleware(requireAuth(managerInst.ConfigSetHandler())))
	mux.HandleFunc("/api/admin/stats", loggingMiddleware(requireAuth(managerInst.StatsHandler())))
	mux.HandleFunc("/api/admin/stats/reset", loggingMiddleware(apiKeyAuthMiddleware(managerInst.ResetStatsHandler())))
	mux.HandleFunc("/api/admin/call-log", loggingMiddleware(requireAuth(managerInst.CallLogHandler())))
	mux.HandleFunc("/api/admin/call-log/clear", loggingMiddleware(requireAuth(managerInst.ClearCallLogHandler())))
	mux.HandleFunc("/api/admin/binaries", loggingMiddleware(apiKeyAuthMiddleware(managerInst.BinariesHandler())))
	mux.HandleFunc("/api/admin/instances", loggingMiddleware(requireAuth(managerInst.InstancesHandler())))
	// P4-5：装配运行依赖（进程执行器 / 网关 / 扫描），HTTP 管理面用同一份核心。
	managerInst.SetDeps(manager.NewRealRunner(), manager.NewGateway(managerInst, 0), nil)
	// M1: 订阅自动拉取后台循环（配置 subscribe_url/interval_min 生效时运行，配置热更新无需重启）。
	managerInst.StartSubscribeLoop()
	// M2: 健康巡检后台循环（配置 health_check_interval_sec 生效时运行）。
	managerInst.StartHealthLoop()
	// P4-5: 管理域操作面路由（/api/admin/*）。
	mux.HandleFunc("/api/admin/nodes", loggingMiddleware(requireAuth(managerInst.NodesHandler())))
	mux.HandleFunc("/api/admin/instances/add", loggingMiddleware(requireAuth(managerInst.InstancesAddHandler())))
	mux.HandleFunc("/api/admin/instances/remove", loggingMiddleware(requireAuth(managerInst.InstancesRemoveHandler())))
	mux.HandleFunc("/api/admin/instances/start", loggingMiddleware(requireAuth(managerInst.InstancesStartHandler())))
	mux.HandleFunc("/api/admin/instances/stop", loggingMiddleware(requireAuth(managerInst.InstancesStopHandler())))
	mux.HandleFunc("/api/admin/instances/refresh", loggingMiddleware(requireAuth(managerInst.InstancesRefreshHandler())))
	mux.HandleFunc("/api/admin/instances/test", loggingMiddleware(requireAuth(managerInst.InstancesTestHandler())))
	mux.HandleFunc("/api/admin/instances/batch/add", loggingMiddleware(requireAuth(managerInst.BatchAddHandler())))
	mux.HandleFunc("/api/admin/instances/batch/start", loggingMiddleware(requireAuth(managerInst.BatchStartHandler())))
	mux.HandleFunc("/api/admin/instances/batch/stop", loggingMiddleware(requireAuth(managerInst.BatchStopHandler())))
	mux.HandleFunc("/api/admin/instances/batch/delete", loggingMiddleware(requireAuth(managerInst.BatchDeleteHandler())))
	mux.HandleFunc("/api/admin/instances/join-gateway", loggingMiddleware(requireAuth(managerInst.JoinGatewayHandler())))
	mux.HandleFunc("/api/admin/port/suggest", loggingMiddleware(requireAuth(managerInst.PortSuggestHandler())))
	mux.HandleFunc("/api/admin/port/check", loggingMiddleware(requireAuth(managerInst.PortCheckHandler())))
	mux.HandleFunc("/api/admin/scan/start", loggingMiddleware(requireAuth(managerInst.ScanStartHandler())))
	mux.HandleFunc("/api/admin/scan/status", loggingMiddleware(requireAuth(managerInst.ScanStatusHandler())))
	mux.HandleFunc("/api/admin/scan/stop", loggingMiddleware(requireAuth(managerInst.ScanStopHandler())))
	mux.HandleFunc("/api/admin/autostart", loggingMiddleware(requireAuth(managerInst.AutostartGetHandler())))
	mux.HandleFunc("/api/admin/autostart/set", loggingMiddleware(requireAuth(managerInst.AutostartSetHandler())))
	// 订阅拉取与批量导入（main 分支功能迁移 M1）。
	mux.HandleFunc("/api/admin/subscribe/preview", loggingMiddleware(requireAuth(managerInst.SubscribePreviewHandler())))
	mux.HandleFunc("/api/admin/subscribe/import", loggingMiddleware(requireAuth(managerInst.SubscribeImportHandler())))
	mux.HandleFunc("/api/admin/subscribe/import-pool", loggingMiddleware(requireAuth(managerInst.SubscribeImportPoolHandler())))
	// 健康巡检（main 分支功能迁移 M2）。
	mux.HandleFunc("/api/admin/health/check", loggingMiddleware(requireAuth(managerInst.HealthCheckHandler())))
	mux.HandleFunc("/api/admin/health/summary", loggingMiddleware(requireAuth(managerInst.HealthSummaryHandler())))
	// 报表导出（main 分支功能迁移 M3）。
	mux.HandleFunc("/api/admin/export/call-log.csv", loggingMiddleware(requireAuth(managerInst.ExportCallLogCSVHandler())))
	mux.HandleFunc("/api/admin/export/instances.json", loggingMiddleware(requireAuth(managerInst.ExportInstancesJSONHandler())))
	mux.HandleFunc("/api/admin/export/stats.json", loggingMiddleware(requireAuth(managerInst.ExportStatsJSONHandler())))
	mux.HandleFunc("/api/admin/data/clean", loggingMiddleware(requireAuth(managerInst.DataCleanHandler())))
	mux.HandleFunc("/api/admin/gateway/status", loggingMiddleware(requireAuth(managerInst.GatewayStatusHandler())))
	mux.HandleFunc("/api/admin/gateway/route-mode", loggingMiddleware(requireAuth(managerInst.GatewayRouteModeHandler())))
	mux.HandleFunc("/api/admin/gateway/stop", loggingMiddleware(requireAuth(managerInst.GatewayStopHandler())))
	mux.HandleFunc("/api/admin/pool/restart", loggingMiddleware(requireAuth(managerInst.RestartPoolHandler())))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	// P4-5: 前端静态托管。仓库构建产物 dist/「存在」时托管 SPA（Web 版），否则退回内嵌管理面板。
	if distDir := frontendDistDir(); distDir != "" {
		mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(filepath.Join(distDir, "assets")))))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/", "/index.html":
				http.ServeFile(w, r, filepath.Join(distDir, "index.html"))
			default:
				http.NotFound(w, r)
			}
		})
		slog.Info("frontend dist served", "dir", distDir)
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				requireAuth(adminPageHandler)(w, r)
				return
			}
			http.NotFound(w, r)
		})
	}

}
