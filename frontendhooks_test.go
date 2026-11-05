// تست‌های قالب‌ها و هوک‌های فرانت‌اند (بدون تغییر بدنه، جدا از فایل مرحله‌ای قبلی).
package main

import (
	"os"
	"strings"
	"testing"
)

func TestFrontendPermissionModalGetsOpenClass(t *testing.T) {
	raw, err := os.ReadFile("frontend/dist/assets/js/permissions.js")
	if err != nil {
		t.Fatalf("read permissions.js: %v", err)
	}
	if !strings.Contains(string(raw), `classList.add("open")`) {
		t.Fatal("permissions.js must add the .open class to the modal overlay (otherwise the ask modal is invisible and auto-rejects)")
	}
}

// TestFrontendTodosPatchDefersPastAppJS — رگرسیون: پچ GetConversation در
// todos.js باید setFromConversation را با setTimeout (macrotask) به تعویق
// بیندازد تا activeId توسط app.js به‌روز شده باشد؛ در غیر این صورت گارد
// «فقط گفتگوی فعال» گفتگوی تازه‌بازشده را رد می‌کرد و پنل کهنه می‌ماند.
func TestFrontendTodosPatchDefersPastAppJS(t *testing.T) {
	raw, err := os.ReadFile("frontend/dist/assets/js/todos.js")
	if err != nil {
		t.Fatalf("read todos.js: %v", err)
	}
	if !strings.Contains(string(raw), "setTimeout(function () {") {
		t.Fatal("todos.js GetConversation patch must defer setFromConversation by a macrotask (setTimeout 0)")
	}
	// ایمنی متن: هنوز همه‌جا textContent است، نه innerHTML (کامنت‌ها را
	// نادیده می‌گیریم — جمله‌ی توضیحی «نه innerHTML» در کامنت هست).
	for _, ln := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.Contains(ln, "innerHTML") {
			t.Fatalf("todos.js must not use innerHTML outside comments: %s", trimmed)
		}
	}
}

// TestFrontendMCPSaveRefreshesSnapshot — رگرسیون: mcpsettings.js باید پس
// از ذخیره snapshot در MyAIState.view.mcpServers را با SettingsView به‌روز
// کند تا باز کردن دوباره‌ی تب، پیکربندی قبل از ذخیره را نشان ندهد.
func TestFrontendMCPSaveRefreshesSnapshot(t *testing.T) {
	raw, err := os.ReadFile("frontend/dist/assets/js/mcpsettings.js")
	if err != nil {
		t.Fatalf("read mcpsettings.js: %v", err)
	}
	if !strings.Contains(string(raw), "SettingsView()") {
		t.Fatal("mcpsettings.js save() must re-fetch SettingsView to refresh the snapshot")
	}
	if !strings.Contains(string(raw), `window.MyAIState.view.mcpServers = view.mcpServers`) {
		t.Fatal("mcpsettings.js save() must update MyAIState.view.mcpServers")
	}
	// نمونه‌ی دست‌نخورده نباید به‌عنوان پیکربندی واقعی ذخیره شود.
	if !strings.Contains(string(raw), `json = "[]"`) {
		t.Fatal("mcpsettings.js must save [] when the untouched SAMPLE is the only content")
	}
}

// ---------------------------------------------------------------- امنیت مرز داده

// TestSettingsSanitizesMaliciousProviderFields — رگرسیون امنیتی: فایل
// settings.json دست‌کاری/هم‌رسانی‌شده با شناسه یا رنگِ حاوی نویسه‌های خطرناک
// HTML نباید همان‌طور به innerHTML صفحات (modals/popovers) برسد — صفحه‌ی
// برنامه به bridge کامل گو دسترسی دارد. شناسه از نام بازسازی، رنگ به
// پیش‌فرض برمی‌گردد و کلید API به شناسه‌ی تازه منتقل می‌شود.
