// تست: زیرفرمان‌های git diff/show/log از مسیر run_command ایجنت رد می‌شوند.
//
// چرا: این زیرفرمان‌ها با .gitattributes (`diff=<driver>`) و
// `diff.<driver>.textconv` یا `diff.external` در .git/config می‌توانند فرمان
// بیرونی اجرا کنند و git در ویندوز از سندباکس AppContainer مستثناست. ایجنت
// برای همین کار سه ابزار اختصاصی دارد که با --no-ext-diff --no-textconv
// اجرا می‌شوند.
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
		"git diff",
		"git diff HEAD",
		"git diff --stat --cached",
		"git log",
		"git log --oneline -n 5",
		"git show HEAD",
		"git --no-pager diff",
		"git status && git diff",
		"git status && git log --oneline",
		"git diff | head -5",
		`sh -c "git diff"`,
		// زیرفرمان‌های دیگری که همان درایور/فرمان بیرونی را اجرا می‌کنند
		"git diff-tree HEAD",
		"git diff-index HEAD",
		"git diff-files",
		"git range-diff HEAD~1 HEAD",
		"git format-patch -1",
		"git whatchanged",
		"git stash show",
		"git stash show -p",
	}
	for _, cmd := range blocked {
		reason, ok := agentCommandBlocked(cmd)
		if !ok {
			t.Errorf("%q must be refused from run_command: it can run a diff driver", cmd)
			continue
		}
		if !strings.Contains(reason, "git_diff") {
			t.Errorf("%q: the refusal must point at the git tools, got %q", cmd, reason)
		}
	}

	allowed := []string{
		"git status",
		"git status --short",
		"git branch -a",
		"git branch show", // نام شاخه، نه زیرفرمان show
		"git branch stash",
		"git rev-parse --show-toplevel",
		"git remote -v",
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

	// ابزار اختصاصی همان کار را بدون درایور انجام می‌دهد.
	if diff, ok := a.execTool("git_diff", `{}`); !ok || strings.Contains(diff, "TEXTCONV_RAN") || !strings.Contains(diff, "+two") {
		t.Fatalf("git_diff must show the real change and no driver output: %v", diff)
	}
}
