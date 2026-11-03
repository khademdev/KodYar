// تست‌های ابزارهای Git (اولویت ۴).
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupGitRepo یک workspace با مخزن git واقعی و دو کامیت می‌سازد.
// اگر git در دسترس نباشد، تست skip می‌شود (CI بدون git).
func setupGitRepo(t *testing.T) *App {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", ws}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("hello\n"), 0o644)
	run("add", ".")
	run("commit", "-m", "first commit")
	os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("hello world\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "new.txt"), []byte("new file\n"), 0o644)
	run("add", "new.txt")
	run("commit", "-m", "second commit")
	// تغییر working-tree بدون commit (برای dirty state)
	os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("hello world changed\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "untracked.txt"), []byte("u"), 0o644)
	return a
}

func TestGitStatus(t *testing.T) {
	a := setupGitRepo(t)
	out, ok := a.execTool("git_status", `{}`)
	if !ok {
		t.Fatal(out)
	}
	if !strings.Contains(out, "branch: main") && !strings.Contains(out, "Branch: main") {
		t.Fatalf("branch missing: %s", out)
	}
	if !strings.Contains(out, "Untracked: 1") || !strings.Contains(out, "Unstaged: 1") {
		t.Fatalf("counts wrong:\n%s", out)
	}
	if !strings.Contains(out, "untracked.txt") || !strings.Contains(out, "hello.txt") {
		t.Fatalf("files missing:\n%s", out)
	}
}

func TestGitDiffAndLog(t *testing.T) {
	a := setupGitRepo(t)

	// diff بدون staged → تغییر working tree (مقایسه با index: نسخه‌ی کامیت‌شده «hello»)
	out, ok := a.execTool("git_diff", `{}`)
	if !ok {
		t.Fatal(out)
	}
	if !strings.Contains(out, "-hello\n") || !strings.Contains(out, "+hello world changed") {
		t.Fatalf("working-tree diff wrong:\n%s", out)
	}

	// diff یک مسیر مشخص
	out, ok = a.execTool("git_diff", `{"path":"hello.txt"}`)
	if !ok {
		t.Fatal(out)
	}
	if strings.Contains(out, "new.txt") {
		t.Fatal("path filter failed — new.txt must not appear")
	}

	// لاگ
	out, ok = a.execTool("git_log", `{"limit":5}`)
	if !ok {
		t.Fatal(out)
	}
	if !strings.Contains(out, "second commit") || !strings.Contains(out, "first commit") {
		t.Fatalf("log missing commits:\n%s", out)
	}
}

func TestGitShowRefValidation(t *testing.T) {
	a := setupGitRepo(t)

	out, ok := a.execTool("git_show", `{"ref":"HEAD"}`)
	if !ok {
		t.Fatal(out)
	}
	if !strings.Contains(out, "second commit") {
		t.Fatalf("HEAD show must contain latest commit:\n%s", out)
	}

	// ref خطرناک — option injection باید رد شود
	if out, ok := a.execTool("git_show", `{"ref":"--exec=evil"}`); ok {
		t.Fatalf("option injection must be rejected, got: %s", out)
	}
	if out, ok := a.execTool("git_show", `{"ref":"x;y"}`); ok {
		t.Fatalf("invalid ref must be rejected, got: %s", out)
	}
	// ref خالی
	if _, ok := a.execTool("git_show", `{"ref":""}`); ok {
		t.Fatal("empty ref must fail")
	}
}

func TestGitContextLine(t *testing.T) {
	a := setupGitRepo(t)
	line := a.gitContextLine(context.Background())
	if line == "" {
		t.Fatal("context line must exist for a real repo")
	}
	if !strings.Contains(line, "branch: main") || !strings.Contains(line, "DIRTY") {
		t.Fatalf("context line wrong: %s", line)
	}

	// workspace بدون git → خالی
	a2 := newTestApp(t)
	if l := a2.gitContextLine(context.Background()); l != "" {
		t.Fatalf("non-repo must give empty line, got: %s", l)
	}
}

func TestGitToolsOnNonRepo(t *testing.T) {
	a := newTestApp(t) // workspace بدون git
	out, ok := a.execTool("git_status", `{}`)
	if ok || !strings.Contains(out, "not a git repository") {
		t.Fatalf("non-repo must produce clear error, got: %s", out)
	}
}
