package ai

import "testing"

func TestIsContextOverflow(t *testing.T) {
	tests := []struct {
		name          string
		message       *AssistantMessage
		contextWindow int
		expected      bool
	}{
		{
			name: "Anthropic overflow",
			message: &AssistantMessage{
				StopReason:   StopReasonError,
				ErrorMessage: "prompt is too long: 213462 tokens > 200000 maximum",
			},
			expected: true,
		},
		{
			name: "OpenAI overflow",
			message: &AssistantMessage{
				StopReason:   StopReasonError,
				ErrorMessage: "Your input exceeds the context window of this model",
			},
			expected: true,
		},
		{
			name: "Google overflow",
			message: &AssistantMessage{
				StopReason:   StopReasonError,
				ErrorMessage: "The input token count (1196265) exceeds the maximum number of tokens allowed (1048575)",
			},
			expected: true,
		},
		{
			name: "Cerebras no body",
			message: &AssistantMessage{
				StopReason:   StopReasonError,
				ErrorMessage: "413 status code (no body)",
			},
			expected: true,
		},
		{
			name: "Silent overflow via usage",
			message: &AssistantMessage{
				StopReason: StopReasonStop,
				Usage:      Usage{Input: 150000, CacheRead: 10000},
			},
			contextWindow: 100000,
			expected:      true,
		},
		{
			name: "Not an overflow",
			message: &AssistantMessage{
				StopReason:   StopReasonError,
				ErrorMessage: "rate limit exceeded",
			},
			expected: false,
		},
		{
			name: "Normal completion",
			message: &AssistantMessage{
				StopReason: StopReasonStop,
				Usage:      Usage{Input: 1000},
			},
			contextWindow: 100000,
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsContextOverflow(tt.message, tt.contextWindow)
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}
