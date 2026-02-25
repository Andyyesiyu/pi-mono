package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ===== Hot-reload tests =====

// TestReloadJSONRPCExtension starts a JSON-RPC extension in "basic" mode,
// then rewrites the manifest to use "blocker" mode, reloads, and verifies
// the new behavior takes effect.
func TestReloadJSONRPCExtension(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	dir := createTestExtScript(t, "basic")

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

	// Before reload: basic mode allows Bash.
	runner := em.NewHookRunner()
	result := runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-1",
		ToolName:   "Bash",
		Input:      map[string]any{"command": "ls"},
	})
	if result != nil && result.Block {
		t.Fatal("basic mode should not block Bash")
	}

	// Rewrite the entry script to blocker mode.
	testBinary, _ := os.Executable()
	scriptPath := filepath.Join(dir, "extension")
	script := "#!/bin/sh\nexec env PI_TEST_EXT_MODE=blocker " + testBinary + " -test.run=^$\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	// Reload.
	if err := em.Reload(ext.Manifest.ID); err != nil {
		t.Fatal("reload failed:", err)
	}

	// After reload: blocker mode blocks Bash.
	runner = em.NewHookRunner()
	result = runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-2",
		ToolName:   "Bash",
		Input:      map[string]any{"command": "ls"},
	})
	if result == nil || !result.Block {
		t.Fatal("after reload, blocker mode should block Bash")
	}
}

// TestReloadGRPCExtension starts a gRPC extension in "basic" mode,
// rewrites to "blocker" mode, reloads, and verifies.
func TestReloadGRPCExtension(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	dir := createGRPCTestExtScript(t, "basic")

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

	// Before reload: basic mode allows Bash.
	runner := em.NewHookRunner()
	result := runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-1",
		ToolName:   "Bash",
		Input:      map[string]any{"command": "ls"},
	})
	if result != nil && result.Block {
		t.Fatal("basic mode should not block Bash")
	}

	// Rewrite entry script to blocker mode.
	testBinary, _ := os.Executable()
	scriptPath := filepath.Join(dir, "extension")
	script := "#!/bin/sh\nexec env PI_TEST_GRPC_EXT_MODE=blocker " + testBinary + " -test.run=^$\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	// Reload.
	if err := em.Reload(ext.Manifest.ID); err != nil {
		t.Fatal("reload failed:", err)
	}

	// After reload: blocker mode blocks Bash.
	runner = em.NewHookRunner()
	result = runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-2",
		ToolName:   "Bash",
		Input:      map[string]any{"command": "ls"},
	})
	if result == nil || !result.Block {
		t.Fatal("after reload, blocker mode should block Bash via gRPC")
	}
}

// TestReloadPreservesState verifies that in-memory state carries over across reloads.
func TestReloadPreservesState(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	dir := createTestExtScript(t, "state_updater")

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

	// Wait for state notification.
	time.Sleep(150 * time.Millisecond)

	if ext.State["initialized_at"] != "2025-01-01" {
		t.Fatalf("expected initialized_at before reload, got: %v", ext.State)
	}

	// Set additional state manually.
	ext.State["custom_key"] = "custom_value"

	// Reload.
	if err := em.Reload(ext.Manifest.ID); err != nil {
		t.Fatal("reload failed:", err)
	}

	// Verify state was preserved (carried over, not cleared).
	if ext.State["custom_key"] != "custom_value" {
		t.Fatalf("expected custom_key to be preserved after reload, got: %v", ext.State)
	}
}

// TestReloadManifestChange verifies that manifest changes on disk are picked up during reload.
func TestReloadManifestChange(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	dir := createTestExtScript(t, "basic")

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

	originalName := ext.Manifest.Name

	// Rewrite manifest with a new name and description.
	manifest := ExtensionManifest{
		ID:          ext.Manifest.ID,
		Name:        "Updated Extension Name",
		Description: "New description after reload",
		Type:        ProcessExtensionType,
		EntryPoint:  "extension",
	}
	manifestData, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestData, 0644); err != nil {
		t.Fatal(err)
	}

	// Reload.
	if err := em.Reload(ext.Manifest.ID); err != nil {
		t.Fatal("reload failed:", err)
	}

	// Verify manifest was updated.
	if ext.Manifest.Name == originalName {
		t.Fatal("expected manifest name to change after reload")
	}
	if ext.Manifest.Name != "Updated Extension Name" {
		t.Fatalf("expected updated name, got: %s", ext.Manifest.Name)
	}
	if ext.Manifest.Description != "New description after reload" {
		t.Fatalf("expected updated description, got: %s", ext.Manifest.Description)
	}
}

// TestReloadAllExtensions tests ReloadAll with multiple process extensions.
func TestReloadAllExtensions(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	// Create two JSON-RPC extensions, both starting in basic mode.
	dir1 := createTestExtScript(t, "basic")
	dir2 := createTestExtScript(t, "basic")

	// Give them unique IDs via manifest rewrite.
	rewriteManifestID(t, dir1, "ext-reload-1")
	rewriteManifestID(t, dir2, "ext-reload-2")

	em := NewExtensionManager("", nil)

	ext1, err := loadExtensionManifest(dir1)
	if err != nil {
		t.Fatal(err)
	}
	ext2, err := loadExtensionManifest(dir2)
	if err != nil {
		t.Fatal(err)
	}
	em.Register(ext1)
	em.Register(ext2)

	if err := em.StartAllProcessExtensions(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { em.StopProcessExtensions() })

	// Switch ext1 to blocker mode.
	testBinary, _ := os.Executable()
	script := "#!/bin/sh\nexec env PI_TEST_EXT_MODE=blocker " + testBinary + " -test.run=^$\n"
	if err := os.WriteFile(filepath.Join(dir1, "extension"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	// ReloadAll.
	if err := em.ReloadAll(); err != nil {
		t.Fatal("ReloadAll failed:", err)
	}

	// ext1 should now block Bash.
	runner := em.NewHookRunner()
	result := runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-1",
		ToolName:   "Bash",
		Input:      map[string]any{"command": "ls"},
	})
	if result == nil || !result.Block {
		t.Fatal("ext1 should block Bash after ReloadAll")
	}
}

// TestReloadWithSupervisor verifies that the supervisor is paused during reload
// and re-created after.
func TestReloadWithSupervisor(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	dir := createTestExtScript(t, "basic")

	em := NewExtensionManager("", nil)
	em.SupervisorConfig = &DefaultSupervisorConfig

	ext, err := loadExtensionManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	em.Register(ext)

	if err := em.StartAllProcessExtensions(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { em.StopProcessExtensions() })

	// Verify supervisor was created.
	em.mu.RLock()
	_, hasSupervisor := em.supervisors[ext.Manifest.ID]
	em.mu.RUnlock()
	if !hasSupervisor {
		t.Fatal("expected supervisor to be created")
	}

	// Reload.
	if err := em.Reload(ext.Manifest.ID); err != nil {
		t.Fatal("reload failed:", err)
	}

	// Verify supervisor is re-created (not the same object).
	em.mu.RLock()
	_, hasSupervisorAfter := em.supervisors[ext.Manifest.ID]
	em.mu.RUnlock()
	if !hasSupervisorAfter {
		t.Fatal("expected supervisor to be re-created after reload")
	}

	// Extension should still be running.
	if ext.Process == nil || !ext.Process.IsRunning() {
		t.Fatal("expected extension to be running after reload with supervisor")
	}
}

// TestReloadErrorHandlerWired verifies that OnError is properly wired after reload.
func TestReloadErrorHandlerWired(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	dir := createTestExtScript(t, "basic")

	var mu sync.Mutex
	var errors []string
	errorHandler := func(extID, op string, err error) {
		mu.Lock()
		errors = append(errors, extID+":"+op+":"+err.Error())
		mu.Unlock()
	}

	em := NewExtensionManager("", nil)
	em.OnError = errorHandler

	ext, err := loadExtensionManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	em.Register(ext)

	if err := em.StartProcessExtension(ext.Manifest.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { em.StopProcessExtensions() })

	// Verify OnError is set on the process.
	if ext.Process.OnError == nil {
		t.Fatal("expected OnError to be set on initial start")
	}

	// Reload.
	if err := em.Reload(ext.Manifest.ID); err != nil {
		t.Fatal("reload failed:", err)
	}

	// After reload, OnError should still be wired.
	if ext.Process.OnError == nil {
		t.Fatal("expected OnError to be set after reload")
	}
}

// TestReloadNonProcessExtension verifies Reload works for in-process extensions
// (just re-reads manifest).
func TestReloadNonProcessExtension(t *testing.T) {
	dir := t.TempDir()

	manifest := ExtensionManifest{
		ID:   "in-proc-ext",
		Name: "Original Name",
	}
	manifestData, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestData, 0644); err != nil {
		t.Fatal(err)
	}

	ext := &Extension{
		Manifest: manifest,
		Dir:      dir,
		State:    make(map[string]any),
	}

	em := NewExtensionManager("", nil)
	em.Register(ext)

	// Rewrite manifest.
	manifest.Name = "Updated In-Proc Name"
	manifestData, _ = json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestData, 0644); err != nil {
		t.Fatal(err)
	}

	if err := em.Reload("in-proc-ext"); err != nil {
		t.Fatal("reload failed:", err)
	}

	if ext.Manifest.Name != "Updated In-Proc Name" {
		t.Fatalf("expected updated name, got: %s", ext.Manifest.Name)
	}
}

// TestReloadNotFound verifies Reload returns an error for unknown extension IDs.
func TestReloadNotFound(t *testing.T) {
	em := NewExtensionManager("", nil)
	err := em.Reload("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent extension")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}
}

// TestReloadCustomHandler verifies OnReload takes priority over default behavior.
func TestReloadCustomHandler(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	dir := createTestExtScript(t, "basic")

	em := NewExtensionManager("", nil)
	ext, err := loadExtensionManifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	customCalled := false
	ext.OnReload = func(e *Extension) error {
		customCalled = true
		return nil
	}

	em.Register(ext)

	if err := em.StartProcessExtension(ext.Manifest.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { em.StopProcessExtensions() })

	if err := em.Reload(ext.Manifest.ID); err != nil {
		t.Fatal("reload failed:", err)
	}

	if !customCalled {
		t.Fatal("expected custom OnReload handler to be called")
	}
}

// TestReloadProtocolSwitch verifies switching from JSON-RPC to gRPC during reload.
func TestReloadProtocolSwitch(t *testing.T) {
	if _, err := os.Executable(); err != nil {
		t.Skip("cannot determine test executable")
	}

	// Start with JSON-RPC basic mode.
	dir := createTestExtScript(t, "basic")

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

	// Verify it started as JSON-RPC.
	if ext.Process == nil {
		t.Fatal("expected JSON-RPC process")
	}
	if ext.GRPCProcess != nil {
		t.Fatal("expected no gRPC process")
	}

	// Rewrite to gRPC blocker mode.
	testBinary, _ := os.Executable()
	script := "#!/bin/sh\nexec env PI_TEST_GRPC_EXT_MODE=blocker " + testBinary + " -test.run=^$\n"
	if err := os.WriteFile(filepath.Join(dir, "extension"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	// Also update manifest to gRPC protocol.
	manifest := ExtensionManifest{
		ID:         ext.Manifest.ID,
		Name:       ext.Manifest.Name,
		Type:       ProcessExtensionType,
		Protocol:   GRPCProtocol,
		EntryPoint: "extension",
	}
	manifestData, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestData, 0644); err != nil {
		t.Fatal(err)
	}

	// Reload.
	if err := em.Reload(ext.Manifest.ID); err != nil {
		t.Fatal("reload failed:", err)
	}

	// Verify it's now running as gRPC.
	if ext.GRPCProcess == nil {
		t.Fatal("expected gRPC process after protocol switch")
	}

	// Verify the blocker behavior works.
	runner := em.NewHookRunner()
	result := runner.RunToolCall(&ToolCallHookEvent{
		ToolCallID: "call-1",
		ToolName:   "Bash",
		Input:      map[string]any{"command": "ls"},
	})
	if result == nil || !result.Block {
		t.Fatal("expected Bash blocked after protocol switch to gRPC blocker")
	}
}

// ===== Helpers =====

func rewriteManifestID(t *testing.T, dir, newID string) {
	t.Helper()
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest ExtensionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.ID = newID
	newData, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, newData, 0644); err != nil {
		t.Fatal(err)
	}
}
