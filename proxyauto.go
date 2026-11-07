package main

// حالت «هوشمند» پروکسی (auto) — بازگرداندن رفتار خودکارِ قدیمی بدون تنظیم دستی:
//  1. پروکسی سیستم ویندوز (رجیستری WinINET) — همان تنظیمی که نرم‌افزارهای
//     VPN/پروکسی با گزینه‌ی «System Proxy» تغییر می‌دهند؛
//  2. متغیرهای محیطی HTTP(S)_PROXY / ALL_PROXY (با احترام به NO_PROXY)؛
//  3. اگر هیچ‌کدام نباشد، اتصال مستقیم.
//
// fail-open: اگر اتصال به پروکسیِ انتخاب‌شده خودش شکست بخورد (proxyconnect)،
// همان درخواست بدون پروکسی ادامه می‌یابد و پروکسی تا پایان فاصله‌ی خنک‌سازی
// کنار گذاشته می‌شود تا خطای proxyconnect هرگز به کاربر نرسد و نیاز به انتخاب
// دستی «خاموش» از بین برود.

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// proxyCooldown فاصله‌ی خنک‌سازی پس از شکست پروکسی؛ پس از آن دوباره امتحان
// می‌شود تا اگر پروکسی روشن شد، خودش برگردد.
const proxyCooldown = 5 * time.Minute

var (
	proxyDeadUntil int64 // unix-nano؛ صفر یعنی پروکسی سالم است
	// systemProxyFn منبع پروکسی سیستم؛ برای تست‌ها قابل تزریق است.
	systemProxyFn = systemProxyFromRegistry
	// envProxyFn منبع پروکسی محیط؛ برای تست‌ها قابل تزریق است (کشِ یک‌باره‌ی
	// stdlib وسط پروسس به تغییر env حساس نیست).
	envProxyFn = http.ProxyFromEnvironment
)

// markProxyDead پروکسی را برای فاصله‌ی خنک‌سازی کنار می‌گذارد (fail-open).
func markProxyDead() {
	atomic.StoreInt64(&proxyDeadUntil, time.Now().Add(proxyCooldown).UnixNano())
}

func proxyCoolingDown() bool {
	return atomic.LoadInt64(&proxyDeadUntil) > time.Now().UnixNano()
}

// خواندن رجیستری هر ۱۰ ثانیه یک‌بار انجام می‌شود — سبک برای سیستم‌های ضعیف.
type sysProxyState struct {
	spec, override string
	ok             bool
}

var (
	sysProxyMu    sync.Mutex
	sysProxyCache sysProxyState
	sysProxyAt    time.Time
)

func cachedSystemProxy() sysProxyState {
	sysProxyMu.Lock()
	defer sysProxyMu.Unlock()
	if time.Since(sysProxyAt) < 10*time.Second {
		return sysProxyCache
	}
	spec, override, ok := systemProxyFn()
	sysProxyCache = sysProxyState{spec: spec, override: override, ok: ok}
	sysProxyAt = time.Now()
	return sysProxyCache
}

// resetSystemProxyCache برای تست‌ها — کش بین آزمون‌ها نشت نکند.
func resetSystemProxyCache() {
	sysProxyMu.Lock()
	sysProxyCache = sysProxyState{}
	sysProxyAt = time.Time{}
	sysProxyMu.Unlock()
}

// systemProxyForRequest آدرس پروکسی سیستم را برای این درخواست برمی‌گرداند؛
// nil یعنی «مستقیم». قواعد ProxyOverride (بای‌پس) اعمال می‌شود.
func systemProxyForRequest(req *http.Request) *url.URL {
	sp := cachedSystemProxy()
	if !sp.ok || strings.TrimSpace(sp.spec) == "" {
		return nil
	}
	if hostBypassesProxy(req.URL.Hostname(), sp.override) {
		return nil
	}
	raw := windowsProxyForScheme(sp.spec, req.URL.Scheme)
	if raw == "" {
		return nil
	}
	u, err := parseProxyURL(raw)
	if err != nil {
		return nil // spec خراب = مستقیم، نه خطا
	}
	return u
}

// smartProxy حالت هوشمند: پروکسی سیستم → متغیر محیطی → مستقیم.
func smartProxy(req *http.Request) (*url.URL, error) {
	if proxyCoolingDown() {
		return nil, nil
	}
	if u := systemProxyForRequest(req); u != nil {
		return u, nil
	}
	return envProxyFn(req)
}

// windowsProxyForScheme رشته‌ی ProxyServer ویندوز را به آدرس پروکسی مناسبِ
// scheme تبدیل می‌کند. دو شکل پشتیبانی می‌شود:
//
//	"host:port"                             → همه‌ی schemeها
//	"http=h:1;https=h:2;socks=h:3;ftp=h:4"  → به‌تفکیک scheme
//
// برای هدف https اول https= و بعد http= (پروکسی HTTP با CONNECT) و سپس socks=.
func windowsProxyForScheme(spec, scheme string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	if !strings.Contains(spec, "=") {
		return "http://" + spec
	}
	byScheme := map[string]string{}
	for _, part := range strings.Split(spec, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 || strings.TrimSpace(kv[1]) == "" {
			continue
		}
		byScheme[strings.ToLower(strings.TrimSpace(kv[0]))] = strings.TrimSpace(kv[1])
	}
	order := []string{"http", "https", "socks"}
	if scheme == "https" {
		order = []string{"https", "http", "socks"}
	}
	for _, s := range order {
		if addr := byScheme[s]; addr != "" {
			if s == "socks" {
				return "socks5://" + addr
			}
			return "http://" + addr
		}
	}
	return ""
}

// hostBypassesProxy قواعد ProxyOverride ویندوز را اعمال می‌کند:
// "<local>" یعنی میزبان بدون نقطه، "127.*"/"192.168.*" یعنی wildcard،
// بقیه دقیق یا پسوندی (.example.com شامل sub.example.com هم می‌شود).
func hostBypassesProxy(host, override string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return false
	}
	for _, rule := range strings.Split(override, ";") {
		rule = strings.ToLower(strings.TrimSpace(rule))
		if rule == "" {
			continue
		}
		if rule == "<local>" {
			if !strings.Contains(host, ".") {
				return true
			}
			continue
		}
		if strings.Contains(rule, "*") {
			if wildcardMatch(host, rule) {
				return true
			}
			continue
		}
		if host == rule || strings.HasSuffix(host, "."+rule) {
			return true
		}
	}
	return false
}

// wildcardMatch تطبیق ساده‌ی wildcard فقط با «*» برای میزبان‌ها (بدون مسیر).
func wildcardMatch(s, pattern string) bool {
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	rest := s[len(parts[0]):]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(rest, p)
		if i < 0 {
			return false
		}
		rest = rest[i+len(p):]
	}
	last := parts[len(parts)-1]
	return strings.HasSuffix(rest, last)
}
