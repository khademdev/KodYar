// تست‌های نگهبان سکون استریم (Mission 4 — Stage 2).
//
// سرورهای httptest بدون بستن اتصال «گیر می‌کنند» تا همان نقص واقعی
// بازتولید شود: سرویس‌دهنده هدرها را می‌فرستد و بعد هیچ داده‌ای نمی‌آید.
// مهلت‌ها تزریقی‌اند (۱ ثانیه) تا تست‌ها سریع و قطعی بمانند.
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// withFastStreamIdle مهلت بیکاری را برای تست کوچک و تزریقی می‌کند.
func withFastStreamIdle(t *testing.T, idleSec int) {
	t.Helper()
	oldDef, oldMin, oldMax, oldPoll := streamIdleDefaultSec, streamIdleMinSec, streamIdleMaxSec, streamIdlePollEvery
	streamIdleDefaultSec, streamIdleMinSec, streamIdleMaxSec = idleSec, idleSec, idleSec+1
	streamIdlePollEvery = 25 * time.Millisecond
	t.Cleanup(func() {
		streamIdleDefaultSec, streamIdleMinSec, streamIdleMaxSec, streamIdlePollEvery = oldDef, oldMin, oldMax, oldPoll
	})
}

// withInstantRetry backoff的真实ی را حذف می‌کند (تست بی‌درنگ).
func withInstantRetry(t *testing.T) {
	t.Helper()
	oldSleep, oldBase, oldJitter := retrySleepFn, retryBaseDelay, retryJitterRand
	retrySleepFn = func(context.Context, time.Duration) error { return nil }
	retryBaseDelay = 0
	retryJitterRand = func() float64 { return 0 }
	t.Cleanup(func() { retrySleepFn, retryBaseDelay, retryJitterRand = oldSleep, oldBase, oldJitter })
}

// waitGenerationDone منتظر پایان پاسخ‌دهی می‌ماند (بدون خواب طولانی).
func waitGenerationDone(t *testing.T, a *App, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		a.genMu.Lock()
		g := a.gen
		a.genMu.Unlock()
		if g == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("generation did not finish before the deadline")
}

// assistantMessages کپی پیام‌های دستیار گفتگو را برمی‌گرداند.
func assistantMessages(t *testing.T, a *App, convID string) []Message {
	t.Helper()
	a.mu.Lock()
	conv, err := a.loadConversation(convID)
	a.mu.Unlock()
	if err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	var out []Message
	for _, m := range conv.Messages {
		if m.Role == "assistant" {
			out = append(out, m)
		}
	}
	return out
}

// streamTestApp مثل chatAppOnServer ولی پوشه‌های موقت را با پاک‌سازی
// خطا-نادیده می‌سازد: آنتی‌ویروس/ایندکسر ویندوز فایل گفتگو را لحظه‌ای باز
// نگه می‌دارد و t.TempDir در پاک‌سازی «directory is not empty» می‌دهد.
func streamTestApp(t *testing.T, srvURL string) *App {
	t.Helper()
	cfgDir, err := os.MkdirTemp("", "myai-stream-")
	if err != nil {
		t.Fatalf("config dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cfgDir) })
	wsDir, err := os.MkdirTemp("", "myai-stream-ws-")
	if err != nil {
		t.Fatalf("workspace dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(wsDir) })
	t.Setenv("MYAI_CONFIG_DIR", cfgDir)

	a := NewApp()
	a.settings = defaultSettings()
	a.settings.Providers = []Provider{{
		ID: "ollama", Name: "Ollama", BaseURL: srvURL,
		NoKey: true, ContextWindow: 128000, Models: []string{"llama3.2"},
	}}
	a.settings.Provider = "ollama"
	a.settings.Model = "llama3.2"
	a.settings.WorkspacePath = wsDir
	a.settings.AllowCommands = true
	a.settings.ConfirmFileWrites = false
	a.initChangesStore()
	return a
}

// blockingSSE هدرهای استریم را می‌فرستد و تا لغو، هیچ داده‌ای نمی‌فرستد.
func blockingSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	<-r.Context().Done()
}

// stallAfterDelta یک دلتای متن می‌فرستد و بعد ساکت می‌ماند (اتصال باز).
func stallAfterDelta(w http.ResponseWriter, r *http.Request, delta string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", delta)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	<-r.Context().Done()
}

// TestStreamStallBeforeFirstByteRetries — سکون پیش از هر خروجی باید خطای
// موقتِ قابل‌تلاش‌مجدد باشد: تلاش دوم پاسخ کامل می‌دهد.
func TestStreamStallBeforeFirstByteRetries(t *testing.T) {
	withFastStreamIdle(t, 1)
	withInstantRetry(t)
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&reqs, 1) == 1 {
			blockingSSE(w, r)
			return
		}
		sseOK(w, "پاسخ تلاش دوم")
	}))
	defer srv.Close()

	a := streamTestApp(t, srv.URL)
	conv, err := a.SendMessage("", "سلام", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitGenerationDone(t, a, 15*time.Second)

	if n := atomic.LoadInt32(&reqs); n != 2 {
		t.Fatalf("stall before output must be retried exactly once, requests=%d", n)
	}
	msgs := assistantMessages(t, a, conv.ID)
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].Content, "پاسخ تلاش دوم") {
		t.Fatalf("retry must deliver the full answer, got %+v", msgs)
	}
}

// TestStreamStallMidStreamSavesPartialNoRetry — پس از خروجی، تلاش مجدد ممنوع
// است (تکرار اثر جانبی)؛ پاسخ نیمه‌کاره با Aborted ذخیره می‌شود و «ادامه»
// ممکن می‌ماند.
func TestStreamStallMidStreamSavesPartialNoRetry(t *testing.T) {
	withFastStreamIdle(t, 1)
	withInstantRetry(t)
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqs, 1)
		stallAfterDelta(w, r, "پاسخ نیمه")
	}))
	defer srv.Close()

	a := streamTestApp(t, srv.URL)
	logPath := redirectToTestLog(t)
	conv, err := a.SendMessage("", "یک پاسخ بلند بده", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitGenerationDone(t, a, 15*time.Second)

	if n := atomic.LoadInt32(&reqs); n != 1 {
		t.Fatalf("mid-stream stall must NOT auto-retry, requests=%d", n)
	}
	msgs := assistantMessages(t, a, conv.ID)
	if len(msgs) == 0 {
		t.Fatal("partial answer must be persisted")
	}
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "پاسخ نیمه") || !last.Aborted {
		t.Fatalf("partial answer must be saved as aborted: %+v", last)
	}
	waitErrorLogContains(t, logPath, "دریافت پاسخ متوقف شد (بدون داده برای ۱ ثانیه). می‌توانید «ادامه» را بزنید.")
}

// TestStreamKeepAlivePreventsWatchdog — کامنت‌های keep-alive هم فعالیت‌اند و
// نباید باعث لغو شوند.
func TestStreamKeepAlivePreventsWatchdog(t *testing.T) {
	withFastStreamIdle(t, 1)
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqs, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for i := 0; i < 5; i++ { // ~۱.۲ ثانیه با مهلت ۱ ثانیه
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(240 * time.Millisecond)
		}
		sseOK(w, "پاسخ کامل")
	}))
	defer srv.Close()

	a := streamTestApp(t, srv.URL)
	conv, err := a.SendMessage("", "سلام", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitGenerationDone(t, a, 20*time.Second)

	if n := atomic.LoadInt32(&reqs); n != 1 {
		t.Fatalf("keep-alive comments must count as activity (no retry), requests=%d", n)
	}
	msgs := assistantMessages(t, a, conv.ID)
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].Content, "پاسخ کامل") {
		t.Fatalf("stream with keep-alives must finish normally, got %+v", msgs)
	}
}

// TestStreamStallDoesNotOverrideUserStop — توقف کاربر فوری است و هرگز
// «سکون» گزارش نمی‌شود.
func TestStreamStallDoesNotOverrideUserStop(t *testing.T) {
	withFastStreamIdle(t, 3) // مهلت بزرگ‌تر از زمان توقف کاربر
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stallAfterDelta(w, r, "شروع پاسخ")
	}))
	defer srv.Close()

	a := streamTestApp(t, srv.URL)
	logPath := redirectToTestLog(t)
	if _, err := a.SendMessage("", "سلام", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	a.StopGeneration()
	waitGenerationDone(t, a, 5*time.Second)

	if data, err := os.ReadFile(logPath); err == nil && strings.Contains(string(data), "بدون داده برای") {
		t.Fatalf("user stop must not be reported as a stall:\n%s", string(data))
	}
}

// TestSubagentStallReturnsErrorReport — سکون زیرایجنت گزارش خطا به والد
// می‌دهد (بدون hang).
func TestSubagentStallReturnsErrorReport(t *testing.T) {
	withFastStreamIdle(t, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		blockingSSE(w, r)
	}))
	defer srv.Close()

	a := streamTestApp(t, srv.URL)
	report, ok := a.runSubagent(context.Background(), "explore the workspace")
	if ok {
		t.Fatalf("stalled sub-agent must not report success: %q", report)
	}
	if !strings.Contains(report, "stalled") {
		t.Fatalf("sub-agent stall must produce a clear error report, got %q", report)
	}
}

// TestStreamIdleClampAndDefault — پیش‌فرض ۱۲۰ و بازه‌ی مجاز ۳۰..۹۰۰.
func TestStreamIdleClampAndDefault(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 120}, {-5, 120}, {5, 30}, {30, 30}, {120, 120}, {901, 900}, {100000, 900},
	}
	for _, c := range cases {
		if got := clampStreamIdleSeconds(c.in); got != c.want {
			t.Fatalf("clamp(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestStreamWatchdogNoGoroutineLeak — پس از استریم‌های معمولی و سکون،
// گوروتین‌ها به حالت پایه برمی‌گردند.
func TestStreamWatchdogNoGoroutineLeak(t *testing.T) {
	withFastStreamIdle(t, 1)
	withInstantRetry(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseOK(w, "پاسخ")
	}))
	defer srv.Close()

	a := streamTestApp(t, srv.URL)
	// گرم‌کردن: گوروتین‌های سرور آزمایشی و استخر اتصال keep-alive پیش از
	// اندازه‌گیری پایه ساخته می‌شوند تا رشد آن‌ها با نشت اشتباه نشود.
	if _, err := a.SendMessage("", "سلام", nil); err != nil {
		t.Fatal(err)
	}
	waitGenerationDone(t, a, 15*time.Second)
	time.Sleep(100 * time.Millisecond)
	before := runtime.NumGoroutine()
	for i := 0; i < 2; i++ {
		if _, err := a.SendMessage("", "سلام", nil); err != nil {
			t.Fatal(err)
		}
		waitGenerationDone(t, a, 15*time.Second)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && runtime.NumGoroutine() > before+3 {
		time.Sleep(25 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+3 {
		t.Fatalf("watchdog leak: before=%d after=%d", before, n)
	}
}
