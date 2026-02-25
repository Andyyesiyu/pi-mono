package ai

import "testing"

func TestParseStreamingJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]any
	}{
		{
			name:     "empty string",
			input:    "",
			expected: map[string]any{},
		},
		{
			name:  "complete JSON",
			input: `{"name": "test", "value": 42}`,
			expected: map[string]any{
				"name":  "test",
				"value": float64(42),
			},
		},
		{
			name:  "partial JSON - missing closing brace",
			input: `{"name": "test"`,
			expected: map[string]any{
				"name": "test",
			},
		},
		{
			name:  "partial JSON - incomplete string",
			input: `{"name": "te`,
			expected: map[string]any{
				"name": "te",
			},
		},
		{
			name:     "just opening brace",
			input:    `{`,
			expected: map[string]any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseStreamingJSON(tt.input)
			for k, v := range tt.expected {
				got, ok := result[k]
				if !ok {
					t.Errorf("missing key %q", k)
					continue
				}
				if got != v {
					t.Errorf("key %q: expected %v, got %v", k, v, got)
				}
			}
		})
	}
}

func TestRepairJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "complete JSON",
			input:    `{"a": 1}`,
			expected: `{"a": 1}`,
		},
		{
			name:     "missing closing brace",
			input:    `{"a": 1`,
			expected: `{"a": 1}`,
		},
		{
			name:     "missing closing bracket",
			input:    `[1, 2`,
			expected: `[1, 2]`,
		},
		{
			name:     "unclosed string",
			input:    `{"a": "hello`,
			expected: `{"a": "hello"}`,
		},
		{
			name:     "nested unclosed",
			input:    `{"a": {"b": 1`,
			expected: `{"a": {"b": 1}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := repairJSON(tt.input)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestValidation(t *testing.T) {
	tool := Tool{
		Name:        "read",
		Description: "Read a file",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []any{"path"},
		},
	}

	// Valid call
	tc := &ToolCall{Name: "read", Arguments: map[string]any{"path": "/tmp/test"}}
	args, err := ValidateToolArguments(&tool, tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if args["path"] != "/tmp/test" {
		t.Errorf("expected path '/tmp/test', got %v", args["path"])
	}

	// Missing required field
	tc2 := &ToolCall{Name: "read", Arguments: map[string]any{}}
	_, err = ValidateToolArguments(&tool, tc2)
	if err == nil {
		t.Fatal("expected error for missing required field")
	}

	// Tool not found
	_, err = ValidateToolCall([]Tool{tool}, &ToolCall{Name: "write", Arguments: map[string]any{}})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}
