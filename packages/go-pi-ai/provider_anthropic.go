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

// AnthropicOptions extends StreamOptions with Anthropic-specific settings.
type AnthropicOptions struct {
	StreamOptions
	ThinkingEnabled      bool           `json:"thinkingEnabled,omitempty"`
	ThinkingBudgetTokens int            `json:"thinkingBudgetTokens,omitempty"`
	Effort               string         `json:"effort,omitempty"` // "low", "medium", "high", "max"
	InterleavedThinking  *bool          `json:"interleavedThinking,omitempty"`
	ToolChoice           any            `json:"toolChoice,omitempty"`
}

// StreamAnthropic creates a streaming call to the Anthropic Messages API.
func StreamAnthropic(model *Model, ctx *Context, options *AnthropicOptions) *AssistantMessageEventStream {
	stream := NewAssistantMessageEventStream()

	go func() {
		output := NewAssistantMessage(model.ApiType, model.Provider, model.ID, time.Now().UnixMilli())

		apiKey := options.ApiKey
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

		params := buildAnthropicParams(model, ctx, options)
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
			baseURL = "https://api.anthropic.com"
		}

		req, err := http.NewRequest("POST", baseURL+"/v1/messages", bytes.NewReader(payloadBytes))
		if err != nil {
			output.StopReason = StopReasonError
			output.ErrorMessage = err.Error()
			stream.Push(AssistantMessageEvent{Type: "error", Reason: StopReasonError, ErrorMessage: output})
			stream.End()
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("anthropic-beta", "fine-grained-tool-streaming-2025-05-14,interleaved-thinking-2025-05-14")

		// Add model headers
		for k, v := range model.Headers {
			req.Header.Set(k, v)
		}
		// Add option headers
		for k, v := range options.Headers {
			req.Header.Set(k, v)
		}

		if options.Signal != nil {
			req = req.WithContext(contextFromAbortSignal(options.Signal))
		}

		if options.OnPayload != nil {
			options.OnPayload(params)
		}

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			reason := StopReasonError
			if options.Signal != nil && options.Signal.Aborted() {
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
			output.ErrorMessage = fmt.Sprintf("Anthropic API error: %d %s: %s", resp.StatusCode, resp.Status, string(body))
			stream.Push(AssistantMessageEvent{Type: "error", Reason: StopReasonError, ErrorMessage: output})
			stream.End()
			return
		}

		stream.Push(AssistantMessageEvent{Type: "start", Partial: output})

		// Track content blocks by their Anthropic index
		type blockInfo struct {
			contentIndex int // Index in output.Content
			partialJSON  string
		}
		blocks := make(map[int]*blockInfo)

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "" {
				continue
			}

			var event map[string]json.RawMessage
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			var eventType string
			if typeBytes, ok := event["type"]; ok {
				json.Unmarshal(typeBytes, &eventType)
			}

			switch eventType {
			case "message_start":
				// Extract initial usage from message_start
				var msgStart struct {
					Message struct {
						Usage struct {
							InputTokens              int `json:"input_tokens"`
							OutputTokens             int `json:"output_tokens"`
							CacheReadInputTokens     int `json:"cache_read_input_tokens"`
							CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
						} `json:"usage"`
					} `json:"message"`
				}
				json.Unmarshal([]byte(data), &msgStart)
				output.Usage.Input = msgStart.Message.Usage.InputTokens
				output.Usage.Output = msgStart.Message.Usage.OutputTokens
				output.Usage.CacheRead = msgStart.Message.Usage.CacheReadInputTokens
				output.Usage.CacheWrite = msgStart.Message.Usage.CacheCreationInputTokens
				output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output + output.Usage.CacheRead + output.Usage.CacheWrite
				CalculateCost(model, &output.Usage)

			case "content_block_start":
				var blockStart struct {
					Index        int `json:"index"`
					ContentBlock struct {
						Type    string         `json:"type"`
						ID      string         `json:"id"`
						Name    string         `json:"name"`
						Input   map[string]any `json:"input"`
					} `json:"content_block"`
				}
				json.Unmarshal([]byte(data), &blockStart)

				ci := len(output.Content)
				blocks[blockStart.Index] = &blockInfo{contentIndex: ci}

				switch blockStart.ContentBlock.Type {
				case "text":
					output.Content = append(output.Content, ContentBlock{
						Text: &TextContent{Type: "text", Text: ""},
					})
					stream.Push(AssistantMessageEvent{Type: "text_start", ContentIndex: ci, Partial: output})
				case "thinking":
					output.Content = append(output.Content, ContentBlock{
						Thinking: &ThinkingContent{Type: "thinking", Thinking: ""},
					})
					stream.Push(AssistantMessageEvent{Type: "thinking_start", ContentIndex: ci, Partial: output})
				case "tool_use":
					args := blockStart.ContentBlock.Input
					if args == nil {
						args = map[string]any{}
					}
					output.Content = append(output.Content, ContentBlock{
						ToolCall: &ToolCall{
							Type:      "toolCall",
							ID:        blockStart.ContentBlock.ID,
							Name:      blockStart.ContentBlock.Name,
							Arguments: args,
						},
					})
					stream.Push(AssistantMessageEvent{Type: "toolcall_start", ContentIndex: ci, Partial: output})
				}

			case "content_block_delta":
				var delta struct {
					Index int `json:"index"`
					Delta struct {
						Type        string `json:"type"`
						Text        string `json:"text"`
						Thinking    string `json:"thinking"`
						PartialJSON string `json:"partial_json"`
						Signature   string `json:"signature"`
					} `json:"delta"`
				}
				json.Unmarshal([]byte(data), &delta)

				bi, ok := blocks[delta.Index]
				if !ok {
					continue
				}
				ci := bi.contentIndex
				if ci >= len(output.Content) {
					continue
				}
				block := &output.Content[ci]

				switch delta.Delta.Type {
				case "text_delta":
					if block.Text != nil {
						block.Text.Text += delta.Delta.Text
						stream.Push(AssistantMessageEvent{
							Type: "text_delta", ContentIndex: ci,
							Delta: delta.Delta.Text, Partial: output,
						})
					}
				case "thinking_delta":
					if block.Thinking != nil {
						block.Thinking.Thinking += delta.Delta.Thinking
						stream.Push(AssistantMessageEvent{
							Type: "thinking_delta", ContentIndex: ci,
							Delta: delta.Delta.Thinking, Partial: output,
						})
					}
				case "input_json_delta":
					if block.ToolCall != nil {
						bi.partialJSON += delta.Delta.PartialJSON
						block.ToolCall.Arguments = ParseStreamingJSON(bi.partialJSON)
						stream.Push(AssistantMessageEvent{
							Type: "toolcall_delta", ContentIndex: ci,
							Delta: delta.Delta.PartialJSON, Partial: output,
						})
					}
				case "signature_delta":
					if block.Thinking != nil {
						block.Thinking.ThinkingSignature += delta.Delta.Signature
					}
				}

			case "content_block_stop":
				var blockStop struct {
					Index int `json:"index"`
				}
				json.Unmarshal([]byte(data), &blockStop)

				bi, ok := blocks[blockStop.Index]
				if !ok {
					continue
				}
				ci := bi.contentIndex
				if ci >= len(output.Content) {
					continue
				}
				block := &output.Content[ci]

				if block.Text != nil {
					stream.Push(AssistantMessageEvent{
						Type: "text_end", ContentIndex: ci,
						Content: block.Text.Text, Partial: output,
					})
				} else if block.Thinking != nil {
					stream.Push(AssistantMessageEvent{
						Type: "thinking_end", ContentIndex: ci,
						Content: block.Thinking.Thinking, Partial: output,
					})
				} else if block.ToolCall != nil {
					block.ToolCall.Arguments = ParseStreamingJSON(bi.partialJSON)
					stream.Push(AssistantMessageEvent{
						Type: "toolcall_end", ContentIndex: ci,
						ToolCallData: block.ToolCall, Partial: output,
					})
				}

			case "message_delta":
				var msgDelta struct {
					Delta struct {
						StopReason string `json:"stop_reason"`
					} `json:"delta"`
					Usage struct {
						InputTokens              *int `json:"input_tokens"`
						OutputTokens             *int `json:"output_tokens"`
						CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
						CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
					} `json:"usage"`
				}
				json.Unmarshal([]byte(data), &msgDelta)

				if msgDelta.Delta.StopReason != "" {
					output.StopReason = mapAnthropicStopReason(msgDelta.Delta.StopReason)
				}
				if msgDelta.Usage.InputTokens != nil {
					output.Usage.Input = *msgDelta.Usage.InputTokens
				}
				if msgDelta.Usage.OutputTokens != nil {
					output.Usage.Output = *msgDelta.Usage.OutputTokens
				}
				if msgDelta.Usage.CacheReadInputTokens != nil {
					output.Usage.CacheRead = *msgDelta.Usage.CacheReadInputTokens
				}
				if msgDelta.Usage.CacheCreationInputTokens != nil {
					output.Usage.CacheWrite = *msgDelta.Usage.CacheCreationInputTokens
				}
				output.Usage.TotalTokens = output.Usage.Input + output.Usage.Output + output.Usage.CacheRead + output.Usage.CacheWrite
				CalculateCost(model, &output.Usage)

			case "message_stop":
				// Stream complete
			}

			// Check for abort
			if options.Signal != nil && options.Signal.Aborted() {
				output.StopReason = StopReasonAborted
				output.ErrorMessage = "Request was aborted"
				stream.Push(AssistantMessageEvent{Type: "error", Reason: StopReasonAborted, ErrorMessage: output})
				stream.End()
				return
			}
		}

		if output.StopReason == StopReasonError || output.StopReason == StopReasonAborted {
			stream.Push(AssistantMessageEvent{Type: "error", Reason: output.StopReason, ErrorMessage: output})
		} else {
			stream.Push(AssistantMessageEvent{Type: "done", Reason: output.StopReason, FinalMessage: output})
		}
		stream.End()
	}()

	return stream
}

// StreamSimpleAnthropic creates a streaming call with simplified options.
func StreamSimpleAnthropic(model *Model, ctx *Context, options *SimpleStreamOptions) *AssistantMessageEventStream {
	apiKey := ""
	if options != nil {
		apiKey = options.ApiKey
	}
	if apiKey == "" {
		apiKey = GetEnvApiKey(model.Provider)
	}

	opts := &AnthropicOptions{}
	if options != nil {
		opts.StreamOptions = options.StreamOptions
	}
	opts.ApiKey = apiKey

	maxTokens := 32000
	if model.MaxTokens > 0 && model.MaxTokens < maxTokens {
		maxTokens = model.MaxTokens
	}
	if options != nil && options.MaxTokens != nil {
		maxTokens = *options.MaxTokens
	}
	opts.MaxTokens = &maxTokens

	if options != nil && options.Reasoning != "" {
		if supportsAdaptiveThinking(model.ID) {
			opts.ThinkingEnabled = true
			opts.Effort = mapThinkingLevelToEffort(options.Reasoning)
		} else {
			adjusted := adjustMaxTokensForThinking(maxTokens, model.MaxTokens, options.Reasoning, options.ThinkingBudgets)
			opts.MaxTokens = &adjusted.MaxTokens
			opts.ThinkingEnabled = true
			opts.ThinkingBudgetTokens = adjusted.ThinkingBudget
		}
	}

	return StreamAnthropic(model, ctx, opts)
}

func supportsAdaptiveThinking(modelID string) bool {
	return strings.Contains(modelID, "opus-4-6") || strings.Contains(modelID, "opus-4.6")
}

func mapThinkingLevelToEffort(level ThinkingLevel) string {
	switch level {
	case ThinkingMinimal, ThinkingLow:
		return "low"
	case ThinkingMedium:
		return "medium"
	case ThinkingHigh:
		return "high"
	case ThinkingXHigh:
		return "max"
	default:
		return "high"
	}
}

type adjustedTokens struct {
	MaxTokens      int
	ThinkingBudget int
}

func adjustMaxTokensForThinking(baseMaxTokens, modelMaxTokens int, level ThinkingLevel, customBudgets *ThinkingBudgets) adjustedTokens {
	defaultBudgets := map[ThinkingLevel]int{
		ThinkingMinimal: 1024,
		ThinkingLow:     2048,
		ThinkingMedium:  8192,
		ThinkingHigh:    16384,
	}

	// Apply custom budgets
	if customBudgets != nil {
		if customBudgets.Minimal != nil {
			defaultBudgets[ThinkingMinimal] = *customBudgets.Minimal
		}
		if customBudgets.Low != nil {
			defaultBudgets[ThinkingLow] = *customBudgets.Low
		}
		if customBudgets.Medium != nil {
			defaultBudgets[ThinkingMedium] = *customBudgets.Medium
		}
		if customBudgets.High != nil {
			defaultBudgets[ThinkingHigh] = *customBudgets.High
		}
	}

	clampedLevel := level
	if clampedLevel == ThinkingXHigh {
		clampedLevel = ThinkingHigh
	}

	thinkingBudget := defaultBudgets[clampedLevel]
	maxTokens := baseMaxTokens + thinkingBudget
	if maxTokens > modelMaxTokens {
		maxTokens = modelMaxTokens
	}

	minOutputTokens := 1024
	if maxTokens <= thinkingBudget {
		thinkingBudget = maxTokens - minOutputTokens
		if thinkingBudget < 0 {
			thinkingBudget = 0
		}
	}

	return adjustedTokens{MaxTokens: maxTokens, ThinkingBudget: thinkingBudget}
}

func mapAnthropicStopReason(reason string) StopReason {
	switch reason {
	case "end_turn", "pause_turn", "stop_sequence":
		return StopReasonStop
	case "max_tokens":
		return StopReasonLength
	case "tool_use":
		return StopReasonToolUse
	case "refusal", "sensitive":
		return StopReasonError
	default:
		return StopReasonError
	}
}

func buildAnthropicParams(model *Model, ctx *Context, options *AnthropicOptions) map[string]any {
	params := map[string]any{
		"model":  model.ID,
		"stream": true,
	}

	maxTokens := 0
	if options.MaxTokens != nil {
		maxTokens = *options.MaxTokens
	}
	if maxTokens == 0 {
		maxTokens = model.MaxTokens / 3
	}
	params["max_tokens"] = maxTokens

	// System prompt
	if ctx.SystemPrompt != "" {
		params["system"] = []map[string]any{
			{"type": "text", "text": ctx.SystemPrompt},
		}
	}

	// Temperature
	if options.Temperature != nil {
		params["temperature"] = *options.Temperature
	}

	// Messages
	params["messages"] = convertAnthropicMessages(ctx.Messages, model)

	// Tools
	if len(ctx.Tools) > 0 {
		params["tools"] = convertAnthropicTools(ctx.Tools)
	}

	// Thinking
	if options.ThinkingEnabled && model.Reasoning {
		if supportsAdaptiveThinking(model.ID) {
			params["thinking"] = map[string]any{"type": "adaptive"}
			if options.Effort != "" {
				params["output_config"] = map[string]any{"effort": options.Effort}
			}
		} else {
			budget := options.ThinkingBudgetTokens
			if budget == 0 {
				budget = 1024
			}
			params["thinking"] = map[string]any{
				"type":         "enabled",
				"budget_tokens": budget,
			}
		}
	}

	return params
}

func convertAnthropicMessages(messages []Message, model *Model) []map[string]any {
	var result []map[string]any

	for i := 0; i < len(messages); i++ {
		msg := &messages[i]

		if msg.User != nil {
			content := convertUserContent(msg.User)
			if content != nil {
				result = append(result, map[string]any{
					"role":    "user",
					"content": content,
				})
			}
		} else if msg.Assistant != nil {
			blocks := convertAssistantContent(msg.Assistant)
			if len(blocks) > 0 {
				result = append(result, map[string]any{
					"role":    "assistant",
					"content": blocks,
				})
			}
		} else if msg.ToolResult != nil {
			// Collect consecutive tool results
			toolResults := []map[string]any{
				{
					"type":        "tool_result",
					"tool_use_id": msg.ToolResult.ToolCallID,
					"content":     convertToolResultContent(msg.ToolResult.Content),
					"is_error":    msg.ToolResult.IsError,
				},
			}

			// Look ahead for consecutive tool results
			for i+1 < len(messages) && messages[i+1].ToolResult != nil {
				i++
				tr := messages[i].ToolResult
				toolResults = append(toolResults, map[string]any{
					"type":        "tool_result",
					"tool_use_id": tr.ToolCallID,
					"content":     convertToolResultContent(tr.Content),
					"is_error":    tr.IsError,
				})
			}

			result = append(result, map[string]any{
				"role":    "user",
				"content": toolResults,
			})
		}
	}

	return result
}

func convertUserContent(msg *UserMessage) any {
	if msg.Content.IsText() {
		text := strings.TrimSpace(msg.Content.Text)
		if text == "" {
			return nil
		}
		return text
	}

	var blocks []map[string]any
	for _, block := range msg.Content.Blocks {
		if block.Text != nil {
			text := strings.TrimSpace(block.Text.Text)
			if text == "" {
				continue
			}
			blocks = append(blocks, map[string]any{
				"type": "text",
				"text": text,
			})
		} else if block.Image != nil {
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": block.Image.MimeType,
					"data":       block.Image.Data,
				},
			})
		}
	}
	if len(blocks) == 0 {
		return nil
	}
	return blocks
}

func convertAssistantContent(msg *AssistantMessage) []map[string]any {
	var blocks []map[string]any
	for _, block := range msg.Content {
		if block.Text != nil {
			text := strings.TrimSpace(block.Text.Text)
			if text == "" {
				continue
			}
			blocks = append(blocks, map[string]any{
				"type": "text",
				"text": text,
			})
		} else if block.Thinking != nil {
			thinking := strings.TrimSpace(block.Thinking.Thinking)
			if thinking == "" {
				continue
			}
			if block.Thinking.ThinkingSignature == "" {
				// Convert to text if no signature
				blocks = append(blocks, map[string]any{
					"type": "text",
					"text": thinking,
				})
			} else {
				blocks = append(blocks, map[string]any{
					"type":      "thinking",
					"thinking":  thinking,
					"signature": block.Thinking.ThinkingSignature,
				})
			}
		} else if block.ToolCall != nil {
			args := block.ToolCall.Arguments
			if args == nil {
				args = map[string]any{}
			}
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    block.ToolCall.ID,
				"name":  block.ToolCall.Name,
				"input": args,
			})
		}
	}
	return blocks
}

func convertToolResultContent(content []ToolResultContentBlock) any {
	var texts []string
	hasImages := false
	for _, c := range content {
		if c.Image != nil {
			hasImages = true
			break
		}
	}

	if !hasImages {
		for _, c := range content {
			if c.Text != nil {
				texts = append(texts, c.Text.Text)
			}
		}
		return strings.Join(texts, "\n")
	}

	var blocks []map[string]any
	for _, c := range content {
		if c.Text != nil {
			blocks = append(blocks, map[string]any{
				"type": "text",
				"text": c.Text.Text,
			})
		} else if c.Image != nil {
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": c.Image.MimeType,
					"data":       c.Image.Data,
				},
			})
		}
	}
	return blocks
}

func convertAnthropicTools(tools []Tool) []map[string]any {
	var result []map[string]any
	for _, tool := range tools {
		t := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"input_schema": map[string]any{
				"type":       "object",
				"properties": tool.Parameters["properties"],
				"required":   tool.Parameters["required"],
			},
		}
		result = append(result, t)
	}
	return result
}
