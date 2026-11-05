// تست‌های Stage 4.1 — خواندن جزئی نباید «خواندن کامل» حساب شود.
//
// هر read_file موفق اثر انگشت فایل را ثبت می‌کند؛ تا پیش از این، خواندن
// ۲۰ خط از فایل ۵۰۰۰ خطی هم اجازه‌ی write_file کل فایل را می‌داد و بخش
// دیده‌نشده بی‌صدا نابود می‌شد.
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// putWorkspaceFile یک فایل داخل workspace تست می‌سازد و مسیر مطلق را
// برمی‌گرداند.
func putWorkspaceFile(t *testing.T, a *App, rel, body string) string {
	t.Helper()
	p := filepath.Join(a.settings.WorkspacePath, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return p
}

// TestPartialReadRejectsWholeFileOverwrite — خواندن بازه‌ای، سپس write_file
// کل فایل باید با پیام «partially read» رد شود و فایل دست‌نخورده بماند.
func TestPartialReadRejectsWholeFileOverwrite(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	body := strings.Repeat("line\n", 200)
	p := putWorkspaceFile(t, a, "big.txt", body)

	if _, ok := a.toolReadFile(ctx, "big.txt", 1, 5); !ok {
		t.Fatal("partial read must succeed")
	}
	msg, ok := a.toolWriteFile(ctx, "big.txt", "replaced\n")
	if ok {
		t.Fatalf("overwrite after a partial read must be rejected, got %q", msg)
	}
	if !strings.Contains(msg, "partially read") {
		t.Fatalf("error must explain the partial read, got %q", msg)
	}
	raw, err := os.ReadFile(p)
	if err != nil || string(raw) != body {
		t.Fatalf("file must stay byte-identical after rejection (err=%v)", err)
	}
}

// TestTruncatedReadRejectsOverwrite — خواندن یک خط بسیار بلند که خروجی‌اش
// بریده می‌شود هم «کامل» نیست.
func TestTruncatedReadRejectsOverwrite(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	body := strings.Repeat("x", maxReadBytes*2) + "\nsecond\n"
	putWorkspaceFile(t, a, "long.txt", body)

	out, ok := a.toolReadFile(ctx, "long.txt", 1, 1)
	if !ok || !strings.Contains(out, "[showing lines") {
		t.Fatalf("expected a truncated read, got ok=%v out=%.80q", ok, out)
	}
	msg, ok := a.toolWriteFile(ctx, "long.txt", "short\n")
	if ok || !strings.Contains(msg, "truncated") {
		t.Fatalf("truncated read must block overwrite, got ok=%v msg=%q", ok, msg)
	}
}

// TestFullReadAllowsOverwrite — خواندن کامل (بدون بازه) اجازه‌ی بازنویسی
// می‌دهد.
func TestFullReadAllowsOverwrite(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	putWorkspaceFile(t, a, "notes.txt", "one\ntwo\nthree\n")

	if _, ok := a.toolReadFile(ctx, "notes.txt", 0, 0); !ok {
		t.Fatal("full read must succeed")
	}
	msg, ok := a.toolWriteFile(ctx, "notes.txt", "new\n")
	if !ok {
		t.Fatalf("overwrite after a full read must be accepted, got %q", msg)
	}
}

// TestFullReadAfterPartialAllowsOverwrite — خواندن کامل، رکورد جزئی قبلی را
// ارتقا می‌دهد.
func TestFullReadAfterPartialAllowsOverwrite(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	putWorkspaceFile(t, a, "mixed.txt", strings.Repeat("row\n", 100))

	if _, ok := a.toolReadFile(ctx, "mixed.txt", 10, 20); !ok {
		t.Fatal("partial read must succeed")
	}
	if _, ok := a.toolReadFile(ctx, "mixed.txt", 1, 2); !ok {
		t.Fatal("second partial read must succeed")
	}
	if msg, ok := a.toolWriteFile(ctx, "mixed.txt", "done\n"); ok {
		t.Fatalf("still partial; overwrite must be rejected, got %q", msg)
	}
	if _, ok := a.toolReadFile(ctx, "mixed.txt", 0, 0); !ok {
		t.Fatal("full read must succeed")
	}
	if msg, ok := a.toolWriteFile(ctx, "mixed.txt", "done\n"); !ok {
		t.Fatalf("full read must allow overwrite, got %q", msg)
	}
}

// TestEditAfterPartialIsAllowed — edit_file با رکورد جزئی مجاز است (تطبیق
// دقیق old_string محافظ آن است).
func TestEditAfterPartialIsAllowed(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	putWorkspaceFile(t, a, "code.go", "package main\n\nfunc a() {}\n\nfunc b() {}\n")

	if _, ok := a.toolReadFile(ctx, "code.go", 1, 2); !ok {
		t.Fatal("partial read must succeed")
	}
	msg, ok := a.writeFileWithConfirm(ctx, "code.go", "package main\n\nfunc a() { _ = 1 }\n\nfunc b() {}\n", intentEdit)
	if !ok {
		t.Fatalf("targeted edit after a partial read must be allowed, got %q", msg)
	}
}

// TestEmptyFileReadIsFull — فایل خالی به‌طور کامل خوانده شده است.
func TestEmptyFileReadIsFull(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	putWorkspaceFile(t, a, "empty.txt", "")

	if _, ok := a.toolReadFile(ctx, "empty.txt", 0, 0); !ok {
		t.Fatal("empty read must succeed")
	}
	if msg, ok := a.toolWriteFile(ctx, "empty.txt", "now filled\n"); !ok {
		t.Fatalf("writing a fully-read empty file must be allowed, got %q", msg)
	}
}

// TestStaleAfterPartialReadStillRejected — خواندن جزئی هم هش کامل را ثبت
// می‌کند؛ تغییر روی دیسک باید «کهنه» تشخیص داده شود.
func TestStaleAfterPartialReadStillRejected(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	p := putWorkspaceFile(t, a, "shared.txt", "alpha\nbeta\ngamma\n")

	if _, ok := a.toolReadFile(ctx, "shared.txt", 1, 1); !ok {
		t.Fatal("partial read must succeed")
	}
	if err := os.WriteFile(p, []byte("alpha\nCHANGED\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, ok := a.toolWriteFile(ctx, "shared.txt", "overwritten\n")
	if ok || !strings.Contains(msg, "changed on disk") {
		t.Fatalf("stale file must be rejected, got ok=%v msg=%q", ok, msg)
	}
}

// TestPersianCRLFBOMFullReadThenOverwrite — محتوای فارسی با BOM و CRLF و
// ZWNJ باید بایت‌به‌بایت حفظ شود و خواندن کامل اجازه‌ی بازنویسی بدهد.
func TestPersianCRLFBOMFullReadThenOverwrite(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	orig := "\ufeffخط اول\r\nدوم‌خط\r\n"
	putWorkspaceFile(t, a, "پروژه/یادداشت.txt", orig)

	out, ok := a.toolReadFile(ctx, "پروژه/یادداشت.txt", 0, 0)
	if !ok || !strings.Contains(out, "دوم‌خط") {
		t.Fatalf("Persian content must be readable: ok=%v out=%q", ok, out)
	}
	next := "\ufeffخط اول\r\nسوم‌خط\r\n"
	if msg, ok := a.toolWriteFile(ctx, "پروژه/یادداشت.txt", next); !ok {
		t.Fatalf("overwrite after full read must pass: %v", msg)
	}
	raw, err := os.ReadFile(filepath.Join(a.settings.WorkspacePath, "پروژه", "یادداشت.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != next {
		t.Fatalf("Persian/BOM/CRLF bytes changed: %q", string(raw))
	}
}

// TestLargeFilePartialReadGuard — مسیر فایل بزرگ: خواندن ۲۰ خط از ۵۰۰۰ خط
// اجازه‌ی بازنویسی نمی‌دهد.
func TestLargeFilePartialReadGuard(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	putWorkspaceFile(t, a, "huge.txt", strings.Repeat("some line of text\n", 5000))

	if _, ok := a.toolReadFile(ctx, "huge.txt", 1, 20); !ok {
		t.Fatal("partial read must succeed")
	}
	msg, ok := a.toolWriteFile(ctx, "huge.txt", "tiny\n")
	if ok || !strings.Contains(msg, "partially read") {
		t.Fatalf("large partial read must block overwrite, got ok=%v msg=%q", ok, msg)
	}
}
