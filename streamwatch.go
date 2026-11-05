// نگهبان سکون استریم — «فکر کردن بی‌پایان» دیگر ممکن نیست.
//
// کلاینت HTTP فقط مهلت «هدر پاسخ» دارد (۹۰ ثانیه)؛ اگر سرویس‌دهنده یا پروکسی
// وسط استریم داده را قطع کند و اتصال را نبندد، خواندن بدنه تا ابد بلاک
// می‌ماند و تنها دکمه‌ی توقف کاربر آن را پایان می‌داد. این فایل یک نگهبان
// بیکاری دور هر استریم می‌گذارد: هر بایتی که خوانده شود (حتی کامنت
// keep-alive پروتکل) «فعالیت» است؛ فقط اگر در مهلت هیچ بایتی نیاید، درخواست
// لغو می‌شود و پرچم «سکون» می‌خورد تا لغوِ عمدی کاربر با سکون اشتباه نشود.
//
// همه‌ی مقادیر از طریق متغیرهای package-level در تست‌ها قابل تزریق‌اند؛
// goroutine و تایمر همیشه مسیر توقف تضمین‌شده دارند (Stop).
package main

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// پیش‌فرض و مرزهای مهلت بیکاری (ثانیه). تنظیم پنهان: Settings.StreamIdleTimeoutSec
// (۰ = پیش‌فرض). clamp: ۳۰..۹۰۰ ثانیه.
var (
	streamIdleDefaultSec = 120
	streamIdleMinSec     = 30
	streamIdleMaxSec     = 900
	// streamIdlePollEvery فاصله‌ی بررسی نگهبان — در تست‌ها کوچک می‌شود.
	streamIdlePollEvery = time.Second
)

// clampStreamIdleSeconds مقدار خام تنظیمات را به بازه‌ی مجاز می‌آورد.
func clampStreamIdleSeconds(v int) int {
	if v <= 0 {
		v = streamIdleDefaultSec
	}
	if v < streamIdleMinSec {
		v = streamIdleMinSec
	}
	if v > streamIdleMaxSec {
		v = streamIdleMaxSec
	}
	return v
}

// streamIdleGuard نگهبان یک استریم. بدنه را با Body بپیچید و ctx برگشتی را
// به درخواست/خواننده بدهید؛ در پایان همیشه Stop را صدا بزنید.
type streamIdleGuard struct {
	idle     time.Duration
	lastRead atomic.Int64 // UnixNano آخرین خواندن موفق (هر بایت، حتی کامنت)
	stalled  atomic.Bool  // لغو توسط نگهبان (نه کاربر)
	cancel   context.CancelFunc

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{} // با خروج goroutine نگهبان بسته می‌شود
}

// newStreamIdleGuard نگهبان را می‌سازد و context مشتق‌شده (قابل لغو توسط
// نگهبان) برمی‌گرداند. idle باید از پیش clamp شده باشد.
func newStreamIdleGuard(parent context.Context, idle time.Duration) (*streamIdleGuard, context.Context) {
	ctx, cancel := context.WithCancel(parent)
	g := &streamIdleGuard{
		idle:   idle,
		cancel: cancel,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	g.lastRead.Store(time.Now().UnixNano())
	safeGo(nil, "stream.watchdog", g.watch)
	return g, ctx
}

// watch تنها در صورت نبود داده در مهلت، درخواست را لغو می‌کند.
func (g *streamIdleGuard) watch() {
	defer close(g.doneCh)
	t := time.NewTicker(streamIdlePollEvery)
	defer t.Stop()
	for {
		select {
		case <-g.stopCh:
			return
		case <-t.C:
			last := time.Unix(0, g.lastRead.Load())
			if time.Since(last) >= g.idle {
				g.stalled.Store(true)
				g.cancel()
				return
			}
		}
	}
}

// Body بدنه‌ی پاسخ را می‌پیچد تا هر خواندن موفق، فعالیت حساب شود.
func (g *streamIdleGuard) Body(r io.ReadCloser) io.ReadCloser {
	return &idleReadCloser{g: g, r: r}
}

// Stalled آیا لغو از سوی نگهبان (سکون) بوده است؟ (نه دکمه‌ی توقف کاربر)
func (g *streamIdleGuard) Stalled() bool { return g.stalled.Load() }

// Stop نگهبان را می‌بندد، درخواست را لغو می‌کند و تا خروج goroutine صبر
// می‌کند — تایمر و goroutine هیچ‌وقت جا نمی‌مانند. چندبار صدا زدن بی‌خطر است.
func (g *streamIdleGuard) Stop() {
	if g == nil {
		return
	}
	g.stopOnce.Do(func() { close(g.stopCh) })
	g.cancel()
	<-g.doneCh
}

// idleReadCloser هر خواندن موفق (n>0) را فعالیت ثبت می‌کند؛ خطاها را
// دست‌نخورده عبور می‌دهد.
type idleReadCloser struct {
	g *streamIdleGuard
	r io.ReadCloser
}

func (c *idleReadCloser) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.g.lastRead.Store(time.Now().UnixNano())
	}
	return n, err
}

func (c *idleReadCloser) Close() error { return c.r.Close() }
