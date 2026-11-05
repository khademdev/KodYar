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
