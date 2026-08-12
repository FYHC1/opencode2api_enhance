// Part of the P1 (core split) refactor: code moved out of main.go.
// Same package (main) - do not change package clause manually.
//
// 旧版内嵌 Web 管理面板（adminPageHandler 及配套 /api/config、/api/stats、
// /api/reload、/api/node-status 等旧路由）已于 2026-08-12 移除：
// 前端统一由 dist/ 六页 UI 提供（frontendDistDir 存在即托管 SPA）。
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
func resetStatsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tokenStatsMu.Lock()
	tokenStats = &TokenStatsData{Models: map[string]*ModelStats{}}
	tokenStatsMu.Unlock()
	saveTokenStats()
	nodeStatsMu.Lock()
	nodeStats = &NodeStatsData{Nodes: map[string]*NodeStat{}}
	nodeStatsMu.Unlock()
	saveNodeStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
