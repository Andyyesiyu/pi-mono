package agent

import (
	"fmt"
	"time"

	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

// AgentEventStream is an EventStream specialized for agent events.
type AgentEventStream = ai.EventStream[AgentEvent, []AgentMessage]

// NewAgentEventStream creates a new AgentEventStream.
func NewAgentEventStream() *AgentEventStream {
	return ai.NewEventStream[AgentEvent, []AgentMessage](
		func(event AgentEvent) bool {
			return event.Type == "agent_end"
		},
		func(event AgentEvent) []AgentMessage {
			if event.Type == "agent_end" {
				return event.Messages
			}
			return nil
		},
	)
}

// AgentLoop starts an agent loop with new prompt messages.
func AgentLoop(
	prompts []AgentMessage,
	ctx *AgentContext,
	config *AgentLoopConfig,
	signal *ai.AbortSignal,
	streamFn StreamFn,
) *AgentEventStream {
	stream := NewAgentEventStream()

	go func() {
		newMessages := make([]AgentMessage, len(prompts))
		copy(newMessages, prompts)

		currentContext := &AgentContext{
			SystemPrompt: ctx.SystemPrompt,
			Messages:     make([]AgentMessage, 0, len(ctx.Messages)+len(prompts)),
			Tools:        ctx.Tools,
		}
		currentContext.Messages = append(currentContext.Messages, ctx.Messages...)
		currentContext.Messages = append(currentContext.Messages, prompts...)

		stream.Push(AgentEvent{Type: "agent_start"})
		stream.Push(AgentEvent{Type: "turn_start"})
		for i := range prompts {
			stream.Push(AgentEvent{Type: "message_start", EventMessage: &prompts[i]})
			stream.Push(AgentEvent{Type: "message_end", EventMessage: &prompts[i]})
		}

		runLoop(currentContext, newMessages, config, signal, stream, streamFn)
	}()

	return stream
}

// AgentLoopContinue continues an agent loop from the current context.
func AgentLoopContinue(
	ctx *AgentContext,
	config *AgentLoopConfig,
	signal *ai.AbortSignal,
	streamFn StreamFn,
) (*AgentEventStream, error) {
	if len(ctx.Messages) == 0 {
		return nil, fmt.Errorf("cannot continue: no messages in context")
	}
	last := ctx.Messages[len(ctx.Messages)-1]
	if last.IsAssistant() {
		return nil, fmt.Errorf("cannot continue from message role: assistant")
	}

	stream := NewAgentEventStream()

	go func() {
		newMessages := []AgentMessage{}
		currentContext := &AgentContext{
			SystemPrompt: ctx.SystemPrompt,
			Messages:     make([]AgentMessage, len(ctx.Messages)),
			Tools:        ctx.Tools,
		}
		copy(currentContext.Messages, ctx.Messages)

		stream.Push(AgentEvent{Type: "agent_start"})
		stream.Push(AgentEvent{Type: "turn_start"})

		runLoop(currentContext, newMessages, config, signal, stream, streamFn)
	}()

	return stream, nil
}

// runLoop is the main loop logic shared by AgentLoop and AgentLoopContinue.
func runLoop(
	currentContext *AgentContext,
	newMessages []AgentMessage,
	config *AgentLoopConfig,
	signal *ai.AbortSignal,
	stream *AgentEventStream,
	streamFn StreamFn,
) {
	firstTurn := true

	// Check for steering messages at start
	var pendingMessages []AgentMessage
	if config.GetSteeringMessages != nil {
		pm, err := config.GetSteeringMessages()
		if err == nil {
			pendingMessages = pm
		}
	}

	// Outer loop: continues when follow-up messages arrive
	for {
		hasMoreToolCalls := true
		var steeringAfterTools []AgentMessage

		// Inner loop: process tool calls and steering messages
		for hasMoreToolCalls || len(pendingMessages) > 0 {
			if !firstTurn {
				stream.Push(AgentEvent{Type: "turn_start"})
			} else {
				firstTurn = false
			}

			// Process pending messages
			if len(pendingMessages) > 0 {
				for i := range pendingMessages {
					stream.Push(AgentEvent{Type: "message_start", EventMessage: &pendingMessages[i]})
					stream.Push(AgentEvent{Type: "message_end", EventMessage: &pendingMessages[i]})
					currentContext.Messages = append(currentContext.Messages, pendingMessages[i])
					newMessages = append(newMessages, pendingMessages[i])
				}
				pendingMessages = nil
			}

			// Stream assistant response
			message, err := streamAssistantResponse(currentContext, config, signal, stream, streamFn)
			if err != nil {
				// Create error message
				errMsg := createErrorMessage(config.Model, err, signal)
				am := NewAgentMessageFromAssistant(errMsg)
				newMessages = append(newMessages, am)
				stream.Push(AgentEvent{Type: "turn_end", TurnMessage: &am, ToolResults: nil})
				stream.Push(AgentEvent{Type: "agent_end", Messages: newMessages})
				stream.End(newMessages)
				return
			}

			am := NewAgentMessageFromAssistant(message)
			newMessages = append(newMessages, am)

			if message.StopReason == ai.StopReasonError || message.StopReason == ai.StopReasonAborted {
				stream.Push(AgentEvent{Type: "turn_end", TurnMessage: &am, ToolResults: nil})
				stream.Push(AgentEvent{Type: "agent_end", Messages: newMessages})
				stream.End(newMessages)
				return
			}

			// Check for tool calls
			var toolCalls []*ai.ToolCall
			for i := range message.Content {
				if message.Content[i].ToolCall != nil {
					toolCalls = append(toolCalls, message.Content[i].ToolCall)
				}
			}
			hasMoreToolCalls = len(toolCalls) > 0

			var toolResults []*ai.ToolResultMessage
			if hasMoreToolCalls {
				tr, steering := executeToolCalls(
					currentContext.Tools, message, signal, stream,
					config.GetSteeringMessages,
				)
				toolResults = tr
				steeringAfterTools = steering

				for _, result := range toolResults {
					rm := NewAgentMessageFromToolResult(result)
					currentContext.Messages = append(currentContext.Messages, rm)
					newMessages = append(newMessages, rm)
				}
			}

			stream.Push(AgentEvent{Type: "turn_end", TurnMessage: &am, ToolResults: toolResults})

			// Get steering messages after turn completes
			if len(steeringAfterTools) > 0 {
				pendingMessages = steeringAfterTools
				steeringAfterTools = nil
			} else if config.GetSteeringMessages != nil {
				pm, err := config.GetSteeringMessages()
				if err == nil {
					pendingMessages = pm
				}
			}
		}

		// Check for follow-up messages
		if config.GetFollowUpMessages != nil {
			followUp, err := config.GetFollowUpMessages()
			if err == nil && len(followUp) > 0 {
				pendingMessages = followUp
				continue
			}
		}

		break
	}

	stream.Push(AgentEvent{Type: "agent_end", Messages: newMessages})
	stream.End(newMessages)
}

// streamAssistantResponse streams a response from the LLM.
func streamAssistantResponse(
	ctx *AgentContext,
	config *AgentLoopConfig,
	signal *ai.AbortSignal,
	stream *AgentEventStream,
	streamFn StreamFn,
) (*ai.AssistantMessage, error) {
	// Apply context transform
	messages := ctx.Messages
	if config.TransformContext != nil {
		var err error
		messages, err = config.TransformContext(messages, signal)
		if err != nil {
			return nil, fmt.Errorf("transform context: %w", err)
		}
	}

	// Convert to LLM messages
	llmMessages, err := config.ConvertToLLM(messages)
	if err != nil {
		return nil, fmt.Errorf("convert to LLM: %w", err)
	}

	// Build LLM context
	var llmTools []ai.Tool
	for _, t := range ctx.Tools {
		llmTools = append(llmTools, t.Tool)
	}

	llmContext := &ai.Context{
		SystemPrompt: ctx.SystemPrompt,
		Messages:     llmMessages,
		Tools:        llmTools,
	}

	// Resolve API key
	apiKey := config.ApiKey
	if config.GetApiKey != nil {
		key, err := config.GetApiKey(config.Model.Provider)
		if err == nil && key != "" {
			apiKey = key
		}
	}

	// Build stream options
	opts := &ai.SimpleStreamOptions{
		StreamOptions: ai.StreamOptions{
			Temperature:     config.Temperature,
			MaxTokens:       config.MaxTokens,
			Signal:          signal,
			ApiKey:          apiKey,
			CacheRetention:  config.CacheRetention,
			SessionID:       config.SessionID,
			Headers:         config.Headers,
			MaxRetryDelayMs: config.MaxRetryDelayMs,
		},
		Reasoning:       config.Reasoning,
		ThinkingBudgets: config.ThinkingBudgets,
	}

	// Use custom stream function or default
	var response *ai.AssistantMessageEventStream
	if streamFn != nil {
		response = streamFn(config.Model, llmContext, opts)
	} else {
		var serr error
		response, serr = ai.StreamSimple(config.Model, llmContext, opts)
		if serr != nil {
			return nil, serr
		}
	}

	var partialMessage *ai.AssistantMessage
	addedPartial := false

	for event := range response.Events() {
		switch event.Type {
		case "start":
			partialMessage = event.Partial
			ctx.Messages = append(ctx.Messages, NewAgentMessageFromAssistant(partialMessage))
			addedPartial = true
			am := NewAgentMessageFromAssistant(partialMessage)
			stream.Push(AgentEvent{Type: "message_start", EventMessage: &am})

		case "text_start", "text_delta", "text_end",
			"thinking_start", "thinking_delta", "thinking_end",
			"toolcall_start", "toolcall_delta", "toolcall_end":
			if partialMessage != nil {
				partialMessage = event.Partial
				ctx.Messages[len(ctx.Messages)-1] = NewAgentMessageFromAssistant(partialMessage)
				am := NewAgentMessageFromAssistant(partialMessage)
				stream.Push(AgentEvent{
					Type:                  "message_update",
					AssistantMessageEvent: &event,
					EventMessage:          &am,
				})
			}

		case "done", "error":
			finalMessage := response.Result()
			if addedPartial {
				ctx.Messages[len(ctx.Messages)-1] = NewAgentMessageFromAssistant(finalMessage)
			} else {
				ctx.Messages = append(ctx.Messages, NewAgentMessageFromAssistant(finalMessage))
			}
			if !addedPartial {
				am := NewAgentMessageFromAssistant(finalMessage)
				stream.Push(AgentEvent{Type: "message_start", EventMessage: &am})
			}
			am := NewAgentMessageFromAssistant(finalMessage)
			stream.Push(AgentEvent{Type: "message_end", EventMessage: &am})
			return finalMessage, nil
		}
	}

	return response.Result(), nil
}

// executeToolCalls executes tool calls from an assistant message.
func executeToolCalls(
	tools []*AgentTool,
	assistantMessage *ai.AssistantMessage,
	signal *ai.AbortSignal,
	stream *AgentEventStream,
	getSteeringMessages func() ([]AgentMessage, error),
) ([]*ai.ToolResultMessage, []AgentMessage) {
	var toolCalls []*ai.ToolCall
	for i := range assistantMessage.Content {
		if assistantMessage.Content[i].ToolCall != nil {
			toolCalls = append(toolCalls, assistantMessage.Content[i].ToolCall)
		}
	}

	var results []*ai.ToolResultMessage
	var steeringMessages []AgentMessage

	for index, toolCall := range toolCalls {
		// Find tool
		var tool *AgentTool
		for _, t := range tools {
			if t.Name == toolCall.Name {
				tool = t
				break
			}
		}

		stream.Push(AgentEvent{
			Type:       "tool_execution_start",
			ToolCallID: toolCall.ID,
			ToolName:   toolCall.Name,
			Args:       toolCall.Arguments,
		})

		var result *AgentToolResult
		isError := false

		if tool == nil {
			result = &AgentToolResult{
				Content: []ai.ToolResultContentBlock{
					{Text: &ai.TextContent{Type: "text", Text: fmt.Sprintf("Tool %s not found", toolCall.Name)}},
				},
			}
			isError = true
		} else {
			// Validate arguments
			_, valErr := ai.ValidateToolArguments(&tool.Tool, toolCall)
			if valErr != nil {
				result = &AgentToolResult{
					Content: []ai.ToolResultContentBlock{
						{Text: &ai.TextContent{Type: "text", Text: valErr.Error()}},
					},
				}
				isError = true
			} else {
				var execErr error
				result, execErr = tool.Execute(toolCall.ID, toolCall.Arguments, signal, func(partial AgentToolResult) {
					stream.Push(AgentEvent{
						Type:          "tool_execution_update",
						ToolCallID:    toolCall.ID,
						ToolName:      toolCall.Name,
						Args:          toolCall.Arguments,
						PartialResult: partial,
					})
				})
				if execErr != nil {
					result = &AgentToolResult{
						Content: []ai.ToolResultContentBlock{
							{Text: &ai.TextContent{Type: "text", Text: execErr.Error()}},
						},
					}
					isError = true
				}
			}
		}

		stream.Push(AgentEvent{
			Type:       "tool_execution_end",
			ToolCallID: toolCall.ID,
			ToolName:   toolCall.Name,
			Result:     result,
			IsError:    isError,
		})

		toolResultMsg := &ai.ToolResultMessage{
			Role:       "toolResult",
			ToolCallID: toolCall.ID,
			ToolName:   toolCall.Name,
			Content:    result.Content,
			Details:    result.Details,
			IsError:    isError,
			Timestamp:  time.Now().UnixMilli(),
		}

		results = append(results, toolResultMsg)
		rm := NewAgentMessageFromToolResult(toolResultMsg)
		stream.Push(AgentEvent{Type: "message_start", EventMessage: &rm})
		stream.Push(AgentEvent{Type: "message_end", EventMessage: &rm})

		// Check for steering messages
		if getSteeringMessages != nil {
			steering, err := getSteeringMessages()
			if err == nil && len(steering) > 0 {
				steeringMessages = steering
				// Skip remaining tool calls
				for _, skipped := range toolCalls[index+1:] {
					skippedResult := skipToolCall(skipped, stream)
					results = append(results, skippedResult)
				}
				break
			}
		}
	}

	return results, steeringMessages
}

func skipToolCall(toolCall *ai.ToolCall, stream *AgentEventStream) *ai.ToolResultMessage {
	result := &AgentToolResult{
		Content: []ai.ToolResultContentBlock{
			{Text: &ai.TextContent{Type: "text", Text: "Skipped due to queued user message."}},
		},
	}

	stream.Push(AgentEvent{
		Type:       "tool_execution_start",
		ToolCallID: toolCall.ID,
		ToolName:   toolCall.Name,
		Args:       toolCall.Arguments,
	})
	stream.Push(AgentEvent{
		Type:       "tool_execution_end",
		ToolCallID: toolCall.ID,
		ToolName:   toolCall.Name,
		Result:     result,
		IsError:    true,
	})

	toolResultMsg := &ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallID: toolCall.ID,
		ToolName:   toolCall.Name,
		Content:    result.Content,
		IsError:    true,
		Timestamp:  time.Now().UnixMilli(),
	}

	rm := NewAgentMessageFromToolResult(toolResultMsg)
	stream.Push(AgentEvent{Type: "message_start", EventMessage: &rm})
	stream.Push(AgentEvent{Type: "message_end", EventMessage: &rm})

	return toolResultMsg
}

func createErrorMessage(model *ai.Model, err error, signal *ai.AbortSignal) *ai.AssistantMessage {
	stopReason := ai.StopReasonError
	if signal != nil && signal.Aborted() {
		stopReason = ai.StopReasonAborted
	}
	return &ai.AssistantMessage{
		Role:    "assistant",
		Content: []ai.ContentBlock{{Text: &ai.TextContent{Type: "text", Text: ""}}},
		ApiType: model.ApiType,
		Provider: model.Provider,
		Model:   model.ID,
		Usage:   ai.Usage{},
		StopReason:   stopReason,
		ErrorMessage: err.Error(),
		Timestamp:    time.Now().UnixMilli(),
	}
}
