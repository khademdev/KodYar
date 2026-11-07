// تلاش مجدد با backoff برای خطاهای موقت سرویس‌دهنده.
//
// فقط «فاز اتصال» (قبل از خواندن نخستین بایت SSE) تلاش مجدد می‌شود؛ شکستِ
// میانه‌ی استریم رفتار قبلی خودش را دارد (savePartial + پیام خطا). مسیر
// اتصال در chat.go با dialStream جایگزین شده است.
//
// تزریقی بودن: ساز و کار تعیین تأخیر (jitter) و خواب (انتظار با لغو) با
// متغیرهای package-level بازنویسی‌پذیرند تا تست‌ها بی‌درنگ اجرا شوند.
package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// تنظیمات تلاش مجدد — در تست‌ها موقتاً بازنویسی می‌شوند (باید در
// پایان تست به مقدار اصلی برگردند؛ به کدهای runtime دست نزنید).
var (
	retryMaxAttempts = 4                 // حداکثر تلاش‌ها (شامل تلاش اول)
	retryBaseDelay   = 1 * time.Second   // پایه‌ی backoff نمایی
	retryMaxDelay    = 20 * time.Second  // سقف backoff نمایی (بدون Retry-After)
	retryAfterCap    = 60 * time.Second  // سقف مطلق Retry-After
	retryJitterRand  = rand.Float64      // منبع تصادف برای full jitter (در تست‌ها قطعی می‌شود)
	retrySleepFn     = defaultRetrySleep // خواب هوشمند: timer یا لغو ctx — هر دو با توقف تایمر
)

// defaultRetrySleep به مدت d (یا تا لغو ctx) صبر می‌کند؛ تایمر همیشه متوقف
// می‌شود. بازگشت خطا یعنی ctx لغو شده و منتظرماندگان باید فوراً خارج شوند.
// نظر: d > 0 و از پیش محدودشده است (٦۰ ثانیه)؛ نیازی به Safety cap دیگر نیست.
func defaultRetrySleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isRetryableStatus فقط خطاهای گذرا؛ 400/401/403/404 خطای کاربر/تنظیمات
// هستند و باید همان‌طور که هست با پیام موجود نمایش داده شوند.
func isRetryableStatus(code int) bool {
	switch code {
	case 408, 429, 500, 502, 503, 504, 529:
		return true
	}
	return false
}

// parseRetryAfter مقدار هدر Retry-After را می‌خواند: عدد ثانیه (اعشاری هم
// پذیرفته می‌شود) یا تاریخ HTTP. ok=false یعنی نبود/نامعتبر است.
// «now» تزریقی است تا تست‌ها بی‌درنگ باشند. تاریخ گذشته/صفر → ok=false
// (به‌جای حلقه‌ی داغ، به backoff نمایی برمی‌گردیم).
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		if secs > 0 {
			return time.Duration(secs * float64(time.Second)), true
		}
		return 0, false
	}
	if d, err := http.ParseTime(v); err == nil {
		wait := d.Sub(now)
		if wait <= 0 {
			return 0, false
		}
		return wait, true
	}
	return 0, false
}

// retryDelay تأخیر پیش از تلاش بعدی: ابتدا Retry-After (با سقف مطلق) و در
// نبودش exponential backoff با full jitter (بین 0 و min(cap, base·2^n)).
func retryDelay(attempt int, retryAfter string, now time.Time) time.Duration {
	if d, ok := parseRetryAfter(retryAfter, now); ok {
		if d > retryAfterCap {
			d = retryAfterCap
		}
		return d
	}
	return jitterDelay(attempt)
}

// jitterDelay طول صبر full-jitter برای تلاش مذکور (0 → n یعنی تلاش n+1).
// شماره‌گذاری attempt از صفر: صبر پیش از تلاش دوم = jitterDelay(0).
func jitterDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	m := retryBaseDelay << attempt
	if m <= 0 || m > retryMaxDelay { // سرریز شیفت یا عبور از سقف
		m = retryMaxDelay
	}
	d := time.Duration(retryJitterRand() * float64(m))
	if d < 0 {
		d = 0
	}
	return d
}

// retryRecord یک لحظه‌ی «تلاش ناموفق، منتظر تلاش بعدی» را توصیف می‌کند.
type retryRecord struct {
	network bool // false = پاسخ HTTP قابل‌تلاش مجدد
	status  int
	// nextAttempt شماره‌ی ۱ مبنای تلاش بعدی (اولین درخواست = ۱)
	nextAttempt int
	delay       time.Duration
	// maxAttempts سقف تلاش برای همین خطا (۰ = همان retryMaxAttempts عمومی؛
	// برای خطای DNS که فقط یک تلاش مجدد می‌گیرد مقدار کوچک‌تری می‌گیرد —
	maxAttempts int
}

// noticeText اعلان فارسی برای هر تلاش مجدد — در runtimeEmit به فرانت‌اند
// می‌رود؛ ارقام فارسی‌اند تا با بقیه‌ی UI همسو باشند.
func (r retryRecord) noticeText() string {
	max := r.maxAttempts
	if max <= 0 {
		max = retryMaxAttempts
	}
	head := "خطای موقت سرویس‌دهنده"
	if r.network {
		head = "اتصال به سرویس‌دهنده برقرار نشد"
	} else if r.status > 0 {
		head = fmt.Sprintf("خطای موقت سرویس‌دهنده (HTTP %s)", faDigits(r.status))
	}
	return fmt.Sprintf("%s؛ تلاش مجدد %s از %s …",
		head, faDigits(r.nextAttempt), faDigits(max))
}

// ---------------------------------------------------------------- دسته‌بندی خطای شبکه

// netErrClass سیاست تلاش مجدد برای خطای برگشتی از http.Client.Do.
type netErrClass int

const (
	netErrRetryable netErrClass = iota // سیاست عادی تا retryMaxAttempts (timeout، reset/refused، EOF، موقت‌ها)
	netErrDNS                          // «no such host» — حداکثر یک تلاش مجدد
	netErrFatal                        // هرگز تلاش مجدد نمی‌شود (TLS/آدرس/لغو)
)

// classifyNetError خطای شبکه را دسته‌بندی می‌کند. تشخیص با errors.Is/As روی
// علت + تطبیق متن برای خطاهایی که فقط به‌صورت رشته از پروکسی/فریم‌ورک می‌رسند.
// خطای TLS (گواهی نامعتبر/ناشناس) و آدرس نامعتبر (scheme پشتیبانی‌نشده و…)
// با هر تعداد تلاش مجدد تغییری نمی‌کنند و تلاش مجدد فقط کاربر را منتظر
// می‌گذارد؛ لغو (context.Canceled) هم معنای تلاش مجدد ندارد.
func classifyNetError(err error) netErrClass {
	if err == nil {
		return netErrRetryable
	}
	if errors.Is(err, context.Canceled) {
		return netErrFatal
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return netErrDNS
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "x509:"), // گواهی ناشناس/نامعتبر/منقضی
		strings.Contains(s, "tls: failed to verify"),
		strings.Contains(s, "unsupported protocol scheme"),
		strings.Contains(s, "no host in request url"),
		strings.Contains(s, "invalid control character in url"):
		return netErrFatal
	case strings.Contains(s, "no such host"):
		return netErrDNS // پوشش متنی برای شکل‌های نهفته در wrap
	}
	return netErrRetryable
}

// fatalNetErrorText پیام فارسی روشن برای خطای غیرقابل‌تلاش‌مجدد (رو به کاربر).
// humanizeNetError پیام شبکه را برای کاربر فارسی و قابل‌اقدام می‌کند.
func humanizeNetError(err error) string {
	if err == nil {
		return ""
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "proxyconnect"), strings.Contains(s, "proxyconnect tcp"):
		// پروکسی محیط یا سفارشی در دسترس نیست (مثلاً 127.0.0.1:8080 خاموش)
		return "پروکسی تنظیم‌شده در دسترس نیست. حالت «هوشمند» خودکار تشخیص می‌دهد و اگر پروکسی کار نکند مستقیم وصل می‌شود؛ یا آدرس درست را «سفارشی» وارد کنید"
	case strings.Contains(s, "connection refused"), strings.Contains(s, "actively refused"):
		if strings.Contains(s, "proxy") || strings.Contains(s, "127.0.0.1") || strings.Contains(s, "localhost") {
			return "اتصال به پروکسی رد شد؛ مطمئن شوید نرم‌افزار پروکسی روشن است یا در تنظیمات پروکسی را خاموش کنید"
		}
		return "اتصال به سرویس‌دهنده رد شد؛ آدرس Base URL و شبکه را بررسی کنید"
	case strings.Contains(s, "timeout"), strings.Contains(s, "deadline exceeded"), strings.Contains(s, "i/o timeout"):
		return "زمان اتصال به پایان رسید؛ شبکه، فایروال یا پروکسی را بررسی کنید"
	case strings.Contains(s, "no such host"):
		return "نام میزبان پیدا نشد؛ Base URL یا DNS را بررسی کنید"
	case strings.Contains(s, "x509:"), strings.Contains(s, "tls:"):
		return "خطای گواهی TLS؛ آدرس HTTPS یا شبکه را بررسی کنید"
	}
	return err.Error()
}

func fatalNetErrorText(err error) string {
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "x509:"), strings.Contains(s, "tls: failed to verify"):
		return "اتصال ناموفق: گواهی TLS سرویس‌دهنده معتبر نیست؛ آدرس یا شبکه را بررسی کنید (تلاش مجدد انجام نشد)"
	case strings.Contains(s, "unsupported protocol scheme"),
		strings.Contains(s, "no host in request url"),
		strings.Contains(s, "invalid control character in url"):
		return "آدرس سرویس‌دهنده (Base URL) نامعتبر است؛ آن را در تنظیمات درست کنید (تلاش مجدد انجام نشد)"
	}
	return "اتصال ناموفق بود و تلاش مجدد ممکن نیست"
}

// dialStream فاز اتصال یک دور از استریم را با تلاش مجدد اجرا می‌کند.
//
// mkReq برای «هر» تلاش یک Request تازه می‌سازد (Body مصرف‌شدنی است)؛ onRetry
// پیش از هر انتظار صدا می‌شود (اینجا اعلان فارسی به فرانت‌اند فرستاده می‌شود).
//
// خروجی‌ها:
// - (resp, nil) با resp.StatusCode == 200 → تمیز.
// - (resp, nil) با وضعیت غیر ۲۰۰یِ غیرِقابل‌تلاش (مثل 400/401) → بدنه
// باز است؛ تصمیم «ابزارها پشتیبانی نمی‌شوند» و پیام خطای موجود در caller.
// - (nil, err) → خطای نهایی پس از اتمام تلاش‌ها (پیام فارسی با وضعیت آخر)
// یا لغو ctx در میان انتظار؛ caller با بررسی ctx.Err تصمیم می‌گیرد.
func (a *App) dialStream(ctx context.Context, mkReq func() (*http.Request, error), onRetry func(retryRecord)) (resp *http.Response, err error) {
	for attempt := 0; ; attempt++ {
		req, err := mkReq()
		if err != nil {
			return nil, fmt.Errorf("ساخت درخواست ناموفق بود: %w", err)
		}

		resp, err := chatHTTPClient.Do(req)
		if err != nil {
			// لغو کاربر (Stop) — هرگز تلاش مجدد نمی‌شود.
			if ctx.Err() != nil {
				return nil, err
			}
			// دسته‌بندی خطای شبکه: خطای TLS/آدرس نامعتبر
			// هرگز دوباره تلاش نمی‌شود؛ «no such host» حداکثر یک‌بار.
			cls := classifyNetError(err)
			if cls == netErrFatal {
				return nil, fmt.Errorf("%s: %w", fatalNetErrorText(err), err)
			}
			maxAttempts := retryMaxAttempts
			if cls == netErrDNS {
				maxAttempts = 2
			}
			// خطای شبکه (رفت‌و برگشت ناموفق) — قابل تلاش مجدد.
			if attempt+1 < maxAttempts {
				rec := retryRecord{network: true, nextAttempt: attempt + 2, delay: retryDelay(attempt, "", time.Now()), maxAttempts: maxAttempts}
				if onRetry != nil {
					onRetry(rec)
				}
				if werr := retrySleepFn(ctx, rec.delay); werr != nil {
					return nil, werr // لغو در میان انتظار → مسیر abort در caller
				}
				continue
			}
			return nil, fmt.Errorf("اتصال به سرویس‌دهنده پس از %s تلاش ناموفق بود: %s", faDigits(maxAttempts), humanizeNetError(err))
		}

		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}

		if isRetryableStatus(resp.StatusCode) {
			reason := readErrorBody(resp.Body)
			resp.Body.Close()
			if attempt+1 < retryMaxAttempts {
				rec := retryRecord{nextAttempt: attempt + 2, status: resp.StatusCode,
					delay: retryDelay(attempt, resp.Header.Get("Retry-After"), time.Now())}
				if onRetry != nil {
					onRetry(rec)
				}
				if werr := retrySleepFn(ctx, rec.delay); werr != nil {
					return nil, werr
				}
				continue
			}
			return nil, fmt.Errorf("سرویس‌دهنده پس از %s تلاش خطا برگرداند (HTTP %s): %s",
				faDigits(retryMaxAttempts), faDigits(resp.StatusCode), reason)
		}

		// وضعیت‌های «غیر قابل تلاش مجدد» — همان مسیر قبلی caller.
		return resp, nil
	}
}

// faDigits ارقام ASCII را به ارقام فارسی تبدیل می‌کند (فقط برای اعلان‌های UI).
func faDigits(n int) string {
	s := itoa(n)
	if !strings.ContainsAny(s, "0123456789") {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return '۰' + (r - '0')
		}
		return r
	}, s)
}
