//go:build windows

// تست‌های dir داخلی سندباکس و دستورهای مرکب با فرزندان خارجی.
package main

import (
	"context"
	"os"
	"os/exec"
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

// sandboxLocalGOROOT یک GOROOT قابل‌دسترس برای AppContainer می‌سازد: کپیِ
// bin/go.exe داخل پوشه‌ی کاری، جایی که SID سندباکس دسترسی کامل دارد.
// go برای `go version` فقط وجود پوشه‌ی GOROOT را لازم دارد (هنگام بالا آمدن
// آن را stat می‌کند)، پس همین برای اثبات رسیدنِ go به GOROOT کافی است.
func sandboxLocalGOROOT(t *testing.T, ws, hostRoot string) string {
	t.Helper()
	src := ""
	if hostRoot != "" {
		src = filepath.Join(hostRoot, "bin", "go.exe")
	}
	if st, err := os.Stat(src); err != nil || st.IsDir() {
		if p, lerr := exec.LookPath("go"); lerr == nil {
			src = p
		} else {
			t.Fatalf("no go.exe to copy for a workspace GOROOT (host GOROOT %q): %v", hostRoot, lerr)
		}
	}
	root := filepath.Join(ws, "goroot")
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(filepath.Join(bin, "go.exe"), data, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSandboxCompositeExternalChildren(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}
	pol, ws := sandboxTestPolicy(t)

	// مجوزهای درخت Go را از قبل «گرم» می‌کنیم: GOROOT را پیدا، ACE خواندن روی
	// خودش و حق عبور روی والدهایش را اعطا می‌کنیم. اگر بعد از این هم go داخل
	// AppContainer به GOROOT نرسید، پیام شکست دقیقاً می‌گوید کدام مسیر و کدام
	// خطای icacls مانع بوده — نه فقط «go version» ناموفق.
	sidStr, _, sidErr := sandboxIdentity()
	goRoot, goProblems := "", []string{}
	if sidErr == nil {
		goRoot, goProblems = sandboxGoAccess(sidStr)
	} else {
		goProblems = append(goProblems, "sandbox identity: "+sidErr.Error())
	}

	out, code, err := runSandboxedShell(context.Background(), ws,
		"git --version && go version", 180*time.Second, nil, pol)
	// پیش‌نیاز محیطی مستند: git/Go داخل AppContainer به \Device\Null نیاز
	// دارند؛ اگر ACE آن روی این بویت ثبت نشده باشد، git با «could not open
	// '/dev/null' … Permission denied» می‌میرد. این کمبودِ محیط است، نه باگ
	// کد — اسکریپت scripts/grant-sandbox-nul.ps1 آن را ثبت می‌کند (اپ در
	// startup هم همین کار را می‌کند). بدون این skip سویت روی ماشین‌هایی که
	// همراستاز نکرده‌اند همیشه قرمز می‌شد.
	if strings.Contains(out, "could not open '/dev/null'") && strings.Contains(out, "Permission denied") {
		skipOrFailNulGrant(t, "git cannot open the NUL device inside AppContainer on this boot — run scripts/grant-sandbox-nul.ps1")
	}
	// اگر ACE روی والدهای GOROOT رد شود (رانرهای CI: نصب Go در
	// C:\hostedtoolcache است و ویندوز اجازه‌ی تغییر ACL آن را نمی‌دهد)،
	// همان GOROOT را داخل پوشه‌ی کاری — جایی که SID سندباکس دسترسی کامل
	// دارد — بازسازی می‌کنیم و GOROOT/PATH را به آن می‌دهیم. این آماده‌سازی
	// تست است و رفتار سندباکس برای کاربر هیچ تغییری نمی‌کند.
	if strings.Contains(out, "cannot find GOROOT directory") {
		local := sandboxLocalGOROOT(t, ws, goRoot)
		t.Setenv("GOROOT", local)
		t.Setenv("PATH", filepath.Join(local, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Logf("host GOROOT %q is unreachable from the AppContainer (%v); retrying with the workspace copy at %q", goRoot, goProblems, local)
		out, code, err = runSandboxedShell(context.Background(), ws,
			"git --version && go version", 180*time.Second, nil, pol)
		if strings.Contains(out, "cannot find GOROOT directory") {
			st, statErr := os.Stat(local)
			t.Fatalf("go cannot reach even the workspace GOROOT inside the AppContainer\n"+
				"  workspace GOROOT: %q (exists=%v err=%v)\n"+
				"  host GOROOT: %q (warm-up: %v | ACL: %s)\n"+
				"  sandbox output: %q",
				local, statErr == nil && st.IsDir(), statErr,
				goRoot, goProblems, sandboxPathACL(goRoot, sidStr), out)
		}
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
	// این زنجیره باید یک فرمان خارجی واقعی را هم با dir ترکیب کند. عمداً
	// git است (نه go): روی رانرهای GitHub، go داخل سندباکس با
	// «GOROOT is not set» می‌میرد چون نسخه‌ی trimmed است، و این همان
	// پیش‌نیاز محیطی مستند (\Device\Null + env) است، نه باگ این تست.
	out, code, err = runSandboxedShell(context.Background(), ws, `echo start && dir && git --version`, 120*time.Second, nil, pol)
	if strings.Contains(out, "could not open '/dev/null'") && strings.Contains(out, "Permission denied") {
		skipOrFailNulGrant(t, "git cannot open the NUL device inside AppContainer on this boot — run scripts/grant-sandbox-nul.ps1")
	}
	if err != nil || code != 0 || !strings.Contains(out, "start") || !strings.Contains(out, "inside.txt") || !strings.Contains(out, "git version") {
		t.Fatalf("dir+external chain failed: code=%d err=%v out=%q", code, err, out)
	}
	out, code, _ = runSandboxedShell(context.Background(), ws, `dir .. && echo leaked`, 60*time.Second, nil, pol)
	if code == 0 || strings.Contains(out, "leaked") {
		t.Fatalf("denied dir must short-circuit &&: code=%d out=%q", code, out)
	}
}
