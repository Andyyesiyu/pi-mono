package ai

import "sync"

// EventStream is a generic async event stream.
// It supports push-based production and pull-based consumption via channels.
type EventStream[T any, R any] struct {
	events chan T
	done   bool

	resultOnce sync.Once
	resultCh   chan R
	result     R

	isComplete    func(event T) bool
	extractResult func(event T) R

	mu sync.Mutex
}

// NewEventStream creates a new EventStream.
// isComplete determines if an event signals stream completion.
// extractResult extracts the final result from the completion event.
func NewEventStream[T any, R any](
	isComplete func(event T) bool,
	extractResult func(event T) R,
) *EventStream[T, R] {
	return &EventStream[T, R]{
		events:        make(chan T, 100),
		resultCh:      make(chan R, 1),
		isComplete:    isComplete,
		extractResult: extractResult,
	}
}

// Push sends an event to the stream.
func (s *EventStream[T, R]) Push(event T) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}

	if s.isComplete(event) {
		s.done = true
		s.result = s.extractResult(event)
		s.resultOnce.Do(func() {
			s.resultCh <- s.result
		})
	}
	s.mu.Unlock()

	s.events <- event
}

// End signals the stream is complete.
func (s *EventStream[T, R]) End(result ...R) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		close(s.events)
		return
	}
	s.done = true
	if len(result) > 0 {
		s.result = result[0]
		s.resultOnce.Do(func() {
			s.resultCh <- s.result
		})
	}
	s.mu.Unlock()

	close(s.events)
}

// Events returns a channel for consuming events.
func (s *EventStream[T, R]) Events() <-chan T {
	return s.events
}

// Result blocks until the stream completes and returns the final result.
func (s *EventStream[T, R]) Result() R {
	return <-s.resultCh
}

// ResultChan returns a channel that receives the final result.
func (s *EventStream[T, R]) ResultChan() <-chan R {
	return s.resultCh
}

// AssistantMessageEventStream is an EventStream specialized for assistant messages.
type AssistantMessageEventStream = EventStream[AssistantMessageEvent, *AssistantMessage]

// NewAssistantMessageEventStream creates a new AssistantMessageEventStream.
func NewAssistantMessageEventStream() *AssistantMessageEventStream {
	return NewEventStream[AssistantMessageEvent, *AssistantMessage](
		func(event AssistantMessageEvent) bool {
			return event.Type == "done" || event.Type == "error"
		},
		func(event AssistantMessageEvent) *AssistantMessage {
			if event.Type == "done" {
				return event.FinalMessage
			}
			if event.Type == "error" {
				return event.ErrorMessage
			}
			return nil
		},
	)
}
