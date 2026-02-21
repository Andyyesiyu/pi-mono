package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SessionNode is a single node in the session tree.
// Each node holds messages and can have child branches.
type SessionNode struct {
	ID        string         `json:"id"`
	ParentID  string         `json:"parentId,omitempty"`
	Messages  []AgentMessage `json:"messages"`
	CreatedAt int64          `json:"createdAt"`
	Summary   string         `json:"summary,omitempty"`
}

// Session represents a persistent session with a tree structure.
// The tree allows branching from any point in the conversation.
type Session struct {
	ID            string                    `json:"id"`
	Nodes         map[string]*SessionNode   `json:"nodes"`
	ActiveNodeID  string                    `json:"activeNodeId"`
	RootNodeID    string                    `json:"rootNodeId"`
	CreatedAt     int64                     `json:"createdAt"`
	UpdatedAt     int64                     `json:"updatedAt"`
	ExtensionData map[string]map[string]any `json:"extensionData,omitempty"`
	filePath      string
}

// NewSession creates a new empty session.
func NewSession() *Session {
	id := generateID()
	rootID := generateID()
	now := time.Now().UnixMilli()

	root := &SessionNode{
		ID:        rootID,
		Messages:  []AgentMessage{},
		CreatedAt: now,
	}

	return &Session{
		ID:            id,
		Nodes:         map[string]*SessionNode{rootID: root},
		ActiveNodeID:  rootID,
		RootNodeID:    rootID,
		CreatedAt:     now,
		UpdatedAt:     now,
		ExtensionData: make(map[string]map[string]any),
	}
}

// ActiveNode returns the currently active node.
func (s *Session) ActiveNode() *SessionNode {
	return s.Nodes[s.ActiveNodeID]
}

// Messages returns all messages from root to the active node.
func (s *Session) Messages() []AgentMessage {
	path := s.pathToNode(s.ActiveNodeID)
	var msgs []AgentMessage
	for _, nodeID := range path {
		node := s.Nodes[nodeID]
		msgs = append(msgs, node.Messages...)
	}
	return msgs
}

// AppendMessage adds a message to the active node.
func (s *Session) AppendMessage(msg AgentMessage) {
	node := s.Nodes[s.ActiveNodeID]
	node.Messages = append(node.Messages, msg)
	s.UpdatedAt = time.Now().UnixMilli()
}

// AppendMessages adds multiple messages to the active node.
func (s *Session) AppendMessages(msgs []AgentMessage) {
	node := s.Nodes[s.ActiveNodeID]
	node.Messages = append(node.Messages, msgs...)
	s.UpdatedAt = time.Now().UnixMilli()
}

// Branch creates a new child branch from the active node at the given message index.
// Messages after the index stay in the current node; the branch starts empty.
// Returns the new node ID.
func (s *Session) Branch(atMessageIndex int) string {
	parent := s.Nodes[s.ActiveNodeID]

	newID := generateID()
	now := time.Now().UnixMilli()

	newNode := &SessionNode{
		ID:        newID,
		ParentID:  s.ActiveNodeID,
		CreatedAt: now,
		Messages:  []AgentMessage{},
	}

	// If branching mid-conversation, truncate parent and copy remaining to new node
	if atMessageIndex >= 0 && atMessageIndex < len(parent.Messages) {
		// Keep messages up to index in parent, rest go to branch
		newNode.Messages = make([]AgentMessage, len(parent.Messages)-atMessageIndex)
		copy(newNode.Messages, parent.Messages[atMessageIndex:])
		parent.Messages = parent.Messages[:atMessageIndex]
	}

	s.Nodes[newID] = newNode
	s.ActiveNodeID = newID
	s.UpdatedAt = now
	return newID
}

// BranchEmpty creates a new empty branch from the active node.
// Returns the new node ID.
func (s *Session) BranchEmpty() string {
	newID := generateID()
	now := time.Now().UnixMilli()

	newNode := &SessionNode{
		ID:        newID,
		ParentID:  s.ActiveNodeID,
		CreatedAt: now,
		Messages:  []AgentMessage{},
	}

	s.Nodes[newID] = newNode
	s.ActiveNodeID = newID
	s.UpdatedAt = now
	return newID
}

// Navigate switches the active node. Returns error if node doesn't exist.
func (s *Session) Navigate(nodeID string) error {
	if _, ok := s.Nodes[nodeID]; !ok {
		return fmt.Errorf("node %s not found", nodeID)
	}
	s.ActiveNodeID = nodeID
	s.UpdatedAt = time.Now().UnixMilli()
	return nil
}

// Rewind moves back to the parent node. Returns error if already at root.
func (s *Session) Rewind() error {
	node := s.Nodes[s.ActiveNodeID]
	if node.ParentID == "" {
		return fmt.Errorf("already at root node")
	}
	s.ActiveNodeID = node.ParentID
	s.UpdatedAt = time.Now().UnixMilli()
	return nil
}

// Children returns the IDs of direct children of the given node.
func (s *Session) Children(nodeID string) []string {
	var children []string
	for id, node := range s.Nodes {
		if node.ParentID == nodeID {
			children = append(children, id)
		}
	}
	return children
}

// SetExtensionState stores arbitrary state for an extension.
func (s *Session) SetExtensionState(extensionID string, state map[string]any) {
	if s.ExtensionData == nil {
		s.ExtensionData = make(map[string]map[string]any)
	}
	s.ExtensionData[extensionID] = state
	s.UpdatedAt = time.Now().UnixMilli()
}

// GetExtensionState retrieves state for an extension.
func (s *Session) GetExtensionState(extensionID string) map[string]any {
	if s.ExtensionData == nil {
		return nil
	}
	return s.ExtensionData[extensionID]
}

// DeleteExtensionState removes state for an extension.
func (s *Session) DeleteExtensionState(extensionID string) {
	if s.ExtensionData != nil {
		delete(s.ExtensionData, extensionID)
		s.UpdatedAt = time.Now().UnixMilli()
	}
}

// Save persists the session to disk. If no path was set, uses the sessions directory.
func (s *Session) Save(sessionsDir string) error {
	if s.filePath == "" {
		s.filePath = filepath.Join(sessionsDir, s.ID+".json")
	}

	if err := os.MkdirAll(filepath.Dir(s.filePath), 0755); err != nil {
		return fmt.Errorf("create sessions directory: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	if err := os.WriteFile(s.filePath, data, 0644); err != nil {
		return fmt.Errorf("write session file: %w", err)
	}

	return nil
}

// FilePath returns the path the session was loaded from or will be saved to.
func (s *Session) FilePath() string {
	return s.filePath
}

// LoadSession loads a session from a file.
func LoadSession(filePath string) (*Session, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read session file: %w", err)
	}

	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}

	s.filePath = filePath
	if s.ExtensionData == nil {
		s.ExtensionData = make(map[string]map[string]any)
	}
	return &s, nil
}

// LoadSessionByID loads a session by ID from the sessions directory.
func LoadSessionByID(sessionsDir, sessionID string) (*Session, error) {
	return LoadSession(filepath.Join(sessionsDir, sessionID+".json"))
}

// ListSessions returns all session IDs in the given directory.
func ListSessions(sessionsDir string) ([]string, error) {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sessions directory: %w", err)
	}

	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) == ".json" {
			ids = append(ids, name[:len(name)-5])
		}
	}
	return ids, nil
}

// pathToNode returns the list of node IDs from root to the given node.
func (s *Session) pathToNode(nodeID string) []string {
	var path []string
	current := nodeID
	for current != "" {
		path = append([]string{current}, path...)
		node := s.Nodes[current]
		if node == nil {
			break
		}
		current = node.ParentID
	}
	return path
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
