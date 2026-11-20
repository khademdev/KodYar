// تست‌های مهارت‌ها (skills.go): استخراج فراداده، اولویت ریشه‌ها، حذف
// تکراری، سوییچ خاموش‌کننده، شکل بلوک پرامپت و مدل جدا برای زیرایجنت.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, ws, rel, content string) {
	t.Helper()
	full := filepath.Join(ws, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestParseSkillMetaFrontmatter(t *testing.T) {
	name, desc := parseSkillMeta("---\nname: release-check\ndescription: \"Pre-release checklist\"\n---\n# Heading\nbody\n")
	if name != "release-check" || desc != "Pre-release checklist" {
		t.Fatalf("frontmatter parse: name=%q desc=%q", name, desc)
	}
}

func TestParseSkillMetaFallbackHeadingAndText(t *testing.T) {
	name, desc := parseSkillMeta("# Add API Endpoint\n\nFollow these steps to add an endpoint.\n\n- step\n")
	if name != "Add API Endpoint" {
		t.Fatalf("heading name = %q", name)
	}
	if desc != "Follow these steps to add an endpoint." {
		t.Fatalf("desc = %q", desc)
	}
}

func TestDiscoverSkillsPriorityAndDedupe(t *testing.T) {
	ws := t.TempDir()
	writeSkill(t, ws, ".kodyar/skills/release/SKILL.md", "---\nname: release\ndescription: kodyar version\n---\n")
	writeSkill(t, ws, ".claude/skills/release/SKILL.md", "---\nname: release\ndescription: claude version\n---\n")
	writeSkill(t, ws, ".claude/skills/review/SKILL.md", "---\nname: review\ndescription: review changes\n---\n")
	// پوشه‌ی بدون فایل مهارت نباید وارد فهرست شود
	if err := os.MkdirAll(filepath.Join(ws, "skills", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	a := newTestApp(t)
	a.mu.Lock()
	a.settings.WorkspacePath = ws
	a.mu.Unlock()

	a.mu.Lock()
	got := a.discoverSkillsLocked()
	a.mu.Unlock()

	if len(got) != 2 {
		t.Fatalf("skills = %+v, want 2", got)
	}
	if got[0].Name != "release" || got[0].Description != "kodyar version" {
		t.Fatalf("priority root must win: %+v", got[0])
	}
	if got[0].Path != ".kodyar/skills/release/SKILL.md" {
		t.Fatalf("path must be slash-relative: %q", got[0].Path)
	}
}

func TestDiscoverSkillsDisabled(t *testing.T) {
	ws := t.TempDir()
	writeSkill(t, ws, "skills/x/SKILL.md", "---\nname: x\ndescription: d\n---\n")
	a := newTestApp(t)
	a.mu.Lock()
	a.settings.WorkspacePath = ws
	a.settings.SkillsDisabled = true
	got := a.discoverSkillsLocked()
	block := a.skillsPromptBlockLocked()
	a.mu.Unlock()
	if len(got) != 0 || block != "" {
		t.Fatalf("disabled skills must be empty: %+v %q", got, block)
	}
}

func TestSkillsPromptBlockShape(t *testing.T) {
	ws := t.TempDir()
	writeSkill(t, ws, "skills/deploy/SKILL.md", "---\nname: deploy\ndescription: ship it\n---\n")
	a := newTestApp(t)
	a.mu.Lock()
	a.settings.WorkspacePath = ws
	block := a.skillsPromptBlockLocked()
	a.mu.Unlock()

	if !strings.Contains(block, "<available_skills>") || !strings.Contains(block, "</available_skills>") {
		t.Fatalf("block wrappers missing: %q", block)
	}
	if !strings.Contains(block, "deploy — ship it (skills/deploy/SKILL.md)") {
		t.Fatalf("entry missing: %q", block)
	}
	if !strings.Contains(block, "read_file") {
		t.Fatalf("block must tell the model how to load the skill: %q", block)
	}
}

func TestSkillsPromptBlockEmptyWithoutWorkspace(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.settings.WorkspacePath = ""
	block := a.skillsPromptBlockLocked()
	a.mu.Unlock()
	if block != "" {
		t.Fatalf("empty workspace must yield no block, got %q", block)
	}
}

func TestEffectiveSubagentModel(t *testing.T) {
	if got := effectiveSubagentModel("main-model", ""); got != "main-model" {
		t.Fatalf("empty override = %q", got)
	}
	if got := effectiveSubagentModel("main-model", "  cheap-model  "); got != "cheap-model" {
		t.Fatalf("override = %q", got)
	}
}

func TestSetSubagentModelPersists(t *testing.T) {
	a := newTestApp(t)
	if err := a.SetSubagentModel("  cheap-model  "); err != nil {
		t.Fatalf("set: %v", err)
	}
	a.mu.Lock()
	got := a.settings.SubagentModel
	a.mu.Unlock()
	if got != "cheap-model" {
		t.Fatalf("persisted model = %q", got)
	}
}
