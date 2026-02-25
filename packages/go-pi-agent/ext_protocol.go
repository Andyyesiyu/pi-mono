package agent

import "encoding/json"

// JSON-RPC 2.0 protocol for process extension communication.
//
// Process extensions are subprocesses that communicate with the host via
// newline-delimited JSON-RPC 2.0 over stdin/stdout. The protocol supports:
//   - Initialization handshake (host → extension → host)
//   - Hook event dispatch (host → extension → host)
//   - Tool execution (host → extension → host)
//   - State updates (extension → host, via notifications)
//   - Graceful shutdown (host → extension)

// ExtProtocolVersion is the protocol version exchanged during handshake.
const ExtProtocolVersion = "1.0"

// JSON-RPC method constants.
const (
	// Lifecycle
	MethodInitialize  = "initialize"
	MethodInitialized = "initialized"
	MethodShutdown    = "shutdown"

	// Hook dispatch
	MethodHookToolCall         = "hook/tool_call"
	MethodHookToolResult       = "hook/tool_result"
	MethodHookContext          = "hook/context"
	MethodHookBeforeAgentStart = "hook/before_agent_start"
	MethodHookAgentStart       = "hook/agent_start"
	MethodHookAgentEnd         = "hook/agent_end"
	MethodHookTurnStart        = "hook/turn_start"
	MethodHookTurnEnd          = "hook/turn_end"
	MethodHookSessionStart     = "hook/session_start"
	MethodHookSessionShutdown  = "hook/session_shutdown"

	// Tool execution
	MethodToolExecute  = "tool/execute"
	MethodToolProgress = "tool/progress"

	// State management
	MethodStateSet = "state/set"
)

// RPCRequest is a JSON-RPC 2.0 request or notification.
// When ID is nil, it is a notification (no response expected).
type RPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// RPCResponse is a JSON-RPC 2.0 response.
type RPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	RPCParseError     = -32700
	RPCInvalidRequest = -32600
	RPCMethodNotFound = -32601
	RPCInvalidParams  = -32602
	RPCInternalError  = -32603
)

// --- Initialization ---

// InitializeParams is sent from host to extension during handshake.
type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	ExtensionID     string         `json:"extensionId"`
	ExtensionDir    string         `json:"extensionDir"`
	State           map[string]any `json:"state,omitempty"`
}

// InitializeResult is returned by the extension with its capabilities.
type InitializeResult struct {
	ProtocolVersion string           `json:"protocolVersion"`
	Hooks           []string         `json:"hooks,omitempty"`
	Tools           []ProcessToolDef `json:"tools,omitempty"`
}

// ProcessToolDef describes a tool registered by a process extension.
type ProcessToolDef struct {
	Name        string         `json:"name"`
	Label       string         `json:"label"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// --- Tool execution ---

// ToolExecuteParams is sent to execute a tool in the extension process.
type ToolExecuteParams struct {
	ToolCallID string         `json:"toolCallId"`
	ToolName   string         `json:"toolName"`
	Params     map[string]any `json:"params"`
}

// ToolExecuteResult is returned after tool execution.
type ToolExecuteResult struct {
	Content []json.RawMessage `json:"content"`
	Details any               `json:"details,omitempty"`
	IsError bool              `json:"isError,omitempty"`
}

// --- State ---

// StateSetParams is sent by extensions to update persistent state.
type StateSetParams struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// --- Hook event wire types ---
// These mirror the in-process hook types but with JSON tags for serialization.

// WireToolCallHookEvent is the JSON wire format for tool_call hooks.
type WireToolCallHookEvent struct {
	ToolCallID string         `json:"toolCallId"`
	ToolName   string         `json:"toolName"`
	Input      map[string]any `json:"input"`
}

// WireToolCallHookResult is the JSON wire format for tool_call hook results.
type WireToolCallHookResult struct {
	Block  bool   `json:"block"`
	Reason string `json:"reason,omitempty"`
}

// WireToolResultHookEvent is the JSON wire format for tool_result hooks.
type WireToolResultHookEvent struct {
	ToolCallID string            `json:"toolCallId"`
	ToolName   string            `json:"toolName"`
	Input      map[string]any    `json:"input"`
	Content    []json.RawMessage `json:"content"`
	IsError    bool              `json:"isError"`
}

// WireToolResultHookResult is the JSON wire format for tool_result hook results.
type WireToolResultHookResult struct {
	Content []json.RawMessage `json:"content,omitempty"`
	IsError *bool             `json:"isError,omitempty"`
}

// WireContextHookEvent is the JSON wire format for context hooks.
type WireContextHookEvent struct {
	Messages []json.RawMessage `json:"messages"`
}

// WireContextHookResult is the JSON wire format for context hook results.
type WireContextHookResult struct {
	Messages []json.RawMessage `json:"messages,omitempty"`
}

// WireBeforeAgentStartHookEvent is the JSON wire format for before_agent_start hooks.
type WireBeforeAgentStartHookEvent struct {
	SystemPrompt string `json:"systemPrompt"`
}

// WireBeforeAgentStartHookResult is the JSON wire format for before_agent_start hook results.
type WireBeforeAgentStartHookResult struct {
	SystemPrompt string `json:"systemPrompt,omitempty"`
}

// WireAgentEndHookEvent is the JSON wire format for agent_end hooks.
type WireAgentEndHookEvent struct {
	Messages []json.RawMessage `json:"messages"`
}

// WireTurnEndHookEvent is the JSON wire format for turn_end hooks.
type WireTurnEndHookEvent struct {
	TurnMessage json.RawMessage   `json:"turnMessage"`
	ToolResults []json.RawMessage `json:"toolResults"`
}

// hookMethodForType maps HookEventType to JSON-RPC method names.
var hookMethodForType = map[HookEventType]string{
	HookToolCall:         MethodHookToolCall,
	HookToolResult:       MethodHookToolResult,
	HookContext:          MethodHookContext,
	HookBeforeAgentStart: MethodHookBeforeAgentStart,
	HookAgentStart:       MethodHookAgentStart,
	HookAgentEnd:         MethodHookAgentEnd,
	HookTurnStart:        MethodHookTurnStart,
	HookTurnEnd:          MethodHookTurnEnd,
	HookSessionStart:     MethodHookSessionStart,
	HookSessionShutdown:  MethodHookSessionShutdown,
}
