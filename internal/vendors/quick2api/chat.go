package quick2api

// 执行层：把宿主的 chat-completions 请求转成 Quick AIDP 回合，
// 并把事件流转成 OpenAI 兼容的响应体（非流式 JSON / 流式 SSE data 载荷）。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"quick2api-plugin/cpasdk/pluginapi"
	"quick2api-plugin/internal/core"
	"quick2api-plugin/internal/quick"
)

// Execute 非流式执行。
func (a *adapter) Execute(ctx context.Context, cred *core.Credential, req *core.ExecuteRequest) (*pluginapi.ExecutorResponse, error) {
	c, err := a.native(cred)
	if err != nil {
		return nil, err
	}
	params, err := buildParams(req)
	if err != nil {
		return nil, err
	}
	client := quick.NewClient(nil, a.proxy)
	var result *core.TurnResult
	result, err = client.Turn(ctx, c.Session(), params.ChatParams, nil)
	if err != nil {
		return nil, core.ToPluginError(err)
	}
	body := buildNonStreamResponse(result, params.model)
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return &pluginapi.ExecutorResponse{
		Payload: payload,
		Headers: map[string][]string{"Content-Type": {core.ContentTypeJSON}},
	}, nil
}

// ExecuteStream 流式执行：sink.Emit 接收裸 SSE data 载荷。
func (a *adapter) ExecuteStream(ctx context.Context, cred *core.Credential, req *core.ExecuteRequest, sink core.StreamSink) error {
	c, err := a.native(cred)
	if err != nil {
		return err
	}
	params, err := buildParams(req)
	if err != nil {
		return err
	}
	client := quick.NewClient(nil, a.proxy)
	id := "chatcmpl-" + quick.NewUUID()
	created := time.Now().Unix()

	// 首包：role=assistant
	if err := emitChunk(sink, id, created, params.model, deltaChunk{Role: "assistant"}); err != nil {
		return err
	}

	_, err = client.Turn(ctx, c.Session(), params.ChatParams, func(ch core.Chunk) {
		if ch.Delta != "" {
			_ = emitChunk(sink, id, created, params.model, deltaChunk{Content: ch.Delta})
		}
		if ch.Reasoning != "" {
			_ = emitChunk(sink, id, created, params.model, deltaChunk{Reasoning: ch.Reasoning})
		}
	})
	if err != nil {
		return core.ToPluginError(err)
	}
	// 末包：finish_reason=stop + usage
	_ = emitChunk(sink, id, created, params.model, deltaChunk{FinishReason: "stop"})
	return sink.Close("")
}

func (a *adapter) native(cred *core.Credential) (*Credential, error) {
	if cred == nil || cred.Native == nil {
		return nil, fmt.Errorf("missing quick2api credential")
	}
	c, ok := cred.Native.(*Credential)
	if !ok || c == nil {
		return nil, fmt.Errorf("credential is not quick2api type")
	}
	return c, nil
}

type turnParams struct {
	quick.ChatParams
	model string
}

// buildParams 从宿主翻译好的 chat-completions payload 构造协议层入参。
func buildParams(req *core.ExecuteRequest) (turnParams, error) {
	var body struct {
		Model           string `json:"model"`
		Messages        []struct {
			Role       string `json:"role"`
			Content    any    `json:"content"`
		} `json:"messages"`
		Stream          bool `json:"stream"`
		ReasoningEffort string `json:"reasoning_effort"`
	}
	if err := json.Unmarshal(req.Payload, &body); err != nil {
		return turnParams{}, fmt.Errorf("invalid chat-completions payload: %w", err)
	}
	mode := modeForModel(body.Model)
	params := turnParams{model: body.Model}
	params.AgentMode = mode
	params.ThinkingEffort = effortFor(body.ReasoningEffort)
	params.Timezone = "Asia/Shanghai"
	params.Locale = "zh-CN"

	// 渲染消息：system 走专用字段，其余拼进正文。
	var sys, text strings.Builder
	for _, m := range body.Messages {
		content := contentToString(m.Content)
		switch m.Role {
		case "system":
			sys.WriteString(content)
			sys.WriteString("\n")
		case "user":
			fmt.Fprintf(&text, "[user]\n%s\n\n", content)
		case "assistant":
			fmt.Fprintf(&text, "[assistant]\n%s\n\n", content)
		case "tool":
			fmt.Fprintf(&text, "[tool_result] %s\n\n", content)
		}
	}
	params.SystemPrompt = strings.TrimSpace(sys.String())
	params.Text = text.String()
	return params, nil
}

func contentToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var b strings.Builder
		for _, part := range x {
			if m, ok := part.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					b.WriteString(t)
				}
			}
		}
		return b.String()
	}
	return ""
}

func effortFor(v string) string {
	switch strings.ToLower(v) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "max":
		return "max"
	}
	return ""
}

// ---- OpenAI 兼容响应构造 ----

type deltaChunk struct {
	Role         string `json:"role,omitempty"`
	Content      string `json:"content,omitempty"`
	Reasoning    string `json:"reasoning_content,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
}

func emitChunk(sink core.StreamSink, id string, created int64, model string, d deltaChunk) error {
	payload := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         d,
			"finish_reason": nil,
		}},
	}
	if d.FinishReason != "" {
		payload["choices"] = []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": d.FinishReason,
		}}
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return sink.Emit(b)
}

func buildNonStreamResponse(res *core.TurnResult, model string) map[string]any {
	msg := map[string]any{"role": "assistant", "content": res.Text}
	if res.Reasoning != "" {
		msg["reasoning_content"] = res.Reasoning
	}
	choice := map[string]any{
		"index":         0,
		"message":       msg,
		"finish_reason": "stop",
	}
	usage := map[string]any{}
	if res.Usage != nil {
		usage = map[string]any{
			"prompt_tokens":     res.Usage.InputTokens,
			"completion_tokens": res.Usage.OutputTokens,
			"total_tokens":      res.Usage.TotalTokens,
		}
	}
	return map[string]any{
		"id":      "chatcmpl-" + quick.NewUUID(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{choice},
		"usage":   usage,
	}
}
