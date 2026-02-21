package agent

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

// mockStreamFn creates a stream function that returns a predefined response.
func mockStreamFn(text string) StreamFn {
	return func(model *ai.Model, ctx *ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		go func() {
			msg := ai.NewAssistantMessage(model.ApiType, model.Provider, model.ID, time.Now().UnixMilli())
			msg.Content = append(msg.Content, ai.ContentBlock{
				Text: &ai.TextContent{Type: "text", Text: text},
			})
			msg.StopReason = ai.StopReasonStop
			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "text_start", ContentIndex: 0, Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: text, Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "text_end", ContentIndex: 0, Content: text, Partial: msg})
			stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: ai.StopReasonStop, FinalMessage: msg})
			stream.End()
		}()
		return stream
	}
}

// mockStreamFnWithToolCall creates a stream function that makes a tool call.
func mockStreamFnWithToolCall(toolName string, args map[string]any) StreamFn {
	callCount := 0
	return func(model *ai.Model, ctx *ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		go func() {
			msg := ai.NewAssistantMessage(model.ApiType, model.Provider, model.ID, time.Now().UnixMilli())
			callCount++

			if callCount == 1 {
				// First call: return tool call
				msg.Content = append(msg.Content, ai.ContentBlock{
					ToolCall: &ai.ToolCall{
						Type:      "toolCall",
						ID:        "call-1",
						Name:      toolName,
						Arguments: args,
					},
				})
				msg.StopReason = ai.StopReasonToolUse
			} else {
				// Second call: return text
				msg.Content = append(msg.Content, ai.ContentBlock{
					Text: &ai.TextContent{Type: "text", Text: "Done!"},
				})
				msg.StopReason = ai.StopReasonStop
			}

			stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
			if callCount == 1 {
				stream.Push(ai.AssistantMessageEvent{Type: "toolcall_start", ContentIndex: 0, Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "toolcall_end", ContentIndex: 0, ToolCallData: msg.Content[0].ToolCall, Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: ai.StopReasonToolUse, FinalMessage: msg})
			} else {
				stream.Push(ai.AssistantMessageEvent{Type: "text_start", ContentIndex: 0, Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "Done!", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "text_end", ContentIndex: 0, Content: "Done!", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: ai.StopReasonStop, FinalMessage: msg})
			}
			stream.End()
		}()
		return stream
	}
}

func testModel() *ai.Model {
	return &ai.Model{
		ID:        "test-model",
		Name:      "Test Model",
		ApiType:   "test-api",
		Provider:  "test-provider",
		BaseURL:   "https://test.api.com",
		MaxTokens: 4096,
	}
}

func TestAgentDefaultState(t *testing.T) {
	agent := NewAgent()
	state := agent.State()

	if state.SystemPrompt != "" {
		t.Errorf("expected empty system prompt, got %q", state.SystemPrompt)
	}
	if state.ThinkingLevel != ThinkingOff {
		t.Errorf("expected thinking off, got %q", state.ThinkingLevel)
	}
	if state.IsStreaming {
		t.Error("expected IsStreaming to be false")
	}
	if len(state.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(state.Messages))
	}
}

func TestAgentStateMutators(t *testing.T) {
	agent := NewAgent()

	agent.SetSystemPrompt("You are a helpful assistant")
	if agent.State().SystemPrompt != "You are a helpful assistant" {
		t.Error("SetSystemPrompt failed")
	}

	model := testModel()
	agent.SetModel(model)
	if agent.State().Model != model {
		t.Error("SetModel failed")
	}

	agent.SetThinkingLevel(ThinkingHigh)
	if agent.State().ThinkingLevel != ThinkingHigh {
		t.Error("SetThinkingLevel failed")
	}
}

func TestAgentPromptBasic(t *testing.T) {
	model := testModel()
	agent := NewAgent(AgentOptions{
		InitialState: &AgentState{Model: model},
		StreamFn:     mockStreamFn("Hello, world!"),
	})

	var events []AgentEvent
	var mu sync.Mutex
	agent.Subscribe(func(e AgentEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	err := agent.Prompt("Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	agent.WaitForIdle()

	mu.Lock()
	defer mu.Unlock()

	// Check we got expected event types
	eventTypes := make(map[string]bool)
	for _, e := range events {
		eventTypes[e.Type] = true
	}

	expectedTypes := []string{"agent_start", "turn_start", "message_start", "message_update", "message_end", "turn_end", "agent_end"}
	for _, et := range expectedTypes {
		if !eventTypes[et] {
			t.Errorf("missing event type: %s", et)
		}
	}

	// Check messages were added to state
	state := agent.State()
	if len(state.Messages) < 2 {
		t.Fatalf("expected at least 2 messages (user + assistant), got %d", len(state.Messages))
	}
}

func TestAgentToolExecution(t *testing.T) {
	model := testModel()

	calculateTool := &AgentTool{
		Tool: ai.Tool{
			Name:        "calculate",
			Description: "Evaluate a math expression",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"expression": map[string]any{"type": "string"},
				},
				"required": []any{"expression"},
			},
		},
		Label: "Calculator",
		Execute: func(toolCallID string, params map[string]any, signal *ai.AbortSignal, onUpdate AgentToolUpdateCallback) (*AgentToolResult, error) {
			expr, _ := params["expression"].(string)
			return &AgentToolResult{
				Content: []ai.ToolResultContentBlock{
					{Text: &ai.TextContent{Type: "text", Text: fmt.Sprintf("%s = 42", expr)}},
				},
			}, nil
		},
	}

	agent := NewAgent(AgentOptions{
		InitialState: &AgentState{
			Model: model,
			Tools: []*AgentTool{calculateTool},
		},
		StreamFn: mockStreamFnWithToolCall("calculate", map[string]any{"expression": "6*7"}),
	})

	var toolExecuted bool
	var mu sync.Mutex
	agent.Subscribe(func(e AgentEvent) {
		mu.Lock()
		if e.Type == "tool_execution_end" {
			toolExecuted = true
		}
		mu.Unlock()
	})

	err := agent.Prompt("What is 6*7?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	agent.WaitForIdle()

	mu.Lock()
	defer mu.Unlock()
	if !toolExecuted {
		t.Error("expected tool to be executed")
	}

	// Should have user, assistant (tool call), tool result, assistant (final)
	state := agent.State()
	if len(state.Messages) < 4 {
		t.Errorf("expected at least 4 messages, got %d", len(state.Messages))
	}
}

func TestAgentSubscribe(t *testing.T) {
	agent := NewAgent()

	var called bool
	unsub := agent.Subscribe(func(e AgentEvent) {
		called = true
	})

	agent.emit(AgentEvent{Type: "test"})
	if !called {
		t.Error("listener should have been called")
	}

	called = false
	unsub()
	agent.emit(AgentEvent{Type: "test"})
	if called {
		t.Error("listener should not be called after unsubscribe")
	}
}

func TestAgentSteeringQueue(t *testing.T) {
	agent := NewAgent()

	msg := NewAgentMessageFromMessage(ai.NewUserMessage("interrupt", time.Now().UnixMilli()))
	agent.Steer(msg)

	if !agent.HasQueuedMessages() {
		t.Error("expected queued messages")
	}

	agent.ClearSteeringQueue()
	if agent.HasQueuedMessages() {
		t.Error("expected no queued messages after clear")
	}
}

func TestAgentFollowUpQueue(t *testing.T) {
	agent := NewAgent()

	msg := NewAgentMessageFromMessage(ai.NewUserMessage("follow up", time.Now().UnixMilli()))
	agent.FollowUp(msg)

	if !agent.HasQueuedMessages() {
		t.Error("expected queued messages")
	}

	agent.ClearAllQueues()
	if agent.HasQueuedMessages() {
		t.Error("expected no queued messages after clear all")
	}
}

func TestAgentReset(t *testing.T) {
	agent := NewAgent()
	agent.AppendMessage(NewAgentMessageFromMessage(ai.NewUserMessage("test", time.Now().UnixMilli())))
	agent.Steer(NewAgentMessageFromMessage(ai.NewUserMessage("steer", time.Now().UnixMilli())))

	agent.Reset()

	state := agent.State()
	if len(state.Messages) != 0 {
		t.Errorf("expected 0 messages after reset, got %d", len(state.Messages))
	}
	if agent.HasQueuedMessages() {
		t.Error("expected no queued messages after reset")
	}
}

func TestAgentReplaceMessages(t *testing.T) {
	agent := NewAgent()
	msg1 := NewAgentMessageFromMessage(ai.NewUserMessage("a", 1000))
	msg2 := NewAgentMessageFromMessage(ai.NewUserMessage("b", 2000))
	agent.ReplaceMessages([]AgentMessage{msg1, msg2})

	state := agent.State()
	if len(state.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(state.Messages))
	}
}

func TestAgentConcurrentPromptRejection(t *testing.T) {
	model := testModel()

	// Use a slow stream function
	agent := NewAgent(AgentOptions{
		InitialState: &AgentState{Model: model},
		StreamFn: func(m *ai.Model, ctx *ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			stream := ai.NewAssistantMessageEventStream()
			go func() {
				time.Sleep(100 * time.Millisecond)
				msg := ai.NewAssistantMessage(m.ApiType, m.Provider, m.ID, time.Now().UnixMilli())
				msg.Content = append(msg.Content, ai.ContentBlock{
					Text: &ai.TextContent{Type: "text", Text: "ok"},
				})
				msg.StopReason = ai.StopReasonStop
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: ai.StopReasonStop, FinalMessage: msg})
				stream.End()
			}()
			return stream
		},
	})

	err := agent.Prompt("First")
	if err != nil {
		t.Fatalf("first prompt should succeed: %v", err)
	}

	// Second prompt should fail immediately
	time.Sleep(10 * time.Millisecond)
	err = agent.Prompt("Second")
	if err == nil {
		t.Error("expected error for concurrent prompt")
	}

	agent.WaitForIdle()
}

func TestDefaultConvertToLLM(t *testing.T) {
	messages := []AgentMessage{
		NewAgentMessageFromMessage(ai.NewUserMessage("hello", 1000)),
		NewCustomAgentMessage("notification", map[string]any{"text": "info"}),
		NewAgentMessageFromMessage(ai.NewUserMessage("world", 2000)),
	}

	result, err := DefaultConvertToLLM(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Custom messages should be filtered out
	if len(result) != 2 {
		t.Errorf("expected 2 LLM messages (custom filtered), got %d", len(result))
	}
}

func TestGetTextContent(t *testing.T) {
	msg := ai.NewAssistantMessage("api", "provider", "model", 1000)
	msg.Content = append(msg.Content, ai.ContentBlock{
		Text: &ai.TextContent{Type: "text", Text: "Hello"},
	})
	msg.Content = append(msg.Content, ai.ContentBlock{
		Text: &ai.TextContent{Type: "text", Text: " World"},
	})

	messages := []AgentMessage{
		NewAgentMessageFromAssistant(msg),
	}

	text := GetTextContent(messages)
	if text != "Hello\n World" {
		t.Errorf("expected 'Hello\\n World', got %q", text)
	}
}
