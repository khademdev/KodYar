// حسابداری توکن به تفکیک مدل + اعلان «پاسخ خاموش» (مأموریت ۹).
//
// دو قرارداد کاربر که این‌جا گارد می‌شوند:
// ۱) توکن‌های مدل‌های مختلف هرگز با هم قاطی نمی‌شوند — حتی وقتی هر دو مدل
//
//	از یک Provider هستند و کاربر بین‌شان جابه‌جا می‌شود.
//
// ۲) اگر دور آخر یک نوبت هیچ متن قابل‌نمایشی نداشت (فقط استدلال، برش
//
//	«سقف توکن خروجی» یا پاسخ کاملاً خالی)، کاربر با اعلان فارسی مطلع
//	می‌شود؛ دیگر هیچ نوبتی بی‌صدا «فکر می‌کند و جواب نمی‌دهد».
package main

import (
	"strings"
	"testing"
)

// TestPerModelTokenAccounting جداسازی سطل‌های مدل و سلامت سطل‌های دیگر.
func TestPerModelTokenAccounting(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	// نوبت اول: مدل A — ۱۵۰
	a.recordTokenUsageLocked(nil, "model-a", 100, 50, true)
	// نوبت دوم: مدل B از همان Provider — ۲۷۰
	a.recordTokenUsageLocked(nil, "model-b", 200, 70, true)
	// نوبت سوم: دوباره مدل A — انباشت در همان سطل، نه سطل جدید — ۱۵
	a.recordTokenUsageLocked(nil, "model-a", 10, 5, true)
	// نوبت بدون مدل (رشته خالی) — فقط Provider/کل؛ سطل مدل نباید ساخته شود
	a.recordTokenUsageLocked(nil, "", 7, 3, true)
	a.mu.Unlock()

	if got := a.settings.TokensByModel["model-a"]; got != 165 {
		t.Fatalf("model-a bucket = %d, want 165 (150+15)", got)
	}
	if got := a.settings.TokensByModel["model-b"]; got != 270 {
		t.Fatalf("model-b bucket = %d, want 270", got)
	}
	if got := a.settings.TokensUsed; got != 445 {
		t.Fatalf("global total = %d, want 445", got)
	}
	if got := a.settings.TokensByProvider["ollama"]; got != 445 {
		t.Fatalf("provider bucket = %d, want 445 (still the sum of both models)", got)
	}
	if _, ok := a.settings.TokensByModel[""]; ok {
		t.Fatal("empty model name must not create a model bucket")
	}
	// RecentUsage فقط completionها: 50+70+5+3 در ۴ نمونه
	if len(a.settings.RecentUsage) != 4 {
		t.Fatalf("recent usage len = %d, want 4", len(a.settings.RecentUsage))
	}
	var sum int64
	for _, v := range a.settings.RecentUsage {
		sum += v
	}
	if sum != 128 {
		t.Fatalf("recent usage sum = %d, want 128", sum)
	}
}

// TestSettingsViewPerModelTokens نمای تنظیمات باید مصرف مدل فعال و سطل‌های
// مدل را بدون کلیدها و بدون نشت map بیرون بدهد.
func TestSettingsViewPerModelTokens(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.recordTokenUsageLocked(nil, a.settings.Model, 100, 50, true)
	a.mu.Unlock()

	v := a.SettingsView()
	mu, ok := v["modelTokensUsed"].(int64)
	if !ok || mu != 150 {
		t.Fatalf("modelTokensUsed = %v (%T), want 150", v["modelTokensUsed"], v["modelTokensUsed"])
	}
	buckets, ok := v["tokensByModel"].(map[string]int64)
	if !ok {
		t.Fatalf("tokensByModel type = %T, want map[string]int64", v["tokensByModel"])
	}
	if buckets[a.settings.Model] != 150 {
		t.Fatalf("tokensByModel[active] = %d, want 150", buckets[a.settings.Model])
	}
	// سطل Provider هم باید همان جمع را بدهد (هم‌راستا با رفتار قبلی)
	pu, ok := v["providerTokensUsed"].(int64)
	if !ok || pu != 150 {
		t.Fatalf("providerTokensUsed = %v (%T), want 150", v["providerTokensUsed"], v["providerTokensUsed"])
	}
}

// TestAgentThinkOnlyAutoRecovers حالت ۱: مدل فقط reasoning می‌فرستد و تمام —
// یک‌بار خودکار ادامه خواسته می‌شود و پاسخ نهایی ذخیره می‌شود.
func TestAgentThinkOnlyAutoRecovers(t *testing.T) {
	raw := "data: " + `{"choices":[{"delta":{"reasoning_content":"Analyzing the request..."},"finish_reason":null}]}` + "\n\n" +
		"data: " + `{"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]" + "\n\n"
	env := newE2E(t, scenarioStep{Raw: raw})
	conv := env.send("سلام")

	if n := env.model.count(); n != 2 {
		t.Fatalf("expected exactly one automatic recovery request (2 total), got %d", n)
	}
	reqs := env.model.requests()
	if !strings.Contains(requestMessages(reqs[1]), "no visible answer") {
		t.Fatalf("recovery directive missing from second request:\n%s", requestMessages(reqs[1]))
	}
	if got := lastAssistant(conv); got != "تمام شد." {
		t.Fatalf("final answer after recovery = %q, want fallback answer", got)
	}
	found := false
	for _, n := range env.noticesText() {
		if strings.Contains(n, "پاسخ متنی") {
			found = true
		}
	}
	if !found {
		t.Fatalf("recovery notice missing; notices=%v", env.noticesText())
	}
}

// TestAgentLengthCutAutoRecovers حالت ۲: استدلال با finish_reason=length
// برش می‌خورد — یک‌بار خودکار ادامه خواسته می‌شود و پاسخ نهایی می‌آید.
func TestAgentLengthCutAutoRecovers(t *testing.T) {
	raw := "data: " + `{"choices":[{"delta":{"reasoning_content":"Deep reasoning that got cut"},"finish_reason":"length"}]}` + "\n\n" +
		"data: [DONE]" + "\n\n"
	env := newE2E(t, scenarioStep{Raw: raw})
	conv := env.send("سلام")

	if n := env.model.count(); n != 2 {
		t.Fatalf("expected exactly one automatic recovery request (2 total), got %d", n)
	}
	reqs := env.model.requests()
	if !strings.Contains(requestMessages(reqs[1]), "output token limit") {
		t.Fatalf("length-aware recovery directive missing:\n%s", requestMessages(reqs[1]))
	}
	if got := lastAssistant(conv); got != "تمام شد." {
		t.Fatalf("final answer after recovery = %q, want fallback answer", got)
	}
	found := false
	for _, n := range env.noticesText() {
		if strings.Contains(n, "سقف توکن خروجی") {
			found = true
		}
	}
	if !found {
		t.Fatalf("length-cut recovery notice missing; notices=%v", env.noticesText())
	}
}

// TestAgentEmptyAnswerAutoRecovers حالت ۳: پاسخ کاملاً خالی (بدون متن، بدون
// فکر، بدون ابزار) — یک‌بار خودکار ادامه خواسته می‌شود.
func TestAgentEmptyAnswerAutoRecovers(t *testing.T) {
	env := newE2E(t, scenarioStep{})
	conv := env.send("سلام")

	if n := env.model.count(); n != 2 {
		t.Fatalf("expected exactly one automatic recovery request (2 total), got %d", n)
	}
	if got := lastAssistant(conv); got != "تمام شد." {
		t.Fatalf("final answer after recovery = %q, want fallback answer", got)
	}
}

// TestAgentSilentTurnPersistsNoticeAfterFailedRecovery: اگر بازیابی خودکار هم
// پاسخی نگیرد، نوبت نباید خاموش تمام شود؛ یک پیام ماندگار با دلیل و راه‌حل
// ذخیره می‌شود و thinking دورِ اول حفظ می‌ماند.
func TestAgentSilentTurnPersistsNoticeAfterFailedRecovery(t *testing.T) {
	raw := "data: " + `{"choices":[{"delta":{"reasoning_content":"Analyzing the request..."},"finish_reason":null}]}` + "\n\n" +
		"data: " + `{"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]" + "\n\n"
	env := newE2E(t, scenarioStep{Raw: raw}, scenarioStep{Raw: raw})
	conv := env.send("سلام")

	if n := env.model.count(); n != 2 {
		t.Fatalf("recovery must be attempted exactly once (2 requests), got %d", n)
	}
	var last *Message
	for i := range conv.Messages {
		if conv.Messages[i].Role == "assistant" {
			last = &conv.Messages[i]
		}
	}
	if last == nil {
		t.Fatal("a persistent notice message must be saved instead of a silent turn")
	}
	if !strings.Contains(last.Content, "⚠️") || !strings.Contains(last.Content, "فقط استدلال") {
		t.Fatalf("persistent notice text missing: %q", last.Content)
	}
	if !strings.Contains(last.Thinking, "Analyzing the request") {
		t.Fatalf("thinking of the silent round must be preserved, got %q", last.Thinking)
	}
}

// TestAgentUnnamedToolCallAutoRecovers: tool_call بدون name (pend غیرخالی ولی
// فراخوانی قابل‌اجرا صفر) قبلاً هیچ اعلانی هم صادر نمی‌کرد؛ حالا باید بازیابی
// شود، نه سکوت.
func TestAgentUnnamedToolCallAutoRecovers(t *testing.T) {
	raw := "data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"arguments":"{}"}}]},"finish_reason":null}]}` + "\n\n" +
		"data: " + `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		"data: [DONE]" + "\n\n"
	env := newE2E(t, scenarioStep{Raw: raw})
	conv := env.send("سلام")

	if n := env.model.count(); n != 2 {
		t.Fatalf("unnamed tool call must trigger one recovery request (2 total), got %d", n)
	}
	if got := lastAssistant(conv); got != "تمام شد." {
		t.Fatalf("final answer after recovery = %q, want fallback answer", got)
	}
}

// TestAgentProviderErrorIsPersisted: خطای پایان نوبت (مثل ۴۲۹) فقط توست
// گذرا نباشد؛ باید به‌صورت پیام ماندگار در گفتگو بماند.
func TestAgentProviderErrorIsPersisted(t *testing.T) {
	withInstantRetry(t)
	env := newE2E(t,
		scenarioStep{Status: 429}, scenarioStep{Status: 429},
		scenarioStep{Status: 429}, scenarioStep{Status: 429},
	)
	conv := env.send("سلام")

	var last *Message
	for i := range conv.Messages {
		if conv.Messages[i].Role == "assistant" {
			last = &conv.Messages[i]
		}
	}
	if last == nil {
		t.Fatal("provider error must be persisted as a visible message")
	}
	if !strings.Contains(last.Content, "⚠️") || !strings.Contains(last.Content, "429") {
		t.Fatalf("persisted error message missing reason: %q", last.Content)
	}
}

// TestAgentNormalAnswerHasNoSilentNotice در پاسخ سالم هیچ بازیابی/اعلان
// «خاموش» نباید رخ دهد — گاردِ ضد-هشدار-کاذب.
func TestAgentNormalAnswerHasNoSilentNotice(t *testing.T) {
	env := newE2E(t, scenarioStep{Text: "پاسخ سالم مدل."})
	conv := env.send("سلام")

	if n := env.model.count(); n != 1 {
		t.Fatalf("healthy answer must be a single request, got %d", n)
	}
	for _, n := range env.noticesText() {
		if strings.Contains(n, "پاسخ متنی") || strings.Contains(n, "سقف توکن خروجی") {
			t.Fatalf("false-positive silent-answer notice on a healthy turn: %q", n)
		}
	}
	if got := lastAssistant(conv); got != "پاسخ سالم مدل." {
		t.Fatalf("healthy answer content = %q", got)
	}
}
