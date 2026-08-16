// 解析优化回归测试（移植自 PR #9，按 main 现状适配）：
// SSE/非流式 map 版转换与历史签名等价（消除重复解析后输出一致）。
package main

import (
	"encoding/json"
	"testing"
)

// ---------- SSE chunk 转换等价性 ----------

func TestConvertStreamChunkFromObjEquivalent(t *testing.T) {
	line := `data: {"choices":[{"delta":{"content":"你好","reasoning_content":"think"}}],"usage":{"total_tokens":10},"cost":0.5}`
	for _, keep := range []bool{false, true} {
		// 历史签名路径
		fromLine, usageLine := convertStreamChunkWithUsage(line, keep)
		// map 版路径（先解析一次，与 SSE 循环一致）
		var obj map[string]any
		if json.Unmarshal([]byte(line[6:]), &obj) != nil {
			t.Fatal("parse failed")
		}
		conv, usageObj := convertStreamChunkFromObj(obj, keep)
		if fromLine != "data: "+conv {
			t.Fatalf("keep=%v 转换结果不一致:\n fromLine=%s\n fromObj =%s", keep, fromLine, "data: "+conv)
		}
		if (usageLine == nil) != (usageObj == nil) || (usageLine != nil && usageLine["total_tokens"] != usageObj["total_tokens"]) {
			t.Fatalf("keep=%v usage 提取不一致: line=%v obj=%v", keep, usageLine, usageObj)
		}
	}
}

// ---------- 非流式响应转换等价性 ----------

func TestConvertResponseFromObjEquivalent(t *testing.T) {
	body := `{"choices":[{"message":{"content":"hi","reasoning_content":"think","logprobs":null}}],"cost":0.5}`
	for _, keep := range []bool{false, true} {
		// 历史签名路径
		want, err := convertResponse([]byte(body), keep)
		if err != nil {
			t.Fatalf("convertResponse: %v", err)
		}
		// map 版路径（与 chat_handler 单次解析一致）
		var obj map[string]any
		if json.Unmarshal([]byte(body), &obj) != nil {
			t.Fatal("parse failed")
		}
		got, err := convertResponseFromObj(obj, keep)
		if err != nil {
			t.Fatalf("convertResponseFromObj: %v", err)
		}
		if string(got) != string(want) {
			t.Fatalf("keep=%v 转换结果不一致:\n want=%s\n got =%s", keep, want, got)
		}
	}
}
