// Package ai provides a unified LLM API provider abstraction.
// It defines common types for messages, models, tools, and streaming
// that work across multiple LLM providers (Anthropic, OpenAI, Google, etc.).
package ai

import "encoding/json"

// KnownApi represents the known API protocol types.
type KnownApi string

const (
	ApiOpenAICompletions    KnownApi = "openai-completions"
	ApiOpenAIResponses      KnownApi = "openai-responses"
	ApiAzureOpenAIResponses KnownApi = "azure-openai-responses"
	ApiOpenAICodexResponses KnownApi = "openai-codex-responses"
	ApiAnthropicMessages    KnownApi = "anthropic-messages"
	ApiBedrockConverseStream KnownApi = "bedrock-converse-stream"
	ApiGoogleGenerativeAI   KnownApi = "google-generative-ai"
	ApiGoogleGeminiCli      KnownApi = "google-gemini-cli"
	ApiGoogleVertex         KnownApi = "google-vertex"
)

// Api is a string type for API protocol identifiers.
type Api = string

// KnownProvider represents a known LLM provider.
type KnownProvider string

const (
	ProviderAmazonBedrock    KnownProvider = "amazon-bedrock"
	ProviderAnthropic        KnownProvider = "anthropic"
	ProviderGoogle           KnownProvider = "google"
	ProviderGoogleGeminiCli  KnownProvider = "google-gemini-cli"
	ProviderGoogleAntigravity KnownProvider = "google-antigravity"
	ProviderGoogleVertex     KnownProvider = "google-vertex"
	ProviderOpenAI           KnownProvider = "openai"
	ProviderAzureOpenAI      KnownProvider = "azure-openai-responses"
	ProviderOpenAICodex      KnownProvider = "openai-codex"
	ProviderGithubCopilot    KnownProvider = "github-copilot"
	ProviderXAI              KnownProvider = "xai"
	ProviderGroq             KnownProvider = "groq"
	ProviderCerebras         KnownProvider = "cerebras"
	ProviderOpenRouter       KnownProvider = "openrouter"
	ProviderVercelAIGateway  KnownProvider = "vercel-ai-gateway"
	ProviderZAI              KnownProvider = "zai"
	ProviderMistral          KnownProvider = "mistral"
	ProviderMiniMax          KnownProvider = "minimax"
	ProviderMiniMaxCN        KnownProvider = "minimax-cn"
	ProviderHuggingFace      KnownProvider = "huggingface"
	ProviderOpenCode         KnownProvider = "opencode"
	ProviderKimiCoding       KnownProvider = "kimi-coding"
)

// Provider is a string type for provider identifiers.
type Provider = string

// ThinkingLevel controls the reasoning effort for models that support it.
type ThinkingLevel string

const (
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
)

// ThinkingBudgets defines token budgets for each thinking level.
type ThinkingBudgets struct {
	Minimal *int `json:"minimal,omitempty"`
	Low     *int `json:"low,omitempty"`
	Medium  *int `json:"medium,omitempty"`
	High    *int `json:"high,omitempty"`
}

// CacheRetention controls prompt cache retention.
type CacheRetention string

const (
	CacheNone  CacheRetention = "none"
	CacheShort CacheRetention = "short"
	CacheLong  CacheRetention = "long"
)

// StreamOptions are the base options shared by all streaming calls.
type StreamOptions struct {
	Temperature    *float64          `json:"temperature,omitempty"`
	MaxTokens      *int              `json:"maxTokens,omitempty"`
	Signal         *AbortSignal      `json:"-"`
	ApiKey         string            `json:"apiKey,omitempty"`
	CacheRetention CacheRetention    `json:"cacheRetention,omitempty"`
	SessionID      string            `json:"sessionId,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	MaxRetryDelayMs *int             `json:"maxRetryDelayMs,omitempty"`
	OnPayload      func(payload any) `json:"-"`
}

// SimpleStreamOptions extends StreamOptions with reasoning support.
type SimpleStreamOptions struct {
	StreamOptions
	Reasoning       ThinkingLevel    `json:"reasoning,omitempty"`
	ThinkingBudgets *ThinkingBudgets `json:"thinkingBudgets,omitempty"`
}

// TextContent represents a text content block.
type TextContent struct {
	Type          string `json:"type"` // Always "text"
	Text          string `json:"text"`
	TextSignature string `json:"textSignature,omitempty"`
}

// ThinkingContent represents a thinking/reasoning content block.
type ThinkingContent struct {
	Type              string `json:"type"` // Always "thinking"
	Thinking          string `json:"thinking"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
}

// ImageContent represents an image content block.
type ImageContent struct {
	Type     string `json:"type"` // Always "image"
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// ToolCall represents a tool call from the assistant.
type ToolCall struct {
	Type             string         `json:"type"` // Always "toolCall"
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Arguments        map[string]any `json:"arguments"`
	ThoughtSignature string         `json:"thoughtSignature,omitempty"`
}

// ContentBlock is a union type for assistant message content blocks.
// Only one of the fields will be non-nil.
type ContentBlock struct {
	Text     *TextContent     `json:"-"`
	Thinking *ThinkingContent `json:"-"`
	ToolCall *ToolCall        `json:"-"`
}

// ContentType returns the type of the content block.
func (c *ContentBlock) ContentType() string {
	if c.Text != nil {
		return "text"
	}
	if c.Thinking != nil {
		return "thinking"
	}
	if c.ToolCall != nil {
		return "toolCall"
	}
	return ""
}

// MarshalJSON implements custom JSON marshaling for ContentBlock.
func (c ContentBlock) MarshalJSON() ([]byte, error) {
	if c.Text != nil {
		return json.Marshal(c.Text)
	}
	if c.Thinking != nil {
		return json.Marshal(c.Thinking)
	}
	if c.ToolCall != nil {
		return json.Marshal(c.ToolCall)
	}
	return []byte("null"), nil
}

// UnmarshalJSON implements custom JSON unmarshaling for ContentBlock.
func (c *ContentBlock) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	typeBytes, ok := raw["type"]
	if !ok {
		return nil
	}
	var typ string
	if err := json.Unmarshal(typeBytes, &typ); err != nil {
		return err
	}

	switch typ {
	case "text":
		c.Text = &TextContent{}
		return json.Unmarshal(data, c.Text)
	case "thinking":
		c.Thinking = &ThinkingContent{}
		return json.Unmarshal(data, c.Thinking)
	case "toolCall":
		c.ToolCall = &ToolCall{}
		return json.Unmarshal(data, c.ToolCall)
	}
	return nil
}

// UserContentBlock is a union type for user message content blocks.
type UserContentBlock struct {
	Text  *TextContent  `json:"-"`
	Image *ImageContent `json:"-"`
}

// ContentType returns the type of the content block.
func (u *UserContentBlock) ContentType() string {
	if u.Text != nil {
		return "text"
	}
	if u.Image != nil {
		return "image"
	}
	return ""
}

// MarshalJSON implements custom JSON marshaling for UserContentBlock.
func (u UserContentBlock) MarshalJSON() ([]byte, error) {
	if u.Text != nil {
		return json.Marshal(u.Text)
	}
	if u.Image != nil {
		return json.Marshal(u.Image)
	}
	return []byte("null"), nil
}

// UnmarshalJSON implements custom JSON unmarshaling for UserContentBlock.
func (u *UserContentBlock) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	typeBytes, ok := raw["type"]
	if !ok {
		return nil
	}
	var typ string
	if err := json.Unmarshal(typeBytes, &typ); err != nil {
		return err
	}

	switch typ {
	case "text":
		u.Text = &TextContent{}
		return json.Unmarshal(data, u.Text)
	case "image":
		u.Image = &ImageContent{}
		return json.Unmarshal(data, u.Image)
	}
	return nil
}

// ToolResultContentBlock is a union type for tool result content blocks.
type ToolResultContentBlock struct {
	Text  *TextContent  `json:"-"`
	Image *ImageContent `json:"-"`
}

// ContentType returns the type of the content block.
func (t *ToolResultContentBlock) ContentType() string {
	if t.Text != nil {
		return "text"
	}
	if t.Image != nil {
		return "image"
	}
	return ""
}

// MarshalJSON implements custom JSON marshaling for ToolResultContentBlock.
func (t ToolResultContentBlock) MarshalJSON() ([]byte, error) {
	if t.Text != nil {
		return json.Marshal(t.Text)
	}
	if t.Image != nil {
		return json.Marshal(t.Image)
	}
	return []byte("null"), nil
}

// UnmarshalJSON implements custom JSON unmarshaling for ToolResultContentBlock.
func (t *ToolResultContentBlock) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	typeBytes, ok := raw["type"]
	if !ok {
		return nil
	}
	var typ string
	if err := json.Unmarshal(typeBytes, &typ); err != nil {
		return err
	}

	switch typ {
	case "text":
		t.Text = &TextContent{}
		return json.Unmarshal(data, t.Text)
	case "image":
		t.Image = &ImageContent{}
		return json.Unmarshal(data, t.Image)
	}
	return nil
}

// Usage tracks token usage and cost for a completion.
type Usage struct {
	Input      int      `json:"input"`
	Output     int      `json:"output"`
	CacheRead  int      `json:"cacheRead"`
	CacheWrite int      `json:"cacheWrite"`
	TotalTokens int     `json:"totalTokens"`
	Cost       UsageCost `json:"cost"`
}

// UsageCost tracks the monetary cost of token usage.
type UsageCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

// StopReason indicates why the model stopped generating.
type StopReason string

const (
	StopReasonStop    StopReason = "stop"
	StopReasonLength  StopReason = "length"
	StopReasonToolUse StopReason = "toolUse"
	StopReasonError   StopReason = "error"
	StopReasonAborted StopReason = "aborted"
)

// UserMessage represents a user's message.
type UserMessage struct {
	Role      string             `json:"role"` // Always "user"
	Content   UserContent        `json:"content"`
	Timestamp int64              `json:"timestamp"`
}

// UserContent can be either a string or a slice of UserContentBlock.
type UserContent struct {
	Text   string             // Simple string content
	Blocks []UserContentBlock // Structured content blocks
}

// IsText returns true if the content is a simple string.
func (u *UserContent) IsText() bool {
	return u.Text != "" || len(u.Blocks) == 0
}

// MarshalJSON implements custom JSON marshaling for UserContent.
func (u UserContent) MarshalJSON() ([]byte, error) {
	if len(u.Blocks) > 0 {
		return json.Marshal(u.Blocks)
	}
	return json.Marshal(u.Text)
}

// UnmarshalJSON implements custom JSON unmarshaling for UserContent.
func (u *UserContent) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		return json.Unmarshal(data, &u.Text)
	}
	return json.Unmarshal(data, &u.Blocks)
}

// AssistantMessage represents the assistant's response.
type AssistantMessage struct {
	Role         string         `json:"role"` // Always "assistant"
	Content      []ContentBlock `json:"content"`
	ApiType      Api            `json:"api"`
	Provider     Provider       `json:"provider"`
	Model        string         `json:"model"`
	Usage        Usage          `json:"usage"`
	StopReason   StopReason     `json:"stopReason"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	Timestamp    int64          `json:"timestamp"`
}

// ToolResultMessage represents a tool execution result.
type ToolResultMessage struct {
	Role       string                   `json:"role"` // Always "toolResult"
	ToolCallID string                   `json:"toolCallId"`
	ToolName   string                   `json:"toolName"`
	Content    []ToolResultContentBlock `json:"content"`
	Details    any                      `json:"details,omitempty"`
	IsError    bool                     `json:"isError"`
	Timestamp  int64                    `json:"timestamp"`
}

// Message is a union type for all message types.
type Message struct {
	User      *UserMessage       `json:"-"`
	Assistant *AssistantMessage   `json:"-"`
	ToolResult *ToolResultMessage `json:"-"`
}

// Role returns the role of the message.
func (m *Message) Role() string {
	if m.User != nil {
		return "user"
	}
	if m.Assistant != nil {
		return "assistant"
	}
	if m.ToolResult != nil {
		return "toolResult"
	}
	return ""
}

// Timestamp returns the timestamp of the message.
func (m *Message) Timestamp() int64 {
	if m.User != nil {
		return m.User.Timestamp
	}
	if m.Assistant != nil {
		return m.Assistant.Timestamp
	}
	if m.ToolResult != nil {
		return m.ToolResult.Timestamp
	}
	return 0
}

// MarshalJSON implements custom JSON marshaling for Message.
func (m Message) MarshalJSON() ([]byte, error) {
	if m.User != nil {
		return json.Marshal(m.User)
	}
	if m.Assistant != nil {
		return json.Marshal(m.Assistant)
	}
	if m.ToolResult != nil {
		return json.Marshal(m.ToolResult)
	}
	return []byte("null"), nil
}

// UnmarshalJSON implements custom JSON unmarshaling for Message.
func (m *Message) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	roleBytes, ok := raw["role"]
	if !ok {
		return nil
	}
	var role string
	if err := json.Unmarshal(roleBytes, &role); err != nil {
		return err
	}

	switch role {
	case "user":
		m.User = &UserMessage{}
		return json.Unmarshal(data, m.User)
	case "assistant":
		m.Assistant = &AssistantMessage{}
		return json.Unmarshal(data, m.Assistant)
	case "toolResult":
		m.ToolResult = &ToolResultMessage{}
		return json.Unmarshal(data, m.ToolResult)
	}
	return nil
}

// NewUserMessage creates a new user message from a string.
func NewUserMessage(text string, timestamp int64) Message {
	return Message{
		User: &UserMessage{
			Role:      "user",
			Content:   UserContent{Text: text},
			Timestamp: timestamp,
		},
	}
}

// NewAssistantMessage creates a new empty assistant message.
func NewAssistantMessage(api Api, provider Provider, model string, timestamp int64) *AssistantMessage {
	return &AssistantMessage{
		Role:      "assistant",
		Content:   []ContentBlock{},
		ApiType:   api,
		Provider:  provider,
		Model:     model,
		Usage:     Usage{},
		StopReason: StopReasonStop,
		Timestamp: timestamp,
	}
}

// NewToolResultMessage creates a new tool result message.
func NewToolResultMessage(toolCallID, toolName string, content []ToolResultContentBlock, isError bool, timestamp int64) Message {
	return Message{
		ToolResult: &ToolResultMessage{
			Role:       "toolResult",
			ToolCallID: toolCallID,
			ToolName:   toolName,
			Content:    content,
			IsError:    isError,
			Timestamp:  timestamp,
		},
	}
}

// Tool defines a tool that the LLM can call.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Context provides the conversation context for an LLM call.
type Context struct {
	SystemPrompt string    `json:"systemPrompt,omitempty"`
	Messages     []Message `json:"messages"`
	Tools        []Tool    `json:"tools,omitempty"`
}

// ModelCost defines the cost per million tokens for a model.
type ModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// Model represents an LLM model with its configuration.
type Model struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	ApiType       Api               `json:"api"`
	Provider      Provider          `json:"provider"`
	BaseURL       string            `json:"baseUrl"`
	Reasoning     bool              `json:"reasoning"`
	Input         []string          `json:"input"`
	Cost          ModelCost         `json:"cost"`
	ContextWindow int               `json:"contextWindow"`
	MaxTokens     int               `json:"maxTokens"`
	Headers       map[string]string `json:"headers,omitempty"`
}

// AssistantMessageEvent represents streaming events from the LLM.
type AssistantMessageEvent struct {
	Type         string            `json:"type"`
	ContentIndex int               `json:"contentIndex,omitempty"`
	Delta        string            `json:"delta,omitempty"`
	Content      string            `json:"content,omitempty"`
	ToolCallData *ToolCall         `json:"toolCall,omitempty"`
	Reason       StopReason        `json:"reason,omitempty"`
	Partial      *AssistantMessage `json:"partial,omitempty"`
	FinalMessage *AssistantMessage `json:"message,omitempty"`
	ErrorMessage *AssistantMessage `json:"error,omitempty"`
}

// AbortSignal provides cooperative cancellation.
type AbortSignal struct {
	aborted chan struct{}
	done    bool
}

// NewAbortSignal creates a new AbortSignal.
func NewAbortSignal() *AbortSignal {
	return &AbortSignal{
		aborted: make(chan struct{}),
	}
}

// Abort signals cancellation.
func (a *AbortSignal) Abort() {
	if !a.done {
		a.done = true
		close(a.aborted)
	}
}

// Aborted returns true if the signal has been aborted.
func (a *AbortSignal) Aborted() bool {
	if a == nil {
		return false
	}
	select {
	case <-a.aborted:
		return true
	default:
		return false
	}
}

// Done returns a channel that is closed when the signal is aborted.
func (a *AbortSignal) Done() <-chan struct{} {
	if a == nil {
		return nil
	}
	return a.aborted
}

// StreamFunction is the type signature for provider stream functions.
type StreamFunction func(model *Model, ctx *Context, options *StreamOptions) *AssistantMessageEventStream

// SimpleStreamFunction is the type signature for simple stream functions.
type SimpleStreamFunction func(model *Model, ctx *Context, options *SimpleStreamOptions) *AssistantMessageEventStream
