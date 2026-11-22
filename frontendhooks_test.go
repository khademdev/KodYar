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

// TestFrontendThinkingRevealsStreamMessage — رگرسیون: دلتای استدلال باید
// حباب پیام استریم (#stream-msg) را از حالت hidden بیرون بیاورد. پیش از این
// دلتاها فقط داخل think-body نوشته می‌شدند ولی والدشان تا اولین دلتای متن
// پنهان می‌ماند؛ کاربر فقط «در حال فکر کردن…» می‌دید و متن استدلال دیده نمی‌شد.
func TestFrontendThinkingRevealsStreamMessage(t *testing.T) {
	raw, err := os.ReadFile("frontend/dist/assets/js/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	s := string(raw)
	idx := strings.Index(s, `setStreamPhase("think")`)
	if idx < 0 {
		t.Fatal("app.js: chat:thinking handler (setStreamPhase(\"think\")) not found")
	}
	end := idx + 400
	if end > len(s) {
		end = len(s)
	}
	window := s[idx:end]
	if !strings.Contains(window, `getElementById("stream-msg")`) || !strings.Contains(window, "hidden=!1") {
		t.Fatal("chat:thinking handler must unhide #stream-msg, otherwise reasoning text stays invisible behind a hidden bubble")
	}
}

// TestFrontendChatPhaseDoesNotClaimWriting — رگرسیون: در حالت چت معمولی،
// نوار وضعیت پیش از رسیدن اولین متن (و پس از پایان هر ابزار) «در حال نوشتن…»
// نمی‌گوید؛ فاز فقط با دلتای واقعی متن به write می‌رود. پیش از این حالت چت
// از همان لحظه‌ی ارسال write می‌شد و کاربر فکر می‌کرد پاسخ در حال نوشته شدن است
// در حالی که مدل هنوز در حال فکر/اجرای ابزار بود.
func TestFrontendChatPhaseDoesNotClaimWriting(t *testing.T) {
	raw, err := os.ReadFile("frontend/dist/assets/js/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	s := string(raw)
	if strings.Contains(s, `?"start":"write"`) {
		t.Fatal("startStreamClock must not start in the write phase for chat mode (it claims writing before any text)")
	}
	if !strings.Contains(s, `r.streamPhase="start",lbl()`) {
		t.Fatal("startStreamClock must start in the start phase for both modes")
	}
	// write فقط باید با دلتای واقعی متن ست شود (یک مورد در chat:delta).
	if n := strings.Count(s, `setStreamPhase("write")`); n != 1 {
		t.Fatalf("setStreamPhase(\"write\") must appear exactly once (chat:delta), found %d", n)
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
// از ذخیره snapshot در KodYarState.view.mcpServers را با SettingsView به‌روز
// کند تا باز کردن دوباره‌ی تب، پیکربندی قبل از ذخیره را نشان ندهد.
func TestFrontendMCPSaveRefreshesSnapshot(t *testing.T) {
	raw, err := os.ReadFile("frontend/dist/assets/js/mcpsettings.js")
	if err != nil {
		t.Fatalf("read mcpsettings.js: %v", err)
	}
	if !strings.Contains(string(raw), "SettingsView()") {
		t.Fatal("mcpsettings.js save() must re-fetch SettingsView to refresh the snapshot")
	}
	if !strings.Contains(string(raw), `window.KodYarState.view.mcpServers = view.mcpServers`) {
		t.Fatal("mcpsettings.js save() must update KodYarState.view.mcpServers")
	}
	// نمونه‌ی دست‌نخورده نباید به‌عنوان پیکربندی واقعی ذخیره شود.
	if !strings.Contains(string(raw), `json = "[]"`) {
		t.Fatal("mcpsettings.js must save [] when the untouched SAMPLE is the only content")
	}
}

// TestFrontendVisionSwitchContract — سوئیچ تصویر در تب «مدل و رفتار»:
// سه حالت auto/on/off، ذخیره از طریق SetVisionImages و خواندن مقدار از
// KodYarState.view.visionImages؛ بدون این، کاربر مجبور بود settings.json را
// دستی ویرایش کند.
func TestFrontendVisionSwitchContract(t *testing.T) {
	raw, err := os.ReadFile("frontend/dist/assets/js/agentsettings.js")
	if err != nil {
		t.Fatalf("read agentsettings.js: %v", err)
	}
	js := string(raw)
	for _, want := range []string{
		`id: "gen-vision"`,
		`SetVisionImages`,
		`["on", "همیشه بفرست`, `["off", "هرگز`,
		`visionImages`,
		`saveVision();`,
		`populateVision();`,
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("agentsettings.js is missing %q (vision switch contract)", want)
		}
	}
}

// TestFrontendToolCardShowsAttachedScreenshot — تصویری که به مدل رفت باید
// در کارت ابزار هم دیده شود (نه فقط مسیر فایل در متن نتیجه).
func TestFrontendToolCardShowsAttachedScreenshot(t *testing.T) {
	js, err := os.ReadFile("frontend/dist/assets/js/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	for _, want := range []string{
		`Array.isArray(e.images)`,
		`shots.className="tool-shots"`,
		`im.alt="browser_screenshot"`,
		`vb.className="tool-vision "`,
		`دیده‌شده توسط مدل`,
		`فقط برای کاربر ذخیره شد`,
	} {
		if !strings.Contains(string(js), want) {
			t.Fatalf("tool card must render attached screenshots and their vision state; app.js is missing %q", want)
		}
	}
	css, err := os.ReadFile("frontend/dist/assets/css/app.css")
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	for _, want := range []string{".tool-card .tool-shots img", ".tool-card .tool-vision"} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("app.css is missing %q", want)
		}
	}
}

// TestFrontendActivityLiveDetails — «عملیات زندهٔ دقیق‌تر»: زمان‌سنج ردیف
// در حال اجرا، آخرین خط خروجی دستور و آمار diff بعد از ویرایش.
func TestFrontendActivityLiveDetails(t *testing.T) {
	js, err := os.ReadFile("frontend/dist/assets/js/activity.js")
	if err != nil {
		t.Fatalf("read activity.js: %v", err)
	}
	for _, want := range []string{
		`startedAt: Date.now()`,
		`function ensureTick(`,
		`data-start`,
		`chat:tool_output`,
		`agent:change_applied`,
		`act-live`,
		`act-diff`,
	} {
		if !strings.Contains(string(js), want) {
			t.Fatalf("activity.js is missing %q (live ops detail)", want)
		}
	}
	css, err := os.ReadFile("frontend/dist/assets/css/activity.css")
	if err != nil {
		t.Fatalf("read activity.css: %v", err)
	}
	for _, want := range []string{".act-live", ".act-diff"} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("activity.css is missing %q", want)
		}
	}
}

// TestFrontendThinkingPanelLiveSummary — «فکر کردن» در طول نوبت باز و زنده
// می‌ماند، زمان و توکن تقریبی نشان می‌دهد و بعد از پایان جمع می‌شود.
func TestFrontendThinkingPanelLiveSummary(t *testing.T) {
	raw, err := os.ReadFile("frontend/dist/assets/js/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	js := string(raw)
	for _, want := range []string{
		`thinkStartedAt:0,thinkEndedAt:0`,
		`function thinkInfo(`,
		`function thinkLabelSync(`,
		`n(tok)+" توکن"`,
		`window.KodYarLastThink`,
		`thinkLabelSync();`,
		`r.thinkStartedAt||(r.thinkStartedAt=Date.now())`,
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("app.js is missing %q (live thinking panel)", want)
		}
	}
	// بلوک فکر کردنِ پیام‌های ذخیره‌شده باید بسته باشد (جمع‌شده پس از پایان).
	if strings.Contains(js, `class="think-block"`+" open") {
		t.Fatal("saved thinking blocks must stay collapsed (no open attribute)")
	}
}

// TestFrontendPermissionModalExpiresSafely — رگرسیون باگ «اجازه می‌دهم ولی
// عملیات رد می‌شود»: مودال دسترسی باید با پایان نوبت (chat:error/chat:done)
// خودش بسته شود، پیام «منقضی شد، دوباره بفرست» بدهد و خطای
// RespondPermissionConfirm را نبلعد. پیش‌تر پاسخ در یک catch خالی قورت
// داده می‌شد و مودالِ منقضی روی صفحه می‌ماند.
func TestFrontendPermissionModalExpiresSafely(t *testing.T) {
	raw, err := os.ReadFile("frontend/dist/assets/js/permissions.js")
	if err != nil {
		t.Fatalf("read permissions.js: %v", err)
	}
	s := string(raw)
	for _, want := range []string{
		`EventsOn("chat:error"`,
		`EventsOn("chat:done"`,
		"منقضی شد",
		"دوباره بفرست",
		"expireActive",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("permissions.js must contain %q so an expired ask closes itself and says so", want)
		}
	}
	if strings.Contains(s, "/* منقضی شده — بی‌خطر */") {
		t.Fatal("permissions.js must not swallow RespondPermissionConfirm errors")
	}
}

// TestFrontendPermissionModalShowsCountdown — مودال دسترسی باید مهلت واقعی
// بک‌اند (ask.timeoutSec) را به شمارش معکوس تبدیل کند تا کاربر پیش از ردِ
// خودکار بداند چقدر وقت دارد؛ تایمر هم با بسته‌شدن مودال پاک شود.
func TestFrontendPermissionModalShowsCountdown(t *testing.T) {
	raw, err := os.ReadFile("frontend/dist/assets/js/permissions.js")
	if err != nil {
		t.Fatalf("read permissions.js: %v", err)
	}
	s := string(raw)
	for _, want := range []string{"ask.timeoutSec", "زمان باقی‌مانده", "setInterval", "clearInterval"} {
		if !strings.Contains(s, want) {
			t.Fatalf("permissions.js must contain %q for the expiry countdown", want)
		}
	}
}

// ---------------------------------------------------------------- امنیت مرز داده

// TestSettingsSanitizesMaliciousProviderFields — رگرسیون امنیتی: فایل
// settings.json دست‌کاری/هم‌رسانی‌شده با شناسه یا رنگِ حاوی نویسه‌های خطرناک
// HTML نباید همان‌طور به innerHTML صفحات (modals/popovers) برسد — صفحه‌ی
// برنامه به bridge کامل گو دسترسی دارد. شناسه از نام بازسازی، رنگ به
// پیش‌فرض برمی‌گردد و کلید API به شناسه‌ی تازه منتقل می‌شود.
