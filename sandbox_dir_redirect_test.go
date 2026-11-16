//go:build windows

// تست‌های تغییرمسیر stdout برای dir داخلی سندباکس («>» و «>>»).
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSandboxDirRedirect(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}
	pol, ws := sandboxTestPolicy(t)
	if err := os.WriteFile(filepath.Join(ws, "inside.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(ws, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}

	// dir > list.txt — فایل داخل پوشه‌ی کاری ساخته می‌شود و خروجی چاپ نمی‌شود.
	out, code, err := runSandboxedShell(context.Background(), ws, `dir /b > list.txt`, 60*time.Second, nil, pol)
	if err != nil || code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("dir > list.txt failed: code=%d err=%v out=%q", code, err, out)
	}
	data, rerr := os.ReadFile(filepath.Join(ws, "list.txt"))
	if rerr != nil {
		t.Fatalf("redirect target must be written inside the workspace: %v", rerr)
	}
	if !strings.Contains(string(data), "inside.txt") {
		t.Fatalf("redirected listing missing entries: %q", string(data))
	}

	// dir >> list.txt — افزودن به همان فایل.
	out, code, err = runSandboxedShell(context.Background(), ws, `dir /b >> list.txt`, 60*time.Second, nil, pol)
	if err != nil || code != 0 {
		t.Fatalf("dir >> list.txt failed: code=%d err=%v out=%q", code, err, out)
	}
	data, rerr = os.ReadFile(filepath.Join(ws, "list.txt"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if n := strings.Count(string(data), "inside.txt"); n != 2 {
		t.Fatalf("append mode must keep the previous listing (inside.txt x%d): %q", n, string(data))
	}

	// مقصد بیرون از پوشه‌ی کاری — رد می‌شود و فایلی بیرون ساخته نمی‌شود.
	escape := filepath.Join(filepath.Dir(ws), "escaped-sandbox-dir.txt")
	_ = os.Remove(escape)
	out, code, _ = runSandboxedShell(context.Background(), ws, `dir /b > ..\escaped-sandbox-dir.txt`, 60*time.Second, nil, pol)
	if code == 0 || !strings.Contains(out, "Access is denied") {
		t.Fatalf("redirect outside the workspace must be denied: code=%d out=%q", code, out)
	}
	if _, serr := os.Stat(escape); serr == nil {
		_ = os.Remove(escape)
		t.Fatal("redirect outside the workspace must not create a file")
	}

	// تغییرمسیر داخل زنجیره — dir > out && echo done.
	out, code, err = runSandboxedShell(context.Background(), ws, `dir /b > chain.txt && echo done`, 60*time.Second, nil, pol)
	if err != nil || code != 0 || !strings.Contains(out, "done") {
		t.Fatalf("chained dir redirect failed: code=%d err=%v out=%q", code, err, out)
	}
	if data, rerr := os.ReadFile(filepath.Join(ws, "chain.txt")); rerr != nil || !strings.Contains(string(data), "inside.txt") {
		t.Fatalf("chained redirect target missing: err=%v data=%q", rerr, string(data))
	}

	// مقصد داخل زیرپوشه‌ی پوشه‌ی کاری هم مجاز است.
	out, code, err = runSandboxedShell(context.Background(), ws, `dir /b > sub\deep.txt`, 60*time.Second, nil, pol)
	if err != nil || code != 0 {
		t.Fatalf("redirect into subdirectory failed: code=%d err=%v out=%q", code, err, out)
	}
	if data, rerr := os.ReadFile(filepath.Join(ws, "sub", "deep.txt")); rerr != nil || !strings.Contains(string(data), "inside.txt") {
		t.Fatalf("subdirectory redirect missing: err=%v data=%q", rerr, string(data))
	}
}
