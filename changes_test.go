// تست‌های Diff و مدیریت تغییرات فایل (اولویت ۱).
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// SleepShort مکث کوتاه برای تست‌های همگام.
func SleepShort() { time.Sleep(10 * time.Millisecond) }

// ---------------------------------------------------------------- diff

func TestUnifiedDiffBasic(t *testing.T) {
	before := "line1\nline2\nline3\n"
	after := "line1\nline2-changed\nline3\n"
	diff, add, rem := unifiedDiff("a/f.txt", "b/f.txt", before, after)
	if add != 1 || rem != 1 {
		t.Fatalf("stats: add=%d rem=%d", add, rem)
	}
	if !strings.Contains(diff, "-line2\n") || !strings.Contains(diff, "+line2-changed\n") {
		t.Fatalf("diff missing change lines:\n%s", diff)
	}
	if !strings.Contains(diff, "@@ -1,3 +1,3 @@") {
		t.Fatalf("hunk header wrong:\n%s", diff)
	}
	if !strings.Contains(diff, " a/f.txt") && !strings.Contains(diff, "--- a/f.txt") {
		t.Fatal("missing from header")
	}
}

func TestUnifiedDiffCreateAndEmpty(t *testing.T) {
	// ساخت فایل جدید
	diff, add, rem := unifiedDiff("/dev/null", "b/new.txt", "", "a\nb\n")
	if add != 2 || rem != 0 {
		t.Fatalf("create stats: %d %d", add, rem)
	}
	if !strings.Contains(diff, "+a") || !strings.Contains(diff, "+b") {
		t.Fatalf("create diff:\n%s", diff)
	}
	// بدون تغییر
	diff, add, rem = unifiedDiff("a/x", "b/x", "same\n", "same\n")
	if diff != "" || add != 0 || rem != 0 {
		t.Fatalf("no-change should be empty: %q", diff)
	}
}

func TestUnifiedDiffMultipleHunks(t *testing.T) {
	// دو تغییر دور از هم → دو hunk جدا
	var b1, b2 []string
	for i := 1; i <= 30; i++ {
		b1 = append(b1, itoa(i))
	}
	b2 = append([]string{}, b1...)
	b2[2] = "changed-3"
	b2[27] = "changed-28"
	diff, _, _ := unifiedDiff("a/x", "b/x", strings.Join(b1, "\n"), strings.Join(b2, "\n"))
	n := strings.Count(diff, "@@ -") // هر هدر hunk با «@@ -» شروع می‌شود
	if n != 2 {
		t.Fatalf("expected 2 hunks, got %d:\n%s", n, diff)
	}
}

func TestDiffHunkLineNumbers(t *testing.T) {
	// صحت شماره‌های hunk: تغییر در خط ۶ از ۸؛ زمینه ۳ خط قبل (۳و۴و۵) و
	// فقط ۲ خط بعد (۷و۸) موجود است → ۶ خط در هر سمت، شروع از خط ۳.
	before := "a\nb\nc\nd\ne\nf\ng\nh\n"
	after := "a\nb\nc\nd\ne\nX\ng\nh\n"
	diff, _, _ := unifiedDiff("a/x", "b/x", before, after)
	if !strings.Contains(diff, "@@ -3,6 +3,6 @@") {
		t.Fatalf("hunk header: %s", diff)
	}
	// تغییر در ابتدای فایل → شروع از خط ۱
	diff, _, _ = unifiedDiff("a/x", "b/x", before, "A\nb\nc\nd\ne\nf\ng\nh\n")
	if !strings.Contains(diff, "@@ -1,4 +1,4 @@") {
		t.Fatalf("hunk header at top: %s", diff)
	}
}

func TestMyersLargeIdentical(t *testing.T) {
	// فایل‌های یکسان — بودجه صفر
	var lines []string
	for i := 0; i < 500; i++ {
		lines = append(lines, "line-"+itoa(i))
	}
	s := strings.Join(lines, "\n")
	diff, add, rem := unifiedDiff("a/x", "b/x", s, s)
	if diff != "" || add != 0 || rem != 0 {
		t.Fatal("identical files must produce empty diff")
	}
}

func TestMyersCompletelyDifferent(t *testing.T) {
	// دو فایل کاملاً متفاوت — باید بدون خطا جایگزینی کامل بدهد
	var a, b []string
	for i := 0; i < 3000; i++ {
		a = append(a, "aaa"+itoa(i))
		b = append(b, "bbb"+itoa(i))
	}
	_, add, rem := unifiedDiff("a/x", "b/x", strings.Join(a, "\n"), strings.Join(b, "\n"))
	if add != 3000 || rem != 3000 {
		t.Fatalf("full replace stats: add=%d rem=%d", add, rem)
	}
}

// ---------------------------------------------------------------- چرخه‌ی تغییر + revert

func TestWriteFileRecordsChangeAndRevert(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath

	// نوشتن فایل جدید (بدون تأیید چون ConfirmFileWrites=false)
	r, ok := a.execTool("write_file", `{"path":"app/main.go","content":"package main\n"}`)
	if !ok {
		t.Fatal(r)
	}
	changes := a.GetFileChanges()
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	ch := changes[0]
	if ch.Kind != ChangeCreate || ch.Path != "app/main.go" || ch.Rejected {
		t.Fatalf("bad change record: %+v", ch)
	}

	// ویرایش همان فایل
	r, ok = a.execTool("edit_file", `{"path":"app/main.go","old_string":"package main","new_string":"package main\n// edited"}`)
	if !ok {
		t.Fatal(r)
	}
	changes = a.GetFileChanges()
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(changes))
	}

	// revert ویرایش → محتوای اولیه برمی‌گردد
	if err := a.RevertFileChange(changes[0].ID); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(ws, "app", "main.go"))
	if string(data) != "package main\n" {
		t.Fatalf("revert produced wrong content: %q", string(data))
	}

	// revert دوباره → خطا
	if err := a.RevertFileChange(changes[0].ID); err == nil {
		t.Fatal("double revert must fail")
	}

	// revert ساخت فایل → فایل حذف می‌شود
	if err := a.RevertFileChange(changes[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, "app", "main.go")); !os.IsNotExist(err) {
		t.Fatal("revert-create must delete the file")
	}
}

func TestDeleteAndMoveRevert(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	os.WriteFile(filepath.Join(ws, "a.txt"), []byte("data-a"), 0o644)

	if _, ok := a.execTool("move_path", `{"from":"a.txt","to":"b.txt"}`); !ok {
		t.Fatal("move failed")
	}
	if _, err := os.Stat(filepath.Join(ws, "b.txt")); err != nil {
		t.Fatal("move did not create destination")
	}
	changes := a.GetFileChanges()
	// برگرداندن move
	if err := a.RevertFileChange(changes[0].ID); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(ws, "a.txt"))
	if string(data) != "data-a" {
		t.Fatalf("move revert failed: %q", string(data))
	}

	// حذف و revert
	if _, ok := a.execTool("delete_path", `{"path":"a.txt"}`); !ok {
		t.Fatal("delete failed")
	}
	if _, err := os.Stat(filepath.Join(ws, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("delete failed")
	}
	changes = a.GetFileChanges()
	if err := a.RevertFileChange(changes[0].ID); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(ws, "a.txt"))
	if string(data) != "data-a" {
		t.Fatal("delete revert failed")
	}
}

func TestRevertAllFileChanges(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	a.execTool("write_file", `{"path":"x1.txt","content":"one"}`)
	a.execTool("write_file", `{"path":"x2.txt","content":"two"}`)
	n, err := a.RevertAllFileChanges()
	if err != nil || n != 2 {
		t.Fatalf("revert all: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(ws, "x1.txt")); !os.IsNotExist(err) {
		t.Fatal("x1 must be deleted")
	}
}

// ---------------------------------------------------------------- چرخه‌ی تأیید تعاملی

func TestConfirmFlowReject(t *testing.T) {
	a := newTestApp(t)
	// از ۱.۸.۰ حالت ویرایش مرجع تصمیم است: این تست چرخه‌ی مودالِ کامل را
	// می‌آزماید → review_each (تغییر عمدی رفتار پیش‌فرض، docs/dev/PROGRESS.md مرحله‌ی ۲).
	a.settings.EditMode = "review_each"
	a.settings.ConfirmFileWrites = true
	ws := a.settings.WorkspacePath
	a.ctx = context.Background() // شبیه‌سازی UI فعال

	// در goroutine جدا پاسخ «رد» می‌دهیم به‌محض ثبت درخواست
	go func() {
		for i := 0; i < 100; i++ {
			a.changes.mu.Lock()
			var id string
			for k := range a.changes.confirms {
				id = k
				break
			}
			a.changes.mu.Unlock()
			if id != "" {
				_ = a.RespondFileConfirm(id, false)
				return
			}
			SleepShort()
		}
	}()

	r, ok := a.execTool("write_file", `{"path":"secret.txt","content":"x"}`)
	if ok {
		t.Fatal("write must fail when user rejects")
	}
	if !strings.Contains(r, "REJECTED") {
		t.Fatalf("message: %s", r)
	}
	if _, err := os.Stat(filepath.Join(ws, "secret.txt")); !os.IsNotExist(err) {
		t.Fatal("file must NOT be written on reject")
	}
	// رکورد ردشده ثبت شده باشد
	changes := a.GetFileChanges()
	if len(changes) != 1 || !changes[0].Rejected {
		t.Fatalf("rejected record: %+v", changes)
	}
}

func TestConfirmFlowAccept(t *testing.T) {
	a := newTestApp(t)
	// مثل بالا: مسیر مودال کامل → review_each.
	a.settings.EditMode = "review_each"
	a.settings.ConfirmFileWrites = true
	ws := a.settings.WorkspacePath
	a.ctx = context.Background()

	go func() {
		for i := 0; i < 100; i++ {
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

	r, ok := a.execTool("write_file", `{"path":"ok.txt","content":"approved"}`)
	if !ok {
		t.Fatal(r)
	}
	data, _ := os.ReadFile(filepath.Join(ws, "ok.txt"))
	if string(data) != "approved" {
		t.Fatal("file not written after accept")
	}
}

// ---------------------------------------------------------------- تنظیم تأیید

func TestSetConfirmFileWrites(t *testing.T) {
	a := newTestApp(t)
	if !a.settings.ConfirmFileWrites {
		// newTestApp پیش‌فرض را خاموش می‌کند؛ روشن می‌کنیم
		a.settings.ConfirmFileWrites = true
	}
	if err := a.SetConfirmFileWrites(false); err != nil {
		t.Fatal(err)
	}
	if a.settings.ConfirmFileWrites {
		t.Fatal("setter did not disable confirm")
	}
	// همگامی دوطرفه با EditMode (۴.۱): خاموش = auto
	if a.settings.EditMode != "auto" {
		t.Fatalf("EditMode must sync to auto, got %q", a.settings.EditMode)
	}
	// باید روی دیسک هم ذخیره شده باشد
	a2 := NewApp()
	os.Setenv("MYAI_CONFIG_DIR", a.mustConfigDir())
	_ = a2.loadSettings()
	if a2.settings.ConfirmFileWrites {
		t.Fatal("confirmFileWrites must be persisted to disk")
	}
	if a2.settings.EditMode != "auto" {
		t.Fatalf("EditMode must persist as auto, got %q", a2.settings.EditMode)
	}
	// و روشن‌کردن دوباره → review_each
	if err := a.SetConfirmFileWrites(true); err != nil {
		t.Fatal(err)
	}
	if a.settings.EditMode != "review_each" {
		t.Fatalf("EditMode must sync to review_each, got %q", a.settings.EditMode)
	}
}
