package agent

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

func TestHookRunnerToolCallBlock(t *testing.T) {
	ext := &Extension{
		Manifest: ExtensionManifest{ID: "blocker"},
		Hooks: &ExtensionHooks{
			ToolCall: []func(*ToolCallHookEvent) *ToolCallHookResult{
				func(e *ToolCallHookEvent) *ToolCallHookResult {
					if e.ToolName == "Bash" {
						return &ToolCallHookResult{Block: true, Reason: "bash blocked"}
					}
					return nil
				},
			},
		},
	}

	runner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	// Should block bash
	result := runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-1",
		ToolName:   "Bash",
		Input:      map[string]any{"command": "rm -rf /"},
	})
	if result == nil || !result.Block {
		t.Fatal("expected tool call to be blocked")
	}
	if result.Reason != "bash blocked" {
		t.Fatalf("expected reason 'bash blocked', got %q", result.Reason)
	}

	// Should not block read
	result = runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-2",
		ToolName:   "Read",
		Input:      map[string]any{"file_path": "/etc/passwd"},
	})
	if result != nil && result.Block {
		t.Fatal("expected read to not be blocked")
	}
}

func TestHookRunnerToolResultModify(t *testing.T) {
	isErrorTrue := true
	ext := &Extension{
		Manifest: ExtensionManifest{ID: "modifier"},
		Hooks: &ExtensionHooks{
			ToolResult: []func(*ToolResultHookEvent) *ToolResultHookResult{
				func(e *ToolResultHookEvent) *ToolResultHookResult {
					// Sanitize all bash output
					if e.ToolName == "Bash" {
						return &ToolResultHookResult{
							Content: []ai.ToolResultContentBlock{
								{Text: &ai.TextContent{Type: "text", Text: "[redacted]"}},
							},
							IsError: &isErrorTrue,
						}
					}
					return nil
				},
			},
		},
	}

	runner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	result := runner.RunToolResult(&ToolResultHookEvent{
		ToolCallID: "call-1",
		ToolName:   "Bash",
		Content: []ai.ToolResultContentBlock{
			{Text: &ai.TextContent{Type: "text", Text: "secret data"}},
		},
		IsError: false,
	})

	if result == nil {
		t.Fatal("expected modified result")
	}
	if len(result.Content) != 1 || result.Content[0].Text.Text != "[redacted]" {
		t.Fatal("expected content to be redacted")
	}
	if result.IsError == nil || !*result.IsError {
		t.Fatal("expected isError to be true")
	}
}

func TestHookRunnerToolResultChaining(t *testing.T) {
	ext1 := &Extension{
		Manifest: ExtensionManifest{ID: "ext1"},
		Hooks: &ExtensionHooks{
			ToolResult: []func(*ToolResultHookEvent) *ToolResultHookResult{
				func(e *ToolResultHookEvent) *ToolResultHookResult {
					// Append suffix
					if len(e.Content) > 0 && e.Content[0].Text != nil {
						return &ToolResultHookResult{
							Content: []ai.ToolResultContentBlock{
								{Text: &ai.TextContent{Type: "text", Text: e.Content[0].Text.Text + " [ext1]"}},
							},
						}
					}
					return nil
				},
			},
		},
	}
	ext2 := &Extension{
		Manifest: ExtensionManifest{ID: "ext2"},
		Hooks: &ExtensionHooks{
			ToolResult: []func(*ToolResultHookEvent) *ToolResultHookResult{
				func(e *ToolResultHookEvent) *ToolResultHookResult {
					// Append suffix
					if len(e.Content) > 0 && e.Content[0].Text != nil {
						return &ToolResultHookResult{
							Content: []ai.ToolResultContentBlock{
								{Text: &ai.TextContent{Type: "text", Text: e.Content[0].Text.Text + " [ext2]"}},
							},
						}
					}
					return nil
				},
			},
		},
	}

	runner := NewHookRunner(func() []*Extension { return []*Extension{ext1, ext2} })

	result := runner.RunToolResult(&ToolResultHookEvent{
		ToolCallID: "call-1",
		ToolName:   "Read",
		Content: []ai.ToolResultContentBlock{
			{Text: &ai.TextContent{Type: "text", Text: "original"}},
		},
	})

	if result == nil {
		t.Fatal("expected modified result")
	}
	expected := "original [ext1] [ext2]"
	if result.Content[0].Text.Text != expected {
		t.Fatalf("expected %q, got %q", expected, result.Content[0].Text.Text)
	}
}

func TestHookRunnerContext(t *testing.T) {
	ext := &Extension{
		Manifest: ExtensionManifest{ID: "injector"},
		Hooks: &ExtensionHooks{
			Context: []func(*ContextHookEvent) *ContextHookResult{
				func(e *ContextHookEvent) *ContextHookResult {
					// Inject a system message
					injected := NewCustomAgentMessage("system-reminder", map[string]any{
						"text": "Remember to be helpful",
					})
					return &ContextHookResult{
						Messages: append(e.Messages, injected),
					}
				},
			},
		},
	}

	runner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	event := &ContextHookEvent{
		Messages: []AgentMessage{
			NewAgentMessageFromMessage(ai.NewUserMessage("hello", time.Now().UnixMilli())),
		},
	}

	result := runner.RunContext(event)
	if result == nil {
		t.Fatal("expected context result")
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result.Messages))
	}
	if result.Messages[1].Role() != "system-reminder" {
		t.Fatalf("expected injected message role, got %q", result.Messages[1].Role())
	}
}

func TestHookRunnerBeforeAgentStart(t *testing.T) {
	ext := &Extension{
		Manifest: ExtensionManifest{ID: "prompt-modifier"},
		Hooks: &ExtensionHooks{
			BeforeAgentStart: []func(*BeforeAgentStartHookEvent) *BeforeAgentStartHookResult{
				func(e *BeforeAgentStartHookEvent) *BeforeAgentStartHookResult {
					return &BeforeAgentStartHookResult{
						SystemPrompt: e.SystemPrompt + "\nAlways be concise.",
					}
				},
			},
		},
	}

	runner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	event := &BeforeAgentStartHookEvent{SystemPrompt: "You are helpful."}
	result := runner.RunBeforeAgentStart(event)

	if result == nil {
		t.Fatal("expected result")
	}
	if result.SystemPrompt != "You are helpful.\nAlways be concise." {
		t.Fatalf("unexpected system prompt: %q", result.SystemPrompt)
	}
}

func TestHookRunnerLifecycleEvents(t *testing.T) {
	var log []string
	var mu sync.Mutex

	ext := &Extension{
		Manifest: ExtensionManifest{ID: "lifecycle"},
		Hooks: &ExtensionHooks{
			AgentStart: []func(){
				func() {
					mu.Lock()
					log = append(log, "agent_start")
					mu.Unlock()
				},
			},
			AgentEnd: []func(*AgentEndHookEvent){
				func(e *AgentEndHookEvent) {
					mu.Lock()
					log = append(log, fmt.Sprintf("agent_end:%d", len(e.Messages)))
					mu.Unlock()
				},
			},
			TurnStart: []func(){
				func() {
					mu.Lock()
					log = append(log, "turn_start")
					mu.Unlock()
				},
			},
			TurnEnd: []func(*TurnEndHookEvent){
				func(e *TurnEndHookEvent) {
					mu.Lock()
					log = append(log, "turn_end")
					mu.Unlock()
				},
			},
			SessionStart: []func(){
				func() {
					mu.Lock()
					log = append(log, "session_start")
					mu.Unlock()
				},
			},
			SessionShutdown: []func(){
				func() {
					mu.Lock()
					log = append(log, "session_shutdown")
					mu.Unlock()
				},
			},
		},
	}

	runner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	runner.RunSessionStart()
	runner.RunAgentStart()
	runner.RunTurnStart()
	runner.RunTurnEnd(&TurnEndHookEvent{})
	runner.RunAgentEnd(&AgentEndHookEvent{Messages: []AgentMessage{{}, {}}})
	runner.RunSessionShutdown()

	mu.Lock()
	defer mu.Unlock()

	expected := []string{"session_start", "agent_start", "turn_start", "turn_end", "agent_end:2", "session_shutdown"}
	if len(log) != len(expected) {
		t.Fatalf("expected %d events, got %d: %v", len(expected), len(log), log)
	}
	for i, e := range expected {
		if log[i] != e {
			t.Fatalf("event %d: expected %q, got %q", i, e, log[i])
		}
	}
}

func TestHookRunnerNilSafe(t *testing.T) {
	var runner *HookRunner
	// All methods should be nil-safe
	runner.RunToolCall(&ToolCallHookEvent{})
	runner.RunToolResult(&ToolResultHookEvent{})
	runner.RunContext(&ContextHookEvent{})
	runner.RunBeforeAgentStart(&BeforeAgentStartHookEvent{})
	runner.RunAgentStart()
	runner.RunAgentEnd(&AgentEndHookEvent{})
	runner.RunTurnStart()
	runner.RunTurnEnd(&TurnEndHookEvent{})
	runner.RunSessionStart()
	runner.RunSessionShutdown()
}

func TestHookRunnerNoHooksExtension(t *testing.T) {
	ext := &Extension{
		Manifest: ExtensionManifest{ID: "no-hooks"},
		// Hooks is nil
	}

	runner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	// Should not panic
	result := runner.RunToolCall(&ToolCallHookEvent{ToolName: "Bash"})
	if result != nil {
		t.Fatal("expected nil result for extension with no hooks")
	}
}

func TestToolCallBlockInAgentLoop(t *testing.T) {
	model := testModel()

	// Extension that blocks all bash calls
	ext := &Extension{
		Manifest: ExtensionManifest{ID: "bash-blocker"},
		Hooks: &ExtensionHooks{
			ToolCall: []func(*ToolCallHookEvent) *ToolCallHookResult{
				func(e *ToolCallHookEvent) *ToolCallHookResult {
					if e.ToolName == "echo" {
						return &ToolCallHookResult{Block: true, Reason: "blocked"}
					}
					return nil
				},
			},
		},
	}

	hookRunner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	echoTool := &AgentTool{
		Tool: ai.Tool{
			Name:        "echo",
			Description: "Echo text",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{"type": "string"},
				},
			},
		},
		Label: "Echo",
		Execute: func(toolCallID string, params map[string]any, signal *ai.AbortSignal, onUpdate AgentToolUpdateCallback) (*AgentToolResult, error) {
			t.Error("execute should not be called when blocked")
			return toolText("should not reach"), nil
		},
	}

	ctx := &AgentContext{
		SystemPrompt: "test",
		Tools:        []*AgentTool{echoTool},
	}

	config := &AgentLoopConfig{
		Model:        model,
		ConvertToLLM: DefaultConvertToLLM,
		Hooks:        hookRunner,
	}

	prompt := NewAgentMessageFromMessage(ai.NewUserMessage("echo hello", time.Now().UnixMilli()))

	stream := AgentLoop(
		[]AgentMessage{prompt},
		ctx,
		config,
		nil,
		mockStreamFnWithToolCall("echo", map[string]any{"text": "hello"}),
	)

	var toolBlocked bool
	for event := range stream.Events() {
		if event.Type == "tool_execution_end" && event.IsError {
			toolBlocked = true
		}
	}

	if !toolBlocked {
		t.Error("expected tool call to be blocked with error")
	}
}

func TestToolResultModifyInAgentLoop(t *testing.T) {
	model := testModel()

	ext := &Extension{
		Manifest: ExtensionManifest{ID: "sanitizer"},
		Hooks: &ExtensionHooks{
			ToolResult: []func(*ToolResultHookEvent) *ToolResultHookResult{
				func(e *ToolResultHookEvent) *ToolResultHookResult {
					return &ToolResultHookResult{
						Content: []ai.ToolResultContentBlock{
							{Text: &ai.TextContent{Type: "text", Text: "sanitized output"}},
						},
					}
				},
			},
		},
	}

	hookRunner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	echoTool := &AgentTool{
		Tool: ai.Tool{
			Name:        "echo",
			Description: "Echo text",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{"type": "string"},
				},
			},
		},
		Label: "Echo",
		Execute: func(toolCallID string, params map[string]any, signal *ai.AbortSignal, onUpdate AgentToolUpdateCallback) (*AgentToolResult, error) {
			return toolText("original output"), nil
		},
	}

	ctx := &AgentContext{
		SystemPrompt: "test",
		Tools:        []*AgentTool{echoTool},
	}

	config := &AgentLoopConfig{
		Model:        model,
		ConvertToLLM: DefaultConvertToLLM,
		Hooks:        hookRunner,
	}

	prompt := NewAgentMessageFromMessage(ai.NewUserMessage("echo hello", time.Now().UnixMilli()))

	stream := AgentLoop(
		[]AgentMessage{prompt},
		ctx,
		config,
		nil,
		mockStreamFnWithToolCall("echo", map[string]any{"text": "hello"}),
	)

	// Collect all messages to check the tool result was sanitized
	var messages []AgentMessage
	for event := range stream.Events() {
		if event.Type == "agent_end" {
			messages = event.Messages
		}
	}

	// Find the tool result message
	for _, m := range messages {
		if m.IsToolResult() {
			content := m.Message.ToolResult.Content
			if len(content) > 0 && content[0].Text != nil {
				if content[0].Text.Text != "sanitized output" {
					t.Fatalf("expected 'sanitized output', got %q", content[0].Text.Text)
				}
				return
			}
		}
	}
	t.Fatal("did not find tool result message")
}

func TestContextHookInAgentLoop(t *testing.T) {
	model := testModel()

	var capturedMessageCount int
	ext := &Extension{
		Manifest: ExtensionManifest{ID: "context-hook"},
		Hooks: &ExtensionHooks{
			Context: []func(*ContextHookEvent) *ContextHookResult{
				func(e *ContextHookEvent) *ContextHookResult {
					capturedMessageCount = len(e.Messages)
					// Inject a system reminder
					injected := NewCustomAgentMessage("system-reminder", map[string]any{
						"text": "Be helpful",
					})
					return &ContextHookResult{
						Messages: append(e.Messages, injected),
					}
				},
			},
		},
	}

	hookRunner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	ctx := &AgentContext{
		SystemPrompt: "test",
	}

	config := &AgentLoopConfig{
		Model:        model,
		ConvertToLLM: DefaultConvertToLLM,
		Hooks:        hookRunner,
	}

	prompt := NewAgentMessageFromMessage(ai.NewUserMessage("hello", time.Now().UnixMilli()))

	stream := AgentLoop(
		[]AgentMessage{prompt},
		ctx,
		config,
		nil,
		mockStreamFn("response"),
	)

	for range stream.Events() {
	}

	if capturedMessageCount == 0 {
		t.Fatal("context hook was not called")
	}
}

func TestExtensionManagerNewHookRunner(t *testing.T) {
	em := NewExtensionManager("", nil)

	called := false
	ext := &Extension{
		Manifest: ExtensionManifest{ID: "hook-ext"},
		Hooks: &ExtensionHooks{
			SessionStart: []func(){
				func() { called = true },
			},
		},
	}
	em.Register(ext)

	runner := em.NewHookRunner()
	runner.RunSessionStart()

	if !called {
		t.Fatal("hook should have been called via extension manager")
	}
}
