// 报表导出（main 分支功能迁移 M3）：
// 调用日志 CSV / 实例 JSON / 统计 JSON，供前端「导出」按钮下载。
//
// 端点：GET /api/admin/export/call-log.csv、/instances.json、/stats.json
package manager

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// csvEscape 与 Rust csv_escape 一致：含逗号/引号/换行时包引号并转义双引号。
func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n\r") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// ExportCallLogCSV 导出调用日志为 CSV 文本。
// 列：ts,model,status,path,err_msg,nodes,duration_ms,req_id（与 main 一致）。
func (m *Manager) ExportCallLogCSV(limit int) string {
	if limit <= 0 {
		limit = 5000
	}
	if limit > 50000 {
		limit = 50000
	}
	records := m.ReadCallLog(limit)
	var b strings.Builder
	// UTF-8 BOM：让 Excel 正确识别中文（main 未加，此处为体验增强）。
	b.WriteString("\uFEFF")
	b.WriteString("ts,model,status,path,err_msg,nodes,duration_ms,req_id\n")
	for _, r := range records {
		status := "ok"
		if r.HasIssue() {
			status = "error"
		}
		line := strings.Join([]string{
			csvEscape(r.TS), csvEscape(r.Model), status, csvEscape(r.Path),
			csvEscape(r.ErrMsg), csvEscape(strings.Join(r.Nodes, "|")),
			itoa64(r.DurationMS), csvEscape(r.ReqID),
		}, ",")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// ExportInstancesJSON 导出实例快照为 JSON 文本。
func (m *Manager) ExportInstancesJSON() (string, error) {
	data, err := json.MarshalIndent(m.ListInstances(), "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ExportStatsJSON 导出统计摘要为 JSON 文本。
func (m *Manager) ExportStatsJSON() (string, error) {
	data, err := json.MarshalIndent(m.AggregateStats(), "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ---------- HTTP handlers（attachment 下载） ----------

func writeAttachment(w http.ResponseWriter, contentType, filename, body string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, _ = w.Write([]byte(body))
}

// ExportCallLogCSVHandler GET 下载调用日志 CSV。
func (m *Manager) ExportCallLogCSVHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodGet) {
			return
		}
		limit := 5000
		if s := r.URL.Query().Get("limit"); s != "" {
			if n := parsePositiveInt(s); n > 0 {
				limit = n
			}
		}
		writeAttachment(w, "text/csv; charset=utf-8", "call-log.csv", m.ExportCallLogCSV(limit))
	}
}

// ExportInstancesJSONHandler GET 下载实例 JSON。
func (m *Manager) ExportInstancesJSONHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodGet) {
			return
		}
		body, err := m.ExportInstancesJSON()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeAttachment(w, "application/json; charset=utf-8", "instances.json", body)
	}
}

// ExportStatsJSONHandler GET 下载统计 JSON。
func (m *Manager) ExportStatsJSONHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireMethodOK(w, r, http.MethodGet) {
			return
		}
		body, err := m.ExportStatsJSON()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeAttachment(w, "application/json; charset=utf-8", "stats.json", body)
	}
}

// itoa64 int64 → 字符串。
func itoa64(v int64) string {
	return strconv.FormatInt(v, 10)
}
