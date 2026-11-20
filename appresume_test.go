// تست‌های ظاهر، لاگ خطا، Resume و خروجی Markdown (جدا شده از فایل رفع‌های مرحله‌ای).
package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

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
	os.Setenv("KODYAR_CONFIG_DIR", a.mustConfigDir())
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
