package quick

import (
	"context"
	"fmt"
	"time"

	"quick2api-plugin/internal/core"
)

// Client 管理会话获取与 turn 执行。
type Client struct {
	store   *SessionStore
	proxy   string
	timeout time.Duration
}

func NewClient(store *SessionStore, proxy string) *Client {
	return &Client{store: store, proxy: proxy, timeout: 10 * time.Minute}
}

// Session 取会话；过期时用 refresh_token 续期并写回。
func (c *Client) Session(key string) (*core.Session, error) {
	sess := c.store.Get(key)
	if sess == nil {
		return nil, ErrNoSession
	}
	if sess.Expired() {
		fresh, err := Refresh(sess, c.store)
		if err != nil {
			return nil, err
		}
		sess = fresh
	}
	return sess, nil
}

// ChatParams 是协议层入参，由上层做协议转换后传入。
type ChatParams struct {
	Text           string
	SystemPrompt   string
	CustomPrompt   string
	AgentMode      string // smart / balanced / fast / advanced
	ThinkingEffort string // off / low / medium / high / max
	Timezone       string
	Locale         string
	ConversationID string // thread_id；为空则新建
	Attachments    []any
}

// Turn 执行一次完整的 user_message 回合。onChunk 为 nil 时只返回最终结果。
func (c *Client) Turn(ctx context.Context, sess *core.Session, p ChatParams, onChunk func(core.Chunk)) (*core.TurnResult, error) {
	conn, err := DialWS(ctx, sess, c.proxy)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if p.AgentMode == "" {
		p.AgentMode = "smart"
	}
	if p.Timezone == "" {
		p.Timezone = "Asia/Shanghai"
	}
	if p.Locale == "" {
		p.Locale = "zh-CN"
	}

	// 1. session_connect：必须带 renderer 的 capability flags，否则会收到
	// unknown_message_type 并被提前关闭。
	if err := conn.Send("session_connect", map[string]any{
		"actor_id":               sess.ActorARN,
		"surfaceType":            SurfaceDesktop,
		"outpostId":              sess.DeviceID,
		"appVersion":             AppVersion,
		"clientInstanceId":       NewUUID(),
		"acp_first_text":         1,
		"user_notification":      1,
		"subtask_progress":       1,
		"external_agent_sign_in": 1,
		"recents_promotion":      1,
	}); err != nil {
		return nil, err
	}
	if err := waitReady(ctx, conn); err != nil {
		return nil, err
	}

	thread := p.ConversationID
	if thread == "" {
		thread = NewUUID()
	}
	// 2. session_create
	if err := conn.Send("session_create", map[string]any{
		"session_id": thread,
		"title":      "quick2api",
	}); err != nil {
		return nil, err
	}
	if err := waitEvent(ctx, conn, "session_created"); err != nil {
		return nil, err
	}

	// 3. user_message
	msg := map[string]any{
		"thread_id":      thread,
		"text":           p.Text,
		"message_id":     NewUUID(),
		"agent_mode":     p.AgentMode,
		"user_timezone":  p.Timezone,
		"user_locale":    p.Locale,
		"attachments":    p.Attachments,
	}
	if p.SystemPrompt != "" {
		msg["system_prompt"] = p.SystemPrompt
	}
	if p.CustomPrompt != "" {
		msg["custom_prompt"] = p.CustomPrompt
	}
	if p.ThinkingEffort != "" {
		msg["thinking_effort"] = p.ThinkingEffort
	}
	if err := conn.Send("user_message", msg); err != nil {
		return nil, err
	}

	// 4. 25s 心跳
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_ = conn.Send("ping", map[string]any{"timestamp": time.Now().UnixMilli()})
			}
		}
	}()
	defer close(stop)

	return c.readTurn(ctx, conn, onChunk)
}

func waitReady(ctx context.Context, conn *WSConn) error {
	established, connected := false, false
	deadline := time.Now().Add(60 * time.Second)
	for !(established && connected) {
		if time.Now().After(deadline) {
			return NewError("handshake_timeout", "session_connect did not complete", 504, true)
		}
		ev, err := conn.Recv(ctx)
		if err != nil {
			return err
		}
		switch ev.Type {
		case "session_established":
			established = true
		case "connected":
			connected = true
		case "error":
			return NewError("upstream_error", str(ev.Payload["message"]), 502, true)
		case "status":
			if str(ev.Payload["status"]) == "unknown_message_type" {
				return NewError("handshake_rejected", "session_connect rejected (capability flags mismatch)", 400, false)
			}
		}
	}
	return nil
}

func waitEvent(ctx context.Context, conn *WSConn, want string) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		if time.Now().After(deadline) {
			return NewError("handshake_timeout", "waiting for "+want, 504, true)
		}
		ev, err := conn.Recv(ctx)
		if err != nil {
			return err
		}
		if ev.Type == want {
			return nil
		}
		if ev.Type == "error" {
			return NewError("upstream_error", str(ev.Payload["message"]), 502, true)
		}
	}
}

func (c *Client) readTurn(ctx context.Context, conn *WSConn, onChunk func(core.Chunk)) (*core.TurnResult, error) {
	res := &core.TurnResult{}
	gotDelta := false
	deadline := time.Now().Add(c.timeout)
	for {
		if time.Now().After(deadline) {
			return nil, ErrTurnTimeout
		}
		ev, err := conn.Recv(ctx)
		if err != nil {
			return nil, err
		}
		switch ev.Type {
		case "message_start":
			// 多步 agent turn 会有多个 message_start，保留累积文本。
		case "text_delta":
			d := str(ev.Payload["delta"])
			if d != "" {
				gotDelta = true
				res.Text += d
				if onChunk != nil {
					onChunk(core.Chunk{Delta: d})
				}
			}
		case "thinking_delta":
			d := str(ev.Payload["delta"])
			if d != "" {
				res.Reasoning += d
				if onChunk != nil {
					onChunk(core.Chunk{Reasoning: d})
				}
			}
		case "turn_usage":
			res.Usage = parseUsage(ev.Payload)
		case "message_complete":
			// text_complete/message_complete 的 payload.text 是全文兜底。
			if !gotDelta {
				if full := str(ev.Payload["text"]); full != "" {
					res.Text = full
					if onChunk != nil {
						onChunk(core.Chunk{Delta: full})
					}
				}
			}
			if cont, _ := ev.Payload["continuation_expected"].(bool); !cont {
				if onChunk != nil {
					onChunk(core.Chunk{FinishReason: "stop", Usage: res.Usage})
				}
				return res, nil
			}
		case "error", "guardrail_blocked", "stream_truncated", "turn_aborted":
			msg := str(ev.Payload["message"])
			if msg == "" {
				msg = fmt.Sprintf("upstream %s", ev.Type)
			}
			return nil, NewError(ev.Type, msg, 502, ev.Type == "error")
		case "keepalive", "status", "runs_updated", "task_list_update", "title_generated":
			// 噪声帧，忽略
		}
	}
}

func parseUsage(p map[string]any) *core.Usage {
	u := &core.Usage{}
	u.InputTokens = asInt(p["input_tokens"])
	u.OutputTokens = asInt(p["output_tokens"])
	u.CacheReadTokens = asInt(p["cache_read_input_tokens"])
	if u.CacheReadTokens == 0 {
		u.CacheReadTokens = asInt(p["cache_read_tokens"])
	}
	u.CacheWriteTokens = asInt(p["cache_write_input_tokens"])
	if u.CacheWriteTokens == 0 {
		u.CacheWriteTokens = asInt(p["cache_write_tokens"])
	}
	u.TotalTokens = asInt(p["total_tokens"])
	if u.TotalTokens == 0 {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	u.ResolvedModel = str(p["resolved_model_id"])
	if u.ResolvedModel == "" {
		u.ResolvedModel = str(p["served_model_id"])
	}
	u.CostUSD = asFloat(p["cost_usd"])
	return u
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}
