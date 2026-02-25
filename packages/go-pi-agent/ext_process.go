package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

const (
	// ProcessExtensionType marks an extension as a subprocess extension.
	ProcessExtensionType = "process"

	// Default timeout for JSON-RPC calls to the extension process.
	defaultProcessTimeout = 30 * time.Second

	// Graceful shutdown wait time before SIGKILL.
	shutdownGracePeriod = 5 * time.Second
)

// ProcessExtension manages a subprocess extension connected via
// JSON-RPC 2.0 over stdin/stdout (newline-delimited).
//
// Lifecycle:
//  1. NewProcessExtension(ext) — create from manifest
//  2. Start() — launch subprocess, perform handshake
//  3. BuildHooks() — generate ExtensionHooks that proxy to the subprocess
//  4. BuildTools() — generate AgentTools that proxy to the subprocess
//  5. Stop() — graceful shutdown
type ProcessExtension struct {
	stateMu sync.Mutex // Protects lifecycle state (started, cmd, etc.)
	writeMu sync.Mutex // Protects stdin writes (separate to avoid deadlock)

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	stderr io.ReadCloser

	nextID  atomic.Int64
	pending sync.Map // map[int64]chan *RPCResponse

	// Capabilities from initialization handshake.
	subscribedHooks map[string]bool
	toolDefs        []ProcessToolDef

	// Configuration.
	ext     *Extension
	timeout time.Duration

	// Lifecycle.
	done    chan struct{}
	started bool

	// OnError is called when a hook/call fails. Set before Start().
	OnError ExtensionErrorHandler

	// ErrLog collects stderr output for diagnostics.
	stderrBuf []byte
	stderrMu  sync.Mutex
}

// reportError reports a hook/tool error via the OnError handler.
func (p *ProcessExtension) reportError(operation string, err error) {
	if p.OnError != nil {
		p.OnError(p.ext.Manifest.ID, operation, err)
	}
}

// NewProcessExtension creates a ProcessExtension for the given Extension.
// The extension's manifest.EntryPoint must be set to the executable path.
func NewProcessExtension(ext *Extension) *ProcessExtension {
	return &ProcessExtension{
		ext:             ext,
		timeout:         defaultProcessTimeout,
		subscribedHooks: make(map[string]bool),
		done:            make(chan struct{}),
	}
}

// Start launches the extension subprocess and performs the initialization handshake.
func (p *ProcessExtension) Start() error {
	p.stateMu.Lock()
	if p.started {
		p.stateMu.Unlock()
		return fmt.Errorf("process extension %s already started", p.ext.Manifest.ID)
	}

	entryPoint := p.ext.Manifest.EntryPoint
	if entryPoint == "" {
		p.stateMu.Unlock()
		return fmt.Errorf("extension %s has no entryPoint", p.ext.Manifest.ID)
	}

	// Resolve relative path against extension directory.
	if !filepath.IsAbs(entryPoint) {
		entryPoint = filepath.Join(p.ext.Dir, entryPoint)
	}

	p.cmd = exec.Command(entryPoint)
	p.cmd.Dir = p.ext.Dir

	var err error
	p.stdin, err = p.cmd.StdinPipe()
	if err != nil {
		p.stateMu.Unlock()
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdoutPipe, err := p.cmd.StdoutPipe()
	if err != nil {
		p.stateMu.Unlock()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	p.stdout = bufio.NewScanner(stdoutPipe)
	p.stdout.Buffer(make([]byte, 4*1024*1024), 4*1024*1024) // 4MB line buffer

	p.stderr, err = p.cmd.StderrPipe()
	if err != nil {
		p.stateMu.Unlock()
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := p.cmd.Start(); err != nil {
		p.stateMu.Unlock()
		return fmt.Errorf("start extension %s: %w", p.ext.Manifest.ID, err)
	}

	p.started = true
	p.stateMu.Unlock()

	// Read responses in background.
	go p.readLoop()

	// Capture stderr for diagnostics.
	go p.drainStderr()

	// Perform initialization handshake (uses call() which needs writeMu, not stateMu).
	initResult, err := p.initialize()
	if err != nil {
		p.Stop()
		return fmt.Errorf("initialize extension %s: %w", p.ext.Manifest.ID, err)
	}

	for _, hook := range initResult.Hooks {
		p.subscribedHooks[hook] = true
	}
	p.toolDefs = initResult.Tools

	return nil
}

func (p *ProcessExtension) initialize() (*InitializeResult, error) {
	params := InitializeParams{
		ProtocolVersion: ExtProtocolVersion,
		ExtensionID:     p.ext.Manifest.ID,
		ExtensionDir:    p.ext.Dir,
		State:           p.ext.State,
	}

	resp, err := p.call(MethodInitialize, params)
	if err != nil {
		return nil, err
	}

	var result InitializeResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse initialize result: %w", err)
	}

	// Send initialized notification.
	_ = p.notify(MethodInitialized, nil)

	return &result, nil
}

// Stop shuts down the extension subprocess gracefully.
func (p *ProcessExtension) Stop() error {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()

	if !p.started {
		return nil
	}

	// Send shutdown notification (uses writeMu, not stateMu).
	_ = p.notify(MethodShutdown, nil)

	// Close stdin to signal EOF.
	p.stdin.Close()

	// Wait for process exit with timeout.
	waitDone := make(chan error, 1)
	go func() { waitDone <- p.cmd.Wait() }()

	select {
	case <-waitDone:
	case <-time.After(shutdownGracePeriod):
		if p.cmd.Process != nil {
			p.cmd.Process.Kill()
		}
		<-waitDone
	}

	close(p.done)
	p.started = false
	return nil
}

// IsRunning returns whether the extension process is running.
func (p *ProcessExtension) IsRunning() bool {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.started
}

// Stderr returns captured stderr output for diagnostics.
func (p *ProcessExtension) Stderr() string {
	p.stderrMu.Lock()
	defer p.stderrMu.Unlock()
	return string(p.stderrBuf)
}

// --- JSON-RPC transport ---

// call sends a JSON-RPC request and waits for the response.
func (p *ProcessExtension) call(method string, params any) (json.RawMessage, error) {
	id := p.nextID.Add(1)

	paramsBytes, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	req := RPCRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  paramsBytes,
	}

	// Register pending response channel.
	ch := make(chan *RPCResponse, 1)
	p.pending.Store(id, ch)
	defer p.pending.Delete(id)

	// Write request as a single line.
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	p.writeMu.Lock()
	_, writeErr := fmt.Fprintf(p.stdin, "%s\n", data)
	p.writeMu.Unlock()
	if writeErr != nil {
		return nil, fmt.Errorf("write request: %w", writeErr)
	}

	// Wait for response.
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("extension error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-time.After(p.timeout):
		return nil, fmt.Errorf("timeout waiting for response to %s (id=%d)", method, id)
	case <-p.done:
		return nil, fmt.Errorf("extension process exited")
	}
}

// notify sends a JSON-RPC notification (no response expected).
func (p *ProcessExtension) notify(method string, params any) error {
	req := RPCRequest{
		JSONRPC: "2.0",
		Method:  method,
	}

	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return err
		}
		req.Params = data
	}

	data, err := json.Marshal(req)
	if err != nil {
		return err
	}

	p.writeMu.Lock()
	_, writeErr := fmt.Fprintf(p.stdin, "%s\n", data)
	p.writeMu.Unlock()
	return writeErr
}

// readLoop reads JSON-RPC messages from the subprocess stdout.
func (p *ProcessExtension) readLoop() {
	for p.stdout.Scan() {
		line := p.stdout.Bytes()
		if len(line) == 0 {
			continue
		}

		// Try parsing as response (has non-nil "id").
		var resp RPCResponse
		if err := json.Unmarshal(line, &resp); err == nil && resp.ID != nil {
			if val, ok := p.pending.Load(*resp.ID); ok {
				ch := val.(chan *RPCResponse)
				ch <- &resp
			}
			continue
		}

		// Try parsing as notification (has "method", no "id").
		var notif RPCRequest
		if err := json.Unmarshal(line, &notif); err == nil && notif.Method != "" && notif.ID == nil {
			p.handleNotification(&notif)
		}
	}
}

func (p *ProcessExtension) handleNotification(notif *RPCRequest) {
	switch notif.Method {
	case MethodStateSet:
		var params StateSetParams
		if json.Unmarshal(notif.Params, &params) == nil {
			if p.ext.State == nil {
				p.ext.State = make(map[string]any)
			}
			p.ext.State[params.Key] = params.Value
		}
	}
}

func (p *ProcessExtension) drainStderr() {
	buf := make([]byte, 4096)
	for {
		n, err := p.stderr.Read(buf)
		if n > 0 {
			p.stderrMu.Lock()
			// Cap at 64KB to prevent unbounded growth.
			if len(p.stderrBuf) < 64*1024 {
				p.stderrBuf = append(p.stderrBuf, buf[:n]...)
			}
			p.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// --- Hook proxying ---

// BuildHooks generates ExtensionHooks that proxy calls to the subprocess.
// The returned hooks can be assigned to ext.Hooks and work seamlessly
// with the existing HookRunner.
func (p *ProcessExtension) BuildHooks() *ExtensionHooks {
	hooks := &ExtensionHooks{}

	if p.subscribedHooks["tool_call"] {
		hooks.ToolCall = append(hooks.ToolCall, func(event *ToolCallHookEvent) *ToolCallHookResult {
			wireEvent := WireToolCallHookEvent{
				ToolCallID: event.ToolCallID,
				ToolName:   event.ToolName,
				Input:      event.Input,
			}
			resp, err := p.call(MethodHookToolCall, wireEvent)
			if err != nil {
				p.reportError("hook/tool_call", err)
				return nil
			}
			var result WireToolCallHookResult
			if err := json.Unmarshal(resp, &result); err != nil {
				p.reportError("hook/tool_call", err)
				return nil
			}
			return &ToolCallHookResult{Block: result.Block, Reason: result.Reason}
		})
	}

	if p.subscribedHooks["tool_result"] {
		hooks.ToolResult = append(hooks.ToolResult, func(event *ToolResultHookEvent) *ToolResultHookResult {
			// Serialize content blocks.
			contentJSON := marshalContentBlocks(event.Content)
			wireEvent := WireToolResultHookEvent{
				ToolCallID: event.ToolCallID,
				ToolName:   event.ToolName,
				Input:      event.Input,
				Content:    contentJSON,
				IsError:    event.IsError,
			}
			resp, err := p.call(MethodHookToolResult, wireEvent)
			if err != nil {
				p.reportError("hook/tool_result", err)
				return nil
			}
			var wireResult WireToolResultHookResult
			if err := json.Unmarshal(resp, &wireResult); err != nil {
				p.reportError("hook/tool_result", err)
				return nil
			}
			result := &ToolResultHookResult{IsError: wireResult.IsError}
			if wireResult.Content != nil {
				result.Content = unmarshalContentBlocks(wireResult.Content)
			}
			return result
		})
	}

	if p.subscribedHooks["context"] {
		hooks.Context = append(hooks.Context, func(event *ContextHookEvent) *ContextHookResult {
			msgsJSON := marshalMessages(event.Messages)
			wireEvent := WireContextHookEvent{Messages: msgsJSON}
			resp, err := p.call(MethodHookContext, wireEvent)
			if err != nil {
				p.reportError("hook/context", err)
				return nil
			}
			var wireResult WireContextHookResult
			if err := json.Unmarshal(resp, &wireResult); err != nil {
				p.reportError("hook/context", err)
				return nil
			}
			if wireResult.Messages == nil {
				return nil
			}
			return &ContextHookResult{Messages: unmarshalMessages(wireResult.Messages)}
		})
	}

	if p.subscribedHooks["before_agent_start"] {
		hooks.BeforeAgentStart = append(hooks.BeforeAgentStart, func(event *BeforeAgentStartHookEvent) *BeforeAgentStartHookResult {
			wireEvent := WireBeforeAgentStartHookEvent{SystemPrompt: event.SystemPrompt}
			resp, err := p.call(MethodHookBeforeAgentStart, wireEvent)
			if err != nil {
				p.reportError("hook/before_agent_start", err)
				return nil
			}
			var wireResult WireBeforeAgentStartHookResult
			if err := json.Unmarshal(resp, &wireResult); err != nil {
				p.reportError("hook/before_agent_start", err)
				return nil
			}
			if wireResult.SystemPrompt == "" {
				return nil
			}
			return &BeforeAgentStartHookResult{SystemPrompt: wireResult.SystemPrompt}
		})
	}

	if p.subscribedHooks["agent_start"] {
		hooks.AgentStart = append(hooks.AgentStart, func() {
			if err := p.notify(MethodHookAgentStart, nil); err != nil {
				p.reportError("hook/agent_start", err)
			}
		})
	}

	if p.subscribedHooks["agent_end"] {
		hooks.AgentEnd = append(hooks.AgentEnd, func(event *AgentEndHookEvent) {
			msgsJSON := marshalMessages(event.Messages)
			wireEvent := WireAgentEndHookEvent{Messages: msgsJSON}
			if err := p.notify(MethodHookAgentEnd, wireEvent); err != nil {
				p.reportError("hook/agent_end", err)
			}
		})
	}

	if p.subscribedHooks["turn_start"] {
		hooks.TurnStart = append(hooks.TurnStart, func() {
			if err := p.notify(MethodHookTurnStart, nil); err != nil {
				p.reportError("hook/turn_start", err)
			}
		})
	}

	if p.subscribedHooks["turn_end"] {
		hooks.TurnEnd = append(hooks.TurnEnd, func(event *TurnEndHookEvent) {
			turnJSON, _ := json.Marshal(event.TurnMessage)
			var resultsJSON []json.RawMessage
			for _, r := range event.ToolResults {
				b, _ := json.Marshal(r)
				resultsJSON = append(resultsJSON, b)
			}
			wireEvent := WireTurnEndHookEvent{TurnMessage: turnJSON, ToolResults: resultsJSON}
			if err := p.notify(MethodHookTurnEnd, wireEvent); err != nil {
				p.reportError("hook/turn_end", err)
			}
		})
	}

	if p.subscribedHooks["session_start"] {
		hooks.SessionStart = append(hooks.SessionStart, func() {
			if err := p.notify(MethodHookSessionStart, nil); err != nil {
				p.reportError("hook/session_start", err)
			}
		})
	}

	if p.subscribedHooks["session_shutdown"] {
		hooks.SessionShutdown = append(hooks.SessionShutdown, func() {
			if err := p.notify(MethodHookSessionShutdown, nil); err != nil {
				p.reportError("hook/session_shutdown", err)
			}
		})
	}

	return hooks
}

// BuildTools generates AgentTools that proxy execution to the subprocess.
func (p *ProcessExtension) BuildTools() []*AgentTool {
	var tools []*AgentTool
	for _, def := range p.toolDefs {
		toolName := def.Name // capture for closure
		tool := &AgentTool{
			Tool: ai.Tool{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Parameters,
			},
			Label: def.Label,
			Execute: func(toolCallID string, params map[string]any, signal *ai.AbortSignal, onUpdate AgentToolUpdateCallback) (*AgentToolResult, error) {
				execParams := ToolExecuteParams{
					ToolCallID: toolCallID,
					ToolName:   toolName,
					Params:     params,
				}
				resp, err := p.call(MethodToolExecute, execParams)
				if err != nil {
					return nil, fmt.Errorf("process extension tool %s: %w", toolName, err)
				}
				var wireResult ToolExecuteResult
				if err := json.Unmarshal(resp, &wireResult); err != nil {
					return nil, fmt.Errorf("parse tool result: %w", err)
				}
				result := &AgentToolResult{
					Content: unmarshalContentBlocks(wireResult.Content),
					Details: wireResult.Details,
				}
				return result, nil
			},
		}
		tools = append(tools, tool)
	}
	return tools
}

// --- Serialization helpers ---

func marshalContentBlocks(blocks []ai.ToolResultContentBlock) []json.RawMessage {
	var result []json.RawMessage
	for _, b := range blocks {
		data, err := json.Marshal(b)
		if err != nil {
			continue
		}
		result = append(result, data)
	}
	return result
}

func unmarshalContentBlocks(raw []json.RawMessage) []ai.ToolResultContentBlock {
	var result []ai.ToolResultContentBlock
	for _, r := range raw {
		var block ai.ToolResultContentBlock
		if json.Unmarshal(r, &block) == nil {
			result = append(result, block)
		}
	}
	return result
}

func marshalMessages(msgs []AgentMessage) []json.RawMessage {
	var result []json.RawMessage
	for _, m := range msgs {
		data, err := json.Marshal(m)
		if err != nil {
			continue
		}
		result = append(result, data)
	}
	return result
}

func unmarshalMessages(raw []json.RawMessage) []AgentMessage {
	var result []AgentMessage
	for _, r := range raw {
		var msg AgentMessage
		if json.Unmarshal(r, &msg) == nil {
			result = append(result, msg)
		}
	}
	return result
}
