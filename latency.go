// اندازه‌گیری تأخیر نوبت ایجنت — TTFT، زمان هر دور و زمان هر ابزار.
//
// چرا: پیش از این «کند است» فقط حس بود؛ هیچ عددی از زمان تا اولین توکن،
// طول هر دور یا زمان ابزارها ثبت نمی‌شد و هر بهینه‌سازی بی‌سنجه بود.
//
// اصول:
//   - جمع‌آوری در یک struct قفل‌دار (ابزارها موازی اجرا می‌شوند).
//   - TTFT = از شروع نوبت تا اولین دلتای محتوا/استدلال/فراخوانی ابزار.
//   - خروجی: رویداد chat:timing (هر نوبت) + GetLastTurnTiming برای UI/تست
//   - یک خط [latency] در errors.log فقط برای نوبت‌های کند (تا لاگ شلوغ نشود).
//   - هزینهٔ اندازه‌گیری: چند میلی‌ثانیه در کل نوبت (فقط زمان‌گیری، بدون I/O).
package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const (
	// آستانه‌های «نوبت کند» — فراتر از این‌ها یک خط در errors.log ثبت می‌شود.
	latencySlowTTFTms  = int64(6000)
	latencySlowRoundMs = int64(30000)
	latencySlowTurnMs  = int64(120000)
)

// TurnTiming خلاصهٔ عددی یک نوبت (میلی‌ثانیه).
type TurnTiming struct {
	TTFTMs      int64 `json:"ttftMs"`      // تا اولین توکن
	Rounds      int   `json:"rounds"`      // تعداد دورهای مدل
	RoundMs     int64 `json:"roundMs"`     // مجموع زمان دورها
	MaxRoundMs  int64 `json:"maxRoundMs"`  // کندترین دور
	Tools       int   `json:"tools"`       // تعداد فراخوانی ابزار
	ToolMs      int64 `json:"toolMs"`      // مجموع زمان ابزارها
	TotalMs     int64 `json:"totalMs"`     // کل نوبت
	PromptCache bool  `json:"promptCache"` // نشانه‌های کش در آخرین دور
}

// turnTiming جمع‌آورندهٔ زندهٔ اعداد یک نوبت.
type turnTiming struct {
	mu        sync.Mutex
	started   time.Time
	firstAt   time.Time
	rounds    int
	roundMs   int64
	maxRound  int64
	tools     int
	toolMs    int64
	cacheHint bool
}

func newTurnTiming() *turnTiming {
	return &turnTiming{started: time.Now()}
}

// noteFirstToken فقط اولین بار مقدار می‌گیرد (دلتاهای بعدی نادیده).
func (t *turnTiming) noteFirstToken() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.firstAt.IsZero() {
		t.firstAt = time.Now()
	}
	t.mu.Unlock()
}

func (t *turnTiming) noteRound(d time.Duration) {
	if t == nil {
		return
	}
	ms := d.Milliseconds()
	t.mu.Lock()
	t.rounds++
	t.roundMs += ms
	if ms > t.maxRound {
		t.maxRound = ms
	}
	t.mu.Unlock()
}

func (t *turnTiming) noteCacheHints(on bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.cacheHint = on
	t.mu.Unlock()
}

func (t *turnTiming) noteTool(ms int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.tools++
	if ms > 0 {
		t.toolMs += ms
	}
	t.mu.Unlock()
}

func (t *turnTiming) snapshot() TurnTiming {
	if t == nil {
		return TurnTiming{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	ttft := int64(-1)
	if !t.firstAt.IsZero() {
		ttft = t.firstAt.Sub(t.started).Milliseconds()
	}
	return TurnTiming{
		TTFTMs:      ttft,
		Rounds:      t.rounds,
		RoundMs:     t.roundMs,
		MaxRoundMs:  t.maxRound,
		Tools:       t.tools,
		ToolMs:      t.toolMs,
		TotalMs:     time.Since(t.started).Milliseconds(),
		PromptCache: t.cacheHint,
	}
}

// slowReason توضیح کوتاه «چرا این نوبت کند بود»؛ خالی = نوبت عادی.
func (tt TurnTiming) slowReason() string {
	var reason string
	if tt.TTFTMs >= latencySlowTTFTms {
		reason += fmt.Sprintf("ttft=%dms ", tt.TTFTMs)
	}
	if tt.MaxRoundMs >= latencySlowRoundMs {
		reason += fmt.Sprintf("slow_round=%dms ", tt.MaxRoundMs)
	}
	if tt.TotalMs >= latencySlowTurnMs {
		reason += fmt.Sprintf("total=%dms ", tt.TotalMs)
	}
	return reason
}

func (tt TurnTiming) String() string {
	ttft := "n/a"
	if tt.TTFTMs >= 0 {
		ttft = fmt.Sprintf("%dms", tt.TTFTMs)
	}
	return fmt.Sprintf("ttft=%s rounds=%d round=%dms max_round=%dms tools=%d tool=%dms total=%dms cache=%v",
		ttft, tt.Rounds, tt.RoundMs, tt.MaxRoundMs, tt.Tools, tt.ToolMs, tt.TotalMs, tt.PromptCache)
}

// ---------------------------------------------------------------- context

type turnTimingKey struct{}

func withTurnTiming(ctx context.Context, t *turnTiming) context.Context {
	if t == nil {
		return ctx
	}
	return context.WithValue(ctx, turnTimingKey{}, t)
}

func turnTimingFrom(ctx context.Context) *turnTiming {
	if ctx == nil {
		return nil
	}
	if t, ok := ctx.Value(turnTimingKey{}).(*turnTiming); ok {
		return t
	}
	return nil
}

// noteToolTiming از ابزارها (toolbatch.go) صدا زده می‌شود؛ بدون timing بی‌اثر.
func noteToolTiming(ctx context.Context, ms int64) {
	turnTimingFrom(ctx).noteTool(ms)
}

// ---------------------------------------------------------------- App

// setLastTurnTiming آخرین زمان‌بندی را برای GetLastTurnTiming نگه می‌دارد.
func (a *App) setLastTurnTiming(tt TurnTiming) {
	if a == nil {
		return
	}
	a.timingMu.Lock()
	a.lastTiming = tt
	a.timingMu.Unlock()
}

// GetLastTurnTiming زمان‌بندی آخرین نوبت (بدون I/O) — برای UI/تست.
func (a *App) GetLastTurnTiming() TurnTiming {
	defer a.recoverBound("GetLastTurnTiming")
	a.timingMu.Lock()
	defer a.timingMu.Unlock()
	return a.lastTiming
}

// recordTurnTiming اعداد نوبت را منتشر و (اگر کند بود) در لاگ ثبت می‌کند.
func (a *App) recordTurnTiming(convID string, turnID int64, tt TurnTiming) {
	a.setLastTurnTiming(tt)
	runtimeEmit(a, "chat:timing", map[string]interface{}{
		"conversationId": convID,
		"turnId":         turnID,
		"timing":         tt,
	})
	if reason := tt.slowReason(); reason != "" {
		logAppErrorFor(a, "latency", fmt.Sprintf("turn %d %s| %s", turnID, reason, tt.String()))
	}
}
