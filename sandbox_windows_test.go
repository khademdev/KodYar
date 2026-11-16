//go:build windows

// تست‌های واقعی سندباکس ویندوز (نه mock). هر تست یک فرایند واقعی داخل
// AppContainer اجرا می‌کند و مرزهای امنیتی را با اثر روی فایل‌سیستم اثبات
// می‌کند: نوشتن در پوشه‌ی کاری مجاز، نوشتن بیرون از آن ممنوع، خواندن
// پروفایل کاربر ممنوع، و پایان مهلت کل درخت فرایند را می‌کشد.
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

func sandboxTestPolicy(t *testing.T) (*sandboxPolicy, string) {
	t.Helper()
	ws := t.TempDir()
	return &sandboxPolicy{Workspace: ws, AllowNetwork: false}, ws
}

func TestSandboxRunsCommandAndWritesWorkspace(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}
	pol, ws := sandboxTestPolicy(t)
	out, code, err := runSandboxedShell(context.Background(), ws,
		`echo hello-sandbox && echo inside>inside.txt`, 90*time.Second, nil, pol)
	if err != nil || code != 0 || !strings.Contains(out, "hello-sandbox") {
		t.Fatalf("sandboxed command failed: out=%q code=%d err=%v", out, code, err)
	}
	b, rerr := os.ReadFile(filepath.Join(ws, "inside.txt"))
	if rerr != nil || !strings.Contains(string(b), "inside") {
		t.Fatalf("sandbox must allow writing inside the workspace: %q, %v", b, rerr)
	}

	// شمارش پوشه با for — همان مسیر FindFirstFile که ابزارهای واقعی
	// استفاده می‌کنند و باید فایل ساخته‌شده را ببینند؛ خودِ dir هم حالا
	// لیست‌کننده‌ی داخلی سندباکس دارد (sandbox_windows_dir_test.go).
	out2, code2, err2 := runSandboxedShell(context.Background(), ws,
		`for %f in (*) do @echo FOUND:%f`, 90*time.Second, nil, pol)
	if err2 != nil || code2 != 0 || !strings.Contains(out2, "FOUND:inside.txt") {
		t.Fatalf("sandboxed listing failed: out=%q code=%d err=%v", out2, code2, err2)
	}
	if strings.Contains(out2, "Access is denied") {
		t.Fatalf("sandbox output leaked a denial message: %q", out2)
	}
}

func TestSandboxBlocksWriteOutsideWorkspace(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}
	pol, ws := sandboxTestPolicy(t)
	// مسیر واقعی پروفایل کاربر (پیش از بازنویسی env در سندباکس).
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	target := filepath.Join(base, fmt.Sprintf("myai-sandbox-escape-%d.txt", time.Now().UnixNano()))
	defer os.Remove(target)

	cmd := `echo escaped>"` + target + `"`
	out, _, _ := runSandboxedShell(context.Background(), ws, cmd, 90*time.Second, nil, pol)
	if _, statErr := os.Stat(target); statErr == nil {
		t.Fatalf("sandbox allowed writing outside the workspace: %s (out=%q)", target, out)
	}
}

func TestSandboxBlocksReadingUserProfile(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}
	pol, ws := sandboxTestPolicy(t)
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		t.Skip("no local appdata to test against")
	}
	secret := filepath.Join(base, fmt.Sprintf("myai-sandbox-secret-%d.txt", time.Now().UnixNano()))
	if err := os.WriteFile(secret, []byte("TOP-SECRET-MARKER"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(secret)

	out, _, _ := runSandboxedShell(context.Background(), ws,
		`type "`+secret+`"`, 90*time.Second, nil, pol)
	if strings.Contains(out, "TOP-SECRET-MARKER") {
		t.Fatalf("sandbox leaked a file from the user profile: %q", out)
	}
}

func TestSandboxTimeoutKillsProcessTree(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}
	pol, ws := sandboxTestPolicy(t)
	start := time.Now()
	_, _, err := runSandboxedShell(context.Background(), ws,
		`ping -n 30 127.0.0.1`, 1500*time.Millisecond, nil, pol)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed > 15*time.Second {
		t.Fatalf("sandbox timeout did not kill the tree in time: %s", elapsed)
	}
}

func TestSandboxEnvironmentIsolated(t *testing.T) {
	home, err := sandboxHome()
	if err != nil {
		t.Fatal(err)
	}
	block := string(utf16BlockToString(sandboxEnvironment(home, false)))
	for _, want := range []string{
		"USERPROFILE=" + home,
		"TEMP=" + filepath.Join(home, "tmp"),
		"GOCACHE=" + filepath.Join(home, "go", "cache"),
		"MYAI_SANDBOX=1",
		"MYAI_SANDBOX_NETWORK=0",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sandbox environment missing %q", want)
		}
	}
}

// utf16BlockToString برای assert کردن بلوک env (جداشده با NUL) در تست.
func utf16BlockToString(u []uint16) []byte {
	b := make([]byte, 0, len(u))
	for _, r := range u {
		if r == 0 {
			b = append(b, '|')
			continue
		}
		b = append(b, byte(r))
	}
	return b
}
