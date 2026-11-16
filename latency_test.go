// تست‌های اندازه‌گیری تأخیر نوبت (latency.go) و پیش‌فرضِ روشنِ کش پرامپت.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------- واحد

func TestTurnTimingRecordsNotes(t *testing.T) {
	tm := newTurnTiming()
	tm.noteFirstToken()
	tm.noteFirstToken() // دومین دلتا نباید TTFT را جلو ببرد
	tm.noteRound(120 * time.Millisecond)
	tm.noteRound(300 * time.Millisecond)
	tm.noteTool(80)
	tm.noteTool(20)
	tm.noteCacheHints(true)

	tt := tm.snapshot()
	if tt.TTFTMs < 0 || tt.TTFTMs > 5000 {
		t.Fatalf("ttft out of range: %d", tt.TTFTMs)
	}
	if tt.Rounds != 2 || tt.RoundMs != 420 || tt.MaxRoundMs != 300 {
		t.Fatalf("round aggregation wrong: %+v", tt)
	}
	if tt.Tools != 2 || tt.ToolMs != 100 {
		t.Fatalf("tool aggregation wrong: %+v", tt)
	}
	// TotalMs زمان واقعی سپری‌شده است؛ در این تست مصنوعی (دورها با مقدار
	// ساختگی) فقط باید نامنفی باشد — رابطه‌اش با دورها در تست یکپارچه سنجیده
	// می‌شود که دورها واقعی‌اند.
	if tt.TotalMs < 0 {
		t.Fatalf("total must be non-negative: %+v", tt)
	}
	if !tt.PromptCache {
		t.Fatal("cache flag must be recorded")
	}
	if tt.slowReason() != "" {
		t.Fatalf("fast turn must not be reported slow: %q", tt.slowReason())
	}
	slow := TurnTiming{TTFTMs: latencySlowTTFTms, MaxRoundMs: latencySlowRoundMs, TotalMs: latencySlowTurnMs}
	if r := slow.slowReason(); !strings.Contains(r, "ttft=") || !strings.Contains(r, "total=") {
		t.Fatalf("slow turn must explain why: %q", r)
	}
	if !strings.Contains(slow.String(), "rounds=0") {
		t.Fatalf("summary string missing fields: %s", slow.String())
	}
}

// ---------------------------------------------------------------- یکپارچه

// capturedRequest یک بدنه‌ی درخواست ثبت‌شده + زمان اولین دلتا.
type capturedRequest struct {
	raw  string
	body map[string]interface{}
}

// latencyServer سروری که بدنه‌ها را ثبت می‌کند و پیش از نخستین چانک
// «firstDelay» صبر می‌کند (شبیه‌سازی TTFT واقعی سرویس‌دهنده).
func latencyServer(t *testing.T, firstDelay time.Duration, mu *sync.Mutex, out *[]capturedRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var parsed map[string]interface{}
		_ = json.Unmarshal(raw, &parsed)
		mu.Lock()
		*out = append(*out, capturedRequest{raw: string(raw), body: parsed})
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		time.Sleep(firstDelay)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"سلام\"},\"finish_reason\":null}]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(20 * time.Millisecond)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" دنیا\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestTurnTimingTTFTAndCacheHintsRoundTrip(t *testing.T) {
	var mu sync.Mutex
	var captured []capturedRequest
	const delay = 180 * time.Millisecond
	srv := latencyServer(t, delay, &mu, &captured)
	defer srv.Close()

	a := chatAppOnServer(t, srv.URL)
	if !a.promptCacheEnabled() {
		t.Fatal("prompt cache must be enabled by default")
	}

	conv, err := a.SendMessage("", "سلام", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitStreamDone(t, a)

	tt := a.GetLastTurnTiming()
	if tt.TTFTMs < int64(delay.Milliseconds()/2) {
		t.Fatalf("ttft must include the provider delay, got %dms", tt.TTFTMs)
	}
	if tt.Rounds != 1 {
		t.Fatalf("single round expected, got %+v", tt)
	}
	if tt.TotalMs < tt.RoundMs || tt.RoundMs <= 0 {
		t.Fatalf("round/total timing wrong: %+v", tt)
	}
	if !tt.PromptCache {
		t.Fatal("default-on cache must mark the request")
	}
	t.Logf("measured turn: %s (provider injected %dms before first chunk)", tt.String(), delay.Milliseconds())

	// نشانه‌های کش باید واقعاً در بدنه‌ی ارسالی باشند (system + آخرین tool).
	mu.Lock()
	first := captured[0].raw
	mu.Unlock()
	if strings.Count(first, `"cache_control":{"type":"ephemeral"}`) == 0 {
		t.Fatalf("default-on cache must send cache_control hints:\n%s", first[:min(len(first), 400)])
	}

	// خاموش‌کردن کاربر → دور بعد بدون نشانه (و ماندگار در تنظیمات).
	if err := a.SetPromptCache(false); err != nil {
		t.Fatal(err)
	}
	if v, ok := a.SettingsView()["promptCache"].(bool); !ok || v {
		t.Fatalf("SettingsView must expose promptCache=false after user opt-out, got %#v", a.SettingsView()["promptCache"])
	}
	conv2, err := a.SendMessage(conv.ID, "دوباره", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitStreamDone(t, a)
	_ = conv2

	mu.Lock()
	last := captured[len(captured)-1].raw
	mu.Unlock()
	if strings.Contains(last, "cache_control") {
		t.Fatal("after opt-out the request must not carry cache hints")
	}
	if a.GetLastTurnTiming().PromptCache {
		t.Fatal("timing must reflect that hints were off")
	}
}

// TestPromptCachePayloadShare عدد قابل‌گزارش برای «قبل/بعد» را می‌سازد:
// چه سهمی از بدنه‌ی درخواست (بایت) در بلوک پایدار و قابلِ‌کش است.
func TestPromptCachePayloadShare(t *testing.T) {
	tools := agentToolDefs()
	sys := agentSystemPromptForOS(`C:\Projects\Demo`, "windows")
	req := chatRequest{
		Model: "m",
		Messages: []chatMessage{
			{Role: "system", Content: sys},
			{Role: "user", Content: "یک تابع Go بنویس"},
			{Role: "assistant", Content: strings.Repeat("توضیح ", 40)},
			{Role: "user", Content: "بازبینی کن"},
		},
		Tools:  tools,
		Stream: true,
	}
	plain, _ := json.Marshal(req)
	hinted, applied := withPromptCacheHints(req)
	if !applied {
		t.Fatal("hints must apply for a normal agent request")
	}
	withHints, _ := json.Marshal(hinted)

	// سهم بلوک پایدار: system + تعریف همهٔ ابزارها (همان چیزی که نشانه می‌گیرد).
	stable, _ := json.Marshal(chatRequest{
		Messages: []chatMessage{{Role: "system", Content: sys}},
		Tools:    tools,
	})
	share := float64(len(stable)) / float64(len(plain)) * 100
	stableTokens := estimateTokens(string(stable))
	t.Logf("payload: full=%dB (~%d tok) stable(cacheable)=%dB (~%d tok, %.1f%%) hint_overhead=%dB tools=%d",
		len(plain), estimateTokens(string(plain)), len(stable), stableTokens, share, len(withHints)-len(plain), len(tools))
	if share < 50 {
		t.Fatalf("stable prefix should dominate an agent turn, got %.1f%%", share)
	}
	if len(withHints) <= len(plain) {
		t.Fatal("hint payload must differ from plain payload")
	}
}
