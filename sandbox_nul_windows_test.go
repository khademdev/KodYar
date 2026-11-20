//go:build windows

// تست‌های مجوز NUL سندباکس: helper مرتفع، فایل وضعیت، راهنمای خطا و پروب
// واقعی داخل AppContainer (sandbox_nul_windows.go).
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSandboxNulEnableScriptIsComplete — اسکریپت مرتفع باید دقیقاً همان
// زنجیره‌ای را بسازد که طراحی کرده‌ایم: ACE + Task سیستمی ONSTART + پاک‌سازی
// مسیرهای قدیمی + ثبت وضعیت. اگر یکی بیفتد، کاربر بعد از بوت دوباره بی‌مجوز
// می‌ماند و هیچ خطایی هم نمی‌بیند.
func TestSandboxNulEnableScriptIsComplete(t *testing.T) {
	if len(sandboxNulEnableScript) < 400 {
		t.Fatalf("embedded enable script looks empty (%d bytes)", len(sandboxNulEnableScript))
	}
	for _, want := range []string{
		"/Create", "/SC ONSTART", "/RU SYSTEM", "/RL HIGHEST", "/TR", "/F",
		"/Delete", "KodYar-sandbox-nul.vbs", "KodYar-sandbox-nul.cmd",
		"nul-grant.json", "ConvertTo-Json", "-WindowStyle Hidden", "Test-Path",
	} {
		if !strings.Contains(sandboxNulEnableScript, want) {
			t.Fatalf("enable script is missing %q", want)
		}
	}
	if !strings.Contains(sandboxNulEnableScript, "ps1") {
		t.Fatalf("enable script must reference the grant script")
	}
}

// فایل وضعیتی که helper می‌نویسد باید خواندنی و صادق باشد (خطا هم ثبت شود).
func TestSandboxNulGrantStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	okPath := filepath.Join(dir, "ok.json")
	if err := os.WriteFile(okPath, []byte(`{"ok":true,"at":"2026-11-18T10:00:00Z","task":"KodYar Sandbox NUL (System)"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, found := readNulGrantState(okPath)
	if !found || !st.OK || !strings.Contains(st.Task, "System") {
		t.Fatalf("state file must round-trip: %+v found=%v", st, found)
	}

	errPath := filepath.Join(dir, "err.json")
	if err := os.WriteFile(errPath, []byte(`{"ok":false,"error":"Access is denied."}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, found = readNulGrantState(errPath)
	if !found || st.OK || !strings.Contains(st.Error, "denied") {
		t.Fatalf("failed attempt must keep its error: %+v", st)
	}

	if _, found := readNulGrantState(filepath.Join(dir, "missing.json")); found {
		t.Fatal("a missing state file must not be reported as found")
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found := readNulGrantState(filepath.Join(dir, "bad.json")); found {
		t.Fatal("a corrupt state file must not be reported as found")
	}
}

// راهنما فقط وقتی می‌آید که خطا مربوط به NUL باشد و مجوز هنوز اعمال نشده؛
// وگرنه هر خطای «Access is denied» یک پیام گمراه‌کننده می‌گرفت.
func TestSandboxNulDeniedHintOnlyWhenUseful(t *testing.T) {
	nulProbeMu.Lock()
	oldDone, oldOK, oldDetail := nulProbeDone, nulProbeOK, nulProbeDetail
	nulProbeDone, nulProbeOK, nulProbeDetail = true, false, "test"
	nulProbeMu.Unlock()
	defer func() {
		nulProbeMu.Lock()
		nulProbeDone, nulProbeOK, nulProbeDetail = oldDone, oldOK, oldDetail
		nulProbeMu.Unlock()
	}()

	hint := sandboxNulDeniedHint("go: open NUL: Access is denied.")
	if !strings.Contains(hint, "فعال‌سازی دسترسی NUL") {
		t.Fatalf("a denied NUL must point at the one-time grant, got %q", hint)
	}
	if hint := sandboxNulDeniedHint("Access is denied."); hint != "" {
		t.Fatalf("unrelated denials must not get the NUL hint: %q", hint)
	}
	if hint := sandboxNulDeniedHint("everything is fine"); hint != "" {
		t.Fatalf("successful output must not get a hint: %q", hint)
	}

	nulProbeMu.Lock()
	nulProbeOK = true
	nulProbeMu.Unlock()
	if hint := sandboxNulDeniedHint("go: open NUL: Access is denied."); hint != "" {
		t.Fatalf("when the grant is in effect the hint must disappear: %q", hint)
	}
}

// پروب باید واقعاً داخل AppContainer اجرا شود و نتیجهٔ قطعی بدهد؛ اگر مجوز
// فعال است، KODYAR_EXPECT_NUL_GRANTED=1 این را هم تأیید می‌کند (برای سنجش
// دستی روی ماشینی که یک‌بار فعال‌سازی انجام شده).
func TestSandboxNulProbeReportsRealState(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}
	invalidateSandboxNulProbe()
	granted, detail := probeSandboxNul()
	if strings.TrimSpace(detail) == "" {
		t.Fatal("probe must explain its verdict")
	}
	again, _ := probeSandboxNul()
	if again != granted {
		t.Fatalf("probe must be deterministic within a boot: %v then %v", granted, again)
	}
	if os.Getenv("KODYAR_EXPECT_NUL_GRANTED") == "1" && !granted {
		t.Fatalf("expected the NUL grant to be in effect, probe said: %s", detail)
	}
	invalidateSandboxNulProbe()
}

// TestSandboxGoWorksAfterNulGrant سنجش واقعی end-to-end: بعد از فعال‌سازی
// مجوز NUL، `go version` باید داخل AppContainer کار کند (پیش از آن با
// «open NUL: Access is denied» می‌میرد). پیش‌فرض Skip است چون ماشین بدون
// مجوز شکست می‌خورد:
//
//	KODYAR_EXPECT_NUL_GRANTED=1 go test -run TestSandboxGoWorksAfterNulGrant -v .
func TestSandboxGoWorksAfterNulGrant(t *testing.T) {
	if os.Getenv("KODYAR_EXPECT_NUL_GRANTED") != "1" {
		t.Skip("set KODYAR_EXPECT_NUL_GRANTED=1 after enabling the NUL grant")
	}
	if !sandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}
	pol, ws := sandboxTestPolicy(t)
	out, code, err := runSandboxedShell(context.Background(), ws, "go version", 180*time.Second, nil, pol)
	if err != nil || code != 0 || !strings.Contains(out, "go version") {
		t.Fatalf("go must run inside the sandbox once NUL is granted: code=%d err=%v out=%q", code, err, out)
	}
}

// متد بایندشده باید همیشه شکل ثابت داشته باشد (UI روی کلیدها حساب می‌کند).
func TestSandboxNulStatusShape(t *testing.T) {
	a := newTestApp(t)
	st := a.SandboxNulStatus()
	for _, k := range []string{"available", "granted", "detail", "task"} {
		if _, ok := st[k]; !ok {
			t.Fatalf("SandboxNulStatus is missing %q: %+v", k, st)
		}
	}
	if st["available"] != true {
		t.Fatalf("windows must report the sandbox as available: %+v", st)
	}
	if got, _ := st["granted"].(bool); got {
		// اگر روی این ماشین مجوز برقرار است، حداقل باید detail داشته باشد.
		if strings.TrimSpace(st["detail"].(string)) == "" {
			t.Fatalf("a granted probe must explain itself: %+v", st)
		}
	}
}
