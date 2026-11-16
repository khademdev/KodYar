// تست‌های منطق مصرف توکن و دایرهٔ مصرف (context ring).
//
// قراردادهایی که این‌جا گارد می‌شوند:
//
//	۱) عددِ نمایشی کانتکست (ContextTokens / chat:done.tokensUsed) فقط حجمِ
//	   چیزی است که واقعاً به مدل فرستاده می‌شود: thinking ارسال نمی‌شود،
//	   Attached فقط برای آخرین پیام کاربر/سنجاق‌شده‌ها می‌رود و آرگومان‌های
//	   tool_calls بخشی از درخواست بعدی‌اند.
//	۲) Tokens هر پیام دستیار حجم خودِ همان متن است، نه مجموع completion کل
//	   نوبت (باگ بادکردن دایره در نوبت‌های چنددوری ایجنت).
//	۳) «دقیق» فقط وقتی ادعا می‌شود که prompt_tokens واقعی از API آمده باشد؛
//	   اگر فقط completion/total آمده باشد، عدد تخمینی است.
//	۴) عدد دایره به تخمینِ همان تاریخی که ارسال می‌شود نزدیک باشد (نه چند
//	   برابر آن) — هم در نوبت متنی و هم در نوبت چنددوری با ابزار.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// estimateSentMessagesForTest تخمین حجم پیام‌هایی است که prepareStream برای
// درخواست بعدی می‌سازد، بدون system prompt (که در دایره فقط پیام‌ها شمرده
// می‌شوند). این همان «تاریخچه‌ی واقعاً ارسالی» است.
func estimateSentMessagesForTest(t *testing.T, a *App, convID string) int64 {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	conv, err := a.loadConversation(convID)
	if err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	_, _, _, _, history, err := a.prepareStream(conv, false, "")
	if err != nil {
		t.Fatalf("prepareStream: %v", err)
	}
	if len(history) > 0 && history[0].Role == "system" {
		history = history[1:]
	}
	return (&historyTokenCache{}).totalOf(history)
}

// lastEvent آخرین رویداد با این نام را برمی‌گرداند.
func (e *e2eEnv) lastEvent(name string) *e2eEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := len(e.events) - 1; i >= 0; i-- {
		if e.events[i].name == name {
			ev := e.events[i]
			return &ev
		}
	}
	return nil
}

// TestContextTokensExcludeThinkingAndUnsentAttachments قرارداد ۱.
func TestContextTokensExcludeThinkingAndUnsentAttachments(t *testing.T) {
	longAttach := strings.Repeat("محتوای فایل پیوست‌شده طولانی. ", 200)
	thinking := strings.Repeat("استدلال طولانی مدل. ", 300)
	conv := &Conversation{ID: "c1", Messages: []Message{
		{ID: "u1", Role: "user", Content: "پیام اول", Tokens: estimateTokens("پیام اول"), Attached: longAttach},
		{ID: "a1", Role: "assistant", Content: "باشه", Tokens: estimateTokens("باشه"), Thinking: thinking},
		{ID: "t1", Role: "tool", Content: strings.Repeat("tool out\n", 50), ToolCallID: "x"},
		{ID: "u2", Role: "user", Content: "پیام دوم", Tokens: estimateTokens("پیام دوم"), Attached: longAttach},
	}}

	got := conversationContextTokens(conv)
	// پیوستِ پیام اول ارسال نمی‌شود؛ thinking هم همین‌طور.
	withAll := got + estimateTokens(longAttach) + estimateTokens(thinking)
	if got >= withAll {
		t.Fatalf("context estimate must exclude unsent attachments/thinking: got=%d withAll=%d", got, withAll)
	}
	// پیوستِ آخرین پیام کاربر باید حساب شود.
	base := estimateTokens("پیام اول") + estimateTokens("باشه") + estimateTokens("پیام دوم") + int64(len(strings.Repeat("tool out\n", 50)))*2/5
	if got < base {
		t.Fatalf("context estimate must include the last user's attachment: got=%d base=%d", got, base)
	}

	// پیام سنجاق‌شده در پنجره هم Attachedش ارسال می‌شود.
	pinned := &Conversation{ID: "c2", Messages: []Message{
		{ID: "u1", Role: "user", Content: "سنجاق", Tokens: 5, Attached: longAttach, Pinned: true},
		{ID: "u2", Role: "user", Content: "آخر", Tokens: 3},
	}}
	if gotP, wantP := conversationContextTokens(pinned), estimateTokens("سنجاق")+estimateTokens("آخر")+estimateTokens(longAttach); gotP != wantP {
		t.Fatalf("pinned attachment must be counted: got=%d want=%d", gotP, wantP)
	}
}

// TestContextTokensCountToolCalls آرگومان‌های tool_calls (که در درخواست بعدی
// دوباره فرستاده می‌شوند) نباید از قلم بیفتند.
func TestContextTokensCountToolCalls(t *testing.T) {
	args := `{"path":"src/main.go","content":"` + strings.Repeat("x", 400) + `"}`
	conv := &Conversation{Messages: []Message{
		{Role: "assistant", Content: "می‌نویسم", Tokens: estimateTokens("می‌نویسم"),
			ToolCalls: []toolCallRecord{{ID: "c1", Name: "write_file", Args: args}}},
		{Role: "tool", ToolCallID: "c1", ToolName: "write_file", Content: "ok"},
	}}
	got := conversationContextTokens(conv)
	withoutArgs := estimateTokens("می‌نویسم") + estimateTokens("ok")
	if got <= withoutArgs {
		t.Fatalf("tool_calls arguments must count in the context: got=%d without=%d", got, withoutArgs)
	}
	if want := withoutArgs + estimateTokens("write_file") + estimateTokens(args); got != want {
		t.Fatalf("context estimate mismatch: got=%d want=%d", got, want)
	}
}

// TestRingEstimateTracksRealRequestForTextTurn قرارداد ۴ (نوبت متنی).
func TestRingEstimateTracksRealRequestForTextTurn(t *testing.T) {
	env := newE2E(t, scenarioStep{
		Text:  strings.Repeat("پاسخ آزمایشی. ", 60),
		Usage: &sseUsage{PromptTokens: 1000, CompletionTokens: 120},
	})
	conv := env.send("سلام؛ کانتکست را اندازه بگیر")

	want := estimateSentMessagesForTest(t, env.app, conv.ID)
	if want <= 0 {
		t.Fatal("history estimate must be positive")
	}
	if conv.ContextTokens <= 0 {
		t.Fatalf("conversation must expose a context estimate: %+v", conv)
	}
	if ratio := float64(conv.ContextTokens) / float64(want); ratio < 0.6 || ratio > 1.6 {
		t.Fatalf("ring value must track the request that would be sent: ctx=%d history=%d ratio=%.2f", conv.ContextTokens, want, ratio)
	}
	if conv.ContextTokens > 3*want {
		t.Fatalf("context estimate must not be inflated by billing tokens: ctx=%d history=%d", conv.ContextTokens, want)
	}
}

// TestRingNotInflatedByAgentRounds قرارداد ۲: در نوبت چنددوری، Tokens پیام
// نهایی نباید مجموع completion همه‌ی دورها باشد.
func TestRingNotInflatedByAgentRounds(t *testing.T) {
	finalText := strings.Repeat("نتیجه‌ی نهایی کار. ", 100)
	env := newE2E(t,
		stepToolUsage("c1", "read_file", `{"path":"note.txt"}`, 500, 20),
		stepToolUsage("c2", "list_dir", `{"path":"."}`, 600, 25),
		scenarioStep{Text: finalText, Usage: &sseUsage{PromptTokens: 700, CompletionTokens: 30}},
	)
	env.writeFile("note.txt", strings.Repeat("line\n", 30))
	conv := env.send("پروژه را بررسی کن")

	// عدد دایره باید به تاریخی که واقعاً ارسال می‌شود نزدیک بماند و نباید
	// completion کل نوبت (۲۰+۲۵+۳۰) را چند بار حساب کند.
	want := estimateSentMessagesForTest(t, env.app, conv.ID)
	if ratio := float64(conv.ContextTokens) / float64(want); ratio < 0.6 || ratio > 1.6 {
		t.Fatalf("agent turn ring drifted from the real request: ctx=%d history=%d ratio=%.2f", conv.ContextTokens, want, ratio)
	}
}

// TestRingExactnessRequiresPromptTokens قرارداد ۳: اگر سرویس‌دهنده فقط
// completion_tokens بفرستد، عدد کانتکست تخمینی است و نباید «دقیق» برچسب بخورد.
func TestRingExactnessRequiresPromptTokens(t *testing.T) {
	env := newE2E(t, scenarioStep{
		Text:  "پاسخ بدون prompt_tokens",
		Usage: &sseUsage{CompletionTokens: 40},
	})
	conv := env.send("سلام")

	if conv.TokensExact {
		t.Fatalf("TokensExact must stay false when prompt_tokens was never reported: %+v", conv)
	}
	done := env.lastEvent("chat:done")
	if done == nil {
		t.Fatal("chat:done must be emitted")
	}
	if exact, _ := done.data[0].(map[string]interface{})["tokensExact"].(bool); exact {
		t.Fatalf("chat:done must not claim exact usage: %+v", done.data[0])
	}
	usage := env.lastEvent("chat:usage")
	if usage == nil {
		t.Fatal("chat:usage must be emitted")
	}
	m, _ := usage.data[0].(map[string]interface{})
	if exact, _ := m["exact"].(bool); exact {
		t.Fatalf("live usage must not claim exactness without prompt tokens: %+v", m)
	}
	if ctx, _ := m["contextTokens"].(int64); ctx <= 0 {
		t.Fatalf("live context must fall back to a positive estimate: %+v", m)
	}
}

// TestRingExactWhenPromptReported حالت سالم: prompt واقعی → «دقیق».
func TestRingExactWhenPromptReported(t *testing.T) {
	env := newE2E(t, scenarioStep{
		Text:  "پاسخ",
		Usage: &sseUsage{PromptTokens: 321, CompletionTokens: 12},
	})
	conv := env.send("سلام")
	if !conv.TokensExact {
		t.Fatalf("TokensExact must be true when the provider reported prompt+completion: %+v", conv)
	}
	usage := env.lastEvent("chat:usage")
	if usage == nil {
		t.Fatal("chat:usage must be emitted")
	}
	m, _ := usage.data[0].(map[string]interface{})
	if ctx, _ := m["contextTokens"].(int64); ctx != 321 {
		t.Fatalf("live context must be the real prompt tokens (321): %+v", m)
	}
	if exact, _ := m["exact"].(bool); !exact {
		t.Fatalf("live usage must be exact when prompt tokens are real: %+v", m)
	}
}

// TestRingWidgetContract قرارداد فرانت‌اند دایره: مقدار درصد از همان سقف
// کانتکست محاسبه و روی ring ست می‌شود، رویداد زنده/پایان به آن وصل است و
// تخمین سمت کلاینت با بک‌اند هم‌قاعده است (thinking و پیوست‌های ارسال‌نشده
// شمرده نمی‌شوند، آرگومان‌های tool_calls شمرده می‌شوند).
func TestRingWidgetContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("frontend", "dist", "assets", "js", "app.js"))
	if err != nil {
		t.Fatalf("app.js: %v", err)
	}
	js := string(raw)
	for _, want := range []string{
		`#usage-ring`,                   // عنصر دایره
		`style.setProperty("--usage-p"`, // مقدار پرشدگی
		`#usage-pct`,                    // عدد درصد
		`Number(t.tokenLimit)`,          // سقف از view (نه عدد ثابت)
		`"chat:usage"`,                  // مصرف زنده
		`Math.min(100,Math.round(ctx/Math.max(limit,1)*100))`,
		`n.toolCalls`,                 // آرگومان‌های ابزار در تخمین کلاینت
		`i===last?estTok(n.attached)`, // پیوست فقط برای آخرین پیام کاربر
		`r.streaming&&lu.exact`,       // برچسب دقیق در حالت زنده
		`"منبع: تخمین محلی`,           // شفاف‌سازی منبع عدد
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("usage ring wiring missing %q", want)
		}
	}
	if strings.Contains(js, `+estTok(n&&n.thinking)`) {
		t.Fatal("client context estimate must not count thinking (it is never sent to the model)")
	}
}
