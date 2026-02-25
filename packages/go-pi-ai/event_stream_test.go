package ai

import (
	"testing"
	"time"
)

func TestEventStreamBasic(t *testing.T) {
	stream := NewEventStream[string, string](
		func(event string) bool { return event == "done" },
		func(event string) string { return event },
	)

	go func() {
		stream.Push("hello")
		stream.Push("world")
		stream.Push("done")
		stream.End()
	}()

	var events []string
	for event := range stream.Events() {
		events = append(events, event)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0] != "hello" || events[1] != "world" || events[2] != "done" {
		t.Errorf("unexpected events: %v", events)
	}

	result := stream.Result()
	if result != "done" {
		t.Errorf("expected result 'done', got %q", result)
	}
}

func TestEventStreamResult(t *testing.T) {
	stream := NewEventStream[int, int](
		func(event int) bool { return event == 42 },
		func(event int) int { return event * 2 },
	)

	go func() {
		stream.Push(1)
		stream.Push(42)
		stream.End()
	}()

	result := stream.Result()
	if result != 84 {
		t.Errorf("expected result 84, got %d", result)
	}
}

func TestEventStreamEndWithResult(t *testing.T) {
	stream := NewEventStream[string, string](
		func(event string) bool { return false },
		func(event string) string { return "" },
	)

	go func() {
		stream.Push("a")
		stream.End("final")
	}()

	// Wait briefly for the goroutine
	time.Sleep(10 * time.Millisecond)

	result := stream.Result()
	if result != "final" {
		t.Errorf("expected result 'final', got %q", result)
	}
}

func TestAssistantMessageEventStream(t *testing.T) {
	stream := NewAssistantMessageEventStream()

	msg := NewAssistantMessage("test-api", "test-provider", "test-model", 1000)
	msg.Content = append(msg.Content, ContentBlock{
		Text: &TextContent{Type: "text", Text: "Hello"},
	})

	go func() {
		stream.Push(AssistantMessageEvent{Type: "start", Partial: msg})
		stream.Push(AssistantMessageEvent{Type: "done", Reason: StopReasonStop, FinalMessage: msg})
		stream.End()
	}()

	var events []AssistantMessageEvent
	for event := range stream.Events() {
		events = append(events, event)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	result := stream.Result()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Model != "test-model" {
		t.Errorf("expected model 'test-model', got %q", result.Model)
	}
}
