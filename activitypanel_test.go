// تست قراردادِ فرانت‌اند برای نوار «فعالیت ایجنت» (activity.js/activity.css):
// اتصال به رویدادهای موجود، بارگذاری در index.html، ایمنی متن و رنگ‌های تم.
// اجرای واقعی مرورگر در frontend/tests (Playwright) است؛ این تست همیشه‌روان است.
package main

import (
	"os"
	"strings"
	"testing"
)

func TestFrontendActivityPanelWiring(t *testing.T) {
	raw, err := os.ReadFile("frontend/dist/assets/js/activity.js")
	if err != nil {
		t.Fatalf("read activity.js: %v", err)
	}
	js := string(raw)

	// رویدادهایی که نوار بر آن‌ها سوار می‌شود — بدون تغییر بک‌اند.
	for _, want := range []string{
		`"chat:tool"`,
		`"chat:tool_result"`,
		`"chat:start"`,
		`"chat:done"`,
		`"chat:error"`,
		`#composer`,
		`GetConversation`,
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("activity.js must hook %s", want)
		}
	}

	// ایمنی متن: هیچ innerHTMLای (مسیر/دستور/خطا از مدل می‌آید).
	for _, ln := range strings.Split(js, "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.Contains(ln, "innerHTML") {
			t.Fatalf("activity.js must not use innerHTML: %s", trimmed)
		}
	}
	// مسیرها ایزوله می‌شوند تا در RTL به‌هم نریزند.
	if !strings.Contains(js, `"bdi"`) || !strings.Contains(js, `"ltr"`) {
		t.Fatal("activity.js must render path/detail in an isolated <bdi dir=\"ltr\">")
	}

	// بارگذاری در index.html: CSS بعد از todos.css و اسکریپت بعد از todos.js
	// (تا پنل زیر «کارهای ایجنت» و پچ GetConversation زنجیره‌ای شود).
	htmlRaw, err := os.ReadFile("frontend/dist/index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(htmlRaw)
	if !strings.Contains(html, "assets/css/activity.css") {
		t.Fatal("index.html must link assets/css/activity.css")
	}
	if !strings.Contains(html, "assets/js/activity.js") {
		t.Fatal("index.html must load assets/js/activity.js")
	}
	if strings.Index(html, "assets/css/todos.css") > strings.Index(html, "assets/css/activity.css") {
		t.Fatal("activity.css must load after todos.css")
	}
	if strings.Index(html, "assets/js/todos.js") > strings.Index(html, "assets/js/activity.js") {
		t.Fatal("activity.js must load after todos.js (panel order + patch chain)")
	}

	// استایل فقط از متغیرهای تم استفاده کند (تم روشن/تاریک و اندازهٔ فونت).
	cssRaw, err := os.ReadFile("frontend/dist/assets/css/activity.css")
	if err != nil {
		t.Fatalf("read activity.css: %v", err)
	}
	css := string(cssRaw)
	for _, v := range []string{"var(--ok)", "var(--danger)", "var(--accent-h)", "var(--radius)", "var(--ui-size)", "var(--font-code)"} {
		if !strings.Contains(css, v) {
			t.Fatalf("activity.css must use theme variable %s", v)
		}
	}
	// نوار هم‌عرض composer است (۸۸۰px در app.css)
	if !strings.Contains(css, "max-width: 880px") {
		t.Fatal("activity.css must match the composer width (880px)")
	}
}

// TestActivityPanelStrings — جمله‌های فارسی حالت‌ها و ابزارهای اصلی در ماژول
// حاضرند (قرارداد قابل‌جست‌وجو؛ رفتار واقعی در مرورگر). نام‌ها از نسخهٔ ۱.۲
// تغییر کردند: «فعالیت ایجنت» ← «گزارش زنده»، «کارهای ایجنت» ← «برنامهٔ کار».
func TestActivityPanelStrings(t *testing.T) {
	raw, err := os.ReadFile("frontend/dist/assets/js/activity.js")
	if err != nil {
		t.Fatalf("read activity.js: %v", err)
	}
	js := string(raw)
	for _, want := range []string{
		"در حال خواندن",
		"خوانده شد",
		"ناموفق بود",
		"در حال ویرایش",
		"ویرایش شد",
		"حذف شد",
		"در حال حذف",
		"در حال اجرای دستور",
		"اجرا شد",
		"در حال جست‌وجوی",
		"جست‌وجو",
		"در حال سپردن زیرکار",
		"در حال به‌روزرسانی برنامه",
		"در حال بررسی وضعیت گیت",
		"در حال دریافت",
		// عنوان و شمارنده‌ی جدید — تفکیک «گزارش زنده» از «برنامهٔ کار»
		"گزارش زنده",
		"عملیات",
		// ارجاع خطوط گزارش به وظیفه‌ی جاری و سوییچ نمایش
		"taskChip",
		"currentTask",
		"SetShowActivity",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("activity.js is missing the Persian phrasing %q", want)
		}
	}
}
