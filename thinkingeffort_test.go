package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

// TestLegacySettingsFileGetsLowThinkingEffort — settings.json قدیمی که فیلد
// thinkingEffort را ندارد باید بعد از بارگذاری low شود و نوبت بعدی واقعاً
// پارامتر را بفرستد (اگر این دو از هم جدا شوند، صرفه‌جویی فقط روی کاغذ است).
func TestLegacySettingsFileGetsLowThinkingEffort(t *testing.T) {
	a := newTestApp(t)
	path, err := a.settingsPath()
	if err != nil {
		t.Fatalf("settingsPath: %v", err)
	}
	// فایل قدیمی: فقط کلیدهای زمان خودش (بدون thinkingEffort) — ساخت با همان
	// تگ‌های واقعی Provider تا خطای فرمت، نتیجه را الکی خراب نکند.
	legacy, err := json.Marshal(map[string]interface{}{
		"provider":  "pw",
		"model":     "m1",
		"workspace": a.settings.WorkspacePath,
		"providers": []Provider{{
			ID: "pw", Name: "PW", BaseURL: "http://localhost:9/v1",
			NoKey: true, Models: []string{"m1"}, ContextWindow: 128000,
		}},
	})
	if err != nil {
		t.Fatalf("build legacy settings: %v", err)
	}
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatalf("write legacy settings: %v", err)
	}
	if err := a.loadSettings(); err != nil {
		t.Fatalf("load legacy settings: %v", err)
	}
	if len(a.settings.Providers) != 1 {
		t.Fatalf("legacy settings were not loaded (providers=%d)", len(a.settings.Providers))
	}
	if got := a.settings.ThinkingEffort; got != thinkingEffortLow {
		t.Fatalf("legacy settings loaded with thinkingEffort=%q, want low", got)
	}
	if got := a.SettingsView()["thinkingEffort"]; got != thinkingEffortLow {
		t.Fatalf("SettingsView thinkingEffort = %v, want low", got)
	}

	// نوبت بعدی روی همان اپ بارگذاری‌شده: پارامتر باید واقعاً برود.
	var sent string
	model := newScriptedModel(t, scenarioStep{
		Text:  "تمام شد.",
		Check: func(t *testing.T, req chatRequest) { sent = req.ReasoningEffort },
	})
	a.mu.Lock()
	a.settings.Providers[0].BaseURL = model.URL()
	a.mu.Unlock()
	env := initE2E(t, a)
	env.model = model
	defer env.shutdown()
	env.send("سلام")
	if sent != thinkingEffortLow {
		t.Fatalf("next turn after legacy load sent reasoning_effort=%q, want low", sent)
	}
}

// TestSubagentRequestCarriesThinkingEffort — زیرایجنت (task) هم همان
// reasoning_effort نوبت والد را می‌فرستد؛ بیشترین استدلال معمولاً همین‌جاست.
func TestSubagentRequestCarriesThinkingEffort(t *testing.T) {
	var subEffort, mainEffort string
	subSeen, mainSeen := 0, 0
	env := newE2E(t,
		stepTool("c1", "task", `{"description":"بررسی ساختار پروژه"}`),
		stepText("تمام شد."),
	)
	defer env.shutdown()
	env.model.route = func(req chatRequest) *scenarioStep {
		sys := ""
		if len(req.Messages) > 0 {
			sys = strings.TrimSpace(historyContentString(req.Messages[0].Content))
		}
		if strings.HasPrefix(sys, "You are KodYar's read-only sub-agent") {
			subEffort, subSeen = req.ReasoningEffort, subSeen+1
			return &scenarioStep{Text: "گزارش زیرایجنت."}
		}
		mainEffort, mainSeen = req.ReasoningEffort, mainSeen+1
		return nil
	}
	env.send("بررسی کن")
	if subSeen == 0 {
		t.Fatal("the sub-agent request never reached the provider")
	}
	if mainSeen == 0 {
		t.Fatal("the main turn never reached the provider")
	}
	if subEffort != thinkingEffortLow {
		t.Fatalf("sub-agent reasoning_effort = %q, want low", subEffort)
	}
	if mainEffort != thinkingEffortLow {
		t.Fatalf("main turn reasoning_effort = %q, want low", mainEffort)
	}
}

// captureOneShotBodies یک سرور غیراستریمی می‌سازد که بدنهٔ درخواست‌ها را ثبت
// می‌کند؛ اولین پاسخ ۴۰۰ است (اگر rejectFirst) و بقیه ۲۰۰ با متن خلاصه.
func captureOneShotBodies(t *testing.T, rejectFirst bool) (*httptest.Server, *[]map[string]interface{}) {
	t.Helper()
	bodies := &[]map[string]interface{}{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Errorf("summarizer body is not JSON: %v", err)
		}
		*bodies = append(*bodies, m)
		if rejectFirst && len(*bodies) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"unknown parameter: reasoning_effort"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"خلاصهٔ کوتاه"}}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, bodies
}

// TestSummarizerSendsThinkingEffort — خلاصه‌ساز (oneShotChat) هم مثل نوبت
// اصلی reasoning_effort را می‌فرستد؛ استدلال اضافی در فشرده‌سازی فقط توکن
// می‌سوزاند و خلاصه را بهتر نمی‌کند.
func TestSummarizerSendsThinkingEffort(t *testing.T) {
	srv, bodies := captureOneShotBodies(t, false)
	a := newTestApp(t)
	a.settings.Providers[0].BaseURL = srv.URL
	out, err := a.oneShotChat(context.Background(), "system", "user", 120)
	if err != nil {
		t.Fatalf("oneShotChat: %v", err)
	}
	if out != "خلاصهٔ کوتاه" {
		t.Fatalf("summary = %q", out)
	}
	if len(*bodies) != 1 {
		t.Fatalf("summarizer sent %d requests, want 1", len(*bodies))
	}
	if got := (*bodies)[0]["reasoning_effort"]; got != thinkingEffortLow {
		t.Fatalf("summarizer reasoning_effort = %v, want low", got)
	}
}

// TestSummarizerDropsThinkingEffortOnRejection — همان عقب‌نشینی نوبت اصلی:
// ۴۰۰ِ مربوط به reasoning_effort → یک‌بار بدون پارامتر، بدون شکست خلاصه‌سازی.
func TestSummarizerDropsThinkingEffortOnRejection(t *testing.T) {
	srv, bodies := captureOneShotBodies(t, true)
	a := newTestApp(t)
	a.settings.Providers[0].BaseURL = srv.URL
	out, err := a.oneShotChat(context.Background(), "system", "user", 120)
	if err != nil {
		t.Fatalf("summarizer must retry without reasoning_effort: %v", err)
	}
	if out != "خلاصهٔ کوتاه" {
		t.Fatalf("summary after retry = %q", out)
	}
	if len(*bodies) != 2 {
		t.Fatalf("want rejected request + retry, got %d", len(*bodies))
	}
	if got := (*bodies)[0]["reasoning_effort"]; got != thinkingEffortLow {
		t.Fatalf("first request reasoning_effort = %v, want low", got)
	}
	if _, exists := (*bodies)[1]["reasoning_effort"]; exists {
		t.Fatalf("retry must omit reasoning_effort, got %v", (*bodies)[1]["reasoning_effort"])
	}
	if providerSupportsReasoningEffort(srv.URL) {
		t.Fatal("provider memory must remember the rejection")
	}
}

// TestSummarizerRespectsAutoEffort — حالت «خودکار» هیچ پارامتری نمی‌فرستد.
func TestSummarizerRespectsAutoEffort(t *testing.T) {
	srv, bodies := captureOneShotBodies(t, false)
	a := newTestApp(t)
	a.settings.Providers[0].BaseURL = srv.URL
	if err := a.SetThinkingEffort("auto"); err != nil {
		t.Fatalf("SetThinkingEffort: %v", err)
	}
	if _, err := a.oneShotChat(context.Background(), "system", "user", 120); err != nil {
		t.Fatalf("oneShotChat: %v", err)
	}
	if _, exists := (*bodies)[0]["reasoning_effort"]; exists {
		t.Fatalf("auto mode must omit reasoning_effort, got %v", (*bodies)[0]["reasoning_effort"])
	}
}
