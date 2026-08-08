// Part of the P1 (core split) refactor: code moved out of main.go.
// Same package (main) - do not change package clause manually.
package main

import (
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
)

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = letters[b[i]%byte(len(letters))]
	}
	return string(b)
}

func randomHex(n int) string {
	const hex = "0123456789abcdef"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = hex[b[i]%byte(len(hex))]
	}
	return string(b)
}

// ======================== OpenCode 会话 ========================

var (
	ocSessionID  string
	ocProjectID  string
	ocClientVer  string
	ocOnce       sync.Once
	requestCount atomic.Int64
)

func fetchOCVersion() string {
	req, _ := http.NewRequest("GET", "https://registry.npmjs.org/opencode-ai/latest", nil)
	req.Header.Set("Accept", "application/json")
	resp, err := getHTTPClient().Do(req)
	if err != nil {
		return "1.15.3"
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var info struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(body, &info) == nil && info.Version != "" {
		return info.Version
	}
	return "1.15.3"
}

func initOCSession() {
	ocOnce.Do(func() {
		ocClientVer = fetchOCVersion()
		ocSessionID = "ses_" + randomString(24)
		ocProjectID = randomHex(40)
		slog.Info("opencode version", "version", ocClientVer)
		slog.Info("session initialized", "session_id", ocSessionID)
		slog.Info("project initialized", "project_id", ocProjectID)
	})
}

func refreshOCSession() {
	ocClientVer = fetchOCVersion()
	ocSessionID = "ses_" + randomString(24)
	ocProjectID = randomHex(40)
	slog.Info("session refreshed", "version", ocClientVer, "session_id", ocSessionID)
	// 重置 Once 以便后续 initOCSession 调用直接通过
	ocOnce = sync.Once{}
}

// ======================== 模型 ========================
