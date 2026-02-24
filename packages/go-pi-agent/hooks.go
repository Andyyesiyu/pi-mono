package agent

import (
	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

// HookEventType identifies the type of hook event.
type HookEventType string

const (
	HookToolCall         HookEventType = "tool_call"
	HookToolResult       HookEventType = "tool_result"
	HookContext          HookEventType = "context"
	HookBeforeAgentStart HookEventType = "before_agent_start"
	HookAgentStart       HookEventType = "agent_start"
	HookAgentEnd         HookEventType = "agent_end"
	HookTurnStart        HookEventType = "turn_start"
	HookTurnEnd          HookEventType = "turn_end"
	HookSessionStart     HookEventType = "session_start"
	HookSessionShutdown  HookEventType = "session_shutdown"
)

// ToolCallHookEvent is the payload passed to tool_call hook handlers.
type ToolCallHookEvent struct {
	ToolCallID string
	ToolName   string
	Input      map[string]any
}

// ToolCallHookResult is returned by tool_call handlers.
// Set Block to true to prevent the tool from executing.
type ToolCallHookResult struct {
	Block  bool
	Reason string
}

// ToolResultHookEvent is the payload passed to tool_result hook handlers.
type ToolResultHookEvent struct {
	ToolCallID string
	ToolName   string
	Input      map[string]any
	Content    []ai.ToolResultContentBlock
	Details    any
	IsError    bool
}

// ToolResultHookResult is returned by tool_result handlers to modify the result.
// Only non-nil fields are applied.
type ToolResultHookResult struct {
	Content []ai.ToolResultContentBlock
	Details any
	IsError *bool
}

// ContextHookEvent is the payload passed to context hook handlers.
type ContextHookEvent struct {
	Messages []AgentMessage
}

// ContextHookResult is returned by context handlers to modify messages.
type ContextHookResult struct {
	Messages []AgentMessage
}

// BeforeAgentStartHookEvent is the payload for before_agent_start hooks.
type BeforeAgentStartHookEvent struct {
	SystemPrompt string
}

// BeforeAgentStartHookResult can modify the system prompt for the upcoming run.
type BeforeAgentStartHookResult struct {
	SystemPrompt string
}

// AgentEndHookEvent is the payload for agent_end hooks.
type AgentEndHookEvent struct {
	Messages []AgentMessage
}

// TurnEndHookEvent is the payload for turn_end hooks.
type TurnEndHookEvent struct {
	TurnMessage *AgentMessage
	ToolResults []*ai.ToolResultMessage
}

// ExtensionHooks holds all hook handlers registered by an extension.
type ExtensionHooks struct {
	ToolCall         []func(*ToolCallHookEvent) *ToolCallHookResult
	ToolResult       []func(*ToolResultHookEvent) *ToolResultHookResult
	Context          []func(*ContextHookEvent) *ContextHookResult
	BeforeAgentStart []func(*BeforeAgentStartHookEvent) *BeforeAgentStartHookResult
	AgentStart       []func()
	AgentEnd         []func(*AgentEndHookEvent)
	TurnStart        []func()
	TurnEnd          []func(*TurnEndHookEvent)
	SessionStart     []func()
	SessionShutdown  []func()
}

// HookRunner dispatches hook events across all registered extensions.
type HookRunner struct {
	extensions func() []*Extension
}

// NewHookRunner creates a HookRunner. The extensions function is called
// each time to get the current list of extensions (supporting hot-reload).
func NewHookRunner(extensions func() []*Extension) *HookRunner {
	return &HookRunner{extensions: extensions}
}

// RunToolCall dispatches a tool_call event to all extension handlers.
// If any handler returns Block=true, the tool call is prevented and
// the blocking result is returned.
func (r *HookRunner) RunToolCall(event *ToolCallHookEvent) *ToolCallHookResult {
	if r == nil {
		return nil
	}
	for _, ext := range r.extensions() {
		if ext.Hooks == nil {
			continue
		}
		for _, handler := range ext.Hooks.ToolCall {
			result := handler(event)
			if result != nil && result.Block {
				return result
			}
		}
	}
	return nil
}

// RunToolResult dispatches a tool_result event to all extension handlers.
// Handlers are chained: each handler sees the result of the previous one.
// The final modified result is returned, or nil if no handler modified it.
func (r *HookRunner) RunToolResult(event *ToolResultHookEvent) *ToolResultHookResult {
	if r == nil {
		return nil
	}
	var lastResult *ToolResultHookResult
	for _, ext := range r.extensions() {
		if ext.Hooks == nil {
			continue
		}
		for _, handler := range ext.Hooks.ToolResult {
			result := handler(event)
			if result != nil {
				if result.Content != nil {
					event.Content = result.Content
				}
				if result.Details != nil {
					event.Details = result.Details
				}
				if result.IsError != nil {
					event.IsError = *result.IsError
				}
				lastResult = result
			}
		}
	}
	return lastResult
}

// RunContext dispatches a context event to all extension handlers.
// Handlers are chained: each sees the messages from the previous one.
func (r *HookRunner) RunContext(event *ContextHookEvent) *ContextHookResult {
	if r == nil {
		return nil
	}
	var lastResult *ContextHookResult
	for _, ext := range r.extensions() {
		if ext.Hooks == nil {
			continue
		}
		for _, handler := range ext.Hooks.Context {
			result := handler(event)
			if result != nil && result.Messages != nil {
				event.Messages = result.Messages
				lastResult = result
			}
		}
	}
	return lastResult
}

// RunBeforeAgentStart dispatches a before_agent_start event.
// If multiple handlers return a SystemPrompt, they are chained.
func (r *HookRunner) RunBeforeAgentStart(event *BeforeAgentStartHookEvent) *BeforeAgentStartHookResult {
	if r == nil {
		return nil
	}
	var lastResult *BeforeAgentStartHookResult
	for _, ext := range r.extensions() {
		if ext.Hooks == nil {
			continue
		}
		for _, handler := range ext.Hooks.BeforeAgentStart {
			result := handler(event)
			if result != nil && result.SystemPrompt != "" {
				event.SystemPrompt = result.SystemPrompt
				lastResult = result
			}
		}
	}
	return lastResult
}

// RunAgentStart dispatches an agent_start lifecycle event.
func (r *HookRunner) RunAgentStart() {
	if r == nil {
		return
	}
	for _, ext := range r.extensions() {
		if ext.Hooks == nil {
			continue
		}
		for _, handler := range ext.Hooks.AgentStart {
			handler()
		}
	}
}

// RunAgentEnd dispatches an agent_end lifecycle event.
func (r *HookRunner) RunAgentEnd(event *AgentEndHookEvent) {
	if r == nil {
		return
	}
	for _, ext := range r.extensions() {
		if ext.Hooks == nil {
			continue
		}
		for _, handler := range ext.Hooks.AgentEnd {
			handler(event)
		}
	}
}

// RunTurnStart dispatches a turn_start lifecycle event.
func (r *HookRunner) RunTurnStart() {
	if r == nil {
		return
	}
	for _, ext := range r.extensions() {
		if ext.Hooks == nil {
			continue
		}
		for _, handler := range ext.Hooks.TurnStart {
			handler()
		}
	}
}

// RunTurnEnd dispatches a turn_end lifecycle event.
func (r *HookRunner) RunTurnEnd(event *TurnEndHookEvent) {
	if r == nil {
		return
	}
	for _, ext := range r.extensions() {
		if ext.Hooks == nil {
			continue
		}
		for _, handler := range ext.Hooks.TurnEnd {
			handler(event)
		}
	}
}

// RunSessionStart dispatches a session_start lifecycle event.
func (r *HookRunner) RunSessionStart() {
	if r == nil {
		return
	}
	for _, ext := range r.extensions() {
		if ext.Hooks == nil {
			continue
		}
		for _, handler := range ext.Hooks.SessionStart {
			handler()
		}
	}
}

// RunSessionShutdown dispatches a session_shutdown lifecycle event.
func (r *HookRunner) RunSessionShutdown() {
	if r == nil {
		return
	}
	for _, ext := range r.extensions() {
		if ext.Hooks == nil {
			continue
		}
		for _, handler := range ext.Hooks.SessionShutdown {
			handler()
		}
	}
}
