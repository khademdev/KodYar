// تست‌های حسابداری مصرف تجمعی و نشانگر مصرف (نسخهٔ ۱.۱۱).
//
// قراردادها:
//
//	۱) شمارنده‌ی تجمعی (کل/هر Provider/هر مدل) با بستن برنامه یا قطع/وصل اتصال
//	   صفر نمی‌شود؛ صفر شدن فقط خودکار است، وقتی شارژ سرویس‌دهنده تشخیص
//	   داده شود (usageguard.go) — هیچ دکمهٔ دستی در UI وجود ندارد.
//	۲) هر دور فقط یک‌بار ثبت می‌شود: جمع prompt+completion همان دور.
//	۳) صفر کردن شمارنده، مصرف هر گفتگو را دست نمی‌زند.
//	۴) نشانگر مصرف، برچسب فارسی و پاپ‌اور توضیح دارد و در index.html بار می‌شود؛
//	   دکمهٔ «بررسی شارژ» فقط موجودی می‌خواند، هرگز مستقیم صفر نمی‌کند.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// usageTestApp یک App با تنظیمات موقت (بدون شبکه) می‌سازد.
func usageTestApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	a.mu.Lock()
	if a.settings.TokensByProvider == nil {
		a.settings.TokensByProvider = map[string]int64{}
	}
	if a.settings.TokensByModel == nil {
		a.settings.TokensByModel = map[string]int64{}
	}
	a.mu.Unlock()
	return a
}

func providerOf(a *App) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.TrimSpace(a.settings.Provider)
}

// recording از سمت حلقهٔ ایجنت است: هر دور یک بار.
func TestUsageCountersAccumulatePerRound(t *testing.T) {
	a := usageTestApp(t)
	pid := providerOf(a)
	if pid == "" {
		t.Fatal("test app must have a provider")
	}
	a.mu.Lock()
	a.recordTokenUsageLocked(nil, "model-a", 1000, 200, true)
	a.recordTokenUsageLocked(nil, "model-a", 1500, 300, true)
	a.recordTokenUsageLocked(nil, "model-b", 400, 100, false)
	total := a.settings.TokensUsed
	byProv := a.settings.TokensByProvider[pid]
	byModelA := a.settings.TokensByModel["model-a"]
	byModelB := a.settings.TokensByModel["model-b"]
	a.mu.Unlock()

	if total != 3500 {
		t.Fatalf("total = %d, want 3500 (a sum of all rounds)", total)
	}
	if byProv != 3500 {
		t.Fatalf("provider total = %d, want 3500", byProv)
	}
	if byModelA != 3000 || byModelB != 500 {
		t.Fatalf("per-model split = %d/%d, want 3000/500", byModelA, byModelB)
	}
	// صفر و منفی هیچ‌وقت اضافه نمی‌شوند (نه دور باطل، نه عدد منفی)
	a.mu.Lock()
	a.recordTokenUsageLocked(nil, "model-a", 0, 0, false)
	after := a.settings.TokensUsed
	a.mu.Unlock()
	if after != total {
		t.Fatalf("zero usage must not change counters: %d → %d", total, after)
	}
}

// TestUsageCountersSurviveRestart — «قطع/وصل یا بستن برنامه نباید از نو حساب
// کند»: مقدار روی دیسک می‌ماند و بعد از بارگذاری دوباره همان است.
func TestUsageCountersSurviveRestart(t *testing.T) {
	a := usageTestApp(t)
	a.mu.Lock()
	// شناسهٔ ثابت و غیرbuiltin: مهاجرت «حذف Providerهای پیش‌فرض» در
	// loadSettings نباید کلید شمارنده را تغییر دهد (وگرنه تست، مهاجرت را
	// نه پایداری مصرف را می‌سنجید).
	a.settings.Provider = "p-usage-test"
	a.settings.DefaultsRemoved = true
	a.recordTokenUsageLocked(nil, "m", 700, 300, true)
	_ = a.saveSettings()
	a.mu.Unlock()

	// «راه‌اندازی دوباره»: پاک کردن حافظه و خواندن دوباره از دیسک
	a.mu.Lock()
	a.settings.TokensUsed = 0
	a.settings.TokensByProvider = map[string]int64{}
	a.settings.TokensByModel = map[string]int64{}
	a.mu.Unlock()
	if err := a.loadSettings(); err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	view := a.SettingsView()
	if got := view["providerTokensUsed"]; got != int64(1000) {
		t.Fatalf("provider usage after restart = %v, want 1000", got)
	}
	if got := view["tokensUsed"]; got != int64(1000) {
		t.Fatalf("total usage after restart = %v, want 1000", got)
	}
}

// TestResetUsageOnlyOnDemand — مسیر قدیمی صفر کردن دستی فقط برای سازگاری
// نسخه‌های قبل به‌جا مانده است (UI دیگر دکمه‌ای ندارد)؛ رفتارش همان است:
// همهٔ شمارنده‌ها صفر و usageSince ثبت می‌شود، بدون دست‌زدن به مصرف گفتگو.
func TestResetUsageOnlyOnDemand(t *testing.T) {
	a := usageTestApp(t)
	a.mu.Lock()
	a.recordTokenUsageLocked(nil, "m", 500, 500, true)
	conv := &Conversation{ID: "c-usage", TokensUsed: 800, TokensExact: true}
	a.recordTokenUsageLocked(conv, "m", 100, 100, true)
	a.mu.Unlock()

	if err := a.ResetUsage(); err != nil {
		t.Fatalf("ResetUsage: %v", err)
	}
	view := a.SettingsView()
	if view["providerTokensUsed"] != int64(0) || view["tokensUsed"] != int64(0) || view["modelTokensUsed"] != int64(0) {
		t.Fatalf("reset must zero all cumulative counters: %+v", view)
	}
	if since, _ := view["usageSince"].(int64); since <= 0 {
		t.Fatalf("reset must stamp usageSince, got %v", view["usageSince"])
	}
	// مصرف گفتگو دست‌نخورده است
	if conv.TokensUsed != 1000 {
		t.Fatalf("conversation usage must survive reset, got %d", conv.TokensUsed)
	}
	// پس از صفر شدن، شمارش از نو شروع می‌شود
	a.mu.Lock()
	a.recordTokenUsageLocked(nil, "m", 120, 80, true)
	total := a.settings.TokensUsed
	a.mu.Unlock()
	if total != 200 {
		t.Fatalf("usage after reset = %d, want 200", total)
	}
}

// TestUsageWidgetWiring — نشانگر جدید بار می‌شود و متن‌های توضیح فارسی‌اش
// حاضرند (قررارداد قابل‌جست‌وجو؛ رفتار واقعی در مرورگر). از ۱.۱۱:
// هیچ دکمهٔ صفر کردن دستی وجود ندارد — فقط پایش خودکار شارژ و دکمهٔ
// «بررسی شارژ» که فقط موجودی می‌خواند.
func TestUsageWidgetWiring(t *testing.T) {
	htmlRaw, err := os.ReadFile(filepath.Join("frontend", "dist", "index.html"))
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(htmlRaw)
	for _, want := range []string{"assets/js/usagering.js", "assets/css/usagering.css"} {
		if !strings.Contains(html, want) {
			t.Fatalf("index.html must load %s", want)
		}
	}
	// نشانگر پس از app.js بار شود (app.js دایره را به‌روز می‌کند)
	if strings.Index(html, "assets/js/app.js") > strings.Index(html, "assets/js/usagering.js") {
		t.Fatal("usagering.js must load after app.js")
	}

	jsRaw, err := os.ReadFile(filepath.Join("frontend", "dist", "assets", "js", "usagering.js"))
	if err != nil {
		t.Fatalf("read usagering.js: %v", err)
	}
	js := string(jsRaw)
	for _, want := range []string{
		"کانتکست",           // برچسب هدف نشانگر
		"usage-pop",         // پاپ‌اور توضیحات
		"usage:auto-reset",  // رویداد صفر شدن خودکار (تشخیص شارژ)
		"خودکار صفر می‌شود", // صفر شدن دست کاربر نیست
		"بررسی شارژ",        // دکمهٔ فقط-خواندنی موجودی
		"usage-pill",        // ظاهر پیل (نه دایرهٔ گنگ)
		"usage-ring-svg",    // دونات SVG بازطراحی‌شده
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("usagering.js is missing %q", want)
		}
	}
	// دکمهٔ دستی «صفر کن» باید حذف شده باشد
	if strings.Contains(js, "ResetUsage") {
		t.Fatal("usagering.js must not call ResetUsage — auto-reset only")
	}
	// هیچ innerHTML‌ای (متن مدل/کاربر نباید به‌صورت HTML درج شود)
	if strings.Contains(js, "innerHTML") {
		t.Fatal("usagering.js must not use innerHTML")
	}

	cssRaw, err := os.ReadFile(filepath.Join("frontend", "dist", "assets", "css", "usagering.css"))
	if err != nil {
		t.Fatalf("read usagering.css: %v", err)
	}
	css := string(cssRaw)
	for _, v := range []string{"var(--text3)", "var(--border)", "var(--ui-size)", "var(--accent)", "var(--warn-soft)", "var(--danger-soft)"} {
		if !strings.Contains(css, v) {
			t.Fatalf("usagering.css must use theme variable %s", v)
		}
	}
}
