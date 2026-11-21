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
	gitInForTest(t, ws, "init", "-q")
	gitInForTest(t, ws, "config", "core.fsmonitor", filepath.Join(ws, "evil-fsmonitor"))
	gitInForTest(t, ws, "config", "core.hooksPath", filepath.Join(ws, "evil-hooks"))
	return ws
}

// gitInForTest یک فرمان git را در ws اجرا می‌کند.
func gitInForTest(t *testing.T, ws string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", ws}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, strings.TrimSpace(string(out)))
	}
	return out
}

// markerScript یک اسکریپت کوچک شِل می‌سازد که فقط یک نشانگر چاپ می‌کند — همان
// کاری که textconv/diff.external مخرب انجام می‌دهد (اجرای کد).
//
// مسیر با اسلش برگردانده می‌شود: git در ویندوز درایور را با sh اجرا می‌کند و
// بک‌اسلش‌های مسیر ویندوزی در آن از بین می‌رفت.
func markerScript(t *testing.T, dir, marker string) string {
	t.Helper()
	p := filepath.Join(dir, "driver.sh")
	// خط اول محتوای ورودی، خط دوم نشانگر: با textconv هر دو سمت نشانگر را
	// می‌گیرند، پس diff خالی نمی‌شود و اجرای درایور از خروجی دیده می‌شود.
	script := "#!/bin/sh\ncat \"$1\" 2>/dev/null\necho " + marker + "\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write driver script: %v", err)
	}
	return filepath.ToSlash(p)
}

// TestGitToolsIgnoreExternalDiffDrivers: ابزارهای git_diff / git_show / git_log
// نباید درایورهای بیرونیِ diff را اجرا کنند.
//
// حمله: ایجنت (با تأیید کاربر) یک .gitattributes با `*.txt diff=evil` و
// `diff.evil.textconv` در .git/config می‌نویسد؛ git هنگام نمایش diff مقدار
// جایگزینِ textconv را چاپ می‌کند و به این ترتیب فرمان دلخواه اجرا می‌شود.
func TestGitToolsIgnoreExternalDiffDrivers(t *testing.T) {
	gitAvailableForTest(t)
	ws := t.TempDir()
	gitInForTest(t, ws, "init", "-q")
	gitInForTest(t, ws, "config", "user.email", "test@example.com")
	gitInForTest(t, ws, "config", "user.name", "Test")

	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".gitattributes"), []byte("*.txt diff=evil\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitInForTest(t, ws, "add", "-A")
	gitInForTest(t, ws, "commit", "-qm", "init")

	tc := markerScript(t, ws, "TEXTCONV_RAN")
	gitInForTest(t, ws, "config", "diff.evil.textconv", tc)
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// پیش‌شرط: با git خام همین حمله باید واقعاً کار کند، وگرنه تست بی‌معناست.
	rawDiff := exec.Command("git", "-C", ws, "diff")
	rawDiff.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	if raw, _ := rawDiff.CombinedOutput(); !strings.Contains(string(raw), "TEXTCONV_RAN") {
		t.Skipf("the textconv driver cannot be executed in this environment, so there is nothing to prove: %s", strings.TrimSpace(string(raw)))
	}

	a := newTestApp(t)
	a.settings.WorkspacePath = ws

	if out, ok := a.execTool("git_diff", `{}`); !ok || strings.Contains(out, "TEXTCONV_RAN") || !strings.Contains(out, "+two") {
		t.Fatalf("git_diff must show the real change and never the textconv driver output: %v", out)
	}
	if out, ok := a.execTool("git_log", `{}`); !ok || strings.Contains(out, "TEXTCONV_RAN") {
		t.Fatalf("git_log must not run diff drivers: %v", out)
	}
	if out, ok := a.execTool("git_show", `{"ref":"HEAD"}`); !ok || strings.Contains(out, "TEXTCONV_RAN") {
		t.Fatalf("git_show must not run diff drivers: %v", out)
	}

	// همان حمله از راه diff.external (GIT_EXTERNAL_DIFF).
	ext := markerScript(t, ws, "EXTERNAL_DIFF_RAN")
	gitInForTest(t, ws, "config", "diff.external", ext)
	gitInForTest(t, ws, "config", "--unset", "diff.evil.textconv")
	if out, ok := a.execTool("git_diff", `{}`); !ok || strings.Contains(out, "EXTERNAL_DIFF_RAN") || !strings.Contains(out, "+two") {
		t.Fatalf("git_diff must not run diff.external: %v", out)
	}
	if out, ok := a.execTool("git_show", `{"ref":"HEAD"}`); !ok || strings.Contains(out, "EXTERNAL_DIFF_RAN") {
		t.Fatalf("git_show must not run diff.external: %v", out)
	}
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
