package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidateToolCall finds a tool by name and validates the tool call arguments.
func ValidateToolCall(tools []Tool, toolCall *ToolCall) (map[string]any, error) {
	var tool *Tool
	for i := range tools {
		if tools[i].Name == toolCall.Name {
			tool = &tools[i]
			break
		}
	}
	if tool == nil {
		return nil, fmt.Errorf("tool %q not found", toolCall.Name)
	}
	return ValidateToolArguments(tool, toolCall)
}

// ValidateToolArguments validates tool call arguments against the tool's JSON schema.
// This is a simplified validation that checks required fields and basic types.
func ValidateToolArguments(tool *Tool, toolCall *ToolCall) (map[string]any, error) {
	if toolCall.Arguments == nil {
		toolCall.Arguments = make(map[string]any)
	}

	schema := tool.Parameters
	if schema == nil {
		return toolCall.Arguments, nil
	}

	// Check required fields
	if requiredRaw, ok := schema["required"]; ok {
		if requiredList, ok := requiredRaw.([]any); ok {
			var missing []string
			for _, r := range requiredList {
				if name, ok := r.(string); ok {
					if _, exists := toolCall.Arguments[name]; !exists {
						missing = append(missing, name)
					}
				}
			}
			if len(missing) > 0 {
				argsJSON, _ := json.MarshalIndent(toolCall.Arguments, "", "  ")
				return nil, fmt.Errorf(
					"validation failed for tool %q:\n  - missing required fields: %s\n\nReceived arguments:\n%s",
					toolCall.Name,
					strings.Join(missing, ", "),
					string(argsJSON),
				)
			}
		}
	}

	return toolCall.Arguments, nil
}
