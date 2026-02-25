// Command pi is a minimal coding agent with four tools: Read, Write, Edit, Bash.
//
// Pi's philosophy is simplicity: a tiny system prompt, a small set of powerful
// tools, and an extension system that lets the agent extend itself by writing code.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nickolaev/pi-mono/packages/go-pi-agent"
	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

const systemPrompt = `You are Pi, a coding agent. You help the user by reading, writing, editing files and running commands.

You have four tools: Read, Write, Edit, Bash. Use them to accomplish tasks.

Be concise. Think step by step. When you need to explore, use Bash and Read. When you need to change code, use Edit for surgical changes or Write for new files.

If the user asks you to do something you can't do with your tools, write code or a script to accomplish it.`

func main() {
	var (
		modelID     string
		provider    string
		sessionsDir string
		sessionID   string
		extDir      string
		thinking    string
		apiKey      string
	)

	flag.StringVar(&modelID, "model", "claude-sonnet-4-20250514", "Model ID to use")
	flag.StringVar(&provider, "provider", "anthropic", "LLM provider")
	flag.StringVar(&sessionsDir, "sessions-dir", defaultSessionsDir(), "Directory for session files")
	flag.StringVar(&sessionID, "session", "", "Resume a session by ID")
	flag.StringVar(&extDir, "extensions-dir", "", "Directory containing extensions")
	flag.StringVar(&thinking, "thinking", "", "Thinking level: off, minimal, low, medium, high, xhigh")
	flag.StringVar(&apiKey, "api-key", "", "API key (defaults to env var for the provider)")
	flag.Parse()

	// Resolve working directory
	cwd, _ := os.Getwd()

	// Resolve API key from environment if not provided
	if apiKey == "" {
		apiKey = ai.GetEnvApiKey(provider)
	}
	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "Error: no API key found. Set the appropriate environment variable or use -api-key.\n")
		os.Exit(1)
	}

	// Set up model
	model := &ai.Model{
		ID:            modelID,
		Name:          modelID,
		ApiType:       resolveApiType(provider),
		Provider:      provider,
		BaseURL:       resolveBaseURL(provider),
		Reasoning:     thinking != "" && thinking != "off",
		Input:         []string{"text", "image"},
		ContextWindow: 200000,
		MaxTokens:     16384,
	}

	// Set up session
	var session *agent.Session
	if sessionID != "" {
		var err error
		session, err = agent.LoadSessionByID(sessionsDir, sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading session: %s\n", err)
			os.Exit(1)
		}
		fmt.Printf("Resumed session %s\n", session.ID)
	} else {
		session = agent.NewSession()
		fmt.Printf("New session %s\n", session.ID)
	}

	// Set up extensions
	extMgr := agent.NewExtensionManager(extDir, session)
	if extDir != "" {
		if err := extMgr.LoadFromDir(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: error loading extensions: %s\n", err)
		}
		exts := extMgr.All()
		if len(exts) > 0 {
			fmt.Printf("Loaded %d extension(s)\n", len(exts))
		}
	}

	// Build tools: core tools + extension tools
	tools := agent.CoreToolsWithDir(cwd)
	tools = append(tools, extMgr.AllTools()...)

	// Resolve thinking level
	thinkingLevel := agent.ThinkingOff
	switch thinking {
	case "minimal":
		thinkingLevel = agent.ThinkingMinimal
	case "low":
		thinkingLevel = agent.ThinkingLow
	case "medium":
		thinkingLevel = agent.ThinkingMedium
	case "high":
		thinkingLevel = agent.ThinkingHigh
	case "xhigh":
		thinkingLevel = agent.ThinkingXHigh
	}

	// Restore messages from session
	var messages []agent.AgentMessage
	if sessionID != "" {
		messages = session.Messages()
	}

	// Create agent
	a := agent.NewAgent(agent.AgentOptions{
		InitialState: &agent.AgentState{
			SystemPrompt:  systemPrompt,
			Model:         model,
			ThinkingLevel: thinkingLevel,
			Tools:         tools,
			Messages:      messages,
		},
		SessionID: session.ID,
		GetApiKey: func(p string) (string, error) {
			return apiKey, nil
		},
	})

	// Subscribe to events for output
	a.Subscribe(func(e agent.AgentEvent) {
		switch e.Type {
		case "message_update":
			if e.AssistantMessageEvent != nil {
				evt := e.AssistantMessageEvent
				switch evt.Type {
				case "text_delta":
					fmt.Print(evt.Delta)
				case "thinking_delta":
					// Suppress thinking output by default
				}
			}
		case "message_end":
			if e.EventMessage != nil && e.EventMessage.IsAssistant() {
				fmt.Println()
			}
		case "tool_execution_start":
			fmt.Printf("\n[%s] %s\n", e.ToolName, formatArgs(e.Args))
		case "tool_execution_end":
			if e.IsError {
				fmt.Printf("[%s] error\n", e.ToolName)
			} else {
				fmt.Printf("[%s] done\n", e.ToolName)
			}
		case "agent_end":
			// Save session
			extMgr.SaveAllState()
			session.AppendMessages(e.Messages)
			if err := session.Save(sessionsDir); err != nil {
				fmt.Fprintf(os.Stderr, "\nWarning: failed to save session: %s\n", err)
			}
		}
	})

	// REPL
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// Handle built-in commands
		switch {
		case input == "/quit" || input == "/exit":
			fmt.Println("Goodbye.")
			return
		case input == "/session":
			fmt.Printf("Session: %s\n", session.ID)
			fmt.Printf("Active node: %s\n", session.ActiveNodeID)
			fmt.Printf("Messages: %d\n", len(session.Messages()))
			continue
		case input == "/branch":
			nodeID := session.BranchEmpty()
			fmt.Printf("Created branch %s\n", nodeID)
			a.ClearMessages()
			continue
		case input == "/rewind":
			if err := session.Rewind(); err != nil {
				fmt.Printf("Error: %s\n", err)
			} else {
				msgs := session.Messages()
				a.ReplaceMessages(msgs)
				fmt.Printf("Rewound to %s (%d messages)\n", session.ActiveNodeID, len(msgs))
			}
			continue
		case strings.HasPrefix(input, "/nav "):
			nodeID := strings.TrimPrefix(input, "/nav ")
			if err := session.Navigate(nodeID); err != nil {
				fmt.Printf("Error: %s\n", err)
			} else {
				msgs := session.Messages()
				a.ReplaceMessages(msgs)
				fmt.Printf("Navigated to %s (%d messages)\n", nodeID, len(msgs))
			}
			continue
		case input == "/reload":
			if err := extMgr.ReloadAll(); err != nil {
				fmt.Printf("Error reloading extensions: %s\n", err)
			} else {
				tools = agent.CoreToolsWithDir(cwd)
				tools = append(tools, extMgr.AllTools()...)
				a.SetTools(tools)
				fmt.Println("Extensions reloaded.")
			}
			continue
		case input == "/help":
			printHelp()
			continue
		}

		_ = a.Prompt(input)
		a.WaitForIdle()
	}
}

func defaultSessionsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pi", "sessions")
}

func resolveApiType(provider string) string {
	switch provider {
	case "anthropic":
		return string(ai.ApiAnthropicMessages)
	case "openai":
		return string(ai.ApiOpenAICompletions)
	case "google":
		return string(ai.ApiGoogleGenerativeAI)
	case "amazon-bedrock":
		return string(ai.ApiBedrockConverseStream)
	default:
		return string(ai.ApiOpenAICompletions)
	}
}

func resolveBaseURL(provider string) string {
	switch provider {
	case "anthropic":
		return "https://api.anthropic.com"
	case "openai":
		return "https://api.openai.com/v1"
	case "google":
		return "https://generativelanguage.googleapis.com"
	default:
		return ""
	}
}

func formatArgs(args any) string {
	if args == nil {
		return ""
	}
	m, ok := args.(map[string]any)
	if !ok {
		return fmt.Sprintf("%v", args)
	}

	// Show a compact summary
	if cmd, ok := m["command"]; ok {
		s := fmt.Sprintf("%v", cmd)
		if len(s) > 80 {
			return s[:80] + "..."
		}
		return s
	}
	if fp, ok := m["file_path"]; ok {
		return fmt.Sprintf("%v", fp)
	}
	return fmt.Sprintf("%v", m)
}

func printHelp() {
	help := `Pi - Minimal Coding Agent

Commands:
  /help     Show this help
  /quit     Exit Pi
  /session  Show session info
  /branch   Create a new branch from current point
  /rewind   Go back to parent branch
  /nav ID   Navigate to a specific branch node
  /reload   Reload all extensions

Session: %s
Time: %s`
	fmt.Printf(help+"\n", "~/.pi/sessions/", time.Now().Format(time.RFC3339))
}
