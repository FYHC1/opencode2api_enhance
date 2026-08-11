// 类型别名桥（P1.2b 渐进式拆包）：协议类型已迁入 core/protocol，
// 此处为根目录 package main 提供向后兼容别名 —— 调用方可继续用 Message/OpenAIRequest，
// 后续按函数下沉时逐步改为 protocol.XXX 直至删除本桥。
package main

import "github.com/6Kmfi6HP/opencode2api/core/protocol"

type Message = protocol.Message
type ToolCall = protocol.ToolCall
type FunctionCall = protocol.FunctionCall
type Tool = protocol.Tool
type ToolFunction = protocol.ToolFunction
type OpenAIRequest = protocol.OpenAIRequest
type ClaudeRequest = protocol.ClaudeRequest
type ClaudeMessage = protocol.ClaudeMessage
type ClaudeContent = protocol.ClaudeContent
type ClaudeTool = protocol.ClaudeTool
type ClaudeResponse = protocol.ClaudeResponse
type ClaudeUsage = protocol.ClaudeUsage
type ResponsesAPIRequest = protocol.ResponsesAPIRequest
type ResponsesTool = protocol.ResponsesTool
type ReasonEffort = protocol.ReasonEffort
type StoredResponseState = protocol.StoredResponseState