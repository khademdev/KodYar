package main

import (
	"strings"
	"testing"
)

// TestNormalizeThinkingEffort — مقدار پیش‌فرض و نامعتبر باید low باشد (بدون
// ۴۰۰ِ سرویس‌دهنده) و «auto» یعنی پارامتر اصلاً فرستاده نشود.
func TestNormalizeThinkingEffort(t *testing.T) {
	cases := map[string]string{
		"":         thinkingEffortLow,
		"low":      thinkingEffortLow,
		" MEDIUM ": thinkingEffortMedium,
		"High":     thinkingEffortHigh,
		"auto":     thinkingEffortAuto,
		"weird":    thinkingEffortLow,
	}
	for in, want := range cases {
		if got := normalizeThinkingEffort(in); got != want {
			t.Fatalf("normalizeThinkingEffort(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestThinkingEffortUnsupportedDetection — فقط ۴۰۰ای که به reasoning_effort
// اشاره دارد باید عقب‌نشینی را فعال کند.
func TestThinkingEffortUnsupportedDetection(t *testing.T) {
	if !isReasoningEffortUnsupported(400, `{"error":{"message":"Unknown parameter: reasoning_effort"}}`) {
		t.Fatal("400 mentioning reasoning_effort must be recognized")
	}
	if isReasoningEffortUnsupported(500, "reasoning_effort failed") {
		t.Fatal("only HTTP 400 triggers the fallback")
	}
	if isReasoningEffortUnsupported(400, "invalid model") {
		t.Fatal("unrelated 400 must not drop the parameter")
	}
}

// TestThinkingEffortSettingDefaultsAndView — پیش‌فرض low است، ذخیره/بازخوانی
// مقدار را نگه می‌دارد و SettingsView همان مقدار مؤثر را به UI می‌دهد.
func TestThinkingEffortSettingDefaultsAndView(t *testing.T) {
	a := newTestApp(t)
	view := a.SettingsView()
	if view["thinkingEffort"] != thinkingEffortLow {
		t.Fatalf("default thinkingEffort = %v, want low", view["thinkingEffort"])
	}
	if err := a.SetThinkingEffort("HIGH"); err != nil {
		t.Fatalf("SetThinkingEffort: %v", err)
	}
	if got := a.SettingsView()["thinkingEffort"]; got != thinkingEffortHigh {
		t.Fatalf("thinkingEffort after save = %v, want high", got)
	}
}

// TestAgentPromptAsksForShortReasoning — قاعدهٔ پرامپت که مدل را از استدلال
// طولانی به «عمل کردن» می‌برد.
func TestAgentPromptAsksForShortReasoning(t *testing.T) {
	p := agentSystemPrompt("/tmp/ws")
	if !strings.Contains(p, "Keep internal reasoning short") {
		t.Fatal("agent prompt must contain the short-reasoning rule")
	}
}

// TestAgentRequestCarriesThinkingEffort — درخواست واقعی نوبت ایجنت باید
// reasoning_effort=low داشته باشد تا مدل کمتر فکر کند.
func TestAgentRequestCarriesThinkingEffort(t *testing.T) {
	var got string
	env := newE2E(t, scenarioStep{
		Text: "تمام شد.",
		Check: func(t *testing.T, req chatRequest) {
			got = req.ReasoningEffort
		},
	})
	defer env.shutdown()
	env.send("سلام")
	if got != thinkingEffortLow {
		t.Fatalf("request reasoning_effort = %q, want low", got)
	}
}

// TestAgentDropsThinkingEffortOnRejection — سرویس‌دهنده‌ای که reasoning_effort
// را نمی‌پذیرد: یک‌بار ۴۰۰ با اشاره به همان پارامتر، سپس همان دور بدون آن
// تکرار می‌شود (بدون شکستن نوبت و بدون مصرف دور اضافه).
func TestAgentDropsThinkingEffortOnRejection(t *testing.T) {
	env := newE2E(t,
		scenarioStep{Status: 400, StatusBody: `{"error":{"message":"unsupported parameter: reasoning_effort"}}`},
		scenarioStep{Text: "بدون پارامتر ادامه دادم."},
	)
	defer env.shutdown()
	env.send("سلام")
	env.model.mu.Lock()
	bodies := append([]chatRequest(nil), env.model.body...)
	env.model.mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("expected a rejected request and its retry, got %d requests", len(bodies))
	}
	first, second := bodies[0].ReasoningEffort, bodies[1].ReasoningEffort
	if first != thinkingEffortLow {
		t.Fatalf("first request must carry reasoning_effort, got %q", first)
	}
	if second != "" {
		t.Fatalf("retry without the parameter must omit it, got %q", second)
	}
	if providerSupportsReasoningEffort(env.model.URL()) {
		t.Fatalf("expected provider memory to avoid resending later (key %q)", env.model.URL())
	}
}
