// تست‌های رگرسیون مهاجرت نام برند در «دستور سیستمی» ذخیره‌شده:
// پرامپت‌های نسخه‌های قبل مدل را وادار می‌کردند خودشان را MyAI معرفی کنند.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteLegacyBrandPrompt(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		changed bool
	}{
		{"You are MyAI, a concise assistant.", "You are KodYar, a concise assistant.", true},
		{"You are MyAi.", "You are KodYar.", true},
		{"MYAI help", "KodYar help", true},
		{"You are KodYar.", "You are KodYar.", false},
		{"", "", false},
		// مسیرها و شناسه‌ها نباید دست بخورند
		{"keep .myai/rules.md", "keep .myai/rules.md", false},
		{"keep MyAI-1.0.6 and MyAI\\settings.json", "keep MyAI-1.0.6 and MyAI\\settings.json", false},
		{"MyAI: be nice", "KodYar: be nice", true},
	}
	for _, c := range cases {
		got, changed := rewriteLegacyBrandPrompt(c.in)
		if got != c.want || changed != c.changed {
			t.Errorf("rewriteLegacyBrandPrompt(%q) = (%q, %v), want (%q, %v)", c.in, got, changed, c.want, c.changed)
		}
	}
}

func TestLoadSettingsMigratesLegacyBrandPrompt(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("KODYAR_CONFIG_DIR", dir)
	resetErrorLogPath()

	// تنظیمات ذخیره‌شده‌ی قدیمی: پرامپت پیش‌فرضِ قبل از تغییر نام
	old := map[string]interface{}{
		"systemPrompt": "You are MyAI, a concise and helpful AI assistant for coding and questions. Answer in Persian (Farsi).",
		"temperature":  0.7,
	}
	raw, _ := json.Marshal(old)
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewApp()
	if err := a.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.settings.SystemPrompt, "MyAI") {
		t.Fatalf("stored prompt still names the old brand: %q", a.settings.SystemPrompt)
	}
	if !strings.Contains(a.settings.SystemPrompt, "KodYar") {
		t.Fatalf("stored prompt must name KodYar, got %q", a.settings.SystemPrompt)
	}
	if !a.settings.BrandPromptMigrated {
		t.Fatal("BrandPromptMigrated must be set after the migration")
	}

	// مهاجرت باید روی دیسک هم ذخیره شده باشد
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[string]interface{}
	if err := json.Unmarshal(saved, &onDisk); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(onDisk["systemPrompt"].(string), "KodYar") {
		t.Fatalf("migrated prompt not persisted: %v", onDisk["systemPrompt"])
	}
	if onDisk["brandPromptMigrated"] != true {
		t.Fatalf("brandPromptMigrated not persisted: %v", onDisk["brandPromptMigrated"])
	}
}

func TestBrandPromptMigrationRunsOnce(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("KODYAR_CONFIG_DIR", dir)
	resetErrorLogPath()

	old := map[string]interface{}{
		"systemPrompt":        "You are KodYar. Always call yourself MyAI here.",
		"brandPromptMigrated": true, // کاربر خودش بعد از مهاجرت این متن را نوشته
	}
	raw, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewApp()
	if err := a.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.settings.SystemPrompt, "MyAI") {
		t.Fatalf("migration must not rerun after it already happened, got %q", a.settings.SystemPrompt)
	}
}

func TestFreshSettingsPromptNamesKodYar(t *testing.T) {
	s := defaultSettings()
	if strings.Contains(s.SystemPrompt, "MyAI") || !strings.Contains(s.SystemPrompt, "KodYar") {
		t.Fatalf("default prompt must name KodYar, got %q", s.SystemPrompt)
	}
	if strings.Contains(agentSystemPromptForOS("C:\\ws", "windows"), "MyAI") {
		t.Fatal("agent system prompt must not mention the old brand")
	}
	if strings.Contains(subagentSystemPrompt("/ws"), "MyAI") {
		t.Fatal("subagent system prompt must not mention the old brand")
	}
}
