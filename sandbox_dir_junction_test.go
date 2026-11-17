//go:build windows

// رگرسیون شکست CI ویندوز (رانرهای GitHub): روی رانر، مسیر پوشه‌ی کاری و
// مسیر «حل‌شده»‌اش یکی نیست (پوشه‌ی موقت با symlink/مؤلفه‌ی کوتاه به مسیر
// بلند می‌رسد). sandboxDirTarget مسیر هدف را با EvalSymlinks حل می‌کرد ولی
// مقایسه‌ی «داخل پوشه‌ی کاری» را با pwdِ خام انجام می‌داد؛ نتیجه این بود که
// `dir` داخل خودِ پوشه‌ی کاری «Access is denied.» می‌داد و `dir > list.txt`
// هم رد می‌شد. اکنون هر دو طرف با sandboxRealDir حل می‌شوند و مرز امنیتی هم
// شل نمی‌شود.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxDirSymlinkedWorkspace(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "inside.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("cannot create a directory symlink here (needs Developer Mode/admin): %v", err)
	}
	resolved, rerr := filepath.EvalSymlinks(link)
	if rerr != nil || resolved == link {
		t.Skipf("this platform does not resolve the symlinked path (resolved=%q err=%v)", resolved, rerr)
	}

	// dir داخل پوشه‌ی کاری (از مسیر symlink) باید لیست شود.
	out, code, handled, err := sandboxBuiltinDir("dir", link)
	if !handled {
		t.Fatal("dir must be handled by the built-in lister")
	}
	if err != nil || code != 0 || !strings.Contains(out, "inside.txt") {
		t.Fatalf("dir inside a symlinked workspace must list it: code=%d err=%v out=%q", code, err, out)
	}

	// تغییرمسیر stdout هم باید داخل همان پوشه بنویسد.
	out, code, handled, err = sandboxBuiltinDir(`dir /b > list.txt`, link)
	if !handled || err != nil || code != 0 {
		t.Fatalf("dir redirect inside a symlinked workspace failed: handled=%v code=%d err=%v out=%q", handled, code, err, out)
	}
	if data, derr := os.ReadFile(filepath.Join(realDir, "list.txt")); derr != nil || !strings.Contains(string(data), "inside.txt") {
		t.Fatalf("redirect target must be written inside the workspace: %v %q", derr, data)
	}

	// مرز امنیتی نباید شل شود: بیرون از پوشه‌ی کاری همچنان رد است.
	out, code, _, _ = sandboxBuiltinDir("dir ..", link)
	if code == 0 || !strings.Contains(out, "Access is denied") {
		t.Fatalf("dir outside the workspace must stay denied: code=%d out=%q", code, out)
	}
}
