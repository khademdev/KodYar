// کش پرامپت (فاز ۲): نشانه‌گذاری بلوک ثابت پرامپت برای ارائه‌دهنده‌هایی که
// `cache_control` را می‌فهمند (سبک Anthropic).
//
// اصول طراحی:
//   - پیش‌فرض روشن است (۱.۹): کش، TTFT و هزینهٔ ورودی را در هر دور کم
//     می‌کند و بزرگ‌ترین اهرمِ سرعت در نوبت‌های چنددوری است.
//   - ارائه‌دهنده‌ی سخت‌گیر که فیلد را رد کند یک‌بار ۴۰۰ می‌گیرد و برای همان
//     Provider خودکار علامت‌دار می‌شود (بازگشت دور مصرف نمی‌شود). کاربر هم
//     می‌تواند از تنظیمات ← «مدل و رفتار» خاموشش کند.
//   - فقط بلوک پایدار نشانه می‌خورد: پیام system و آخرین tool. پیام‌های
//     گفتگو دست نمی‌خورند (محتوا هر نوبت عوض می‌شود).
//   - هر نشانه روی یک **کپی** گذاشته می‌شود تا تاریخچه‌ی ذخیره‌شده/محلی آلوده
//     نشود و عقب‌نشینی (۴۰۰) بدون پاک‌سازی دستی ممکن باشد.
//   - حافظه‌ی per-provider مثل stream_options: اولین ۴۰۰ به‌خاطر cache_control،
//     آن سرویس‌دهنده را تا restart علامت‌دار می‌کند و دور بعد اصلاً فرستاده
//     نمی‌شود.
package main

import (
	"strings"
	"sync"
)

// cacheControlHint شکل استاندارد نشانه‌ی کش در سبک Anthropic.
type cacheControlHint struct {
	Type string `json:"type"` // "ephemeral"
}

// promptCacheUnsupportedText نشانه‌هایی که در متن خطای ۴۰۰ می‌گردیم.
var promptCacheUnsupportedText = []string{"cache_control", "prompt caching", "cache control"}

// isPromptCacheUnsupported آیا این پاسخ به‌خاطر فیلد کش رد شده است؟
func isPromptCacheUnsupported(status int, reason string) bool {
	if status != 400 && status != 422 {
		return false
	}
	r := strings.ToLower(reason)
	for _, needle := range promptCacheUnsupportedText {
		if strings.Contains(r, needle) {
			return true
		}
	}
	return false
}

// promptCacheMem حافظه‌ی درون‌حافظه‌ای «این سرویس‌دهنده cache_control را
// نمی‌پذیرد» — تا restart عمر دارد، کلید = baseURL نرمال‌شده.
var promptCacheMem struct {
	mu     sync.Mutex
	broken map[string]bool
}

// providerSupportsPromptCache آیا برای این سرویس‌دهنده هنوز نشانه می‌فرستیم؟
func providerSupportsPromptCache(baseURL string) bool {
	k := streamUsageProviderKey(baseURL)
	promptCacheMem.mu.Lock()
	defer promptCacheMem.mu.Unlock()
	if promptCacheMem.broken == nil {
		return true
	}
	return !promptCacheMem.broken[k]
}

// markPromptCacheBroken سرویس‌دهنده را در حافظه نشانه‌گذاری می‌کند.
func markPromptCacheBroken(baseURL string) {
	k := streamUsageProviderKey(baseURL)
	promptCacheMem.mu.Lock()
	defer promptCacheMem.mu.Unlock()
	if promptCacheMem.broken == nil {
		promptCacheMem.broken = map[string]bool{}
	}
	promptCacheMem.broken[k] = true
}

// resetPromptCacheMemoryForTest برای تست‌ها: حافظه را پاک و قبلی را برمی‌گرداند.
func resetPromptCacheMemoryForTest() map[string]bool {
	promptCacheMem.mu.Lock()
	old := promptCacheMem.broken
	promptCacheMem.broken = map[string]bool{}
	promptCacheMem.mu.Unlock()
	return old
}

// restorePromptCacheMemoryForTest مقدار قبلی حافظه را برمی‌گرداند.
func restorePromptCacheMemoryForTest(old map[string]bool) {
	promptCacheMem.mu.Lock()
	promptCacheMem.broken = old
	promptCacheMem.mu.Unlock()
}

// withPromptCacheHints نشانه‌ی کش را روی کپیِ پیام system و آخرین tool
// می‌گذارد. خروجی: بدنه‌ی تازه + اینکه نشانه‌ای اعمال شد یا نه.
func withPromptCacheHints(req chatRequest) (chatRequest, bool) {
	applied := false
	if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
		msgs := append([]chatMessage(nil), req.Messages...)
		msgs[0].CacheControl = &cacheControlHint{Type: "ephemeral"}
		req.Messages = msgs
		applied = true
	}
	if len(req.Tools) > 0 {
		tools := append([]toolDef(nil), req.Tools...)
		tools[len(tools)-1].CacheControl = &cacheControlHint{Type: "ephemeral"}
		req.Tools = tools
		applied = true
	}
	return req, applied
}
