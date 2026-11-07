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

// TestAgentThinkOnlyTurnNotice حالت ۱: مدل فقط reasoning می‌فرستد و تمام —
// اعلان «فقط استدلال» باید صادر شود و thinking بدون متنِ قابل‌نمایش ذخیره شود.
func TestAgentThinkOnlyTurnNotice(t *testing.T) {
	raw := "data: " + `{"choices":[{"delta":{"reasoning_content":"Analyzing the request..."},"finish_reason":null}]}` + "\n\n" +
		"data: " + `{"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]" + "\n\n"
	env := newE2E(t, scenarioStep{Raw: raw})
	conv := env.send("سلام")

	found := false
	for _, n := range env.noticesText() {
		if strings.Contains(n, "فقط استدلال") {
			found = true
		}
	}
	if !found {
		t.Fatalf("think-only notice missing; notices=%v", env.noticesText())
	}
	var last *Message
	for i := range conv.Messages {
		if conv.Messages[i].Role == "assistant" {
			last = &conv.Messages[i]
		}
	}
	if last == nil {
		t.Fatal("assistant message (thinking-only) was not saved")
	}
	if strings.TrimSpace(last.Content) != "" {
		t.Fatalf("visible content must be empty, got %q", last.Content)
	}
	if last.Thinking == "" {
		t.Fatal("thinking must be saved on the assistant message")
	}
}

// TestAgentLengthCutThinkingNotice حالت ۲: استدلال با finish_reason=length
// برش می‌خورد — اعلان «سقف توکن خروجی» باید صادر شود.
func TestAgentLengthCutThinkingNotice(t *testing.T) {
	raw := "data: " + `{"choices":[{"delta":{"reasoning_content":"Deep reasoning that got cut"},"finish_reason":"length"}]}` + "\n\n" +
		"data: [DONE]" + "\n\n"
	env := newE2E(t, scenarioStep{Raw: raw})
	_ = env.send("سلام")

	found := false
	for _, n := range env.noticesText() {
		if strings.Contains(n, "سقف توکن خروجی") {
			found = true
		}
	}
	if !found {
		t.Fatalf("length-cut notice missing; notices=%v", env.noticesText())
	}
}

// TestAgentEmptyAnswerNotice حالت ۳: پاسخ کاملاً خالی (بدون متن، بدون فکر،
// بدون ابزار) — اعلان «پاسخ خالی» باید صادر شود و پیام دستیار خالی ذخیره
// نشود (رفتار قبلی حفظ می‌شود).
func TestAgentEmptyAnswerNotice(t *testing.T) {
	env := newE2E(t, scenarioStep{})
	conv := env.send("سلام")

	found := false
	for _, n := range env.noticesText() {
		if strings.Contains(n, "پاسخ خالی") {
			found = true
		}
	}
	if !found {
		t.Fatalf("empty-answer notice missing; notices=%v", env.noticesText())
	}
	for i := range conv.Messages {
		if conv.Messages[i].Role == "assistant" {
			t.Fatal("empty answer must not append an assistant message")
		}
	}
}

// TestAgentNormalAnswerHasNoSilentNotice در پاسخ سالم هیچ اعلان «خاموش»
// نباید صادر شود — گاردِ ضد-هشدار-کاذب.
func TestAgentNormalAnswerHasNoSilentNotice(t *testing.T) {
	env := newE2E(t, scenarioStep{Text: "پاسخ سالم مدل."})
	conv := env.send("سلام")

	for _, n := range env.noticesText() {
		if strings.Contains(n, "پاسخ خالی") || strings.Contains(n, "فقط استدلال") || strings.Contains(n, "سقف توکن خروجی") {
			t.Fatalf("false-positive silent-answer notice on a healthy turn: %q", n)
		}
	}
	if got := lastAssistant(conv); got != "پاسخ سالم مدل." {
		t.Fatalf("healthy answer content = %q", got)
	}
}
