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
	EntryPoint  string `json:"entryPoint,omitempty"`
}

// Extension is a loaded extension that can register tools, hooks, and persist state.
type Extension struct {
	Manifest ExtensionManifest
	Dir      string
	Tools    []*AgentTool
	Hooks    *ExtensionHooks
	State    map[string]any
	OnReload func(ext *Extension) error
}

// ExtensionManager manages loading, state, and hot-reloading of extensions.
type ExtensionManager struct {
	mu         sync.RWMutex
	extensions map[string]*Extension
	session    *Session
	extDir     string
}

// NewExtensionManager creates a new extension manager.
func NewExtensionManager(extensionsDir string, session *Session) *ExtensionManager {
	return &ExtensionManager{
		extensions: make(map[string]*Extension),
		session:    session,
		extDir:     extensionsDir,
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
func (em *ExtensionManager) Reload(id string) error {
	em.mu.Lock()
	ext, ok := em.extensions[id]
	em.mu.Unlock()

	if !ok {
		return fmt.Errorf("extension %s not found", id)
	}

	if ext.OnReload != nil {
		return ext.OnReload(ext)
	}

	// Default: re-read manifest
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
