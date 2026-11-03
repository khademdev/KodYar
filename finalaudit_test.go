// تست‌های رگرسیون بازبینی نهایی ۱.۷.۱ — باگ‌های MCP و فرانت‌اند که در
// بازبینی مستقل پیدا و رفع شدند:
//
//  1. کرش سرور MCP «بعد از نصب» باید onExit را فعال کند (حذف از نقشه +
//     شمارش backoff) و پس از گذشت backoff، راه‌اندازی مجدد کار کند —
//     قبلاً onClientExit هرگز صدا زده نمی‌شد و سرور برای همیشه می‌مرد.
//  2. نوشتن روی stdin سرورِ هنگ‌شده باید سقف زمانی داشته باشد و درخت
//     فرایند را بکشد — قبلاً Write برای همیشه بلاک و کل نوبت ایجنت
//     قفل می‌شد (حتی Stop بازش نمی‌کرد).
//  3. onClientExit نباید کلاینتِ نصب‌نشده/جایگزین‌شده را بشمارد (دوباره‌شماری).
//  4. مودال اجازه (permissions.js) باید کلاس open بگیرد — قبلاً نامرئی و
//     غیرقابل‌کلیک بود و همه‌ی درخواست‌ها پس از ۳ دقیقه خودکار رد می‌شدند.
//  5. پنل todos باید پس از تعویض گفتگو با تأخیر macrotask بازگردانی شود.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMCPCrashAfterInstallWiresOnExitBackoffAndRecovery
//
// سرور fake-die بعد از handshake موفق (نصب در نقشه‌ی manager) خودش خارج
// می‌شود. باید: کلاینت مرده از نقشه در برود، شکست‌ها بشمارند، تلاش فوری
// backoff بخورد و پس از گذشت مهلت، راه‌اندازی مجدد موفق شود.
func TestMCPCrashAfterInstallWiresOnExitBackoffAndRecovery(t *testing.T) {
	a := newFakeMCPApp(t, "fake-die", true)
	c := ensureMCPStarted(t, a)
	defer a.mcp.stopAll()

	// اندکی صبر تا خروج غیرمنتظره (سرور ~۴۰۰ms بعد از handshake می‌میرد).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.mcp.mu.Lock()
		_, inMap := a.mcp.clients["fake"]
		a.mcp.mu.Unlock()
		if !inMap && a.mcp.client("fake") == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	a.mcp.mu.Lock()
	_, stillInMap := a.mcp.clients["fake"]
	st := a.mcp.stateLocked("fake")
	failures, deadUntil, status := st.failures, st.deadUntil, st.status
	a.mcp.mu.Unlock()
	if stillInMap {
		t.Fatal("dead client must be removed from the manager map via onExit")
	}
	if failures != 1 {
		t.Fatalf("unexpected crash must count exactly one failure, got %d", failures)
	}
	if !time.Now().Before(deadUntil) {
		t.Fatal("unexpected crash must schedule a restart backoff")
	}
	if status != mcpStatusStopped {
		t.Fatalf("status after crash = %q, want stopped", status)
	}
	if !c.isClosed() {
		t.Fatal("crashed client must report closed")
	}

	// تلاش فوری → backoff
	if _, err := a.mcp.ensureStarted(context.Background(), a, a.settings.MCPServers[0]); err == nil || !strings.Contains(err.Error(), "backoff") {
		t.Fatalf("immediate restart must be blocked by backoff: %v", err)
	}

	// گذشت مهلت backoff → راه‌اندازی مجدد موفق (چرخه‌ی ازکارافتادن/بهبود)
	a.mcp.mu.Lock()
	a.mcp.stateLocked("fake").deadUntil = time.Now().Add(-time.Second)
	a.mcp.mu.Unlock()
	c2 := ensureMCPStarted(t, a)
	defer c2.stop()
	res, ok := a.toolMCPCall(context.Background(), "mcp_fake_echo", `{"text":"recovered"}`)
	if !ok || !strings.Contains(res, "recovered") {
		t.Fatalf("restarted server must serve tool calls, got ok=%v res=%q", ok, res)
	}
	a.mcp.mu.Lock()
	st = a.mcp.stateLocked("fake")
	a.mcp.mu.Unlock()
	if st.failures != 0 || st.gaveUp {
		t.Fatalf("successful restart must reset backoff, failures=%d gaveUp=%v", st.failures, st.gaveUp)
	}
}

// TestMCPWriteWatchdogKillsHungServer
//
// سرور fake-silent پس از handshake موفق stdin را نمی‌خواند. نوشتن پیام
// بزرگ‌تر از بافر لوله باید تا سقف mcpWriteTimeout بلاک بماند، بعد خطا
// برگرداند و درخت فرایند را بکشد — نه اینکه برای همیشه بلاک بماند.
func TestMCPWriteWatchdogKillsHungServer(t *testing.T) {
	restore := setMCPWriteTimeoutForTest(700 * time.Millisecond)
	defer restore()

	a := newFakeMCPApp(t, "fake-silent", true)
	// فقط handshake — سرور بلافاصله پس از آن stdin را رها می‌کند و
	// tools/list پاسخ نمی‌دهد (بافر لوله پیام کوچک را می‌بلعد).
	ctxStart, cancelStart := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStart()
	c, err := a.mcp.ensureStarted(ctxStart, a, a.settings.MCPServers[0])
	if err != nil {
		t.Fatalf("fake-silent server must complete the handshake: %v", err)
	}
	defer a.mcp.stopAll()

	// پیام بزرگ‌تر از بافر لوله (۶۴KB لینوکس/ویندوز کمتر) → Write بلاک می‌شود.
	payload := map[string]interface{}{"blob": strings.Repeat("x", 256*1024)}
	start := time.Now()
	werr := c.notify("test/huge", payload)
	elapsed := time.Since(start)
	if werr == nil {
		t.Fatal("write to a non-reading server must fail, not hang forever")
	}
	if !strings.Contains(werr.Error(), "timed out") {
		t.Fatalf("watchdog must report a write timeout, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("watchdog fired too late: %s", elapsed)
	}

	// درخت کشته شد → کلاینت باید مرده گزارش شود (تا start مجدد ممکن شود).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.isClosed() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !c.isClosed() {
		t.Fatal("killed hung server must leave its client closed")
	}
}

// TestMCPOnClientExitIgnoresForeignClient
//
// onClientExit فقط نمونه‌ی نصب‌شده در نقشه را می‌شمارد؛ کلاینت مرده‌ی
// جایگزین‌شده یا هرگز نصب‌نشده نباید شکست جدیدی بشمارد.
func TestMCPOnClientExitIgnoresForeignClient(t *testing.T) {
	a := newFakeMCPApp(t, "fake-ok", true)
	c := ensureMCPStarted(t, a)
	defer a.mcp.stopAll()

	a.mcp.mu.Lock()
	before := a.mcp.stateLocked("fake").failures
	a.mcp.mu.Unlock()

	// نمونه‌ی غریبه (نصب‌نشده) — نباید بشمارد.
	a.mcp.onClientExit("fake", &mcpClient{name: "fake"})

	// نمونه‌ی درست ولی هنوز زنده — علامت‌گذاری عمدی توقف، بدون onExit.
	a.mcp.onClientExit("other", c)

	a.mcp.mu.Lock()
	after := a.mcp.stateLocked("fake").failures
	a.mcp.mu.Unlock()
	if after != before {
		t.Fatalf("foreign/unscheduled exits must not count: before=%d after=%d", before, after)
	}
}

// TestFrontendPermissionModalGetsOpenClass — رگرسیون باگ بحرانی:
// permissions.js باید کلاس «open» را به overlay اضافه کند؛ بدون آن،
// .modal-overlay به‌طور پیش‌فرض opacity:0 و pointer-events:none است و
// مودال تأیید اجازه نامرئی/غیرقابل‌کلیک بود (همه‌ی درخواست‌ها پس از
// مهلت ۳ دقیقه‌ای خودکار «رد» می‌شدند).
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
func TestSettingsSanitizesMaliciousProviderFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MYAI_CONFIG_DIR", dir)
	const evilID = `"><img src=x onerror=alert(1)>`
	const evilColor = `red" onmouseover="alert(2)`
	// فیکسچر با Marshal ساخته می‌شود تا کوتیشن‌های مخرب به‌درستی
	// JSON-escape شوند (فایل دست‌کاری‌شده‌ی واقعی همین کار را می‌کند).
	fixture := map[string]interface{}{
		"providers": []map[string]interface{}{
			{"id": evilID, "name": "Evil Cloud", "baseUrl": "https://evil.example/v1", "color": evilColor, "models": []string{"m1"}},
			{"id": "safe-id", "name": "Good Cloud", "baseUrl": "https://good.example/v1", "color": "#3fb984", "models": []string{"m2"}},
			{"id": "ابر-پارسی", "name": "ابر فارسی", "baseUrl": "https://fa.example/v1", "color": "", "models": []string{"m3"}},
		},
		"apiKeys": map[string]string{evilID: "sk-secret"},
	}
	raw, merr := json.Marshal(fixture)
	if merr != nil {
		t.Fatal(merr)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	if err := a.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if len(a.settings.Providers) != 3 {
		t.Fatalf("want 3 providers, got %d", len(a.settings.Providers))
	}
	p0 := a.settings.Providers[0]
	if !providerIDSafe(p0.ID) || strings.ContainsAny(p0.ID, `"><=`) {
		t.Fatalf("malicious ID must be regenerated safe, got %q", p0.ID)
	}
	if p0.ID != "evil-cloud" {
		t.Fatalf("regenerated ID must be the name slug, got %q", p0.ID)
	}
	if p0.Color != "#7c6cf0" {
		t.Fatalf("malicious color must reset to default, got %q", p0.Color)
	}
	if got := a.settings.APIKeys["evil-cloud"]; got != "sk-secret" {
		t.Fatalf("API key must migrate to the regenerated ID, got %q", got)
	}
	if a.settings.Providers[1].ID != "safe-id" || a.settings.Providers[1].Color != "#3fb984" {
		t.Fatal("already-safe provider fields must be preserved")
	}
	// شناسه‌های فارسیِ مشروع (خروجی slugify) نباید قربانی پاک‌سازی شوند.
	if a.settings.Providers[2].ID != "ابر-پارسی" {
		t.Fatalf("legitimate Persian ID must survive, got %q", a.settings.Providers[2].ID)
	}
}

// TestSaveProviderRejectsUnsafeFields — مسیر ذخیره‌سازی هم همان گارد را
// دارد (ویرایشِ ساختگی با شناسه/رنگ مخرب رد می‌شود).
func TestSaveProviderRejectsUnsafeFields(t *testing.T) {
	a := newTestApp(t)
	_, err := a.SaveProvider(Provider{
		ID: `x"><img`, Name: "X", BaseURL: "https://x.example/v1", Models: []string{"m"},
	}, "")
	if err == nil || !strings.Contains(err.Error(), "شناسه") {
		t.Fatalf("malicious provider ID must be rejected, got err=%v", err)
	}
	_, err = a.SaveProvider(Provider{
		Name: "Y", BaseURL: "https://y.example/v1", Color: `red" onmouseover="x`, Models: []string{"m"},
	}, "")
	if err == nil || !strings.Contains(err.Error(), "هگز") {
		t.Fatalf("malicious provider color must be rejected, got err=%v", err)
	}
}

// TestConversationIndexSkipsMaliciousIDs — رگرسیون امنیتی: فایل گفتگوی
// کاشته‌شده با شناسه‌ی مخرب نباید به ایندکس (نوار کناری — data-id بدون
// escape) راه پیدا کند.
func TestConversationIndexSkipsMaliciousIDs(t *testing.T) {
	dir := t.TempDir()
	bad := `{"id": "\"><img src=x onerror=alert(1)>", "title": "evil", "messages": []}`
	if err := os.WriteFile(filepath.Join(dir, "evil.json"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	good := `{"id": "20250101-120000-abcd1234", "title": "ok", "messages": []}`
	if err := os.WriteFile(filepath.Join(dir, "good.json"), []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	idx := buildIndexFromFiles(dir)
	if len(idx.Entries) != 1 || idx.Entries[0].ID != "20250101-120000-abcd1234" {
		t.Fatalf("planted malicious conversation ID must be skipped, got %+v", idx.Entries)
	}
}
