// Package agent provides a stateful agent runtime with tool execution.
// It wraps the ai package's LLM streaming with an event-driven loop
// that handles tool calls, steering messages, and follow-up messages.
package agent

import (
	"encoding/json"

	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

// ThinkingLevel controls the reasoning effort for the agent.
type ThinkingLevel string

const (
	ThinkingOff     ThinkingLevel = "off"
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
)

// AgentMessage is a union type that can hold standard LLM messages
// or custom application-specific messages.
type AgentMessage struct {
	// Standard message types (at most one is set)
	Message *ai.Message `json:"-"`

	// Custom message data for application-specific messages
	Custom map[string]any `json:"-"`

	// Role is always set for quick type checking
	RoleStr string `json:"role"`
}

// NewAgentMessageFromMessage wraps a standard Message as an AgentMessage.
func NewAgentMessageFromMessage(msg ai.Message) AgentMessage {
	return AgentMessage{
		Message: &msg,
		RoleStr: msg.Role(),
	}
}

// NewAgentMessageFromUser creates an agent message from a user message.
func NewAgentMessageFromUser(msg *ai.UserMessage) AgentMessage {
	m := ai.Message{User: msg}
	return AgentMessage{
		Message: &m,
		RoleStr: "user",
	}
}

// NewAgentMessageFromAssistant creates an agent message from an assistant message.
func NewAgentMessageFromAssistant(msg *ai.AssistantMessage) AgentMessage {
	m := ai.Message{Assistant: msg}
	return AgentMessage{
		Message: &m,
		RoleStr: "assistant",
	}
}

// NewAgentMessageFromToolResult creates an agent message from a tool result.
func NewAgentMessageFromToolResult(msg *ai.ToolResultMessage) AgentMessage {
	m := ai.Message{ToolResult: msg}
	return AgentMessage{
		Message: &m,
		RoleStr: "toolResult",
	}
}

// NewCustomAgentMessage creates a custom agent message with a role.
func NewCustomAgentMessage(role string, data map[string]any) AgentMessage {
	return AgentMessage{
		Custom:  data,
		RoleStr: role,
	}
}

// Role returns the role of the message.
func (m *AgentMessage) Role() string {
	return m.RoleStr
}

// IsUser returns true if this is a user message.
func (m *AgentMessage) IsUser() bool {
	return m.Message != nil && m.Message.User != nil
}

// IsAssistant returns true if this is an assistant message.
func (m *AgentMessage) IsAssistant() bool {
	return m.Message != nil && m.Message.Assistant != nil
}

// IsToolResult returns true if this is a tool result message.
func (m *AgentMessage) IsToolResult() bool {
	return m.Message != nil && m.Message.ToolResult != nil
}

// AsMessage returns the underlying ai.Message, or nil if this is a custom message.
func (m *AgentMessage) AsMessage() *ai.Message {
	return m.Message
}

// MarshalJSON implements custom JSON marshaling.
func (m AgentMessage) MarshalJSON() ([]byte, error) {
	if m.Message != nil {
		return json.Marshal(m.Message)
	}
	if m.Custom != nil {
		data := make(map[string]any)
		for k, v := range m.Custom {
			data[k] = v
		}
		data["role"] = m.RoleStr
		return json.Marshal(data)
	}
	return []byte("null"), nil
}

// AgentToolResult is the result of executing a tool.
type AgentToolResult struct {
	Content []ai.ToolResultContentBlock `json:"content"`
	Details any                         `json:"details"`
}

// AgentToolUpdateCallback is called during tool execution to stream progress.
type AgentToolUpdateCallback func(partialResult AgentToolResult)

// AgentTool extends ai.Tool with execution capability.
type AgentTool struct {
	ai.Tool
	Label   string                                                                                    `json:"label"`
	Execute func(toolCallID string, params map[string]any, signal *ai.AbortSignal, onUpdate AgentToolUpdateCallback) (*AgentToolResult, error) `json:"-"`
}

// AgentContext provides the conversation context for the agent loop.
type AgentContext struct {
	SystemPrompt string
	Messages     []AgentMessage
	Tools        []*AgentTool
}

// AgentState contains all configuration and conversation state.
type AgentState struct {
	SystemPrompt    string
	Model           *ai.Model
	ThinkingLevel   ThinkingLevel
	Tools           []*AgentTool
	Messages        []AgentMessage
	IsStreaming      bool
	StreamMessage   *AgentMessage
	PendingToolCalls map[string]bool
	Error           string
}

// AgentEvent represents events emitted by the agent for UI updates.
type AgentEvent struct {
	Type string `json:"type"`

	// For agent_end
	Messages []AgentMessage `json:"messages,omitempty"`

	// For turn_end
	TurnMessage *AgentMessage           `json:"message,omitempty"`
	ToolResults []*ai.ToolResultMessage `json:"toolResults,omitempty"`

	// For message_start, message_update, message_end
	EventMessage *AgentMessage `json:"eventMessage,omitempty"`

	// For message_update
	AssistantMessageEvent *ai.AssistantMessageEvent `json:"assistantMessageEvent,omitempty"`

	// For tool_execution_start, tool_execution_update, tool_execution_end
	ToolCallID    string `json:"toolCallId,omitempty"`
	ToolName      string `json:"toolName,omitempty"`
	Args          any    `json:"args,omitempty"`
	PartialResult any    `json:"partialResult,omitempty"`
	Result        any    `json:"result,omitempty"`
	IsError       bool   `json:"isError,omitempty"`
}

// StreamFn is a function type for custom streaming implementations.
type StreamFn func(model *ai.Model, ctx *ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream

// AgentLoopConfig configures the agent loop.
type AgentLoopConfig struct {
	ai.SimpleStreamOptions
	Model *ai.Model

	// ConvertToLLM converts AgentMessage[] to LLM-compatible Message[].
	ConvertToLLM func(messages []AgentMessage) ([]ai.Message, error)

	// TransformContext optionally transforms context before ConvertToLLM.
	TransformContext func(messages []AgentMessage, signal *ai.AbortSignal) ([]AgentMessage, error)

	// GetApiKey resolves an API key dynamically for each LLM call.
	GetApiKey func(provider string) (string, error)

	// GetSteeringMessages returns steering messages to inject mid-run.
	GetSteeringMessages func() ([]AgentMessage, error)

	// GetFollowUpMessages returns follow-up messages after the agent stops.
	GetFollowUpMessages func() ([]AgentMessage, error)

	// Hooks dispatches extension hook events during the agent loop.
	Hooks *HookRunner
}
