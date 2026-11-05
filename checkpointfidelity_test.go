// تست‌های Stage 4.2/4.3 — چک‌پوینت بایت‌به‌بایت و بازگردانی ایمن/کران‌دار.
//
// 4.2: snapshot و restore نباید هیچ فیلتری (core.autocrlf، .gitattributes،
// Git LFS/clean/smudge) اعمال کنند؛ بازگردانی باید بایت‌به‌بایت باشد.
// 4.3: مسیری که یک جزءش symlink/junction به بیرون است رد می‌شود، و مقایسه/
// بازگردانی جریانی است (بدون materialize کامل و بدون ReadFile ×۲).
package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fidelityWorkspace یک مخزن git با تنظیمات «مخرب» کاربر می‌سازد: autocrlf،
// .gitattributes با eol=crlf و filter شکسته (شبیه LFS نصب‌نشده).
func fidelityWorkspace(t *testing.T) (string, map[string][]byte) {
	t.Helper()
	ws := t.TempDir()
	gitRun(t, ws, "init", "-q")
	gitRun(t, ws, "config", "user.name", "T")
	gitRun(t, ws, "config", "user.email", "t@t")
	gitRun(t, ws, "config", "core.autocrlf", "true")
	gitRun(t, ws, "config", "filter.broken.clean", "myai-filter-does-not-exist")
	gitRun(t, ws, "config", "filter.broken.smudge", "myai-filter-does-not-exist")

	files := map[string][]byte{
		".gitattributes":       []byte("* text=auto eol=crlf\n*.bin -text\n*.dat filter=broken\n"),
		"lf.txt":               []byte("alpha\nbeta\ngamma\n"),
		"crlf.txt":             []byte("one\r\ntwo\r\n"),
		"blob.bin":             {0x00, 0x01, 0xFF, 0xFE, '\r', '\n', 0x7F, 0x80},
		"data.dat":             []byte("payload\r\nwith binary \x00 tail"),
		"پروژه/یادداشت‌ها.txt": []byte("\ufeffخط اول\r\nدوم‌خط با نیم‌فاصله\r\n"),
	}
	for rel, body := range files {
		p := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, ws, "add", "-A")
	gitRun(t, ws, "commit", "-q", "-m", "init")
	return ws, files
}

// readTree فایل‌های داده‌شده را بایت‌به‌بایت می‌خواند.
func readTree(t *testing.T, ws string, files map[string][]byte) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte, len(files))
	for rel := range files {
		raw, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		out[rel] = raw
	}
	return out
}

// TestCheckpointByteFaithfulWithoutFilters — با autocrlf، attributes و فیلتر
// شکسته، ساخت و بازگردانی باید موفق و بایت‌به‌بایت باشد.
func TestCheckpointNoFilterByteFaithful(t *testing.T) {
	a := newTestApp(t)
	ws, files := fidelityWorkspace(t)
	a.mu.Lock()
	a.settings.WorkspacePath = ws
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := a.createCheckpoint(ctx, ws, "conv-fid", 3); err != nil {
		t.Fatalf("checkpoint must not apply user filters: %v", err)
	}
	before := readTree(t, ws, files)

	// تغییر، حذف و فایل تازه
	if err := os.WriteFile(filepath.Join(ws, "lf.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(ws, "blob.bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "new.txt"), []byte("new\r\nfile\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prev := a.PreviewCheckpointRestore("conv-fid", 3)
	if !prev.Available || prev.Error != "" {
		t.Fatalf("preview unavailable: %+v", prev)
	}
	if !containsPath(prev.Rewrite, "lf.txt") || !containsPath(prev.Rewrite, "blob.bin") {
		t.Fatalf("preview must list changed and deleted files: %+v", prev.Rewrite)
	}
	if !containsPath(prev.Trash, "new.txt") {
		t.Fatalf("preview must list the new file as trash: %+v", prev.Trash)
	}
	for _, p := range []string{"crlf.txt", "data.dat", "پروژه/یادداشت‌ها.txt"} {
		if !containsPath(prev.Unchanged, p) {
			t.Fatalf("untouched file %q must be unchanged (no filter rewriting): rewrite=%v unchanged=%v", p, prev.Rewrite, prev.Unchanged)
		}
	}

	rep := a.RestoreCheckpoint("conv-fid", 3)
	if len(rep.Errors) != 0 {
		t.Fatalf("restore errors: %v", rep.Errors)
	}
	after := readTree(t, ws, files)
	for rel, want := range before {
		if string(after[rel]) != string(want) {
			t.Fatalf("%q not byte-identical after restore: got %q want %q", rel, after[rel], want)
		}
	}
	if _, err := os.Stat(filepath.Join(ws, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("file created after the checkpoint must be moved to trash, stat err=%v", err)
	}
	// بازگردانی خودش قابل‌برگشت است: نسخه‌ی تغییریافته در سبد زباله است.
	if rep.TrashDir == "" {
		t.Fatal("trash dir must be reported so the restore itself can be undone")
	}
	trashed, err := os.ReadFile(filepath.Join(rep.TrashDir, "lf.txt"))
	if err != nil || string(trashed) != "changed\n" {
		t.Fatalf("previous version must be preserved in trash (err=%v content=%q)", err, string(trashed))
	}
}

// containsPath آیا مسیر در فهرست آمده است؟
func containsPath(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestCheckpointPreviewListsOnlyChanged — پیش‌نمایش نباید فایل‌های دست‌نخورده
// را «rewrite» نشان دهد (مشکل قبلی با CRLF/LFS).
func TestCheckpointPreviewListsOnlyChanged(t *testing.T) {
	a := newTestApp(t)
	ws, _ := fidelityWorkspace(t)
	a.mu.Lock()
	a.settings.WorkspacePath = ws
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := a.createCheckpoint(ctx, ws, "conv-prev", 1); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "lf.txt"), []byte("only this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := a.PreviewCheckpointRestore("conv-prev", 1)
	if len(prev.Rewrite) != 1 || prev.Rewrite[0] != "lf.txt" {
		t.Fatalf("preview must list exactly lf.txt, got %v (skipped=%v)", prev.Rewrite, prev.Skipped)
	}
}

// fakeInfo پیاده‌سازی کمینه‌ی os.FileInfo برای مسیریاب تزریقی.
type fakeInfo struct{ dir bool }

func (f fakeInfo) Name() string { return "fake" }
func (f fakeInfo) Size() int64  { return 0 }
func (f fakeInfo) Mode() os.FileMode {
	if f.dir {
		return os.ModeDir
	}
	return 0
}
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.dir }
func (f fakeInfo) Sys() interface{}   { return nil }

// TestResolveRestorePathRefusesSymlinkedParent — با FS جعلی: هر جزء پیوندی رد
// می‌شود (روی همه‌ی سکوها اجرا می‌شود).
func TestResolveRestorePathRefusesSymlinkedParent(t *testing.T) {
	ws := t.TempDir()
	fs := restoreFS{
		evalSymlinks: func(p string) (string, error) { return p, nil },
		lstat:        func(string) (os.FileInfo, error) { return fakeInfo{dir: true}, nil },
		reparse:      func(p string) bool { return strings.HasSuffix(filepath.ToSlash(p), "link") },
	}
	if _, err := resolveRestorePath(ws, "link/secret.txt", fs); err == nil {
		t.Fatal("a symlink/junction component must be refused")
	}
}

// TestResolveRestorePathRefusesOutsideResolution — حتی بدون جزء پیوندی، اگر
// والد واقعی بیرون ریشه حل شود، نوشتن رد می‌شود.
func TestResolveRestorePathRefusesOutsideResolution(t *testing.T) {
	ws := t.TempDir()
	outside := filepath.Dir(ws)
	fs := restoreFS{
		evalSymlinks: func(p string) (string, error) {
			if filepath.Clean(p) == filepath.Clean(ws) {
				return ws, nil
			}
			return outside, nil
		},
		lstat:   func(string) (os.FileInfo, error) { return fakeInfo{dir: true}, nil },
		reparse: func(string) bool { return false },
	}
	if _, err := resolveRestorePath(ws, "sub/file.txt", fs); err == nil {
		t.Fatal("a parent resolving outside the workspace must be refused")
	}
}

// TestResolveRestorePathAcceptsPlainPath — مسیر معمولی داخل پروژه پذیرفته
// می‌شود.
func TestResolveRestorePathAcceptsPlainPath(t *testing.T) {
	ws := t.TempDir()
	fs := restoreFS{
		evalSymlinks: func(p string) (string, error) { return p, nil },
		lstat:        func(string) (os.FileInfo, error) { return fakeInfo{dir: true}, nil },
		reparse:      func(string) bool { return false },
	}
	abs, err := resolveRestorePath(ws, "src/main.go", fs)
	if err != nil {
		t.Fatalf("plain path must be accepted: %v", err)
	}
	if abs != filepath.Join(ws, "src", "main.go") {
		t.Fatalf("unexpected path %q", abs)
	}
}

// TestRestoreLargeFileMemoryBounded — فایل بزرگ نباید کل محتوا را در حافظه
// بیاورد (مقایسه/بازگردانی جریانی).
func TestRestoreLargeFileMemoryBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("large file test")
	}
	a := newTestApp(t)
	ws := t.TempDir()
	gitRun(t, ws, "init", "-q")
	gitRun(t, ws, "config", "user.name", "T")
	gitRun(t, ws, "config", "user.email", "t@t")

	const size = 32 << 20 // ۳۲ مگابایت — سقف حافظه باید بسیار کمتر از دوبرابر آن باشد
	big := filepath.Join(ws, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for i := range chunk {
		chunk[i] = byte(i % 251)
	}
	for i := 0; i < size/len(chunk); i++ {
		if _, err := f.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	gitRun(t, ws, "add", "-A")
	gitRun(t, ws, "commit", "-q", "-m", "init")
	a.mu.Lock()
	a.settings.WorkspacePath = ws
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := a.createCheckpoint(ctx, ws, "conv-big", 1); err != nil {
		t.Fatal(err)
	}
	// تغییر یک بایت در ابتدا و انتها
	h, err := os.OpenFile(big, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.WriteAt([]byte{0xAA}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := h.WriteAt([]byte{0xBB}, size-1); err != nil {
		t.Fatal(err)
	}
	_ = h.Close()

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	prev := a.PreviewCheckpointRestore("conv-big", 1)
	runtime.ReadMemStats(&after)
	if !prev.Available || !containsPath(prev.Rewrite, "big.bin") {
		t.Fatalf("large changed file must be detected: %+v", prev)
	}
	if grown := after.HeapAlloc - before.HeapAlloc; grown > size/2 {
		t.Fatalf("preview must stream, but heap grew by %d bytes for a %d-byte file", grown, size)
	}

	rep := a.RestoreCheckpoint("conv-big", 1)
	if len(rep.Errors) != 0 || !containsPath(rep.Restored, "big.bin") {
		t.Fatalf("large file restore failed: %v", rep.Errors)
	}
	st, err := os.Stat(big)
	if err != nil || st.Size() != size {
		t.Fatalf("restored size mismatch: %v %d", err, st.Size())
	}
}

// TestCheckpointLimitExceededChangesNothing — بیش از سقف فایل: خطای روشن،
// بدون هیچ تغییری در پروژه.
func TestCheckpointLimitExceededChangesNothing(t *testing.T) {
	a := newTestApp(t)
	ws := t.TempDir()
	gitRun(t, ws, "init", "-q")
	gitRun(t, ws, "config", "user.name", "T")
	gitRun(t, ws, "config", "user.email", "t@t")
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(ws, n), []byte(n+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, ws, "add", "-A")
	gitRun(t, ws, "commit", "-q", "-m", "init")
	a.mu.Lock()
	a.settings.WorkspacePath = ws
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := a.createCheckpoint(ctx, ws, "conv-lim", 1); err != nil {
		t.Fatal(err)
	}
	// تغییر فایل، سپس سقف را پایین می‌آوریم: مقایسه باید پیش از هر نوشتنی رد شود.
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("CHANGED\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldMax := checkpointMaxFiles
	checkpointMaxFiles = 2
	defer func() { checkpointMaxFiles = oldMax }()

	rep := a.RestoreCheckpoint("conv-lim", 1)
	if len(rep.Errors) == 0 {
		t.Fatal("exceeding the file limit must be reported")
	}
	raw, _ := os.ReadFile(filepath.Join(ws, "a.txt"))
	if string(raw) != "CHANGED\n" {
		t.Fatalf("nothing must be modified when the limit is exceeded, got %q", string(raw))
	}
}
