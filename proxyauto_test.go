package main

// تست‌های «پروکسی هوشمند» (مأموریت ۱۰): پارسر ProxyServer ویندوز، قواعد
// بای‌پس، اولویت سیستم→محیط→مستقیم، نجات fail-open از proxyconnect و
// مهاجرت یک‌باره‌ی تنظیمات قدیمی به auto.

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// restoreProxyTestState کش رجیستری، فاصله‌ی خنک‌سازی و تزریق systemProxyFn
// را بین تست‌ها پاک می‌کند تا نشت نکنند.
func restoreProxyTestState(t *testing.T, oldFn func() (string, string, bool)) {
	t.Helper()
	systemProxyFn = oldFn
	resetSystemProxyCache()
	atomic.StoreInt64(&proxyDeadUntil, 0)
}

func TestNormalizeProxyMode(t *testing.T) {
	cases := map[string]string{
		"": "auto", "ENV": "auto", "env": "auto", "auto": "auto",
		"junk": "auto", "  ": "auto",
		"off": "off", "Off": "off", "none": "off", "direct": "off",
		"custom": "custom", "Custom": "custom",
	}
	for in, want := range cases {
		if got := normalizeProxyMode(in); got != want {
			t.Errorf("normalizeProxyMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWindowsProxyForScheme(t *testing.T) {
	cases := []struct {
		spec, scheme, want string
	}{
		{"127.0.0.1:7890", "https", "http://127.0.0.1:7890"},
		{"127.0.0.1:7890", "http", "http://127.0.0.1:7890"},
		{"http=1.2.3.4:80;https=5.6.7.8:443", "https", "http://5.6.7.8:443"},
		{"http=1.2.3.4:80;https=5.6.7.8:443", "http", "http://1.2.3.4:80"},
		// هدف https بدون https= به پروکسی http برمی‌گردد (CONNECT)
		{"http=1.2.3.4:80;ftp=9.9.9.9:21", "https", "http://1.2.3.4:80"},
		{"socks=10.0.0.1:1080", "https", "socks5://10.0.0.1:1080"},
		{"https=h.local:8443;socks=s.local:1080", "http", "http://h.local:8443"},
		{"", "https", ""},
		{"   ", "http", ""},
		{"http=;https=;socks=", "https", ""},
	}
	for _, c := range cases {
		if got := windowsProxyForScheme(c.spec, c.scheme); got != c.want {
			t.Errorf("windowsProxyForScheme(%q, %s) = %q, want %q", c.spec, c.scheme, got, c.want)
		}
	}
}

func TestHostBypassesProxy(t *testing.T) {
	cases := []struct {
		host, override string
		want           bool
	}{
		{"localhost", "<local>", true},
		{"myserver", "<local>", true},              // بدون نقطه
		{"myserver.example.com", "<local>", false}, // با نقطه
		{"192.168.5.10", "192.168.*", true},
		{"10.0.0.1", "192.168.*", false},
		{"api.example.com", "example.com", true}, // پسوندی
		{"example.com", "example.com", true},     // دقیق
		{"notexample.com", "example.com", false}, // پسوند کلاهبردار
		{"sub.corp.local", "<local>;*.corp.local", true},
		{"corp.local.evil.com", "*.corp.local", false},
		{"api.kodyar.dev", "localhost;<local>*", false},
		{"", "localhost", false},
	}
	for _, c := range cases {
		if got := hostBypassesProxy(c.host, c.override); got != c.want {
			t.Errorf("hostBypassesProxy(%q, %q) = %v, want %v", c.host, c.override, got, c.want)
		}
	}
}

// TestSmartProxyAlwaysBypassesLoopback: پروکسی سیستم روشن است و ProxyOverride
// کاربر 127.* را ندارد؛ باز هم localhost/127.*/::1 باید مستقیم بروند تا
// Provider محلی و آزمون‌های httptest قربانی پروکسی نشوند.
func TestSmartProxyAlwaysBypassesLoopback(t *testing.T) {
	old := systemProxyFn
	defer restoreProxyTestState(t, old)
	systemProxyFn = func() (string, string, bool) {
		return "127.0.0.1:8080", "<local>;10.*;192.168.*", true
	}
	resetSystemProxyCache()

	for _, raw := range []string{"http://127.0.0.1:9/v1", "http://localhost:9", "http://[::1]:9"} {
		req := httptest.NewRequest(http.MethodGet, raw, nil)
		if u, err := smartProxy(req); err != nil || u != nil {
			t.Fatalf("loopback %s must bypass proxy; got %v err=%v", raw, u, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "https://api.example.com/v1", nil)
	u, err := smartProxy(req)
	if err != nil || u == nil || u.String() != "http://127.0.0.1:8080" {
		t.Fatalf("remote host must still use the system proxy; got %v err=%v", u, err)
	}
}

func TestSmartProxySystemFirst(t *testing.T) {
	old := systemProxyFn
	oldEnv := envProxyFn
	defer func() {
		systemProxyFn = old
		envProxyFn = oldEnv
		restoreProxyTestState(t, old)
	}()
	// این محیط ممکن است HTTP_PROXY داشته باشد؛ برای سنجش بای‌پسِ سیستم،
	// منبع محیطی را خنثی می‌کنیم (مرحله‌ی ۳ خودش آن را تزریق می‌کند).
	envProxyFn = func(*http.Request) (*url.URL, error) { return nil, nil }

	// 1) رجیستری ویندوز اولویت دارد
	systemProxyFn = func() (string, string, bool) {
		return "http=1.2.3.4:80;https=5.6.7.8:443", "<local>;*.corp.local", true
	}
	resetSystemProxyCache()
	req := httptest.NewRequest(http.MethodGet, "https://api.example.com/v1", nil)
	u, err := smartProxy(req)
	if err != nil || u == nil || u.String() != "http://5.6.7.8:443" {
		t.Fatalf("registry-first failed: url=%v err=%v", u, err)
	}

	// 2) میزبانِ بای‌پس‌شده مستقیم می‌رود
	req2 := httptest.NewRequest(http.MethodGet, "http://myserver/x", nil)
	if u2, _ := smartProxy(req2); u2 != nil {
		t.Fatalf("bypass failed: got %v", u2)
	}

	// 3) رجیستری خاموش → منبع محیطی (تزریق‌شده؛ کش stdlib وسط پروسس
	// به تغییر env حساس نیست)
	systemProxyFn = func() (string, string, bool) { return "", "", false }
	envProxyFn = func(*http.Request) (*url.URL, error) {
		return &url.URL{Scheme: "http", Host: "envproxy:3128"}, nil
	}
	resetSystemProxyCache()
	req3 := httptest.NewRequest(http.MethodGet, "https://api.example.com/v1", nil)
	u3, err3 := smartProxy(req3)
	if err3 != nil || u3 == nil || u3.String() != "http://envproxy:3128" {
		t.Fatalf("env fallback failed: url=%v err=%v", u3, err3)
	}

	// 4) فاصله‌ی خنک‌سازی: پروکسی مرده → مستقیم حتی اگر رجیستری فعال باشد
	systemProxyFn = func() (string, string, bool) { return "1.2.3.4:80", "", true }
	resetSystemProxyCache()
	markProxyDead()
	req4 := httptest.NewRequest(http.MethodGet, "https://api.example.com/v1", nil)
	if u4, _ := smartProxy(req4); u4 != nil {
		t.Fatalf("cooldown failed: got %v", u4)
	}
}

// TestFailOpenDeadProxyServesDirect: پروکسیِ سیستم روی پورتِ بسته است؛
// درخواست اول باید مستقیم ادامه یابد و فاصله‌ی خنک‌سازی علامت بخورد.
func TestFailOpenDeadProxyServesDirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	old := systemProxyFn
	oldApp := appForLogs
	appForLogs = nil // fail-open فقط در حالت auto — بدون اپ هم auto است
	defer func() {
		appForLogs = oldApp
		restoreProxyTestState(t, old)
	}()
	systemProxyFn = func() (string, string, bool) { return "127.0.0.1:1", "", true } // پورت بسته
	resetSystemProxyCache()
	atomic.StoreInt64(&proxyDeadUntil, 0)

	// هدف باید غیر-loopback باشد؛ خود loopback طبق قرارداد جدید همیشه
	// مستقیم می‌رود و سناریوی «پروکسیِ مرده» را قابل سنجش نمی‌گذارد. نام
	// غیرواقعی را با DialContext به همان سرور تست می‌رسانیم.
	addr := target.Listener.Addr().String()
	dialTest := func(ctx context.Context, network, address string) (net.Conn, error) {
		if strings.Contains(address, "kodyar-test.invalid") {
			address = addr
		}
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}
	// مسیر نجات fail-open از directTransport استفاده می‌کند، نه از tr؛ آن را
	// هم موقتاً به سرور تست وصل می‌کنیم تا سناریوی غیر-loopback قابل سنجش باشد.
	oldDirect := directTransport
	directTransport = &http.Transport{DialContext: dialTest}
	defer func() { directTransport = oldDirect }()
	tr := &http.Transport{Proxy: smartProxy, DialContext: dialTest}
	client := &http.Client{Transport: failOpenTransport{base: tr}}

	resp, err := client.Get("http://kodyar-test.invalid/") // پروکسی 127.0.0.1:1 بسته است
	if err != nil {
		t.Fatalf("fail-open failed: %v", err)
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(resp.Body)
	if err != nil || string(buf) != "ok" {
		t.Fatalf("body mismatch: %q err=%v", buf, err)
	}
	if atomic.LoadInt64(&proxyDeadUntil) == 0 {
		t.Fatal("cooldown was not marked after proxyconnect failure")
	}

	// درخواست دوم با فاصله‌ی خنک‌سازی مستقیماً می‌رود (بدون تلاش مجدد)
	resp2, err := client.Get("http://kodyar-test.invalid/")
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	_ = resp2.Body.Close()
}

// TestProxyModeMigration: مهاجرت یک‌باره‌ی «خاموش» به auto؛ انتخاب صریحِ
// بعدی کاربر (off/custom) پس از مهاجرت دست‌نخورده می‌ماند.
func TestProxyModeMigration(t *testing.T) {
	cases := []struct {
		name, file, wantMode string
		wantMigrated         bool
	}{
		{"off-migrates-once", `{"proxyMode":"off"}`, "auto", true},
		{"off-stays-after-migration", `{"proxyMode":"off","proxySmartMigrated":true}`, "off", true},
		{"custom-preserved", `{"proxyMode":"custom","proxyURL":"http://127.0.0.1:7890","proxySmartMigrated":true}`, "custom", true},
		{"env-normalizes-to-auto", `{"proxyMode":"env","proxySmartMigrated":true}`, "auto", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("KODYAR_CONFIG_DIR", dir)
			resetErrorLogPath()
			if werr := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(c.file), 0o600); werr != nil {
				t.Fatal(werr)
			}
			a := NewApp()
			if lerr := a.loadSettings(); lerr != nil {
				t.Fatalf("loadSettings: %v", lerr)
			}
			// قرارداد رفتاری: همه‌ی مصرف‌کننده‌ها (resolveAppProxy/SettingsView/
			// فرانت) مقدار را از normalizeProxyMode می‌خوانند.
			if got := normalizeProxyMode(a.settings.ProxyMode); got != c.wantMode {
				t.Fatalf("normalized ProxyMode = %q, want %q (raw %q)", got, c.wantMode, a.settings.ProxyMode)
			}
			if a.settings.ProxySmartMigrated != c.wantMigrated {
				t.Fatalf("ProxySmartMigrated = %v, want %v", a.settings.ProxySmartMigrated, c.wantMigrated)
			}
			// مهاجرت باید در فایل هم ذخیره شده باشد
			raw, rerr := os.ReadFile(filepath.Join(dir, "settings.json"))
			if rerr != nil {
				t.Fatal(rerr)
			}
			if c.wantMigrated && c.file == `{"proxyMode":"off"}` {
				if string(raw) == c.file {
					t.Fatal("migration was not persisted to settings.json")
				}
			}
		})
	}
}
