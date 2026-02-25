package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/nickolaev/pi-mono/packages/go-pi-agent/extpb"
	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

// ===== Test gRPC extension subprocess =====
//
// When PI_TEST_GRPC_EXT_MODE is set, the test binary acts as a gRPC
// extension server using extpb.Serve().

func init() {
	if mode := os.Getenv("PI_TEST_GRPC_EXT_MODE"); mode != "" {
		var impl extpb.ExtensionServiceServer
		switch mode {
		case "basic":
			impl = &grpcBasicExt{}
		case "blocker":
			impl = &grpcBlockerExt{}
		case "tool_provider":
			impl = &grpcToolProviderExt{}
		case "context_modifier":
			impl = &grpcContextModifierExt{}
		default:
			fmt.Fprintf(os.Stderr, "unknown gRPC ext mode: %s\n", mode)
			os.Exit(1)
		}
		if err := extpb.Serve(impl); err != nil {
			fmt.Fprintf(os.Stderr, "serve error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

// --- Mock implementations ---

// grpcBasicExt: subscribes to tool_call + before_agent_start hooks.
type grpcBasicExt struct {
	extpb.UnimplementedExtensionServiceServer
}

func (e *grpcBasicExt) Initialize(_ context.Context, _ *extpb.InitializeRequest) (*extpb.InitializeResponse, error) {
	return &extpb.InitializeResponse{
		ProtocolVersion: "1.0",
		Hooks:           []string{"tool_call", "before_agent_start"},
	}, nil
}

func (e *grpcBasicExt) OnToolCall(_ context.Context, _ *extpb.ToolCallHookEvent) (*extpb.ToolCallHookResult, error) {
	return &extpb.ToolCallHookResult{Block: false}, nil
}

func (e *grpcBasicExt) OnBeforeAgentStart(_ context.Context, req *extpb.BeforeAgentStartHookEvent) (*extpb.BeforeAgentStartHookResult, error) {
	return &extpb.BeforeAgentStartHookResult{
		SystemPrompt: req.SystemPrompt + "\n[injected by gRPC extension]",
	}, nil
}

// grpcBlockerExt: blocks all Bash tool calls.
type grpcBlockerExt struct {
	extpb.UnimplementedExtensionServiceServer
}

func (e *grpcBlockerExt) Initialize(_ context.Context, _ *extpb.InitializeRequest) (*extpb.InitializeResponse, error) {
	return &extpb.InitializeResponse{
		ProtocolVersion: "1.0",
		Hooks:           []string{"tool_call"},
	}, nil
}

func (e *grpcBlockerExt) OnToolCall(_ context.Context, req *extpb.ToolCallHookEvent) (*extpb.ToolCallHookResult, error) {
	if req.ToolName == "Bash" {
		return &extpb.ToolCallHookResult{Block: true, Reason: "bash is blocked by gRPC"}, nil
	}
	return &extpb.ToolCallHookResult{Block: false}, nil
}

// grpcToolProviderExt: provides an echo tool with streaming progress.
type grpcToolProviderExt struct {
	extpb.UnimplementedExtensionServiceServer
}

func (e *grpcToolProviderExt) Initialize(_ context.Context, _ *extpb.InitializeRequest) (*extpb.InitializeResponse, error) {
	params, _ := structpb.NewStruct(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message": map[string]any{
				"type":        "string",
				"description": "Message to echo",
			},
		},
		"required": []any{"message"},
	})
	return &extpb.InitializeResponse{
		ProtocolVersion: "1.0",
		Tools: []*extpb.ToolDefinition{
			{
				Name:        "grpc_echo",
				Label:       "Echo (gRPC)",
				Description: "Echoes input back via gRPC",
				Parameters:  params,
			},
		},
	}, nil
}

func (e *grpcToolProviderExt) ExecuteTool(req *extpb.ToolExecuteRequest, stream extpb.ExtensionService_ExecuteToolServer) error {
	msg := ""
	if req.Params != nil {
		if m, ok := req.Params.AsMap()["message"]; ok {
			msg = fmt.Sprintf("%v", m)
		}
	}

	// Send a progress update first.
	stream.Send(&extpb.ToolExecuteResponse{
		IsProgress: true,
		Content: []*extpb.ContentBlock{
			{Block: &extpb.ContentBlock_Text{Text: &extpb.TextContent{Text: "processing..."}}},
		},
	})

	// Send the final result.
	stream.Send(&extpb.ToolExecuteResponse{
		IsProgress: false,
		Content: []*extpb.ContentBlock{
			{Block: &extpb.ContentBlock_Text{Text: &extpb.TextContent{Text: "grpc_echo: " + msg}}},
		},
	})

	return nil
}

// grpcContextModifierExt: modifies context and tool results.
type grpcContextModifierExt struct {
	extpb.UnimplementedExtensionServiceServer
}

func (e *grpcContextModifierExt) Initialize(_ context.Context, _ *extpb.InitializeRequest) (*extpb.InitializeResponse, error) {
	return &extpb.InitializeResponse{
		ProtocolVersion: "1.0",
		Hooks:           []string{"context", "tool_result"},
	}, nil
}

func (e *grpcContextModifierExt) OnContext(_ context.Context, req *extpb.ContextHookEvent) (*extpb.ContextHookResult, error) {
	// Add a marker message.
	marker, _ := json.Marshal(map[string]any{"role": "context_marker_grpc"})
	msgs := append(req.Messages, marker)
	return &extpb.ContextHookResult{Messages: msgs}, nil
}

func (e *grpcContextModifierExt) OnToolResult(_ context.Context, req *extpb.ToolResultHookEvent) (*extpb.ToolResultHookResult, error) {
	var newContent []*extpb.ContentBlock
	for _, c := range req.Content {
		if t := c.GetText(); t != nil {
			newContent = append(newContent, &extpb.ContentBlock{
				Block: &extpb.ContentBlock_Text{
					Text: &extpb.TextContent{Text: t.Text + " [reviewed-grpc]"},
				},
			})
		} else {
			newContent = append(newContent, c)
		}
	}
	isErr := req.IsError
	return &extpb.ToolResultHookResult{Content: newContent, IsError: &isErr}, nil
}

// ===== Helpers =====

func createGRPCTestExtScript(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()

	testBinary, _ := os.Executable()
	scriptPath := filepath.Join(dir, "extension")
	script := fmt.Sprintf("#!/bin/sh\nexec env PI_TEST_GRPC_EXT_MODE=%s %s -test.run=^$\n", mode, testBinary)
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	manifest := ExtensionManifest{
		ID:         "grpc-test-" + mode,
		Name:       "gRPC Test " + mode,
		Type:       ProcessExtensionType,
		Protocol:   GRPCProtocol,
		EntryPoint: "extension",
	}
	manifestData, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestData, 0644); err != nil {
		t.Fatal(err)
	}

	return dir
}

func startGRPCTestExtension(t *testing.T, mode string) (*Extension, *GRPCProcessExtension) {
	t.Helper()
	dir := createGRPCTestExtScript(t, mode)

	ext := &Extension{
		Manifest: ExtensionManifest{
			ID:         "grpc-test-" + mode,
			Name:       "gRPC Test " + mode,
			Type:       ProcessExtensionType,
			Protocol:   GRPCProtocol,
			EntryPoint: "extension",
		},
		Dir:   dir,
		State: make(map[string]any),
	}

	proc := NewGRPCProcessExtension(ext)
	if err := proc.Start(); err != nil {
		t.Fatalf("failed to start gRPC extension: %v", err)
	}
	t.Cleanup(func() { proc.Stop() })

	return ext, proc
}

// ===== Tests =====

func TestGRPCProcessExtensionInitialize(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	_, proc := startGRPCTestExtension(t, "basic")

	if !proc.IsRunning() {
		t.Fatal("expected gRPC extension to be running")
	}
	if !proc.subscribedHooks["tool_call"] {
		t.Error("expected tool_call hook")
	}
	if !proc.subscribedHooks["before_agent_start"] {
		t.Error("expected before_agent_start hook")
	}
}

func TestGRPCProcessExtensionToolCallHook(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	ext, proc := startGRPCTestExtension(t, "blocker")
	ext.Hooks = proc.BuildHooks()

	runner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	// Bash should be blocked.
	result := runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-1",
		ToolName:   "Bash",
		Input:      map[string]any{"command": "ls"},
	})
	if result == nil || !result.Block {
		t.Fatal("expected Bash to be blocked via gRPC")
	}
	if !strings.Contains(result.Reason, "gRPC") {
		t.Fatalf("expected gRPC in reason, got: %s", result.Reason)
	}

	// Read should not be blocked.
	result = runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-2",
		ToolName:   "Read",
		Input:      map[string]any{},
	})
	if result != nil && result.Block {
		t.Fatal("expected Read to not be blocked")
	}
}

func TestGRPCProcessExtensionBeforeAgentStartHook(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	ext, proc := startGRPCTestExtension(t, "basic")
	ext.Hooks = proc.BuildHooks()

	runner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	result := runner.RunBeforeAgentStart(&BeforeAgentStartHookEvent{
		SystemPrompt: "You are helpful.",
	})
	if result == nil {
		t.Fatal("expected before_agent_start result")
	}
	if !strings.Contains(result.SystemPrompt, "[injected by gRPC extension]") {
		t.Fatalf("expected gRPC injection in prompt, got: %s", result.SystemPrompt)
	}
}

func TestGRPCProcessExtensionToolProvider(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	_, proc := startGRPCTestExtension(t, "tool_provider")

	tools := proc.BuildTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0]
	if tool.Name != "grpc_echo" {
		t.Fatalf("expected tool name 'grpc_echo', got %q", tool.Name)
	}

	// Execute with streaming progress.
	var progressUpdates []AgentToolResult
	result, err := tool.Execute("tc-1", map[string]any{"message": "hello"}, nil, func(partial AgentToolResult) {
		progressUpdates = append(progressUpdates, partial)
	})
	if err != nil {
		t.Fatalf("tool execution failed: %v", err)
	}

	// Should have received at least one progress update.
	if len(progressUpdates) == 0 {
		t.Error("expected progress updates from streaming tool")
	}

	// Final result.
	if len(result.Content) == 0 || result.Content[0].Text == nil {
		t.Fatal("expected text content")
	}
	if result.Content[0].Text.Text != "grpc_echo: hello" {
		t.Fatalf("expected 'grpc_echo: hello', got %q", result.Content[0].Text.Text)
	}
}

func TestGRPCProcessExtensionContextModifier(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	ext, proc := startGRPCTestExtension(t, "context_modifier")
	ext.Hooks = proc.BuildHooks()

	runner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	msgs := []AgentMessage{
		NewAgentMessageFromUser(&ai.UserMessage{
			Role:    "user",
			Content: ai.UserContent{Text: "hello"},
		}),
	}
	contextResult := runner.RunContext(&ContextHookEvent{Messages: msgs})
	if contextResult == nil {
		t.Fatal("expected context result")
	}
	if len(contextResult.Messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(contextResult.Messages))
	}
}

func TestGRPCProcessExtensionToolResultModifier(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	ext, proc := startGRPCTestExtension(t, "context_modifier")
	ext.Hooks = proc.BuildHooks()

	runner := NewHookRunner(func() []*Extension { return []*Extension{ext} })

	result := runner.RunToolResult(&ToolResultHookEvent{
		ToolCallID: "tc-1",
		ToolName:   "Read",
		Input:      map[string]any{},
		Content: []ai.ToolResultContentBlock{
			{Text: &ai.TextContent{Type: "text", Text: "file contents"}},
		},
		IsError: false,
	})
	if result == nil {
		t.Fatal("expected tool result modification")
	}
	if len(result.Content) == 0 || result.Content[0].Text == nil {
		t.Fatal("expected text content")
	}
	if !strings.Contains(result.Content[0].Text.Text, "[reviewed-grpc]") {
		t.Fatalf("expected '[reviewed-grpc]' in text, got: %s", result.Content[0].Text.Text)
	}
}

func TestGRPCProcessExtensionGracefulShutdown(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	_, proc := startGRPCTestExtension(t, "basic")

	if !proc.IsRunning() {
		t.Fatal("expected running")
	}
	if err := proc.Stop(); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	if proc.IsRunning() {
		t.Fatal("expected not running after stop")
	}
}

func TestGRPCProcessExtensionManagerIntegration(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	dir := createGRPCTestExtScript(t, "blocker")

	em := NewExtensionManager("", nil)
	ext, err := loadExtensionManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	em.Register(ext)

	if err := em.StartProcessExtension(ext.Manifest.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { em.StopProcessExtensions() })

	runner := em.NewHookRunner()
	result := runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-1",
		ToolName:   "Bash",
		Input:      map[string]any{"command": "ls"},
	})
	if result == nil || !result.Block {
		t.Fatal("expected Bash blocked via gRPC ExtensionManager")
	}
}

func TestGRPCProcessExtensionMixedProtocols(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	// JSON-RPC extension (allows everything).
	jsonRPCExt, jsonRPCProc := startTestProcessExtension(t, "basic")
	jsonRPCExt.Hooks = jsonRPCProc.BuildHooks()

	// gRPC extension (blocks Bash).
	grpcExt, grpcProc := startGRPCTestExtension(t, "blocker")
	grpcExt.Hooks = grpcProc.BuildHooks()

	runner := NewHookRunner(func() []*Extension {
		return []*Extension{jsonRPCExt, grpcExt}
	})

	// gRPC extension should block Bash.
	result := runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-1",
		ToolName:   "Bash",
		Input:      map[string]any{"command": "ls"},
	})
	if result == nil || !result.Block {
		t.Fatal("expected Bash blocked by gRPC extension in mixed setup")
	}
}

// TestGRPCProtoConversions verifies proto ↔ Go type conversions.
func TestGRPCProtoConversions(t *testing.T) {
	// ContentBlock roundtrip.
	blocks := []ai.ToolResultContentBlock{
		{Text: &ai.TextContent{Type: "text", Text: "hello"}},
		{Image: &ai.ImageContent{Type: "image", Data: "base64data", MimeType: "image/png"}},
	}

	pbBlocks := contentBlocksToProto(blocks)
	if len(pbBlocks) != 2 {
		t.Fatalf("expected 2 proto blocks, got %d", len(pbBlocks))
	}
	if pbBlocks[0].GetText().Text != "hello" {
		t.Fatal("text roundtrip failed")
	}
	if pbBlocks[1].GetImage().MimeType != "image/png" {
		t.Fatal("image roundtrip failed")
	}

	back := contentBlocksFromProto(pbBlocks)
	if len(back) != 2 {
		t.Fatalf("expected 2 blocks back, got %d", len(back))
	}
	if back[0].Text.Text != "hello" {
		t.Fatal("text back conversion failed")
	}
	if back[1].Image.Data != "base64data" {
		t.Fatal("image back conversion failed")
	}
}

// TestGRPCExtensionNoEntryPoint verifies proper error for missing entry point.
func TestGRPCExtensionNoEntryPoint(t *testing.T) {
	ext := &Extension{
		Manifest: ExtensionManifest{
			ID:       "no-entry",
			Type:     ProcessExtensionType,
			Protocol: GRPCProtocol,
		},
		Dir:   t.TempDir(),
		State: make(map[string]any),
	}

	proc := NewGRPCProcessExtension(ext)
	err := proc.Start()
	if err == nil {
		proc.Stop()
		t.Fatal("expected error for missing entry point")
	}
	if !strings.Contains(err.Error(), "no entryPoint") {
		t.Fatalf("expected 'no entryPoint' error, got: %v", err)
	}
}

// Suppress unused import warning for io.
var _ = io.EOF
