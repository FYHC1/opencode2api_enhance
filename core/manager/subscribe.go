// 订阅拉取与解析（Rust subscribe.rs 移植）：
// Clash YAML / V2Ray base64 / 明文链接（vmess/vless/trojan/ss/hysteria2）。
//
// 拉取的订阅节点持久化到本地缓存（dataDir/subscription.json），
// 节点列表（ListNodesWithGroup）一并读取，保证：
//   - 实例启动时能按节点名找到完整配置生成 sing-box
//   - 节点池页面可展示订阅节点
//
// 端点：/api/admin/subscribe/preview|import|import-pool
package manager

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// SubscribeNode 订阅节点（轻量结构，可落为实例；raw 保留原始链接）。
// JSON 字段与 Rust SubscribeNode（serde snake_case）一致。
type SubscribeNode struct {
	Name     string `json:"name"`
	Server   string `json:"server"`
	Port     uint16 `json:"port"`
	NodeType string `json:"node_type"`
	Password string `json:"password,omitempty"`
	UUID     string `json:"uuid,omitempty"`
	Cipher   string `json:"cipher,omitempty"`
	SNI      string `json:"sni,omitempty"`
	Network  string `json:"network,omitempty"`
	WsPath   string `json:"ws_path,omitempty"`
	Flow     string `json:"flow,omitempty"`
	TLS      bool   `json:"tls"`
	Raw      string `json:"raw"`
}

// fetchSubscription 拉取并解析订阅 URL，返回节点列表。
func fetchSubscription(url string) ([]SubscribeNode, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("订阅拉取失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("订阅拉取失败: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("读取订阅内容失败: %v", err)
	}
	return parseSubscription(string(body))
}

// parseSubscription 解析订阅内容（自动识别 Clash YAML / base64 / 明文链接）。
func parseSubscription(body string) ([]SubscribeNode, error) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(body, "\uFEFF"))
	if trimmed == "" {
		return nil, fmt.Errorf("订阅内容为空")
	}
	if strings.HasPrefix(trimmed, "proxies:") || (strings.Contains(trimmed, "proxies:") && strings.Contains(trimmed, "type:")) {
		nodes, err := parseClashYAML(trimmed)
		if err != nil {
			return nil, fmt.Errorf("解析 Clash YAML 失败: %v", err)
		}
		out := make([]SubscribeNode, 0, len(nodes))
		for i := range nodes {
			out = append(out, subscribeFromClash(nodes[i]))
		}
		return out, nil
	}
	if isBase64Like(trimmed) {
		text, err := decodeBase64String(trimmed)
		if err == nil {
			return parsePlainLinks(text)
		}
	}
	return parsePlainLinks(trimmed)
}

// subscribeFromClash ClashNode → SubscribeNode。
func subscribeFromClash(n ClashNode) SubscribeNode {
	sni := n.SNI
	if sni == "" {
		sni = n.ServerName
	}
	tls := true
	if n.TLS != nil {
		tls = *n.TLS
	}
	return SubscribeNode{
		Name:     n.Name,
		Server:   n.Server,
		Port:     n.Port,
		NodeType: n.NodeType,
		Password: n.Password,
		UUID:     n.UUID,
		Cipher:   n.Cipher,
		SNI:      sni,
		Network:  n.Network,
		WsPath:   n.WsPath,
		Flow:     n.Flow,
		TLS:      tls,
		Raw:      fmt.Sprintf("%s@%s:%d", n.NodeType, n.Server, n.Port),
	}
}

// isBase64Like 粗判 base64：长度 >40 且前 60 字符均为 base64 字母表。
func isBase64Like(s string) bool {
	if len(s) <= 40 {
		return false
	}
	n := len(s)
	if n > 60 {
		n = 60
	}
	for _, c := range s[:n] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '=' || c == '-') {
			return false
		}
	}
	return true
}

// decodeBase64String 宽容 base64 解码：先带 padding，再无 padding。
func decodeBase64String(s string) (string, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), nil
		}
	}
	return "", fmt.Errorf("base64 解码失败")
}

// parsePlainLinks 逐行解析 vmess:// vless:// trojan:// ss:// hysteria2:// 链接。
func parsePlainLinks(s string) ([]SubscribeNode, error) {
	var nodes []SubscribeNode
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if node, ok, err := parseURILink(line); err != nil {
			// 对齐 Rust：跳过无法解析的行（仅记录，不中断）
			continue
		} else if ok {
			nodes = append(nodes, node)
		}
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("订阅内容中未解析到任何可用节点")
	}
	return nodes, nil
}

// parseURILink 识别并解析单个 v2ray 风格链接。
func parseURILink(line string) (SubscribeNode, bool, error) {
	switch {
	case strings.HasPrefix(line, "vmess://"):
		n, err := parseVmess(strings.TrimPrefix(line, "vmess://"))
		if err != nil || n.Port == 0 {
			return SubscribeNode{}, false, err
		}
		return n, true, nil
	case strings.HasPrefix(line, "vless://"):
		n, err := parseVless(strings.TrimPrefix(line, "vless://"))
		return n, true, err
	case strings.HasPrefix(line, "trojan://"):
		n, err := parseTrojan(strings.TrimPrefix(line, "trojan://"))
		return n, true, err
	case strings.HasPrefix(line, "ss://"):
		n, err := parseSS(strings.TrimPrefix(line, "ss://"))
		return n, true, err
	case strings.HasPrefix(line, "hysteria2://"):
		n, err := parseHysteria2(strings.TrimPrefix(line, "hysteria2://"))
		return n, true, err
	case strings.HasPrefix(line, "hy2://"):
		n, err := parseHysteria2(strings.TrimPrefix(line, "hy2://"))
		return n, true, err
	}
	return SubscribeNode{}, false, nil
}

// parseVmess vmess://base64(JSON)。JSON 字段：add/port/ps/id/scy/sni/net/path/tls。
func parseVmess(rest string) (SubscribeNode, error) {
	text, err := decodeBase64String(rest)
	if err != nil {
		return SubscribeNode{}, fmt.Errorf("vmess:// 非 base64 编码: %v", err)
	}
	var v map[string]any
	if json.Unmarshal([]byte(text), &v) != nil {
		return SubscribeNode{}, fmt.Errorf("vmess JSON 解析失败")
	}
	server, _ := v["add"].(string)
	port := jsonUint16(v["port"])
	name, _ := v["ps"].(string)
	if name == "" {
		name = server
	}
	if server == "" || port == 0 {
		return SubscribeNode{}, nil
	}
	tls := v["tls"] == "tls"
	return SubscribeNode{
		Name:     name,
		Server:   server,
		Port:     port,
		NodeType: "vmess",
		UUID:     asString(v["id"]),
		Cipher:   asDefaultString(v["scy"], "auto"),
		SNI:      asString(v["sni"]),
		Network:  asDefaultString(v["net"], "tcp"),
		WsPath:   asString(v["path"]),
		TLS:      tls,
		Raw:      "vmess://" + rest,
	}, nil
}

// parseVless vless://uuid@host:port?params#name。
func parseVless(rest string) (SubscribeNode, error) {
	auth, name := splitFragment(rest)
	userinfo, hostport, ok := strings.Cut(auth, "@")
	if !ok {
		return SubscribeNode{}, fmt.Errorf("vless 链接缺少 @")
	}
	server, port, err := splitHostPort(hostport)
	if err != nil {
		return SubscribeNode{}, err
	}
	params := parseQuery(auth)
	uuid := strings.SplitN(userinfo, "?", 2)[0]
	network := params["type"]
	if network == "" {
		network = "tcp"
	}
	security := params["security"]
	path := params["path"]
	if path == "" {
		if h := params["host"]; h != "" && !strings.HasPrefix(h, ".") {
			path = h
		}
	}
	nname := name
	if nname == "" {
		nname = server
	}
	return SubscribeNode{
		Name:     nname,
		Server:   server,
		Port:     port,
		NodeType: "vless",
		UUID:     uuid,
		SNI:      params["sni"],
		Network:  network,
		WsPath:   path,
		Flow:     params["flow"],
		TLS:      security == "tls" || security == "reality",
		Raw:      "vless://" + rest,
	}, nil
}

// parseTrojan trojan://password@host:port?params#name。
func parseTrojan(rest string) (SubscribeNode, error) {
	auth, name := splitFragment(rest)
	userinfo, hostport, ok := strings.Cut(auth, "@")
	if !ok {
		return SubscribeNode{}, fmt.Errorf("trojan 链接缺少 @")
	}
	server, port, err := splitHostPort(hostport)
	if err != nil {
		return SubscribeNode{}, err
	}
	params := parseQuery(auth)
	password := strings.SplitN(userinfo, "?", 2)[0]
	nname := name
	if nname == "" {
		nname = server
	}
	network := params["type"]
	if network == "" {
		network = "tcp"
	}
	tls := params["security"] != "none"
	return SubscribeNode{
		Name:     nname,
		Server:   server,
		Port:     port,
		NodeType: "trojan",
		Password: password,
		SNI:      params["sni"],
		Network:  network,
		WsPath:   params["path"],
		TLS:      tls,
		Raw:      "trojan://" + rest,
	}, nil
}

// parseSS ss://base64(method:password)@host:port#name 或 ss://base64(method:password@host:port)#name。
func parseSS(rest string) (SubscribeNode, error) {
	auth, name := splitFragment(rest)
	userinfo := auth
	hostport := ""
	if u, h, ok := strings.Cut(auth, "@"); ok {
		userinfo, hostport = u, h
	} else {
		text, err := decodeBase64String(auth)
		if err != nil {
			return SubscribeNode{}, fmt.Errorf("ss:// 非 base64 编码: %v", err)
		}
		u, h, ok := strings.Cut(text, "@")
		if !ok {
			return SubscribeNode{}, fmt.Errorf("ss 链接缺少 @")
		}
		userinfo, hostport = u, h
	}
	if hostport == "" {
		return SubscribeNode{}, fmt.Errorf("ss 链接缺少服务器地址")
	}
	server, port, err := splitHostPort(hostport)
	if err != nil {
		return SubscribeNode{}, err
	}
	method, password := userinfo, ""
	if m, p, ok := strings.Cut(userinfo, ":"); ok {
		method, password = m, p
	} else if text, err := decodeBase64String(userinfo); err == nil {
		if m, p, ok := strings.Cut(text, ":"); ok {
			method, password = m, p
		}
	}
	nname := name
	if nname == "" {
		nname = server
	}
	return SubscribeNode{
		Name:     nname,
		Server:   server,
		Port:     port,
		NodeType: "ss",
		Password: password,
		Cipher:   method,
		Network:  "tcp",
		TLS:      false,
		Raw:      "ss://" + rest,
	}, nil
}

// parseHysteria2 hysteria2://password@host:port?params#name。
func parseHysteria2(rest string) (SubscribeNode, error) {
	auth, name := splitFragment(rest)
	userinfo, hostport, ok := strings.Cut(auth, "@")
	if !ok {
		return SubscribeNode{}, fmt.Errorf("hysteria2 链接缺少 @")
	}
	server, port, err := splitHostPort(hostport)
	if err != nil {
		return SubscribeNode{}, err
	}
	params := parseQuery(auth)
	password := strings.SplitN(userinfo, "?", 2)[0]
	nname := name
	if nname == "" {
		nname = server
	}
	return SubscribeNode{
		Name:     nname,
		Server:   server,
		Port:     port,
		NodeType: "hysteria2",
		Password: password,
		SNI:      params["sni"],
		TLS:      true,
		Raw:      "hysteria2://" + rest,
	}, nil
}

// splitFragment 拆分 #名称，返回 (去除 fragment 的主体, 名称)。
func splitFragment(s string) (string, string) {
	if head, frag, ok := strings.Cut(s, "#"); ok {
		return head, strings.TrimSpace(frag)
	}
	return s, ""
}

// splitHostPort 解析 host:port（? 后的 query 截断）。IPv6 括号形式与 Rust 一致不支持。
func splitHostPort(hostport string) (string, uint16, error) {
	host, portStr, ok := strings.Cut(hostport, ":")
	if !ok {
		return "", 0, fmt.Errorf("链接缺少端口: %s", hostport)
	}
	portStr = strings.SplitN(portStr, "?", 2)[0]
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("端口无效: %s", portStr)
	}
	return host, uint16(port), nil
}

// parseQuery 解析链接 query（取第一个 ? 之后的 & 对）。
func parseQuery(full string) map[string]string {
	m := map[string]string{}
	_, q, _ := strings.Cut(full, "?")
	for _, pair := range strings.Split(q, "&") {
		if k, v, ok := strings.Cut(pair, "="); ok && k != "" {
			m[k] = v
		}
	}
	return m
}

// ---------- 订阅缓存（dataDir/subscription.json） ----------

// subscriptionCachePath 订阅缓存文件路径。
func (m *Manager) subscriptionCachePath() string {
	return filepath.Join(m.paths.DataDir, "subscription.json")
}

// saveSubscriptionCache 持久化订阅节点缓存。
func (m *Manager) saveSubscriptionCache(nodes []SubscribeNode) error {
	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化订阅缓存失败: %v", err)
	}
	return writeFileMkdir(m.subscriptionCachePath(), data)
}

// loadSubscriptionCache 读取订阅缓存（不存在/损坏返回空）。
func (m *Manager) loadSubscriptionCache() []SubscribeNode {
	data, err := os.ReadFile(m.subscriptionCachePath())
	if err != nil {
		return nil
	}
	var nodes []SubscribeNode
	if json.Unmarshal(data, &nodes) != nil {
		return nil
	}
	return nodes
}

// RemoveSubscriptionNode 从订阅缓存删除节点（按名称），返回删除数量。
// 供节点池「删除节点」——仅订阅缓存中的节点可删（外部 Clash 节点只读）。
func (m *Manager) RemoveSubscriptionNode(name string) (int, error) {
	nodes := m.loadSubscriptionCache()
	before := len(nodes)
	filtered := nodes[:0]
	for _, n := range nodes {
		if n.Name != name {
			filtered = append(filtered, n)
		}
	}
	if len(filtered) == before {
		return 0, nil
	}
	if err := m.saveSubscriptionCache(filtered); err != nil {
		return 0, err
	}
	return before - len(filtered), nil
}

// RemoveSubscriptionNodes 批量删除订阅缓存节点（一次加载+持久化），返回删除数量。
// 已入实例的节点照常列入（实例仍保留其完整配置），外部 Clash 节点静默跳过。
func (m *Manager) RemoveSubscriptionNodes(names []string) (int, error) {
	if len(names) == 0 {
		return 0, nil
	}
	wanted := map[string]bool{}
	for _, n := range names {
		wanted[n] = true
	}
	nodes := m.loadSubscriptionCache()
	before := len(nodes)
	filtered := nodes[:0]
	for _, n := range nodes {
		if !wanted[n.Name] {
			filtered = append(filtered, n)
		}
	}
	if len(filtered) == before {
		return 0, nil
	}
	if err := m.saveSubscriptionCache(filtered); err != nil {
		return 0, err
	}
	return before - len(filtered), nil
}

// toClashNode SubscribeNode → ClashNode（供 sing-box 生成与节点列表合并）。
func toClashNode(n SubscribeNode) ClashNode {
	return ClashNode{
		Name:       n.Name,
		Server:     n.Server,
		Port:       n.Port,
		NodeType:   n.NodeType,
		Password:   n.Password,
		UUID:       n.UUID,
		Cipher:     n.Cipher,
		SNI:        n.SNI,
		ServerName: n.SNI,
		TLS:        boolPtr(n.TLS),
		Network:    n.Network,
		WsPath:     n.WsPath,
		Flow:       n.Flow,
	}
}

// listSubscriptionNodes 订阅缓存节点 → ClashNode 列表（并入节点池）。
func (m *Manager) listSubscriptionNodes() []ClashNode {
	cache := m.loadSubscriptionCache()
	out := make([]ClashNode, 0, len(cache))
	for _, n := range cache {
		out = append(out, toClashNode(n))
	}
	return out
}

// ---------- 导入 ----------

// importSubscriptionPool 仅拉取并缓存订阅节点（不创建实例），返回节点数。
func (m *Manager) importSubscriptionPool(url string) (int, error) {
	nodes, err := fetchSubscription(url)
	if err != nil {
		return 0, err
	}
	if len(nodes) == 0 {
		return 0, fmt.Errorf("订阅中未解析到任何节点")
	}
	if err := m.saveSubscriptionCache(nodes); err != nil {
		return 0, err
	}
	return len(nodes), nil
}

// importSubscription 批量导入订阅节点为实例（含持久化订阅缓存）。
// joinGateway 为 true 时导入的实例打上入池标记（不自动启动，启停由实例池页控制）。
// 按节点身份（节点名+端口）匹配已存在实例，重复的订阅节点不重复创建
// （自动拉取每轮调用本函数，否则实例会无限增长）。
func (m *Manager) importSubscription(url string, joinGateway bool) (int, error) {
	nodes, err := fetchSubscription(url)
	if err != nil {
		return 0, err
	}
	if len(nodes) == 0 {
		return 0, fmt.Errorf("订阅中未解析到任何节点")
	}
	if err := m.saveSubscriptionCache(nodes); err != nil {
		return 0, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.load()
	existingNames := map[string]bool{}
	usedPorts := map[uint16]bool{}
	existingIDs := map[string]bool{}
	for _, e := range list {
		existingNames[e.Name] = true
		usedPorts[e.Port] = true
		usedPorts[e.SingboxPort] = true
		// 节点身份 = 节点名 + 节点地址（server:port）；实例端口是本地监听端口，与节点身份无关。
		existingIDs[e.Node+"|"+e.IP] = true
	}
	imported := 0
	for _, node := range nodes {
		nodeID := node.Name + "|" + fmt.Sprintf("%s:%d", node.Server, node.Port)
		if existingIDs[nodeID] {
			continue
		}
		existingIDs[nodeID] = true
		name := sanitizeInstanceName(node.Name)
		if existingNames[name] {
			i := 2
			for existingNames[name+"-"+itoa(uint16(i))] {
				i++
			}
			name = name + "-" + itoa(uint16(i))
		}
		// 实例端口是本地 opencode2api 监听端口，与节点服务器端口无关：
		// 从 basePort 段分配空闲（443 等节点端口留给远端，不占用本地监听）。
		port := instanceBasePort()
		for usedPorts[port] || usedPorts[port+10000] || !isPortFree(port) || !isPortFree(port+10000) {
			port++
		}
		ip := fmt.Sprintf("%s:%d", node.Server, node.Port)
		inst := Instance{
			Name:        name,
			Port:        port,
			Node:        node.Name,
			Password:    genSkKey(),
			IP:          ip,
			SingboxPort: port + 10000,
			JoinGateway: joinGateway,
			Status:      StatusStopped(),
		}
		// 锁内直接追加（复用 AddInstance 的校验语义，但不再二次加锁——
		// sync.Mutex 不可重入，循环内调 AddInstance 会死锁）。
		for i := range list {
			if list[i].Name == inst.Name {
				return imported, fmt.Errorf("导入实例 '%s' 失败: 已存在", node.Name)
			}
			if list[i].Port == inst.Port {
				return imported, fmt.Errorf("导入实例 '%s' 失败: 端口 %d 已占用", node.Name, inst.Port)
			}
		}
		list = append(list, inst)
		if err := m.save(list); err != nil {
			return imported, fmt.Errorf("保存实例清单失败: %v", err)
		}
		existingNames[name] = true
		usedPorts[port] = true
		usedPorts[port+10000] = true
		imported++
	}
	return imported, nil
}

// StartSubscribeLoop 后台订阅循环：按配置间隔自动拉取并入实例。
// intervalMin <= 0 或 URL 为空时休眠 30s 再查配置（配置变更无需重启）。
func (m *Manager) StartSubscribeLoop() {
	go func() {
		for {
			cfg := m.loadConfig()
			intervalMin := cfg.SubscribeIntervalMin
			url := cfg.SubscribeURL
			if intervalMin > 0 && url != "" {
				if n, err := m.importSubscription(url, false); err != nil {
					slog.Warn("订阅自动拉取失败", "error", err)
				} else {
					slog.Info("订阅自动拉取完成", "imported", n)
				}
			}
			wait := time.Duration(intervalMin) * time.Minute
			if wait < 30*time.Second {
				wait = 30 * time.Second
			}
			time.Sleep(wait)
		}
	}()
}

// ---------- HTTP handlers ----------

// SubscribePreviewHandler POST {url} → 拉取并解析节点列表（不落盘）。
func (m *Manager) SubscribePreviewHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		var req struct {
			URL string `json:"url"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.URL == "" {
			writeErr(w, http.StatusBadRequest, "url 必填")
			return
		}
		nodes, err := fetchSubscription(req.URL)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"nodes": nodes, "count": len(nodes)})
	}
}

// SubscribeImportHandler POST {url, join_gateway} → 导入为实例。
func (m *Manager) SubscribeImportHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		var req struct {
			URL         string `json:"url"`
			JoinGateway bool   `json:"join_gateway"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.URL == "" {
			writeErr(w, http.StatusBadRequest, "url 必填")
			return
		}
		n, err := m.importSubscription(req.URL, req.JoinGateway)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"status": "ok", "imported": n})
	}
}

// SubscribeImportPoolHandler POST {url} → 仅入订阅缓存（节点池页再勾选入池/独享）。
func (m *Manager) SubscribeImportPoolHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodPost) {
			return
		}
		var req struct {
			URL string `json:"url"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.URL == "" {
			writeErr(w, http.StatusBadRequest, "url 必填")
			return
		}
		n, err := m.importSubscriptionPool(req.URL)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"status": "ok", "imported": n})
	}
}

// ---------- 小工具 ----------

// jsonUint16 兼容 JSON 里 port 的字符串/数字两种形态。
func jsonUint16(v any) uint16 {
	switch t := v.(type) {
	case float64:
		if t > 0 && t <= 65535 {
			return uint16(t)
		}
	case string:
		if p, err := strconv.Atoi(t); err == nil && p > 0 && p <= 65535 {
			return uint16(p)
		}
	}
	return 0
}

// asString map 取值转 string。
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// asDefaultString 取值或默认。
func asDefaultString(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

// boolPtr 便捷 bool 指针。
func boolPtr(b bool) *bool { return &b }
