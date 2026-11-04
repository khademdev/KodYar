// سناریوهای سرتاسری ایجنت (مأموریت ۳ — مرحله D، هسته‌ی مشترک با A.2/A.4).
//
// یک سرور OpenAI-compatible واقعی (httptest + SSE) با «فیلم‌نامه» اجرا
// می‌شود: هر درخواست یک مرحله (متن و/یا فراخوانی ابزار) می‌گیرد، مرحله
// می‌تواند بدنه‌ی درخواست را بازرسی کند، و حلقه‌ی واقعی streamChat روی یک
// پوشه‌ی کاری موقت اجرا می‌شود. کاربر هم «فیلم‌نامه‌ای» است: هر مودال
// دسترسی ثبت و بر اساس تصمیم سناریو پاسخ داده می‌شود. بدون شبکه‌ی واقعی،
// بدون خواب طولانی.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------- فیلم‌نامه

// scriptCall یک فراخوانی ابزار در پاسخ مدل.
type scriptCall struct {
	ID   string
	Name string
	Args string
}

// scenarioStep یک پاسخ مدل: متن و/یا فراخوانی ابزار + usage اختیاری +
// بازرسی اختیاری بدنه‌ی درخواست.
type scenarioStep struct {
	Text    string
	Calls   []scriptCall
	Usage   *sseUsage
	Check   func(t *testing.T, req chatRequest)
	ReqNote string // برچسب مرحله در پیام‌های خطا
}

// scriptedModel سرور فیلم‌نامه‌ای سازگار با OpenAI (SSE).
type scriptedModel struct {
	t    *testing.T
	srv  *httptest.Server
	mu   sync.Mutex
	ln   int
	body []chatRequest
	// steps فیلم‌نامه؛ هر درخواست (که route آن را نگیرد) یک مرحله مصرف می‌کند.
	steps []scenarioStep
	// route وقتی غیرnil باشد، مرحله را بر اساس درخواست انتخاب می‌کند
	// (مثلاً زیرایجنت از گفتگوی اصلی جدا شود)؛ خروجی nil → مرحله‌ی بعدی.
	route func(req chatRequest) *scenarioStep
	// fallback پاسخ پس از پایان مراحل (پیش‌فرض «تمام شد»).
	fallback string
}

// newScriptedModel سرور را روی مراحل داده‌شده بالا می‌آورد.
func newScriptedModel(t *testing.T, steps ...scenarioStep) *scriptedModel {
	t.Helper()
	m := &scriptedModel{t: t, fallback: "تمام شد.", steps: steps}
	m.srv = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.srv.Close)
	return m
}

// URL آدرس پایه برای Provider.
func (m *scriptedModel) URL() string { return m.srv.URL }

// requests کپی درخواست‌های دریافت‌شده.
func (m *scriptedModel) requests() []chatRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]chatRequest(nil), m.body...)
}

// count تعداد درخواست‌ها.
func (m *scriptedModel) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.body)
}

func (m *scriptedModel) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	var req chatRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	idx := m.ln
	m.body = append(m.body, req)
	step := (*scenarioStep)(nil)
	if m.route != nil {
		step = m.route(req)
	}
	if step == nil {
		// فقط درخواست‌های «گفتگوی اصلی» یک مرحله مصرف می‌کنند؛ درخواست
		// مسیریابی‌شده (مثلاً زیرایجنت) ترتیب فیلم‌نامه را جابه‌جا نمی‌کند.
		m.ln++
		if idx < len(m.steps) {
			step = &m.steps[idx]
		}
	}
	m.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	if step == nil {
		writeSSEChunk(w, map[string]interface{}{
			"choices": []map[string]interface{}{{"delta": map[string]string{"content": m.fallback}}},
		})
		flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush()
		return
	}
	if step.Check != nil {
		step.Check(m.t, req)
	}
	for _, c := range step.Calls {
		writeSSEChunk(w, map[string]interface{}{
			"choices": []map[string]interface{}{{
				"delta": map[string]interface{}{
					"tool_calls": []map[string]interface{}{{
						"index": 0, "id": c.ID, "type": "function",
						"function": map[string]string{"name": c.Name, "arguments": c.Args},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		})
	}
	if step.Text != "" {
		writeSSEChunk(w, map[string]interface{}{
			"choices": []map[string]interface{}{{"delta": map[string]string{"content": step.Text}}},
		})
	}
	if len(step.Calls) == 0 {
		writeSSEChunk(w, map[string]interface{}{
			"choices": []map[string]interface{}{{"delta": map[string]string{}, "finish_reason": "stop"}},
		})
	}
	if step.Usage != nil {
		writeSSEChunk(w, map[string]interface{}{"choices": []interface{}{}, "usage": step.Usage})
	}
	flush()
	fmt.Fprint(w, "data: [DONE]\n\n")
	flush()
}

// writeSSEChunk یک chunk با پیشوند data: می‌نویسد.
func writeSSEChunk(w io.Writer, v interface{}) {
	raw, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", raw)
}

// ---------------------------------------------------------------- محیط اجرا

// stepBuilder کمکی برای ساخت فیلم‌نامه.
func stepText(text string) scenarioStep { return scenarioStep{Text: text} }

func stepTool(id, name, args string) scenarioStep {
	return scenarioStep{Calls: []scriptCall{{ID: id, Name: name, Args: args}}}
}

func stepToolUsage(id, name, args string, prompt, completion int64) scenarioStep {
	return scenarioStep{
		Calls: []scriptCall{{ID: id, Name: name, Args: args}},
		Usage: &sseUsage{PromptTokens: prompt, CompletionTokens: completion},
	}
}

// e2eEnv محیط یک سناریو: اپ، سرور و رویدادهای ثبت‌شده.
type e2eEnv struct {
	t       *testing.T
	app     *App
	model   *scriptedModel
	convID  string
	mu      sync.Mutex
	events  []e2eEvent
	asks    []string
	decide  string // پاسخ به مودال‌های دسترسی (allow/reject)
	summary []TurnSummary
	notices []string
}

type e2eEvent struct {
	name string
	data []interface{}
}

// newE2E یک محیط کامل می‌سازد: پوشه‌ی کاری موقت، گفتگو، سرور فیلم‌نامه‌ای و
// ثبت همه‌ی رویدادها. با perm=false مودال‌های دسترسی خودکار پذیرفته می‌شوند
// (کاربر فیلم‌نامه‌ای).
func newE2E(t *testing.T, steps ...scenarioStep) *e2eEnv {
	t.Helper()
	model := newScriptedModel(t, steps...)
	a := chatAppOnServer(t, model.URL())
	a.mu.Lock()
	a.settings.AgentMode = true
	a.settings.AllowCommands = true
	a.settings.MaxHistory = 200
	a.settings.MaxAgentRounds = 20
	a.mu.Unlock()

	env := &e2eEnv{t: t, app: a, model: model, decide: permRespondAllow}
	// ctx غیرnil لازم است تا مودال‌ها قابل «پاسخ دادن» باشند؛ uiReady
	// false می‌ماند، پس هیچ رویدادی به Wails نمی‌رود (eventEmit جایگزین).
	a.ctx = context.Background()
	a.eventEmit = func(name string, data []interface{}) {
		env.mu.Lock()
		env.events = append(env.events, e2eEvent{name: name, data: data})
		env.mu.Unlock()
	}
	a.permAskEmit = func(ask *permissionAsk) {
		env.mu.Lock()
		env.asks = append(env.asks, ask.Kind+":"+ask.Command)
		decide := env.decide
		env.mu.Unlock()
		_ = a.RespondPermissionConfirm(ask.ID, decide)
	}

	conv := &Conversation{ID: newID(), Title: "e2e", CreatedAt: time.Now().Unix()}
	a.mu.Lock()
	if err := a.saveConversation(conv); err != nil {
		a.mu.Unlock()
		t.Fatal(err)
	}
	a.mu.Unlock()
	env.convID = conv.ID
	return env
}

// ws پوشه‌ی کاری.
func (e *e2eEnv) ws() string { return e.app.settings.WorkspacePath }

// writeFile فایل را مستقیم روی دیسک می‌نویسد (نه با ابزار ایجنت).
func (e *e2eEnv) writeFile(rel, content string) {
	e.t.Helper()
	p := filepath.Join(e.ws(), rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// readFile محتوای فایل در پوشه‌ی کاری.
func (e *e2eEnv) readFile(rel string) string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.ws(), rel))
	if err != nil {
		return ""
	}
	return string(data)
}

// send یک پیام کاربر می‌فرستد و تا پایان کامل نوبت صبر می‌کند.
func (e *e2eEnv) send(text string) *Conversation {
	e.t.Helper()
	conv, err := e.app.SendMessage(e.convID, text, nil)
	if err != nil {
		e.t.Fatalf("SendMessage: %v", err)
	}
	waitStreamDone(e.t, e.app)
	out, err := e.app.loadConversationForTest(conv.ID)
	if err != nil {
		e.t.Fatalf("load: %v", err)
	}
	return out
}

// loadConversationForTest خواندن گفتگو با قفل.
func (a *App) loadConversationForTest(id string) (*Conversation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loadConversation(id)
}

// eventsByName رویدادهای یک نام.
func (e *e2eEnv) eventsByName(name string) []e2eEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []e2eEvent
	for _, ev := range e.events {
		if ev.name == name {
			out = append(out, ev)
		}
	}
	return out
}

// summaries خلاصه‌های نوبت منتشرشده.
func (e *e2eEnv) summaries() []TurnSummary {
	var out []TurnSummary
	for _, ev := range e.eventsByName("chat:turn_summary") {
		if len(ev.data) == 0 {
			continue
		}
		m, ok := ev.data[0].(map[string]interface{})
		if !ok {
			continue
		}
		if s, ok := m["summary"].(TurnSummary); ok {
			out = append(out, s)
		}
	}
	return out
}

// notices متن اعلان‌های chat:notice.
func (e *e2eEnv) noticesText() []string {
	var out []string
	for _, ev := range e.eventsByName("chat:notice") {
		if len(ev.data) == 0 {
			continue
		}
		if m, ok := ev.data[0].(map[string]interface{}); ok {
			if s, ok := m["text"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// askCount تعداد مودال‌های دسترسی نمایش‌داده‌شده.
func (e *e2eEnv) askCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.asks)
}

// lastAssistant آخرین پیام دستیار گفتگو.
func lastAssistant(conv *Conversation) string {
	for i := len(conv.Messages) - 1; i >= 0; i-- {
		if conv.Messages[i].Role == "assistant" {
			return conv.Messages[i].Content
		}
	}
	return ""
}

// requestMessages متن همه‌ی پیام‌های یک درخواست (برای جستجوی تذکر/نشانگر).
func requestMessages(req chatRequest) string {
	var b strings.Builder
	for _, m := range req.Messages {
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(historyContentString(m.Content))
		b.WriteString("\n")
		for _, tc := range m.ToolCalls {
			b.WriteString("  tool_call ")
			b.WriteString(tc.Function.Name)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// ---------------------------------------------------------------- A.2 — تذکر

func TestNudgeFiresOnceWhenUnverified(t *testing.T) {
	env := newE2E(t,
		stepTool("c1", "write_file", `{"path":"n.txt","content":"hello\n"}`),
		stepText("انجام شد."),
		stepText("نتوانستم تأیید کنم."),
	)
	env.send("فایل بساز")

	if n := env.model.count(); n != 3 {
		t.Fatalf("expected 3 model requests (write → final → nudged final), got %d", n)
	}
	reqs := env.model.requests()
	if strings.Contains(requestMessages(reqs[1]), verificationNudgeText) {
		t.Fatal("no nudge may be injected before the model's first final answer")
	}
	if !strings.Contains(requestMessages(reqs[2]), verificationNudgeText) {
		t.Fatalf("nudge text must be in the request after the unverified final answer:\n%s", requestMessages(reqs[2]))
	}
	// فقط یک تذکر در کل نوبت
	total := 0
	for _, r := range reqs {
		total += strings.Count(requestMessages(r), verificationNudgeText)
	}
	if total != 1 {
		t.Fatalf("nudge must appear exactly once in all requests, got %d", total)
	}
	// خلاصه‌ی نوبت: راستی‌آزمایی نشده
	ss := env.summaries()
	if len(ss) != 1 {
		t.Fatalf("expected one turn summary, got %d", len(ss))
	}
	if ss[0].Verification.OK {
		t.Fatalf("verification must be reported as not verified: %+v", ss[0].Verification)
	}
	if len(ss[0].Files) != 1 || ss[0].Files[0].Path != "n.txt" {
		t.Fatalf("summary files: %+v", ss[0].Files)
	}
}

func TestNoNudgeWhenVerified(t *testing.T) {
	env := newE2E(t,
		stepTool("c1", "write_file", `{"path":"main.go","content":"package main\n\nfunc main() {}\n"}`),
		stepTool("c2", "write_file", `{"path":"go.mod","content":"module e2e\n\ngo 1.22\n"}`),
		stepTool("c3", "run_command", `{"command":"go vet ./..."}`),
		stepText("ساخته و تأیید شد."),
	)
	env.send("پروژه بساز")
	for i, r := range env.model.requests() {
		if strings.Contains(requestMessages(r), verificationNudgeText) {
			t.Fatalf("no nudge expected when verification passed (request %d)", i)
		}
	}
	ss := env.summaries()
	if len(ss) != 1 || !ss[0].Verification.OK {
		t.Fatalf("turn must be reported as verified: %+v", ss)
	}
	if ss[0].Verification.Command == "" {
		t.Fatal("verification command must be recorded")
	}
}

func TestNoNudgeWithoutFileChanges(t *testing.T) {
	env := newE2E(t, stepText("فقط پاسخ متنی."))
	env.send("سلام")
	if n := env.model.count(); n != 1 {
		t.Fatalf("plain answer must be a single request, got %d", n)
	}
	if strings.Contains(requestMessages(env.model.requests()[0]), verificationNudgeText) {
		t.Fatal("no nudge for a turn without file changes")
	}
}

// تذکر تنها زمانی می‌آید که راستی‌آزمایی «موفق» نبوده باشد؛ تست خراب یعنی
// همچنان تأییدنشده.
func TestNudgeAfterFailingVerification(t *testing.T) {
	env := newE2E(t,
		stepTool("c1", "write_file", `{"path":"x.txt","content":"x\n"}`),
		stepTool("c2", "run_command", `{"command":"go test ./..."}`), // پوشه Go نیست → شکست
		stepText("تست شکست خورد."),
		stepText("تأیید ممکن نشد."),
	)
	env.send("کار کن")
	reqs := env.model.requests()
	if len(reqs) < 4 {
		t.Fatalf("expected a nudge round, got %d requests", len(reqs))
	}
	if !strings.Contains(requestMessages(reqs[3]), verificationNudgeText) {
		t.Fatal("a failed verification must still trigger the nudge")
	}
	ss := env.summaries()
	if len(ss) == 0 || ss[0].Verification.OK {
		t.Fatalf("failed test must not count as verified: %+v", ss)
	}
}

// ---------------------------------------------------------------- A.4 — سقف توکن

func TestTokenCapStopsTurnGracefully(t *testing.T) {
	env := newE2E(t,
		stepToolUsage("c1", "write_file", `{"path":"t.txt","content":"a\n"}`, 150, 0),
		stepText("ادامه…"),
	)
	env.app.mu.Lock()
	env.app.settings.MaxTurnTokens = 100
	env.app.mu.Unlock()

	conv := env.send("یک فایل بساز")
	if n := env.model.count(); n != 1 {
		t.Fatalf("cap must stop the turn after the current round, got %d requests", n)
	}
	if last := lastAssistant(conv); !strings.Contains(last, "سقف توکن هر نوبت") {
		t.Fatalf("graceful stop notice missing: %q", last)
	}
	// گفتگو در وضعیت قابل‌ادامه می‌ماند: نتیجه‌ی ابزار همان دور حفظ شده و
	// آخرین پیام user نیست (ResumeAgent همین را می‌پذیرد).
	toolMsgs := 0
	for _, m := range conv.Messages {
		if m.Role == "tool" {
			toolMsgs++
		}
	}
	if toolMsgs == 0 {
		t.Fatal("the executed tool round must be persisted before the cap stop")
	}
	if n := len(conv.Messages); n == 0 || conv.Messages[n-1].Role == "user" {
		t.Fatalf("conversation must stay resumable, last role=%q", conv.Messages[n-1].Role)
	}
	ss := env.summaries()
	if len(ss) != 1 || !ss[0].TokenCapHit || ss[0].TokenCap != 100 {
		t.Fatalf("turn summary must note the cap: %+v", ss)
	}
}

func TestTokenCapWarnsOnce(t *testing.T) {
	// دور ۱: ۸۵٪ سقف (هشدار، بدون توقف) — دور ۲: ۹۵٪ (بدون هشدار دوم)
	env := newE2E(t,
		stepToolUsage("c1", "write_file", `{"path":"w.txt","content":"a\n"}`, 8500, 0),
		stepToolUsage("c2", "write_file", `{"path":"w2.txt","content":"b\n"}`, 1000, 0),
		stepText("تمام."),
	)
	env.app.mu.Lock()
	env.app.settings.MaxTurnTokens = 10000
	env.app.mu.Unlock()
	env.send("کار کن")

	warn := 0
	for _, n := range env.noticesText() {
		if strings.Contains(n, "۸۰٪") {
			warn++
		}
	}
	if warn != 1 {
		t.Fatalf("80%% warning must fire exactly once, got %d (notices: %v)", warn, env.noticesText())
	}
}

func TestTokenCapZeroDisables(t *testing.T) {
	env := newE2E(t,
		stepToolUsage("c1", "write_file", `{"path":"z.txt","content":"a\n"}`, 5000, 0),
		stepText("ادامه می‌دهم."),
		stepText("تمام."),
	)
	env.app.mu.Lock()
	env.app.settings.MaxTurnTokens = 0
	env.app.mu.Unlock()
	conv := env.send("کار کن")
	if n := env.model.count(); n < 2 {
		t.Fatalf("disabled cap must not stop the turn, got %d requests", n)
	}
	if strings.Contains(lastAssistant(conv), "سقف توکن") {
		t.Fatal("disabled cap must not emit the stop notice")
	}
}

func TestTokenCapCountsSubagentUsage(t *testing.T) {
	env := newE2E(t,
		stepTool("c1", "write_file", `{"path":"s.txt","content":"a\n"}`),
		stepTool("c2", "task", `{"description":"بررسی ساختار پروژه"}`),
		stepText("تمام."),
	)
	// زیرایجنت: مرحله‌ی مخصوص خودش (system prompt متفاوت)
	env.model.route = func(req chatRequest) *scenarioStep {
		if len(req.Messages) > 0 && strings.Contains(historyContentString(req.Messages[0].Content), "You are MyAI's read-only sub-agent") {
			return &scenarioStep{Text: "گزارش زیرایجنت.", Usage: &sseUsage{PromptTokens: 5000, CompletionTokens: 10}}
		}
		return nil
	}
	env.app.mu.Lock()
	env.app.settings.MaxTurnTokens = 4000 // مصرف زیرایجنت باید نوبت را متوقف کند
	env.app.mu.Unlock()

	conv := env.send("کار کن")
	if strings.Contains(lastAssistant(conv), "سقف توکن") == false {
		t.Fatalf("sub-agent usage must count toward the cap; last=%q requests=%d", lastAssistant(conv), env.model.count())
	}
}

func TestTokenCapAllowsResume(t *testing.T) {
	env := newE2E(t,
		stepToolUsage("c1", "write_file", `{"path":"r.txt","content":"a\n"}`, 200, 0),
		stepText("ادامه دادم و تمام شد."),
	)
	env.app.mu.Lock()
	env.app.settings.MaxTurnTokens = 120
	env.app.mu.Unlock()
	env.send("شروع کن")

	// سقف را برمی‌داریم و «ادامه» را می‌زنیم
	env.app.mu.Lock()
	env.app.settings.MaxTurnTokens = 0
	env.app.mu.Unlock()
	if _, err := env.app.ResumeAgent(env.convID); err != nil {
		t.Fatalf("ResumeAgent: %v", err)
	}
	waitStreamDone(t, env.app)
	conv, err := env.app.loadConversationForTest(env.convID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lastAssistant(conv), "تمام شد") {
		t.Fatalf("resume must continue the turn, last=%q", lastAssistant(conv))
	}
}

// ---------------------------------------------------------------- A.5 — نشانگر

func TestContextDropMarkerKeepsTodos(t *testing.T) {
	todosBlock := "- [in_progress] پیاده‌سازی محافظ\n- [pending] نوشتن تست"
	h := []chatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: contextDropMarkerText},
		{Role: "assistant", Content: "ok"},
	}
	out := enrichContextDropMarker(h, todosBlock)
	markers := 0
	for _, m := range out {
		if isContextDropMarker(m) {
			markers++
			if !strings.Contains(historyContentString(m.Content), "پیاده‌سازی محافظ") {
				t.Fatalf("marker must contain the todo list: %q", historyContentString(m.Content))
			}
		}
	}
	if markers != 1 {
		t.Fatalf("exactly one marker expected, got %d", markers)
	}
	// idempotent: اجرای دوباره نشانگر دوم نمی‌سازد و متن انباشته نمی‌شود
	again := enrichContextDropMarker(out, todosBlock)
	markers = 0
	for _, m := range again {
		if isContextDropMarker(m) {
			markers++
			if strings.Count(historyContentString(m.Content), "پیاده‌سازی محافظ") != 1 {
				t.Fatalf("marker content accumulated: %q", historyContentString(m.Content))
			}
		}
	}
	if markers != 1 || len(again) != len(out) {
		t.Fatalf("enrich must be idempotent (markers=%d len=%d/%d)", markers, len(again), len(out))
	}
	// بدون بلوک کارها نشانگر دست‌نخورده می‌ماند
	if got := enrichContextDropMarker(h, ""); len(got) != len(h) {
		t.Fatal("empty todo block must not change the history")
	}
}

func TestTurnRecorderTodosBlock(t *testing.T) {
	rec := &turnRecorder{}
	if rec.todosBlock() != "" || rec.hasTodos() {
		t.Fatal("empty recorder must have no todo block")
	}
	items := make([]TodoItem, 0, contextTodosMaxItems+5)
	for i := 0; i < contextTodosMaxItems+5; i++ {
		status := "pending"
		if i == 0 {
			status = "in_progress"
		}
		items = append(items, TodoItem{Content: "کار " + itoa(i) + "\u200cی", Status: status})
	}
	rec.setTodos(items)
	if !rec.hasTodos() {
		t.Fatal("hasTodos must be true after setTodos")
	}
	block := rec.todosBlock()
	if !strings.Contains(block, "[in_progress]") {
		t.Fatalf("status must be in the block: %q", block)
	}
	// سقف موارد + یک خط «… more item(s)»
	if n := strings.Count(block, "\n- "); n > contextTodosMaxItems+1 {
		t.Fatalf("todo block must be bounded: %d lines", n)
	}
	if !strings.Contains(block, "more item(s)") {
		t.Fatalf("truncation must be announced: %q", block)
	}
	if !todoItemsValidUTF8(items) {
		t.Fatal("test fixture must stay valid UTF-8")
	}
}
