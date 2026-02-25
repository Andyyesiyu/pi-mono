package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

// ===== Test extension subprocess =====
//
// When the test binary is run with PI_TEST_EXT_MODE set, it acts as
// a mock extension process, reading JSON-RPC from stdin and writing
// to stdout.

func init() {
	if os.Getenv("PI_TEST_EXT_MODE") != "" {
		runMockExtension()
		os.Exit(0)
	}
}

func runMockExtension() {
	mode := os.Getenv("PI_TEST_EXT_MODE")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1*1024*1024), 1*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req RPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}

		// Notifications have no ID — no response needed.
		if req.ID == nil {
			if req.Method == MethodShutdown {
				return
			}
			continue
		}

		var resp RPCResponse
		resp.JSONRPC = "2.0"
		resp.ID = req.ID

		switch mode {
		case "basic":
			resp.Result = handleBasicMode(req.Method, req.Params)
		case "blocker":
			resp.Result = handleBlockerMode(req.Method, req.Params)
		case "tool_provider":
			resp.Result = handleToolProviderMode(req.Method, req.Params)
		case "state_updater":
			resp.Result = handleStateUpdaterMode(req.Method, req.Params)
		case "context_modifier":
			resp.Result = handleContextModifierMode(req.Method, req.Params)
		default:
			resp.Error = &RPCError{Code: RPCMethodNotFound, Message: "unknown mode"}
		}

		data, _ := json.Marshal(resp)
		fmt.Fprintf(os.Stdout, "%s\n", data)
	}
}

// basic mode: registers tool_call and before_agent_start hooks
func handleBasicMode(method string, params json.RawMessage) json.RawMessage {
	switch method {
	case MethodInitialize:
		result := InitializeResult{
			ProtocolVersion: ExtProtocolVersion,
			Hooks:           []string{"tool_call", "before_agent_start"},
		}
		data, _ := json.Marshal(result)
		return data

	case MethodHookToolCall:
		// Allow all tool calls.
		result := WireToolCallHookResult{Block: false}
		data, _ := json.Marshal(result)
		return data

	case MethodHookBeforeAgentStart:
		var event WireBeforeAgentStartHookEvent
		json.Unmarshal(params, &event)
		result := WireBeforeAgentStartHookResult{
			SystemPrompt: event.SystemPrompt + "\n[injected by process extension]",
		}
		data, _ := json.Marshal(result)
		return data
	}

	data, _ := json.Marshal(map[string]any{})
	return data
}

// blocker mode: blocks all bash tool calls
func handleBlockerMode(method string, params json.RawMessage) json.RawMessage {
	switch method {
	case MethodInitialize:
		result := InitializeResult{
			ProtocolVersion: ExtProtocolVersion,
			Hooks:           []string{"tool_call"},
		}
		data, _ := json.Marshal(result)
		return data

	case MethodHookToolCall:
		var event WireToolCallHookEvent
		json.Unmarshal(params, &event)
		block := event.ToolName == "Bash"
		result := WireToolCallHookResult{Block: block, Reason: "bash is blocked"}
		data, _ := json.Marshal(result)
		return data
	}

	data, _ := json.Marshal(map[string]any{})
	return data
}

// tool_provider mode: registers a custom tool
func handleToolProviderMode(method string, params json.RawMessage) json.RawMessage {
	switch method {
	case MethodInitialize:
		result := InitializeResult{
			ProtocolVersion: ExtProtocolVersion,
			Hooks:           []string{},
			Tools: []ProcessToolDef{
				{
					Name:        "echo_tool",
					Label:       "Echo",
					Description: "Echoes input back",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"message": map[string]any{
								"type":        "string",
								"description": "Message to echo",
							},
						},
						"required": []any{"message"},
					},
				},
			},
		}
		data, _ := json.Marshal(result)
		return data

	case MethodToolExecute:
		var execParams ToolExecuteParams
		json.Unmarshal(params, &execParams)
		msg := "echo: "
		if m, ok := execParams.Params["message"].(string); ok {
			msg += m
		}
		result := ToolExecuteResult{
			Content: []json.RawMessage{
				mustMarshal(map[string]any{"type": "text", "text": msg}),
			},
		}
		data, _ := json.Marshal(result)
		return data
	}

	data, _ := json.Marshal(map[string]any{})
	return data
}

// state_updater mode: sends state/set notification after initialize
func handleStateUpdaterMode(method string, params json.RawMessage) json.RawMessage {
	switch method {
	case MethodInitialize:
		// Send a state update notification before responding.
		stateNotif := RPCRequest{
			JSONRPC: "2.0",
			Method:  MethodStateSet,
			Params:  mustMarshal(StateSetParams{Key: "initialized_at", Value: "2025-01-01"}),
		}
		data, _ := json.Marshal(stateNotif)
		fmt.Fprintf(os.Stdout, "%s\n", data)

		result := InitializeResult{
			ProtocolVersion: ExtProtocolVersion,
			Hooks:           []string{"tool_call"},
		}
		rdata, _ := json.Marshal(result)
		return rdata

	case MethodHookToolCall:
		// Send another state update.
		stateNotif := RPCRequest{
			JSONRPC: "2.0",
			Method:  MethodStateSet,
			Params:  mustMarshal(StateSetParams{Key: "last_tool_call", Value: "seen"}),
		}
		data, _ := json.Marshal(stateNotif)
		fmt.Fprintf(os.Stdout, "%s\n", data)

		result := WireToolCallHookResult{Block: false}
		rdata, _ := json.Marshal(result)
		return rdata
	}

	data, _ := json.Marshal(map[string]any{})
	return data
}

// context_modifier mode: modifies context messages
func handleContextModifierMode(method string, params json.RawMessage) json.RawMessage {
	switch method {
	case MethodInitialize:
		result := InitializeResult{
			ProtocolVersion: ExtProtocolVersion,
			Hooks:           []string{"context", "tool_result"},
		}
		data, _ := json.Marshal(result)
		return data

	case MethodHookContext:
		// Return messages with an additional marker.
		var event WireContextHookEvent
		json.Unmarshal(params, &event)
		// Add a marker message.
		marker := mustMarshal(map[string]any{"role": "context_marker"})
		event.Messages = append(event.Messages, marker)
		result := WireContextHookResult{Messages: event.Messages}
		data, _ := json.Marshal(result)
		return data

	case MethodHookToolResult:
		// Append " [reviewed]" to text content.
		var event WireToolResultHookEvent
		json.Unmarshal(params, &event)
		var newContent []json.RawMessage
		for _, c := range event.Content {
			var block map[string]any
			if json.Unmarshal(c, &block) == nil {
				if text, ok := block["text"].(string); ok {
					block["text"] = text + " [reviewed]"
				}
				newContent = append(newContent, mustMarshal(block))
			}
		}
		isErr := event.IsError
		result := WireToolResultHookResult{Content: newContent, IsError: &isErr}
		data, _ := json.Marshal(result)
		return data
	}

	data, _ := json.Marshal(map[string]any{})
	return data
}

func mustMarshal(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// ===== Helpers =====

// createTestExtScript creates a shell script that re-execs the test binary
// in the given mode.
func createTestExtScript(t *testing.T, mode string) (dir string) {
	t.Helper()
	dir = t.TempDir()

	// Create the entry script that re-invokes our test binary.
	scriptPath := filepath.Join(dir, "extension")
	testBinary, _ := os.Executable()

	script := fmt.Sprintf("#!/bin/sh\nexec env PI_TEST_EXT_MODE=%s %s -test.run=^$\n", mode, testBinary)
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	// Create manifest.
	manifest := ExtensionManifest{
		ID:         "test-" + mode,
		Name:       "Test " + mode,
		Type:       ProcessExtensionType,
		EntryPoint: "extension",
	}
	manifestData, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestData, 0644); err != nil {
		t.Fatal(err)
	}

	return dir
}

func startTestProcessExtension(t *testing.T, mode string) (*Extension, *ProcessExtension) {
	t.Helper()
	dir := createTestExtScript(t, mode)

	ext := &Extension{
		Manifest: ExtensionManifest{
			ID:         "test-" + mode,
			Name:       "Test " + mode,
			Type:       ProcessExtensionType,
			EntryPoint: "extension",
		},
		Dir:   dir,
		State: make(map[string]any),
	}

	proc := NewProcessExtension(ext)
	if err := proc.Start(); err != nil {
		t.Fatalf("failed to start process extension: %v", err)
	}
	t.Cleanup(func() { proc.Stop() })

	return ext, proc
}

// ===== Tests =====

func TestProcessExtensionInitialize(t *testing.T) {
	// Verify test binary can be found.
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	_, proc := startTestProcessExtension(t, "basic")

	if !proc.IsRunning() {
		t.Fatal("expected process to be running")
	}

	// Verify subscribed hooks.
	if !proc.subscribedHooks["tool_call"] {
		t.Error("expected tool_call hook subscription")
	}
	if !proc.subscribedHooks["before_agent_start"] {
		t.Error("expected before_agent_start hook subscription")
	}
	if proc.subscribedHooks["context"] {
		t.Error("should not have context hook subscription")
	}
}

func TestProcessExtensionToolCallHook(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	ext, proc := startTestProcessExtension(t, "blocker")
	ext.Hooks = proc.BuildHooks()

	runner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	// Bash should be blocked.
	result := runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-1",
		ToolName:   "Bash",
		Input:      map[string]any{"command": "ls"},
	})
	if result == nil || !result.Block {
		t.Fatal("expected Bash to be blocked")
	}

	// Read should not be blocked.
	result = runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-2",
		ToolName:   "Read",
		Input:      map[string]any{"file_path": "/tmp/test"},
	})
	if result != nil && result.Block {
		t.Fatal("expected Read to not be blocked")
	}
}

func TestProcessExtensionBeforeAgentStartHook(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	ext, proc := startTestProcessExtension(t, "basic")
	ext.Hooks = proc.BuildHooks()

	runner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	result := runner.RunBeforeAgentStart(&BeforeAgentStartHookEvent{
		SystemPrompt: "You are a helpful assistant.",
	})
	if result == nil {
		t.Fatal("expected before_agent_start result")
	}
	if !strings.Contains(result.SystemPrompt, "[injected by process extension]") {
		t.Fatalf("expected injected text in system prompt, got: %s", result.SystemPrompt)
	}
}

func TestProcessExtensionToolProvider(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	_, proc := startTestProcessExtension(t, "tool_provider")

	tools := proc.BuildTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0]
	if tool.Name != "echo_tool" {
		t.Fatalf("expected tool name 'echo_tool', got %q", tool.Name)
	}
	if tool.Label != "Echo" {
		t.Fatalf("expected label 'Echo', got %q", tool.Label)
	}

	// Execute the tool.
	result, err := tool.Execute("tc-1", map[string]any{"message": "hello"}, nil, nil)
	if err != nil {
		t.Fatalf("tool execution failed: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty content")
	}
	if result.Content[0].Text == nil {
		t.Fatal("expected text content")
	}
	if result.Content[0].Text.Text != "echo: hello" {
		t.Fatalf("expected 'echo: hello', got %q", result.Content[0].Text.Text)
	}
}

func TestProcessExtensionStateUpdate(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	ext, proc := startTestProcessExtension(t, "state_updater")

	// After initialization, the extension should have set state via notification.
	// Give a moment for the notification to be processed.
	time.Sleep(100 * time.Millisecond)

	if ext.State["initialized_at"] != "2025-01-01" {
		t.Fatalf("expected initialized_at state, got: %v", ext.State)
	}

	// Trigger a hook to get another state update.
	ext.Hooks = proc.BuildHooks()
	runner := NewHookRunner(func() []*Extension { return []*Extension{ext} })
	runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-1",
		ToolName:   "Read",
		Input:      map[string]any{},
	})

	time.Sleep(100 * time.Millisecond)

	if ext.State["last_tool_call"] != "seen" {
		t.Fatalf("expected last_tool_call state, got: %v", ext.State)
	}
}

func TestProcessExtensionContextModifier(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	ext, proc := startTestProcessExtension(t, "context_modifier")
	ext.Hooks = proc.BuildHooks()

	runner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	// Context hook should add a marker message.
	msgs := []AgentMessage{
		NewAgentMessageFromUser(&ai.UserMessage{
			Role:    "user",
			Content: ai.UserContent{Text: "hello"},
		}),
	}
	contextResult := runner.RunContext(&ContextHookEvent{Messages: msgs})
	if contextResult == nil {
		t.Fatal("expected context result")
	}
	// The extension adds a marker, so we should have at least 2 messages.
	if len(contextResult.Messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(contextResult.Messages))
	}
}

func TestProcessExtensionToolResultModifier(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	ext, proc := startTestProcessExtension(t, "context_modifier")
	ext.Hooks = proc.BuildHooks()

	runner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	// Tool result hook should append " [reviewed]".
	result := runner.RunToolResult(&ToolResultHookEvent{
		ToolCallID: "tc-1",
		ToolName:   "Read",
		Input:      map[string]any{},
		Content: []ai.ToolResultContentBlock{
			{Text: &ai.TextContent{Type: "text", Text: "file contents"}},
		},
		IsError: false,
	})
	if result == nil {
		t.Fatal("expected tool result modification")
	}
	if len(result.Content) == 0 || result.Content[0].Text == nil {
		t.Fatal("expected text content in result")
	}
	if !strings.Contains(result.Content[0].Text.Text, "[reviewed]") {
		t.Fatalf("expected '[reviewed]' in text, got: %s", result.Content[0].Text.Text)
	}
}

func TestProcessExtensionGracefulShutdown(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	_, proc := startTestProcessExtension(t, "basic")

	if !proc.IsRunning() {
		t.Fatal("expected running")
	}

	if err := proc.Stop(); err != nil {
		t.Fatalf("stop failed: %v", err)
	}

	if proc.IsRunning() {
		t.Fatal("expected not running after stop")
	}
}

func TestProcessExtensionManagerIntegration(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	dir := createTestExtScript(t, "blocker")

	em := NewExtensionManager("", nil)

	// Load manually from the dir.
	ext, err := loadExtensionManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	em.Register(ext)

	// Start the process extension.
	if err := em.StartProcessExtension(ext.Manifest.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { em.StopProcessExtensions() })

	// Verify hooks are wired.
	runner := em.NewHookRunner()
	result := runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-1",
		ToolName:   "Bash",
		Input:      map[string]any{"command": "ls"},
	})
	if result == nil || !result.Block {
		t.Fatal("expected Bash to be blocked via ExtensionManager")
	}
}

func TestProcessExtensionEntryPointNotFound(t *testing.T) {
	ext := &Extension{
		Manifest: ExtensionManifest{
			ID:         "bad-ext",
			Type:       ProcessExtensionType,
			EntryPoint: "/nonexistent/path/to/binary",
		},
		Dir:   t.TempDir(),
		State: make(map[string]any),
	}

	proc := NewProcessExtension(ext)
	err := proc.Start()
	if err == nil {
		proc.Stop()
		t.Fatal("expected error for nonexistent entry point")
	}
}

func TestProcessExtensionNoEntryPoint(t *testing.T) {
	ext := &Extension{
		Manifest: ExtensionManifest{
			ID:   "no-entry",
			Type: ProcessExtensionType,
		},
		Dir:   t.TempDir(),
		State: make(map[string]any),
	}

	proc := NewProcessExtension(ext)
	err := proc.Start()
	if err == nil {
		proc.Stop()
		t.Fatal("expected error for missing entry point")
	}
	if !strings.Contains(err.Error(), "no entryPoint") {
		t.Fatalf("expected 'no entryPoint' error, got: %v", err)
	}
}

func TestProcessExtensionMixedWithInProcess(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	// Create one in-process extension that allows bash,
	// and one process extension that blocks bash.
	inProcExt := &Extension{
		Manifest: ExtensionManifest{ID: "in-proc"},
		Hooks: &ExtensionHooks{
			ToolCall: []func(*ToolCallHookEvent) *ToolCallHookResult{
				func(e *ToolCallHookEvent) *ToolCallHookResult {
					return nil // allow everything
				},
			},
		},
	}

	procExt, proc := startTestProcessExtension(t, "blocker")
	procExt.Hooks = proc.BuildHooks()

	runner := NewHookRunner(func() []*Extension {
		return []*Extension{inProcExt, procExt}
	})

	// The process extension should block Bash even though the in-process one allows it.
	result := runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-1",
		ToolName:   "Bash",
		Input:      map[string]any{"command": "ls"},
	})
	if result == nil || !result.Block {
		t.Fatal("expected Bash to be blocked by process extension")
	}
}

// TestProcessExtensionProtocolTypes verifies wire type serialization.
func TestProcessExtensionProtocolTypes(t *testing.T) {
	// RPCRequest roundtrip
	id := int64(42)
	req := RPCRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "test",
		Params:  json.RawMessage(`{"key":"value"}`),
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RPCRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if *decoded.ID != 42 || decoded.Method != "test" {
		t.Fatalf("roundtrip failed: %+v", decoded)
	}

	// Notification (no ID)
	notif := RPCRequest{JSONRPC: "2.0", Method: "notify"}
	data, _ = json.Marshal(notif)
	if strings.Contains(string(data), `"id"`) {
		t.Fatal("notification should not have id field")
	}

	// Error response
	errResp := RPCResponse{
		JSONRPC: "2.0",
		ID:      &id,
		Error:   &RPCError{Code: RPCInternalError, Message: "boom"},
	}
	data, _ = json.Marshal(errResp)
	var decodedResp RPCResponse
	json.Unmarshal(data, &decodedResp)
	if decodedResp.Error == nil || decodedResp.Error.Code != RPCInternalError {
		t.Fatal("error roundtrip failed")
	}

	// InitializeResult with tools
	initResult := InitializeResult{
		ProtocolVersion: "1.0",
		Hooks:           []string{"tool_call", "context"},
		Tools: []ProcessToolDef{
			{Name: "my_tool", Label: "My Tool", Description: "Does stuff", Parameters: map[string]any{"type": "object"}},
		},
	}
	data, _ = json.Marshal(initResult)
	var decodedInit InitializeResult
	json.Unmarshal(data, &decodedInit)
	if len(decodedInit.Tools) != 1 || decodedInit.Tools[0].Name != "my_tool" {
		t.Fatal("InitializeResult roundtrip failed")
	}
}

// TestProcessExtensionHookMethodMapping verifies the hook method map is complete.
func TestProcessExtensionHookMethodMapping(t *testing.T) {
	expected := map[HookEventType]string{
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

	for hookType, expectedMethod := range expected {
		actual, ok := hookMethodForType[hookType]
		if !ok {
			t.Errorf("missing mapping for %s", hookType)
			continue
		}
		if actual != expectedMethod {
			t.Errorf("wrong method for %s: got %s, want %s", hookType, actual, expectedMethod)
		}
	}
}

// Verify that exec.Command is available.
func TestExecAvailable(t *testing.T) {
	cmd := exec.Command("echo", "test")
	out, err := cmd.Output()
	if err != nil {
		t.Skip("exec not available in this environment")
	}
	if !strings.Contains(string(out), "test") {
		t.Fatal("unexpected echo output")
	}
}
