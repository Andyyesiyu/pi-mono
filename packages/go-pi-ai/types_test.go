package ai

import (
	"encoding/json"
	"testing"
)

func TestNewUserMessage(t *testing.T) {
	msg := NewUserMessage("Hello", 1234567890)
	if msg.User == nil {
		t.Fatal("expected User to be non-nil")
	}
	if msg.User.Role != "user" {
		t.Errorf("expected role 'user', got %q", msg.User.Role)
	}
	if msg.User.Content.Text != "Hello" {
		t.Errorf("expected content 'Hello', got %q", msg.User.Content.Text)
	}
	if msg.User.Timestamp != 1234567890 {
		t.Errorf("expected timestamp 1234567890, got %d", msg.User.Timestamp)
	}
	if msg.Role() != "user" {
		t.Errorf("expected Role() 'user', got %q", msg.Role())
	}
}

func TestNewAssistantMessage(t *testing.T) {
	msg := NewAssistantMessage("anthropic-messages", "anthropic", "claude-3", 1234567890)
	if msg.Role != "assistant" {
		t.Errorf("expected role 'assistant', got %q", msg.Role)
	}
	if msg.ApiType != "anthropic-messages" {
		t.Errorf("expected api 'anthropic-messages', got %q", msg.ApiType)
	}
	if msg.Provider != "anthropic" {
		t.Errorf("expected provider 'anthropic', got %q", msg.Provider)
	}
	if msg.Model != "claude-3" {
		t.Errorf("expected model 'claude-3', got %q", msg.Model)
	}
	if len(msg.Content) != 0 {
		t.Errorf("expected empty content, got %d blocks", len(msg.Content))
	}
}

func TestNewToolResultMessage(t *testing.T) {
	content := []ToolResultContentBlock{
		{Text: &TextContent{Type: "text", Text: "result"}},
	}
	msg := NewToolResultMessage("call-1", "read", content, false, 1234567890)
	if msg.ToolResult == nil {
		t.Fatal("expected ToolResult to be non-nil")
	}
	if msg.ToolResult.ToolCallID != "call-1" {
		t.Errorf("expected toolCallId 'call-1', got %q", msg.ToolResult.ToolCallID)
	}
	if msg.ToolResult.IsError {
		t.Error("expected IsError to be false")
	}
	if msg.Role() != "toolResult" {
		t.Errorf("expected Role() 'toolResult', got %q", msg.Role())
	}
}

func TestMessageJSON(t *testing.T) {
	msg := NewUserMessage("Hello world", 1000)

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}

	var decoded Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded.User == nil {
		t.Fatal("expected User to be non-nil after decode")
	}
	if decoded.User.Content.Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", decoded.User.Content.Text)
	}
}

func TestContentBlockJSON(t *testing.T) {
	blocks := []ContentBlock{
		{Text: &TextContent{Type: "text", Text: "hello"}},
		{Thinking: &ThinkingContent{Type: "thinking", Thinking: "hmm"}},
		{ToolCall: &ToolCall{Type: "toolCall", ID: "1", Name: "read", Arguments: map[string]any{"path": "/tmp"}}},
	}

	data, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}

	var decoded []ContentBlock
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if len(decoded) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(decoded))
	}
	if decoded[0].Text == nil || decoded[0].Text.Text != "hello" {
		t.Error("expected text block with 'hello'")
	}
	if decoded[1].Thinking == nil || decoded[1].Thinking.Thinking != "hmm" {
		t.Error("expected thinking block with 'hmm'")
	}
	if decoded[2].ToolCall == nil || decoded[2].ToolCall.Name != "read" {
		t.Error("expected tool call block with name 'read'")
	}
}

func TestAbortSignal(t *testing.T) {
	signal := NewAbortSignal()
	if signal.Aborted() {
		t.Error("signal should not be aborted initially")
	}

	signal.Abort()
	if !signal.Aborted() {
		t.Error("signal should be aborted after Abort()")
	}

	// Abort should be idempotent
	signal.Abort()
	if !signal.Aborted() {
		t.Error("signal should still be aborted")
	}

	// Done channel should be closed
	select {
	case <-signal.Done():
		// ok
	default:
		t.Error("Done channel should be closed")
	}
}

func TestNilAbortSignal(t *testing.T) {
	var signal *AbortSignal
	if signal.Aborted() {
		t.Error("nil signal should not be aborted")
	}
	if signal.Done() != nil {
		t.Error("nil signal Done() should return nil")
	}
}

func TestUserContentJSON(t *testing.T) {
	// Test string content
	uc := UserContent{Text: "hello"}
	data, err := json.Marshal(uc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `"hello"` {
		t.Errorf("expected string JSON, got %s", string(data))
	}

	var decoded UserContent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Text != "hello" {
		t.Errorf("expected 'hello', got %q", decoded.Text)
	}

	// Test block content
	uc2 := UserContent{
		Blocks: []UserContentBlock{
			{Text: &TextContent{Type: "text", Text: "hi"}},
		},
	}
	data2, err := json.Marshal(uc2)
	if err != nil {
		t.Fatal(err)
	}

	var decoded2 UserContent
	if err := json.Unmarshal(data2, &decoded2); err != nil {
		t.Fatal(err)
	}
	if len(decoded2.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(decoded2.Blocks))
	}
}
