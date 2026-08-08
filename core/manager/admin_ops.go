// 管理域操作面 HTTP 处理器（/api/admin/*，P4-5 前端走 fetch 的端点集）。
// 只加工 JSON；核心逻辑在各模块。由 main 挂载，沿用既有鉴权中间件。
package manager

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// ---------------------------------------------------------------- 节点

// NodeView 节点（前端契约）。
type NodeView struct {
	Name     string `json:"name"`
	NodeType string `json:"node_type"`
	Server   string `json:"server"`
	Port     uint16 `json:"port"`
	HasCred  bool   `json:"has_cred"`
	Group    string `json:"group"`
}

func (m *Manager) nodeViews() []NodeView {
	sf := m.currentSeams()
	if sf.ListNodes == nil {
		return []NodeView{}
	}
	out := []NodeView{}
	for _, n := range sf.ListNodes() {
		out = append(out, NodeView{
			Name: n.Name, NodeType: n.NodeType, Server: n.Server, Port: n.Port,
			HasCred: n.Password != "" || n.UUID != "", Group: n.Group,
		})
	}
	return out
}

// NodesHandler GET /api/admin/nodes。
func (m *Manager) NodesHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, m.nodeViews())
	}
}

// ---------------------------------------------------------------- 实例生命周期

// InstancesAddHandler POST {name,port,node,password} → Instance。
func (m *Manager) InstancesAddHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		var req struct {
			Name     string `json:"name"`
			Port     uint16 `json:"port"`
			Node     string `json:"node"`
			Password string `json:"password"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeErr(w, http.StatusBadRequest, "bad request body")
			return
		}
		if req.Name == "" || req.Node == "" {
			writeErr(w, http.StatusBadRequest, "name/node 必填")
			return
		}
		inst := Instance{
			Name: req.Name, Port: req.Port, Node: req.Node, Password: req.Password,
			SingboxPort: req.Port + 10000, JoinGateway: false,
		}
		if err := m.AddInstance(inst); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		got, _ := m.FindInstance(req.Name)
		writeJSON(w, got)
	}
}

// InstancesRemoveHandler POST {name}。
func (m *Manager) InstancesRemoveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		name, err := decodeName(r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := m.RemoveInstanceAlive(m.Run(), name); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"status": "ok"})
	}
}

// InstancesStartHandler POST {name}。
func (m *Manager) InstancesStartHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		name, err := decodeName(r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := m.StartInstance(m.Run(), name); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"status": "ok"})
	}
}

// InstancesStopHandler POST {name}。
func (m *Manager) InstancesStopHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		name, err := decodeName(r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := m.StopInstance(m.Run(), name); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"status": "ok"})
	}
}

// InstancesRefreshHandler POST {names} → []Instance。
func (m *Manager) InstancesRefreshHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		var req struct {
			Names []string `json:"names"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeErr(w, http.StatusBadRequest, "bad request body")
			return
		}
		writeJSON(w, m.RefreshStates(m.Run(), req.Names))
	}
}

// TestResult 实例测试结果（前端契约）。
type TestResult struct {
	Name       string `json:"name"`
	Port       uint16 `json:"port"`
	OK         bool   `json:"ok"`
	StatusCode *int   `json:"status_code"`
	ModelCount *int   `json:"model_count"`
	Message    string `json:"message"`
	LatencyMS  int64  `json:"latency_ms"`
}

// InstancesTestHandler POST {name} → TestResult（免费模型最小请求实测）。
func (m *Manager) InstancesTestHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		name, err := decodeName(r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		inst, ok := m.FindInstance(name)
		if !ok {
			writeErr(w, http.StatusNotFound, "实例不存在: "+name)
			return
		}
		start := time.Now()
		status, _, modelCount, err := freeCompletion(inst.Port, inst.Password, 15*time.Second)
		res := TestResult{Name: name, Port: inst.Port, LatencyMS: time.Since(start).Milliseconds()}
		sc := status
		res.StatusCode = &sc
		if status >= 200 && status < 300 && err == nil {
			res.OK = true
			if modelCount >= 0 {
				res.ModelCount = &modelCount
			}
			res.Message = "OK"
		} else {
			res.Message = errMsg(err, status)
		}
		writeJSON(w, res)
	}
}

// errMsg 拼接失败描述。
func errMsg(err error, status int) string {
	if err != nil {
		return err.Error()
	}
	return "HTTP " + itoa(uint16(status))
}

// decodeName 读取 {"name":"..."}。
func decodeName(r *http.Request) (string, error) {
	var req struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Name == "" {
		return "", errors.New("请求体需含 name")
	}
	return req.Name, nil
}

// requireMethodOK 校验方法（与 requireMethod 语义一致，返回是否放行）。
func requireMethodOK(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

// ---------------------------------------------------------------- 批量

// BatchAddHTTPItem 批量添加入参（前端 BatchAddItem）。
type BatchAddHTTPItem struct {
	Node string  `json:"node"`
	Name *string `json:"name,omitempty"`
	Port *uint16 `json:"port,omitempty"`
}

// BatchAddEntry / BatchAddHTTPResult 批量添加结果（前端契约）。
type BatchAddEntry struct {
	Name string `json:"name"`
	Port uint16 `json:"port"`
	Node string `json:"node"`
}

type BatchAddErr struct {
	Node  string `json:"node"`
	Error string `json:"error"`
}

type BatchAddHTTPResult struct {
	Added      []BatchAddEntry `json:"added"`
	Errors     []BatchAddErr   `json:"errors"`
	AddedCount int             `json:"added_count"`
	ErrorCount int             `json:"error_count"`
}

// BatchAddHandler POST {nodes:[{node,name?,port?}], basePort?, useNodeName?, namePrefix?}。
func (m *Manager) BatchAddHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		var req struct {
			Nodes       []BatchAddHTTPItem `json:"nodes"`
			BasePort    *uint16            `json:"basePort"`
			UseNodeName *bool              `json:"useNodeName"`
			NamePrefix  string             `json:"namePrefix"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeErr(w, http.StatusBadRequest, "bad body")
			return
		}
		basePort := uint16(defaultBasePort)
		if req.BasePort != nil {
			basePort = *req.BasePort
		}
		useNodeName := false
		if req.UseNodeName != nil {
			useNodeName = *req.UseNodeName
		}
		writeJSON(w, m.httpBatchAdd(req.Nodes, basePort, useNodeName, req.NamePrefix))
	}
}

// BatchOpResult 批量启停结果（前端契约）。
type BatchOpResult struct {
	Success      []string          `json:"success"`
	Errors       map[string]string `json:"errors"`
	SuccessCount int               `json:"success_count"`
	ErrorCount   int               `json:"error_count"`
}

func opResult(res map[string]error) BatchOpResult {
	out := BatchOpResult{Success: []string{}, Errors: map[string]string{}}
	for name, err := range res {
		if err == nil {
			out.Success = append(out.Success, name)
			out.SuccessCount++
		} else {
			out.Errors[name] = err.Error()
			out.ErrorCount++
		}
	}
	return out
}

// BatchStartHandler POST {names}。
func (m *Manager) BatchStartHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		var req struct {
			Names []string `json:"names"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeErr(w, http.StatusBadRequest, "bad body")
			return
		}
		writeJSON(w, opm(m.BatchStart(m.Run(), req.Names)))
	}
}

// BatchStopHandler POST {names:[...]}。
func (m *Manager) BatchStopHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		var req struct {
			Names []string `json:"names"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeErr(w, http.StatusBadRequest, "bad body")
			return
		}
		writeJSON(w, opm(m.BatchStop(m.Run(), req.Names)))
	}
}

// BatchDeleteHandler POST {names:[...]}。
func (m *Manager) BatchDeleteHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		var req struct {
			Names []string `json:"names"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeErr(w, http.StatusBadRequest, "bad body")
			return
		}
		writeJSON(w, opm(m.BatchDelete(m.Run(), req.Names)))
	}
}

// ---------------------------------------------------------------- 端口

// PortSuggestHandler GET → 建议端口。
func (m *Manager) PortSuggestHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodGet) {
			return
		}
		p, err := m.PortSuggest()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, p)
	}
}

// PortCheckHandler GET ?port=N → PortCheckResult。
func (m *Manager) PortCheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodGet) {
			return
		}
		var p uint16
		if _, err := parsePortQuery(r, &p); err != nil {
			writeErr(w, http.StatusBadRequest, "port 必填")
			return
		}
		writeJSON(w, m.PortCheck(p))
	}
}

// ---------------------------------------------------------------- 数据清理 / 自启

// DataCleanHandler POST {level:1|2|3}。
func (m *Manager) DataCleanHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		var req struct {
			Level int `json:"level"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeErr(w, http.StatusBadRequest, "bad body")
			return
		}
		if err := m.DataClean(m.Run(), m.Gateway(), req.Level); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"status": "ok"})
	}
}

// AutostartGetHandler GET → {enabled}（core 不承载自启，由壳层实现；Web 返回 off）。
func (m *Manager) AutostartGetHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, map[string]any{"enabled": false, "platform": "core"})
	}
}

// AutostartSetHandler POST {enabled} → 明确错误（壳层独占）。
func (m *Manager) AutostartSetHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		writeErr(w, http.StatusNotImplemented, "开机自启仅由桌面壳提供（Tauri 壳），Web 无法设置")
	}
}

// opm 批量结果转换。
var opm = opResult

// httpBatchAdd 批量添加（前端契约形态：按节点去重、自动命名、端口冲突+1）。
func (m *Manager) httpBatchAdd(items []BatchAddHTTPItem, basePort uint16, useNodeName bool, prefix string) BatchAddHTTPResult {
	res := BatchAddHTTPResult{Added: []BatchAddEntry{}, Errors: []BatchAddErr{}}
	haveNode := map[string]bool{}
	for _, e := range m.ListInstances() {
		haveNode[e.Node] = true
	}
	next := 1
	for _, item := range items {
		if item.Node == "" {
			res.Errors = append(res.Errors, BatchAddErr{Node: "", Error: "node 必填"})
			res.ErrorCount++
			continue
		}
		if haveNode[item.Node] {
			res.Errors = append(res.Errors, BatchAddErr{Node: item.Node, Error: "节点已存在"})
			res.ErrorCount++
			continue
		}
		name := item.Node
		if !useNodeName || name == "" {
			for {
				name = prefix + "实例" + itoa16(next)
				next++
				if !m.hasInstanceName(name) {
					break
				}
			}
		}
		if m.hasInstanceName(name) {
			res.Errors = append(res.Errors, BatchAddErr{Node: item.Node, Error: "实例名已存在"})
			res.ErrorCount++
			continue
		}
		port := basePort
		if item.Port != nil {
			port = *item.Port
		}
		for m.isPortUsedByInstance(port) || m.isPortUsedByInstance(port+10000) || !isPortFree(port) {
			port++
		}
		inst := Instance{
			Name: name, Port: port, Node: item.Node, Password: genSkKey(),
			SingboxPort: port + 10000, JoinGateway: false,
		}
		if err := m.AddInstance(inst); err != nil {
			res.Errors = append(res.Errors, BatchAddErr{Node: item.Node, Error: err.Error()})
			res.ErrorCount++
			continue
		}
		haveNode[item.Node] = true
		res.Added = append(res.Added, BatchAddEntry{Name: name, Port: port, Node: item.Node})
		res.AddedCount++
	}
	return res
}

func (m *Manager) hasInstanceName(name string) bool {
	for _, e := range m.ListInstances() {
		if e.Name == name {
			return true
		}
	}
	return false
}

// itoa16 小工具（与 itoa 一致）。
func itoa16(v int) string {
	return itoa(uint16(v))
}

// parsePortQuery 从查询参数读取 port。
func parsePortQuery(r *http.Request, out *uint16) (bool, error) {
	s := r.URL.Query().Get("port")
	if s == "" {
		return false, errors.New("missing port")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return false, errors.New("bad port")
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 || n > 65535 {
		return false, errors.New("bad port")
	}
	*out = uint16(n)
	return true, nil
}

// ---------------------------------------------------------------- 扫描

// ScanStartHandler POST（nodes/apiPort/socksPort/timeout/concurrency）。
func (m *Manager) ScanStartHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		var req struct {
			Nodes       []string `json:"nodes"`
			APIPort     *uint16  `json:"apiPort"`
			SocksPort   *uint16  `json:"socksPort"`
			Timeout     *int     `json:"timeout"`
			Concurrency *int     `json:"concurrency"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeErr(w, http.StatusBadRequest, "bad body")
			return
		}
		opts := ScanOptions{Nodes: req.Nodes}
		if req.APIPort != nil {
			opts.APIPort = *req.APIPort
		}
		if req.SocksPort != nil {
			opts.SocksPort = *req.SocksPort
		}
		if req.Timeout != nil {
			opts.TimeoutSec = *req.Timeout
		}
		if req.Concurrency != nil {
			opts.Concurrency = *req.Concurrency
		}
		progress, err := m.Scanner().Start(opts)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, progress)
	}
}

// ScanStatusHandler GET。
func (m *Manager) ScanStatusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, m.Scanner().Snapshot())
	}
}

// ScanStopHandler POST。
func (m *Manager) ScanStopHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		writeJSON(w, m.Scanner().RequestStop())
	}
}

// ---------------------------------------------------------------- 网关 / 重启池

// GatewayStatusHandler GET。
func (m *Manager) GatewayStatusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, m.Gateway().Status(m.Run()))
	}
}

// GatewayRouteModeHandler POST {mode}。
func (m *Manager) GatewayRouteModeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		var req struct {
			Mode string `json:"mode"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.Mode == "" {
			writeErr(w, http.StatusBadRequest, "mode 必填")
			return
		}
		m.Gateway().SetRouteMode(req.Mode)
		_ = m.Gateway().sync(m.Run()) // 立即按新模式重启生效
		writeJSON(w, map[string]any{"status": "ok", "mode": req.Mode})
	}
}

// GatewayStopHandler POST。
func (m *Manager) GatewayStopHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		m.Gateway().stop(m.Run())
		writeJSON(w, map[string]any{"status": "ok"})
	}
}

// JoinGatewayHandler POST {name, join}。
func (m *Manager) JoinGatewayHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		var req struct {
			Name string `json:"name"`
			Join bool   `json:"join"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.Name == "" {
			writeErr(w, http.StatusBadRequest, "name 必填")
			return
		}
		inst, ok := m.FindInstance(req.Name)
		if !ok {
			writeErr(w, http.StatusNotFound, "实例不存在")
			return
		}
		inst.JoinGateway = req.Join
		if err := m.UpdateInstance(inst); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		_ = m.Gateway().sync(m.Run())
		writeJSON(w, map[string]any{"status": "ok"})
	}
}

// RestartPoolHandler POST → RestartPoolResult。
func (m *Manager) RestartPoolHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		writeJSON(w, m.RestartPool(m.Run(), m.Gateway()))
	}
}
