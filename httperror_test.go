// تست‌های پیام خطای سرویس‌دهنده (نسخهٔ ۱.۵).
//
// قراردادها:
//
//	۱) بدنهٔ خام پاسخ (JSON یا صفحهٔ HTML) هرگز در پیام کاربر نمی‌آید؛
//	   حداکثر ۱۸۰ نویسه جزئیات پاک‌شده.
//	۲) کدهای پرتکرار (۴۰۱/۴۰۲/۴۰۳/۴۰۴/۴۲۹/۵xx) پیام فارسی و راهنما دارند.
//	۳) تشخیص خطاها (stream_options/tools/overflow) به متن خام نیاز دارد و
//	   نباید به نسخهٔ پاک‌شده وابسته شود.
package main

import (
	"strings"
	"testing"
)

// googleLike403 همان صفحهٔ HTML گوگل است که در لاگ واقعی دیده شد.
const googleLike403 = `<!DOCTYPE html>
<html lang=en>
  <meta charset=utf-8>
  <title>Error 403 (Forbidden)!!1</title>
  <style>
    *{margin:0;padding:0}html,code{font:15px/22px arial,sans-serif}
    #logo{display:inline-block;height:54px;width:150px}
  </style>
  <script>var x = "<b>not text</b>";</script>
  <a href=//www.google.com/><span id=logo aria-label=Google></span></a>
  <p><b>403.</b> <ins>That's an error.</ins>
  <p>Your client does not have permission to get URL <code>/v1beta/openai/chat/completions</code> from this server.
</html>`

func TestHumanizeProviderHTTPErrorInsufficientCredits(t *testing.T) {
	got := humanizeProviderHTTPError(402, `{"error":{"message":"insufficient credits"}}`)
	if !strings.Contains(got, "اعتبار") {
		t.Fatalf("402 must explain credits in Persian: %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "insufficient credits") {
		t.Fatalf("402 must keep the provider detail: %q", got)
	}
	if strings.ContainsAny(got, "<>") {
		t.Fatalf("provider body must not leak markup: %q", got)
	}
}

func TestHumanizeProviderHTTPErrorHTMLBody(t *testing.T) {
	got := humanizeProviderHTTPError(403, googleLike403)
	for _, bad := range []string{"<", "DOCTYPE", "<style", "margin:0", "arial"} {
		if strings.Contains(got, bad) {
			t.Fatalf("HTML body leaked %q into the message: %q", bad, got)
		}
	}
	if !strings.Contains(got, "HTML") || !strings.Contains(got, "HTTP 403") {
		t.Fatalf("403 HTML must be explained (HTML + status): %q", got)
	}
	if len([]rune(got)) > 400 {
		t.Fatalf("message must stay short, got %d runes", len([]rune(got)))
	}
}

func TestHumanizeProviderHTTPErrorCodes(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{401, `{"error":{"message":"invalid api key"}}`, "کلید API"},
		{404, `not found`, "مدل"},
		{429, `{"error":{"message":"rate limit"}}`, "محدودیت تعداد"},
		{500, ``, "خطای داخلی"},
		{503, `<html><body>upstream down</body></html>`, "HTTP 503"},
	}
	for _, c := range cases {
		got := humanizeProviderHTTPError(c.status, c.body)
		if !strings.Contains(got, c.want) {
			t.Fatalf("HTTP %d → %q, want %q", c.status, got, c.want)
		}
		if strings.ContainsAny(got, "<>") {
			t.Fatalf("HTTP %d leaked markup: %q", c.status, got)
		}
	}
}

func TestSanitizeErrorSnippetBounds(t *testing.T) {
	long := strings.Repeat("خطای طولانی سرویس‌دهنده ", 200)
	got := sanitizeErrorSnippet(long, maxSnippetRunes)
	if len([]rune(got)) > maxSnippetRunes+1 { // +1 برای «…»
		t.Fatalf("snippet must be bounded, got %d runes", len([]rune(got)))
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("snippet must be single-line: %q", got)
	}
	// نیستی → خالی
	if sanitizeErrorSnippet("   \n ", maxSnippetRunes) != "" {
		t.Fatal("blank body must stay empty")
	}
	// HTML بدون عنوان هم باید متن بدهد
	plain := sanitizeErrorSnippet(`<div><p>Bad &amp; broken</p></div>`, maxSnippetRunes)
	if plain != "Bad & broken" {
		t.Fatalf("html without title → %q", plain)
	}
}

// تشخیص خطاها روی بدنهٔ خام انجام می‌شود؛ این تست جلوی «پاک‌سازیِ زودهنگام»
// را می‌گیرد (اگر روزی humanize جای readErrorBody بنشیند، اینجا شکسته می‌شود).
func TestErrorDetectionStillUsesRawBody(t *testing.T) {
	raw := `{"error":{"message":"unknown field cache_control"}}`
	if !isPromptCacheUnsupported(400, raw) {
		t.Fatal("cache_control detection must see the raw body")
	}
	overflow := `{"error":{"message":"This model's maximum context length is 8192 tokens"}}`
	if !isContextOverflowError(400, overflow) {
		t.Fatal("context overflow detection must see the raw body")
	}
	// و پیام نمایشی همان خطا دیگر جزئیات فنی خام را نشان نمی‌دهد
	if strings.Contains(humanizeProviderHTTPError(400, raw), "{") {
		t.Fatal("display message must not contain raw JSON braces")
	}
}
