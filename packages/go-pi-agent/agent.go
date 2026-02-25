package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

// DefaultConvertToLLM keeps only LLM-compatible messages.
func DefaultConvertToLLM(messages []AgentMessage) ([]ai.Message, error) {
	var result []ai.Message
	for _, m := range messages {
		if m.Message != nil {
			role := m.Message.Role()
			if role == "user" || role == "assistant" || role == "toolResult" {
				result = append(result, *m.Message)
			}
		}
	}
	return result, nil
}

// AgentOptions configures a new Agent.
type AgentOptions struct {
	InitialState     *AgentState
	ConvertToLLM     func(messages []AgentMessage) ([]ai.Message, error)
	TransformContext func(messages []AgentMessage, signal *ai.AbortSignal) ([]AgentMessage, error)
	SteeringMode     string // "all" or "one-at-a-time"
	FollowUpMode     string // "all" or "one-at-a-time"
	StreamFn         StreamFn
	SessionID        string
	GetApiKey        func(provider string) (string, error)
	ThinkingBudgets  *ai.ThinkingBudgets
	MaxRetryDelayMs  *int
}

// Agent manages the agent lifecycle, state, and event emission.
type Agent struct {
	mu sync.RWMutex

	state AgentState

	listeners  map[int]func(AgentEvent)
	listenerID int

	abortSignal     *ai.AbortSignal
	convertToLLM    func(messages []AgentMessage) ([]ai.Message, error)
	transformContext func(messages []AgentMessage, signal *ai.AbortSignal) ([]AgentMessage, error)

	steeringQueue  []AgentMessage
	followUpQueue  []AgentMessage
	steeringMode   string
	followUpMode   string
	streamFn       StreamFn
	sessionID      string
	getApiKey      func(provider string) (string, error)
	thinkingBudgets *ai.ThinkingBudgets
	maxRetryDelayMs *int

	runningDone chan struct{}
}

// NewAgent creates a new Agent with the given options.
func NewAgent(opts ...AgentOptions) *Agent {
	a := &Agent{
		state: AgentState{
			SystemPrompt:    "",
			ThinkingLevel:   ThinkingOff,
			Tools:           nil,
			Messages:        nil,
			PendingToolCalls: make(map[string]bool),
		},
		listeners:    make(map[int]func(AgentEvent)),
		convertToLLM: DefaultConvertToLLM,
		steeringMode: "one-at-a-time",
		followUpMode: "one-at-a-time",
	}

	if len(opts) > 0 {
		opt := opts[0]
		if opt.InitialState != nil {
			if opt.InitialState.SystemPrompt != "" {
				a.state.SystemPrompt = opt.InitialState.SystemPrompt
			}
			if opt.InitialState.Model != nil {
				a.state.Model = opt.InitialState.Model
			}
			if opt.InitialState.ThinkingLevel != "" {
				a.state.ThinkingLevel = opt.InitialState.ThinkingLevel
			}
			if opt.InitialState.Tools != nil {
				a.state.Tools = opt.InitialState.Tools
			}
			if opt.InitialState.Messages != nil {
				a.state.Messages = opt.InitialState.Messages
			}
		}
		if opt.ConvertToLLM != nil {
			a.convertToLLM = opt.ConvertToLLM
		}
		if opt.TransformContext != nil {
			a.transformContext = opt.TransformContext
		}
		if opt.SteeringMode != "" {
			a.steeringMode = opt.SteeringMode
		}
		if opt.FollowUpMode != "" {
			a.followUpMode = opt.FollowUpMode
		}
		if opt.StreamFn != nil {
			a.streamFn = opt.StreamFn
		}
		if opt.SessionID != "" {
			a.sessionID = opt.SessionID
		}
		if opt.GetApiKey != nil {
			a.getApiKey = opt.GetApiKey
		}
		if opt.ThinkingBudgets != nil {
			a.thinkingBudgets = opt.ThinkingBudgets
		}
		if opt.MaxRetryDelayMs != nil {
			a.maxRetryDelayMs = opt.MaxRetryDelayMs
		}
	}

	return a
}

// State returns the current agent state (read-only snapshot).
func (a *Agent) State() AgentState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state
}

// Subscribe registers a listener for agent events. Returns an unsubscribe function.
func (a *Agent) Subscribe(fn func(AgentEvent)) func() {
	a.mu.Lock()
	id := a.listenerID
	a.listenerID++
	a.listeners[id] = fn
	a.mu.Unlock()

	return func() {
		a.mu.Lock()
		delete(a.listeners, id)
		a.mu.Unlock()
	}
}

// SetSystemPrompt updates the system prompt.
func (a *Agent) SetSystemPrompt(v string) {
	a.mu.Lock()
	a.state.SystemPrompt = v
	a.mu.Unlock()
}

// SetModel updates the model.
func (a *Agent) SetModel(m *ai.Model) {
	a.mu.Lock()
	a.state.Model = m
	a.mu.Unlock()
}

// SetThinkingLevel updates the thinking level.
func (a *Agent) SetThinkingLevel(l ThinkingLevel) {
	a.mu.Lock()
	a.state.ThinkingLevel = l
	a.mu.Unlock()
}

// SetSteeringMode sets the steering mode ("all" or "one-at-a-time").
func (a *Agent) SetSteeringMode(mode string) {
	a.mu.Lock()
	a.steeringMode = mode
	a.mu.Unlock()
}

// GetSteeringMode returns the current steering mode.
func (a *Agent) GetSteeringMode() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.steeringMode
}

// SetFollowUpMode sets the follow-up mode ("all" or "one-at-a-time").
func (a *Agent) SetFollowUpMode(mode string) {
	a.mu.Lock()
	a.followUpMode = mode
	a.mu.Unlock()
}

// GetFollowUpMode returns the current follow-up mode.
func (a *Agent) GetFollowUpMode() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.followUpMode
}

// SetTools updates the available tools.
func (a *Agent) SetTools(tools []*AgentTool) {
	a.mu.Lock()
	a.state.Tools = tools
	a.mu.Unlock()
}

// ReplaceMessages replaces all messages with a copy of the provided slice.
func (a *Agent) ReplaceMessages(msgs []AgentMessage) {
	a.mu.Lock()
	a.state.Messages = make([]AgentMessage, len(msgs))
	copy(a.state.Messages, msgs)
	a.mu.Unlock()
}

// AppendMessage adds a message to the conversation.
func (a *Agent) AppendMessage(m AgentMessage) {
	a.mu.Lock()
	a.state.Messages = append(a.state.Messages, m)
	a.mu.Unlock()
}

// Steer queues a steering message to interrupt the agent mid-run.
func (a *Agent) Steer(m AgentMessage) {
	a.mu.Lock()
	a.steeringQueue = append(a.steeringQueue, m)
	a.mu.Unlock()
}

// FollowUp queues a follow-up message for after the agent finishes.
func (a *Agent) FollowUp(m AgentMessage) {
	a.mu.Lock()
	a.followUpQueue = append(a.followUpQueue, m)
	a.mu.Unlock()
}

// ClearSteeringQueue clears all queued steering messages.
func (a *Agent) ClearSteeringQueue() {
	a.mu.Lock()
	a.steeringQueue = nil
	a.mu.Unlock()
}

// ClearFollowUpQueue clears all queued follow-up messages.
func (a *Agent) ClearFollowUpQueue() {
	a.mu.Lock()
	a.followUpQueue = nil
	a.mu.Unlock()
}

// ClearAllQueues clears both steering and follow-up queues.
func (a *Agent) ClearAllQueues() {
	a.mu.Lock()
	a.steeringQueue = nil
	a.followUpQueue = nil
	a.mu.Unlock()
}

// HasQueuedMessages returns true if there are queued steering or follow-up messages.
func (a *Agent) HasQueuedMessages() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.steeringQueue) > 0 || len(a.followUpQueue) > 0
}

// ClearMessages clears all conversation messages.
func (a *Agent) ClearMessages() {
	a.mu.Lock()
	a.state.Messages = nil
	a.mu.Unlock()
}

// Abort cancels the current operation.
func (a *Agent) Abort() {
	a.mu.RLock()
	signal := a.abortSignal
	a.mu.RUnlock()
	if signal != nil {
		signal.Abort()
	}
}

// WaitForIdle blocks until the agent finishes processing.
func (a *Agent) WaitForIdle() {
	a.mu.RLock()
	done := a.runningDone
	a.mu.RUnlock()
	if done != nil {
		<-done
	}
}

// Reset clears all state.
func (a *Agent) Reset() {
	a.mu.Lock()
	a.state.Messages = nil
	a.state.IsStreaming = false
	a.state.StreamMessage = nil
	a.state.PendingToolCalls = make(map[string]bool)
	a.state.Error = ""
	a.steeringQueue = nil
	a.followUpQueue = nil
	a.mu.Unlock()
}

// SessionID returns the current session ID.
func (a *Agent) SessionID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessionID
}

// SetSessionID sets the session ID.
func (a *Agent) SetSessionID(id string) {
	a.mu.Lock()
	a.sessionID = id
	a.mu.Unlock()
}

// Prompt sends a prompt to the agent.
func (a *Agent) Prompt(input string, images ...ai.ImageContent) error {
	a.mu.Lock()
	if a.state.IsStreaming {
		a.mu.Unlock()
		return fmt.Errorf("agent is already processing a prompt")
	}
	a.mu.Unlock()

	content := ai.UserContent{
		Blocks: []ai.UserContentBlock{
			{Text: &ai.TextContent{Type: "text", Text: input}},
		},
	}
	for _, img := range images {
		content.Blocks = append(content.Blocks, ai.UserContentBlock{
			Image: &ai.ImageContent{Type: "image", Data: img.Data, MimeType: img.MimeType},
		})
	}

	msgs := []AgentMessage{
		NewAgentMessageFromUser(&ai.UserMessage{
			Role:      "user",
			Content:   content,
			Timestamp: time.Now().UnixMilli(),
		}),
	}

	return a.runLoop(msgs, false)
}

// PromptMessages sends one or more AgentMessages as a prompt.
func (a *Agent) PromptMessages(messages ...AgentMessage) error {
	a.mu.Lock()
	if a.state.IsStreaming {
		a.mu.Unlock()
		return fmt.Errorf("agent is already processing a prompt")
	}
	a.mu.Unlock()

	return a.runLoop(messages, false)
}

// Continue resumes from the current context.
func (a *Agent) Continue() error {
	a.mu.Lock()
	if a.state.IsStreaming {
		a.mu.Unlock()
		return fmt.Errorf("agent is already processing")
	}

	messages := a.state.Messages
	if len(messages) == 0 {
		a.mu.Unlock()
		return fmt.Errorf("no messages to continue from")
	}
	last := messages[len(messages)-1]
	if last.IsAssistant() {
		// Check for queued messages
		steering := a.dequeueSteeringMessages()
		if len(steering) > 0 {
			a.mu.Unlock()
			return a.runLoop(steering, true)
		}

		followUp := a.dequeueFollowUpMessages()
		if len(followUp) > 0 {
			a.mu.Unlock()
			return a.runLoop(followUp, false)
		}

		a.mu.Unlock()
		return fmt.Errorf("cannot continue from message role: assistant")
	}
	a.mu.Unlock()

	return a.runLoop(nil, false)
}

func (a *Agent) dequeueSteeringMessages() []AgentMessage {
	if a.steeringMode == "one-at-a-time" {
		if len(a.steeringQueue) > 0 {
			first := a.steeringQueue[0]
			a.steeringQueue = a.steeringQueue[1:]
			return []AgentMessage{first}
		}
		return nil
	}
	steering := a.steeringQueue
	a.steeringQueue = nil
	return steering
}

func (a *Agent) dequeueFollowUpMessages() []AgentMessage {
	if a.followUpMode == "one-at-a-time" {
		if len(a.followUpQueue) > 0 {
			first := a.followUpQueue[0]
			a.followUpQueue = a.followUpQueue[1:]
			return []AgentMessage{first}
		}
		return nil
	}
	followUp := a.followUpQueue
	a.followUpQueue = nil
	return followUp
}

func (a *Agent) runLoop(messages []AgentMessage, skipInitialSteeringPoll bool) error {
	a.mu.Lock()
	model := a.state.Model
	if model == nil {
		a.mu.Unlock()
		return fmt.Errorf("no model configured")
	}

	done := make(chan struct{})
	a.runningDone = done
	a.abortSignal = ai.NewAbortSignal()
	a.state.IsStreaming = true
	a.state.StreamMessage = nil
	a.state.Error = ""

	var reasoning ai.ThinkingLevel
	if a.state.ThinkingLevel != ThinkingOff {
		reasoning = ai.ThinkingLevel(a.state.ThinkingLevel)
	}

	ctx := &AgentContext{
		SystemPrompt: a.state.SystemPrompt,
		Messages:     make([]AgentMessage, len(a.state.Messages)),
		Tools:        a.state.Tools,
	}
	copy(ctx.Messages, a.state.Messages)

	skipSteering := skipInitialSteeringPoll

	config := &AgentLoopConfig{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				SessionID:       a.sessionID,
				MaxRetryDelayMs: a.maxRetryDelayMs,
			},
			Reasoning:       reasoning,
			ThinkingBudgets: a.thinkingBudgets,
		},
		Model:        model,
		ConvertToLLM: a.convertToLLM,
		TransformContext: a.transformContext,
		GetApiKey: a.getApiKey,
		GetSteeringMessages: func() ([]AgentMessage, error) {
			if skipSteering {
				skipSteering = false
				return nil, nil
			}
			a.mu.Lock()
			result := a.dequeueSteeringMessages()
			a.mu.Unlock()
			return result, nil
		},
		GetFollowUpMessages: func() ([]AgentMessage, error) {
			a.mu.Lock()
			result := a.dequeueFollowUpMessages()
			a.mu.Unlock()
			return result, nil
		},
	}

	signal := a.abortSignal
	streamFnCopy := a.streamFn
	a.mu.Unlock()

	var stream *AgentEventStream
	if messages != nil {
		stream = AgentLoop(messages, ctx, config, signal, streamFnCopy)
	} else {
		var err error
		stream, err = AgentLoopContinue(ctx, config, signal, streamFnCopy)
		if err != nil {
			a.mu.Lock()
			a.state.IsStreaming = false
			a.state.StreamMessage = nil
			a.state.PendingToolCalls = make(map[string]bool)
			a.abortSignal = nil
			a.runningDone = nil
			a.mu.Unlock()
			close(done)
			return err
		}
	}

	// Process events
	go func() {
		defer func() {
			a.mu.Lock()
			a.state.IsStreaming = false
			a.state.StreamMessage = nil
			a.state.PendingToolCalls = make(map[string]bool)
			a.abortSignal = nil
			a.runningDone = nil
			a.mu.Unlock()
			close(done)
		}()

		for event := range stream.Events() {
			a.mu.Lock()
			switch event.Type {
			case "message_start":
				a.state.StreamMessage = event.EventMessage
			case "message_update":
				a.state.StreamMessage = event.EventMessage
			case "message_end":
				a.state.StreamMessage = nil
				if event.EventMessage != nil {
					a.state.Messages = append(a.state.Messages, *event.EventMessage)
				}
			case "tool_execution_start":
				pending := make(map[string]bool)
				for k, v := range a.state.PendingToolCalls {
					pending[k] = v
				}
				pending[event.ToolCallID] = true
				a.state.PendingToolCalls = pending
			case "tool_execution_end":
				pending := make(map[string]bool)
				for k, v := range a.state.PendingToolCalls {
					pending[k] = v
				}
				delete(pending, event.ToolCallID)
				a.state.PendingToolCalls = pending
			case "turn_end":
				if event.TurnMessage != nil && event.TurnMessage.IsAssistant() {
					msg := event.TurnMessage.Message.Assistant
					if msg.ErrorMessage != "" {
						a.state.Error = msg.ErrorMessage
					}
				}
			case "agent_end":
				a.state.IsStreaming = false
				a.state.StreamMessage = nil
			}
			a.mu.Unlock()

			a.emit(event)
		}
	}()

	return nil
}

func (a *Agent) emit(e AgentEvent) {
	a.mu.RLock()
	listeners := make([]func(AgentEvent), 0, len(a.listeners))
	for _, fn := range a.listeners {
		listeners = append(listeners, fn)
	}
	a.mu.RUnlock()

	for _, fn := range listeners {
		fn(e)
	}
}

// GetTextContent extracts the text content from assistant messages.
func GetTextContent(messages []AgentMessage) string {
	var parts []string
	for _, m := range messages {
		if m.IsAssistant() {
			for _, block := range m.Message.Assistant.Content {
				if block.Text != nil {
					parts = append(parts, block.Text.Text)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}
