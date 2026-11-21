// تست‌های ابزارهای بصری (باز کردن صفحه، کنسول، اسکرین‌شات) روی یک مرورگر
// واقعی نصب‌شده روی سیستم؛ اگر مرورگری نبود تست با پیام روشن skip می‌شود.
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDevToolsURL(t *testing.T) {
	cases := []struct {
		line string
		want string
		ok   bool
	}{
		{"DevTools listening on ws://127.0.0.1:9222/devtools/browser/abc", "ws://127.0.0.1:9222/devtools/browser/abc", true},
		{"[1102/123456.789:ERROR:foo] DevTools listening on ws://127.0.0.1:1234/devtools/browser/x", "ws://127.0.0.1:1234/devtools/browser/x", true},
		{"DevTools listening on", "", false},
		{"some other log line", "", false},
	}
	for _, c := range cases {
		got, ok := parseDevToolsURL(c.line)
		if ok != c.ok || got != c.want {
			t.Fatalf("parseDevToolsURL(%q) = %q,%v; want %q,%v", c.line, got, ok, c.want, c.ok)
		}
	}
}

// TestBrowserToolsRejectNonHTTP: فقط http(s) — قبل از هر اجرای مرورگر.
func TestBrowserToolsRejectNonHTTP(t *testing.T) {
	a := newTestApp(t)
	for _, u := range []string{"file:///C:/Windows/win.ini", "javascript:alert(1)", "ftp://x/y", ""} {
		if out, ok := a.toolBrowserOpen(u, 0, 0); ok || !strings.Contains(out, "http(s)") {
			t.Fatalf("browser_open(%q) must be rejected, got ok=%v: %s", u, ok, out)
		}
	}
	if out, ok := a.toolBrowserConsole("file:///etc/passwd", false); ok || !strings.Contains(out, "http(s)") {
		t.Fatalf("browser_console must reject file://, got ok=%v: %s", ok, out)
	}
	if out, ok := a.toolBrowserScreenshot("javascript:alert(1)", false, ""); ok || !strings.Contains(out, "http(s)") {
		t.Fatalf("browser_screenshot must reject javascript:, got ok=%v: %s", ok, out)
	}
}

// TestBrowserShotPathStaysInWorkspace: اسکرین‌شات نباید بیرون workspace بنویسد.
func TestBrowserShotPathStaysInWorkspace(t *testing.T) {
	a := newTestApp(t)
	for _, p := range []string{"../escape.png", "sub/../../escape.png", "/tmp/x.png", "C:\\Windows\\x.png", "shot.jpg"} {
		if _, err := a.browserShotPath(p); err == nil {
			t.Fatalf("path %q must be rejected", p)
		}
	}
	got, err := a.browserShotPath("screenshots/home.png")
	if err != nil {
		t.Fatalf("valid path rejected: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(got), "screenshots/home.png") {
		t.Fatalf("unexpected resolved path %q", got)
	}
}

// TestBrowserRestrictedSandboxEnv: پیام «مرورگر نیست» روشن و قابل‌عمل است.
func TestBrowserMissingMessage(t *testing.T) {
	t.Setenv("KODYAR_BROWSER_PATH", filepath.Join(t.TempDir(), "nope"))
	t.Setenv("CHROME_PATH", filepath.Join(t.TempDir(), "nope2"))
	if runtimeHasBrowser() {
		t.Skip("a real browser is installed; the missing-browser path cannot be exercised")
	}
	_, err := findBrowserExecutable()
	if err == nil || !strings.Contains(err.Error(), "KODYAR_BROWSER_PATH") {
		t.Fatalf("missing browser must name the env override, got %v", err)
	}
}

// runtimeHasBrowser آیا روی این ماشین مرورگر CDP-سازگار پیدا می‌شود؟
func runtimeHasBrowser() bool {
	_, err := findBrowserExecutable()
	return err == nil
}

// TestBrowserEndToEnd: صفحه‌ی واقعی محلی → عنوان، کنسول/خطای شبکه، PNG.
func TestBrowserEndToEnd(t *testing.T) {
	if !runtimeHasBrowser() {
		t.Skip("no Chrome/Edge/Chromium found; skipping the CDP end-to-end check")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing.png" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><title>Kodyar Preview</title></head>
<body style="margin:0;background:#0b1020;color:#fff;font:16px sans-serif">
<h1 id="t">Hello preview</h1>
<div style="width:1400px;height:900px;background:linear-gradient(90deg,#f60,#06f)">wide</div>
<img src="/missing.png" alt="">
<script>console.log('hello-ok'); console.error('boom-err');</script>
</body></html>`)
	}))
	defer srv.Close()

	a := newTestApp(t)
	t.Cleanup(a.browserShutdown)

	out, ok := a.toolBrowserOpen(srv.URL+"/", 900, 600)
	if !ok || !strings.Contains(out, "Kodyar Preview") || !strings.Contains(out, "complete") {
		t.Fatalf("browser_open failed: %s", out)
	}

	msgs, ok := a.toolBrowserConsole("", false)
	if !ok {
		t.Fatalf("browser_console failed: %s", msgs)
	}
	for _, want := range []string{"hello-ok", "boom-err", "http 404"} {
		if !strings.Contains(msgs, want) {
			t.Fatalf("console output missing %q:\n%s", want, msgs)
		}
	}

	shot, ok := a.toolBrowserScreenshot("", false, "screenshots/view.png")
	if !ok {
		t.Fatalf("browser_screenshot failed: %s", shot)
	}
	full := filepath.Join(a.settings.WorkspacePath, "screenshots", "view.png")
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("screenshot file missing: %v", err)
	}
	if len(data) < 1000 {
		t.Fatalf("screenshot suspiciously small: %d bytes", len(data))
	}
	if string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("screenshot is not a PNG (first bytes %q)", data[:8])
	}

	fullShot, ok := a.toolBrowserScreenshot("", true, "screenshots/full.png")
	if !ok {
		t.Fatalf("full-page screenshot failed: %s", fullShot)
	}
	fullData, err := os.ReadFile(filepath.Join(a.settings.WorkspacePath, "screenshots", "full.png"))
	if err != nil || len(fullData) == 0 {
		t.Fatalf("full-page screenshot missing: %v", err)
	}
}
