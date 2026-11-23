// Package core 定义插件内部与宿主能力层之间的中性契约。
// 不依赖 gorilla/websocket 等实现细节，便于后续替换协议实现。
package core

// Session 是 Quick 的会话凭据，既可来自 OAuth，也可来自桌面端凭据/outpost。
type Session struct {
	IDToken     string `json:"id_token"`
	AccessToken string `json:"access_token,omitempty"`
	WSHost      string `json:"ws_host"`
	TenantURL   string `json:"tenant_url,omitempty"`
	ActorARN    string `json:"actor_arn,omitempty"`
	Provider    string `json:"provider"` // social / gateway
	AccountID   string `json:"aws_account_id,omitempty"`
	Email       string `json:"email,omitempty"`
	UsableUntil int64  `json:"usable_until"`

	// OAuth 专用：用于刷新
	RefreshToken string `json:"refresh_token,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	IssuerURL    string `json:"issuer_url,omitempty"`
	IDCRegion    string `json:"idc_region,omitempty"`

	// 设备 ID：WS 与附件上传都需要，OAuth 账号固定一个稳定 UUID。
	DeviceID string `json:"device_id,omitempty"`
}

func (s *Session) Expired() bool {
	if s == nil {
		return true
	}
	return s.UsableUntil > 0 && s.UsableUntil <= NowUnix()
}

// Message 是规范化的对话消息。
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ChatRequest 由 executor 能力层传入。
type ChatRequest struct {
	Model           string         `json:"model"`
	Messages        []Message      `json:"messages"`
	Stream          bool           `json:"stream"`
	MaxTokens       int            `json:"max_tokens,omitempty"`
	Temperature     *float64       `json:"temperature,omitempty"`
	Tools           []Tool         `json:"tools,omitempty"`
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
	ConversationID  string         `json:"conversation_id,omitempty"`
	Extra           map[string]any `json:"extra,omitempty"`
}

// Usage 与 turn_usage 对齐，同时兼容 OpenAI 的 usage 字段。
type Usage struct {
	InputTokens     int     `json:"input_tokens"`
	OutputTokens    int     `json:"output_tokens"`
	CacheReadTokens int     `json:"cache_read_tokens"`
	CacheWriteTokens int    `json:"cache_write_tokens"`
	TotalTokens     int     `json:"total_tokens"`
	ResolvedModel   string  `json:"resolved_model,omitempty"`
	CostUSD         float64 `json:"cost_usd,omitempty"`
}

// Chunk 是流式的规范化增量。
type Chunk struct {
	Delta        string     `json:"delta,omitempty"`
	Reasoning    string     `json:"reasoning,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	Usage        *Usage     `json:"usage,omitempty"`
}

// TurnResult 是一次非流式调用的结果。
type TurnResult struct {
	Text      string     `json:"text"`
	Reasoning string     `json:"reasoning,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Usage     *Usage     `json:"usage,omitempty"`
}
