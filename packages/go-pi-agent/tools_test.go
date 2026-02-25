package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTool(t *testing.T) {
	// Create a temp file
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadTool()

	t.Run("read entire file", func(t *testing.T) {
		result, err := tool.Execute("1", map[string]any{"file_path": path}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(result)
		if !strings.Contains(text, "line 1") {
			t.Errorf("expected line 1 in output, got: %s", text)
		}
		if !strings.Contains(text, "line 5") {
			t.Errorf("expected line 5 in output, got: %s", text)
		}
		// Check line numbers
		if !strings.Contains(text, "     1\t") {
			t.Errorf("expected line numbers in output, got: %s", text)
		}
	})

	t.Run("read with offset", func(t *testing.T) {
		result, err := tool.Execute("2", map[string]any{"file_path": path, "offset": float64(3)}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(result)
		if strings.Contains(text, "line 1") {
			t.Errorf("should not contain line 1, got: %s", text)
		}
		if !strings.Contains(text, "line 3") {
			t.Errorf("expected line 3 in output, got: %s", text)
		}
	})

	t.Run("read with limit", func(t *testing.T) {
		result, err := tool.Execute("3", map[string]any{"file_path": path, "limit": float64(2)}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(result)
		if !strings.Contains(text, "line 1") {
			t.Errorf("expected line 1, got: %s", text)
		}
		if !strings.Contains(text, "line 2") {
			t.Errorf("expected line 2, got: %s", text)
		}
		if strings.Contains(text, "line 3") {
			t.Errorf("should not contain line 3, got: %s", text)
		}
	})

	t.Run("read nonexistent file", func(t *testing.T) {
		result, err := tool.Execute("4", map[string]any{"file_path": "/nonexistent/file.txt"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(result)
		if !strings.Contains(text, "Error reading file") {
			t.Errorf("expected error message, got: %s", text)
		}
	})
}

func TestWriteTool(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool()

	t.Run("write new file", func(t *testing.T) {
		path := filepath.Join(dir, "new.txt")
		result, err := tool.Execute("1", map[string]any{
			"file_path": path,
			"content":   "hello world\n",
		}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(result)
		if !strings.Contains(text, "Wrote") {
			t.Errorf("expected success message, got: %s", text)
		}

		data, _ := os.ReadFile(path)
		if string(data) != "hello world\n" {
			t.Errorf("file content mismatch: %s", string(data))
		}
	})

	t.Run("create parent directories", func(t *testing.T) {
		path := filepath.Join(dir, "a", "b", "c", "deep.txt")
		result, err := tool.Execute("2", map[string]any{
			"file_path": path,
			"content":   "deep",
		}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(result)
		if !strings.Contains(text, "Wrote") {
			t.Errorf("expected success, got: %s", text)
		}

		data, _ := os.ReadFile(path)
		if string(data) != "deep" {
			t.Errorf("file content mismatch: %s", string(data))
		}
	})
}

func TestEditTool(t *testing.T) {
	dir := t.TempDir()
	tool := NewEditTool()

	t.Run("replace single occurrence", func(t *testing.T) {
		path := filepath.Join(dir, "edit.txt")
		os.WriteFile(path, []byte("foo bar baz"), 0644)

		result, err := tool.Execute("1", map[string]any{
			"file_path":  path,
			"old_string": "bar",
			"new_string": "qux",
		}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(result)
		if !strings.Contains(text, "Replaced 1") {
			t.Errorf("expected replaced message, got: %s", text)
		}

		data, _ := os.ReadFile(path)
		if string(data) != "foo qux baz" {
			t.Errorf("unexpected content: %s", string(data))
		}
	})

	t.Run("reject ambiguous match", func(t *testing.T) {
		path := filepath.Join(dir, "ambig.txt")
		os.WriteFile(path, []byte("aaa bbb aaa"), 0644)

		result, err := tool.Execute("2", map[string]any{
			"file_path":  path,
			"old_string": "aaa",
			"new_string": "ccc",
		}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(result)
		if !strings.Contains(text, "found 2 times") {
			t.Errorf("expected ambiguity error, got: %s", text)
		}
	})

	t.Run("replace all occurrences", func(t *testing.T) {
		path := filepath.Join(dir, "all.txt")
		os.WriteFile(path, []byte("aaa bbb aaa"), 0644)

		result, err := tool.Execute("3", map[string]any{
			"file_path":   path,
			"old_string":  "aaa",
			"new_string":  "ccc",
			"replace_all": true,
		}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(result)
		if !strings.Contains(text, "Replaced 2") {
			t.Errorf("expected 2 replacements, got: %s", text)
		}

		data, _ := os.ReadFile(path)
		if string(data) != "ccc bbb ccc" {
			t.Errorf("unexpected content: %s", string(data))
		}
	})

	t.Run("string not found", func(t *testing.T) {
		path := filepath.Join(dir, "notfound.txt")
		os.WriteFile(path, []byte("hello"), 0644)

		result, err := tool.Execute("4", map[string]any{
			"file_path":  path,
			"old_string": "missing",
			"new_string": "replacement",
		}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(result)
		if !strings.Contains(text, "not found") {
			t.Errorf("expected not found error, got: %s", text)
		}
	})
}

func TestBashTool(t *testing.T) {
	tool := NewBashTool()

	t.Run("simple command", func(t *testing.T) {
		result, err := tool.Execute("1", map[string]any{"command": "echo hello"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(result)
		if !strings.Contains(text, "hello") {
			t.Errorf("expected hello, got: %s", text)
		}
	})

	t.Run("command with error", func(t *testing.T) {
		result, err := tool.Execute("2", map[string]any{"command": "exit 1"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(result)
		if !strings.Contains(text, "Exit code: 1") {
			t.Errorf("expected exit code 1, got: %s", text)
		}
	})

	t.Run("working directory", func(t *testing.T) {
		dir := t.TempDir()
		dirTool := NewBashToolWithDir(dir)
		result, err := dirTool.Execute("3", map[string]any{"command": "pwd"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(result)
		if !strings.Contains(text, dir) {
			t.Errorf("expected %s in output, got: %s", dir, text)
		}
	})
}

func TestCoreTools(t *testing.T) {
	tools := CoreTools()
	if len(tools) != 4 {
		t.Errorf("expected 4 core tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}

	for _, expected := range []string{"Read", "Write", "Edit", "Bash"} {
		if !names[expected] {
			t.Errorf("missing tool: %s", expected)
		}
	}
}

func resultText(r *AgentToolResult) string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	if r.Content[0].Text != nil {
		return r.Content[0].Text.Text
	}
	return ""
}
