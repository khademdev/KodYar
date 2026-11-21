// تست رگرسیون: فرمان‌های git که خود برنامه اجرا می‌کند (git status/diff/log/show)
// نباید تحت تأثیر تنظیمات اجراییِ .git/config باشند.
//
// چرا: ایجنت می‌تواند (با تأیید کاربر) در .git/config بنویسد. اگر
// core.fsmonitor یا core.hooksPath به یک فرمان بیرونی اشاره کند و ابزارهای
// git برنامه بدون پرچم خنثی‌سازی اجرا شوند، آن فرمان اجرا می‌شود — یعنی
// اجرای کد از دل یک فایل متنی.
//
// تست قبل از اصلاح: `git config --get core.fsmonitor` مقدار آلوده را چاپ
// می‌کرد (FAIL). بعد از اصلاح: مقدار مؤثر false / مسیر خنثی است (PASS).
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitAvailableForTest اگر git نصب نباشد تست را با پیام روشن skip می‌کند.
func gitAvailableForTest(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed on this machine")
	}
}

// initPoisonedRepo یک مخزن تازه می‌سازد و .git/config را به فرمان‌های بیرونی
// آلوده می‌کند. مقادیر عمداً مسیرهایی هستند که اگر اجرا شوند اثر می‌گذارند.
func initPoisonedRepo(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", ws}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, strings.TrimSpace(string(out)))
		}
	}
	git("init", "-q")
	git("config", "core.fsmonitor", filepath.Join(ws, "evil-fsmonitor"))
	git("config", "core.hooksPath", filepath.Join(ws, "evil-hooks"))
	return ws
}

// TestRunGitNeutralizesPoisonedRepoConfig اثبات رفتاری: همان مسیر production
// (a.runGit) به git می‌گوید fsmonitor خاموش و hooksPath خنثی است.
func TestRunGitNeutralizesPoisonedRepoConfig(t *testing.T) {
	gitAvailableForTest(t)
	ws := initPoisonedRepo(t)

	a := newTestApp(t)
	a.settings.WorkspacePath = ws
	ctx := context.Background()

	out, ok := a.runGit(ctx, "config", "--get", "core.fsmonitor")
	if !ok {
		t.Fatalf("git config failed: %s", out)
	}
	if got := strings.TrimSpace(out); got != "false" {
		t.Fatalf("git would run core.fsmonitor=%q from .git/config; the app must force it to false (got %q)", filepath.Join(ws, "evil-fsmonitor"), got)
	}

	out, ok = a.runGit(ctx, "config", "--get", "core.hooksPath")
	if !ok {
		t.Fatalf("git config failed: %s", out)
	}
	if got := strings.TrimSpace(out); got != os.DevNull {
		t.Fatalf("git would use core.hooksPath=%q from .git/config; the app must force %q (got %q)", filepath.Join(ws, "evil-hooks"), os.DevNull, got)
	}

	// خواندن‌های عادی باید همچنان کار کنند (پرچم‌ها نباید ابزار را بشکنند).
	if r, ok := a.runGit(ctx, "status", "--porcelain=v1", "-b"); !ok || !strings.Contains(r, "## ") {
		t.Fatalf("git status must still work with the safety flags: %v (%q)", ok, r)
	}
}
