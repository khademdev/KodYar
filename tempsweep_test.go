// تست‌های رگرسیون — بخش جاروی فایل‌های موقتی (قابل‌حمل، هر دو OS).
//
// sweepStaleTempFiles در راه‌اندازی، فایل‌های.tmp جامانده از نوشتن‌های
// اتمیک نیم‌کاره (کرش بین نوشتن و rename — settings.json.tmp،
// changes.json.tmp، conversations/*.json.tmp، img/*.b64.tmp) را پاک می‌کند.
// تست‌های F4 (گارد ESRCH در کشتن گروه فرایند) در phase4_posix_test.go
// هستند چون به syscall یونیکس وابسته‌اند.
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSweepStaleTempFilesRemovesOnlyTmp درخت config شبیه‌سازی‌شده با
// فایل‌های موقتیِ جامانده در هر سه لایه (ریشه، conversations، img):
// فقط.tmpها حذف می‌شوند؛ فایل‌های واقعی و پوشه‌ها (حتی پوشه‌ی اسمش
// .tmp است) دست‌نخورده می‌مانند.
func TestSweepStaleTempFilesRemovesOnlyTmp(t *testing.T) {
	root := t.TempDir()

	// ساخت درخت: ریشه + conversations + conversations/img
	mustWrite(t, filepath.Join(root, "settings.json"), "{}")
	mustWrite(t, filepath.Join(root, "settings.json.tmp"), "partial")
	mustWrite(t, filepath.Join(root, "changes.json"), "[]")
	mustWrite(t, filepath.Join(root, "changes.json.tmp"), "partial")

	convDir := filepath.Join(root, "conversations")
	mustWrite(t, filepath.Join(convDir, "c1.json"), "{}")
	mustWrite(t, filepath.Join(convDir, "c1.json.tmp"), "partial")
	mustWrite(t, filepath.Join(convDir, ".index.json"), "{}")
	mustWrite(t, filepath.Join(convDir, ".index.json.tmp"), "partial")

	imgDir := filepath.Join(convDir, "img")
	mustWrite(t, filepath.Join(imgDir, "m1-0.b64"), "data:image/png;base64,AAAA")
	mustWrite(t, filepath.Join(imgDir, "m1-0.b64.tmp"), "partial")

	// پوشه‌ای که اسمش.tmp دارد — پوشه است، نباید حذف شود
	keepDir := filepath.Join(convDir, "notes.tmp")
	if err := os.MkdirAll(keepDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(keepDir, "inner.txt"), "keep me")

	// فایلی که فقط «شامل» tmp است نه پسوندش — نباید حذف شود
	mustWrite(t, filepath.Join(convDir, "notmp.txt"), "keep")

	removed := sweepStaleTempFiles(root)
	if removed != 5 {
		t.Fatalf("sweep must remove exactly 5 .tmp files, removed %d", removed)
	}

	// هیچ فایل.tmp فایلی نباید مانده باشد
	leftover := []string{
		filepath.Join(root, "settings.json.tmp"),
		filepath.Join(root, "changes.json.tmp"),
		filepath.Join(convDir, "c1.json.tmp"),
		filepath.Join(convDir, ".index.json.tmp"),
		filepath.Join(imgDir, "m1-0.b64.tmp"),
	}
	for _, p := range leftover {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("stale temp file must be gone: %s", p)
		}
	}

	// فایل‌های واقعی سالم
	survivors := []string{
		filepath.Join(root, "settings.json"),
		filepath.Join(root, "changes.json"),
		filepath.Join(convDir, "c1.json"),
		filepath.Join(convDir, ".index.json"),
		filepath.Join(imgDir, "m1-0.b64"),
		filepath.Join(convDir, "notmp.txt"),
		filepath.Join(keepDir, "inner.txt"),
	}
	for _, p := range survivors {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("real file/dir must survive the sweep: %s (%v)", p, err)
		}
	}

	// idempotent: جاروی دوم چیزی برای حذف ندارد
	if again := sweepStaleTempFiles(root); again != 0 {
		t.Fatalf("second sweep must remove nothing, removed %d", again)
	}
}

// TestSweepStaleTempFilesMissingRoot مسیر موجود‌نبودن: بدون خطا و بدون
// پنیک، صفر برمی‌گرداند (best-effort).
func TestSweepStaleTempFilesMissingRoot(t *testing.T) {
	if n := sweepStaleTempFiles(filepath.Join(t.TempDir(), "does-not-exist")); n != 0 {
		t.Fatalf("sweep of missing dir must return 0, got %d", n)
	}
}

// TestSweepStaleTempFilesEmptyDir درخت خالی: صفر، بدون خطا.
func TestSweepStaleTempFilesEmptyDir(t *testing.T) {
	if n := sweepStaleTempFiles(t.TempDir()); n != 0 {
		t.Fatalf("sweep of empty dir must return 0, got %d", n)
	}
}

// mustWrite فایل می‌سازد یا تست را می‌کشد (setup تست).
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
