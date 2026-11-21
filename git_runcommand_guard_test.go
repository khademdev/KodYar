// تست: در run_command ایجنت فقط یک لیست سفید کوچک از git اجرا می‌شود.
//
// چرا لیست سفید: لیست سیاهِ فزاینده هر بار یک زیرفرمان از قلم می‌انداخت.
// هر زیرفرمانی جز status/rev-parse/remote/branch یا می‌تواند درایور بیرونیِ
// مخزن را اجرا کند (خانواده‌ی diff با textconv/diff.external)، یا بیرون از
// جریان تأیید diff فایل می‌نویسد/حذف می‌کند (apply، am، checkout، reset،
// switch، clean، …)، یا تاریخ را بازنویسی می‌کند.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentCommandBlockedGitDiffDrivers جدول تشخیص (تابع خالص).
func TestAgentCommandBlockedGitDiffDrivers(t *testing.T) {
	blocked := []string{
		// خانواده‌ی diff (درایور بیرونی)
		"git diff", "git diff HEAD", "git diff --stat --cached",
		"git log", "git log --oneline -n 5", "git show HEAD",
		"git --no-pager diff",
		"git diff-tree HEAD", "git diff-index HEAD", "git diff-files",
		"git range-diff HEAD~1 HEAD", "git format-patch -1", "git whatchanged",
		"git stash show", "git stash show -p",
		// ترکیب/زیرپوسته
		"git status && git diff", "git status && git log --oneline",
		"git diff | head -5", `sh -c "git diff"`,
		// نوشتن/حذف در مخزن
		"git apply fix.patch", "git apply --index fix.patch",
		"git am 0001-fix.patch",
		"git submodule update --init",
		"git submodule add https://example.com/x.git vendor/x",
		"git checkout main", "git checkout -- .", "git checkout -b feature",
		"git restore .", "git restore --staged a.txt",
		"git clean -fd", "git clean -fdx",
		"git reset", "git reset --hard", "git reset --soft HEAD~1",
		"git switch main", "git switch -c feature",
		"git rm a.txt", "git mv a b", "git add -A",
		// تاریخ/شبکه/پیکربندی — زیرفرمان‌هایی که در هیچ لیست سیاهی نبودند
		"git commit -m x", "git push", "git push --force",
		"git fetch", "git merge x", "git rebase main", "git cherry-pick abc",
		"git tag v1", "git init", "git clone https://example.com/x.git",
		"git config core.fsmonitor evil", "git worktree add ../wt",
	}
	for _, cmd := range blocked {
		reason, ok := agentCommandBlocked(cmd)
		if !ok {
			t.Errorf("%q must be refused from run_command (only the small allow-list may run)", cmd)
			continue
		}
		if !strings.Contains(reason, "git_diff") && !strings.Contains(reason, "apply_patch") {
			t.Errorf("%q: the refusal must point at the safe alternative, got %q", cmd, reason)
		}
	}

	allowed := []string{
		"git status",
		"git status --short",
		"git branch -a",
		"git branch show",  // نام شاخه، نه زیرفرمان show
		"git branch stash", // همان
		"git rev-parse",
		"git rev-parse --show-toplevel",
		"git remote -v",
		"git --version", // بدون زیرفرمان → دروازه تصمیم می‌گیرد
		"echo git diff",
		"go test ./...",
		"ls -la",
	}
	for _, cmd := range allowed {
		if reason, ok := agentCommandBlocked(cmd); ok {
			t.Errorf("%q must not be refused, got %q", cmd, reason)
		}
	}
}

// TestRunCommandRejectsGitDiffDriver اثبات سرتاسری: مخزن آلوده، و run_command
// ایجنت به‌جای اجرای فرمان، ابزار درست را پیشنهاد می‌دهد.
func TestRunCommandRejectsGitDiffDriver(t *testing.T) {
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

	// همان قاعده برای یک زیرفرمان نویسنده که در لیست سیاه قدیمی نبود.
	if out, ok := a.execTool("run_command", `{"command":"git reset --hard"}`); ok {
		t.Fatalf("run_command must refuse `git reset --hard` (it rewrites files without a diff): %s", out)
	}

	// ابزار اختصاصی همان کار را بدون درایور انجام می‌دهد.
	if diff, ok := a.execTool("git_diff", `{}`); !ok || strings.Contains(diff, "TEXTCONV_RAN") || !strings.Contains(diff, "+two") {
		t.Fatalf("git_diff must show the real change and no driver output: %v", diff)
	}
}
