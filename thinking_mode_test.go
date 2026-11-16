package main

import (
	"os"
	"strings"
	"testing"
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
		// برچسب ردیف استریم بر اساس حالت
		`(r.view&&r.view.agentMode)?"در حال فکر کردن…":"در حال نوشتن…"`,
	}
	for _, w := range wants {
		if !strings.Contains(js, w) {
			t.Fatalf("thinking UI guard missing: %s", w)
		}
	}
}
