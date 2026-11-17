//go:build windows

// تست‌های dir داخلی سندباکس و دستورهای مرکب با فرزندان خارجی.
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSandboxDirBuiltin(t *testing.T) {
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

	out, code, err := runSandboxedShell(context.Background(), ws, "dir", 60*time.Second, nil, pol)
	if err != nil || code != 0 {
		t.Fatalf("dir failed: code=%d err=%v out=%q", code, err, out)
	}
	for _, want := range []string{"inside.txt", "sub", "<DIR>", "File(s)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dir output missing %q: %q", want, out)
		}
	}

	out, code, err = runSandboxedShell(context.Background(), ws, "dir /b", 60*time.Second, nil, pol)
	if err != nil || code != 0 || !strings.Contains(out, "inside.txt") || strings.Contains(out, "Volume in drive") {
		t.Fatalf("dir /b wrong: code=%d err=%v out=%q", code, err, out)
	}

	out, code, _ = runSandboxedShell(context.Background(), ws, "dir ..", 60*time.Second, nil, pol)
	if code == 0 || !strings.Contains(out, "Access is denied") {
		t.Fatalf("dir outside the workspace must be denied: code=%d out=%q", code, out)
	}

	out, code, err = runSandboxedShell(context.Background(), ws, `dir "sub"`, 60*time.Second, nil, pol)
	if err != nil || code != 0 {
		t.Fatalf("dir sub failed: code=%d err=%v out=%q", code, err, out)
	}
}

func TestSandboxLaunchTargetBuiltins(t *testing.T) {
	if _, _, direct := sandboxLaunchTarget("dir", `C:\x`); direct {
		t.Fatal("dir is a cmd builtin and must not be resolved from PATH")
	}
	if _, _, direct := sandboxLaunchTarget("git --version", `C:\x`); !direct {
		t.Fatal("git --version should use direct execution")
	}
}

func TestSandboxCompositeExternalChildren(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}
	pol, ws := sandboxTestPolicy(t)
	out, code, err := runSandboxedShell(context.Background(), ws,
		"git --version && go version", 120*time.Second, nil, pol)
	// پیش‌نیاز محیطی مستند: git/Go داخل AppContainer به \Device\Null نیاز
	// دارند؛ اگر ACE آن روی این بویت ثبت نشده باشد، git با «could not open
	// '/dev/null' … Permission denied» می‌میرد. این کمبودِ محیط است، نه باگ
	// کد — اسکریپت scripts/grant-sandbox-nul.ps1 آن را ثبت می‌کند (اپ در
	// startup هم همین کار را می‌کند). بدون این skip سویت روی ماشین‌هایی که
	// همراستاز نکرده‌اند همیشه قرمز می‌شد.
	if strings.Contains(out, "could not open '/dev/null'") && strings.Contains(out, "Permission denied") {
		t.Skip("git cannot open \\Device\\Null inside AppContainer on this boot — run scripts/grant-sandbox-nul.ps1 (environment prerequisite, not a code defect)")
	}
	if err != nil || code != 0 || !strings.Contains(out, "git version") || !strings.Contains(out, "go version") {
		t.Fatalf("composite command failed: code=%d err=%v out=%q", code, err, out)
	}
}

func TestSandboxDirInComposite(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}
	pol, ws := sandboxTestPolicy(t)
	if err := os.WriteFile(filepath.Join(ws, "inside.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code, err := runSandboxedShell(context.Background(), ws, `dir && echo done`, 60*time.Second, nil, pol)
	if err != nil || code != 0 || !strings.Contains(out, "inside.txt") || !strings.Contains(out, "done") {
		t.Fatalf("dir && echo failed: code=%d err=%v out=%q", code, err, out)
	}
	out, code, err = runSandboxedShell(context.Background(), ws, `echo start && dir && go version`, 120*time.Second, nil, pol)
	if err != nil || code != 0 || !strings.Contains(out, "start") || !strings.Contains(out, "inside.txt") || !strings.Contains(out, "go version") {
		t.Fatalf("dir+external chain failed: code=%d err=%v out=%q", code, err, out)
	}
	out, code, _ = runSandboxedShell(context.Background(), ws, `dir .. && echo leaked`, 60*time.Second, nil, pol)
	if code == 0 || strings.Contains(out, "leaked") {
		t.Fatalf("denied dir must short-circuit &&: code=%d out=%q", code, out)
	}
}
