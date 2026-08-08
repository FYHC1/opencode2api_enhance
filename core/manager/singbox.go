// sing-box 客户端配置生成（Rust singbox.rs 移植）。
// 每个节点类型产出对应 outbound；本地 SOCKS 入站监听 127.0.0.1:listenPort。
package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// buildSingboxConfig 生成 sing-box 配置 JSON。
func buildSingboxConfig(node ClashNode, listenPort uint16) ([]byte, error) {
	outbound, err := singboxOutbound(node)
	if err != nil {
		return nil, err
	}
	cfg := map[string]any{
		"log":      map[string]any{"level": "warn", "timestamp": true},
		"inbounds": []any{map[string]any{"type": "socks", "listen": "127.0.0.1", "listen_port": listenPort}},
		"outbounds": []any{
			outbound,
			map[string]any{"type": "direct", "tag": "direct"},
		},
		"route": map[string]any{"final": "proxy"},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// BuildSingboxConfigFor 导出包装（main 装配实例接缝用）。
func BuildSingboxConfigFor(node ClashNode, listenPort uint16) ([]byte, error) {
	return buildSingboxConfig(node, listenPort)
}

// buildSingboxOutbound 按类型构建 outbound。
func singboxOutbound(n ClashNode) (map[string]any, error) {
	tlsOn := func(def bool) bool {
		if n.TLS == nil {
			return def
		}
		return *n.TLS
	}
	serverName := func(def string) string {
		if n.ServerName != "" {
			return n.ServerName
		}
		if n.SNI != "" {
			return n.SNI
		}
		return def
	}
	insecure := func(def bool) bool {
		if n.SkipCertVerify == nil {
			return def
		}
		return *n.SkipCertVerify
	}

	switch strings.ToLower(n.NodeType) {
	case "trojan":
		if n.Password == "" {
			return nil, errors.New("缺少 password")
		}
		return map[string]any{
			"type":        "trojan",
			"tag":         "proxy",
			"server":      n.Server,
			"server_port": n.Port,
			"password":    n.Password,
			"tls": map[string]any{
				"enabled":     tlsOn(true),
				"server_name": serverName(n.Server),
				"insecure":    insecure(false),
			},
		}, nil
	case "vless":
		if n.UUID == "" {
			return nil, errors.New("缺少 uuid")
		}
		out := map[string]any{
			"type":        "vless",
			"tag":         "proxy",
			"server":      n.Server,
			"server_port": n.Port,
			"uuid":        n.UUID,
		}
		tls := map[string]any{
			"enabled":     tlsOn(true),
			"server_name": serverName(n.Server),
		}
		if n.RealityPublicKey != "" {
			reality := map[string]any{"enabled": true, "public_key": n.RealityPublicKey}
			if n.RealityShortID != "" {
				reality["short_id"] = n.RealityShortID
			}
			tls["reality"] = reality
			fp := n.ClientFingerprint
			if fp == "" {
				fp = "chrome"
			}
			out["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		} else {
			tls["insecure"] = insecure(false)
		}
		out["tls"] = tls
		if n.Flow != "" {
			out["flow"] = n.Flow
		}
		if tr := singTransport(n); tr != nil {
			out["transport"] = tr
		}
		return out, nil
	case "vmess":
		if n.UUID == "" {
			return nil, errors.New("缺少 uuid")
		}
		out := map[string]any{
			"type":        "vmess",
			"tag":         "proxy",
			"server":      n.Server,
			"server_port": n.Port,
			"uuid":        n.UUID,
			"security":    "auto",
			"alter_id":    0,
			"tls": map[string]any{
				"enabled":     tlsOn(false),
				"server_name": serverName(n.Server),
				"insecure":    insecure(false),
			},
		}
		if tr := singTransport(n); tr != nil {
			out["transport"] = tr
		}
		return out, nil
	case "ss", "shadowsocks":
		if n.Password == "" {
			return nil, errors.New("缺少 password")
		}
		method := n.Cipher
		if method == "" {
			method = "aes-256-gcm"
		}
		return map[string]any{
			"type":        "shadowsocks",
			"tag":         "proxy",
			"server":      n.Server,
			"server_port": n.Port,
			"method":      method,
			"password":    n.Password,
		}, nil
	case "hysteria2", "hy2":
		if n.Password == "" {
			return nil, errors.New("缺少 password")
		}
		out := map[string]any{
			"type":        "hysteria2",
			"tag":         "proxy",
			"server":      n.Server,
			"server_port": n.Port,
			"password":    n.Password,
			"tls": map[string]any{
				"enabled":     tlsOn(true),
				"server_name": serverName(n.Server),
				"insecure":    insecure(false),
			},
		}
		if n.Obfs != "" {
			obfs := map[string]any{"type": n.Obfs}
			if n.ObfsPassword != "" {
				obfs["password"] = n.ObfsPassword
			}
			out["obfs"] = obfs
		}
		if n.Up != "" {
			out["up_mbps"] = n.Up
		}
		if n.Down != "" {
			out["down_mbps"] = n.Down
		}
		return out, nil
	case "anytls":
		if n.Password == "" {
			return nil, errors.New("缺少 password")
		}
		return map[string]any{
			"type":        "anytls",
			"tag":         "proxy",
			"server":      n.Server,
			"server_port": n.Port,
			"password":    n.Password,
			"tls": map[string]any{
				"enabled":     tlsOn(true),
				"server_name": serverName(n.Server),
				"insecure":    insecure(false),
			},
		}, nil
	default:
		return nil, fmt.Errorf("不支持的节点类型: %s", n.NodeType)
	}
}

// singTransport ws/http 传输（network tcp/空则省略）。
func singTransport(n ClashNode) map[string]any {
	switch strings.ToLower(n.Network) {
	case "ws":
		ws := map[string]any{"type": "ws"}
		path := n.WsPath
		if path == "" {
			path = "/"
		}
		ws["path"] = path
		if n.WSHeaders != nil {
			ws["headers"] = n.WSHeaders
		}
		return ws
	case "http":
		return map[string]any{"type": "http"}
	default:
		return nil
	}
}
