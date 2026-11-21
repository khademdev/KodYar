// ماتریس تست مسیرهای ویژه‌ی ویندوز روی resolver امن.
//
// هدف: اثبات اینکه اشکال‌های خاصِ ویندوز (پیشوندهای دستگاه، UNC، مسیر
// drive-relative، junction، نام کوتاه 8.3، ADS، نام‌های رزروشده، نقطه/فاصلهٔ
// پایانی) یا رد می‌شوند یا مسیرشان داخل workspace می‌ماند — هرگز بیرون نمی‌رود.
//
// این فایل با پسوند _windows_test.go فقط روی ویندوز کامپایل/اجرا می‌شود؛
// حالت‌های عمومی («..») در workspace_guard_test.go برای همه‌ی سکوها هستند.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// TestValidatePathNameWindowsMatrix نام‌های ممنوع/مجاز ویندوز را جدولی می‌سنجد.
func TestValidatePathNameWindowsMatrix(t *testing.T) {
	bad := []string{
		"",                                                        // خالی
		"CON", "con", "Nul.txt", "COM1", "LPT9.log", "AUX", "PRN", // دستگاه‌های رزروشده
		"name.", "name ", // نقطه/فاصلهٔ پایانی
		"a:b", "file.txt:stream", // ADS و نویسهٔ ممنوع (:) — هر دو یک علامت هستند
		"a<b", "a>b", "a|b", "a*b", "a?b", `a"b`,
		"tab\tname", "ctrl\x01name",
	}
	for _, name := range bad {
		if err := validatePathName(name); err == nil {
			t.Errorf("validatePathName(%q) accepted a name Windows cannot use safely", name)
		}
	}
	good := []string{"a.txt", "src", "a.b.c", "CONF", "COM10", "notes", "دستور.txt"}
	for _, name := range good {
		if err := validatePathName(name); err != nil {
			t.Errorf("validatePathName(%q) rejected a legitimate name: %v", name, err)
		}
	}
}

// TestResolveWorkspacePathWindowsForms ریخت‌های خطرناک مسیر را روی resolver
// واقعی می‌سنجد: یا خطا، یا نتیجه‌ای که قطعاً داخل workspace است.
func TestResolveWorkspacePathWindowsForms(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	outsideDir := t.TempDir()

	cases := []struct {
		name  string
		path  string
		notes string
	}{
		{"parent traversal", `..\..\escape.txt`, "کلاسیک"},
		{"nested traversal", `sub\..\..\escape.txt`, "عبور از زیرپوشه"},
		{"absolute outside", filepath.Join(outsideDir, "escape.txt"), "مسیر مطلق بیرونی"},
		{"UNC share", `\\localhost\C$\Windows\win.ini`, "UNC باید رد شود"},
		{"device prefix \\\\?\\", `\\?\C:\Windows\win.ini`, "پیشوند \\\\?\\"},
		{"device prefix \\\\.\\", `\\.\NUL`, "دستگاه NUL"},
		{"drive-relative", `C:escape.txt`, "مسیر وابسته به درایو"},
		{"trailing dot dir", `sub.\escape.txt`, "نقطهٔ پایانی"},
		{"reserved name", `NUL`, "نام دستگاه"},
		{"ads write", `a.txt:evil`, "جریان جایگزین داده"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := a.resolveWorkspacePath(c.path)
			if err != nil {
				return // رد شدن، امن‌ترین نتیجه
			}
			if !withinDir(p, ws) {
				t.Fatalf("%s: resolver returned %q which is outside %q", c.notes, p, ws)
			}
		})
	}
}

// TestWriteToolsRejectWindowsHazardNames ابزارهای نوشتن باید نام‌های ADS،
// دستگاه‌های رزروشده و نام‌های با نقطه/فاصلهٔ پایانی را رد کنند.
func TestWriteToolsRejectWindowsHazardNames(t *testing.T) {
	a := newTestApp(t)
	cases := []struct{ name, args string }{
		{"ads", `{"path":"a.txt:evil","content":"x"}`},
		{"reserved NUL", `{"path":"NUL","content":"x"}`},
		{"reserved CON.txt", `{"path":"CON.txt","content":"x"}`},
		{"trailing dot", `{"path":"name.","content":"x"}`},
		{"trailing dot file", `{"path":".env.","content":"x"}`},
	}
	for _, c := range cases {
		if out, ok := a.execTool("write_file", c.args); ok {
			t.Errorf("%s: write_file accepted a hazardous Windows name: %s", c.name, out)
		}
	}
}

// makeJunction یک junction واقعی می‌سازد (بدون نیاز به Administrator).
func makeJunction(t *testing.T, link, target string) bool {
	t.Helper()
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("could not create junction %q -> %q: %v (%s)", link, target, err, strings.TrimSpace(string(out)))
		return false
	}
	return true
}

// TestWriteFileTrimmedTrailingSpaceName ثبت رفتار عمدی resolver است: فاصلهٔ
// پایانیِ نامِ مسیر trim می‌شود، پس فایل با نام trim داخل workspace نوشته
// می‌شود (ویندوز چنین نامی را نمی‌پذیرد) و هیچ مسیری بیرون نمی‌رود.
func TestWriteFileTrimmedTrailingSpaceName(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	if out, ok := a.execTool("write_file", `{"path":"name ","content":"x"}`); !ok {
		t.Fatalf("expected the trailing space to be trimmed, got an error: %s", out)
	}
	if _, err := os.Stat(filepath.Join(ws, "name")); err != nil {
		t.Fatalf("expected the trimmed file 'name' inside the workspace: %v", err)
	}
}

// TestResolveWorkspacePathRejectsJunctionEscape یک junction داخل workspace که
// به بیرون اشاره می‌کند نباید راه فرار باشد (قبل و بعد از عبور از آن).
func TestResolveWorkspacePathRejectsJunctionEscape(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}

	link := filepath.Join(ws, "out")
	if !makeJunction(t, link, outside) {
		t.Skip("this machine cannot create junctions (no privilege / unsupported filesystem)")
	}

	for _, p := range []string{"out", `out\secret.txt`} {
		resolved, err := a.resolveWorkspacePath(p)
		if err == nil {
			t.Fatalf("junction escape accepted: %q resolved to %q (outside %q)", p, resolved, ws)
		}
	}

	// ابزار خواندن هم نباید محتوای بیرون را برگرداند.
	if out, ok := a.execTool("read_file", `{"path":"out\\secret.txt"}`); ok {
		t.Fatalf("read_file escaped through a junction: %s", out)
	}
}

var (
	guardKernel32          = syscall.NewLazyDLL("kernel32.dll")
	guardGetShortPathNameW = guardKernel32.NewProc("GetShortPathNameW")
)

// shortPathWindows نام کوتاه 8.3 یک مسیر را برمی‌گرداند (اگر فعال باشد).
func shortPathWindows(p string) (string, bool) {
	p16, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return "", false
	}
	buf := make([]uint16, syscall.MAX_PATH)
	n, _, _ := guardGetShortPathNameW.Call(
		uintptr(unsafe.Pointer(p16)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if n == 0 || int(n) >= len(buf) {
		return "", false
	}
	return syscall.UTF16ToString(buf[:n]), true
}

// TestResolveWorkspacePathShortNamesNeverEscape نام کوتاه 8.3 نباید ابزار فرار
// باشد. اگر 8.3 روی این حجم غیرفعال است تست skip می‌شود (پیام روشن).
func TestResolveWorkspacePathShortNamesNeverEscape(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	outside := t.TempDir()

	shortOutside, ok := shortPathWindows(outside)
	if !ok || strings.EqualFold(shortOutside, outside) {
		t.Skip("8.3 short names are disabled on this volume; nothing to test")
	}

	// نام کوتاهِ مسیر بیرونی نباید پذیرفته شود.
	if p, err := a.resolveWorkspacePath(filepath.Join(shortOutside, "escape.txt")); err == nil && !withinDir(p, ws) {
		t.Fatalf("short-name path escaped the workspace: %q", p)
	}

	// نام کوتاهِ خودِ workspace: یا رد می‌شود (fail-closed) یا داخل می‌ماند.
	if shortWs, ok := shortPathWindows(ws); ok && !strings.EqualFold(shortWs, ws) {
		p, err := a.resolveWorkspacePath(filepath.Join(shortWs, "a.txt"))
		if err == nil && !withinDir(p, ws) {
			t.Fatalf("short-name form of the workspace resolved outside: %q", p)
		}
	}
}
