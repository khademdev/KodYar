// تست‌های ابزارهای حالت ایجنت — امنیت مسیر، CRUD فایل و بازسازی تاریخچه.
// با `go test` اجرا می‌شود و در باینری نهایی اثری ندارد.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetErrorLogPath کش مسیر errors.log را پاک می‌کند؛ هر تست با config dir
// خودش لاگ می‌نویسد و به پوشه‌ی موقتِ تستِ قبلی وابسته نمی‌ماند.
func resetErrorLogPath() {
	errLogMu.Lock()
	errLogPath = ""
	errLogMu.Unlock()
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	// config dir ایزوله برای هر تست تا changes.json بین تست‌ها نشت نکند
	os.Setenv("KODYAR_CONFIG_DIR", t.TempDir())
	resetErrorLogPath()
	a := NewApp()
	a.settings = defaultSettings()
	// از ۱.3.0 Provider پیش‌فرضی وجود ندارد؛ تست‌ها یک Provider آزمایشی
	// محلی (بدون کلید) تعریف می‌کنند تا prepareStream رد نشود.
	a.settings.Providers = []Provider{{
		ID: "ollama", Name: "Ollama", BaseURL: "http://localhost:11434/v1",
		NoKey: true, ContextWindow: 128000, Models: []string{"llama3.2", "qwen2.5-coder"},
	}}
	a.settings.Provider = "ollama"
	a.settings.Model = "llama3.2"
	a.settings.WorkspacePath = t.TempDir()
	// تست‌های واحد/E2E منطق ایجنت را می‌سنجند، نه ایزوله‌سازی سیستم‌عامل؛
	// سندباکس واقعی تست مستقل خودش را دارد (sandbox_windows_test.go).
	a.settings.SandboxMode = "off"
	a.settings.AllowCommands = true
	// در تست‌ها UI وجود ندارد؛ تأیید تعاملی خاموش می‌شود (تست‌های اختصاصی
	// چرخه‌ی تأیید در changes_test.go با کانال مصنوعی بررسی می‌شوند).
	a.settings.ConfirmFileWrites = false
	a.initChangesStore()
	return a
}

func TestResolveWorkspacePath(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath

	p, err := a.resolveWorkspacePath("src/main.go")
	if err != nil || p != filepath.Join(ws, "src", "main.go") {
		t.Fatalf("relative resolve failed: %v %v", p, err)
	}
	if _, err := a.resolveWorkspacePath("../outside.txt"); err == nil {
		t.Fatal("expected error for path outside workspace")
	}
	if _, err := a.resolveWorkspacePath("sub/../../../etc"); err == nil {
		t.Fatal("expected error for traversal path")
	}
	p, err = a.resolveWorkspacePath(filepath.Join(ws, "a.txt"))
	if err != nil || p != filepath.Join(ws, "a.txt") {
		t.Fatalf("absolute inside failed: %v %v", p, err)
	}
}

func TestAgentToolsCRUD(t *testing.T) {
	a := newTestApp(t)

	if r, ok := a.execTool("create_dir", `{"path":"sub"}`); !ok {
		t.Fatal(r)
	}
	if r, ok := a.execTool("write_file", `{"path":"sub/a.txt","content":"hello\nworld"}`); !ok {
		t.Fatal(r)
	}
	r, ok := a.execTool("read_file", `{"path":"sub/a.txt"}`)
	if !ok || !strings.Contains(r, "world") {
		t.Fatalf("read_file: %v", r)
	}
	r, ok = a.execTool("list_dir", `{"path":"."}`)
	if !ok || !strings.Contains(r, "sub/") {
		t.Fatalf("list_dir: %v", r)
	}
	if r, ok := a.execTool("edit_file", `{"path":"sub/a.txt","old_string":"world","new_string":"there"}`); !ok {
		t.Fatal(r)
	}
	data, _ := os.ReadFile(filepath.Join(a.settings.WorkspacePath, "sub", "a.txt"))
	if !strings.Contains(string(data), "there") {
		t.Fatal("edit not applied")
	}

	// چند متغیر ویرایش: باید خطا دهد
	a.execTool("write_file", `{"path":"b.txt","content":"x x x"}`)
	if _, ok := a.execTool("edit_file", `{"path":"b.txt","old_string":"x","new_string":"y"}`); ok {
		t.Fatal("expected failure on multiple matches")
	}
	if _, ok := a.execTool("edit_file", `{"path":"b.txt","old_string":"x","new_string":"y","replace_all":true}`); !ok {
		t.Fatal("expected success with replace_all")
	}

	// جستجو
	r, ok = a.execTool("search_files", `{"query":"there"}`)
	if !ok || !strings.Contains(r, "sub/a.txt:2:") {
		t.Fatalf("search: %v", r)
	}

	// جابه‌جایی و حذف
	if r, ok := a.execTool("move_path", `{"from":"b.txt","to":"c.txt"}`); !ok {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(a.settings.WorkspacePath, "c.txt")); err != nil {
		t.Fatal("move failed")
	}
	if r, ok := a.execTool("delete_path", `{"path":"c.txt"}`); !ok {
		t.Fatal(r)
	}

	// اجرای دستور (AllowCommands فعال)
	r, ok = a.execTool("run_command", `{"command":"echo hi"}`)
	if !ok || !strings.Contains(r, "hi") {
		t.Fatalf("run_command: %v (%v)", r, ok)
	}
}

func TestRunCommandDisabled(t *testing.T) {
	a := newTestApp(t)
	a.settings.AllowCommands = false
	r, ok := a.execTool("run_command", `{"command":"echo hi"}`)
	if ok || !strings.Contains(r, "disabled") {
		t.Fatalf("expected disabled error: %v", r)
	}
}

func TestHistoryRebuildWithTools(t *testing.T) {
	a := newTestApp(t)
	a.settings.Providers[0].NoKey = true // بدون نیاز به کلید در تست

	conv := &Conversation{ID: "t", Messages: []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "let me look",
			ToolCalls: []toolCallRecord{{ID: "c1", Name: "list_dir", Args: "{}"}}},
		{Role: "tool", Content: "result", ToolCallID: "c1"},
	}}
	_, _, _, _, hist, err := a.prepareStream(conv, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 4 {
		t.Fatalf("history length = %d", len(hist))
	}
	if hist[2].ToolCalls == nil || hist[2].ToolCalls[0].ID != "c1" {
		t.Fatal("tool_calls not rebuilt on assistant message")
	}
	if hist[3].Role != "tool" || hist[3].ToolCallID != "c1" {
		t.Fatal("tool message not rebuilt")
	}
}

func TestHistoryTrimSkipsOrphanToolMessages(t *testing.T) {
	a := newTestApp(t)
	a.settings.Providers[0].NoKey = true
	a.settings.MaxHistory = 2

	conv := &Conversation{ID: "t", Messages: []Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1",
			ToolCalls: []toolCallRecord{{ID: "c1", Name: "list_dir", Args: "{}"}}},
		{Role: "tool", Content: "r1", ToolCallID: "c1"},
		{Role: "user", Content: "q2"},
	}}
	_, _, _, _, hist, err := a.prepareStream(conv, false, "")
	if err != nil {
		t.Fatal(err)
	}
	// باید از q2 شروع شود، نه از پیام tool یتیم
	if len(hist) != 2 || hist[1].Role != "user" || hist[1].Content != "q2" {
		t.Fatalf("trim logic wrong: %+v", hist)
	}
}

func TestSchemaRequiredNeverNull(t *testing.T) {
	for _, d := range agentToolDefs() {
		m, ok := d.Function.Parameters.(map[string]interface{})
		if !ok {
			t.Fatalf("%s: bad parameters type", d.Function.Name)
		}
		if req, ok := m["required"].([]string); !ok || req == nil {
			t.Fatalf("%s: required must be a non-nil array", d.Function.Name)
		}
		// properties:null هم درخواست را نزد سرویس‌دهنده‌های سخت‌گیر (مثل
		// zro.moonmath.ai با «Request failed.») با 400 رد می‌کند.
		if props, ok := m["properties"].(map[string]interface{}); !ok || props == nil {
			t.Fatalf("%s: properties must be a non-nil object", d.Function.Name)
		}
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("%s: %v", d.Function.Name, err)
		}
		if strings.Contains(string(raw), `"properties":null`) {
			t.Fatalf("%s: parameters must not serialize properties as null: %s", d.Function.Name, raw)
		}
	}
}

// شمای ناقصِ سرور MCP یا ابزار سفارشی هم باید قبل از ارسال کامل شود.
func TestNormalizeParametersSchema(t *testing.T) {
	cases := []map[string]interface{}{
		nil,
		{},
		{"type": "object"},
		{"type": "object", "properties": nil, "required": nil},
		{"type": "object", "properties": map[string]interface{}{"a": propStr("a")}},
	}
	for _, c := range cases {
		got := normalizeParametersSchema(c)
		if got["type"] != "object" {
			t.Fatalf("type must be object: %#v", got)
		}
		if props, ok := got["properties"].(map[string]interface{}); !ok || props == nil {
			t.Fatalf("properties must be a non-nil object: %#v", got)
		}
		if got["required"] == nil {
			t.Fatalf("required must be a non-nil array: %#v", got)
		}
		raw, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(raw), `:null`) {
			t.Fatalf("schema must not contain null fields: %s", raw)
		}
	}
}

func TestHistorySkipsEmptyAssistant(t *testing.T) {
	a := newTestApp(t)
	a.settings.Providers[0].NoKey = true
	conv := &Conversation{ID: "t", Messages: []Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "", Aborted: true},
		{Role: "user", Content: "q2"},
	}}
	_, _, _, _, hist, err := a.prepareStream(conv, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 3 {
		t.Fatalf("history length = %d, want 3", len(hist))
	}
}

func TestSupportsTemperature(t *testing.T) {
	if supportsTemperature("o4-mini") || !supportsTemperature("gpt-4o") {
		t.Fatal("supportsTemperature wrong")
	}
}

func TestCutUTF8(t *testing.T) {
	s := "سلام"
	for n := 0; n <= len(s); n++ {
		if out := cutUTF8(s, n); !strings.HasPrefix(s, out) || len([]rune(out)) > len([]rune(s)) {
			t.Fatalf("bad cut at %d", n)
		}
	}
}
