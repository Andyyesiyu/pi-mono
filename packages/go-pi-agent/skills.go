package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/nickolaev/pi-mono/packages/go-pi-ai"
)

// Skill represents a loaded skill from a SKILL.md file.
type Skill struct {
	Name                   string
	Description            string
	FilePath               string
	BaseDir                string
	Content                string // The markdown body (instructions)
	DisableModelInvocation bool
}

var validSkillName = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// SkillManager handles skill discovery, loading, and lookup.
type SkillManager struct {
	mu     sync.RWMutex
	skills map[string]*Skill
	cwd    string
}

// NewSkillManager creates a new SkillManager.
func NewSkillManager(cwd string) *SkillManager {
	return &SkillManager{
		skills: make(map[string]*Skill),
		cwd:    cwd,
	}
}

// LoadDefaults loads skills from the default locations:
// 1. Project: <cwd>/.pi/skills/
// 2. Global: ~/.pi/agent/skills/
func (sm *SkillManager) LoadDefaults() error {
	dirs := []string{
		filepath.Join(sm.cwd, ".pi", "skills"),
	}
	home, err := os.UserHomeDir()
	if err == nil {
		dirs = append(dirs, filepath.Join(home, ".pi", "agent", "skills"))
	}
	return sm.LoadFromDirs(dirs...)
}

// LoadFromDirs discovers and loads skills from the given directories.
func (sm *SkillManager) LoadFromDirs(dirs ...string) error {
	for _, dir := range dirs {
		if err := sm.loadDir(dir); err != nil {
			return err
		}
	}
	return nil
}

func (sm *SkillManager) loadDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat skills directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read skills directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			// Look for SKILL.md inside subdirectory
			skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
			if _, err := os.Stat(skillPath); err == nil {
				if err := sm.loadSkillFile(skillPath); err != nil {
					return err
				}
			}
		} else if strings.HasSuffix(entry.Name(), ".md") {
			// Direct .md file in skills root
			skillPath := filepath.Join(dir, entry.Name())
			if err := sm.loadSkillFile(skillPath); err != nil {
				return err
			}
		}
	}

	return nil
}

func (sm *SkillManager) loadSkillFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read skill file %s: %w", path, err)
	}

	skill, err := parseSkill(string(data), path)
	if err != nil {
		return fmt.Errorf("parse skill %s: %w", path, err)
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// First registered skill wins on name collision
	if _, exists := sm.skills[skill.Name]; !exists {
		sm.skills[skill.Name] = skill
	}

	return nil
}

// parseSkill parses a SKILL.md file with YAML-like frontmatter.
func parseSkill(content string, filePath string) (*Skill, error) {
	skill := &Skill{
		FilePath: filePath,
		BaseDir:  filepath.Dir(filePath),
	}

	// Parse frontmatter delimited by ---
	if strings.HasPrefix(content, "---\n") {
		end := strings.Index(content[4:], "\n---")
		if end >= 0 {
			frontmatter := content[4 : 4+end]
			skill.Content = strings.TrimSpace(content[4+end+4:])
			parseFrontmatter(frontmatter, skill)
		} else {
			skill.Content = content
		}
	} else {
		skill.Content = content
	}

	// Default name from directory or filename
	if skill.Name == "" {
		base := filepath.Base(filePath)
		if base == "SKILL.md" {
			skill.Name = filepath.Base(filepath.Dir(filePath))
		} else {
			skill.Name = strings.TrimSuffix(base, ".md")
		}
		skill.Name = strings.ToLower(skill.Name)
	}

	// Validate
	if skill.Description == "" {
		return nil, fmt.Errorf("skill %q has no description", skill.Name)
	}

	if len(skill.Name) > 64 {
		skill.Name = skill.Name[:64]
	}

	if !validSkillName.MatchString(skill.Name) {
		// Sanitize: replace invalid chars with hyphens
		sanitized := strings.ToLower(skill.Name)
		sanitized = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(sanitized, "-")
		sanitized = regexp.MustCompile(`-{2,}`).ReplaceAllString(sanitized, "-")
		sanitized = strings.Trim(sanitized, "-")
		if sanitized == "" {
			sanitized = "skill"
		}
		skill.Name = sanitized
	}

	if len(skill.Description) > 1024 {
		skill.Description = skill.Description[:1024]
	}

	return skill, nil
}

// parseFrontmatter extracts key-value pairs from YAML-like frontmatter.
func parseFrontmatter(fm string, skill *Skill) {
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		// Remove surrounding quotes if present
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}

		switch key {
		case "name":
			skill.Name = value
		case "description":
			skill.Description = value
		case "disable-model-invocation":
			skill.DisableModelInvocation = value == "true"
		}
	}
}

// Register adds a skill manually.
func (sm *SkillManager) Register(skill *Skill) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.skills[skill.Name] = skill
}

// Get returns a skill by name, or nil if not found.
func (sm *SkillManager) Get(name string) *Skill {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.skills[name]
}

// All returns all loaded skills.
func (sm *SkillManager) All() []*Skill {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	result := make([]*Skill, 0, len(sm.skills))
	for _, s := range sm.skills {
		result = append(result, s)
	}
	return result
}

// VisibleSkills returns skills that are not hidden from the model.
func (sm *SkillManager) VisibleSkills() []*Skill {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	var result []*Skill
	for _, s := range sm.skills {
		if !s.DisableModelInvocation {
			result = append(result, s)
		}
	}
	return result
}

// FormatForPrompt formats visible skills for inclusion in the system prompt.
// Uses the Agent Skills standard XML format.
func (sm *SkillManager) FormatForPrompt() string {
	visible := sm.VisibleSkills()
	if len(visible) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<available-skills>\n")
	for _, s := range visible {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", s.Name, s.Description))
	}
	sb.WriteString("</available-skills>\n\n")
	sb.WriteString("Use the Skill tool to invoke a skill when the user's request matches one. ")
	sb.WriteString("Pass the skill name and any additional arguments.")
	return sb.String()
}

// NewSkillTool creates the Skill tool that the LLM uses to invoke skills.
func NewSkillTool(sm *SkillManager) *AgentTool {
	return &AgentTool{
		Tool: ai.Tool{
			Name:        "Skill",
			Description: "Invoke a skill by name. Skills provide specialized capabilities and domain knowledge.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"skill": map[string]any{
						"type":        "string",
						"description": "The name of the skill to invoke.",
					},
					"args": map[string]any{
						"type":        "string",
						"description": "Optional arguments to pass to the skill.",
					},
				},
				"required": []string{"skill"},
			},
		},
		Label: "Skill",
		Execute: func(toolCallID string, params map[string]any, signal *ai.AbortSignal, onUpdate AgentToolUpdateCallback) (*AgentToolResult, error) {
			skillName, _ := params["skill"].(string)
			if skillName == "" {
				return toolError("skill name is required"), nil
			}

			skill := sm.Get(skillName)
			if skill == nil {
				// List available skills in error
				available := sm.All()
				var names []string
				for _, s := range available {
					names = append(names, s.Name)
				}
				return toolError(fmt.Sprintf("Skill %q not found. Available skills: %s", skillName, strings.Join(names, ", "))), nil
			}

			args, _ := params["args"].(string)

			var result strings.Builder
			result.WriteString(fmt.Sprintf("<skill name=%q>\n", skill.Name))
			result.WriteString(skill.Content)
			if args != "" {
				result.WriteString("\n\n---\nArguments: " + args)
			}
			result.WriteString("\n</skill>")

			return toolText(result.String()), nil
		},
	}
}
