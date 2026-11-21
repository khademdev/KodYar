// تست‌های ابزارهای نوشتنی git — git_create_branch و git_commit (نقشه‌ی راه ۱.۰.۱).
//
// قرارداد: هر دو ابزار پیش از هر تغییری مودال تأیید (kind=git_write) نشان
// می‌دهند؛ رد کاربر هیچ تغییری نمی‌سازد؛ hookهای مخزن اجرا نمی‌شوند و هیچ
// چیزی push نمی‌شود.
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitWriteTestRepo مخزن تازه با یک کامیت پایه می‌سازد و App را به آن وصل می‌کند.
func gitWriteTestRepo(t *testing.T) (*App, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ws := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", ws}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
		return string(out)
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/main")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@example.com")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "initial")

	a := newTestApp(t)
	a.mu.Lock()
	a.settings.WorkspacePath = ws
	a.mu.Unlock()
	a.ctx = context.Background()
	return a, ws
}

// gitWriteAnswerAsks همهٔ askها را ثبت می‌کند و با decision پاسخ می‌دهد.
// خروجی دوم: تعداد askهای دیده‌شده.
func gitWriteAnswerAsks(a *App, decision string) (*[]*permissionAsk, func() int) {
	asks := &[]*permissionAsk{}
	a.permAskEmit = func(ask *permissionAsk) {
		*asks = append(*asks, ask)
		_ = a.RespondPermissionConfirm(ask.ID, decision)
	}
	return asks, func() int { return len(*asks) }
}

func gitWriteOutput(t *testing.T, ws string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", ws}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestGitCreateBranchAsksThenCreates(t *testing.T) {
	a, ws := gitWriteTestRepo(t)
	asks, count := gitWriteAnswerAsks(a, permRespondAllow)

	out, ok := a.execTool("git_create_branch", `{"name":"feature/login"}`)
	if !ok {
		t.Fatalf("creating a branch must succeed after approval: %s", out)
	}
	if count() != 1 {
		t.Fatalf("exactly one confirmation modal expected, got %d", count())
	}
	ask := (*asks)[0]
	if ask.Kind != "git_write" || !ask.NoAlways {
		t.Fatalf("branch creation must ask with kind=git_write and no always option: %+v", ask)
	}
	if !strings.Contains(ask.Command, "feature/login") || !strings.Contains(ask.Message, "main") {
		t.Fatalf("modal must name the new branch and the current one: %+v", ask)
	}
	if branch := gitWriteOutput(t, ws, "rev-parse", "--abbrev-ref", "HEAD"); branch != "feature/login" {
		t.Fatalf("branch must be switched, got %q", branch)
	}
	if !strings.Contains(out, "feature/login") {
		t.Fatalf("result must name the branch: %s", out)
	}
}

func TestGitCreateBranchRejectedChangesNothing(t *testing.T) {
	a, ws := gitWriteTestRepo(t)
	gitWriteAnswerAsks(a, permRespondReject)

	out, ok := a.execTool("git_create_branch", `{"name":"feature/nope"}`)
	if ok {
		t.Fatalf("rejected branch creation must fail: %s", out)
	}
	if branch := gitWriteOutput(t, ws, "rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
		t.Fatalf("branch must stay unchanged after reject, got %q", branch)
	}
}

func TestGitCommitStagesAndCommits(t *testing.T) {
	a, ws := gitWriteTestRepo(t)
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "b.txt"), []byte("new file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	asks, count := gitWriteAnswerAsks(a, permRespondAllow)

	out, ok := a.execTool("git_commit", `{"message":"Update a and add b"}`)
	if !ok {
		t.Fatalf("commit must succeed after approval: %s", out)
	}
	if count() != 1 {
		t.Fatalf("exactly one confirmation modal expected, got %d", count())
	}
	ask := (*asks)[0]
	if ask.Kind != "git_write" || !ask.NoAlways {
		t.Fatalf("commit must ask with kind=git_write and no always option: %+v", ask)
	}
	joined := strings.Join(ask.Detail, " ")
	if !strings.Contains(joined, "a.txt") || !strings.Contains(joined, "b.txt") {
		t.Fatalf("modal must list the files to commit, got %v", ask.Detail)
	}
	if !strings.Contains(ask.Command, "Update a and add b") {
		t.Fatalf("modal must show the commit command: %q", ask.Command)
	}
	if status := gitWriteOutput(t, ws, "status", "--porcelain"); status != "" {
		t.Fatalf("working tree must be clean after commit, got %q", status)
	}
	if subject := gitWriteOutput(t, ws, "log", "-1", "--pretty=%s"); subject != "Update a and add b" {
		t.Fatalf("commit subject mismatch: %q", subject)
	}
	if files := gitWriteOutput(t, ws, "show", "--name-only", "--pretty=", "HEAD"); !strings.Contains(files, "b.txt") {
		t.Fatalf("new file must be part of the commit, got %q", files)
	}
}

func TestGitCommitRejectedLeavesRepoDirty(t *testing.T) {
	a, ws := gitWriteTestRepo(t)
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := gitWriteOutput(t, ws, "rev-parse", "HEAD")
	gitWriteAnswerAsks(a, permRespondReject)

	if out, ok := a.execTool("git_commit", `{"message":"nope"}`); ok {
		t.Fatalf("rejected commit must fail: %s", out)
	}
	if after := gitWriteOutput(t, ws, "rev-parse", "HEAD"); after != before {
		t.Fatal("rejected commit must not create a commit")
	}
	if status := gitWriteOutput(t, ws, "status", "--porcelain"); !strings.Contains(status, "a.txt") {
		t.Fatalf("rejected commit must not stage files, got %q", status)
	}
}

// TestGitCommitDoesNotRunRepoHooks — hookهای مخزن کد دلخواه اجرا می‌کنند؛
// ابزار commit باید با core.hooksPath خنثی‌شده کار کند.
func TestGitCommitDoesNotRunRepoHooks(t *testing.T) {
	a, ws := gitWriteTestRepo(t)
	marker := filepath.Join(ws, "hook_ran.txt")
	hook := filepath.Join(ws, ".git", "hooks", "pre-commit")
	script := "#!/bin/sh\necho ran > \"$(dirname \"$0\")/../../hook_ran.txt\"\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitWriteAnswerAsks(a, permRespondAllow)

	if out, ok := a.execTool("git_commit", `{"message":"no hooks"}`); !ok {
		t.Fatalf("commit must succeed: %s", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("repository pre-commit hook must never run")
	}
}

func TestGitWriteToolsValidateInput(t *testing.T) {
	a, ws := gitWriteTestRepo(t)
	_, count := gitWriteAnswerAsks(a, permRespondAllow)

	for _, name := range []string{"-x", "a..b", "x y", "trailing/", "with@{brace", ""} {
		body := `{"name":"` + name + `"}`
		if out, ok := a.execTool("git_create_branch", body); ok {
			t.Fatalf("invalid branch name %q must be rejected: %s", name, out)
		}
	}
	if out, ok := a.execTool("git_commit", `{"message":"   "}`); ok {
		t.Fatalf("empty commit message must be rejected: %s", out)
	}
	if out, ok := a.execTool("git_commit", `{"message":"ok","paths":["../outside.txt"]}`); ok {
		t.Fatalf("path outside the workspace must be rejected: %s", out)
	}
	if out, ok := a.execTool("git_commit", `{"message":"ok","paths":["/etc/passwd"]}`); ok {
		t.Fatalf("absolute path outside the workspace must be rejected: %s", out)
	}
	if count() != 0 {
		t.Fatalf("invalid arguments must never open a modal, got %d", count())
	}
	if branch := gitWriteOutput(t, ws, "rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
		t.Fatalf("invalid input must not change the branch, got %q", branch)
	}
}

// TestGitWriteToolsAlwaysAskEvenIfAllowed — override «allow» نمی‌تواند
// مودال نوشتن git را حذف کند (fail-closed).
func TestGitWriteToolsAlwaysAskEvenIfAllowed(t *testing.T) {
	a, _ := gitWriteTestRepo(t)
	a.mu.Lock()
	a.settings.Permissions.Tools = map[string]string{"git_commit": "allow", "git_create_branch": "allow"}
	a.mu.Unlock()
	for _, name := range []string{"git_commit", "git_create_branch"} {
		if d := a.toolPermDecisionLocked(name); d != permAsk {
			t.Fatalf("%s must stay permAsk even when the user allowed it, got %v", name, d)
		}
	}
}

func TestGitCommitNothingToCommit(t *testing.T) {
	a, _ := gitWriteTestRepo(t)
	asks, count := gitWriteAnswerAsks(a, permRespondAllow)
	out, ok := a.execTool("git_commit", `{"message":"empty"}`)
	if !ok {
		t.Fatalf("clean tree must not be a failure: %s", out)
	}
	if !strings.Contains(strings.ToLower(out), "nothing to commit") {
		t.Fatalf("clean tree must be reported clearly: %s", out)
	}
	if count() != 0 {
		t.Fatal("nothing to commit must not open a modal")
	}
	_ = asks
}

func TestGitCommitOutsideRepoFails(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.settings.WorkspacePath = t.TempDir()
	a.mu.Unlock()
	a.ctx = context.Background()
	asks, count := gitWriteAnswerAsks(a, permRespondAllow)
	if out, ok := a.execTool("git_commit", `{"message":"x"}`); ok || count() != 0 {
		t.Fatalf("non-repo commit must fail without a modal: ok=%v out=%s asks=%d", ok, out, count())
	}
	_ = asks
}
