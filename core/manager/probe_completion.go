// 免费模型探测与判定（Rust is_free_model / probe_free_completion_response 语义）。
package manager

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

// isFreeModelID 免费模型判定。
func isFreeModelID(id string) bool {
	low := strings.ToLower(strings.TrimSpace(id))
	if strings.Contains(low, "-free") || low == "big-pickle" {
		return true
	}
	switch low {
	case "deepseek-v4-flash", "mimo-v2.5", "ling-3.0-flash",
		"nemotron-3-ultra", "north-mini-code", "laguna-s-2.1":
		return true
	}
	return false
}

// pickFreeModel 从 /v1/models data[] 挑选免费模型（-free/big-pickle 立即命中）。
func pickFreeModel(data []map[string]any) string {
	var firstFree string
	for _, mp := range data {
		id, _ := mp["id"].(string)
		if id == "" {
			continue
		}
		low := strings.ToLower(id)
		if strings.Contains(low, "-free") || low == "big-pickle" {
			return id
		}
		if firstFree == "" && isFreeModelID(id) {
			firstFree = id
		}
	}
	return firstFree
}

// freeCompletion 免费模型测试：GET /v1/models → 挑免费模型 → POST chat。
// 返回 (status, body, modelCount, err)；modelCount 来自 /v1/models 条目数（未知 -1）。
// L7：GET/POST 共享同一 deadline（进入时算 now+budget）——POST 只拿 GET 后的剩余
// 预算，两请求总耗时 ≤ 总预算（原实现 GET budget/2 + POST 整 budget 可超支 50%）。
func freeCompletion(port uint16, password string, budget time.Duration) (int, []byte, int, error) {
	deadline := time.Now().Add(budget)
	modelStatus, body, err := httpGetJSON(port, "/v1/models", time.Until(deadline), password)
	if err != nil || modelStatus < 200 || modelStatus >= 300 {
		if modelStatus != 0 {
			return modelStatus, body, -1, nil
		}
		return 0, body, -1, err
	}
	modelCount := -1
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Data != nil {
		modelCount = len(payload.Data)
	} else {
		var raw []map[string]any
		if json.Unmarshal(body, &raw) == nil {
			modelCount = len(raw)
		}
	}
	modelID := pickFreeModel(payload.Data)
	if modelID == "" {
		return 503, []byte(`{"error":"models 接口成功，但没有可测试的免费模型"}`), modelCount, nil
	}
	chatBody, _ := json.Marshal(map[string]any{
		"model":      modelID,
		"messages":   []any{map[string]any{"role": "user", "content": "Reply with OK"}},
		"max_tokens": 1,
		"stream":     false,
	})
	status, chatResp, err := httpPostJSON(port, "/v1/chat/completions", time.Until(deadline), password, chatBody)
	return status, chatResp, modelCount, err
}

// probeCompletionSuccess 判定 chat 通过：2xx 且 choices 非空。
func probeCompletionSuccess(status int, body []byte) bool {
	if status < 200 || status >= 300 {
		return false
	}
	var obj map[string]any
	if json.Unmarshal(body, &obj) != nil {
		return false
	}
	chs, ok := obj["choices"].([]any)
	return ok && len(chs) > 0
}

// modelsCount 从 /v1/models 响应统计数量。
func modelsCount(body []byte) (int, bool) {
	var obj struct {
		Data []any `json:"data"`
	}
	if json.Unmarshal(body, &obj) == nil && obj.Data != nil {
		return len(obj.Data), true
	}
	var arr []any
	if json.Unmarshal(body, &arr) == nil {
		return len(arr), true
	}
	return 0, false
}

// readFileTail 读文件尾部 max 字节。
func readFileTail(path string, max int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() <= 0 {
		return nil, os.ErrNotExist
	}
	if fi.Size() > int64(max) {
		if _, err := f.Seek(-int64(max), io.SeekEnd); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(f)
}
