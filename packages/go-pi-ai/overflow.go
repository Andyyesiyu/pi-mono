package ai

import "regexp"

// Overflow patterns to detect context overflow errors from different providers.
var overflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)prompt is too long`),
	regexp.MustCompile(`(?i)input is too long for requested model`),
	regexp.MustCompile(`(?i)exceeds the context window`),
	regexp.MustCompile(`(?i)input token count.*exceeds the maximum`),
	regexp.MustCompile(`(?i)maximum prompt length is \d+`),
	regexp.MustCompile(`(?i)reduce the length of the messages`),
	regexp.MustCompile(`(?i)maximum context length is \d+ tokens`),
	regexp.MustCompile(`(?i)exceeds the limit of \d+`),
	regexp.MustCompile(`(?i)exceeds the available context size`),
	regexp.MustCompile(`(?i)greater than the context length`),
	regexp.MustCompile(`(?i)context window exceeds limit`),
	regexp.MustCompile(`(?i)exceeded model token limit`),
	regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),
	regexp.MustCompile(`(?i)too many tokens`),
	regexp.MustCompile(`(?i)token limit exceeded`),
}

var noBodyPattern = regexp.MustCompile(`(?i)^4(00|13)\s*(status code)?\s*\(no body\)`)

// IsContextOverflow checks if an assistant message represents a context overflow error.
func IsContextOverflow(message *AssistantMessage, contextWindow int) bool {
	// Case 1: Check error message patterns
	if message.StopReason == StopReasonError && message.ErrorMessage != "" {
		for _, p := range overflowPatterns {
			if p.MatchString(message.ErrorMessage) {
				return true
			}
		}
		if noBodyPattern.MatchString(message.ErrorMessage) {
			return true
		}
	}

	// Case 2: Silent overflow - successful but usage exceeds context
	if contextWindow > 0 && message.StopReason == StopReasonStop {
		inputTokens := message.Usage.Input + message.Usage.CacheRead
		if inputTokens > contextWindow {
			return true
		}
	}

	return false
}

// GetOverflowPatterns returns the overflow patterns for testing purposes.
func GetOverflowPatterns() []*regexp.Regexp {
	result := make([]*regexp.Regexp, len(overflowPatterns))
	copy(result, overflowPatterns)
	return result
}
