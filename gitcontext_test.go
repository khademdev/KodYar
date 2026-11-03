// تست‌های خط کانتکست git (gitContextLineForSend) — محاسبه‌ی «بیرون از
// a.mu» (exec زیر قفل ممنوع) و کران‌دار بودن خروجی git.
//
// نقاط ورودی استریم (Send/Regenerate/ApprovePlan/Resume) خط کانتکست git را
// پیش از گرفتن قفل‌ها محاسبه و به prepareStream پاس می‌دهند؛ خود
// prepareStream هیچ exec ای انجام نمی‌دهد.
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------- F1

// TestPrepareStreamInjectsPassedGitLine تزریق خط git پاس‌داده‌شده (و فقط آن)
// را بررسی می‌کند: با gitLine خالی هیچ چیزی تزریق نمی‌شود.
func TestPrepareStreamInjectsPassedGitLine(t *testing.T) {
	a := newTestApp(t)
	a.settings.AgentMode = true
	conv := &Conversation{ID: newID(), Title: "t", Messages: []Message{
		{ID: newID(), Role: "user", Content: "hi", At: time.Now().Unix()},
	}}

	const line = "Git repo state — branch: main, working tree: clean."
	a.mu.Lock()
	_, _, _, _, hist, err := a.prepareStream(conv, false, line)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) == 0 {
		t.Fatal("empty history")
	}
	if sys := contentStr(hist[0].Content); !strings.Contains(sys, "branch: main") {
		t.Fatalf("git line not injected into system prompt: %q", sys)
	}

	a.mu.Lock()
	_, _, _, _, hist2, err2 := a.prepareStream(conv, false, "")
	a.mu.Unlock()
	if err2 != nil {
		t.Fatal(err2)
	}
	if sys := contentStr(hist2[0].Content); strings.Contains(sys, "Git repo state") {
		t.Fatal("empty gitLine must inject nothing")
	}
}

// TestPrepareStreamNeverExecsGit اثبات رگرسیون F1: با AgentMode روشن و
// workspace واقعاً git-repo، prepareStream (با gitLine خالی) نباید خودش
// فرمان git اجرا کند — پیش از اصلاح، خط «Git repo state» تزریق می‌شد.
func TestPrepareStreamNeverExecsGit(t *testing.T) {
	a := setupGitRepo(t) // skip اگر git در دسترس نباشد
	a.settings.AgentMode = true
	conv := &Conversation{ID: newID(), Title: "t", Messages: []Message{
		{ID: newID(), Role: "user", Content: "hi", At: time.Now().Unix()},
	}}

	a.mu.Lock()
	_, _, _, _, hist, err := a.prepareStream(conv, false, "")
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if sys := contentStr(hist[0].Content); strings.Contains(sys, "Git repo state") {
		t.Fatal("prepareStream must not exec git on its own (F1 regression)")
	}

	// مسیر واقعی نقاط ورودی: gitContextLineForSend خط را «بیرون از قفل» می‌سازد
	if l := a.gitContextLineForSend(); !strings.Contains(l, "branch: main") || !strings.Contains(l, "DIRTY") {
		t.Fatalf("gitContextLineForSend wrong: %s", l)
	}
}

// TestGitContextLineForSendGates دروازه‌های helper: حالت ایجنت خاموش،
// workspace خالی و workspace غیر-repo همگی باید خط خالی بدهند. (نتیجه در
// نبودِ git binary هم خالی است — تست بدون git هم معتبر است.)
func TestGitContextLineForSendGates(t *testing.T) {
	a := newTestApp(t)
	if l := a.gitContextLineForSend(); l != "" {
		t.Fatalf("agent mode off must give empty line, got: %s", l)
	}
	a.settings.AgentMode = true
	if l := a.gitContextLineForSend(); l != "" {
		t.Fatalf("non-repo workspace must give empty line, got: %s", l)
	}
	ws := a.settings.WorkspacePath
	a.settings.WorkspacePath = ""
	if l := a.gitContextLineForSend(); l != "" {
		t.Fatalf("empty workspace must give empty line, got: %s", l)
	}
	a.settings.WorkspacePath = ws
}

// ---------------------------------------------------------------- F2

// TestAtomicWritesLeaveNoTmp پس از ذخیره‌ی گفتگو (فایل + ایندکس) و افزودن
// تغییر، نباید هیچ فایل موقتی «.tmp» باقی بماند و داده باید سالم
// round-trip شود — نشانه‌ی اجرای کامل مسیر atomicWriteFile (rename انجام شده).
func TestCappedGitBuffer(t *testing.T) {
	w := newCappedGitBuffer(10)
	n, err := w.Write([]byte("12345678"))
	if err != nil || n != 8 {
		t.Fatalf("write1: n=%d err=%v", n, err)
	}
	// فقط ۲ بایت جا دارد — ۸ بایت دور ریخته می‌شود اما Write «همه را خورده»
	n, err = w.Write([]byte("abcdefghij"))
	if err != nil || n != 10 {
		t.Fatalf("write2 must report full consumption: n=%d err=%v", n, err)
	}
	if got := w.buf.String(); got != "12345678ab" {
		t.Fatalf("kept=%q want 12345678ab", got)
	}
	if w.dropped != 8 {
		t.Fatalf("dropped=%d want 8", w.dropped)
	}

	// سقف صفر: همه‌چیز دور ریخته می‌شود، Write هنوز موفق است
	z := newCappedGitBuffer(0)
	if n, err := z.Write([]byte("xyz")); err != nil || n != 3 {
		t.Fatalf("zero-cap write: n=%d err=%v", n, err)
	}
	if z.buf.Len() != 0 || z.dropped != 3 {
		t.Fatal("zero-cap must drop everything")
	}
}

// TestRunGitInCapsOutput با یک «git» جعلی روی PATH که ~۳MiB خروجی می‌دهد،
// کراندار بودن بافر runGitIn را می‌سنجد: دقیقاً gitCaptureCap بایت برمی‌گردد.
// فقط پلتفرم‌های POSIX (اسکریپت shell) — روی ویندوز skip می‌شود.
func TestRunGitInCapsOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake git script requires a POSIX shell")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	a := newTestApp(t)
	bin := t.TempDir()
	script := filepath.Join(bin, "git")
	// ۴۸ × ۶۴KiB = ۳MiB خروجی — بیش از سقف ۲MiB
	body := "#!/bin/sh\ni=0\nwhile [ $i -lt 48 ]; do\n  head -c 65536 /dev/zero | tr '\\0' 'x'\n  i=$((i+1))\ndone\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	// gitEnv هم PATH همین env را به فرزند می‌دهد؛ resolve خود exec هم همین PATH را می‌بیند
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, ok := a.runGitIn(context.Background(), a.settings.WorkspacePath, "log")
	if !ok {
		t.Fatalf("fake git must succeed: %s", out)
	}
	if len(out) != gitCaptureCap {
		t.Fatalf("captured %d bytes, want exactly the %d-byte cap", len(out), gitCaptureCap)
	}
	if strings.TrimLeft(out, "x") != "" {
		t.Fatal("captured bytes must be all 'x'")
	}
}
