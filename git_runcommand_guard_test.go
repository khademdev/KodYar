// تست: در run_command ایجنت فقط یک لیست سفید کوچک از git اجرا می‌شود
// (status/rev-parse/remote/branch). دلیل: هر زیرفرمان دیگر یا می‌تواند
// درایور بیرونیِ مخزن را اجرا کند (textconv/diff.external) یا بیرون از
// جریان تأیید diff فایل می‌نویسد/حذف می‌کند/تاریخ را بازنویسی می‌کند.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// committedRepoForTest یک مخزن تازه با یک کامیت برمی‌گرداند.
func committedRepoForTest(t *testing.T) string {
	t.Helper()
	gitAvailableForTest(t)
	ws := t.TempDir()
	gitInForTest(t, ws, "init", "-q")
	gitInForTest(t, ws, "config", "user.email", "test@example.com")
	gitInForTest(t, ws, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitInForTest(t, ws, "add", "-A")
	gitInForTest(t, ws, "commit", "-qm", "init")
	return ws
}

// TestGitRunCommandAllowList جدول تشخیص (تابع خالص).
func TestGitRunCommandAllowList(t *testing.T) {
	blocked := []string{
		// درایور بیرونی
		"git diff", "git diff HEAD", "git log --oneline -n 5", "git show HEAD",
		"git --no-pager diff", "git diff-tree HEAD", "git diff-index HEAD",
		"git diff-files", "git range-diff HEAD~1 HEAD", "git format-patch -1",
		"git whatchanged", "git stash show", "git stash show -p",
		// ترکیب/زیرپوسته
		"git status && git diff", "git diff | head -5", `sh -c "git diff"`,
		// نوشتن/حذف/تاریخ در مخزن
		"git apply fix.patch", "git am 0001-fix.patch",
		"git submodule update --init",
		"git checkout main", "git checkout -- .", "git restore .",
		"git clean -fd", "git reset --hard", "git switch main",
		"git rm a.txt", "git mv a b", "git add -A", "git commit -m x",
		// شبکه/پیکربندی — در هیچ لیست سیاه قدیمی نبودند
		"git push", "git fetch", "git merge x", "git rebase main",
		"git tag v1", "git init", "git config core.fsmonitor evil",
	}
	for _, cmd := range blocked {
		reason, ok := agentCommandBlocked(cmd)
		if !ok {
			t.Errorf("%q must be refused from run_command", cmd)
			continue
		}
		if !strings.Contains(reason, "git_diff") {
			t.Errorf("%q: the refusal must name the safe alternative, got %q", cmd, reason)
		}
	}

	allowed := []string{
		"git status", "git status --short", "git branch -a",
		"git branch show", // نام شاخه، نه زیرفرمان show
		"git rev-parse", "git rev-parse --show-toplevel", "git remote -v",
		"git --version", // بدون زیرفرمان → دروازه تصمیم می‌گیرد
		"echo git diff", "go test ./...", "ls -la",
	}
	for _, cmd := range allowed {
		if reason, ok := agentCommandBlocked(cmd); ok {
			t.Errorf("%q must not be refused, got %q", cmd, reason)
		}
	}
}

// TestAllowedGitSubcommandsStillUseTheGate: لیست سفید فقط درِ ورودی است؛
// زیرفرمان‌های مجاز باید مثل قبل از دروازه‌ی allow/ask بگذرند.
func TestAllowedGitSubcommandsStillUseTheGate(t *testing.T) {
	ws := committedRepoForTest(t)

	a := newTestApp(t)
	a.settings.WorkspacePath = ws

	// allow: فقط‌خواندنیِ مجاز → بدون مودال اجرا می‌شود.
	for _, cmd := range []string{"git status --short", "git branch -a", "git rev-parse --short HEAD"} {
		if out, ok := a.execTool("run_command", fmt.Sprintf(`{"command":%q}`, cmd)); !ok {
			t.Fatalf("%s must run (the gate allows it): %s", cmd, out)
		}
	}

	// ask: پرچم خطرناکِ همان زیرفرمان مجاز → مودال؛ بدون UI = رد.
	out, ok := a.execTool("run_command", `{"command":"git branch -D no-such-branch"}`)
	if ok {
		t.Fatalf("a destructive branch flag must not run without approval: %s", out)
	}
	if !strings.Contains(out, "rejected") {
		t.Fatalf("the gate must decide, not the allow-list; got: %s", out)
	}
}

// TestRunCommandRefusesGitOutsideAllowList اثبات سرتاسری: مخزن آلوده (textconv)
// و زیرفرمان بازنویسنده، و ابزار اختصاصی همان کار را سالم انجام می‌دهد.
func TestRunCommandRefusesGitOutsideAllowList(t *testing.T) {
	ws := committedRepoForTest(t)

	tc := markerScript(t, ws, "TEXTCONV_RAN")
	gitInForTest(t, ws, "config", "diff.evil.textconv", tc)
	if err := os.WriteFile(filepath.Join(ws, ".gitattributes"), []byte("*.txt diff=evil\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := newTestApp(t)
	a.settings.WorkspacePath = ws

	out, ok := a.execTool("run_command", `{"command":"git diff"}`)
	if ok {
		t.Fatalf("run_command must refuse `git diff` (a repo driver could execute code): %s", out)
	}
	if !strings.Contains(out, "git_diff") {
		t.Fatalf("the refusal must name the safe tools, got: %s", out)
	}
	if out, ok := a.execTool("run_command", `{"command":"git reset --hard"}`); ok {
		t.Fatalf("run_command must refuse `git reset --hard` (it rewrites files without a diff): %s", out)
	}

	if diff, ok := a.execTool("git_diff", `{}`); !ok || strings.Contains(diff, "TEXTCONV_RAN") || !strings.Contains(diff, "+two") {
		t.Fatalf("git_diff must show the real change and no driver output: %v", diff)
	}
}
