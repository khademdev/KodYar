// ابزار web_fetch — خواندن مستندات/صفحات خطا از وب با محافظت SSRF.
//
// مدل می‌تواند برای خواندن مستندات یا پیام‌های خطای آنلاین URL بگیرد؛
// همه‌ی محدودیت‌های امنیتی در «لحظه‌ی اتصال» اعمال می‌شوند، نه فقط روی
// رشته‌ی URL:
//
// - گارد اتصال (Control روی net.Dialer) IPِ «واقعاً resolve‌شده» را
// بازرسی می‌کند و محدوده‌های ممنوع (loopback، private، link-local
// شامل 169.254.169.254 ابر، CGNAT، 0.0.0.0/8، multicast، broadcast،
// رزرو 240/4، benchmarking، مستندسازی، ULA و …) را رد می‌کند — این
// مسیر DNS-rebinding را می‌بندد و روی «هر پرش redirect» هم اجرا
// می‌شود (هر مقصد جدید = اتصال جدید = گارد جدید).
// - پروکسی‌های محیط (HTTP_PROXY و…) عمداً نادیده گرفته می‌شوند: پروکسی
// خودش resolve/connect می‌کند و چک IP را دور می‌زند.
// - فقط http/https؛ URL با userinfo (user:pass@) یا بدون host رد می‌شود؛
// redirect به scheme غیر http(s) دنبال نمی‌شود (حداکثر ۵ پرش).
//
// محدودیت‌ها: مهلت کل ۲۰ ثانیه؛ حداکثر ۲MiB از بدنه؛ خروجی حداکثر
// ۱۰۰٫۰۰۰ کاراکتر (پیش‌فرض کمتر با max_chars؛ برش rune-safe)؛ فقط
// content-type متنی (text/*، JSON، XML، XHTML و +json/+xml).
//
// charset: از Content-Type، BOM و <meta> تشخیص و به UTF-8 تبدیل می‌شود
// (golang.org/x/net/html/charset) — صفحات windows-1256 و ISO-8859-6 هم
// درست decode می‌شوند.
//
// خروجی نامطمئن: نتیجه در پوشش <fetched_content url="…" untrusted="true">
// می‌آید و هر رخدادِ تگِ بسته‌شونده داخل متن escape می‌شود تا نتواند از
// پوشش بیرون بزند. پرامپت سیستمی هم به مدل می‌گوید محتوای fetch شده
// «داده» است، نه دستور.
//
// دسترسی: پیش‌فرض «بپرس» — مودال اجازه، URL را با ایزوله‌سازی bidi نشان
// می‌دهد (فیلد url روی permissionAsk + permissions.js). نه در فهرست
// موازی و نه در allowlist زیرایجنت.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// محدودیت‌های web_fetch.
const (
	webFetchTimeout      = 20 * time.Second // مهلت کل (اتصال + بدنه)
	webFetchMaxBody      = 2 * 1024 * 1024  // حداکثر بایت خوانده‌شده از بدنه
	webFetchMaxChars     = 100000           // سقف مطلق خروجی (کاراکتر)
	webFetchDefaultChars = 20000            // پیش‌فرض وقتی max_chars داده نشده
	webFetchMinChars     = 500              // کف clamp
	webFetchMaxRedirects = 5                // حداکثر پرش‌های redirect
)

// webFetchGuard گارد «لحظه‌ی اتصال» — تزریق‌پذیر برای تست‌ها (تست‌ها با
// گارد مجاز، سرورهای httptest حلقه‌ی محلی را می‌بینند؛ تست جدا ثابت
// می‌کند گارد واقعی همان‌ها را می‌بندد). باید فقط پیش از شروع درخواست‌های
// هم‌زمان عوض شود (بدون قفل خوانده می‌شود). signature: (network, address).
var webFetchGuard = ssrfConnectGuard

// محدوده‌های IPv4 ممنوع (به‌جز مواردی که predicate استاندارد netip دارند).
var (
	ssrfPrefixes = []struct {
		prefix netip.Prefix
		reason string
	}{
		{netip.MustParsePrefix("0.0.0.0/8"), "\"this network\" range"},
		{netip.MustParsePrefix("100.64.0.0/10"), "carrier-grade NAT (CGNAT) range"},
		{netip.MustParsePrefix("192.0.2.0/24"), "documentation range (TEST-NET-1)"},
		{netip.MustParsePrefix("198.51.100.0/24"), "documentation range (TEST-NET-2)"},
		{netip.MustParsePrefix("203.0.113.0/24"), "documentation range (TEST-NET-3)"},
		{netip.MustParsePrefix("198.18.0.0/15"), "benchmarking range"},
		{netip.MustParsePrefix("192.0.0.0/24"), "IETF protocol assignments range"},
		{netip.MustParsePrefix("192.88.99.0/24"), "6to4 relay anycast range"},
		{netip.MustParsePrefix("240.0.0.0/4"), "reserved range"},
	}
	ssrfNAT64Prefix = netip.MustParsePrefix("64:ff9b::/96")
	ssrfBroadcast   = netip.MustParseAddr("255.255.255.255")

	// C.3: فرم‌های انتقال IPv6 → IPv4 — هر کدام می‌تواند یک نشانی داخلی را
	// از فیلتر IPv4 رد کند؛ همه ممنوع‌اند (برای دریافت مستندات لازم نیستند).
	ssrfIPv6TransitionPrefixes = []struct {
		prefix netip.Prefix
		reason string
	}{
		{netip.MustParsePrefix("2001::/32"), "Teredo transition address"},
		{netip.MustParsePrefix("64:ff9b:1::/48"), "local-use NAT64 range"},
		{netip.MustParsePrefix("::/96"), "IPv4-compatible IPv6 address"},
		{netip.MustParsePrefix("fec0::/10"), "deprecated site-local range"},
	}
	// 6to4 (2002::/16) IPv4 را در بایت‌های ۲..۵ حمل می‌کند؛ همان IPv4
	// نهفته بررسی می‌شود تا به‌جای رد کردن همه‌ی مسیر‌های عمومی، فقط
	// مقصدهای داخلی بسته شوند.
	ssrf6to4Prefix = netip.MustParsePrefix("2002::/16")
)

// ssrfConnectGuard گارد واقعی: آدرسِ resolve‌شده (ip:port) که لایه‌ی
// transport در Control می‌دهد را بازرسی می‌کند.
func ssrfConnectGuard(network, address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address // بدون پورت — باز هم بررسی IP
	}
	ip, perr := netip.ParseAddr(host)
	if perr != nil {
		return fmt.Errorf("web_fetch: cannot parse the resolved address %q", address)
	}
	if reason := blockedIPReason(ip); reason != "" {
		return fmt.Errorf("web_fetch: connection to %s is not allowed (%s)", ip, reason)
	}
	return nil
}

// unmapNAT64 فرم NAT64 (64:ff9b::/96) را به IPv4 نهفته‌اش باز می‌کند تا
// همان بررسی‌های IPv4 رویش اعمال شود (IPv4-mapped با Unmap باز می‌شود).
func unmapNAT64(ip netip.Addr) netip.Addr {
	if ssrfNAT64Prefix.Contains(ip) {
		b := ip.As16()
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	}
	return ip
}

// blockedIPReason دلیل ممنوع‌بودن IP را برمی‌گرداند ("" = مجاز). همه‌ی
// فرم‌های IPv6ِ نگاشت‌شده به IPv4 اول باز می‌شوند تا دورزدن با
// ::ffff:10.0.0.1 یا NAT64 ناممکن باشد.
func blockedIPReason(ip netip.Addr) string {
	ip = ip.Unmap()
	if ip.Is4In6() { // نتیجه‌ی بازکردن NAT64 می‌تواند 4in6 باشد
		ip = ip.Unmap()
	}
	ip = unmapNAT64(ip)
	if ip.Is4In6() {
		ip = ip.Unmap()
	}
	switch {
	case ip.IsLoopback(): // 127.0.0.0/8 و::1
		return "loopback address"
	case ip.IsPrivate(): // 10/8، 172.16/12، 192.168/16 و ULA fc00::/7
		return "private address"
	case ip.IsLinkLocalUnicast(): // 169.254/16 (شامل 169.254.169.254 ابر) و fe80::/10
		return "link-local address"
	case ip.IsLinkLocalMulticast(): // 224.0.0.0/24 و ff02::/16
		return "link-local multicast address"
	case ip.IsMulticast():
		return "multicast address"
	case ip.IsUnspecified(): // 0.0.0.0 و::
		return "unspecified address"
	case ip == ssrfBroadcast:
		return "broadcast address"
	}
	if ip.Is4() {
		for _, p := range ssrfPrefixes {
			if p.prefix.Contains(ip) {
				return p.reason
			}
		}
	}
	if ip.Is6() {
		// C.3 — 6to4: IPv4 نهفته را مثل خودش بررسی کن
		if ssrf6to4Prefix.Contains(ip) {
			b := ip.As16()
			if r := blockedIPReason(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]})); r != "" {
				return "6to4 address embedding a " + r
			}
		}
		for _, p := range ssrfIPv6TransitionPrefixes {
			if p.prefix.Contains(ip) {
				return p.reason
			}
		}
	}
	return ""
}

// validateWebURL قواعد رشته‌ی URL (scheme/userinfo/host) — روی URL اولیه و
// همه‌ی مقصدهای redirect اجرا می‌شود.
func validateWebURL(u *url.URL) error {
	if u == nil {
		return errors.New("web_fetch: URL is empty")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("web_fetch: only http and https URLs are supported (got %q)", u.Scheme)
	}
	if u.User != nil {
		return errors.New("web_fetch: URLs with embedded credentials (user:pass@host) are not allowed")
	}
	if strings.TrimSpace(u.Host) == "" {
		return errors.New("web_fetch: URL has no host")
	}
	return nil
}

// webFetchDialer دیالر با گارد اتصال — کنترل روی IPِ resolve‌شده انجام
// می‌شود (شکست DNS-rebinding).
var webFetchDialer = &net.Dialer{
	Timeout:   10 * time.Second,
	KeepAlive: 30 * time.Second,
	Control: func(network, address string, c syscall.RawConn) error {
		return webFetchGuard(network, address)
	},
}

// webFetchClient کلاینت اختصاصی web_fetch:
// - Proxy = nil: پروکسی محیط resolve/connect را به‌جای ما انجام می‌داد و
// چک IPِ مقصد را دور می‌زد (SSRF) — عمداً نادیده گرفته می‌شود.
// - CheckRedirect: سقف ۵ پرش + اعتبارسنجی دوباره‌ی scheme/userinfo/host
// روی هر مقصد (پروتکل غیر http(s) هرگز دنبال نمی‌شود). گارد IP برای
// هر مقصد جدید به‌صورت خودکار در اتصال جدید اجرا می‌شود.
var webFetchClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 nil,
		DialContext:           webFetchDialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		ExpectContinueTimeout: time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= webFetchMaxRedirects {
			return fmt.Errorf("web_fetch: too many redirects (more than %d)", webFetchMaxRedirects)
		}
		return validateWebURL(req.URL)
	},
}

// clampWebFetchMaxChars سقف/کف/پیش‌فرض max_chars: بدون مقدار → پیش‌فرض،
// زیر کف → کف، بالای سقف مطلق → سقف.
func clampWebFetchMaxChars(n int) int {
	if n <= 0 {
		return webFetchDefaultChars
	}
	if n < webFetchMinChars {
		return webFetchMinChars
	}
	if n > webFetchMaxChars {
		return webFetchMaxChars
	}
	return n
}

// webFetchAllowedContentType فقط content-typeهای متنی پذیرفته می‌شوند.
// نبودِ هدر (سرورهای کوچک) بخشیده می‌شود و «متن» فرض می‌شود.
func webFetchAllowedContentType(ct string) bool {
	mt := strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	if mt == "" {
		return true
	}
	if strings.HasPrefix(mt, "text/") {
		return true
	}
	switch mt {
	case "application/json", "application/xml", "application/xhtml+xml":
		return true
	}
	if strings.HasSuffix(mt, "+json") || strings.HasSuffix(mt, "+xml") {
		return true
	}
	return false
}

func webFetchIsHTML(ct string) bool {
	mt := strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	return mt == "text/html" || mt == "application/xhtml+xml"
}

// countingReader شمارنده‌ی بایت خام خوانده‌شده (برای سقف بدنه).
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// toolWebFetch اجرای ابزار web_fetch.
func (a *App) toolWebFetch(ctx context.Context, rawURL string, maxChars int) (string, bool) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "ERROR: url is empty.", false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "ERROR: invalid URL: " + err.Error(), false
	}
	if err := validateWebURL(u); err != nil {
		return "ERROR: " + err.Error(), false
	}
	maxChars = clampWebFetchMaxChars(maxChars)

	reqCtx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	req.Header.Set("User-Agent", "MyAI/"+version+" (Wails desktop coding agent)")
	req.Header.Set("Accept", "text/html;q=0.9, application/xhtml+xml;q=0.9, text/*;q=0.8, application/json;q=0.8, application/xml;q=0.8")

	resp, err := webFetchClient.Do(req)
	if err != nil {
		// لغو کاربر (Stop) و مهلت را از هم تفکیک کن — پیام رو به مدل انگلیسی است
		if ctx.Err() != nil {
			return "ERROR: cancelled by the user.", false
		}
		if reqCtx.Err() != nil {
			return "ERROR: request timed out after 20 seconds.", false
		}
		return "ERROR: " + err.Error(), false
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Sprintf("ERROR: the server answered HTTP %s for '%s'. The page was not fetched.", resp.Status, u.String()), false
	}
	ct := resp.Header.Get("Content-Type")
	if !webFetchAllowedContentType(ct) {
		return fmt.Sprintf("ERROR: unsupported content type %q — only text-like content (text/*, JSON, XML, XHTML) can be fetched.", ct), false
	}

	// سقف بدنه (۲MiB) روی بایت خام؛ تبدیل charset روی همان جریان کراندار.
	counter := &countingReader{r: io.LimitReader(resp.Body, webFetchMaxBody+1)}
	reader, cerr := charset.NewReader(counter, ct)
	if cerr != nil {
		// خطای خواندن (لغو/مهلت) نباید «charset نامعتبر» خوانده شود
		if ctx.Err() != nil {
			return "ERROR: cancelled by the user.", false
		}
		if reqCtx.Err() != nil {
			return "ERROR: request timed out after 20 seconds.", false
		}
		return fmt.Sprintf("ERROR: unsupported charset in Content-Type %q: %v", ct, cerr), false
	}
	raw, rerr := io.ReadAll(reader)
	if rerr != nil {
		if ctx.Err() != nil {
			return "ERROR: cancelled by the user.", false
		}
		if reqCtx.Err() != nil {
			return "ERROR: request timed out after 20 seconds.", false
		}
		return "ERROR: reading the response body failed: " + rerr.Error(), false
	}
	bodyTruncated := counter.n > webFetchMaxBody

	text := string(raw)
	if webFetchIsHTML(ct) {
		text = htmlToReadableText(text)
	}

	// سقف خروجی — برش rune-safe (متن فارسی نباید نصف شود)
	if r := []rune(text); len(r) > maxChars {
		text = string(r[:maxChars]) + fmt.Sprintf("\n…[truncated at %d characters]", maxChars)
	}
	if bodyTruncated {
		text += "\n…[response body truncated at 2 MiB]"
	}

	return wrapUntrustedContent("fetched_content", map[string]string{
		"url":       u.String(),
		"untrusted": "true",
	}, text), true
}

// ---------------------------------------------------------------- untrusted wrapper

// escapeUntrustedClosingTag هر «</tag» داخل محتوای نامطمئن را HTML-escape
// می‌کند تا نتواند پوشش <fetched_content>/<mcp_content> را ببندد و محتوای
// بیرونی القا شود (prompt-injection از درون نتیجه‌ی ابزار).
func escapeUntrustedClosingTag(content, tag string) string {
	lower := strings.ToLower(content)
	needle := "</" + strings.ToLower(tag)
	var b strings.Builder
	i := 0
	for {
		j := strings.Index(lower[i:], needle)
		if j < 0 {
			b.WriteString(content[i:])
			return b.String()
		}
		j += i
		b.WriteString(content[i:j])
		b.WriteString("&lt;/" + tag) // نقطه‌ی تزریق خنثی شد؛ بقیه‌ی تگ دست‌نخورده می‌ماند
		i = j + len(needle)
	}
}

// wrapUntrustedContent محتوای نامطمئن را در پوشش تگ با attributeهای داده‌شده
// می‌پیچد و تگِ بسته‌شونده‌ی داخل محتوا را escape می‌کند.
func wrapUntrustedContent(tag string, attrs map[string]string, content string) string {
	var b strings.Builder
	b.WriteString("<" + tag)
	for _, k := range []string{"url", "server", "tool", "untrusted"} {
		if v, ok := attrs[k]; ok {
			b.WriteString(" " + k + "=\"" + strings.ReplaceAll(v, "\"", "%22") + "\"")
		}
	}
	b.WriteString(">\n")
	b.WriteString(escapeUntrustedClosingTag(content, tag))
	b.WriteString("\n</" + tag + ">")
	return b.String()
}

// ---------------------------------------------------------------- HTML → text

// htmlDropTags محتوای این تگ‌ها کامل حذف می‌شود (کد/استایل/قاب).
var htmlDropTags = map[string]bool{
	"script": true, "style": true, "noscript": true,
	"template": true, "iframe": true, "svg": true,
}

// htmlBlockTags قبل از این تگ‌های شروع/پایان، خط جدید می‌نشیند تا ساختار
// سند در متنِ فشرده بازتاب پیدا کند.
var htmlBlockTags = map[string]bool{
	"p": true, "div": true, "section": true, "article": true, "header": true,
	"footer": true, "nav": true, "aside": true, "main": true, "table": true,
	"tr": true, "blockquote": true, "pre": true, "form": true, "figure": true,
	"hgroup": true, "ul": true, "ol": true, "dl": true, "hr": true,
}

// collapseHTMLWhitespace فاصله‌های سفید HTML را به یک فاصله فشرده می‌کند —
// نویسه‌های صفر-عرض (مثل ZWNJ) «فاصله سفید» نیستند و بایت‌به‌بایت می‌مانند.
func collapseHTMLWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// htmlToReadableText HTML را به متن خوانا تبدیل می‌کند: محتوای
// script/style/noscript/template/iframe/svg حذف؛ تیترها به شکل
// markdown-مانند (# …)؛ li با «-»؛ لینک‌ها به «متن (url)»؛ فاصله‌های
// سفید فشرده (ZWNJ حفظ می‌شود).
func htmlToReadableText(body string) string {
	tz := html.NewTokenizer(strings.NewReader(body))
	var b strings.Builder
	dropDepth := 0    // عمق تگ‌های حذفی (تودرتو هم درست کار می‌کند)
	pendingHref := "" // href لینکِ باز (تا پایان تگ a متنش جمع می‌شود)
	var linkText strings.Builder

	newline := func(n int) {
		for i := 0; i < n; i++ {
			b.WriteByte('\n')
		}
	}

	// flushLink متنِ لینکِ باز را می‌نویسد (به شکل «متن (url)»).
	//
	// C.2: تگ </a> ممکن است هرگز نرسد (HTML ناقص/بسته‌نشده). بدون این
	// flush، متنِ لنگر تا آخر صفحه در linkText می‌ماند و «بلعیده» می‌شود و
	// بقیه‌ی صفحه از خروجی حذف می‌شود — باگی که در صفحه‌های واقعی دیده شد.
	flushLink := func() {
		if pendingHref == "" {
			return
		}
		lt := strings.TrimSpace(linkText.String())
		href := pendingHref
		pendingHref = ""
		linkText.Reset()
		if lt == "" {
			return
		}
		if b.Len() > 0 {
			last := b.String()[b.Len()-1]
			if last != '\n' && last != ' ' {
				b.WriteByte(' ')
			}
		}
		if lt == href {
			b.WriteString(lt)
			return
		}
		b.WriteString(lt + " (" + href + ")")
	}

	for {
		tt := tz.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.TextToken:
			if dropDepth > 0 {
				continue
			}
			txt := collapseHTMLWhitespace(string(tz.Text()))
			if txt == "" {
				continue
			}
			if pendingHref != "" {
				linkText.WriteString(txt)
				continue
			}
			if b.Len() > 0 {
				last := b.String()[b.Len()-1]
				if last != '\n' && last != ' ' {
					b.WriteByte(' ')
				}
			}
			b.WriteString(txt)

		case html.StartTagToken, html.SelfClosingTagToken:
			nameB, hasAttr := tz.TagName()
			name := string(nameB)
			if dropDepth > 0 {
				if tt == html.StartTagToken && htmlDropTags[name] {
					dropDepth++
				}
				continue
			}
			// C.2: شروع هر بلوک (یا تیتر/li/br) ⊲ متنِ لینکِ باز را همان‌جا
			// خاتمه می‌دهد؛ </a> گم‌شده دیگر بقیه‌ی صفحه را نمی‌بلعد.
			if pendingHref != "" {
				isHeading := len(name) == 2 && name[0] == 'h' && name[1] >= '1' && name[1] <= '6'
				if htmlBlockTags[name] || isHeading || name == "li" || name == "br" || (name == "a" && hasAttr) {
					flushLink()
				}
			}
			if htmlDropTags[name] {
				if tt == html.StartTagToken {
					dropDepth = 1
				}
				continue
			}
			switch {
			case name == "a" && hasAttr:
				for {
					k, v, more := tz.TagAttr()
					if string(k) == "href" {
						pendingHref = string(v)
						linkText.Reset()
						break
					}
					if !more {
						break
					}
				}
			case name == "br":
				newline(1)
			case name == "li":
				newline(1)
				b.WriteString("- ")
			case name == "h1" || name == "h2" || name == "h3" || name == "h4" || name == "h5" || name == "h6":
				newline(2)
				b.WriteString(strings.Repeat("#", int(name[1]-'0')) + " ")
			case htmlBlockTags[name]:
				newline(1)
			}

		case html.EndTagToken:
			nameB, _ := tz.TagName()
			name := string(nameB)
			if dropDepth > 0 {
				if htmlDropTags[name] {
					dropDepth--
				}
				continue
			}
			switch {
			case name == "a" && pendingHref != "":
				// لینک به «متن (url)» — url فقط اگر با متن فرق دارد
				lt := strings.TrimSpace(linkText.String())
				if lt == "" {
					pendingHref = ""
					break
				}
				if b.Len() > 0 {
					last := b.String()[b.Len()-1]
					if last != '\n' && last != ' ' {
						b.WriteByte(' ')
					}
				}
				if lt == pendingHref || pendingHref == "" {
					b.WriteString(lt)
				} else {
					b.WriteString(lt + " (" + pendingHref + ")")
				}
				pendingHref = ""
			case name == "h1" || name == "h2" || name == "h3" || name == "h4" || name == "h5" || name == "h6":
				newline(1)
			case htmlBlockTags[name] || name == "li":
				newline(1)
			}
		}
	}

	// C.2: لینکِ باز در پایان سند هم نوشته می‌شود (فلاش نهایی)
	flushLink()

	// پاک‌سازی نهایی: خط‌های خالی پشت‌سرهم به یک خط
	lines := strings.Split(b.String(), "\n")
	var out strings.Builder
	blank := false
	for _, ln := range lines {
		ln = strings.TrimRight(ln, " \t")
		if strings.TrimSpace(ln) == "" {
			blank = true
			continue
		}
		if blank && out.Len() > 0 {
			out.WriteByte('\n')
		}
		blank = false
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(ln)
	}
	return out.String()
}
