// Part of the P1 (core split) refactor: code moved out of main.go.
// Same package (main) - do not change package clause manually.
//
// 旧版内嵌 Web 管理面板（adminPageHandler 及配套 /api/config、/api/stats、
// /api/reload、/api/node-status 等旧路由）已于 2026-08-12 移除：
// 前端统一由 dist/ 七页 UI 提供（frontendDistDir 存在即托管 SPA）。
// 本文件仅保留 /api/reset-stats —— 它是实例子进程/网关子进程的复位契约
// （core/manager stats.go ResetStats 对运行中实例发 HTTP DELETE /api/reset-stats）。
package main

import (
	"encoding/json"
	"net/http"
)

// resetStatsHandler 清空本进程 token/节点统计并落盘（供管理端「重置统计」调用）。
// 与 /api/stats 的 DELETE 语义一致，但改用 apiKeyAuth（Bearer 密钥）而非会话 cookie，
// 便于本机管理进程直接以密钥调用，无需先走 /login 拿 session。
// 落盘失败（Windows 瞬时文件占用等）返回 500，管理端据此上报失败而非静默半复位。
func resetStatsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tokenStatsMu.Lock()
	tokenStats = &TokenStatsData{Models: map[string]*ModelStats{}}
	tokenStatsMu.Unlock()
	if err := flushTokenStatsNow(); err != nil {
		writeResetStatsError(w, "token 统计复位失败（落盘被占用/IO 错误）: "+err.Error())
		return
	}
	nodeStatsMu.Lock()
	nodeStats = &NodeStatsData{Nodes: map[string]*NodeStat{}}
	nodeStatsMu.Unlock()
	if err := flushNodeStatsNow(); err != nil {
		writeResetStatsError(w, "节点统计复位失败（落盘被占用/IO 错误）: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// writeResetStatsError 复位失败响应（管理器 httpDeleteJSON 对非 2xx 记失败）。
func writeResetStatsError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
