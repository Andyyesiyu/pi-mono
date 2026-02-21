package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

// ProxyAssistantMessageEvent is a streaming event from the proxy server.
type ProxyAssistantMessageEvent struct {
	Type             string         `json:"type"`
	ContentIndex     int            `json:"contentIndex,omitempty"`
	Delta            string         `json:"delta,omitempty"`
	ContentSignature string         `json:"contentSignature,omitempty"`
	ID               string         `json:"id,omitempty"`
	ToolName         string         `json:"toolName,omitempty"`
	Reason           ai.StopReason  `json:"reason,omitempty"`
	ErrorMessage     string         `json:"errorMessage,omitempty"`
	Usage            *ai.Usage      `json:"usage,omitempty"`
}

// ProxyStreamOptions extends SimpleStreamOptions with proxy settings.
type ProxyStreamOptions struct {
	ai.SimpleStreamOptions
	AuthToken string `json:"authToken"`
	ProxyURL  string `json:"proxyUrl"`
}

// StreamProxy creates a streaming call through a proxy server.
func StreamProxy(model *ai.Model, ctx *ai.Context, options *ProxyStreamOptions) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()

	go func() {
		partial := ai.NewAssistantMessage(model.ApiType, model.Provider, model.ID, time.Now().UnixMilli())

		payload := map[string]any{
			"model":   model,
			"context": ctx,
			"options": map[string]any{
				"temperature": options.Temperature,
				"maxTokens":   options.MaxTokens,
				"reasoning":   options.Reasoning,
			},
		}
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			partial.StopReason = ai.StopReasonError
			partial.ErrorMessage = err.Error()
			stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: ai.StopReasonError, ErrorMessage: partial})
			stream.End()
			return
		}

		req, err := http.NewRequest("POST", options.ProxyURL+"/api/stream", bytes.NewReader(payloadBytes))
		if err != nil {
			partial.StopReason = ai.StopReasonError
			partial.ErrorMessage = err.Error()
			stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: ai.StopReasonError, ErrorMessage: partial})
			stream.End()
			return
		}

		req.Header.Set("Authorization", "Bearer "+options.AuthToken)
		req.Header.Set("Content-Type", "application/json")

		if options.Signal != nil {
			req = req.WithContext(contextFromAbortSignalCtx(options.Signal))
		}

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			reason := ai.StopReasonError
			if options.Signal != nil && options.Signal.Aborted() {
				reason = ai.StopReasonAborted
			}
			partial.StopReason = reason
			partial.ErrorMessage = err.Error()
			stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: reason, ErrorMessage: partial})
			stream.End()
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			errMsg := fmt.Sprintf("Proxy error: %d %s", resp.StatusCode, resp.Status)
			var errData struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(body, &errData) == nil && errData.Error != "" {
				errMsg = fmt.Sprintf("Proxy error: %s", errData.Error)
			}
			partial.StopReason = ai.StopReasonError
			partial.ErrorMessage = errMsg
			stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: ai.StopReasonError, ErrorMessage: partial})
			stream.End()
			return
		}

		// Track partial JSON for tool calls
		partialJSONs := make(map[int]string)

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "" {
				continue
			}

			if options.Signal != nil && options.Signal.Aborted() {
				partial.StopReason = ai.StopReasonAborted
				partial.ErrorMessage = "Request aborted by user"
				stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: ai.StopReasonAborted, ErrorMessage: partial})
				stream.End()
				return
			}

			var proxyEvent ProxyAssistantMessageEvent
			if err := json.Unmarshal([]byte(data), &proxyEvent); err != nil {
				continue
			}

			event := processProxyEvent(&proxyEvent, partial, partialJSONs)
			if event != nil {
				stream.Push(*event)
			}
		}

		stream.End()
	}()

	return stream
}

func processProxyEvent(
	proxyEvent *ProxyAssistantMessageEvent,
	partial *ai.AssistantMessage,
	partialJSONs map[int]string,
) *ai.AssistantMessageEvent {
	switch proxyEvent.Type {
	case "start":
		return &ai.AssistantMessageEvent{Type: "start", Partial: partial}

	case "text_start":
		// Ensure content array is large enough
		for len(partial.Content) <= proxyEvent.ContentIndex {
			partial.Content = append(partial.Content, ai.ContentBlock{})
		}
		partial.Content[proxyEvent.ContentIndex] = ai.ContentBlock{
			Text: &ai.TextContent{Type: "text", Text: ""},
		}
		return &ai.AssistantMessageEvent{Type: "text_start", ContentIndex: proxyEvent.ContentIndex, Partial: partial}

	case "text_delta":
		if proxyEvent.ContentIndex < len(partial.Content) && partial.Content[proxyEvent.ContentIndex].Text != nil {
			partial.Content[proxyEvent.ContentIndex].Text.Text += proxyEvent.Delta
			return &ai.AssistantMessageEvent{
				Type: "text_delta", ContentIndex: proxyEvent.ContentIndex,
				Delta: proxyEvent.Delta, Partial: partial,
			}
		}

	case "text_end":
		if proxyEvent.ContentIndex < len(partial.Content) && partial.Content[proxyEvent.ContentIndex].Text != nil {
			partial.Content[proxyEvent.ContentIndex].Text.TextSignature = proxyEvent.ContentSignature
			return &ai.AssistantMessageEvent{
				Type: "text_end", ContentIndex: proxyEvent.ContentIndex,
				Content: partial.Content[proxyEvent.ContentIndex].Text.Text, Partial: partial,
			}
		}

	case "thinking_start":
		for len(partial.Content) <= proxyEvent.ContentIndex {
			partial.Content = append(partial.Content, ai.ContentBlock{})
		}
		partial.Content[proxyEvent.ContentIndex] = ai.ContentBlock{
			Thinking: &ai.ThinkingContent{Type: "thinking", Thinking: ""},
		}
		return &ai.AssistantMessageEvent{Type: "thinking_start", ContentIndex: proxyEvent.ContentIndex, Partial: partial}

	case "thinking_delta":
		if proxyEvent.ContentIndex < len(partial.Content) && partial.Content[proxyEvent.ContentIndex].Thinking != nil {
			partial.Content[proxyEvent.ContentIndex].Thinking.Thinking += proxyEvent.Delta
			return &ai.AssistantMessageEvent{
				Type: "thinking_delta", ContentIndex: proxyEvent.ContentIndex,
				Delta: proxyEvent.Delta, Partial: partial,
			}
		}

	case "thinking_end":
		if proxyEvent.ContentIndex < len(partial.Content) && partial.Content[proxyEvent.ContentIndex].Thinking != nil {
			partial.Content[proxyEvent.ContentIndex].Thinking.ThinkingSignature = proxyEvent.ContentSignature
			return &ai.AssistantMessageEvent{
				Type: "thinking_end", ContentIndex: proxyEvent.ContentIndex,
				Content: partial.Content[proxyEvent.ContentIndex].Thinking.Thinking, Partial: partial,
			}
		}

	case "toolcall_start":
		for len(partial.Content) <= proxyEvent.ContentIndex {
			partial.Content = append(partial.Content, ai.ContentBlock{})
		}
		partial.Content[proxyEvent.ContentIndex] = ai.ContentBlock{
			ToolCall: &ai.ToolCall{
				Type:      "toolCall",
				ID:        proxyEvent.ID,
				Name:      proxyEvent.ToolName,
				Arguments: map[string]any{},
			},
		}
		partialJSONs[proxyEvent.ContentIndex] = ""
		return &ai.AssistantMessageEvent{Type: "toolcall_start", ContentIndex: proxyEvent.ContentIndex, Partial: partial}

	case "toolcall_delta":
		if proxyEvent.ContentIndex < len(partial.Content) && partial.Content[proxyEvent.ContentIndex].ToolCall != nil {
			partialJSONs[proxyEvent.ContentIndex] += proxyEvent.Delta
			partial.Content[proxyEvent.ContentIndex].ToolCall.Arguments = ai.ParseStreamingJSON(partialJSONs[proxyEvent.ContentIndex])
			return &ai.AssistantMessageEvent{
				Type: "toolcall_delta", ContentIndex: proxyEvent.ContentIndex,
				Delta: proxyEvent.Delta, Partial: partial,
			}
		}

	case "toolcall_end":
		if proxyEvent.ContentIndex < len(partial.Content) && partial.Content[proxyEvent.ContentIndex].ToolCall != nil {
			delete(partialJSONs, proxyEvent.ContentIndex)
			return &ai.AssistantMessageEvent{
				Type: "toolcall_end", ContentIndex: proxyEvent.ContentIndex,
				ToolCallData: partial.Content[proxyEvent.ContentIndex].ToolCall, Partial: partial,
			}
		}

	case "done":
		partial.StopReason = proxyEvent.Reason
		if proxyEvent.Usage != nil {
			partial.Usage = *proxyEvent.Usage
		}
		return &ai.AssistantMessageEvent{Type: "done", Reason: proxyEvent.Reason, FinalMessage: partial}

	case "error":
		partial.StopReason = proxyEvent.Reason
		partial.ErrorMessage = proxyEvent.ErrorMessage
		if proxyEvent.Usage != nil {
			partial.Usage = *proxyEvent.Usage
		}
		return &ai.AssistantMessageEvent{Type: "error", Reason: proxyEvent.Reason, ErrorMessage: partial}
	}

	return nil
}

// contextFromAbortSignalCtx creates a context.Context from an AbortSignal for HTTP requests.
func contextFromAbortSignalCtx(signal *ai.AbortSignal) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-signal.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx
}
