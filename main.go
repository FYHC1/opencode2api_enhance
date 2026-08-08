package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

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
	models, err := fetchModels()
	if err != nil {
		slog.Warn("failed to fetch models on startup", "error", err)
	} else {
		modelMu.Lock()
		modelsCache = models
		modelsLoaded = true
		modelMu.Unlock()
		slog.Info("models loaded", "count", len(models))
	}

	goModels, goErr := fetchGoModels()
	if goErr != nil {
		slog.Warn("failed to fetch go catalog on startup", "error", goErr)
	} else {
		modelMu.Lock()
		goModelsCache = goModels
		modelMu.Unlock()
		slog.Info("go catalog loaded", "count", len(goModels))
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
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			requireAuth(adminPageHandler)(w, r)
			return
		}
		http.NotFound(w, r)
	})
	addr := ":" + port
	slog.Info("listening", "addr", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		slog.Error("server terminated", "error", err)
		os.Exit(1)
	}
}
