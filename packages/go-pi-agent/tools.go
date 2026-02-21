package agent

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

// NewReadTool creates the Read tool that reads file contents.
func NewReadTool() *AgentTool {
	return &AgentTool{
		Tool: ai.Tool{
			Name:        "Read",
			Description: "Read a file from the filesystem. Returns the file contents with line numbers.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "Absolute or relative path to the file to read.",
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": "Line number to start reading from (1-based). Defaults to 1.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of lines to read. Defaults to 0 (read all).",
					},
				},
				"required": []string{"file_path"},
			},
		},
		Label: "Read",
		Execute: func(toolCallID string, params map[string]any, signal *ai.AbortSignal, onUpdate AgentToolUpdateCallback) (*AgentToolResult, error) {
			filePath, _ := params["file_path"].(string)
			if filePath == "" {
				return toolError("file_path is required"), nil
			}

			data, err := os.ReadFile(filePath)
			if err != nil {
				return toolError(fmt.Sprintf("Error reading file: %s", err)), nil
			}

			lines := strings.Split(string(data), "\n")

			offset := 1
			if v, ok := params["offset"]; ok {
				offset = toInt(v)
			}
			if offset < 1 {
				offset = 1
			}

			limit := 0
			if v, ok := params["limit"]; ok {
				limit = toInt(v)
			}

			// Apply offset and limit
			startIdx := offset - 1
			if startIdx >= len(lines) {
				return toolText(""), nil
			}

			endIdx := len(lines)
			if limit > 0 && startIdx+limit < endIdx {
				endIdx = startIdx + limit
			}

			var buf strings.Builder
			for i := startIdx; i < endIdx; i++ {
				fmt.Fprintf(&buf, "%6d\t%s\n", i+1, lines[i])
			}

			return toolText(buf.String()), nil
		},
	}
}

// NewWriteTool creates the Write tool that writes file contents.
func NewWriteTool() *AgentTool {
	return &AgentTool{
		Tool: ai.Tool{
			Name:        "Write",
			Description: "Write content to a file. Creates the file and any parent directories if they don't exist. Overwrites existing files.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "Path to the file to write.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The content to write to the file.",
					},
				},
				"required": []string{"file_path", "content"},
			},
		},
		Label: "Write",
		Execute: func(toolCallID string, params map[string]any, signal *ai.AbortSignal, onUpdate AgentToolUpdateCallback) (*AgentToolResult, error) {
			filePath, _ := params["file_path"].(string)
			if filePath == "" {
				return toolError("file_path is required"), nil
			}
			content, _ := params["content"].(string)

			dir := filepath.Dir(filePath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return toolError(fmt.Sprintf("Error creating directories: %s", err)), nil
			}

			if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
				return toolError(fmt.Sprintf("Error writing file: %s", err)), nil
			}

			lineCount := strings.Count(content, "\n") + 1
			if content == "" {
				lineCount = 0
			}
			return toolText(fmt.Sprintf("Wrote %d bytes (%d lines) to %s", len(content), lineCount, filePath)), nil
		},
	}
}

// NewEditTool creates the Edit tool that performs search-and-replace edits.
func NewEditTool() *AgentTool {
	return &AgentTool{
		Tool: ai.Tool{
			Name:        "Edit",
			Description: "Edit a file by replacing an exact string match with new content. The old_string must match exactly one location in the file.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": "Path to the file to edit.",
					},
					"old_string": map[string]any{
						"type":        "string",
						"description": "The exact string to find and replace. Must be unique in the file.",
					},
					"new_string": map[string]any{
						"type":        "string",
						"description": "The replacement string.",
					},
					"replace_all": map[string]any{
						"type":        "boolean",
						"description": "If true, replace all occurrences. Defaults to false.",
					},
				},
				"required": []string{"file_path", "old_string", "new_string"},
			},
		},
		Label: "Edit",
		Execute: func(toolCallID string, params map[string]any, signal *ai.AbortSignal, onUpdate AgentToolUpdateCallback) (*AgentToolResult, error) {
			filePath, _ := params["file_path"].(string)
			if filePath == "" {
				return toolError("file_path is required"), nil
			}

			oldString, _ := params["old_string"].(string)
			newString, _ := params["new_string"].(string)
			replaceAll, _ := params["replace_all"].(bool)

			data, err := os.ReadFile(filePath)
			if err != nil {
				return toolError(fmt.Sprintf("Error reading file: %s", err)), nil
			}

			content := string(data)
			count := strings.Count(content, oldString)

			if count == 0 {
				return toolError("old_string not found in file"), nil
			}

			if count > 1 && !replaceAll {
				return toolError(fmt.Sprintf("old_string found %d times in file. Use replace_all=true or provide a more specific string.", count)), nil
			}

			var result string
			if replaceAll {
				result = strings.ReplaceAll(content, oldString, newString)
			} else {
				result = strings.Replace(content, oldString, newString, 1)
			}

			if err := os.WriteFile(filePath, []byte(result), 0644); err != nil {
				return toolError(fmt.Sprintf("Error writing file: %s", err)), nil
			}

			if replaceAll {
				return toolText(fmt.Sprintf("Replaced %d occurrences in %s", count, filePath)), nil
			}
			return toolText(fmt.Sprintf("Replaced 1 occurrence in %s", filePath)), nil
		},
	}
}

// NewBashTool creates the Bash tool that executes shell commands.
func NewBashTool() *AgentTool {
	return newBashToolWithDir("")
}

// NewBashToolWithDir creates a Bash tool that executes commands in the given directory.
func NewBashToolWithDir(dir string) *AgentTool {
	return newBashToolWithDir(dir)
}

func newBashToolWithDir(dir string) *AgentTool {
	return &AgentTool{
		Tool: ai.Tool{
			Name:        "Bash",
			Description: "Execute a bash command and return its output. Commands run in a shell with bash -c.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The bash command to execute.",
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Timeout in milliseconds. Defaults to 120000 (2 minutes).",
					},
				},
				"required": []string{"command"},
			},
		},
		Label: "Bash",
		Execute: func(toolCallID string, params map[string]any, signal *ai.AbortSignal, onUpdate AgentToolUpdateCallback) (*AgentToolResult, error) {
			command, _ := params["command"].(string)
			if command == "" {
				return toolError("command is required"), nil
			}

			cmd := exec.Command("bash", "-c", command)
			if dir != "" {
				cmd.Dir = dir
			}

			// Combine stdout and stderr
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			// Handle abort signal
			if signal != nil {
				go func() {
					<-signal.Done()
					if cmd.Process != nil {
						cmd.Process.Signal(syscall.SIGTERM)
					}
				}()
			}

			err := cmd.Run()

			var output strings.Builder
			if stdout.Len() > 0 {
				output.WriteString(stdout.String())
			}
			if stderr.Len() > 0 {
				if output.Len() > 0 {
					output.WriteString("\n")
				}
				output.WriteString(stderr.String())
			}

			if err != nil {
				exitCode := -1
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				}
				text := output.String()
				if text == "" {
					text = err.Error()
				}
				return &AgentToolResult{
					Content: []ai.ToolResultContentBlock{
						{Text: &ai.TextContent{Type: "text", Text: fmt.Sprintf("Exit code: %d\n%s", exitCode, text)}},
					},
					Details: map[string]any{"exitCode": exitCode},
				}, nil
			}

			return toolText(output.String()), nil
		},
	}
}

// CoreTools returns all four core Pi tools: Read, Write, Edit, Bash.
func CoreTools() []*AgentTool {
	return []*AgentTool{
		NewReadTool(),
		NewWriteTool(),
		NewEditTool(),
		NewBashTool(),
	}
}

// CoreToolsWithDir returns all four core Pi tools with Bash running in the given directory.
func CoreToolsWithDir(dir string) []*AgentTool {
	return []*AgentTool{
		NewReadTool(),
		NewWriteTool(),
		NewEditTool(),
		NewBashToolWithDir(dir),
	}
}

// toolText creates a successful text result.
func toolText(text string) *AgentToolResult {
	return &AgentToolResult{
		Content: []ai.ToolResultContentBlock{
			{Text: &ai.TextContent{Type: "text", Text: text}},
		},
	}
}

// toolError creates an error text result.
func toolError(text string) *AgentToolResult {
	return &AgentToolResult{
		Content: []ai.ToolResultContentBlock{
			{Text: &ai.TextContent{Type: "text", Text: text}},
		},
	}
}

// toInt converts various numeric types to int.
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case float32:
		return int(n)
	case int64:
		return int(n)
	default:
		return 0
	}
}
