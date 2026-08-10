// 统一网关管理（Rust gateway.rs 移植）。
// 网关 = 一个以 -gateway 启动的 opencode2api 子进程，cwd=runtime/_unified-gateway，
// 把「运行中且 join_gateway」的实例 sing-box 端口聚成代理池（round_robin/smart/failover）。
package manager

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// UnifiedGatewayPort 统一网关端口（release；debug 由壳层覆盖，值同 tcp_unifiedGatewayPort）。
const UnifiedGatewayPort uint16 = unifiedGatewayPort

// gatewayDirBase 网关运行目录名（runtime/_unified-gateway）。
const gatewayDirBase = "_unified-gateway"

// GatewayStatus 网关状态（前端契约，Rust GatewayStatus 对齐）。
type GatewayStatus struct {
	Running      bool     `json:"running"`
	Address      string   `json:"address"`
	Port         uint16   `json:"port"`
	APIKey       string   `json:"api_key"`
	RunningInsts int      `json:"running_instances"`
	TotalInsts   int      `json:"total_instances"`
	Message      string   `json:"message"`
	RouteMode    string   `json:"route_mode"`
	FreeModels   []string `json:"free_models"`
	UpdatedAt    *int64   `json:"free_models_updated_at,omitempty"`
	ModelsErr    string   `json:"free_models_error,omitempty"`
}

// Gateway 管理统一网关子进程。
type Gateway struct {
	m         *Manager
	port      uint16
	password  string
	routeMode string

	mu      sync.Mutex
	pid     int
	lastErr string

	models    []string
	updatedAt int64
	lastFetch int64
	modelsErr string
	loading   bool
	loadingMu sync.Mutex
}

// managerGatewayPort 网关端口：优先环境变量 OPCODE2API_GATEWAY_PORT（debug/release 隔离），
// 其次 config.gateway_port，否则默认 unifiedGatewayPort（release 18080）。
func (m *Manager) managerGatewayPort() uint16 {
	if s := os.Getenv("OPCODE2API_GATEWAY_PORT"); s != "" {
		if n := parsePositiveInt(s); n > 0 && n < 65536 {
			return uint16(n)
		}
	}
	if m != nil {
		if p := m.loadConfig().GatewayPort; p > 0 {
			return p
		}
	}
	return unifiedGatewayPort
}

// NewGateway 构造网关管理器。
func NewGateway(m *Manager, port uint16) *Gateway {
	if port == 0 {
		port = m.managerGatewayPort()
	}
	return &Gateway{m: m, port: port, password: effectiveGatewayKey(m.loadConfig()), routeMode: "smart"}
}

// Port 返回网关端口。
func (g *Gateway) Port() uint16 { return g.port }

// SetRouteMode 记录路由模式（下次 sync 生效）。
func (g *Gateway) SetRouteMode(mode string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.routeMode = mode
}

// RouteMode 当前路由模式。
func (g *Gateway) RouteMode() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.routeMode
}

func (g *Gateway) gatewayDir() string {
	return filepath.Join(g.m.Paths().RuntimeDir, gatewayDirBase)
}

// isRunning 检查子进程存活。
func (g *Gateway) isRunning(runner Runner) bool {
	g.mu.Lock()
	pid := g.pid
	g.mu.Unlock()
	return pid > 0 && pidAlive(pid)
}

// stopChild 终止网关子进程（幂等）。
func (g *Gateway) stopChild(runner Runner) {
	if runner == nil {
		runner = &realRunner{}
	}
	g.mu.Lock()
	pid := g.pid
	g.pid = 0
	g.mu.Unlock()
	if pid > 0 {
		_ = runner.Kill(pid)
	}
}

// startChild 启动网关子进程（-gateway 模式）。
func (g *Gateway) startChild(runner Runner) error {
	if runner == nil {
		runner = &realRunner{}
	}
	dir := g.gatewayDir()
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(dir, "opencode2api.json")
	pid, err := runner.Start(ExecSpec{
		Bin: g.m.binPath("opencode2api"),
		Args: []string{
			"-port", itoa(g.port),
			"-config", cfgPath,
			"-password", g.password,
			"-gateway",
			"-log-level", "warn",
		},
		Dir:      dir,
		LogOut:   filepath.Join(dir, "logs", "opencode2api.out.log"),
		LogErr:   filepath.Join(dir, "logs", "opencode2api.err.log"),
		NoWindow: true,
	})
	if err != nil {
		g.setErr(fmt.Sprintf("启动统一 API 网关失败: %v", err))
		return err
	}
	g.mu.Lock()
	g.pid = pid
	g.lastErr = ""
	g.mu.Unlock()
	return nil
}

func (g *Gateway) setErr(msg string) {
	g.mu.Lock()
	g.lastErr = msg
	g.mu.Unlock()
}

// sync 同步网关池：成员 = Running 且 JoinGateway 的实例 sing-box 端口。
// 无成员 → 停网关；配置变化且运行中 → 重启以热生效；未运行 → 拉起。
func (g *Gateway) sync(runner Runner) error {
	insts := g.m.ListInstances()
	var ports []uint16
	portNames := map[uint16]string{}
	for _, inst := range insts {
		if inst.Status.State == "Running" && inst.JoinGateway {
			ports = append(ports, inst.SingboxPort)
			portNames[inst.SingboxPort] = inst.Name
		}
	}
	if len(ports) == 0 {
		g.stopChild(runner)
		g.clearModels()
		return nil
	}

	cfg, err := g.m.buildRouterCfg(ports, portNames, g.RouteMode())
	if err != nil {
		return err
	}
	dir := g.gatewayDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(dir, "opencode2api.json")
	changed := true
	if old, err := os.ReadFile(cfgPath); err == nil {
		changed = string(old) != string(cfg)
	}
	if changed {
		if err := os.WriteFile(cfgPath, cfg, 0o644); err != nil {
			return err
		}
	}

	running := g.isRunning(runner)
	if changed && running {
		g.stopChild(runner)
		if err := g.startChild(runner); err != nil {
			return err
		}
	} else if !running {
		if err := g.startChild(runner); err != nil {
			return err
		}
	}
	g.refreshModels()
	return nil
}

// stop 停机（同步调用方用于 restart_pool/data_clean）。
func (g *Gateway) stop(runner Runner) {
	g.stopChild(runner)
	g.clearModels()
}

// Status 查询状态；池非空而网关未运行则自动拉起。
func (g *Gateway) Status(runner Runner) GatewayStatus {
	total := len(g.m.ListInstances())
	running := g.isRunning(runner)
	if !running && g.memberCount() > 0 {
		if err := g.startChild(runner); err != nil {
			g.setErr(err.Error())
		} else {
			running = true
		}
	}
	if running {
		g.refreshModels()
	}
	g.mu.Lock()
	message := "暂无运行中的实例"
	if g.memberCount() > 0 {
		if running {
			message = "已启动，遇到限流或节点错误会自动切换（failover）"
		} else if g.lastErr != "" {
			message = g.lastErr
		} else {
			message = "统一网关未启动"
		}
	}
	// 没有运行中的池成员（实例全停）时：即使网关进程残留，探测错误也应显示
	// 友好提示而非 dial tcp 之类底层错误（残留 modelsErr 一并清理）
	modelsErr := g.modelsErr
	var models []string
	if g.memberCount() == 0 {
		if modelsErr != "" {
			modelsErr = "请先启动下方节点实例"
		}
		models = []string{}
	} else {
		models = append([]string{}, g.models...)
	}
	updated := g.updatedAt
	port := g.port
	routeMode := g.routeMode
	g.mu.Unlock()

	var updatedPtr *int64
	if updated > 0 {
		updatedPtr = &updated
	}
	return GatewayStatus{
		Running:      running,
		Address:      fmt.Sprintf("http://127.0.0.1:%d/v1", port),
		Port:         port,
		APIKey:       g.password,
		RunningInsts: g.memberCount(),
		TotalInsts:   total,
		Message:      message,
		RouteMode:    routeMode,
		FreeModels:   models,
		UpdatedAt:    updatedPtr,
		ModelsErr:    modelsErr,
	}
}

// memberCount 池成员数（Running 且 JoinGateway）。
func (g *Gateway) memberCount() int {
	n := 0
	for _, inst := range g.m.ListInstances() {
		if inst.Status.State == "Running" && inst.JoinGateway {
			n++
		}
	}
	return n
}

// refreshModels 异步抓取网关免费模型（节流：更新后 60s 内不重试、尝试间 10s）。
func (g *Gateway) refreshModels() {
	g.loadingMu.Lock()
	if g.loading {
		g.loadingMu.Unlock()
		return
	}
	now := time.Now().Unix()
	g.mu.Lock()
	need := now-g.lastFetch >= 10 && (g.updatedAt == 0 || now-g.updatedAt >= 60)
	g.mu.Unlock()
	if !need {
		g.loadingMu.Unlock()
		return
	}
	g.loading = true
	g.loadingMu.Unlock()
	g.mu.Lock()
	g.lastFetch = now
	g.mu.Unlock()

	go func() {
		defer func() {
			g.loadingMu.Lock()
			g.loading = false
			g.loadingMu.Unlock()
		}()
		models, err := fetchGatewayModels(g.port, g.password)
		g.mu.Lock()
		defer g.mu.Unlock()
		if err != nil {
			g.modelsErr = err.Error()
			return
		}
		g.models = models
		g.updatedAt = time.Now().Unix()
		g.modelsErr = ""
	}()
}

func (g *Gateway) clearModels() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.models = nil
	g.updatedAt = 0
	g.lastFetch = 0
	g.modelsErr = ""
}

// fetchGatewayModels 请求 http://127.0.0.1:<port>/v1/models，返回免费模型 id。
func fetchGatewayModels(port uint16, key string) ([]string, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/models", port)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /v1/models HTTP %d", resp.StatusCode)
	}
	var v struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	var out []string
	for _, m := range v.Data {
		if isFreeModelID(m.ID) {
			out = append(out, m.ID)
		}
	}
	return out, nil
}
