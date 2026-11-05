// تست‌های رفع اشکال‌ها و قابلیت‌های جلسه‌ی نهایی:
// بن‌بست SettingsView، بکاپ/برگرداندن پوشه‌ها، escape آرگومان shell،
// ابزارهای سفارشی، تنظیمات نمایش، لاگ خطاها و Resume ایجنت.
package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------- بن‌بست قفل‌ها

// TestSettingsViewNoDeadlockWithRevert: SettingsView (a.mu → changes.mu) باید
// با RevertFileChange هم‌زمان (changes.mu → a.mu) بدون بن‌بست تمام شود.
func TestSettingsViewNoDeadlockWithRevert(t *testing.T) {
	a := newTestApp(t)
	// یک تغییر اعمال‌شده‌ی واقعی: نوشتن فایل با تأیید خاموش
	a.settings.ConfirmFileWrites = false
	if out, ok := a.toolWriteFile(context.Background(), "deadlock.txt", "data"); !ok {
		t.Fatalf("write failed: %s", out)
	}
	list := a.loadChanges()
	if len(list) != 1 {
		t.Fatalf("expected 1 change, got %d", len(list))
	}
	id := list[0].ID

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // مسیر A: a.mu → changes.mu
		defer wg.Done()
		for i := 0; i < 40; i++ {
			v := a.SettingsView()
			if v == nil || v["pendingChanges"] == nil {
				t.Error("SettingsView returned nil map/field")
				return
			}
		}
	}()
	go func() { // مسیر B: changes.mu → a.mu
		defer wg.Done()
		for i := 0; i < 40; i++ {
			_ = a.RevertFileChange(id) // بار اول موفق، بعدی‌ها خطای «قبلاً برگشته»
		}
	}()
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		// بدون بن‌بست تمام شد
	case <-time.After(8 * time.Second):
		t.Fatal("DEADLOCK detected: SettingsView ↔ RevertFileChange")
	}
}

// ---------------------------------------------------------------- پوشه‌ها

// TestDeleteAndRevertEmptyDir: حذف پوشه‌ی خالی باید موفق شود و برگرداندن،
// همان پوشه را بازسازی کند (قبلاً backupOriginal روی پوشه خطای EISDIR می‌داد).
// از ۱.۸.۰ (۴.۱) حذفِ پوشه «سنگین» است و حتی در auto تأیید می‌خواهد —
// پس این تست یک تأییدگر خودکار می‌بندد.
func TestDeleteAndRevertEmptyDir(t *testing.T) {
	a := newTestApp(t)
	if err := os.MkdirAll(filepath.Join(a.settings.WorkspacePath, "emptydir"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.ctx = context.Background() // بدون UI، درخواست تأیید رد می‌شود
	go func() {                  // تأییدگر خودکار: اولین درخواست را «اجازه» جواب می‌دهد
		for i := 0; i < 200; i++ {
			a.changes.mu.Lock()
			var id string
			for k := range a.changes.confirms {
				id = k
				break
			}
			a.changes.mu.Unlock()
			if id != "" {
				_ = a.RespondFileConfirm(id, true)
				return
			}
			SleepShort()
		}
	}()
	out, ok := a.toolDeletePath(context.Background(), "emptydir")
	if !ok {
		t.Fatalf("delete of empty dir failed: %s", out)
	}
	if _, err := os.Stat(filepath.Join(a.settings.WorkspacePath, "emptydir")); !os.IsNotExist(err) {
		t.Fatal("dir should be gone")
	}
	list := a.loadChanges()
	if len(list) != 1 {
		t.Fatalf("expected 1 change, got %d", len(list))
	}
	if err := a.RevertFileChange(list[0].ID); err != nil {
		t.Fatalf("revert failed: %v", err)
	}
	if st, err := os.Stat(filepath.Join(a.settings.WorkspacePath, "emptydir")); err != nil || !st.IsDir() {
		t.Fatal("dir should be restored as a directory")
	}
}

// TestMoveAndRevertDirectory: جابه‌جایی پوشه باید موفق شود و برگرداندن،
// پوشه را یکپارچه (rename-back) به مسیر اول بازگرداند.
func TestMoveAndRevertDirectory(t *testing.T) {
	a := newTestApp(t)
	src := filepath.Join(a.settings.WorkspacePath, "olddir")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok := a.toolMovePath(context.Background(), "olddir", "newdir")
	if !ok {
		t.Fatalf("move of directory failed: %s", out)
	}
	if _, err := os.Stat(filepath.Join(a.settings.WorkspacePath, "newdir", "f.txt")); err != nil {
		t.Fatal("dir should be at new location")
	}
	list := a.loadChanges()
	if len(list) != 1 {
		t.Fatalf("expected 1 change, got %d", len(list))
	}
	if err := a.RevertFileChange(list[0].ID); err != nil {
		t.Fatalf("revert failed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(a.settings.WorkspacePath, "olddir", "f.txt")); err != nil || string(data) != "inside" {
		t.Fatal("directory should be renamed back with its content intact")
	}
	if _, err := os.Stat(filepath.Join(a.settings.WorkspacePath, "newdir")); !os.IsNotExist(err) {
		t.Fatal("new location must be gone after revert")
	}
}

// ---------------------------------------------------------------- امنیت shell
