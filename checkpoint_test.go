// تست‌های «نقطه‌ی بازگشت پروژه» (مأموریت ۳ — مرحله B).
// همه با یک مخزن git واقعیِ موقت اجرا می‌شوند؛ اگر git نصب نباشد skip.
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitAvailable آیا git قابل اجراست؟
func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// gitRun یک فرمان git در تست اجرا می‌کند (با هویت ثابت).
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// initGitRepo یک مخزن تازه با یک کامیت اولیه می‌سازد.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "user.name", "T")
	gitRun(t, dir, "config", "user.email", "t@t")
	must := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(".gitignore", "build/\n*.log\n")
	must("a.txt", "alpha\n")
	must("sub/b.txt", "beta\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// newCheckpointApp اپ آزمایشی روی یک مخزن git.
func newCheckpointApp(t *testing.T) (*App, string) {
	t.Helper()
	if !gitAvailable() {
		t.Skip("git is not installed")
	}
	a := newTestApp(t)
	ws := initGitRepo(t)
	a.mu.Lock()
	a.settings.WorkspacePath = ws
	a.mu.Unlock()
	return a, ws
}

// turnCtx زمینه‌ی یک نوبت واقعی برای ابزارها.
func turnCtx(convID string, turnID int64, rec *turnRecorder) context.Context {
	if rec == nil {
		rec = &turnRecorder{}
	}
	return withToolCallInfo(withTurnContext(context.Background(), turnID, rec), convID, "call-1")
}

// repoState وضعیت قابل‌مقایسه‌ی مخزن (HEAD + status + ایندکس).
func repoState(t *testing.T, ws string) string {
	t.Helper()
	return gitRun(t, ws, "rev-parse", "HEAD") + "\n" +
		gitRun(t, ws, "status", "--porcelain") + "\n" +
		gitRun(t, ws, "ls-files", "-s")
}

func readWs(t *testing.T, ws, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// TestCheckpointCreationLeavesRepoUntouched: ساخت نقطه‌ی بازگشت نباید HEAD،
// ایندکس یا working tree را عوض کند.
func TestCheckpointCreationLeavesRepoUntouched(t *testing.T) {
	a, ws := newCheckpointApp(t)
	// تغییرات محلیِ کاربر (staged + unstaged + untracked) باید دست‌نخورده بمانند
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("alpha changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, ws, "add", "a.txt")
	if err := os.WriteFile(filepath.Join(ws, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := repoState(t, ws)

	ctx, cancel := context.WithTimeout(context.Background(), checkpointTimeout)
	defer cancel()
	commit, err := a.createCheckpoint(ctx, ws, "conv-1", 7)
	if err != nil {
		t.Fatalf("createCheckpoint: %v", err)
	}
	if commit == "" {
		t.Fatal("empty commit")
	}
	if after := repoState(t, ws); after != before {
		t.Fatalf("checkpoint creation must not touch HEAD/index/worktree\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// ref ساخته شده و به یک کامیت اشاره می‌کند
	if out := gitRun(t, ws, "rev-parse", "--verify", checkpointRef("conv-1", 7)+"^{commit}"); out != commit {
		t.Fatalf("checkpoint ref points to %q, want %q", out, commit)
	}
	// والد = HEAD فعلی
	if parent := gitRun(t, ws, "rev-parse", commit+"^"); parent != gitRun(t, ws, "rev-parse", "HEAD") {
		t.Fatalf("checkpoint parent must be HEAD")
	}
}

// TestCheckpointRestoreByteExact: ویرایش + حذف + فایل تازه → بازگردانی
// بایت‌به‌بایت + سبد زباله.
func TestCheckpointRestoreByteExact(t *testing.T) {
	a, ws := newCheckpointApp(t)
	ctx := turnCtx("conv-r", 3, nil)
	a.ensureTurnCheckpoint(ctx)

	origA := readWs(t, ws, "a.txt")
	origB := readWs(t, ws, "sub/b.txt")
	// ویرایش، حذف و فایل تازه — مثل اثرهای جانبی یک دستور
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("HACKED\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(ws, "sub", "b.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "extra.txt"), []byte("side effect\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	preview := a.PreviewCheckpointRestore("conv-r", 3)
	if !preview.Available || preview.Error != "" {
		t.Fatalf("preview: %+v", preview)
	}
	if !containsStr(preview.Rewrite, "a.txt") || !containsStr(preview.Rewrite, "sub/b.txt") {
		t.Fatalf("preview must list rewritten files: %+v", preview)
	}
	if !containsStr(preview.Trash, "extra.txt") {
		t.Fatalf("preview must list files moved to trash: %+v", preview)
	}
	if containsStr(preview.Trash, "a.txt") {
		t.Fatalf("modified tracked file belongs to rewrite, not trash: %+v", preview)
	}

	rep := a.RestoreCheckpoint("conv-r", 3)
	if len(rep.Errors) != 0 {
		t.Fatalf("restore errors: %v", rep.Errors)
	}
	if got := readWs(t, ws, "a.txt"); got != origA {
		t.Fatalf("a.txt not restored: %q", got)
	}
	if got := readWs(t, ws, "sub/b.txt"); got != origB {
		t.Fatalf("deleted file not restored: %q", got)
	}
	if _, err := os.Stat(filepath.Join(ws, "extra.txt")); !os.IsNotExist(err) {
		t.Fatal("untracked file must be removed from the workspace")
	}
	// محتوای جایگزین در سبد زباله است (بازگردانی هم قابل‌برگشت است)
	if rep.TrashDir == "" {
		t.Fatal("trash dir must be reported")
	}
	trashed, err := os.ReadFile(filepath.Join(rep.TrashDir, "extra.txt"))
	if err != nil || string(trashed) != "side effect\n" {
		t.Fatalf("trashed content: %q err=%v", trashed, err)
	}
	trashedA, err := os.ReadFile(filepath.Join(rep.TrashDir, "a.txt"))
	if err != nil || string(trashedA) != "HACKED\n" {
		t.Fatalf("replaced content must be kept in trash: %q err=%v", trashedA, err)
	}
}

// TestCheckpointIgnoresIgnoredFiles: فایل ignore‌شده نه در snapshot است نه
// پاک می‌شود.
func TestCheckpointIgnoresIgnoredFiles(t *testing.T) {
	a, ws := newCheckpointApp(t)
	buildDir := filepath.Join(ws, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "out.bin"), []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.ensureTurnCheckpoint(turnCtx("conv-i", 1, nil))

	// دستور مخرب: build و یک فایل ignore‌شده‌ی دیگر را عوض می‌کند
	if err := os.WriteFile(filepath.Join(buildDir, "out.bin"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "debug.log"), []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}
	preview := a.PreviewCheckpointRestore("conv-i", 1)
	if !preview.Available {
		t.Fatalf("preview: %+v", preview)
	}
	for _, p := range append(append([]string{}, preview.Rewrite...), preview.Trash...) {
		if strings.HasPrefix(p, "build/") || p == "debug.log" {
			t.Fatalf("ignored path must never be touched: %s", p)
		}
	}
	rep := a.RestoreCheckpoint("conv-i", 1)
	if len(rep.Errors) != 0 {
		t.Fatalf("restore errors: %v", rep.Errors)
	}
	if got := readWs(t, ws, "build/out.bin"); got != "changed" {
		t.Fatalf("ignored file must stay untouched, got %q", got)
	}
	if got := readWs(t, ws, "debug.log"); got != "log" {
		t.Fatalf("ignored untracked file must stay, got %q", got)
	}
}

// TestCheckpointPreservesCRLFAndBOMOnRestore: بازگردانی بایت‌به‌بایت با
// BOM+CRLF+فارسی/ZWNJ و نام‌های فارسی.
func TestCheckpointPreservesCRLFAndBOMOnRestore(t *testing.T) {
	a, ws := newCheckpointApp(t)
	rel := "یادداشت‌های من/سند ۱.txt"
	content := "\xef\xbb\xbfسطر یک\r\nسطر دو با نیم‌فاصله\u200cاینجا\r\n"
	p := filepath.Join(ws, "یادداشت‌های من", "سند ۱.txt")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	a.ensureTurnCheckpoint(turnCtx("conv-fa", 1, nil))
	if err := os.WriteFile(p, []byte("عوض شد\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := a.RestoreCheckpoint("conv-fa", 1)
	if len(rep.Errors) != 0 {
		t.Fatalf("restore errors: %v", rep.Errors)
	}
	if got := readWs(t, ws, rel); got != content {
		t.Fatalf("Persian/BOM/CRLF bytes not restored: %q", got)
	}
}

// TestCheckpointPrunesOldRefs: حداکثر ۲۰ نقطه در هر پوشه.
func TestCheckpointPrunesOldRefs(t *testing.T) {
	a, ws := newCheckpointApp(t)
	// مهلت باید برای هر نقطه جدا باشد: یک ctx مشترک روی ۲۵ فراخوانی git
	// (هرکدام تا checkpointTimeout) در ماشین کند منقضی می‌شد و تست را
	// به‌صورت کاذب شکست می‌داد، نه منطق prune را.
	for i := 1; i <= checkpointMaxKeep+5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), checkpointTimeout)
		_, err := a.createCheckpoint(ctx, ws, "conv-p", int64(i))
		cancel()
		if err != nil {
			t.Fatalf("checkpoint %d: %v", i, err)
		}
	}
	refs := gitRun(t, ws, "for-each-ref", "--format=%(refname)", checkpointRefPrefix)
	lines := strings.Split(strings.TrimSpace(refs), "\n")
	if n := len(lines); n != checkpointMaxKeep {
		t.Fatalf("expected %d refs, got %d", checkpointMaxKeep, n)
	}
	// جدیدترین‌ها مانده‌اند
	if out := gitRun(t, ws, "rev-parse", "--verify", checkpointRef("conv-p", int64(checkpointMaxKeep+5))); out == "" {
		t.Fatal("newest checkpoint must survive pruning")
	}
}

// TestCheckpointNonGitWorkspace: پوشه‌ی غیر-git بی‌خطر و بی‌صدا رد می‌شود.
func TestCheckpointNonGitWorkspace(t *testing.T) {
	a := newTestApp(t)                                 // workspace موقت بدون git
	a.ensureTurnCheckpoint(turnCtx("conv-ng", 1, nil)) // نباید پنیک کند
	preview := a.PreviewCheckpointRestore("conv-ng", 1)
	if preview.Available || preview.Error == "" {
		t.Fatalf("non-git workspace must report unavailability: %+v", preview)
	}
	rep := a.RestoreCheckpoint("conv-ng", 1)
	if len(rep.Errors) == 0 {
		t.Fatal("restore on non-git workspace must report an error")
	}
}

// TestCheckpointTimeoutPath: زمینه‌ی منقضی → خطا، بدون ref نیمه‌کاره.
func TestCheckpointTimeoutPath(t *testing.T) {
	a, ws := newCheckpointApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.createCheckpoint(ctx, ws, "conv-t", 1); err == nil {
		t.Fatal("cancelled context must fail the checkpoint")
	}
	if a.checkpointExists("conv-t", 1) {
		t.Fatal("no ref may be created on a failed checkpoint")
	}
}

// TestCheckpointEmptyRepo: مخزن بدون هیچ کامیت (HEAD وجود ندارد).
func TestCheckpointEmptyRepo(t *testing.T) {
	a, _ := newCheckpointApp(t)
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "user.name", "T")
	gitRun(t, dir, "config", "user.email", "t@t")
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkpointTimeout)
	defer cancel()
	if _, err := a.createCheckpoint(ctx, dir, "conv-e", 1); err != nil {
		t.Fatalf("empty repo checkpoint: %v", err)
	}
}

// TestCheckpointPathEscapeRejected: مسیرهای فرارکننده هرگز پذیرفته نمی‌شوند.
func TestCheckpointPathEscapeRejected(t *testing.T) {
	bad := []string{"", "../x", "a/../../b", "/etc/passwd", `C:\Windows\win.ini`, "-rf", "..\\..\\x"}
	for _, p := range bad {
		if safeCheckpointRel(p) {
			t.Fatalf("unsafe path accepted: %q", p)
		}
	}
	good := []string{"a.txt", "sub/b.txt", "پوشه/فایل.txt", "پوشه فرعی/فایل ۲.txt"}
	for _, p := range good {
		if !safeCheckpointRel(p) {
			t.Fatalf("safe path rejected: %q", p)
		}
	}
	_, ws := newCheckpointApp(t)
	if _, err := checkpointAbs(ws, "../evil.txt"); err == nil {
		t.Fatal("checkpointAbs must reject escaping paths")
	}
	if err := moveToTrash(ws, "../evil.txt", filepath.Join(ws, ".trash")); err == nil {
		t.Fatal("moveToTrash must reject escaping paths")
	}
}

// TestCheckpointSweepTrash: سبد زباله‌ی قدیمی پاک می‌شود.
func TestCheckpointSweepTrash(t *testing.T) {
	cfg := t.TempDir()
	oldDir := filepath.Join(cfg, "trash", "ws1", "2020-01-01_000000.000")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "f.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// mtime قدیمی (۳۰ روز پیش)
	past := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(oldDir, past, past); err != nil {
		t.Fatal(err)
	}
	if n := sweepCheckpointTrash(cfg); n != 1 {
		t.Fatalf("sweep must remove the old trash folder, removed %d", n)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatal("old trash folder must be gone")
	}
}

func containsStr(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
