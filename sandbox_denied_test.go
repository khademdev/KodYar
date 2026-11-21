// تست راهنمای «سندباکس مانع شد» در پیام شکست دستور.
//
// گزارش واقعی: داخل AppContainer، Node با
// `EPERM: operation not permitted, lstat 'D:\'` می‌افتاد و cmd هم
// «Access is denied.» می‌داد؛ ولی پیام نتیجه فقط خروجی خام بود و مدل همان
// دستور را دوباره تکرار می‌کرد.
package main

import (
	"errors"
	"strings"
	"testing"
)

func TestSandboxDeniedHint(t *testing.T) {
	eperm := "node:fs:2806\nError: EPERM: operation not permitted, lstat 'D:\\'\n    at Object.realpathSync"
	h := sandboxDeniedHint(true, eperm)
	if !strings.Contains(h, "SANDBOX") || !strings.Contains(h, "Do not retry") {
		t.Fatalf("drive-root EPERM must name the sandbox and forbid a retry, got %q", h)
	}
	if !strings.Contains(h, "Settings") {
		t.Fatalf("hint must name the short way out, got %q", h)
	}
	if h := sandboxDeniedHint(true, "Access is denied.\n[command failed: exit status 1]"); !strings.Contains(h, "SANDBOX") {
		t.Fatalf("«Access is denied.» inside the sandbox must name the sandbox, got %q", h)
	}
	if h := sandboxDeniedHint(false, eperm); h != "" {
		t.Fatalf("no sandbox hint when the command did not run sandboxed, got %q", h)
	}
	if h := sandboxDeniedHint(true, "go: build failed\nexit status 1"); h != "" {
		t.Fatalf("ordinary failures must keep their own message, got %q", h)
	}
	if h := sandboxDeniedHint(true, "Error: EPERM: operation not permitted, unlink 'C:\\ws\\a.txt'"); h != "" {
		t.Fatalf("EPERM outside the drive root must not claim the sandbox, got %q", h)
	}
}

func TestCommandFailureResultSandboxHint(t *testing.T) {
	msg := commandFailureResult("Error: EPERM: operation not permitted, lstat 'D:\\'", 60, false, errors.New("exit status 1"), true)
	if !strings.Contains(msg, "SANDBOX") {
		t.Fatalf("sandboxed failure result must carry the hint, got %q", msg)
	}
	plain := commandFailureResult("go: build failed", 60, false, errors.New("exit status 1"), false)
	if strings.Contains(plain, "SANDBOX") {
		t.Fatalf("unsandboxed failure must not mention the sandbox, got %q", plain)
	}
}
