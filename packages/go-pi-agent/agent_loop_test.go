package agent

import (
	"testing"
	"time"

	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

func TestAgentLoopBasic(t *testing.T) {
	model := testModel()
	ctx := &AgentContext{
		SystemPrompt: "You are helpful",
		Messages:     nil,
		Tools:        nil,
	}

	config := &AgentLoopConfig{
		Model:        model,
		ConvertToLLM: DefaultConvertToLLM,
	}

	prompt := NewAgentMessageFromMessage(ai.NewUserMessage("Hello", time.Now().UnixMilli()))

	stream := AgentLoop(
		[]AgentMessage{prompt},
		ctx,
		config,
		nil,
		mockStreamFn("Hi there!"),
	)

	var events []AgentEvent
	for event := range stream.Events() {
		events = append(events, event)
	}

	// Should have: agent_start, turn_start, message_start (user), message_end (user),
	// message_start (assistant), message_update(s), message_end (assistant),
	// turn_end, agent_end
	hasAgentStart := false
	hasAgentEnd := false
	hasTurnStart := false
	hasTurnEnd := false

	for _, e := range events {
		switch e.Type {
		case "agent_start":
			hasAgentStart = true
		case "agent_end":
			hasAgentEnd = true
			if len(e.Messages) < 2 {
				t.Errorf("agent_end should have at least 2 messages, got %d", len(e.Messages))
			}
		case "turn_start":
			hasTurnStart = true
		case "turn_end":
			hasTurnEnd = true
		}
	}

	if !hasAgentStart {
		t.Error("missing agent_start event")
	}
	if !hasAgentEnd {
		t.Error("missing agent_end event")
	}
	if !hasTurnStart {
		t.Error("missing turn_start event")
	}
	if !hasTurnEnd {
		t.Error("missing turn_end event")
	}

	result := stream.Result()
	if len(result) < 2 {
		t.Errorf("expected at least 2 messages in result, got %d", len(result))
	}
}

func TestAgentLoopContinueError(t *testing.T) {
	ctx := &AgentContext{
		Messages: nil,
	}
	config := &AgentLoopConfig{
		Model:        testModel(),
		ConvertToLLM: DefaultConvertToLLM,
	}

	// Empty context should error
	_, err := AgentLoopContinue(ctx, config, nil, nil)
	if err == nil {
		t.Error("expected error for empty context")
	}

	// Context ending with assistant should error
	msg := ai.NewAssistantMessage("api", "provider", "model", time.Now().UnixMilli())
	msg.Content = append(msg.Content, ai.ContentBlock{
		Text: &ai.TextContent{Type: "text", Text: "hello"},
	})
	ctx.Messages = []AgentMessage{NewAgentMessageFromAssistant(msg)}
	_, err = AgentLoopContinue(ctx, config, nil, nil)
	if err == nil {
		t.Error("expected error for context ending with assistant")
	}
}

func TestAgentLoopWithToolExecution(t *testing.T) {
	model := testModel()

	echoTool := &AgentTool{
		Tool: ai.Tool{
			Name:        "echo",
			Description: "Echo back the input",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{"type": "string"},
				},
			},
		},
		Label: "Echo",
		Execute: func(toolCallID string, params map[string]any, signal *ai.AbortSignal, onUpdate AgentToolUpdateCallback) (*AgentToolResult, error) {
			text, _ := params["text"].(string)
			return &AgentToolResult{
				Content: []ai.ToolResultContentBlock{
					{Text: &ai.TextContent{Type: "text", Text: "Echo: " + text}},
				},
			}, nil
		},
	}

	ctx := &AgentContext{
		SystemPrompt: "You are helpful",
		Messages:     nil,
		Tools:        []*AgentTool{echoTool},
	}

	config := &AgentLoopConfig{
		Model:        model,
		ConvertToLLM: DefaultConvertToLLM,
	}

	prompt := NewAgentMessageFromMessage(ai.NewUserMessage("Echo hello", time.Now().UnixMilli()))

	stream := AgentLoop(
		[]AgentMessage{prompt},
		ctx,
		config,
		nil,
		mockStreamFnWithToolCall("echo", map[string]any{"text": "hello"}),
	)

	var toolExecutionStarts int
	var toolExecutionEnds int
	for event := range stream.Events() {
		switch event.Type {
		case "tool_execution_start":
			toolExecutionStarts++
		case "tool_execution_end":
			toolExecutionEnds++
		}
	}

	if toolExecutionStarts != 1 {
		t.Errorf("expected 1 tool_execution_start, got %d", toolExecutionStarts)
	}
	if toolExecutionEnds != 1 {
		t.Errorf("expected 1 tool_execution_end, got %d", toolExecutionEnds)
	}
}

func TestAgentLoopWithSteeringMessages(t *testing.T) {
	model := testModel()

	// Steering messages are polled at the start and after tool executions.
	// When there are NO tool calls, steering from the initial poll is injected
	// before the first LLM call, which means the LLM sees: user prompt + steering.
	// After the LLM responds (no tool calls), steering is polled again.
	// We want to verify that the steering message was injected into the context.
	steeringCallCount := 0

	ctx := &AgentContext{
		SystemPrompt: "You are helpful",
		Messages:     nil,
		Tools:        nil,
	}

	config := &AgentLoopConfig{
		Model:        model,
		ConvertToLLM: DefaultConvertToLLM,
		GetSteeringMessages: func() ([]AgentMessage, error) {
			steeringCallCount++
			if steeringCallCount == 1 {
				return []AgentMessage{
					NewAgentMessageFromMessage(ai.NewUserMessage("steering message", time.Now().UnixMilli())),
				}, nil
			}
			return nil, nil
		},
	}

	prompt := NewAgentMessageFromMessage(ai.NewUserMessage("Hello", time.Now().UnixMilli()))

	stream := AgentLoop(
		[]AgentMessage{prompt},
		ctx,
		config,
		nil,
		mockStreamFn("response"),
	)

	// Collect all events
	var messageStarts int
	for event := range stream.Events() {
		if event.Type == "message_start" {
			messageStarts++
		}
	}

	// Should have message_start events for: user prompt, steering message, and assistant response
	// The steering message is injected into the stream as message_start/message_end events
	if messageStarts < 3 {
		t.Errorf("expected at least 3 message_start events (user + steering + assistant), got %d", messageStarts)
	}

	// Steering should have been polled at least once
	if steeringCallCount < 1 {
		t.Errorf("expected GetSteeringMessages to be called at least once, got %d", steeringCallCount)
	}
}

func TestAgentLoopWithFollowUp(t *testing.T) {
	model := testModel()
	followUpCalled := false

	ctx := &AgentContext{
		SystemPrompt: "You are helpful",
		Messages:     nil,
		Tools:        nil,
	}

	config := &AgentLoopConfig{
		Model:        model,
		ConvertToLLM: DefaultConvertToLLM,
		GetFollowUpMessages: func() ([]AgentMessage, error) {
			if !followUpCalled {
				followUpCalled = true
				return []AgentMessage{
					NewAgentMessageFromMessage(ai.NewUserMessage("follow-up question", time.Now().UnixMilli())),
				}, nil
			}
			return nil, nil
		},
	}

	prompt := NewAgentMessageFromMessage(ai.NewUserMessage("Hello", time.Now().UnixMilli()))

	callCount := 0
	sfn := func(m *ai.Model, ctx *ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		callCount++
		return mockStreamFn("response")(m, ctx, opts)
	}

	stream := AgentLoop(
		[]AgentMessage{prompt},
		ctx,
		config,
		nil,
		sfn,
	)

	for range stream.Events() {
		// consume events
	}

	// Should have been called twice (initial + follow-up)
	if callCount != 2 {
		t.Errorf("expected 2 LLM calls (initial + follow-up), got %d", callCount)
	}
}

func TestAgentLoopCustomConvertToLLM(t *testing.T) {
	model := testModel()

	// Custom converter that filters out "notification" messages
	customConvert := func(messages []AgentMessage) ([]ai.Message, error) {
		var result []ai.Message
		for _, m := range messages {
			if m.RoleStr == "notification" {
				continue
			}
			if m.Message != nil {
				result = append(result, *m.Message)
			}
		}
		return result, nil
	}

	ctx := &AgentContext{
		SystemPrompt: "You are helpful",
		Messages: []AgentMessage{
			NewCustomAgentMessage("notification", map[string]any{"text": "system event"}),
		},
		Tools: nil,
	}

	config := &AgentLoopConfig{
		Model:        model,
		ConvertToLLM: customConvert,
	}

	var capturedMessages []ai.Message
	sfn := func(m *ai.Model, ctx *ai.Context, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		capturedMessages = ctx.Messages
		return mockStreamFn("ok")(m, ctx, opts)
	}

	prompt := NewAgentMessageFromMessage(ai.NewUserMessage("Hello", time.Now().UnixMilli()))
	stream := AgentLoop(
		[]AgentMessage{prompt},
		ctx,
		config,
		nil,
		sfn,
	)

	for range stream.Events() {
	}

	// The notification message should have been filtered out
	for _, m := range capturedMessages {
		if m.Role() == "notification" {
			t.Error("notification message should have been filtered by custom converter")
		}
	}
}
