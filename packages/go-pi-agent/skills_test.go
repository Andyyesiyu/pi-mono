package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSkillBasic(t *testing.T) {
	content := `---
name: deploy
description: Deploy the application to production
---
Follow these steps to deploy:
1. Run tests
2. Build the project
3. Push to production`

	skill, err := parseSkill(content, "/skills/deploy/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}

	if skill.Name != "deploy" {
		t.Fatalf("expected name 'deploy', got %q", skill.Name)
	}
	if skill.Description != "Deploy the application to production" {
		t.Fatalf("expected description, got %q", skill.Description)
	}
	if !strings.Contains(skill.Content, "Follow these steps") {
		t.Fatalf("expected content body, got %q", skill.Content)
	}
	if skill.DisableModelInvocation {
		t.Fatal("expected DisableModelInvocation=false")
	}
}

func TestParseSkillDisableInvocation(t *testing.T) {
	content := `---
name: internal-tool
description: Internal tool not for model use
disable-model-invocation: true
---
Internal instructions.`

	skill, err := parseSkill(content, "/skills/internal-tool/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}

	if !skill.DisableModelInvocation {
		t.Fatal("expected DisableModelInvocation=true")
	}
}

func TestParseSkillNoDescription(t *testing.T) {
	content := `---
name: bad-skill
---
No description means invalid.`

	_, err := parseSkill(content, "/skills/bad-skill/SKILL.md")
	if err == nil {
		t.Fatal("expected error for skill without description")
	}
}

func TestParseSkillNameFromDir(t *testing.T) {
	content := `---
description: A skill with no explicit name
---
Instructions here.`

	skill, err := parseSkill(content, "/skills/my-great-skill/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}

	if skill.Name != "my-great-skill" {
		t.Fatalf("expected name from dir 'my-great-skill', got %q", skill.Name)
	}
}

func TestParseSkillNameFromFile(t *testing.T) {
	content := `---
description: A direct markdown skill
---
Instructions.`

	skill, err := parseSkill(content, "/skills/helper.md")
	if err != nil {
		t.Fatal(err)
	}

	if skill.Name != "helper" {
		t.Fatalf("expected name 'helper', got %q", skill.Name)
	}
}

func TestParseSkillQuotedValues(t *testing.T) {
	content := `---
name: "quoted-name"
description: 'Quoted description'
---
Content.`

	skill, err := parseSkill(content, "/skills/test/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}

	if skill.Name != "quoted-name" {
		t.Fatalf("expected name 'quoted-name', got %q", skill.Name)
	}
	if skill.Description != "Quoted description" {
		t.Fatalf("expected unquoted description, got %q", skill.Description)
	}
}

func TestSkillManagerLoadFromDirs(t *testing.T) {
	dir := t.TempDir()

	// Create skill in subdirectory with SKILL.md
	skillDir := filepath.Join(dir, "deploy")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: deploy
description: Deploy the app
---
Deploy instructions.`), 0644)

	// Create direct .md skill
	os.WriteFile(filepath.Join(dir, "test-runner.md"), []byte(`---
name: test-runner
description: Run the test suite
---
Run all tests.`), 0644)

	sm := NewSkillManager("/")
	if err := sm.LoadFromDirs(dir); err != nil {
		t.Fatal(err)
	}

	all := sm.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(all))
	}

	deploy := sm.Get("deploy")
	if deploy == nil {
		t.Fatal("deploy skill not found")
	}
	if deploy.Description != "Deploy the app" {
		t.Fatalf("wrong description: %q", deploy.Description)
	}

	runner := sm.Get("test-runner")
	if runner == nil {
		t.Fatal("test-runner skill not found")
	}
}

func TestSkillManagerNonExistentDir(t *testing.T) {
	sm := NewSkillManager("/")
	err := sm.LoadFromDirs("/nonexistent/dir/should/be/ok")
	if err != nil {
		t.Fatalf("expected nil error for nonexistent dir, got: %v", err)
	}
}

func TestSkillManagerNameCollision(t *testing.T) {
	sm := NewSkillManager("/")

	sm.Register(&Skill{
		Name:        "deploy",
		Description: "First",
		Content:     "first",
	})

	// Second load with same name should not overwrite
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "deploy")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: deploy
description: Second
---
second`), 0644)

	sm.LoadFromDirs(dir)

	skill := sm.Get("deploy")
	if skill.Description != "First" {
		t.Fatal("first registered skill should win on collision")
	}
}

func TestSkillManagerVisibleSkills(t *testing.T) {
	sm := NewSkillManager("/")

	sm.Register(&Skill{
		Name:        "visible",
		Description: "Visible skill",
		Content:     "instructions",
	})
	sm.Register(&Skill{
		Name:                   "hidden",
		Description:            "Hidden skill",
		Content:                "instructions",
		DisableModelInvocation: true,
	})

	visible := sm.VisibleSkills()
	if len(visible) != 1 {
		t.Fatalf("expected 1 visible skill, got %d", len(visible))
	}
	if visible[0].Name != "visible" {
		t.Fatalf("expected 'visible', got %q", visible[0].Name)
	}
}

func TestSkillManagerFormatForPrompt(t *testing.T) {
	sm := NewSkillManager("/")

	sm.Register(&Skill{
		Name:        "deploy",
		Description: "Deploy the application",
		Content:     "deploy instructions",
	})
	sm.Register(&Skill{
		Name:                   "internal",
		Description:            "Internal only",
		Content:                "internal instructions",
		DisableModelInvocation: true,
	})

	prompt := sm.FormatForPrompt()
	if !strings.Contains(prompt, "deploy") {
		t.Fatal("prompt should contain visible skill")
	}
	if strings.Contains(prompt, "internal") {
		t.Fatal("prompt should not contain hidden skill")
	}
	if !strings.Contains(prompt, "available-skills") {
		t.Fatal("prompt should use XML format")
	}
}

func TestSkillManagerFormatEmpty(t *testing.T) {
	sm := NewSkillManager("/")
	prompt := sm.FormatForPrompt()
	if prompt != "" {
		t.Fatal("expected empty prompt with no skills")
	}
}

func TestNewSkillTool(t *testing.T) {
	sm := NewSkillManager("/")
	sm.Register(&Skill{
		Name:        "deploy",
		Description: "Deploy the app",
		Content:     "Run deploy.sh",
	})

	tool := NewSkillTool(sm)

	if tool.Name != "Skill" {
		t.Fatalf("expected tool name 'Skill', got %q", tool.Name)
	}

	// Execute with valid skill
	result, err := tool.Execute("call-1", map[string]any{"skill": "deploy", "args": "--prod"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) == 0 || result.Content[0].Text == nil {
		t.Fatal("expected text result")
	}
	text := result.Content[0].Text.Text
	if !strings.Contains(text, "Run deploy.sh") {
		t.Fatalf("expected skill content, got %q", text)
	}
	if !strings.Contains(text, "--prod") {
		t.Fatalf("expected args in output, got %q", text)
	}

	// Execute with unknown skill
	result, err = tool.Execute("call-2", map[string]any{"skill": "unknown"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	text = result.Content[0].Text.Text
	if !strings.Contains(text, "not found") {
		t.Fatalf("expected not found error, got %q", text)
	}
	if !strings.Contains(text, "deploy") {
		t.Fatalf("expected available skills list, got %q", text)
	}
}

func TestNewSkillToolNoArgs(t *testing.T) {
	sm := NewSkillManager("/")
	sm.Register(&Skill{
		Name:        "test",
		Description: "Run tests",
		Content:     "Execute test suite",
	})

	tool := NewSkillTool(sm)
	result, err := tool.Execute("call-1", map[string]any{"skill": "test"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := result.Content[0].Text.Text
	if strings.Contains(text, "Arguments:") {
		t.Fatal("should not include Arguments section when no args provided")
	}
}

func TestNewSkillToolEmptySkillName(t *testing.T) {
	sm := NewSkillManager("/")
	tool := NewSkillTool(sm)

	result, err := tool.Execute("call-1", map[string]any{"skill": ""}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	text := result.Content[0].Text.Text
	if !strings.Contains(text, "required") {
		t.Fatalf("expected required error, got %q", text)
	}
}

func TestSkillNameSanitization(t *testing.T) {
	content := `---
name: "BAD Name With Spaces!"
description: Test sanitization
---
Content.`

	skill, err := parseSkill(content, "/skills/test/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}

	// Name should be sanitized to valid format
	if !validSkillName.MatchString(skill.Name) {
		t.Fatalf("name %q should match valid pattern", skill.Name)
	}
}

func TestSkillDescriptionTruncation(t *testing.T) {
	longDesc := strings.Repeat("a", 2000)
	content := "---\nname: test\ndescription: " + longDesc + "\n---\nContent."

	skill, err := parseSkill(content, "/skills/test/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}

	if len(skill.Description) > 1024 {
		t.Fatalf("description should be truncated to 1024, got %d", len(skill.Description))
	}
}
