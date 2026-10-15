package opencode

// 上游免费通道（Authorization: Bearer public）的请求体门禁。2026-10-14 起为 v2 契约：
//
//	① body 必须 stream:true（非流式即使身份头正确也 403）；
//	② tools 必须声明 shell / glob / grep / read 四个名字，且每个都要带**真实的描述与参数
//	   schema**——空 properties 的占位定义会被判为非官方客户端，回 403 FreeTierError；
//	③ tool_choice 不能是 "none"（上游见到即 403）。
//
// 实测取证（docs/issue-log/2026-10-15.md）：同一 body 打到 /inference/v1，
// 「v1 身份头 + 空 schema」403、「v2 身份头 + 空 schema」403、「v2 身份头 + 真实 schema」200。
//
// 旧契约（2026-09-18 起）只要求 bash/glob/grep/read 四个**名字**、schema 不限；v2 客户端把
// bash 改名为 shell 并带上真实定义。历史取证见 docs/issue-log/2026-09-18.md。
var gateToolNames = []string{"shell", "glob", "grep", "read"}

// gateToolPlaceholderNote 是注入门禁工具时补在 system 位置的说明。
// 上游禁止用 tool_choice:"none" 压制工具调用（会 403），只能用提示词降低模型调用注入工具的概率。
// 措辞照社区实测：写成"工具不可用"会让模型连客户端自己的工具也拒绝使用，必须明确"用你已有的其它工具"。
const gateToolPlaceholderNote = "The tools named shell, glob, grep and read in this request are placeholders. " +
	"Do not call them; use the other tools you have been given."

// gateToolSchema 是门禁工具的官方定义（描述与参数照 v2 客户端对齐）。
type gateToolSchema struct {
	Description string
	Parameters  map[string]any
}

// gateToolSchemas 集中维护四个门禁工具的真实定义。
// 参数 schema 必须非空（至少一个 properties + required），否则上游按占位工具处理并回 403。
var gateToolSchemas = map[string]gateToolSchema{
	"shell": {
		Description: "Execute a shell command and return its output.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":    map[string]any{"type": "string", "description": "Shell command string to execute"},
				"workdir":    map[string]any{"type": "string", "description": "Working directory"},
				"timeout":    map[string]any{"type": "integer", "description": "Timeout in milliseconds"},
				"background": map[string]any{"type": "boolean", "description": "Run in background"},
			},
			"required":             []any{"command"},
			"additionalProperties": false,
		},
	},
	"glob": {
		Description: `Search file paths using a glob pattern (examples: "**/*.ts", "src/**/*.tsx").`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Glob pattern to match files against"},
				"path":    map[string]any{"type": "string", "description": "Directory to search. Defaults to current directory."},
				"hidden":  map[string]any{"type": "boolean", "description": "Include hidden files (default: false)."},
				"limit":   map[string]any{"type": "integer", "description": "Max matches (default: 100)"},
			},
			"required":             []any{"pattern"},
			"additionalProperties": false,
		},
	},
	"grep": {
		Description: "Search file contents using ripgrep syntax.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":       map[string]any{"type": "string", "minLength": 1, "description": "Regex pattern to match"},
				"path":          map[string]any{"type": "string", "description": "File or directory to search"},
				"include":       map[string]any{"type": "string", "description": "Glob filter"},
				"literal":       map[string]any{"type": "boolean", "description": "Treat pattern as exact text"},
				"caseSensitive": map[string]any{"type": "boolean", "description": "Use case-sensitive matching"},
				"limit":         map[string]any{"type": "integer", "description": "Max matches (default: 100)"},
			},
			"required":             []any{"pattern"},
			"additionalProperties": false,
		},
	},
	"read": {
		Description: "Read the contents of a file or directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "File or directory to read"},
				"offset": map[string]any{"type": "integer", "description": "Line to start reading from"},
				"limit":  map[string]any{"type": "integer", "description": "Max lines to read"},
			},
			"required":             []any{"path"},
			"additionalProperties": false,
		},
	},
}

// isGateTool 判断工具名是否属于门禁要求集合。
func isGateTool(name string) bool {
	for _, n := range gateToolNames {
		if n == name {
			return true
		}
	}
	return false
}

// gateToolDef 返回 chat/completions 形态（function 包裹）的门禁工具定义。
func gateToolDef(name string) map[string]any {
	s := gateToolSchemas[name]
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        name,
			"description": s.Description,
			"parameters":  s.Parameters,
			"strict":      false,
		},
	}
}

// gateToolDefFlat 返回 Responses 形态（name / parameters 在顶层）的门禁工具定义。
func gateToolDefFlat(name string) map[string]any {
	s := gateToolSchemas[name]
	return map[string]any{
		"type":        "function",
		"name":        name,
		"description": s.Description,
		"parameters":  s.Parameters,
		"strict":      false,
	}
}

// toolIdentity 取出工具定义的名字与参数 schema（兼容 chat 的 function 包裹与 Responses 的平铺形态）。
func toolIdentity(t any) (string, map[string]any) {
	tm, ok := t.(map[string]any)
	if !ok {
		return "", nil
	}
	if fn, ok := tm["function"].(map[string]any); ok {
		name, _ := fn["name"].(string)
		params, _ := fn["parameters"].(map[string]any)
		return name, params
	}
	name, _ := tm["name"].(string)
	params, _ := tm["parameters"].(map[string]any)
	return name, params
}

// paramsNonEmpty 判断参数 schema 是否"有内容"：必须有非空 properties。
// 空 properties（`{"type":"object","properties":{}}`）正是上游判定占位工具的判据。
func paramsNonEmpty(params map[string]any) bool {
	if params == nil {
		return false
	}
	props, ok := params["properties"].(map[string]any)
	return ok && len(props) > 0
}

// ensureAgentGate 就地补齐免费通道的 body 级门禁（chat 与 responses 共用同一 bodyMap）：
//   - stream 强制 true（上游拒绝非流式）；
//   - tool_choice:"none" 被删除（上游见到即 403）；为近似「本轮不调工具」的语义，
//     同时丢弃客户端自己的工具，只留门禁工具；
//   - tools 里四个门禁名字必须带真实定义：客户端已有同名且参数非空的工具则保留，
//     空 schema 的同名工具与缺失的名字统一补成官方定义（顺序按 gateToolNames）；
//   - 真的注入了工具时，在 system 位置补一句占位说明。
//
// flatTools 为真表示当前 body 是 Responses 形态（工具名/参数在顶层）。
// 返回是否注入了门禁工具（供诊断/测试）。
func ensureAgentGate(bodyMap map[string]any, flatTools bool) bool {
	bodyMap["stream"] = true
	suppressClientTools := false
	if tc, ok := bodyMap["tool_choice"].(string); ok && tc == "none" {
		delete(bodyMap, "tool_choice")
		suppressClientTools = true
	}

	tools, _ := bodyMap["tools"].([]any)
	out := make([]any, 0, len(tools)+len(gateToolNames))
	kept := make(map[string]bool, len(gateToolNames))
	for _, t := range tools {
		name, params := toolIdentity(t)
		if isGateTool(name) {
			if !paramsNonEmpty(params) {
				continue // 空 schema 的同名工具会被官方定义取代
			}
			kept[name] = true
		} else if suppressClientTools {
			continue // 近似 tool_choice:none：客户端工具不下发
		}
		out = append(out, t)
	}

	injected := false
	for _, n := range gateToolNames {
		if kept[n] {
			continue
		}
		if flatTools {
			out = append(out, gateToolDefFlat(n))
		} else {
			out = append(out, gateToolDef(n))
		}
		injected = true
	}
	bodyMap["tools"] = out

	if injected {
		appendGateNote(bodyMap, flatTools)
	}
	return injected
}

// appendGateNote 追加门禁占位说明：**只在已有** system / developer 消息（或 responses 的
// instructions）上并入，不新增消息或字段——避免改变请求结构，影响上游语义与下游回放断言。
// 没有 system 位置时宁可不注入：note 只是降低模型调用注入工具的概率，不是门禁判据。
func appendGateNote(bodyMap map[string]any, flatTools bool) {
	if flatTools {
		if cur, ok := bodyMap["instructions"].(string); ok && cur != "" {
			bodyMap["instructions"] = cur + "\n\n" + gateToolPlaceholderNote
		}
		return
	}
	msgs, _ := bodyMap["messages"].([]any)
	for _, it := range msgs {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		if role != "system" && role != "developer" {
			continue
		}
		if s, ok := m["content"].(string); ok {
			m["content"] = s + "\n\n" + gateToolPlaceholderNote
			return
		}
	}
}

// filterGateTools 从 Responses 形态工具列表中只保留门禁工具。
// 用于「客户端显式要求 tool_choice:none」时：既要尊重不调工具，又要留住门禁要求的名字。
func filterGateTools(tools []any) []any {
	var out []any
	for _, t := range tools {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if n, _ := tm["name"].(string); isGateTool(n) {
			out = append(out, tm)
		}
	}
	return out
}
