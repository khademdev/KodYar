// تست‌های قوانین پروژه و @mention (اولویت ۲).
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------- قوانین پروژه

// rulesBlock helper با قفل — برای تست‌ها.
func rulesBlockOf(a *App) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rulesPromptBlockLocked()
}

func TestRulesFromFile(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	a.settings.RulesEnabled = true

	// بدون فایل → خالی
	if block := rulesBlockOf(a); block != "" {
		t.Fatalf("empty workspace must give no rules, got: %s", block)
	}

	// فایل اختصاصی KodYar
	os.MkdirAll(filepath.Join(ws, ".kodyar"), 0o755)
	os.WriteFile(filepath.Join(ws, ".kodyar", "rules.md"), []byte("همیشه پاسخ‌ها کوتاه باشد."), 0o644)

	block := rulesBlockOf(a)
	if !strings.Contains(block, "همیشه پاسخ‌ها کوتاه باشد.") {
		t.Fatalf("rules block missing content: %s", block)
	}
	if !strings.Contains(block, "<project_rules") || !strings.Contains(block, ".kodyar/rules.md") {
		t.Fatalf("rules block format wrong: %s", block)
	}

	// اولویت:.kodyar/rules.md بالاتر از.cursorrules
	os.WriteFile(filepath.Join(ws, ".cursorrules"), []byte("cursor rules"), 0o644)
	block = rulesBlockOf(a)
	if strings.Contains(block, "cursor rules") {
		t.Fatal(".kodyar/rules.md must take priority over .cursorrules")
	}

	// خاموش بودن سوییچ → فایل خوانده نمی‌شود
	a.settings.RulesEnabled = false
	if block := rulesBlockOf(a); block != "" {
		t.Fatal("disabled rules must not load files")
	}

	// قوانین کاربر از تنظیمات هم تزریق می‌شود
	a.settings.RulesEnabled = true
	a.settings.ProjectRules = "قانون دستی کاربر"
	block = rulesBlockOf(a)
	if !strings.Contains(block, "قانون دستی کاربر") || !strings.Contains(block, "همیشه پاسخ‌ها کوتاه باشد.") {
		t.Fatalf("both rule sources must be injected: %s", block)
	}
}

func TestSetProjectRulesAndToggle(t *testing.T) {
	a := newTestApp(t)
	if err := a.SetProjectRules("قانون تست"); err != nil {
		t.Fatal(err)
	}
	if a.settings.ProjectRules != "قانون تست" {
		t.Fatal("SetProjectRules did not save")
	}
	if err := a.SetRulesEnabled(false); err != nil {
		t.Fatal(err)
	}
	if a.settings.RulesEnabled {
		t.Fatal("SetRulesEnabled did not save")
	}
	// ماندگاری روی دیسک
	os.Setenv("KODYAR_CONFIG_DIR", a.mustConfigDir())
	a2 := NewApp()
	_ = a2.loadSettings()
	if a2.settings.ProjectRules != "قانون تست" || a2.settings.RulesEnabled {
		t.Fatal("rules settings must persist to disk")
	}
}

// ---------------------------------------------------------------- @mention

func TestExpandMentionsFile(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	os.MkdirAll(filepath.Join(ws, "src"), 0o755)
	os.WriteFile(filepath.Join(ws, "src", "main.go"), []byte("package main\n"), 0o644)

	att := a.expandMentions("این فایل را ببین @src/main.go لطفاً")
	if !strings.Contains(att, "<attached_files>") {
		t.Fatalf("mention must produce attached block: %q", att)
	}
	if !strings.Contains(att, "## src/main.go") || !strings.Contains(att, "package main") {
		t.Fatalf("mention must include file content: %q", att)
	}

	// بدون mention → خالی
	if att := a.expandMentions("پیام بدون ارجاع"); att != "" {
		t.Fatalf("no mention must give empty, got: %q", att)
	}

	// فایل ناموجود → هشدار در بلوک یا خالی
	att = a.expandMentions("@nope/missing.txt چی؟")
	if strings.Contains(att, "## nope/missing.txt") {
		t.Fatal("missing file must not be attached")
	}
}

func TestExpandMentionsBinaryRejected(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	// باینری واقعی (بایت NUL) با پسوند مجاز
	os.WriteFile(filepath.Join(ws, "data.txt"), []byte{'a', 0, 'b', 0}, 0o644)
	// باینری با پسوند شناخته‌شده
	os.WriteFile(filepath.Join(ws, "img.png"), []byte("fakepng"), 0o644)

	att := a.expandMentions("@data.txt و @img.png")
	if strings.Contains(att, "## data.txt") || strings.Contains(att, "## img.png") {
		t.Fatalf("binary files must be rejected: %q", att)
	}
}

func TestExpandMentionsFolderSmartPick(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	a.settings.ContextFiles = 2

	os.MkdirAll(filepath.Join(ws, "app"), 0o755)
	os.MkdirAll(filepath.Join(ws, "app", "node_modules"), 0o755)
	os.WriteFile(filepath.Join(ws, "app", "user.go"), []byte("user handler"), 0o644)
	os.WriteFile(filepath.Join(ws, "app", "readme.md"), []byte("docs"), 0o644)
	os.WriteFile(filepath.Join(ws, "app", "old.txt"), []byte("old file"), 0o644)
	// node_modules باید نادیده گرفته شود حتی با اسم جذاب
	os.WriteFile(filepath.Join(ws, "app", "node_modules", "user.go"), []byte("junk"), 0o644)

	// پیام با کلیدواژه‌ی «user» → user.go باید انتخاب شود
	att := a.expandMentions("درباره‌ی user چه می‌دانی؟ @app/")
	if !strings.Contains(att, "## app/user.go") {
		t.Fatalf("smart pick must select user.go: %q", att)
	}
	if strings.Contains(att, "node_modules") {
		t.Fatal("node_modules must be skipped in smart pick")
	}
	// سقف تعداد فایل‌های پوشه
	n := strings.Count(att, "## app/")
	if n > 2 {
		t.Fatalf("folder mention must respect ContextFiles cap (2), got %d", n)
	}
}

func TestExpandMentionsPathTraversalBlocked(t *testing.T) {
	a := newTestApp(t)
	// تلاش برای فرار از workspace — mention regex فقط کاراکترهای امن را می‌گیرد؛
	// حتی اگر کسی مسیر نسبی عبوری ساخت، resolveWorkspacePath رد می‌کند
	att := a.expandMentions("@../../etc/passwd")
	if strings.Contains(att, "etc/passwd") && strings.Contains(att, "## ") {
		t.Fatalf("path traversal must be blocked: %q", att)
	}
}

func TestMentionSearch(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	os.MkdirAll(filepath.Join(ws, "src", "deep"), 0o755)
	os.MkdirAll(filepath.Join(ws, "node_modules"), 0o755)
	os.WriteFile(filepath.Join(ws, "src", "main.go"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(ws, "src", "deep", "handler.go"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(ws, "node_modules", "hidden.js"), []byte("x"), 0o644)

	res := a.MentionSearch("main")
	if len(res) == 0 || res[0].Path != "src/main.go" {
		t.Fatalf("search 'main' must find src/main.go first: %+v", res)
	}
	res = a.MentionSearch("")
	if len(res) == 0 {
		t.Fatal("empty query must list files")
	}
	for _, r := range res {
		if strings.Contains(r.Path, "node_modules") {
			t.Fatal("ignored dirs must never appear in results")
		}
	}
	// پوشه‌ها هم باید در نتایج باشند
	res = a.MentionSearch("src")
	foundDir := false
	for _, r := range res {
		if r.IsDir && r.Path == "src" {
			foundDir = true
		}
	}
	if !foundDir {
		t.Fatalf("dir 'src' must appear in results: %+v", res)
	}
}

func TestMentionAttachedInHistory(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("سلام دنیا"), 0o644)

	// شبیه‌سازی SendMessage بدون شبکه: بررسی prepareStream
	conv := &Conversation{ID: newID(), Title: "t", CreatedAt: time.Now().Unix(), Messages: []Message{
		{ID: newID(), Role: "user", Content: "ببین @hello.txt", Attached: a.expandMentions("ببین @hello.txt")},
	}}
	history, err := a.buildHistoryForTest(conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) < 2 {
		t.Fatal("history must have system + user")
	}
	userMsg := history[len(history)-1]
	uc, _ := userMsg.Content.(string)
	if !strings.Contains(uc, "ببین @hello.txt") || !strings.Contains(uc, "سلام دنیا") {
		t.Fatalf("user message must contain original text + attached content: %q", userMsg.Content)
	}
}

// buildHistoryForTest prepareStream را برای تست صدا می‌زند (بدون شبکه).
// از Provider بدون کلید (ollama) استفاده می‌کند تا بررسی کلید رد نشود.
func (a *App) buildHistoryForTest(conv *Conversation) ([]chatMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.Provider = "ollama"
	a.settings.Model = "llama3.2"
	a.settings.AgentMode = false
	_, _, _, _, history, err := a.prepareStream(conv, false, "")
	return history, err
}
