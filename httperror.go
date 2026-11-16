// پیام‌های خطای سرویس‌دهنده — کوتاه، فارسی و بدون HTML.
//
// باگ واقعی: بدنهٔ پاسخ خطا (تا ۴KB، حتی صفحهٔ HTML خطای ۴۰۳ گوگل) خام در
// پیام نهایی می‌رفت، در گفتگو ذخیره می‌شد و به‌صورت یک متن طولانی در UI
// ظاهر می‌شد. کاربر چیزی نمی‌فهمید و پیام هم «می‌رفت».
//
// این فایل فقط لایهٔ نمایش است: تشخیص خطاها (isStreamOptionsUnsupported,
// isContextOverflowError, …) همچنان روی متن خام readErrorBody کار می‌کند،
// چون باید کلیدواژه‌ها را ببیند.
package main

import (
	"encoding/json"
	"fmt"
	stdhtml "html"
	"regexp"
	"strings"
)

// maxSnippetRunes سقف طول جزئیات نمایشی (نویسه، نه بایت — متن فارسی).
const maxSnippetRunes = 180

var (
	htmlBlockRe = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(script|style)>`)
	htmlTagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRe     = regexp.MustCompile(`\s+`)
)

// looksLikeHTML آیا بدنه یک صفحهٔ HTML است (نه JSON API)؟
func looksLikeHTML(s string) bool {
	l := strings.ToLower(s)
	if strings.Contains(l, "<!doctype") || strings.Contains(l, "<html") ||
		strings.Contains(l, "<title") || strings.Contains(l, "<style") || strings.Contains(l, "<body") {
		return true
	}
	return strings.Count(s, "<") >= 4
}

// htmlToText عنوان و متن صفحهٔ خطا را به یک جملهٔ کوتاه تبدیل می‌کند.
func htmlToText(s string) string {
	var title string
	if m := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`).FindStringSubmatch(s); len(m) == 2 {
		title = strings.TrimSpace(stdhtml.UnescapeString(m[1]))
	}
	body := htmlBlockRe.ReplaceAllString(s, " ")
	body = htmlTagRe.ReplaceAllString(body, " ")
	body = stdhtml.UnescapeString(body)
	body = spaceRe.ReplaceAllString(body, " ")
	body = strings.TrimSpace(body)
	if title != "" {
		// عنوان معمولاً خلاصهٔ خطاست؛ اگر در متن هم بود تکرارش نکن
		if strings.Contains(body, title) {
			return body
		}
		if body == "" {
			return title
		}
		return title + " — " + body
	}
	return body
}

// jsonErrorDetail پیام خطای JSON را (بدون آکولاد و کلیدهای فنی) بیرون
// می‌کشد؛ اگر JSON نبود یا پیامی نداشت، "".
func jsonErrorDetail(raw string) string {
	txt := strings.TrimSpace(raw)
	if !strings.HasPrefix(txt, "{") {
		return ""
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(txt), &payload) != nil {
		return ""
	}
	if m := strings.TrimSpace(payload.Error.Message); m != "" {
		return m
	}
	return strings.TrimSpace(payload.Message)
}

// sanitizeErrorSnippet بدنهٔ خطا را برای نمایش آماده می‌کند: HTML حذف،
// JSON به پیامش خلاصه، فاصله‌ها جمع و طول کران‌دار (نویسه‌محور).
func sanitizeErrorSnippet(raw string, maxRunes int) string {
	txt := strings.TrimSpace(raw)
	if txt == "" {
		return ""
	}
	if detail := jsonErrorDetail(txt); detail != "" {
		txt = detail
	} else if looksLikeHTML(txt) {
		txt = htmlToText(txt)
	}
	txt = spaceRe.ReplaceAllString(txt, " ")
	txt = strings.TrimSpace(txt)
	if maxRunes <= 0 {
		maxRunes = maxSnippetRunes
	}
	if r := []rune(txt); len(r) > maxRunes {
		txt = strings.TrimSpace(string(r[:maxRunes])) + "…"
	}
	return txt
}

// codeText کد وضعیت را با رقم ASCII می‌آورد (گرفتن در «لاگ خطاها» آسان و
// قابل جست‌وجو بماند: "HTTP 402"). base خالی → فقط پرانتز کد.
func codeText(status int, base string) string {
	code := fmt.Sprintf("(HTTP %d)", status)
	if strings.TrimSpace(base) == "" {
		return code
	}
	return base + " " + code
}

// humanizeProviderHTTPError پیام نهایی فارسی برای پاسخ غیر-۲۰۰ سرویس‌دهنده.
// status کد HTTP و raw بدنهٔ پاسخ (ممکن است HTML باشد) است.
func humanizeProviderHTTPError(status int, raw string) string {
	snippet := sanitizeErrorSnippet(raw, maxSnippetRunes)
	htmlBody := looksLikeHTML(strings.TrimSpace(raw))
	lower := strings.ToLower(snippet)

	withDetail := func(base string) string {
		if snippet == "" {
			return base
		}
		return base + " — " + snippet
	}
	htmlHint := "درخواست توسط لبهٔ سرویس‌دهنده/پروکسی رد شد و پاسخ HTML بود (نه JSON)؛ معمولاً کلید نامعتبر، محدودیت منطقه‌ای/IP یا آدرس پایهٔ اشتباه است."

	switch {
	case status == 401:
		return withDetail(codeText(status, "کلید API پذیرفته نشد") + ". کلید را در تنظیمات بررسی یا دوباره وارد کنید.")
	case status == 402 || strings.Contains(lower, "insufficient credit") || strings.Contains(lower, "quota"):
		return withDetail(codeText(status, "اعتبار حساب سرویس‌دهنده کافی نیست") + ". حساب را شارژ کنید یا سرویس‌دهندهٔ دیگری را انتخاب کنید.")
	case status == 403:
		if htmlBody {
			return htmlHint + " " + codeText(status, "")
		}
		return withDetail(codeText(status, "دسترسی به این سرویس‌دهنده مجاز نیست") + "؛ کلید/مجوز یا محدودیت منطقه‌ای را بررسی کنید.")
	case status == 404:
		return withDetail(codeText(status, "آدرس یا مدل سرویس‌دهنده پیدا نشد") + ". آدرس پایه و نام مدل را در تنظیمات بررسی کنید.")
	case status == 429:
		return withDetail(codeText(status, "محدودیت تعداد درخواست سرویس‌دهنده فعال شد") + ". کمی بعد دوباره بفرستید.")
	case status == 413:
		return withDetail(codeText(status, "حجم درخواست برای سرویس‌دهنده بزرگ بود") + ". گفتگوی تازه یا فشرده‌سازی زمینه لازم است.")
	case status >= 500:
		return withDetail(codeText(status, "سرویس‌دهنده خطای داخلی برگرداند") + ". چند لحظه بعد دوباره بفرستید.")
	case htmlBody:
		return htmlHint + " " + codeText(status, "")
	}
	if snippet == "" {
		return codeText(status, "سرویس‌دهنده خطا برگرداند") + "."
	}
	return codeText(status, "سرویس‌دهنده خطا برگرداند") + ": " + snippet
}
