package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

func TestNewSession(t *testing.T) {
	s := NewSession()
	if s.ID == "" {
		t.Fatal("session ID should not be empty")
	}
	if s.RootNodeID == "" {
		t.Fatal("root node ID should not be empty")
	}
	if s.ActiveNodeID != s.RootNodeID {
		t.Fatal("active node should be root")
	}
	if len(s.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(s.Nodes))
	}
}

func TestSessionAppendAndMessages(t *testing.T) {
	s := NewSession()

	msg1 := NewAgentMessageFromUser(&ai.UserMessage{
		Role:    "user",
		Content: ai.UserContent{Text: "hello"},
	})
	msg2 := NewAgentMessageFromUser(&ai.UserMessage{
		Role:    "user",
		Content: ai.UserContent{Text: "world"},
	})

	s.AppendMessage(msg1)
	s.AppendMessage(msg2)

	msgs := s.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
}

func TestSessionBranch(t *testing.T) {
	s := NewSession()

	msg1 := NewAgentMessageFromUser(&ai.UserMessage{
		Role:    "user",
		Content: ai.UserContent{Text: "msg1"},
	})
	msg2 := NewAgentMessageFromUser(&ai.UserMessage{
		Role:    "user",
		Content: ai.UserContent{Text: "msg2"},
	})
	msg3 := NewAgentMessageFromUser(&ai.UserMessage{
		Role:    "user",
		Content: ai.UserContent{Text: "msg3"},
	})

	s.AppendMessage(msg1)
	s.AppendMessage(msg2)
	s.AppendMessage(msg3)

	// Branch at message index 1 (after msg1)
	rootID := s.ActiveNodeID
	branchID := s.Branch(1)

	// Active should be the new branch
	if s.ActiveNodeID != branchID {
		t.Fatal("active node should be branch")
	}

	// Root should have only msg1
	rootNode := s.Nodes[rootID]
	if len(rootNode.Messages) != 1 {
		t.Fatalf("root should have 1 message, got %d", len(rootNode.Messages))
	}

	// Branch should have msg2 and msg3
	branchNode := s.Nodes[branchID]
	if len(branchNode.Messages) != 2 {
		t.Fatalf("branch should have 2 messages, got %d", len(branchNode.Messages))
	}

	// Full path from branch should have all 3
	msgs := s.Messages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages from branch, got %d", len(msgs))
	}
}

func TestSessionBranchEmpty(t *testing.T) {
	s := NewSession()

	msg := NewAgentMessageFromUser(&ai.UserMessage{
		Role:    "user",
		Content: ai.UserContent{Text: "original"},
	})
	s.AppendMessage(msg)

	rootID := s.ActiveNodeID
	newID := s.BranchEmpty()

	if s.ActiveNodeID != newID {
		t.Fatal("should be on new branch")
	}

	newNode := s.Nodes[newID]
	if newNode.ParentID != rootID {
		t.Fatal("parent should be root")
	}
	if len(newNode.Messages) != 0 {
		t.Fatal("branch should be empty")
	}

	// Messages from new branch should include parent messages
	msgs := s.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message (from parent), got %d", len(msgs))
	}
}

func TestSessionRewind(t *testing.T) {
	s := NewSession()
	rootID := s.ActiveNodeID

	s.BranchEmpty()
	if s.ActiveNodeID == rootID {
		t.Fatal("should be on branch")
	}

	err := s.Rewind()
	if err != nil {
		t.Fatal(err)
	}
	if s.ActiveNodeID != rootID {
		t.Fatal("should be back on root")
	}

	// Can't rewind past root
	err = s.Rewind()
	if err == nil {
		t.Fatal("should error when rewinding past root")
	}
}

func TestSessionNavigate(t *testing.T) {
	s := NewSession()

	branchID := s.BranchEmpty()
	s.Rewind()

	err := s.Navigate(branchID)
	if err != nil {
		t.Fatal(err)
	}
	if s.ActiveNodeID != branchID {
		t.Fatal("should be on branch")
	}

	err = s.Navigate("nonexistent")
	if err == nil {
		t.Fatal("should error for nonexistent node")
	}
}

func TestSessionChildren(t *testing.T) {
	s := NewSession()
	rootID := s.ActiveNodeID

	b1 := s.BranchEmpty()
	s.Rewind()
	b2 := s.BranchEmpty()
	s.Rewind()

	children := s.Children(rootID)
	if len(children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(children))
	}

	found := map[string]bool{}
	for _, c := range children {
		found[c] = true
	}
	if !found[b1] || !found[b2] {
		t.Fatal("children should include both branches")
	}
}

func TestSessionSaveLoad(t *testing.T) {
	dir := t.TempDir()

	s := NewSession()
	s.AppendMessage(NewAgentMessageFromUser(&ai.UserMessage{
		Role:    "user",
		Content: ai.UserContent{Text: "test message"},
	}))
	s.SetExtensionState("myext", map[string]any{"count": float64(42)})

	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}

	// Check file exists
	filePath := filepath.Join(dir, s.ID+".json")
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("session file should exist: %s", err)
	}

	// Load it back
	loaded, err := LoadSession(filePath)
	if err != nil {
		t.Fatal(err)
	}

	if loaded.ID != s.ID {
		t.Fatalf("ID mismatch: %s != %s", loaded.ID, s.ID)
	}
	if loaded.ActiveNodeID != s.ActiveNodeID {
		t.Fatal("active node mismatch")
	}

	msgs := loaded.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	state := loaded.GetExtensionState("myext")
	if state == nil {
		t.Fatal("extension state should be preserved")
	}
	if state["count"] != float64(42) {
		t.Fatalf("extension state value mismatch: %v", state["count"])
	}
}

func TestSessionLoadByID(t *testing.T) {
	dir := t.TempDir()

	s := NewSession()
	s.Save(dir)

	loaded, err := LoadSessionByID(dir, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != s.ID {
		t.Fatal("ID mismatch")
	}
}

func TestListSessions(t *testing.T) {
	dir := t.TempDir()

	s1 := NewSession()
	s1.Save(dir)
	s2 := NewSession()
	s2.Save(dir)

	ids, err := ListSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(ids))
	}
}

func TestSessionExtensionState(t *testing.T) {
	s := NewSession()

	// Initially nil
	state := s.GetExtensionState("ext1")
	if state != nil {
		t.Fatal("should be nil initially")
	}

	s.SetExtensionState("ext1", map[string]any{"key": "value"})
	state = s.GetExtensionState("ext1")
	if state["key"] != "value" {
		t.Fatal("state not stored")
	}

	s.DeleteExtensionState("ext1")
	state = s.GetExtensionState("ext1")
	if state != nil {
		t.Fatal("state should be deleted")
	}
}
