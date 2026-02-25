package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ExtensionManifest describes an extension loaded from disk.
type ExtensionManifest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Type        string `json:"type,omitempty"`       // "process" for subprocess extensions
	Protocol    string `json:"protocol,omitempty"`    // "grpc" or "jsonrpc" (default: "jsonrpc")
	EntryPoint  string `json:"entryPoint,omitempty"` // executable path for process extensions
}

// Extension is a loaded extension that can register tools, hooks, and persist state.
type Extension struct {
	Manifest ExtensionManifest
	Dir      string
	Tools    []*AgentTool
	Hooks    *ExtensionHooks
	State    map[string]any
	OnReload func(ext *Extension) error

	// Process is non-nil for JSON-RPC subprocess extensions (Protocol="jsonrpc").
	Process *ProcessExtension

	// GRPCProcess is non-nil for gRPC subprocess extensions (Protocol="grpc").
	GRPCProcess *GRPCProcessExtension
}

// ExtensionManager manages loading, state, and hot-reloading of extensions.
type ExtensionManager struct {
	mu          sync.RWMutex
	extensions  map[string]*Extension
	supervisors map[string]*extensionSupervisor
	session     *Session
	extDir      string

	// OnError is called when any extension reports an error (hook failures,
	// supervision events, etc.). Set before starting extensions.
	OnError ExtensionErrorHandler

	// OnLog is called when a gRPC extension sends a log message via HostService.
	// Set before starting extensions.
	OnLog ExtensionLogHandler

	// SupervisorConfig controls auto-restart behavior.
	// Zero value disables supervision.
	SupervisorConfig *SupervisorConfig
}

// NewExtensionManager creates a new extension manager.
func NewExtensionManager(extensionsDir string, session *Session) *ExtensionManager {
	return &ExtensionManager{
		extensions:  make(map[string]*Extension),
		supervisors: make(map[string]*extensionSupervisor),
		session:     session,
		extDir:      extensionsDir,
	}
}

// Register adds an extension manually (without loading from disk).
func (em *ExtensionManager) Register(ext *Extension) {
	em.mu.Lock()
	defer em.mu.Unlock()

	em.extensions[ext.Manifest.ID] = ext

	// Restore persisted state from session
	if em.session != nil {
		if state := em.session.GetExtensionState(ext.Manifest.ID); state != nil {
			ext.State = state
		}
	}
}

// Unregister removes an extension.
func (em *ExtensionManager) Unregister(id string) {
	em.mu.Lock()
	defer em.mu.Unlock()
	delete(em.extensions, id)
}

// Get returns an extension by ID.
func (em *ExtensionManager) Get(id string) *Extension {
	em.mu.RLock()
	defer em.mu.RUnlock()
	return em.extensions[id]
}

// All returns all registered extensions.
func (em *ExtensionManager) All() []*Extension {
	em.mu.RLock()
	defer em.mu.RUnlock()

	result := make([]*Extension, 0, len(em.extensions))
	for _, ext := range em.extensions {
		result = append(result, ext)
	}
	return result
}

// AllTools returns all tools from all registered extensions.
func (em *ExtensionManager) AllTools() []*AgentTool {
	em.mu.RLock()
	defer em.mu.RUnlock()

	var tools []*AgentTool
	for _, ext := range em.extensions {
		tools = append(tools, ext.Tools...)
	}
	return tools
}

// NewHookRunner creates a HookRunner that dispatches to all registered extensions.
func (em *ExtensionManager) NewHookRunner() *HookRunner {
	return NewHookRunner(func() []*Extension {
		return em.All()
	})
}

// SetState updates the state for an extension and persists it to the session.
func (em *ExtensionManager) SetState(extensionID string, state map[string]any) error {
	em.mu.Lock()
	defer em.mu.Unlock()

	ext, ok := em.extensions[extensionID]
	if !ok {
		return fmt.Errorf("extension %s not found", extensionID)
	}

	ext.State = state
	if em.session != nil {
		em.session.SetExtensionState(extensionID, state)
	}
	return nil
}

// GetState retrieves the persisted state for an extension.
func (em *ExtensionManager) GetState(extensionID string) map[string]any {
	em.mu.RLock()
	defer em.mu.RUnlock()

	ext, ok := em.extensions[extensionID]
	if !ok {
		return nil
	}
	return ext.State
}

// LoadFromDir loads all extensions from the extensions directory.
// Each subdirectory containing a manifest.json is treated as an extension.
func (em *ExtensionManager) LoadFromDir() error {
	if em.extDir == "" {
		return nil
	}

	entries, err := os.ReadDir(em.extDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read extensions directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		extDir := filepath.Join(em.extDir, entry.Name())
		manifestPath := filepath.Join(extDir, "manifest.json")

		if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
			continue
		}

		ext, err := loadExtensionManifest(extDir)
		if err != nil {
			return fmt.Errorf("load extension %s: %w", entry.Name(), err)
		}

		em.Register(ext)
	}

	return nil
}

// Reload reloads a specific extension by ID.
// For process extensions, this performs a full hot-reload:
//  1. Pause supervisor (prevent crash-restart during reload)
//  2. Stop the running subprocess
//  3. Re-read manifest from disk
//  4. Start new subprocess with preserved state
//  5. Rebuild hooks/tools from the new process
//  6. Resume supervisor
func (em *ExtensionManager) Reload(id string) error {
	em.mu.Lock()
	ext, ok := em.extensions[id]
	em.mu.Unlock()

	if !ok {
		return fmt.Errorf("extension %s not found", id)
	}

	// Custom reload handler takes priority.
	if ext.OnReload != nil {
		return ext.OnReload(ext)
	}

	isProcess := ext.Manifest.Type == ProcessExtensionType &&
		(ext.Process != nil || ext.GRPCProcess != nil)

	if isProcess {
		return em.hotReloadProcessExtension(ext)
	}

	// In-process extension: just re-read manifest.
	if ext.Dir != "" {
		updated, err := loadExtensionManifest(ext.Dir)
		if err != nil {
			return err
		}
		em.mu.Lock()
		ext.Manifest = updated.Manifest
		em.mu.Unlock()
	}

	return nil
}

// hotReloadProcessExtension performs a zero-downtime reload of a process extension.
func (em *ExtensionManager) hotReloadProcessExtension(ext *Extension) error {
	id := ext.Manifest.ID

	// 1. Pause supervisor to prevent crash-restart during reload.
	em.mu.Lock()
	sv := em.supervisors[id]
	if sv != nil {
		sv.Stop()
		delete(em.supervisors, id)
	}
	em.mu.Unlock()

	// 2. Save current state before stopping.
	savedState := ext.State

	// 3. Stop the old process.
	if ext.Process != nil && ext.Process.IsRunning() {
		ext.Process.Stop()
	}
	if ext.GRPCProcess != nil && ext.GRPCProcess.IsRunning() {
		ext.GRPCProcess.Stop()
	}
	ext.Process = nil
	ext.GRPCProcess = nil

	// 4. Re-read manifest from disk (may have new entryPoint, protocol, hooks).
	if ext.Dir != "" {
		updated, err := loadExtensionManifest(ext.Dir)
		if err != nil {
			return fmt.Errorf("reload manifest: %w", err)
		}
		em.mu.Lock()
		ext.Manifest = updated.Manifest
		em.mu.Unlock()
	}

	// 5. Restore state (not from disk — carry over from the running session).
	ext.State = savedState

	// 6. Start the new process.
	if err := em.StartProcessExtension(id); err != nil {
		return fmt.Errorf("restart after reload: %w", err)
	}

	// 7. Re-create supervisor if configured.
	if em.SupervisorConfig != nil {
		newSv := newExtensionSupervisor(ext, *em.SupervisorConfig, em.OnError)
		newSv.Start()
		em.mu.Lock()
		em.supervisors[id] = newSv
		em.mu.Unlock()
	}

	return nil
}

// ReloadAll reloads all extensions.
func (em *ExtensionManager) ReloadAll() error {
	em.mu.RLock()
	ids := make([]string, 0, len(em.extensions))
	for id := range em.extensions {
		ids = append(ids, id)
	}
	em.mu.RUnlock()

	for _, id := range ids {
		if err := em.Reload(id); err != nil {
			return fmt.Errorf("reload %s: %w", id, err)
		}
	}
	return nil
}

// StartProcessExtension starts a process extension by ID.
// This launches the subprocess, performs the handshake, and wires up
// hooks and tools from the subprocess into the Extension struct.
//
// The protocol is determined by manifest.Protocol:
//   - "grpc": gRPC over localhost TCP (extension prints handshake to stdout)
//   - "jsonrpc" or "": JSON-RPC 2.0 over stdin/stdout (default)
func (em *ExtensionManager) StartProcessExtension(id string) error {
	em.mu.RLock()
	ext, ok := em.extensions[id]
	em.mu.RUnlock()

	if !ok {
		return fmt.Errorf("extension %s not found", id)
	}
	if ext.Manifest.Type != ProcessExtensionType {
		return fmt.Errorf("extension %s is not a process extension", id)
	}

	if ext.Manifest.Protocol == GRPCProtocol {
		return em.startGRPCExtension(ext)
	}
	return em.startJSONRPCExtension(ext)
}

func (em *ExtensionManager) startJSONRPCExtension(ext *Extension) error {
	proc := NewProcessExtension(ext)
	proc.OnError = em.OnError
	if err := proc.Start(); err != nil {
		return err
	}
	ext.Process = proc
	ext.Hooks = proc.BuildHooks()
	ext.Tools = proc.BuildTools()
	return nil
}

func (em *ExtensionManager) startGRPCExtension(ext *Extension) error {
	proc := NewGRPCProcessExtension(ext)
	proc.OnError = em.OnError
	proc.OnLog = em.OnLog
	if err := proc.Start(); err != nil {
		return err
	}
	ext.GRPCProcess = proc
	ext.Hooks = proc.BuildHooks()
	ext.Tools = proc.BuildTools()
	return nil
}

// StartAllProcessExtensions starts all registered process extensions.
// Non-process extensions are silently skipped.
// If SupervisorConfig is set, each extension gets auto-restart supervision.
func (em *ExtensionManager) StartAllProcessExtensions() error {
	em.mu.RLock()
	var processExts []*Extension
	for _, ext := range em.extensions {
		if ext.Manifest.Type == ProcessExtensionType {
			processExts = append(processExts, ext)
		}
	}
	em.mu.RUnlock()

	for _, ext := range processExts {
		if err := em.StartProcessExtension(ext.Manifest.ID); err != nil {
			return fmt.Errorf("start %s: %w", ext.Manifest.ID, err)
		}

		// Start supervisor if configured.
		if em.SupervisorConfig != nil {
			sv := newExtensionSupervisor(ext, *em.SupervisorConfig, em.OnError)
			sv.Start()
			em.mu.Lock()
			em.supervisors[ext.Manifest.ID] = sv
			em.mu.Unlock()
		}
	}
	return nil
}

// LoadAndStartFromDir loads extensions from disk and starts all process extensions.
// This is the single-call entry point for zero-friction extension setup.
func (em *ExtensionManager) LoadAndStartFromDir() error {
	if err := em.LoadFromDir(); err != nil {
		return err
	}
	return em.StartAllProcessExtensions()
}

// StopProcessExtensions stops all running process extensions and their supervisors.
func (em *ExtensionManager) StopProcessExtensions() {
	em.mu.Lock()
	// Stop supervisors first to prevent restarts during shutdown.
	for id, sv := range em.supervisors {
		sv.Stop()
		delete(em.supervisors, id)
	}
	em.mu.Unlock()

	em.mu.RLock()
	defer em.mu.RUnlock()

	for _, ext := range em.extensions {
		if ext.Process != nil && ext.Process.IsRunning() {
			ext.Process.Stop()
		}
		if ext.GRPCProcess != nil && ext.GRPCProcess.IsRunning() {
			ext.GRPCProcess.Stop()
		}
	}
}

// SaveAllState persists all extension states to the session.
func (em *ExtensionManager) SaveAllState() {
	em.mu.RLock()
	defer em.mu.RUnlock()

	if em.session == nil {
		return
	}

	for id, ext := range em.extensions {
		if ext.State != nil {
			em.session.SetExtensionState(id, ext.State)
		}
	}
}

func loadExtensionManifest(dir string) (*Extension, error) {
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var manifest ExtensionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	if manifest.ID == "" {
		manifest.ID = filepath.Base(dir)
	}

	return &Extension{
		Manifest: manifest,
		Dir:      dir,
		State:    make(map[string]any),
	}, nil
}
