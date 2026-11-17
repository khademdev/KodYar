package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// sseSeq یک پاسخ SSE از دلتاهای دلخواه می‌سازد (content/reasoning) + [DONE].
func sseSeq(parts ...string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString("data: " + p + "\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

// eventDeltas متن دلتاهای یک کانال رویداد را به‌ترتیب جمع می‌کند.
func eventDeltas(env *e2eEnv, name string) string {
	var b strings.Builder
	for _, ev := range env.eventsByName(name) {
		if len(ev.data) == 0 {
			continue
		}
		if m, ok := ev.data[0].(map[string]interface{}); ok {
			if s, ok := m["delta"].(string); ok {
				b.WriteString(s)
			}
		}
	}
	return b.String()
}

// lastAssistantOf آخرین پیام دستیار گفتگو.
func lastAssistantOf(t *testing.T, conv *Conversation) *Message {
	t.Helper()
	for i := len(conv.Messages) - 1; i >= 0; i-- {
		if conv.Messages[i].Role == "assistant" {
			return &conv.Messages[i]
		}
	}
	t.Fatal("no assistant message")
	return nil
}

// TestChatModeIgnoresThinking: در چت معمولی نه رویداد «فکر کردن» فرستاده
// می‌شود و نه استدلالِ مدل ذخیره می‌شود — حالت فکر فقط مخصوص ایجنت است.
func TestChatModeIgnoresThinking(t *testing.T) {
	raw := sseSeq(
		`{"choices":[{"delta":{"reasoning_content":"برنامه‌ی مخفی"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":" ادامه‌ی فکر"}}]}`,
		`{"choices":[{"delta":{"content":"سلام! "}}]}`,
		`{"choices":[{"delta":{"content":"در خدمتم."}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	env := newE2E(t, scenarioStep{Raw: raw})
	env.app.mu.Lock()
	env.app.settings.AgentMode = false
	env.app.mu.Unlock()

	conv := env.send("سلام")

	if n := len(env.eventsByName("chat:thinking")); n != 0 {
		t.Fatalf("chat mode must not emit thinking, got %d events", n)
	}
	last := lastAssistantOf(t, conv)
	if last.Thinking != "" {
		t.Fatalf("chat mode must not store thinking, got %q", last.Thinking)
	}
	if !strings.Contains(last.Content, "در خدمتم.") {
		t.Fatalf("visible answer lost: %q", last.Content)
	}
	if strings.Contains(last.Content, "مخفی") {
		t.Fatalf("reasoning leaked into visible answer: %q", last.Content)
	}
	if got := eventDeltas(env, "chat:delta"); !strings.Contains(got, "در خدمتم.") {
		t.Fatalf("streamed deltas lost the tail: %q", got)
	}
}

// TestAgentThinkingStreamsAndStores: در حالت ایجنت استدلال هم زنده فرستاده
// می‌شود و هم ذخیره؛ دلتاهای دسته‌ای نباید ابتدا/انتها را گم کنند.
func TestAgentThinkingStreamsAndStores(t *testing.T) {
	raw := sseSeq(
		`{"choices":[{"delta":{"reasoning_content":"اول "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"دوم "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"سوم"}}]}`,
		`{"choices":[{"delta":{"content":"نتیجه: "}}]}`,
		`{"choices":[{"delta":{"content":"همه‌چیز مرتب است."}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	env := newE2E(t, scenarioStep{Raw: raw})

	conv := env.send("کار کن")

	th := eventDeltas(env, "chat:thinking")
	if th != "اول دوم سوم" {
		t.Fatalf("streamed thinking = %q", th)
	}
	vis := eventDeltas(env, "chat:delta")
	if vis != "نتیجه: همه‌چیز مرتب است." {
		t.Fatalf("streamed visible = %q", vis)
	}
	last := lastAssistantOf(t, conv)
	if last.Thinking != "اول دوم سوم" {
		t.Fatalf("stored thinking = %q", last.Thinking)
	}
	if last.Content != vis {
		t.Fatalf("stored content %q != streamed %q", last.Content, vis)
	}
}

// TestThinkingUIOnlyInAgentMode: نمایش «فکر کردن» در UI هم به حالت ایجنت
// گره خورده است؛ در چت معمولی فقط «در حال نوشتن…» دیده می‌شود.
func TestThinkingUIOnlyInAgentMode(t *testing.T) {
	data, err := os.ReadFile("frontend/dist/assets/js/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(data)
	wants := []string{
		// رویداد فکر در چت نادیده گرفته می‌شود
		`function Z(e){if(!(r.view&&r.view.agentMode))return;`,
		// بلوک فکر پیام ذخیره‌شده فقط در ایجنت
		`if(allTh&&(!r.view||r.view.agentMode)){`,
		// بلوک فکر زنده فقط در ایجنت
		`if(thEl&&r.streamThinkBuf&&r.view&&r.view.agentMode){`,
		// برچسب ردیف استریم فاز-محور است: «فکر کردن» فقط وقتی استدلال واقعاً
		// می‌آید، «نوشتن» با اولین متن، و فاز اولیه بر اساس حالت.
		`id="stream-label"`,
		`function setStreamPhase(p){`,
		`think:"در حال فکر کردن…"`,
		`(r.view&&r.view.agentMode)?"start":"write"`,
		`setStreamPhase("think")`,
	}
	for _, w := range wants {
		if !strings.Contains(js, w) {
			t.Fatalf("thinking UI guard missing: %s", w)
		}
	}
}

// TestAgentThinkingStreamsBeforeContent — رگرسیون «فکر بی‌صدا»:
// flushStream تنها با دلتای content صدا زده می‌شد، پس مدلی که دقایقی فقط
// reasoning_content می‌فرستد هیچ chat:thinking‌ای به UI نمی‌داد: برچسب روی
// «در حال آماده‌سازی…» می‌ماند و کل استدلال تازه پس از «توقف»/پایان دور
// ظاهر می‌شد (شکایت «کند است و نشان نمی‌دهد چه می‌کند؛ بعد از توقف دیدم
// فکر کرده»). حالا نخستین دلتای استدلال همان لحظه منتشر می‌شود.
func TestAgentThinkingStreamsBeforeContent(t *testing.T) {
	release := make(chan struct{})
	reached := make(chan struct{})
	var freeOnce, reachOnce sync.Once
	free := func() { freeOnce.Do(func() { close(release) }) }
	markReached := func() { reachOnce.Do(func() { close(reached) }) }
	defer free()

	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqs, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"فکر عمیق\"}}]}\n\n")
		if fl != nil {
			fl.Flush()
		}
		markReached()
		// تا وقتی تست رویداد استدلال را نبیند، متن پاسخ نمی‌آید.
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"پاسخ نهایی\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
	defer srv.Close()

	a := streamTestApp(t, srv.URL)
	a.mu.Lock()
	a.settings.AgentMode = true
	a.mu.Unlock()

	think := make(chan string, 4)
	a.eventEmit = func(name string, data []interface{}) {
		if name != "chat:thinking" || len(data) == 0 {
			return
		}
		m, ok := data[0].(map[string]interface{})
		if !ok {
			return
		}
		if s, ok := m["delta"].(string); ok {
			select {
			case think <- s:
			default:
			}
		}
	}

	conv, err := a.SendMessage("", "فکر کن", nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		free()
		t.Fatal("provider stream never started")
	}

	select {
	case got := <-think:
		if !strings.Contains(got, "فکر عمیق") {
			free()
			t.Fatalf("first live thinking delta = %q", got)
		}
	case <-time.After(3 * time.Second):
		free()
		t.Fatal("thinking delta was not streamed while the provider was still silent — the UI stays on «در حال آماده‌سازی…» until the round ends (regression)")
	}

	free()
	waitGenerationDone(t, a, 15*time.Second)

	msgs := assistantMessages(t, a, conv.ID)
	if len(msgs) == 0 {
		t.Fatal("no assistant message")
	}
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "پاسخ نهایی") {
		t.Fatalf("final answer lost: %q", last.Content)
	}
	if last.Thinking == "" {
		t.Fatal("thinking must be stored on the assistant message")
	}
}
