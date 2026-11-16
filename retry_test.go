// تست‌های تلاش مجدد با backoff برای خطاهای موقت سرویس‌دهنده.
//
// همه‌ی انتظارها تزریقی‌اند: تأخیرها بدون خواب واقعی ثبت و بررسی می‌شوند و
// هیچ خواب ثابتِ مهمی بیش از ~۳۰۰ms وجود ندارد (حلقه‌های انتظار رخدادها
// ۵–۱۰ms کوتاه‌اند و مهلت دفاعی تست ۵ ثانیه است که در مسیر عادی رخ نمی‌دهد).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// setRetryForTest مقادیر تزریقی تلاش مجدد را برای این تست بازنویسی و در
// پایان برمی‌گرداند. خواب واقعی حذف می‌شود و تأخیر محاسبه‌شده در slept ثبت
// می‌شود تا تست فوراً بگذرد.
func setRetryForTest(t *testing.T) *[]time.Duration {
	t.Helper()
	olds := struct {
		maxAt  int
		base   time.Duration
		max    time.Duration
		capRA  time.Duration
		rand   func() float64
		sleepf func(context.Context, time.Duration) error
	}{retryMaxAttempts, retryBaseDelay, retryMaxDelay, retryAfterCap, retryJitterRand, retrySleepFn}

	slept := &[]time.Duration{}
	retrySleepFn = func(ctx context.Context, d time.Duration) error {
		*slept = append(*slept, d)
		return ctx.Err() // اگر لغو شده باشد، همان‌جا خارج می‌شود
	}
	t.Cleanup(func() {
		retryMaxAttempts, retryBaseDelay, retryMaxDelay = olds.maxAt, olds.base, olds.max
		retryAfterCap, retryJitterRand, retrySleepFn = olds.capRA, olds.rand, olds.sleepf
	})
	return slept
}

// redirectToTestLog مسیر errors.log را روی پوشه‌ی config همین تست محکم
// می‌کند (کش سراسری errLogPath ممکن است به پوشه‌ی یک تست قبلی اشاره کند).
func redirectToTestLog(t *testing.T) string {
	t.Helper()
	old := errLogPath
	p := filepath.Join(os.Getenv("MYAI_CONFIG_DIR"), "errors.log")
	errLogMu.Lock()
	errLogPath = p
	errLogMu.Unlock()
	t.Cleanup(func() {
		errLogMu.Lock()
		errLogPath = old
		errLogMu.Unlock()
	})
	return p
}

// waitErrorLogContains تا رسیدن متن مورد انتظار به errors.log صبر می‌کند
// (chat:error در محیط تست emit نمی‌شود؛ این رد اثباتی پیام فارسی است).
func waitErrorLogContains(t *testing.T, path, sub string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), sub) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("errors.log never contained %q", sub)
}

// waitConversationUntil تا برقرار شدن pred روی گفتگو صبر می‌کند (قفل گیرنده
// در هر پاس فقط برای خواندنِ لحظه‌ای بسته می‌شود).
func waitConversationUntil(t *testing.T, a *App, convID string, pred func(c *Conversation) bool) *Conversation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		conv, err := a.loadConversation(convID)
		a.mu.Unlock()
		if err == nil && conv != nil && pred(conv) {
			return conv
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("conversation never reached the expected state")
	return nil
}

func hasAssistantWith(content string) func(*Conversation) bool {
	return func(c *Conversation) bool {
		for _, m := range c.Messages {
			if m.Role == "assistant" && strings.Contains(m.Content, content) {
				return true
			}
		}
		return false
	}
}

// waitStreamDone خروج «کامل» گوروتین استریم صادرشده را صبر می‌کند.
// پاک‌شدن a.gen پیش از اولین دیسک‌نویسی پایان است؛ g.done آخرین defer است
// (پس از آخرین ذخیره) — پس وصل‌شدن به آن تضمین می‌کند تست‌های TempDir با
// دیسک‌نویسیِ دیرهنگام تداخل نداشته باشند.
func waitStreamDone(t *testing.T, a *App) {
	t.Helper()
	// SendMessage پیش از ثبت دسته برمی‌گردد؛ اگر بلافاصله g را nil ببینیم
	// و برگردیم، تست تمام می‌شود و گوروتین استریم بعداً در پوشه‌ی موقت
	// (که t.TempDir دارد پاک می‌شود) می‌نویسد → «directory is not empty».
	// پس کمی برای ثبت دسته صبر می‌کنیم، بعد تا پایانش منتظر می‌مانیم.
	grabDeadline := time.Now().Add(500 * time.Millisecond)
	for {
		a.genMu.Lock()
		g := a.gen
		a.genMu.Unlock()
		if g != nil {
			select {
			case <-g.done:
			// سناریوهای e2e داخل نوبت واقعاً `go test ./...` اجرا می‌کنند؛ با
			// کش سرد یا دیسک کند این بیش از ۵ ثانیه طول می‌کشد و تست سالم را
			// کاذب fail می‌کرد. این سقف فقط «هنگ نکردن» را می‌سنجد، نه سرعت.
			case <-time.After(30 * time.Second):
				t.Fatal("stream goroutine did not exit in time")
			}
			return
		}
		if time.Now().After(grabDeadline) {
			return // واقعاً هیچ استریمی شروع نشده
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// sseOK یک پاسخ استریم ساده با یک دلتا و [DONE] می‌فرستد.
func sseOK(w http.ResponseWriter, delta string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", delta)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// chatAppOnServer یک App آزمایشی که Provider آن به سرور داده‌شده اشاره می‌کند.
func chatAppOnServer(t *testing.T, srvURL string) *App {
	t.Helper()
	a := newTestApp(t)
	a.mu.Lock()
	a.settings.Providers[0].BaseURL = srvURL
	a.mu.Unlock()
	return a
}

// ---------------------------------------------------------------- واحدی

func TestIsRetryableStatus(t *testing.T) {
	for _, code := range []int{408, 429, 500, 502, 503, 504, 529} {
		if !isRetryableStatus(code) {
			t.Fatalf("status %d must be retryable", code)
		}
	}
	for _, code := range []int{200, 400, 401, 403, 404, 422, 490, 499} {
		if isRetryableStatus(code) {
			t.Fatalf("status %d must NOT be retryable", code)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Unix(1700000000, 0)
	if d, ok := parseRetryAfter("12", now); !ok || d != 12*time.Second {
		t.Fatalf("seconds: %v %v", d, ok)
	}
	if d, ok := parseRetryAfter("0.5", now); !ok || d != 500*time.Millisecond {
		t.Fatalf("fractional: %v %v", d, ok)
	}
	// HTTP-date در آینده
	future := now.Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if d, ok := parseRetryAfter(future, now); !ok || d < 29*time.Second || d > 31*time.Second {
		t.Fatalf("http-date: %v %v", d, ok)
	}
	for _, bad := range []string{"", "-3", "not-a-date", now.UTC().Format(http.TimeFormat)} {
		if _, ok := parseRetryAfter(bad, now); ok {
			t.Fatalf("%q must be invalid", bad)
		}
	}
}

func TestJitterDelayBoundsAndCap(t *testing.T) {
	oldRand := retryJitterRand
	defer func() { retryJitterRand = oldRand }()

	// حداکثرِ تابع تصادف → بازه‌ی کامل (۰ تا min(cap, base·2^n))
	retryJitterRand = func() float64 { return 0.999999 }
	wantMax := []time.Duration{1, 2, 4, 8}
	for n, max := range wantMax {
		d := jitterDelay(n)
		if d < 0 || d > time.Duration(max)*retryBaseDelay {
			t.Fatalf("attempt %d delay %v exceeds %v", n, d, max)
		}
	}
	if d := jitterDelay(4); d <= 15*retryBaseDelay || d > 16*retryBaseDelay {
		t.Fatalf("attempt 4 full jitter must be within (15s, 16s], got %v", d)
	}
	if d := jitterDelay(10); d > retryMaxDelay {
		t.Fatalf("huge attempt delay %v exceeds cap %v", d, retryMaxDelay)
	}
	// حداقلِ تابع تصادف → صفر (full jitter)
	retryJitterRand = func() float64 { return 0 }
	if d := jitterDelay(3); d != 0 {
		t.Fatalf("full jitter must allow 0, got %v", d)
	}
}
func TestRetryDelayPrefersRetryAfterAndCaps(t *testing.T) {
	oldRand, oldCap := retryJitterRand, retryAfterCap
	defer func() { retryJitterRand, retryAfterCap = oldRand, oldCap }()
	retryJitterRand = func() float64 { return 0.5 }

	now := time.Unix(1700000000, 0)
	if d := retryDelay(0, "7", now); d != 7*time.Second {
		t.Fatalf("honor Retry-After 7s: got %v", d)
	}
	if d := retryDelay(0, "3600", now); d != retryAfterCap {
		t.Fatalf("Retry-After must be hard-capped at %v, got %v", retryAfterCap, d)
	}
	d := retryDelay(1, "", now)
	if d < 0 || d > 2*retryBaseDelay {
		t.Fatalf("no Retry-After → full jitter in [0, 1·2^1]: got %v", d)
	}
}

func TestFaDigitsAndNoticeText(t *testing.T) {
	if got := faDigits(42); got != "۴۲" {
		t.Fatalf("faDigits: %q", got)
	}
	if got := faDigits(503); got != "۵۰۳" {
		t.Fatalf("faDigits: %q", got)
	}

	rec := retryRecord{status: 503, nextAttempt: 2}
	txt := rec.noticeText()
	for _, want := range []string{"خطای موقت سرویس‌دهنده", "(HTTP ۵۰۳)", "تلاش مجدد ۲ از ۴", "…"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("notice %q missing %q", txt, want)
		}
	}
	net := retryRecord{network: true, nextAttempt: 3}
	txt = net.noticeText()
	if !strings.Contains(txt, "اتصال به سرویس‌دهنده برقرار نشد") || !strings.Contains(txt, "تلاش مجدد ۳ از ۴") {
		t.Fatalf("network notice: %q", txt)
	}
	// پیام فارسی با ارقام فارسی باید UTF-8 معتبر باشد (برش/گردش نباید خراب کند)
	if !utf8.ValidString(txt) {
		t.Fatal("Persian notice must be valid UTF-8")
	}
}

func TestDefaultRetrySleepStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := defaultRetrySleep(ctx, 10*time.Second); err == nil {
		t.Fatal("cancelled wait must return error")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("cancel must exit immediately, took %v", elapsed)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	if err := defaultRetrySleep(ctx2, 5*time.Millisecond); err != nil {
		t.Fatalf("normal wait must succeed: %v", err)
	}
}

// ---------------------------------------------------------------- استریم

func TestStreamChatRetries503ThenSucceeds(t *testing.T) {
	slept := setRetryForTest(t)
	logPath := redirectToTestLog(t)

	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&reqs, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"error":{"message":"try again"}}`)
			return
		}
		sseOK(w, "سلام؛ با تلاش مجدد شنیده شد")
	}))
	defer srv.Close()

	a := chatAppOnServer(t, srv.URL)
	conv, err := a.SendMessage("", "پیام آزمایشی", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)

	// دقیقاً ۲ درخواست (۵۰۳ + تلاش مجدد) و یک انتظارِ backoff
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&reqs) < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&reqs); got != 2 {
		t.Fatalf("want exactly 2 requests (503 + retry), got %d", got)
	}
	if len(*slept) != 1 || (*slept)[0] > retryBaseDelay {
		t.Fatalf("expected one full-jitter wait ≤ %v, slept=%v", retryBaseDelay, *slept)
	}

	waitConversationUntil(t, a, conv.ID, hasAssistantWith("سلام؛ با تلاش مجدد شنیده شد"))
	data, _ := os.ReadFile(logPath)
	if strings.Contains(string(data), "HTTP 503") {
		t.Fatalf("503 must be retried away, log: %s", data)
	}
}

func TestStreamChat429HonorsRetryAfter(t *testing.T) {
	slept := setRetryForTest(t)
	redirectToTestLog(t)

	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&reqs, 1) == 1 {
			w.Header().Set("Retry-After", "12")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		sseOK(w, "پاسخ نهایی")
	}))
	defer srv.Close()

	a := chatAppOnServer(t, srv.URL)
	conv, err := a.SendMessage("", "آزمون 429", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&reqs) < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&reqs); got != 2 {
		t.Fatalf("want 2 requests, got %d", got)
	}
	if len(*slept) != 1 || (*slept)[0] != 12*time.Second {
		t.Fatalf("Retry-After must be honored exactly as 12s; slept=%v", *slept)
	}
	waitConversationUntil(t, a, conv.ID, hasAssistantWith("پاسخ نهایی"))
}

func TestStreamChatNoRetryOn401(t *testing.T) {
	setRetryForTest(t)

	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqs, 1)
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"invalid key"}}`)
	}))
	defer srv.Close()

	a := chatAppOnServer(t, srv.URL)
	logPath := redirectToTestLog(t)
	conv, err := a.SendMessage("", "کلید خراب", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)
	// ۴۰۱ قابل تلاش مجدد نیست — همان مسیر خطای موجود؛ کمی صبر تا اطمینان.
	waitErrorLogContains(t, logPath, "HTTP 401")
	_ = conv
	// اطمینان نهایی: احتمال هیچ درخواستی بیش از یک‌بار نمی‌رود
	time.Sleep(200 * time.Millisecond)
	if got := atomic.LoadInt32(&reqs); got != 1 {
		t.Fatalf("401 must NOT be retried, got %d requests", got)
	}
	a.mu.Lock()
	convLoaded, lerr := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if lerr == nil {
		// قرارداد جدید: خطا به‌صورت پیام ماندگار ذخیره می‌شود، ولی هیچ
		// پاسخ مدلی نباید ساخته شود؛ تنها پیام دستیار، همان اعلان خطا با
		// کد ۴۰۱ است.
		found := false
		for _, m := range convLoaded.Messages {
			if m.Role != "assistant" {
				continue
			}
			if !strings.Contains(m.Content, "401") {
				t.Fatalf("401 must not produce model content: %q", m.Content)
			}
			found = true
		}
		if !found {
			t.Fatal("401 must be persisted as a visible error message")
		}
	}
}

func TestStreamChatAttemptsExhaustedPersianError(t *testing.T) {
	slept := setRetryForTest(t)

	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqs, 1)
		w.WriteHeader(529)
		io.WriteString(w, "upstream overloaded")
	}))
	defer srv.Close()

	a := chatAppOnServer(t, srv.URL)
	logPath := redirectToTestLog(t)
	conv, err := a.SendMessage("", "فشار سرویس", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&reqs) < int32(retryMaxAttempts) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&reqs); got != int32(retryMaxAttempts) {
		t.Fatalf("want exactly %d attempts, got %d", retryMaxAttempts, got)
	}
	if len(*slept) != retryMaxAttempts-1 {
		t.Fatalf("want %d backoff waits, got %d (%v)", retryMaxAttempts-1, len(*slept), *slept)
	}
	// متن پیام از ۱.۵ کوتاه/فارسی شد (httperror.go)؛ همان قراردادها می‌ماند:
	// تعداد تلاش + کد وضعیت ASCII (قابل جست‌وجو در لاگ) + جزئیات سرویس‌دهنده.
	waitErrorLogContains(t, logPath, "پس از ۴ تلاش")
	waitErrorLogContains(t, logPath, "HTTP 529")
	waitErrorLogContains(t, logPath, "upstream overloaded")
	_ = conv
}

func TestStreamChatCancelDuringRetryWait(t *testing.T) {
	oldSleep := retrySleepFn
	oldRand := retryJitterRand
	defer func() { retrySleepFn, retryJitterRand = oldSleep, oldRand }()
	retryJitterRand = func() float64 { return 0 }

	started := make(chan struct{})
	var once int32
	retrySleepFn = func(ctx context.Context, d time.Duration) error {
		if atomic.CompareAndSwapInt32(&once, 0, 1) {
			close(started)
		}
		<-ctx.Done()
		return ctx.Err()
	}

	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqs, 1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	a := chatAppOnServer(t, srv.URL)
	if _, err := a.SendMessage("", "لغو در حین انتظار", nil); err != nil {
		t.Fatal(err)
	}
	a.genMu.Lock()
	gen := a.gen
	a.genMu.Unlock()

	// تا رسیدن به نخستین انتظار صبر می‌کنیم...
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("retry wait never started")
	}
	//...سپس توقف کاربر: انتظار باید بلافاصله قطع شود و گوروتین کامل بیرون بیاید
	if gen == nil {
		t.Fatal("generation already gone before stop")
	}
	a.StopGeneration()
	select {
	case <-gen.done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream goroutine did not exit after StopGeneration")
	}
	// فقط یک درخواست؛ بدون تلاش مجدد پس از لغو
	if got := atomic.LoadInt32(&reqs); got != 1 {
		t.Fatalf("only one attempt before cancel, got %d", got)
	}
}

func TestStreamChatNoRetryAfterFirstDelta(t *testing.T) {
	slept := setRetryForTest(t)

	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqs, 1)
		sseOK(w, "تکه‌ی نخست فارسی")
		panic(http.ErrAbortHandler) // قطعِ وسط استریم، بعد از فرستادن دلتا
	}))
	defer srv.Close()

	a := chatAppOnServer(t, srv.URL)
	logPath := redirectToTestLog(t)
	conv, err := a.SendMessage("", "شکست وسط استریم", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&reqs) < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // فرصتِ تلاش مجدد «اگر» اشتباهاً انجام می‌شد
	if got := atomic.LoadInt32(&reqs); got != 1 {
		t.Fatalf("mid-stream break must never reconnect, got %d requests", got)
	}
	if len(*slept) != 0 {
		t.Fatalf("no retry waits may happen after first delta; slept=%v", *slept)
	}
	waitErrorLogContains(t, logPath, "جریان پاسخ قطع شد")
	waitConversationUntil(t, a, conv.ID, func(c *Conversation) bool {
		for _, m := range c.Messages {
			if m.Role == "assistant" && m.Aborted && strings.Contains(m.Content, "تکه‌ی نخست فارسی") {
				return true
			}
		}
		return false
	})
}

// ---------------------------------------------------------------- دسته‌بندی خطای شبکه

// roundTripFunc RoundTripper تزریقی برای شبیه‌سازی خطاهای شبکه.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// swapChatClientForTest کلاینت HTTP استریم را موقتاً با Transport تزریقی
// عوض می‌کند (تست‌های این پکیج ترتیبی‌اند و منتظر خروج گوروتین می‌مانند).
func swapChatClientForTest(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	old := chatHTTPClient
	chatHTTPClient = &http.Client{Transport: rt}
	t.Cleanup(func() { chatHTTPClient = old })
}

// mkTestReq سازنده‌ی درخواست آزمایشی برای dialStream.
func mkTestReq() (*http.Request, error) {
	return http.NewRequestWithContext(context.Background(), http.MethodPost, "http://testserver/v1/chat/completions", strings.NewReader("{}"))
}

func TestClassifyNetError(t *testing.T) {
	cases := []struct {
		err  error
		want netErrClass
	}{
		// DNS
		{&net.DNSError{Err: "no such host", Name: "api.example.com", IsNotFound: true}, netErrDNS},
		{fmt.Errorf("Post: %w", &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}), netErrDNS},
		{errors.New("dial tcp: lookup api.example.com: no such host"), netErrDNS},
		// TLS/گواهی
		{errors.New("x509: certificate signed by unknown authority"), netErrFatal},
		{errors.New("tls: failed to verify certificate: x509: certificate signed by unknown authority"), netErrFatal},
		{errors.New("x509: certificate has expired or is not yet valid"), netErrFatal},
		// آدرس نامعتبر
		{&url.Error{Op: "Do", URL: "ftp://x", Err: errors.New(`unsupported protocol scheme "ftp"`)}, netErrFatal},
		{errors.New("http: no Host in request URL"), netErrFatal},
		// لغو
		{context.Canceled, netErrFatal},
		{&url.Error{Op: "Do", URL: "http://x", Err: context.Canceled}, netErrFatal},
		// موقت‌ها — سیاست عادی
		{errors.New("net/http: TLS handshake timeout"), netErrRetryable},
		{errors.New("read tcp 1.2.3.4:443->5.6.7.8:1: read: connection reset by peer"), netErrRetryable},
		{errors.New("EOF"), netErrRetryable},
		{errors.New("dial tcp 1.2.3.4:443: connect: connection refused"), netErrRetryable},
		{&url.Error{Op: "Do", URL: "http://x", Err: os.ErrDeadlineExceeded}, netErrRetryable},
		{&net.DNSError{Err: "server misbehaving", Name: "x", IsTimeout: true}, netErrRetryable}, // DNS ولی نه not-found
		{nil, netErrRetryable},
	}
	for _, c := range cases {
		if got := classifyNetError(c.err); got != c.want {
			t.Fatalf("classifyNetError(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

// TestDialStreamNoRetryOnTLS خطای گواهی TLS → هیچ تلاش مجددی نمی‌شود و
// پیام نهایی فارسی و روشن است.
func TestDialStreamNoRetryOnTLS(t *testing.T) {
	slept := setRetryForTest(t)
	var calls int32
	swapChatClientForTest(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("tls: failed to verify certificate: x509: certificate signed by unknown authority")
	}))
	a := newTestApp(t)

	_, err := a.dialStream(context.Background(), mkTestReq, nil)
	if err == nil {
		t.Fatal("must fail")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("TLS verification error must NOT be retried, got %d attempts", got)
	}
	if len(*slept) != 0 {
		t.Fatalf("no retry sleep expected, got %v", *slept)
	}
	for _, want := range []string{"گواهی TLS", "تلاش مجدد انجام نشد"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Persian final error must contain %q: %v", want, err)
		}
	}
}

// TestDialStreamNoRetryOnBadURL آدرس نامعتبر (scheme پشتیبانی‌نشده) → بدون
// تلاش مجدد، با پیام فارسی «Base URL».
func TestDialStreamNoRetryOnBadURL(t *testing.T) {
	slept := setRetryForTest(t)
	var calls int32
	swapChatClientForTest(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, &url.Error{Op: "Do", URL: "ftp://x", Err: errors.New(`unsupported protocol scheme "ftp"`)}
	}))
	a := newTestApp(t)

	_, err := a.dialStream(context.Background(), mkTestReq, nil)
	if err == nil {
		t.Fatal("must fail")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("unsupported scheme must NOT be retried, got %d attempts", got)
	}
	if len(*slept) != 0 {
		t.Fatalf("no retry sleep expected, got %v", *slept)
	}
	if !strings.Contains(err.Error(), "Base URL") {
		t.Fatalf("error must point at the Base URL setting: %v", err)
	}
}

// TestDialStreamDNSRetriedAtMostOnce «no such host» → حداکثر یک تلاش مجدد
// (۲ تلاش کل) و اعلان فارسی تعداد را درست نشان می‌دهد.
func TestDialStreamDNSRetriedAtMostOnce(t *testing.T) {
	slept := setRetryForTest(t)
	var notices []string
	var calls int32
	swapChatClientForTest(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, &net.DNSError{Err: "no such host", Name: "api.example.com", IsNotFound: true}
	}))
	a := newTestApp(t)

	_, err := a.dialStream(context.Background(), mkTestReq, func(rec retryRecord) {
		notices = append(notices, rec.noticeText())
	})
	if err == nil {
		t.Fatal("must fail")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("DNS may be retried at most once (2 total attempts), got %d", got)
	}
	if len(*slept) != 1 {
		t.Fatalf("exactly one retry wait expected, got %v", *slept)
	}
	if !strings.Contains(err.Error(), "۲") {
		t.Fatalf("Persian error must report the attempt count: %v", err)
	}
	// اعلان تلاش مجدد هم «از ۲» می‌گوید نه «از ۴»
	if len(notices) != 1 || !strings.Contains(notices[0], "از ۲") {
		t.Fatalf("retry notice must reflect the DNS attempt cap: %v", notices)
	}
}

// TestDialStreamRefusedKeepsFullPolicy خطای موقت عادی (refused) → سیاست
// کامل همان قبل (۴ تلاش).
func TestDialStreamRefusedKeepsFullPolicy(t *testing.T) {
	slept := setRetryForTest(t)
	var calls int32
	swapChatClientForTest(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("dial tcp 1.2.3.4:443: connect: connection refused")
	}))
	a := newTestApp(t)

	_, err := a.dialStream(context.Background(), mkTestReq, nil)
	if err == nil {
		t.Fatal("must fail")
	}
	if got := atomic.LoadInt32(&calls); got != int32(retryMaxAttempts) {
		t.Fatalf("transient errors must keep the full retry policy (%d), got %d", retryMaxAttempts, got)
	}
	if len(*slept) != retryMaxAttempts-1 {
		t.Fatalf("expected %d waits, got %v", retryMaxAttempts-1, *slept)
	}
	if !strings.Contains(err.Error(), "۴") {
		t.Fatalf("Persian error must report the full attempt count: %v", err)
	}
}
