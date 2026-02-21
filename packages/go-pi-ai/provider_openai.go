package ai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// StreamOpenAICompletions creates a streaming call to an OpenAI-compatible completions API.
func StreamOpenAICompletions(model *Model, ctx *Context, options *StreamOptions) *AssistantMessageEventStream {
	stream := NewAssistantMessageEventStream()

	go func() {
		output := NewAssistantMessage(model.ApiType, model.Provider, model.ID, time.Now().UnixMilli())

		apiKey := ""
		if options != nil {
			apiKey = options.ApiKey
		}
		if apiKey == "" {
			apiKey = GetEnvApiKey(model.Provider)
		}
		if apiKey == "" {
			output.StopReason = StopReasonError
			output.ErrorMessage = fmt.Sprintf("No API key for provider: %s", model.Provider)
			stream.Push(AssistantMessageEvent{Type: "error", Reason: StopReasonError, ErrorMessage: output})
			stream.End()
			return
		}

		params := buildOpenAIParams(model, ctx, options)
		payloadBytes, err := json.Marshal(params)
		if err != nil {
			output.StopReason = StopReasonError
			output.ErrorMessage = err.Error()
			stream.Push(AssistantMessageEvent{Type: "error", Reason: StopReasonError, ErrorMessage: output})
			stream.End()
			return
		}

		baseURL := model.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com"
		}

		req, err := http.NewRequest("POST", baseURL+"/v1/chat/completions", bytes.NewReader(payloadBytes))
		if err != nil {
			output.StopReason = StopReasonError
			output.ErrorMessage = err.Error()
			stream.Push(AssistantMessageEvent{Type: "error", Reason: StopReasonError, ErrorMessage: output})
			stream.End()
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Authorization", "Bearer "+apiKey)

		for k, v := range model.Headers {
			req.Header.Set(k, v)
		}
		if options != nil {
			for k, v := range options.Headers {
				req.Header.Set(k, v)
			}
		}

		var signal *AbortSignal
		if options != nil {
			signal = options.Signal
		}
		if signal != nil {
			req = req.WithContext(contextFromAbortSignal(signal))
		}

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			reason := StopReasonError
			if signal != nil && signal.Aborted() {
				reason = StopReasonAborted
			}
			output.StopReason = reason
			output.ErrorMessage = err.Error()
			stream.Push(AssistantMessageEvent{Type: "error", Reason: reason, ErrorMessage: output})
			stream.End()
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			output.StopReason = StopReasonError
			output.ErrorMessage = fmt.Sprintf("OpenAI API error: %d %s: %s", resp.StatusCode, resp.Status, string(body))
			stream.Push(AssistantMessageEvent{Type: "error", Reason: StopReasonError, ErrorMessage: output})
			stream.End()
			return
		}

		stream.Push(AssistantMessageEvent{Type: "start", Partial: output})

		// Track current text and tool call content
		currentTextIndex := -1
		toolCalls := make(map[int]struct {
			contentIndex int
			partialJSON  string
		})

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var chunk struct {
				Choices []struct {
					Delta struct {
						Content   string `json:"content"`
						ToolCalls []struct {
							Index    int    `json:"index"`
							ID       string `json:"id"`
							Type     string `json:"type"`
							Function struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
					FinishReason *string `json:"finish_reason"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
					TotalTokens      int `json:"total_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}

			if len(chunk.Choices) > 0 {
				choice := chunk.Choices[0]

				// Handle text content
				if choice.Delta.Content != "" {
					if currentTextIndex < 0 {
						currentTextIndex = len(output.Content)
						output.Content = append(output.Content, ContentBlock{
							Text: &TextContent{Type: "text", Text: ""},
						})
						stream.Push(AssistantMessageEvent{
							Type: "text_start", ContentIndex: currentTextIndex, Partial: output,
						})
					}
					output.Content[currentTextIndex].Text.Text += choice.Delta.Content
					stream.Push(AssistantMessageEvent{
						Type: "text_delta", ContentIndex: currentTextIndex,
						Delta: choice.Delta.Content, Partial: output,
					})
				}

				// Handle tool calls
				for _, tc := range choice.Delta.ToolCalls {
					existing, ok := toolCalls[tc.Index]
					if !ok {
						// New tool call
						ci := len(output.Content)
						output.Content = append(output.Content, ContentBlock{
							ToolCall: &ToolCall{
								Type:      "toolCall",
								ID:        tc.ID,
								Name:      tc.Function.Name,
								Arguments: map[string]any{},
							},
						})
						toolCalls[tc.Index] = struct {
							contentIndex int
							partialJSON  string
						}{contentIndex: ci, partialJSON: tc.Function.Arguments}
						stream.Push(AssistantMessageEvent{
							Type: "toolcall_start", ContentIndex: ci, Partial: output,
						})
					} else {
						// Delta for existing tool call
						existing.partialJSON += tc.Function.Arguments
						toolCalls[tc.Index] = existing
						ci := existing.contentIndex
						output.Content[ci].ToolCall.Arguments = ParseStreamingJSON(existing.partialJSON)
						stream.Push(AssistantMessageEvent{
							Type: "toolcall_delta", ContentIndex: ci,
							Delta: tc.Function.Arguments, Partial: output,
						})
					}
				}

				// Handle finish reason
				if choice.FinishReason != nil {
					switch *choice.FinishReason {
					case "stop":
						output.StopReason = StopReasonStop
					case "length":
						output.StopReason = StopReasonLength
					case "tool_calls":
						output.StopReason = StopReasonToolUse
					default:
						output.StopReason = StopReasonStop
					}
				}
			}

			if chunk.Usage != nil {
				output.Usage.Input = chunk.Usage.PromptTokens
				output.Usage.Output = chunk.Usage.CompletionTokens
				output.Usage.TotalTokens = chunk.Usage.TotalTokens
				CalculateCost(model, &output.Usage)
			}

			if signal != nil && signal.Aborted() {
				output.StopReason = StopReasonAborted
				output.ErrorMessage = "Request was aborted"
				stream.Push(AssistantMessageEvent{Type: "error", Reason: StopReasonAborted, ErrorMessage: output})
				stream.End()
				return
			}
		}

		// Close text content
		if currentTextIndex >= 0 {
			stream.Push(AssistantMessageEvent{
				Type: "text_end", ContentIndex: currentTextIndex,
				Content: output.Content[currentTextIndex].Text.Text, Partial: output,
			})
		}

		// Close tool calls
		for _, tc := range toolCalls {
			output.Content[tc.contentIndex].ToolCall.Arguments = ParseStreamingJSON(tc.partialJSON)
			stream.Push(AssistantMessageEvent{
				Type: "toolcall_end", ContentIndex: tc.contentIndex,
				ToolCallData: output.Content[tc.contentIndex].ToolCall, Partial: output,
			})
		}

		stream.Push(AssistantMessageEvent{Type: "done", Reason: output.StopReason, FinalMessage: output})
		stream.End()
	}()

	return stream
}

// StreamSimpleOpenAICompletions creates a streaming call with simplified options.
func StreamSimpleOpenAICompletions(model *Model, ctx *Context, options *SimpleStreamOptions) *AssistantMessageEventStream {
	var opts *StreamOptions
	if options != nil {
		opts = &options.StreamOptions
	}
	return StreamOpenAICompletions(model, ctx, opts)
}

func buildOpenAIParams(model *Model, ctx *Context, options *StreamOptions) map[string]any {
	params := map[string]any{
		"model":  model.ID,
		"stream": true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}

	maxTokens := 32000
	if model.MaxTokens > 0 && model.MaxTokens < maxTokens {
		maxTokens = model.MaxTokens
	}
	if options != nil && options.MaxTokens != nil {
		maxTokens = *options.MaxTokens
	}
	params["max_completion_tokens"] = maxTokens

	if options != nil && options.Temperature != nil {
		params["temperature"] = *options.Temperature
	}

	// Build messages
	var msgs []map[string]any
	if ctx.SystemPrompt != "" {
		msgs = append(msgs, map[string]any{
			"role":    "system",
			"content": ctx.SystemPrompt,
		})
	}

	for _, msg := range ctx.Messages {
		if msg.User != nil {
			msgs = append(msgs, map[string]any{
				"role":    "user",
				"content": userContentString(msg.User),
			})
		} else if msg.Assistant != nil {
			m := map[string]any{
				"role": "assistant",
			}
			var textParts []string
			var toolCallList []map[string]any
			for _, block := range msg.Assistant.Content {
				if block.Text != nil {
					textParts = append(textParts, block.Text.Text)
				} else if block.ToolCall != nil {
					argsJSON, _ := json.Marshal(block.ToolCall.Arguments)
					toolCallList = append(toolCallList, map[string]any{
						"id":   block.ToolCall.ID,
						"type": "function",
						"function": map[string]any{
							"name":      block.ToolCall.Name,
							"arguments": string(argsJSON),
						},
					})
				}
			}
			if len(textParts) > 0 {
				m["content"] = strings.Join(textParts, "\n")
			}
			if len(toolCallList) > 0 {
				m["tool_calls"] = toolCallList
			}
			msgs = append(msgs, m)
		} else if msg.ToolResult != nil {
			content := toolResultContentString(msg.ToolResult.Content)
			msgs = append(msgs, map[string]any{
				"role":         "tool",
				"tool_call_id": msg.ToolResult.ToolCallID,
				"content":      content,
			})
		}
	}
	params["messages"] = msgs

	// Tools
	if len(ctx.Tools) > 0 {
		var tools []map[string]any
		for _, tool := range ctx.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        tool.Name,
					"description": tool.Description,
					"parameters":  tool.Parameters,
				},
			})
		}
		params["tools"] = tools
	}

	return params
}

func userContentString(msg *UserMessage) string {
	if msg.Content.IsText() {
		return msg.Content.Text
	}
	var parts []string
	for _, b := range msg.Content.Blocks {
		if b.Text != nil {
			parts = append(parts, b.Text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func toolResultContentString(content []ToolResultContentBlock) string {
	var parts []string
	for _, c := range content {
		if c.Text != nil {
			parts = append(parts, c.Text.Text)
		}
	}
	return strings.Join(parts, "\n")
}
