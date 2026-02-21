package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

func TestExtensionManagerRegisterAndGet(t *testing.T) {
	em := NewExtensionManager("", nil)

	ext := &Extension{
		Manifest: ExtensionManifest{
			ID:   "test-ext",
			Name: "Test Extension",
		},
		State: map[string]any{},
	}

	em.Register(ext)

	got := em.Get("test-ext")
	if got == nil {
		t.Fatal("extension should be registered")
	}
	if got.Manifest.Name != "Test Extension" {
		t.Fatal("name mismatch")
	}
}

func TestExtensionManagerUnregister(t *testing.T) {
	em := NewExtensionManager("", nil)

	ext := &Extension{
		Manifest: ExtensionManifest{ID: "test-ext"},
	}
	em.Register(ext)
	em.Unregister("test-ext")

	if em.Get("test-ext") != nil {
		t.Fatal("extension should be unregistered")
	}
}

func TestExtensionManagerAllTools(t *testing.T) {
	em := NewExtensionManager("", nil)

	ext := &Extension{
		Manifest: ExtensionManifest{ID: "toolext"},
		Tools: []*AgentTool{
			{Tool: ai_tool("CustomTool", "does stuff")},
		},
	}
	em.Register(ext)

	tools := em.AllTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "CustomTool" {
		t.Fatal("tool name mismatch")
	}
}

func TestExtensionManagerStateWithSession(t *testing.T) {
	session := NewSession()
	em := NewExtensionManager("", session)

	ext := &Extension{
		Manifest: ExtensionManifest{ID: "stateful"},
		State:    map[string]any{},
	}
	em.Register(ext)

	err := em.SetState("stateful", map[string]any{"count": float64(1)})
	if err != nil {
		t.Fatal(err)
	}

	// Check state is in extension
	state := em.GetState("stateful")
	if state["count"] != float64(1) {
		t.Fatal("state not set on extension")
	}

	// Check state is in session
	sessState := session.GetExtensionState("stateful")
	if sessState["count"] != float64(1) {
		t.Fatal("state not persisted to session")
	}
}

func TestExtensionManagerRestoreState(t *testing.T) {
	session := NewSession()
	session.SetExtensionState("myext", map[string]any{"restored": true})

	em := NewExtensionManager("", session)

	ext := &Extension{
		Manifest: ExtensionManifest{ID: "myext"},
		State:    map[string]any{},
	}
	em.Register(ext)

	// State should be restored from session
	if ext.State["restored"] != true {
		t.Fatal("state should be restored from session on register")
	}
}

func TestExtensionManagerLoadFromDir(t *testing.T) {
	dir := t.TempDir()

	// Create an extension directory with manifest
	extDir := filepath.Join(dir, "my-extension")
	os.MkdirAll(extDir, 0755)

	manifest := ExtensionManifest{
		ID:          "my-extension",
		Name:        "My Extension",
		Description: "A test extension",
		Version:     "1.0.0",
	}
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(extDir, "manifest.json"), data, 0644)

	em := NewExtensionManager(dir, nil)
	if err := em.LoadFromDir(); err != nil {
		t.Fatal(err)
	}

	ext := em.Get("my-extension")
	if ext == nil {
		t.Fatal("extension should be loaded")
	}
	if ext.Manifest.Name != "My Extension" {
		t.Fatalf("name mismatch: %s", ext.Manifest.Name)
	}
	if ext.Dir != extDir {
		t.Fatal("dir mismatch")
	}
}

func TestExtensionManagerLoadSkipsNonDirs(t *testing.T) {
	dir := t.TempDir()

	// Create a plain file (not a directory)
	os.WriteFile(filepath.Join(dir, "not-an-ext.txt"), []byte("nope"), 0644)

	em := NewExtensionManager(dir, nil)
	if err := em.LoadFromDir(); err != nil {
		t.Fatal(err)
	}

	all := em.All()
	if len(all) != 0 {
		t.Fatal("should not load non-directory entries")
	}
}

func TestExtensionManagerReload(t *testing.T) {
	dir := t.TempDir()

	extDir := filepath.Join(dir, "reloadable")
	os.MkdirAll(extDir, 0755)

	manifest := ExtensionManifest{ID: "reloadable", Name: "Original"}
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(extDir, "manifest.json"), data, 0644)

	em := NewExtensionManager(dir, nil)
	em.LoadFromDir()

	// Update manifest on disk
	manifest.Name = "Updated"
	data, _ = json.Marshal(manifest)
	os.WriteFile(filepath.Join(extDir, "manifest.json"), data, 0644)

	if err := em.Reload("reloadable"); err != nil {
		t.Fatal(err)
	}

	ext := em.Get("reloadable")
	if ext.Manifest.Name != "Updated" {
		t.Fatalf("expected Updated, got %s", ext.Manifest.Name)
	}
}

func TestExtensionManagerCustomReload(t *testing.T) {
	em := NewExtensionManager("", nil)

	reloaded := false
	ext := &Extension{
		Manifest: ExtensionManifest{ID: "custom"},
		OnReload: func(e *Extension) error {
			reloaded = true
			return nil
		},
	}
	em.Register(ext)

	if err := em.Reload("custom"); err != nil {
		t.Fatal(err)
	}
	if !reloaded {
		t.Fatal("custom reload handler should have been called")
	}
}

func TestExtensionManagerSaveAllState(t *testing.T) {
	session := NewSession()
	em := NewExtensionManager("", session)

	ext1 := &Extension{
		Manifest: ExtensionManifest{ID: "e1"},
		State:    map[string]any{"a": float64(1)},
	}
	ext2 := &Extension{
		Manifest: ExtensionManifest{ID: "e2"},
		State:    map[string]any{"b": float64(2)},
	}
	em.Register(ext1)
	em.Register(ext2)

	em.SaveAllState()

	s1 := session.GetExtensionState("e1")
	s2 := session.GetExtensionState("e2")
	if s1["a"] != float64(1) {
		t.Fatal("e1 state not saved")
	}
	if s2["b"] != float64(2) {
		t.Fatal("e2 state not saved")
	}
}

func ai_tool(name, desc string) ai.Tool {
	return ai.Tool{Name: name, Description: desc}
}
