package ai

import "encoding/json"

// ParseStreamingJSON attempts to parse potentially incomplete JSON during streaming.
// Returns a valid map even if the JSON is incomplete.
func ParseStreamingJSON(partialJSON string) map[string]any {
	if partialJSON == "" {
		return map[string]any{}
	}

	// Try standard parsing first
	var result map[string]any
	if err := json.Unmarshal([]byte(partialJSON), &result); err == nil {
		return result
	}

	// Try to repair incomplete JSON by closing brackets
	repaired := repairJSON(partialJSON)
	if err := json.Unmarshal([]byte(repaired), &result); err == nil {
		return result
	}

	return map[string]any{}
}

// repairJSON attempts to close unclosed brackets and braces in partial JSON.
func repairJSON(s string) string {
	// Track unclosed brackets/braces
	var stack []byte
	inString := false
	escaped := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inString {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}

	// If we're in a string, close it
	if inString {
		s += `"`
	}

	// Close any unclosed brackets/braces
	for i := len(stack) - 1; i >= 0; i-- {
		s += string(stack[i])
	}

	return s
}
