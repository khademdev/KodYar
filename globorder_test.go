// تست‌های مرتب‌سازی glob بر اساس mtime (جدیدترین اول) — ترتیب، تساوی،
// سقف کاندیدها و رفتار لغو/فیلتر بدون تغییر.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setMtime زمان تغییر فایل را قطعی جلو می‌برد (گام‌های ≥۲ ثانیه — FAT هم
// تفکیک ۲ ثانیه‌ای دارد، پس تست روی همه‌ی فایل‌سیستم‌ها پایدار است).
func setMtime(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestGlobMtimeOrderingNewestFirst(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	base := time.Now().Add(-time.Hour)

	// پنج فایل با mtime صعودی (قدیمی‌تر ← جدیدتر)
	names := []string{"old.txt", "mid.txt", "new.txt", "newest.txt", "tie-a.txt"}
	for i, n := range names {
		writeWsFile(t, ws, n, "x")
		setMtime(t, filepath.Join(ws, n), base.Add(time.Duration(2*i)*time.Second))
	}
	// tie-b.txt هم‌زمانِ tie-a.txt — برخورد با ترتیب مسیر حل می‌شود
	writeWsFile(t, ws, "tie-b.txt", "x")
	setMtime(t, filepath.Join(ws, "tie-b.txt"), base.Add(8*time.Second))

	r, ok := a.toolGlob(context.Background(), "*.txt", "")
	if !ok {
		t.Fatalf("glob failed: %s", r)
	}
	lines := strings.Split(r, "\n")
	// خط اول هدر است؛ ترتیب مورد انتظار: tie-a/tie-b (جدیدترین، +8s) با
	// برخوردِ مسیر، بعد newest (+6s)، new، mid و old (قدیمی‌ترین آخر).
	wantOrder := []string{"tie-a.txt", "tie-b.txt", "newest.txt", "new.txt", "mid.txt", "old.txt"}
	got := []string{}
	for _, l := range lines[1:] {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "...[") {
			got = append(got, l)
		}
	}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d files (%v), want %d", len(got), got, len(wantOrder))
	}
	for i, want := range wantOrder {
		if got[i] != want {
			t.Fatalf("order position %d = %q, want %q (full: %v)", i, got[i], want, got)
		}
	}
	if !strings.Contains(r, "newest first") {
		t.Fatalf("header must say newest first: %.120s", r)
	}
}

func TestGlobMtimeOrderingNestedPaths(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	base := time.Now().Add(-2 * time.Hour)

	// فایل‌های تودرتو — جدیدترین باید صدر بگیرد فارغ از عمق مسیر
	files := []string{"a/deep/x.txt", "b/y.txt", "z.txt"}
	for i, f := range files {
		writeWsFile(t, ws, f, "x")
		setMtime(t, filepath.Join(ws, filepath.FromSlash(f)), base.Add(time.Duration(2*i)*time.Second))
	}
	r, ok := a.toolGlob(context.Background(), "**/*.txt", "")
	if !ok {
		t.Fatalf("glob failed: %s", r)
	}
	lines := strings.SplitN(r, "\n", 4) // هدر + ۳ فایل
	if lines[1] != "z.txt" || lines[2] != "b/y.txt" || lines[3] != "a/deep/x.txt" {
		t.Fatalf("nested order wrong:\n%s", r)
	}
}

func TestGlobCandidateBoundAndResultCap(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath

	// سقف نتیجه: ۵۰۰ نتیجه برمی‌گردد حتی وقتی کاندیدها بیشترند
	for i := 0; i < maxGlobResults+15; i++ {
		writeWsFile(t, ws, fmt.Sprintf("f%04d.txt", i), "x")
	}
	r, ok := a.toolGlob(context.Background(), "*.txt", "")
	if !ok {
		t.Fatalf("glob failed: %s", r)
	}
	if !strings.Contains(r, fmt.Sprintf("Found %d file(s)", maxGlobResults)) {
		t.Fatalf("result must be capped at %d after sorting: %.200s", maxGlobResults, r)
	}
	if !strings.Contains(r, "result limit reached") {
		t.Fatalf("cap notice missing: %.200s", r)
	}

	// سقف کاندیدها (۲۰۰۰۰): توقف پیمایش و اعلام برش — با ساخت سریع فایل‌های
	// خالی قابل تست است (فایل‌ها فقط «دیده» می‌شوند؛ خواندن محتوا در کار نیست).
	if os.MkdirAll(filepath.Join(ws, "many"), 0o755) != nil {
		t.Fatal("mkdir failed")
	}
	for i := 0; i < maxGlobCandidates+10; i++ {
		p := filepath.Join(ws, "many", fmt.Sprintf("m%06d.txt", i))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, ok = a.toolGlob(context.Background(), "many/*.txt", "")
	if !ok {
		t.Fatalf("capped-candidates glob failed: %s", r)
	}
	if !strings.Contains(r, "result limit reached") {
		t.Fatalf("candidate-bound walk must surface the truncation notice: %.200s", r)
	}
	if !strings.Contains(r, fmt.Sprintf("Found %d file(s)", maxGlobResults)) {
		t.Fatalf("must still return exactly %d results: %.200s", maxGlobResults, r)
	}
}

func TestGlobOrderingKeepsFilteringAndCancellation(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	base := time.Now().Add(-time.Hour)

	// فیلترها با مرتب‌سازی جدید تغییری نکرده‌اند: gitignore + skipDirs
	writeGitignore(t, ws, "*.log\nbuild/\n")
	writeWsFile(t, ws, "keep.txt", "x")
	setMtime(t, filepath.Join(ws, "keep.txt"), base.Add(2*time.Second))
	writeWsFile(t, ws, "skip.log", "x")
	setMtime(t, filepath.Join(ws, "skip.log"), base.Add(4*time.Second)) // جدیدتر است ولی نادیده گرفته می‌شود
	writeWsFile(t, ws, "build/gen.txt", "x")
	setMtime(t, filepath.Join(ws, "build", "gen.txt"), base.Add(6*time.Second))
	writeWsFile(t, ws, "node_modules/pkg.txt", "x")

	r, ok := a.toolGlob(context.Background(), "**/*.txt", "")
	if !ok {
		t.Fatalf("glob failed: %s", r)
	}
	if !hasLine(r, "keep.txt") {
		t.Fatalf("kept file missing: %s", r)
	}
	for _, banned := range []string{"skip.log", "build/gen.txt", "node_modules/pkg.txt"} {
		if hasLine(r, banned) {
			t.Fatalf("ignored path %s appeared: %s", banned, r)
		}
	}

	// لغو حین پیمایش — همچنان بدون بازگشت نتیجه‌ی نصفه
	cancelled := false
	ctx := &cancelAfterErrCtx{Context: context.Background(), remaining: 2, cancelled: &cancelled}
	r, ok = a.toolGlob(ctx, "**/*.txt", "")
	if ok || r != "ERROR: cancelled by the user." {
		t.Fatalf("cancel during walk must abort: %v (%v)", r, ok)
	}
	if !cancelled {
		t.Fatal("cancellation must be observed")
	}
}
