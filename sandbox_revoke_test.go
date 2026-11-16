//go:build windows

// تست پس‌گرفتن ACEهای سندباکس هنگام خاموش‌کردن آن.
package main

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// aclText خروجی icacls برای یک مسیر (برای مقایسه‌ی قبل/بعد و جست‌وجوی SID).
func aclText(t *testing.T, path string) string {
	t.Helper()
	cmd := exec.Command("icacls", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flagNoWindow}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("icacls %s: %v (%s)", path, err, strings.TrimSpace(string(out)))
	}
	return string(out)
}

// acePresent آیا SID سندباکس در ACL این مسیر دیده می‌شود؟
func acePresent(t *testing.T, path, sid string) bool {
	t.Helper()
	return strings.Contains(strings.ToUpper(aclText(t, path)), strings.ToUpper(sid))
}

// grantMarkerCount تعداد نشانگرهای grant در کش فعلی را می‌شمارد.
func grantMarkerCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(sandboxCacheDir())
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "grant-") {
			n++
		}
	}
	return n
}

func TestSandboxRevokeGrantsOnDisable(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}
	// کش نشانگرها را ایزوله می‌کنیم تا نشانگرهای واقعی کاربر پس گرفته نشوند.
	t.Setenv("LocalAppData", t.TempDir())
	app := newTestApp(t)
	ws := t.TempDir()
	sid, _, err := sandboxIdentity()
	if err != nil {
		t.Skipf("sandbox identity unavailable: %v", err)
	}
	if err := sandboxGrantACE(sid, ws, "RX", true); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if !acePresent(t, ws, sid) {
		t.Fatal("precondition: the granted ACE must be present")
	}
	if grantMarkerCount(t) == 0 {
		t.Fatal("precondition: a grant marker must be recorded")
	}

	if err := app.SetSandbox(false, false); err != nil {
		t.Fatalf("SetSandbox(false): %v", err)
	}
	if acePresent(t, ws, sid) {
		t.Fatal("turning the sandbox off must remove the ACEs added for its SID")
	}
	if n := grantMarkerCount(t); n != 0 {
		t.Fatalf("grant markers must be cleared after revoking (%d left)", n)
	}
	if app.settings.SandboxMode != "off" {
		t.Fatalf("sandbox mode must be off, got %q", app.settings.SandboxMode)
	}

	// روشن‌کردن دوباره: کش پاک شده، پس مجوز باید از نو داده شود.
	if err := app.SetSandbox(true, true); err != nil {
		t.Fatalf("SetSandbox(true): %v", err)
	}
	if err := sandboxGrantACE(sid, ws, "RX", true); err != nil {
		t.Fatalf("re-grant after re-enabling: %v", err)
	}
	if !acePresent(t, ws, sid) {
		t.Fatal("re-enabling must allow a fresh grant")
	}
}

// مسیرهای بی‌ربط (بدون نشانگر) نباید دست بخورند: فقط SID سندباکس حذف می‌شود.
func TestSandboxRevokeLeavesUnrelatedACL(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}
	t.Setenv("LocalAppData", t.TempDir())
	if _, _, err := sandboxIdentity(); err != nil {
		t.Skipf("sandbox identity unavailable: %v", err)
	}
	other := t.TempDir()
	before := aclText(t, other)
	if err := sandboxRevokeGrants(); err != nil {
		t.Fatalf("revoke with no markers: %v", err)
	}
	if after := aclText(t, other); after != before {
		t.Fatalf("revoking sandbox ACEs must not change an unrelated ACL: \nbefore=%s\nafter=%s", before, after)
	}
}
