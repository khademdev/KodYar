// تست‌های رفع اشکال‌ها و قابلیت‌های جلسه‌ی نهایی:
// بن‌بست SettingsView، بکاپ/برگرداندن پوشه‌ها، escape آرگومان shell،
// ابزارهای سفارشی، تنظیمات نمایش، لاگ خطاها و Resume ایجنت.
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------- بن‌بست قفل‌ها

// TestSettingsViewNoDeadlockWithRevert: SettingsView (a.mu → changes.mu) باید
// با RevertFileChange هم‌زمان (changes.mu → a.mu) بدون بن‌بست تمام شود.
func TestSettingsViewNoDeadlockWithRevert(t *testing.T) {
	a := newTestApp(t)
	// یک تغییر اعمال‌شده‌ی واقعی: نوشتن فایل با تأیید خاموش
	a.settings.ConfirmFileWrites = false
	if out, ok := a.toolWriteFile(context.Background(), "deadlock.txt", "data"); !ok {
		t.Fatalf("write failed: %s", out)
	}
	list := a.loadChanges()
	if len(list) != 1 {
		t.Fatalf("expected 1 change, got %d", len(list))
	}
	id := list[0].ID

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // مسیر A: a.mu → changes.mu
		defer wg.Done()
		for i := 0; i < 40; i++ {
			v := a.SettingsView()
			if v == nil || v["pendingChanges"] == nil {
				t.Error("SettingsView returned nil map/field")
				return
			}
		}
	}()
	go func() { // مسیر B: changes.mu → a.mu
		defer wg.Done()
		for i := 0; i < 40; i++ {
			_ = a.RevertFileChange(id) // بار اول موفق، بعدی‌ها خطای «قبلاً برگشته»
		}
	}()
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		// بدون بن‌بست تمام شد
	case <-time.After(8 * time.Second):
		t.Fatal("DEADLOCK detected: SettingsView ↔ RevertFileChange")
	}
}

// ---------------------------------------------------------------- پوشه‌ها

// TestDeleteAndRevertEmptyDir: حذف پوشه‌ی خالی باید موفق شود و برگرداندن،
// همان پوشه را بازسازی کند (قبلاً backupOriginal روی پوشه خطای EISDIR می‌داد).
// از ۱.۸.۰ (۴.۱) حذفِ پوشه «سنگین» است و حتی در auto تأیید می‌خواهد —
// پس این تست یک تأییدگر خودکار می‌بندد.
func TestDeleteAndRevertEmptyDir(t *testing.T) {
	a := newTestApp(t)
	if err := os.MkdirAll(filepath.Join(a.settings.WorkspacePath, "emptydir"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.ctx = context.Background() // بدون UI، درخواست تأیید رد می‌شود
	go func() {                  // تأییدگر خودکار: اولین درخواست را «اجازه» جواب می‌دهد
		for i := 0; i < 200; i++ {
			a.changes.mu.Lock()
			var id string
			for k := range a.changes.confirms {
				id = k
				break
			}
			a.changes.mu.Unlock()
			if id != "" {
				_ = a.RespondFileConfirm(id, true)
				return
			}
			SleepShort()
		}
	}()
	out, ok := a.toolDeletePath(context.Background(), "emptydir")
	if !ok {
		t.Fatalf("delete of empty dir failed: %s", out)
	}
	if _, err := os.Stat(filepath.Join(a.settings.WorkspacePath, "emptydir")); !os.IsNotExist(err) {
		t.Fatal("dir should be gone")
	}
	list := a.loadChanges()
	if len(list) != 1 {
		t.Fatalf("expected 1 change, got %d", len(list))
	}
	if err := a.RevertFileChange(list[0].ID); err != nil {
		t.Fatalf("revert failed: %v", err)
	}
	if st, err := os.Stat(filepath.Join(a.settings.WorkspacePath, "emptydir")); err != nil || !st.IsDir() {
		t.Fatal("dir should be restored as a directory")
	}
}

// TestMoveAndRevertDirectory: جابه‌جایی پوشه باید موفق شود و برگرداندن،
// پوشه را یکپارچه (rename-back) به مسیر اول بازگرداند.
func TestMoveAndRevertDirectory(t *testing.T) {
	a := newTestApp(t)
	src := filepath.Join(a.settings.WorkspacePath, "olddir")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok := a.toolMovePath(context.Background(), "olddir", "newdir")
	if !ok {
		t.Fatalf("move of directory failed: %s", out)
	}
	if _, err := os.Stat(filepath.Join(a.settings.WorkspacePath, "newdir", "f.txt")); err != nil {
		t.Fatal("dir should be at new location")
	}
	list := a.loadChanges()
	if len(list) != 1 {
		t.Fatalf("expected 1 change, got %d", len(list))
	}
	if err := a.RevertFileChange(list[0].ID); err != nil {
		t.Fatalf("revert failed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(a.settings.WorkspacePath, "olddir", "f.txt")); err != nil || string(data) != "inside" {
		t.Fatal("directory should be renamed back with its content intact")
	}
	if _, err := os.Stat(filepath.Join(a.settings.WorkspacePath, "newdir")); !os.IsNotExist(err) {
		t.Fatal("new location must be gone after revert")
	}
}

// ---------------------------------------------------------------- امنیت shell

func TestEscapeShellArg(t *testing.T) {
	// انتظارها وابسته به پلتفرم‌اند: روی ویندوز دابل‌کوت (cmd)،
	// روی یونیکس تک‌کوت — همان رفتاری که escapeShellArg با isWindows دارد.
	var cases map[string]string
	if isWindows() {
		cases = map[string]string{
			`hello`:         `"hello"`,
			`x; rm -rf /`:   `"x; rm -rf /"`,
			`a'b`:           `"a'b"`,
			`$(whoami)`:     `"$(whoami)"`,
			"with\nnewline": `"with newline"`,
		}
	} else {
		cases = map[string]string{
			`hello`:         `'hello'`,
			`x; rm -rf /`:   `'x; rm -rf /'`,
			`a'b`:           `'a'\''b'`,
			`$(whoami)`:     `'$(whoami)'`,
			"with\nnewline": `'with newline'`,
		}
	}
	for in, want := range cases {
		if got := escapeShellArg(in); got != want {
			t.Errorf("escapeShellArg(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestExecCustomToolEscaping: مقدار خطرناک مدل نباید بتواند از قالب فرمان فرار کند.
func TestExecCustomToolEscaping(t *testing.T) {
	a := newTestApp(t)
	a.settings.CustomTools = []CustomTool{{
		Name:        "greet",
		Command:     "echo hi {{name}}",
		Description: "test",
		Parameters:  `{"type":"object","properties":{"name":{"type":"string"}}}`,
	}}
	// تلاش برای تزریق: مقدار می‌باید quote شود و دستور دوم اجرا نشود
	out, ok := a.execCustomTool(context.Background(), "custom_greet", map[string]interface{}{
		"name": "x; touch PWNED",
	})
	if !ok {
		t.Fatalf("echo must succeed: %s", out)
	}
	if p := filepath.Join(a.settings.WorkspacePath, "PWNED"); fileExists(p) {
		t.Fatal("injection! file PWNED was created")
	}
	if !strings.Contains(out, "x; touch PWNED") {
		t.Fatalf("value must appear quoted/verbatim in output: %q", out)
	}
}

// ---------------------------------------------------------------- ابزار سفارشی

// TestCustomToolDefsAdvertised: تعریف ابزار سفارشی باید در customToolDefs ظاهر شود.
func TestCustomToolDefsAdvertised(t *testing.T) {
	defs := customToolDefs([]CustomTool{
		{Name: "mytool", Command: "echo {{x}}", Description: "d",
			Parameters: `{"type":"object","properties":{"x":{"type":"string"}}}`},
		{Name: "", Command: "x"}, // نامعتبر — باید حذف شود
	})
	if len(defs) != 1 {
		t.Fatalf("expected 1 def, got %d", len(defs))
	}
	if defs[0].Function.Name != "custom_mytool" {
		t.Fatalf("wrong name: %s", defs[0].Function.Name)
	}
}

// ---------------------------------------------------------------- نمایش

func TestSetAppearance(t *testing.T) {
	a := newTestApp(t)
	if err := a.SetAppearance(20, "weird"); err != nil {
		t.Fatal(err)
	}
	if a.settings.FontSize != 18 {
		t.Fatalf("fontSize must clamp to 18, got %d", a.settings.FontSize)
	}
	if a.settings.Density != "normal" {
		t.Fatalf("invalid density must fall back to normal, got %s", a.settings.Density)
	}
	if err := a.SetAppearance(9, "compact"); err != nil {
		t.Fatal(err)
	}
	if a.settings.FontSize != 12 {
		t.Fatalf("fontSize must clamp to 12, got %d", a.settings.FontSize)
	}
	// ماندگاری روی دیسک
	a2 := NewApp()
	os.Setenv("MYAI_CONFIG_DIR", a.mustConfigDir())
	if err := a2.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if a2.settings.FontSize != 12 || a2.settings.Density != "compact" {
		t.Fatal("appearance must persist")
	}
}

// ---------------------------------------------------------------- لاگ خطاها

func TestErrorLog(t *testing.T) {
	a := newTestApp(t)
	if txt, err := a.GetErrorLog(); err != nil || strings.TrimSpace(txt) != "" {
		t.Fatalf("empty log expected, got err=%v txt=%q", err, txt)
	}
	logAppErrorFor(a, "test", "something failed")
	logAppErrorFor(a, "chat", "پاسخ ناموفق")
	txt, err := a.GetErrorLog()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(txt, "something failed") || !strings.Contains(txt, "پاسخ ناموفق") {
		t.Fatalf("log entries missing: %q", txt)
	}
	if !strings.Contains(txt, "[test]") || !strings.Contains(txt, "[chat]") {
		t.Fatalf("kind tags missing: %q", txt)
	}
	if err := a.ClearErrorLog(); err != nil {
		t.Fatal(err)
	}
	if txt, _ := a.GetErrorLog(); strings.TrimSpace(txt) != "" {
		t.Fatalf("log must be empty after clear: %q", txt)
	}
}

// ---------------------------------------------------------------- Resume ایجنت

func TestResumeAgentFlow(t *testing.T) {
	a := newTestApp(t)
	a.settings.Provider = "p1"
	a.settings.Model = "m1"
	a.settings.Providers = []Provider{{ID: "p1", Name: "P", BaseURL: "http://127.0.0.1:1", Models: []string{"m1"}, NoKey: true}}

	// گفتگو با آخرین پیامِ ابزار → قابل ادامه
	conv := &Conversation{ID: newID(), Title: "resume-test", Messages: []Message{
		{ID: newID(), Role: "user", Content: "سلام"},
		{ID: newID(), Role: "tool", ToolName: "read_file", Content: "OK", ToolOK: true},
	}}
	if err := a.saveConversation(conv); err != nil {
		t.Fatal(err)
	}

	got, err := a.ResumeAgent(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := got.Messages[len(got.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "ادامه بده") {
		t.Fatalf("resume message expected, got %+v", last)
	}
	a.StopGeneration()
	waitForGenDone(t, a)

	// حالت غیرقابل ادامه: آخرین پیام کاربر
	conv2 := &Conversation{ID: newID(), Title: "no-resume", Messages: []Message{
		{ID: newID(), Role: "user", Content: "سلام"},
	}}
	if err := a.saveConversation(conv2); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ResumeAgent(conv2.ID); err == nil {
		t.Fatal("resume must fail when last message is user")
	}
}

// ---------------------------------------------------------------- خروجی UTF-8

func TestExportMDUTF8Truncation(t *testing.T) {
	// محتوای فارسیِ چندبایتی بزرگ‌تر از ۴۰۰۰ بایت — برش نباید کاراکتر را نصف کند
	big := strings.Repeat("سلام دنیا — پایتون و گو\n", 300) // ~15KB
	c := &Conversation{ID: newID(), Title: "خروجی", Messages: []Message{
		{ID: newID(), Role: "tool", ToolName: "read_file", Content: big, ToolOK: true},
	}}
	out := exportConversationMD(c)
	if !strings.Contains(out, "[بریده شده در خروجی]") {
		t.Fatal("truncation marker missing")
	}
	if strings.HasSuffix(strings.TrimSpace(out), "\uFFFD") {
		t.Fatal("output ends with broken rune — cutUTF8 not respected")
	}
	// کل رشته باید UTF-8 معتبر باشد
	if !utf8ValidString(out) {
		t.Fatal("exported markdown contains invalid UTF-8")
	}
}

// utf8ValidString کمکِ تست (بدون وابستگی به unicode/utf8 در تست).
func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			// فقط وقتی خطاست که از خود متن نیامده باشد؛ در تست ما کاراکتر
			// جایگزین وجود ندارد پس دیدن آن یعنی برش خراب.
			return false
		}
	}
	return true
}

// fileExists کمکِ تست.
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// waitForGenDone صبر می‌کند تا گوروتین استریم (a.gen == nil شود) تمام شود —
// تا تست‌ها هنگام پاک‌سازی TempDir با نوشتن هم‌زمان درگیر نشوند.
func waitForGenDone(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.genMu.Lock()
		done := a.gen == nil
		a.genMu.Unlock()
		if done {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("stream goroutine did not finish in time")
}
