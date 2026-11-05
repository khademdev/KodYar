// دسترسی web_fetch بر پایه‌ی «دامنه» (مأموریت ۳ — C.1).
//
// چرا: «همیشه اجازه بده»ِ سراسری برای web_fetch یعنی هر آدرسی — از جمله
// آدرسی با رازِ کاربر در query string — بی‌پرسش از ماشین خارج می‌شود
// (کانال نشت داده). پس اجازه‌ی ماندگار فقط برای یک دامنه‌ی مشخص
// (example.com) یا زیردامنه‌های آن (*.example.com) ذخیره می‌شود و
// override سراسری «allow» برای این ابزار وجود ندارد.
//
// قواعد:
//   - الگو = نام میزبان کوچک‌نویسی‌شده (بدون پورت، بدون مسیر) یا *.domain
//   - تطبیق دقیق است؛ *.domain هم خودِ domain و هم زیردامنه‌ها را می‌گیرد
//   - قاعده‌های نشست (حافظه، با a.mu) پیش از قاعده‌های ماندگار بررسی می‌شوند
//   - SSRF و سایر محافظت‌های webfetch.go دست‌نخورده باقی می‌مانند: این لایه
//     فقط «آیا می‌پرسیم؟» را تعیین می‌کند، نه «آیا اجازه دارد؟»
package main

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// webFetchToolName نام ابزار (برای قاعده‌های ویژه‌ی این ابزار).
const webFetchToolName = "web_fetch"

// normalizeWebHost نام میزبان را برای ذخیره/تطبیق یکدست می‌کند:
// کوچک، بدون پورت، بدون نقطه‌ی انتهایی، بدون براکت IPv6.
func normalizeWebHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.TrimSuffix(h, ".")
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
	}
	return h
}

// webHostFromURL میزبان یک URL را برمی‌گرداند (خالی = نامعتبر).
// ورودی خام پذیرفته می‌شود چون این تابع پیش از اعتبارسنجی کامل URL اجرا می‌شود.
func webHostFromURL(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return normalizeWebHost(u.Hostname())
}

// validWebHostPattern الگوی قابل‌ذخیره است؟ (host یا *.domain)
func validWebHostPattern(pattern string) bool {
	p := normalizeWebHost(pattern)
	if p == "" || len(p) > 253 {
		return false
	}
	if strings.HasPrefix(p, "*.") {
		p = strings.TrimPrefix(p, "*.")
	}
	if p == "" {
		return false
	}
	// فقط نویسه‌های مجاز نام میزبان؛ هیچ * دیگری پذیرفته نمی‌شود
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
		case r == ':': // IPv6
		default:
			return false
		}
	}
	return !strings.HasPrefix(p, ".") && !strings.Contains(p, "..")
}

// webHostMatches الگوی دامنه با میزبان تطبیق دارد؟
func webHostMatches(pattern, host string) bool {
	p := normalizeWebHost(pattern)
	h := normalizeWebHost(host)
	if p == "" || h == "" {
		return false
	}
	if strings.HasPrefix(p, "*.") {
		base := strings.TrimPrefix(p, "*.")
		return h == base || strings.HasSuffix(h, "."+base)
	}
	return h == p
}

// webHostRuleListAllowed آیا یکی از قاعده‌ها (allow) با این میزبان می‌خواند؟
func webHostRuleListAllowed(rules []PermissionRule, host string) bool {
	for _, r := range rules {
		if normalizePermissionDecision(r.Decision) != "allow" {
			continue
		}
		if webHostMatches(r.Pattern, host) {
			return true
		}
	}
	return false
}

// webHostAllowed آیا این میزبان از قبل (نشست یا ماندگار) مجاز شده است؟
func (a *App) webHostAllowed(host string) bool {
	if normalizeWebHost(host) == "" {
		return false
	}
	a.mu.Lock()
	sess := a.sessionWebHosts
	persistent := a.settings.Permissions.WebHosts
	a.mu.Unlock()
	return webHostRuleListAllowed(sess, host) || webHostRuleListAllowed(persistent, host)
}

// gateWebFetch دروازه‌ی web_fetch: قاعده‌ی دامنه، وگرنه مودال «اجازه‌ی این
// دامنه» (یک‌بار / تا پایان نشست / همیشه برای همین دامنه). تصمیم deny سراسری
// ابزار در execToolCtx پیش از این تابع گرفته می‌شود.
// خروجی دوم: پیام خطای انگلیسی برای مدل (وقتی مجاز نیست).
func (a *App) gateWebFetch(ctx context.Context, rawURL string) (bool, string) {
	host := webHostFromURL(rawURL)
	if host == "" {
		// URL نامعتبر/ناقص — به خود ابزار واگذار کن تا پیام دقیق بدهد
		return true, ""
	}
	if a.webHostAllowed(host) {
		return true, ""
	}
	ask := &permissionAsk{
		ID:      "perm-" + newID(),
		Kind:    "web_host",
		URL:     rawURL,
		Pattern: host,
		Message: "اجازه‌ی دریافت از دامنه‌ی «" + host + "» را می‌دهید؟",
		At:      timeNow(),
	}
	switch a.requestPermissionConfirm(ctx, ask) {
	case permRespondAllow:
		return true, ""
	case permRespondSession:
		a.appendSessionWebHostRule(host)
		return true, ""
	case permRespondAlways:
		if err := a.appendWebHostRule(host, "allow"); err != nil {
			// ذخیره نشد اما کاربر اجازه داد — این فراخوانی انجام می‌شود
			logAppErrorFor(a, "perm", "web_fetch host rule save failed: "+err.Error())
		}
		return true, ""
	default:
		return false, "ERROR: the user denied fetching from '" + host + "'."
	}
}

// appendSessionWebHostRule قاعده‌ی دامنه تا پایان نشست (حافظه‌ای).
func (a *App) appendSessionWebHostRule(host string) {
	host = normalizeWebHost(host)
	if host == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.sessionWebHosts {
		if a.sessionWebHosts[i].Pattern == host {
			a.sessionWebHosts[i].Decision = "allow"
			return
		}
	}
	a.sessionWebHosts = append(a.sessionWebHosts, PermissionRule{Pattern: host, Decision: "allow"})
}

// appendWebHostRule قاعده‌ی دامنه‌ی ماندگار را ذخیره می‌کند.
func (a *App) appendWebHostRule(host, decision string) error {
	host = normalizeWebHost(host)
	if !validWebHostPattern(host) {
		return errors.New("دامنه‌ی نامعتبر است")
	}
	decision = normalizePermissionDecision(decision)
	if decision == "" {
		return errors.New("تصمیم نامعتبر است")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.settings.Permissions.WebHosts {
		if a.settings.Permissions.WebHosts[i].Pattern == host {
			a.settings.Permissions.WebHosts[i].Decision = decision
			return a.saveSettings()
		}
	}
	a.settings.Permissions.WebHosts = append(a.settings.Permissions.WebHosts,
		PermissionRule{Pattern: host, Decision: decision})
	return a.saveSettings()
}

// RemoveWebHostRule قاعده‌ی ماندگار یک دامنه را حذف می‌کند تا دوباره تأیید
// بخواهد (همتای RemoveCommandRule برای web_fetch).
func (a *App) RemoveWebHostRule(host string) error {
	defer a.recoverBound("RemoveWebHostRule")
	host = normalizeWebHost(host)
	if host == "" {
		return errors.New("دامنه خالی است")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rules := a.settings.Permissions.WebHosts
	for i := range rules {
		if normalizeWebHost(rules[i].Pattern) == host {
			a.settings.Permissions.WebHosts = append(rules[:i:i], rules[i+1:]...)
			return a.saveSettings()
		}
	}
	return errors.New("قاعده‌ای برای این دامنه پیدا نشد")
}
