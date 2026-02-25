package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/nickolaev/pi-mono/packages/go-pi-agent/extpb"
	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

const (
	// GRPCProtocol identifies gRPC-based process extensions.
	GRPCProtocol = "grpc"

	// Timeout for the extension to print its handshake line.
	handshakeTimeout = 10 * time.Second

	// Default timeout for gRPC calls.
	defaultGRPCTimeout = 30 * time.Second
)

// GRPCProcessExtension manages a subprocess extension connected via gRPC.
//
// The extension subprocess:
//  1. Listens on a random TCP port
//  2. Prints handshake: "PLUGIN|1|tcp|127.0.0.1:PORT|grpc"
//  3. Serves the ExtensionService gRPC interface
//
// The host:
//  1. Reads the handshake, dials gRPC to the extension
//  2. Starts a HostService gRPC server for callbacks (SetState/Log)
//  3. Sends the host address in InitializeRequest.host_address
//  4. Proxies hooks and tools
type GRPCProcessExtension struct {
	stateMu sync.Mutex
	cmd     *exec.Cmd
	conn    *grpc.ClientConn
	client  extpb.ExtensionServiceClient
	stdin   io.WriteCloser
	stderr  io.ReadCloser

	// Host-side gRPC server for HostService callbacks.
	hostServer *hostGRPCServer

	subscribedHooks map[string]bool
	toolDefs        []*extpb.ToolDefinition

	ext     *Extension
	timeout time.Duration
	done    chan struct{}
	started bool

	// OnError is called when a hook/call fails. Set before Start().
	OnError ExtensionErrorHandler

	// OnLog is called when the extension sends a log message. Set before Start().
	OnLog ExtensionLogHandler

	stderrBuf []byte
	stderrMu  sync.Mutex
}

// NewGRPCProcessExtension creates a GRPCProcessExtension for the given Extension.
func NewGRPCProcessExtension(ext *Extension) *GRPCProcessExtension {
	return &GRPCProcessExtension{
		ext:             ext,
		timeout:         defaultGRPCTimeout,
		subscribedHooks: make(map[string]bool),
		done:            make(chan struct{}),
	}
}

// Start launches the extension subprocess, reads the handshake, dials gRPC,
// starts the HostService server, and performs the Initialize RPC.
func (g *GRPCProcessExtension) Start() error {
	g.stateMu.Lock()
	if g.started {
		g.stateMu.Unlock()
		return fmt.Errorf("gRPC extension %s already started", g.ext.Manifest.ID)
	}

	entryPoint := g.ext.Manifest.EntryPoint
	if entryPoint == "" {
		g.stateMu.Unlock()
		return fmt.Errorf("extension %s has no entryPoint", g.ext.Manifest.ID)
	}
	if !filepath.IsAbs(entryPoint) {
		entryPoint = filepath.Join(g.ext.Dir, entryPoint)
	}

	g.cmd = exec.Command(entryPoint)
	g.cmd.Dir = g.ext.Dir

	var err error
	g.stdin, err = g.cmd.StdinPipe()
	if err != nil {
		g.stateMu.Unlock()
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdoutPipe, err := g.cmd.StdoutPipe()
	if err != nil {
		g.stateMu.Unlock()
		return fmt.Errorf("stdout pipe: %w", err)
	}

	g.stderr, err = g.cmd.StderrPipe()
	if err != nil {
		g.stateMu.Unlock()
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := g.cmd.Start(); err != nil {
		g.stateMu.Unlock()
		return fmt.Errorf("start extension %s: %w", g.ext.Manifest.ID, err)
	}

	g.started = true
	g.stateMu.Unlock()

	go g.drainStderr()

	// Start HostService gRPC server for callbacks.
	hostSrv, err := startHostServiceServer(g.ext, g.OnLog, g.OnError)
	if err != nil {
		g.Stop()
		return fmt.Errorf("start host service for %s: %w", g.ext.Manifest.ID, err)
	}
	g.hostServer = hostSrv

	// Read handshake line from stdout.
	addr, err := g.readHandshake(stdoutPipe)
	if err != nil {
		g.Stop()
		return fmt.Errorf("handshake for %s: %w", g.ext.Manifest.ID, err)
	}

	// Dial gRPC to the extension.
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		g.Stop()
		return fmt.Errorf("dial gRPC %s: %w", addr, err)
	}
	g.conn = conn
	g.client = extpb.NewExtensionServiceClient(conn)

	// Initialize with host address.
	initResp, err := g.initialize()
	if err != nil {
		g.Stop()
		return fmt.Errorf("initialize %s: %w", g.ext.Manifest.ID, err)
	}

	for _, hook := range initResp.Hooks {
		g.subscribedHooks[hook] = true
	}
	g.toolDefs = initResp.Tools

	return nil
}

// readHandshake reads the handshake line: "PLUGIN|1|tcp|127.0.0.1:PORT|grpc"
func (g *GRPCProcessExtension) readHandshake(stdout io.Reader) (string, error) {
	type result struct {
		addr string
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			line := scanner.Text()
			parts := strings.Split(line, "|")
			if len(parts) != 5 || parts[0] != "PLUGIN" || parts[4] != "grpc" {
				ch <- result{err: fmt.Errorf("invalid handshake: %q", line)}
				return
			}
			ch <- result{addr: parts[3]}
		} else {
			ch <- result{err: fmt.Errorf("extension exited before handshake")}
		}
	}()

	select {
	case r := <-ch:
		return r.addr, r.err
	case <-time.After(handshakeTimeout):
		return "", fmt.Errorf("handshake timeout after %s", handshakeTimeout)
	}
}

func (g *GRPCProcessExtension) initialize() (*extpb.InitializeResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()

	state, _ := structpb.NewStruct(g.ext.State)

	req := &extpb.InitializeRequest{
		ProtocolVersion: ExtProtocolVersion,
		ExtensionId:     g.ext.Manifest.ID,
		ExtensionDir:    g.ext.Dir,
		State:           state,
	}
	if g.hostServer != nil {
		req.HostAddress = g.hostServer.addr
	}

	resp, err := g.client.Initialize(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// Stop shuts down the extension subprocess and host server.
func (g *GRPCProcessExtension) Stop() error {
	g.stateMu.Lock()
	defer g.stateMu.Unlock()

	if !g.started {
		return nil
	}

	if g.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		g.client.Shutdown(ctx, &extpb.Empty{})
		cancel()
	}

	if g.conn != nil {
		g.conn.Close()
	}

	if g.hostServer != nil {
		g.hostServer.Stop()
	}

	if g.stdin != nil {
		g.stdin.Close()
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- g.cmd.Wait() }()

	select {
	case <-waitDone:
	case <-time.After(shutdownGracePeriod):
		if g.cmd.Process != nil {
			g.cmd.Process.Kill()
		}
		<-waitDone
	}

	close(g.done)
	g.started = false
	return nil
}

// IsRunning returns whether the extension process is alive.
func (g *GRPCProcessExtension) IsRunning() bool {
	g.stateMu.Lock()
	defer g.stateMu.Unlock()
	return g.started
}

// Stderr returns captured stderr for diagnostics.
func (g *GRPCProcessExtension) Stderr() string {
	g.stderrMu.Lock()
	defer g.stderrMu.Unlock()
	return string(g.stderrBuf)
}

func (g *GRPCProcessExtension) drainStderr() {
	buf := make([]byte, 4096)
	for {
		n, err := g.stderr.Read(buf)
		if n > 0 {
			g.stderrMu.Lock()
			if len(g.stderrBuf) < 64*1024 {
				g.stderrBuf = append(g.stderrBuf, buf[:n]...)
			}
			g.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// reportError reports a hook/tool error via the OnError handler.
func (g *GRPCProcessExtension) reportError(operation string, err error) {
	if g.OnError != nil {
		g.OnError(g.ext.Manifest.ID, operation, err)
	}
}

// --- Hook proxying ---

// BuildHooks generates ExtensionHooks that proxy to the gRPC subprocess.
// All errors are reported via OnError instead of being silently swallowed.
func (g *GRPCProcessExtension) BuildHooks() *ExtensionHooks {
	hooks := &ExtensionHooks{}

	if g.subscribedHooks["tool_call"] {
		hooks.ToolCall = append(hooks.ToolCall, func(event *ToolCallHookEvent) *ToolCallHookResult {
			ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
			defer cancel()
			input, _ := structpb.NewStruct(event.Input)
			resp, err := g.client.OnToolCall(ctx, &extpb.ToolCallHookEvent{
				ToolCallId: event.ToolCallID,
				ToolName:   event.ToolName,
				Input:      input,
			})
			if err != nil {
				g.reportError("hook/tool_call", err)
				return nil
			}
			return &ToolCallHookResult{Block: resp.Block, Reason: resp.Reason}
		})
	}

	if g.subscribedHooks["tool_result"] {
		hooks.ToolResult = append(hooks.ToolResult, func(event *ToolResultHookEvent) *ToolResultHookResult {
			ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
			defer cancel()
			input, _ := structpb.NewStruct(event.Input)
			resp, err := g.client.OnToolResult(ctx, &extpb.ToolResultHookEvent{
				ToolCallId: event.ToolCallID,
				ToolName:   event.ToolName,
				Input:      input,
				Content:    contentBlocksToProto(event.Content),
				IsError:    event.IsError,
			})
			if err != nil {
				g.reportError("hook/tool_result", err)
				return nil
			}
			result := &ToolResultHookResult{}
			if resp.Content != nil {
				result.Content = contentBlocksFromProto(resp.Content)
			}
			if resp.IsError != nil {
				isErr := *resp.IsError
				result.IsError = &isErr
			}
			return result
		})
	}

	if g.subscribedHooks["context"] {
		hooks.Context = append(hooks.Context, func(event *ContextHookEvent) *ContextHookResult {
			ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
			defer cancel()
			resp, err := g.client.OnContext(ctx, &extpb.ContextHookEvent{
				Messages: marshalMessagesBytes(event.Messages),
			})
			if err != nil {
				g.reportError("hook/context", err)
				return nil
			}
			if resp.Messages == nil {
				return nil
			}
			return &ContextHookResult{Messages: unmarshalMessagesBytes(resp.Messages)}
		})
	}

	if g.subscribedHooks["before_agent_start"] {
		hooks.BeforeAgentStart = append(hooks.BeforeAgentStart, func(event *BeforeAgentStartHookEvent) *BeforeAgentStartHookResult {
			ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
			defer cancel()
			resp, err := g.client.OnBeforeAgentStart(ctx, &extpb.BeforeAgentStartHookEvent{
				SystemPrompt: event.SystemPrompt,
			})
			if err != nil {
				g.reportError("hook/before_agent_start", err)
				return nil
			}
			if resp.SystemPrompt == "" {
				return nil
			}
			return &BeforeAgentStartHookResult{SystemPrompt: resp.SystemPrompt}
		})
	}

	if g.subscribedHooks["agent_start"] {
		hooks.AgentStart = append(hooks.AgentStart, func() {
			ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
			defer cancel()
			if _, err := g.client.OnAgentStart(ctx, &extpb.Empty{}); err != nil {
				g.reportError("hook/agent_start", err)
			}
		})
	}

	if g.subscribedHooks["agent_end"] {
		hooks.AgentEnd = append(hooks.AgentEnd, func(event *AgentEndHookEvent) {
			ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
			defer cancel()
			if _, err := g.client.OnAgentEnd(ctx, &extpb.AgentEndEvent{
				Messages: marshalMessagesBytes(event.Messages),
			}); err != nil {
				g.reportError("hook/agent_end", err)
			}
		})
	}

	if g.subscribedHooks["turn_start"] {
		hooks.TurnStart = append(hooks.TurnStart, func() {
			ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
			defer cancel()
			if _, err := g.client.OnTurnStart(ctx, &extpb.Empty{}); err != nil {
				g.reportError("hook/turn_start", err)
			}
		})
	}

	if g.subscribedHooks["turn_end"] {
		hooks.TurnEnd = append(hooks.TurnEnd, func(event *TurnEndHookEvent) {
			ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
			defer cancel()
			turnJSON, _ := json.Marshal(event.TurnMessage)
			var resultsJSON [][]byte
			for _, r := range event.ToolResults {
				b, _ := json.Marshal(r)
				resultsJSON = append(resultsJSON, b)
			}
			if _, err := g.client.OnTurnEnd(ctx, &extpb.TurnEndEvent{
				TurnMessage: turnJSON,
				ToolResults: resultsJSON,
			}); err != nil {
				g.reportError("hook/turn_end", err)
			}
		})
	}

	if g.subscribedHooks["session_start"] {
		hooks.SessionStart = append(hooks.SessionStart, func() {
			ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
			defer cancel()
			if _, err := g.client.OnSessionStart(ctx, &extpb.Empty{}); err != nil {
				g.reportError("hook/session_start", err)
			}
		})
	}

	if g.subscribedHooks["session_shutdown"] {
		hooks.SessionShutdown = append(hooks.SessionShutdown, func() {
			ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
			defer cancel()
			if _, err := g.client.OnSessionShutdown(ctx, &extpb.Empty{}); err != nil {
				g.reportError("hook/session_shutdown", err)
			}
		})
	}

	return hooks
}

// BuildTools generates AgentTools that proxy execution via gRPC streaming.
func (g *GRPCProcessExtension) BuildTools() []*AgentTool {
	var tools []*AgentTool
	for _, def := range g.toolDefs {
		toolName := def.Name
		params := make(map[string]any)
		if def.Parameters != nil {
			params = def.Parameters.AsMap()
		}
		tool := &AgentTool{
			Tool: ai.Tool{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  params,
			},
			Label: def.Label,
			Execute: func(toolCallID string, toolParams map[string]any, signal *ai.AbortSignal, onUpdate AgentToolUpdateCallback) (*AgentToolResult, error) {
				ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
				defer cancel()

				pbParams, _ := structpb.NewStruct(toolParams)
				stream, err := g.client.ExecuteTool(ctx, &extpb.ToolExecuteRequest{
					ToolCallId: toolCallID,
					ToolName:   toolName,
					Params:     pbParams,
				})
				if err != nil {
					return nil, fmt.Errorf("gRPC tool %s: %w", toolName, err)
				}

				var finalResult *AgentToolResult
				for {
					resp, err := stream.Recv()
					if err == io.EOF {
						break
					}
					if err != nil {
						return nil, fmt.Errorf("gRPC tool stream %s: %w", toolName, err)
					}

					content := contentBlocksFromProto(resp.Content)
					var details any
					if resp.Details != nil {
						details = resp.Details.AsMap()
					}

					if resp.IsProgress && onUpdate != nil {
						onUpdate(AgentToolResult{Content: content, Details: details})
					} else {
						finalResult = &AgentToolResult{Content: content, Details: details}
					}
				}

				if finalResult == nil {
					return &AgentToolResult{
						Content: []ai.ToolResultContentBlock{
							{Text: &ai.TextContent{Type: "text", Text: "no result from extension"}},
						},
					}, nil
				}
				return finalResult, nil
			},
		}
		tools = append(tools, tool)
	}
	return tools
}

// --- Proto conversion helpers ---

func contentBlocksToProto(blocks []ai.ToolResultContentBlock) []*extpb.ContentBlock {
	var result []*extpb.ContentBlock
	for _, b := range blocks {
		pb := &extpb.ContentBlock{}
		if b.Text != nil {
			pb.Block = &extpb.ContentBlock_Text{
				Text: &extpb.TextContent{Text: b.Text.Text},
			}
		} else if b.Image != nil {
			pb.Block = &extpb.ContentBlock_Image{
				Image: &extpb.ImageContent{Data: b.Image.Data, MimeType: b.Image.MimeType},
			}
		}
		result = append(result, pb)
	}
	return result
}

func contentBlocksFromProto(blocks []*extpb.ContentBlock) []ai.ToolResultContentBlock {
	var result []ai.ToolResultContentBlock
	for _, pb := range blocks {
		var b ai.ToolResultContentBlock
		switch v := pb.Block.(type) {
		case *extpb.ContentBlock_Text:
			b.Text = &ai.TextContent{Type: "text", Text: v.Text.Text}
		case *extpb.ContentBlock_Image:
			b.Image = &ai.ImageContent{Type: "image", Data: v.Image.Data, MimeType: v.Image.MimeType}
		}
		result = append(result, b)
	}
	return result
}

func marshalMessagesBytes(msgs []AgentMessage) [][]byte {
	var result [][]byte
	for _, m := range msgs {
		data, err := json.Marshal(m)
		if err != nil {
			continue
		}
		result = append(result, data)
	}
	return result
}

func unmarshalMessagesBytes(raw [][]byte) []AgentMessage {
	var result []AgentMessage
	for _, r := range raw {
		var msg AgentMessage
		if json.Unmarshal(r, &msg) == nil {
			result = append(result, msg)
		}
	}
	return result
}
