// Part of the P1 (core split) refactor: code moved out of main.go.
// Same package (main) - do not change package clause manually.
//
// 随机串工具 + 请求计数。
// OpenCode 会话状态已收拢进 vendors/opencode（Vendor 实例字段），
// 本文件不再持有全局会话；历史 ocSessionID/initOCSession 等已删除。
package main

import (
	"crypto/rand"
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

// requestCount 全局请求计数（生成唯一请求 ID 用，见 chat_handler/claude）。
var requestCount atomic.Int64
