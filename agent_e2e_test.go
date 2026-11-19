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
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
// Hold/Reached فقط برای سناریوهای «توقف در لحظه‌های مختلف» (D.1.11):
// وقتی سرور به این مرحله می‌رسد Reached را سیگنال می‌کند و پاسخ را تا
// بسته‌شدن Hold نگه می‌دارد تا تست دقیقاً حین استریم Stop بزند.
type scenarioStep struct {
	Text    string
	Calls   []scriptCall
	Usage   *sseUsage
	Check   func(t *testing.T, req chatRequest)
	ReqNote string // برچسب مرحله در پیام‌های خطا
	Hold    <-chan struct{}
	Reached chan<- struct{}
	// Status (غیرصفر) پاسخ HTTP با همین کد را برمی‌گرداند (تزریق 429/5xx)؛
	// Raw (غیرخالی) بدنه‌ی خام را به‌جای SSE سالم می‌نویسد (SSE خراب)؛
	// Stall پس از نوشتن محتوای مرحله، اتصال را باز نگه می‌دارد و دیگر هیچ
	// بایتی نمی‌فرستد (تزریق سکون وسط استریم — نگهبان Stage 2).
	Status int
	Raw    string
	Stall  bool
}

// addSteps مراحل بیشتری به فیلم‌نامه اضافه می‌کند (soak و تزریق خرابی).
func (m *scriptedModel) addSteps(steps ...scenarioStep) {
	m.mu.Lock()
	m.steps = append(m.steps, steps...)
	m.mu.Unlock()
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

	if step != nil && step.Status != 0 {
		w.Header().Set("Content-Type", "application/json")
		if step.Status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "0")
		}
		w.WriteHeader(step.Status)
		fmt.Fprintf(w, `{"error":{"message":"injected status %d"}}`, step.Status)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	if step != nil && step.Raw != "" {
		flush()
		fmt.Fprint(w, step.Raw)
		flush()
		return
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
	if step.Reached != nil {
		step.Reached <- struct{}{}
	}
	if step.Hold != nil {
		<-step.Hold
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
	if step.Stall {
		// سکون: بدون [DONE] و بدون بستن اتصال — تا لغو سمت کلاینت (نگهبان
		// بیکاری Stage 2 یا Stop کاربر). هیچ بایت دیگری نمی‌آید.
		flush()
		<-r.Context().Done()
		return
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
	// fileAsks مسیر مودال‌های Diff (agent:confirm) — مسیرهای محافظت‌شده و
	// review_each حتی در حالت auto مودال می‌گیرند (بند D.1.6).
	fileAsks []string
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
	env := initE2E(t, a)
	env.model = model
	return env
}

// initE2E بقیهٔ سیم‌کشی محیط را روی اپی که از قبل ساخته شده انجام می‌دهد
// (اجرای زندهٔ ارزیابی هم از همین مسیر استفاده می‌کند تا رفتار ایجنت یکسان بماند).
func initE2E(t *testing.T, a *App) *e2eEnv {
	t.Helper()
	a.mu.Lock()
	a.settings.AgentMode = true
	a.settings.AllowCommands = true
	a.settings.MaxHistory = 200
	a.settings.MaxAgentRounds = 20
	a.mu.Unlock()

	env := &e2eEnv{t: t, app: a, decide: permRespondAllow}
	// ctx غیرnil لازم است تا مودال‌ها قابل «پاسخ دادن» باشند؛ uiReady
	// false می‌ماند، پس هیچ رویدادی به Wails نمی‌رود (eventEmit جایگزین).
	a.ctx = context.Background()
	a.eventEmit = func(name string, data []interface{}) {
		env.mu.Lock()
		env.events = append(env.events, e2eEvent{name: name, data: data})
		env.mu.Unlock()
		// کاربر فیلم‌نامه‌ای برای مودال Diff: ثبت مسیر و تأیید.
		// کانال تأیید بافر ۱ دارد، پس پاسخِ هم‌زمان (همان گوروتین emit)
		// هرگز بلاک نمی‌شود — شبیه پاسخ فوری کاربر.
		if name == "agent:confirm" && len(data) > 0 {
			if m, ok := data[0].(map[string]interface{}); ok {
				if ch, ok := m["change"].(*FileChange); ok && ch != nil {
					env.mu.Lock()
					env.fileAsks = append(env.fileAsks, ch.Path)
					env.mu.Unlock()
					_ = a.RespondFileConfirm(ch.ID, true)
				}
			}
		}
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
	return e.sendWithin(text, 30*time.Second)
}

// sendWithin مثل send با مهلت دلخواه برای نوبت (اجرای زنده با مدل واقعی).
func (e *e2eEnv) sendWithin(text string, limit time.Duration) *Conversation {
	e.t.Helper()
	conv, err := e.app.SendMessage(e.convID, text, nil)
	if err != nil {
		e.t.Fatalf("SendMessage: %v", err)
	}
	waitStreamDoneWithin(e.t, e.app, limit)
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

// TestVerificationNudgeStaysSingle: پایان نوبت بعد از تغییر فایل بدون
// راستی‌آزمایی، یک تذکر می‌گیرد (سیاست A.2، حالا داخل دروازهٔ پایان کار) و
// چک‌لیست باز تا دو تذکر.
func TestVerificationNudgeStaysSingle(t *testing.T) {
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
	maxSeen := 0
	for _, r := range reqs {
		if c := strings.Count(requestMessages(r), verificationNudgeText); c > maxSeen {
			maxSeen = c
		}
	}
	if maxSeen != 1 {
		t.Fatalf("verification reminder must appear once, got %d", maxSeen)
	}
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
		stepTool("c3", "run_command", `{"command":"go test ./..."}`),
		stepText("ساخته و تست شد."),
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

// TestBuildOnlyVerificationNudgesTest: vet/build تحلیل ایستاست؛ پایان نوبت
// با «فقط build» باید یک‌بار تذکر تست بدهد (و همان یک تذکر کافی است).
func TestBuildOnlyVerificationNudgesTest(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	env := newE2E(t,
		stepTool("c1", "write_file", `{"path":"main.go","content":"package main\n\nfunc main() {}\n"}`),
		stepTool("c2", "run_command", `{"command":"go build ./..."}`),
		stepText("ساخت موفق بود."),
		stepText("تستی اجرا نشد."),
	)
	env.writeFile("go.mod", "module e2e\n\ngo 1.22\n")
	env.send("پروژه را بساز")

	reqs := env.model.requests()
	if len(reqs) != 4 {
		t.Fatalf("expected write → build → final → nudged final (4 requests), got %d", len(reqs))
	}
	if strings.Contains(requestMessages(reqs[2]), verificationNudgeText) {
		t.Fatal("no nudge before the model's first final answer")
	}
	if !strings.Contains(requestMessages(reqs[3]), "build-only") {
		t.Fatalf("a build-only verification must nudge for a test:\n%s", requestMessages(reqs[3]))
	}
	if strings.Contains(requestMessages(reqs[3]), "no successful verification") {
		t.Fatal("build succeeded, so the nudge must not claim there was no verification")
	}
	maxSeen := 0
	for _, r := range reqs {
		if c := strings.Count(requestMessages(r), verificationNudgeText); c > maxSeen {
			maxSeen = c
		}
	}
	if maxSeen != 1 {
		t.Fatalf("the test nudge must appear once, got %d", maxSeen)
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

// ---------------------------------------------------------------- D.1 — سناریوها

// TestScenarioNormalFixVerifiedNoModals — D.1.1: خواندن → ویرایش → go test
// موفق، در حالت auto: صفر مودال، تغییر روی دیسک، خلاصه «تأییدشده».
func TestScenarioNormalFixVerifiedNoModals(t *testing.T) {
	env := newE2E(t,
		stepTool("c1", "read_file", `{"path":"calc.go"}`),
		stepTool("c2", "edit_file", `{"path":"calc.go","old_string":"return a - b","new_string":"return a + b"}`),
		stepTool("c3", "run_command", `{"command":"go test ./..."}`),
		stepText("تست‌ها سبز شد؛ جمع درست شد."),
	)
	env.writeFile("go.mod", "module pw\n\ngo 1.21\n")
	env.writeFile("calc.go", "package pw\n\nfunc Add(a, b int) int { return a - b }\n")
	env.writeFile("calc_test.go", "package pw\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatalf(\"Add(2,3) = %d\", Add(2, 3))\n\t}\n}\n")

	env.send("باگ تابع Add را درست کن")

	if got := env.readFile("calc.go"); !strings.Contains(got, "a + b") {
		t.Fatalf("edit was not applied on disk: %q", got)
	}
	if n := env.askCount(); n != 0 {
		t.Fatalf("auto mode must not ask anything, got %d ask(s): %v", n, env.asks)
	}
	sm := env.summaries()
	if len(sm) == 0 {
		t.Fatal("no turn summary was published")
	}
	last := sm[len(sm)-1]
	if !last.Verification.OK {
		t.Fatalf("summary must report a successful verification: %+v", last.Verification)
	}
	if len(last.Files) == 0 {
		t.Fatalf("summary must list the changed file: %+v", last)
	}
	if !strings.Contains(lastAssistant(env.lastConv(t)), "جمع") {
		t.Fatal("final answer must be kept")
	}
}

// lastConv گفتگوی فعلی را می‌خواند.
func (e *e2eEnv) lastConv(t *testing.T) *Conversation {
	t.Helper()
	conv, err := e.app.loadConversationForTest(e.convID)
	if err != nil {
		t.Fatal(err)
	}
	return conv
}

// ---------------------------------------------------------------- D.1 — سناریوهای ۳ تا ۱۱

// کمکی‌های مشترک سناریوها

// toolResult نتیجه‌ی ابزار با شناسه‌ی فراخوانی داده‌شده را از گفتگو می‌خواند.
func (e *e2eEnv) toolResult(t *testing.T, callID string) string {
	t.Helper()
	conv := e.lastConv(t)
	for _, m := range conv.Messages {
		if m.Role == "tool" && m.ToolCallID == callID {
			return m.Content
		}
	}
	return ""
}

// lastAborted آخرین پیام دستیارِ لغوشده.
func (e *e2eEnv) lastAborted(t *testing.T) bool {
	t.Helper()
	conv := e.lastConv(t)
	for i := len(conv.Messages) - 1; i >= 0; i-- {
		if conv.Messages[i].Role == "assistant" {
			return conv.Messages[i].Aborted
		}
	}
	return false
}

// stepToolCheck مثل stepTool با بازرسی بدنه‌ی درخواست.
func stepToolCheck(id, name, args string, check func(t *testing.T, req chatRequest)) scenarioStep {
	s := stepTool(id, name, args)
	s.Check = check
	return s
}

// waitReached با مهلت، رسیدن استریم به مرحله‌ی نگه‌داشته‌شده را صبر می‌کند.
func waitReached(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("stream never reached the held step")
	}
}

// initGitWorkspace پوشه‌ی کاری را به مخزن git با یک کامیت اولیه تبدیل می‌کند
// (سناریوی checkpoint — نقطه‌ی بازگشت فقط در مخزن git ساخته می‌شود).
func (e *e2eEnv) initGitWorkspace(t *testing.T) {
	t.Helper()
	ws := e.ws()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", ws}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=tester", "GIT_AUTHOR_EMAIL=tester@localhost",
			"GIT_COMMITTER_NAME=tester", "GIT_COMMITTER_EMAIL=tester@localhost",
			"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("add", "-A")
	run("commit", "-q", "-m", "init")
}

// shutdown سرور فیلم‌نامه‌ای را می‌بندد (پیش از شمارش گوروتین).
func (e *e2eEnv) shutdown() { e.model.srv.Close() }

// ---------------------------------------------------------------- سناریوی ۳

// TestScenarioWriteUnreadRejectedThenAccepted — D.1.3: نوشتن روی فایلِ
// موجودِ نخوانده رد می‌شود؛ پس از read_file همان نوشتن پذیرفته می‌شود.
func TestScenarioWriteUnreadRejectedThenAccepted(t *testing.T) {
	env := newE2E(t,
		stepTool("c1", "write_file", `{"path":"calc.go","content":"package pw\n\n// overwritten blind\n"}`),
		stepTool("c2", "read_file", `{"path":"calc.go"}`),
		stepTool("c3", "write_file", `{"path":"calc.go","content":"package pw\n\nfunc Add(a, b int) int { return a + b }\n"}`),
		stepText("بازنویسی انجام شد."),
		stepText("پس از تذکر: تمام."),
	)
	env.writeFile("calc.go", "package pw\n\nfunc Add(a, b int) int { return a - b }\n")

	env.send("calc.go را بازنویسی کن")

	r1 := env.toolResult(t, "c1")
	if !strings.HasPrefix(r1, "ERROR:") || !strings.Contains(r1, "read_file") {
		t.Fatalf("overwrite of an unread file must be rejected with a re-read instruction: %q", r1)
	}
	if got := env.readFile("calc.go"); strings.Contains(got, "overwritten blind") {
		t.Fatalf("rejected write must not touch the file: %q", got)
	}
	r3 := env.toolResult(t, "c3")
	if !strings.HasPrefix(r3, "OK:") {
		t.Fatalf("the same write after read_file must be accepted: %q", r3)
	}
	if got := env.readFile("calc.go"); !strings.Contains(got, "a + b") {
		t.Fatalf("accepted write must land on disk: %q", got)
	}
}

// ---------------------------------------------------------------- سناریوی ۴

// TestScenarioExternalEditBetweenReadAndWriteRejected — D.1.4: تغییر بیرونی
// فایل بین read و write توسط محافظ کهنه‌بودن شناسایی و رد می‌شود. تغییر بیرونی
// در Check مرحله‌ی نوشتن انجام می‌شود — یعنی قطعاً بعد از اجرای read_file.
func TestScenarioExternalEditBetweenReadAndWriteRejected(t *testing.T) {
	var env *e2eEnv // در Check مرحله‌ی نوشتن استفاده می‌شود (بعد از read)
	env = newE2E(t,
		stepTool("c1", "read_file", `{"path":"calc.go"}`),
		stepToolCheck("c2", "write_file", `{"path":"calc.go","content":"package pw\n\nfunc Add(a, b int) int { return a + b }\n"}`,
			func(t *testing.T, req chatRequest) {
				// «کاربر» دقیقاً بین خواندن ایجنت و نوشتنش فایل را عوض می‌کند.
				p := filepath.Join(env.ws(), "calc.go")
				if err := os.WriteFile(p, []byte("package pw\n\n// user touched this file\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}),
		stepTool("c3", "read_file", `{"path":"calc.go"}`),
		stepTool("c4", "write_file", `{"path":"calc.go","content":"package pw\n\n// user touched this file\n\nfunc Add(a, b int) int { return a + b }\n"}`),
		stepText("محتوای جدید را دیدم و بازنویسی شد."),
		stepText("پس از تذکر: تمام."),
	)
	env.writeFile("calc.go", "package pw\n\nfunc Add(a, b int) int { return a - b }\n")

	env.send("تابع را درست کن")

	r2 := env.toolResult(t, "c2")
	if !strings.HasPrefix(r2, "ERROR:") || !strings.Contains(r2, "changed on disk") {
		t.Fatalf("stale write must be rejected with a re-read instruction: %q", r2)
	}
	// محتوای دست‌کاری‌شده‌ی کاربر باید دست‌نخورده مانده باشد تا ایجنت دوباره بخواند
	if got := env.readFile("calc.go"); !strings.Contains(got, "user touched this file") {
		t.Fatalf("user content must survive the rejected stale write: %q", got)
	}
	if !strings.HasPrefix(env.toolResult(t, "c4"), "OK:") {
		t.Fatalf("write based on a fresh read must be accepted: %q", env.toolResult(t, "c4"))
	}
}

// ---------------------------------------------------------------- سناریوی ۵

// TestScenarioCommandGateBypassAttempts — D.1.5: تلاش‌های دور زدن دروازه‌ی
// دستور، در هر دو شله (cmd/posix) با شله‌ی تزریقی. هر مورد باید مودال ask
// ثبت‌شده یا deny بدهد و هرگز اجرا نشود (نتیجه‌ی ابزار ERROR + فایل‌های نشان
// دست‌نخورده). استثنای مستندشده: «echo %COMSPEC%» در شله‌ی posix — رشته‌ی
// %VAR% در sh واژه‌ای است و echo ساده‌ی بی‌خطر همانِ کورپوس Stage 1 است
// (allow)؛ خروجی واقعیِ اجرا هم دقیقاً همین متن واژه‌ای است.
func TestScenarioCommandGateBypassAttempts(t *testing.T) {
	bypass := []struct {
		callID string
		cmd    string
	}{
		{"b1", "echo ok & npm install x"},
		{"b2", "dir & del /q *.cs"},
		{"b3", `echo x > ..\out.txt`},
		{"b4", "type .env"},
		{"b5", "go test -exec evil ./..."},
		{"b6", "git diff --output=f"},
		{"b7", "d^el /q x"},
		{"b8", "echo %COMSPEC%"},
	}
	for _, fl := range []struct {
		name   string
		f      shellFlavor
		echoOK bool // فقط جفت بی‌خطرِ مستندشده در شله‌ی posix اجرا می‌شود
	}{
		{"cmd", flavorCmd, false},
		{"posix", flavorPOSIX, true},
	} {
		t.Run(fl.name, func(t *testing.T) {
			if fl.f == flavorPOSIX && isWindows() {
				// runShell در ویندوز همیشه cmd را اجرا می‌کند (flavor تزریقی فقط
				// تحلیل/دروازه را عوض می‌کند)؛ معنای اجرایی POSIX (echo واژه‌ای،
				// خط پایان LF) فقط روی سکوهای POSIX قابل اثبات است و آن‌جا اجرا می‌شود.
				t.Skip("POSIX execution semantics: runShell uses cmd on Windows")
			}
			withFlavor(t, fl.f)
			var steps []scenarioStep
			for _, b := range bypass {
				steps = append(steps, stepTool(b.callID, "run_command", fmt.Sprintf(`{"command":%q}`, b.cmd)))
				steps = append(steps, stepText("دستور اجرا نشد؛ ادامه می‌دهم."))
			}
			env := newE2E(t, steps...)
			env.decide = permRespondReject // کاربر فیلم‌نامه‌ای به هر مودال «رد» می‌دهد
			// فایل‌های نشان: اگر هر دستوری بی‌اجازه اجرا شده باشد، این‌ها عوض می‌شوند
			env.writeFile("x", "marker\n")
			env.writeFile("marker.cs", "marker\n")
			parentOut := filepath.Join(filepath.Dir(env.ws()), "out.txt")
			t.Cleanup(func() { os.Remove(parentOut) })

			for _, b := range bypass {
				before := env.askCount()
				env.send("این دستور را اجرا کن: " + b.cmd)
				res := env.toolResult(t, b.callID)

				if fl.echoOK && b.callID == "b8" {
					// استثنای مستندشده: echo واژه‌ایِ بی‌خطر در posix اجرا می‌شود
					if strings.HasPrefix(res, "ERROR:") || !strings.Contains(res, "%COMSPEC%") {
						t.Fatalf("%s: harmless literal echo must run and print literally: %q", b.cmd, res)
					}
					continue
				}
				if !strings.HasPrefix(res, "ERROR:") {
					t.Fatalf("%s must never execute without a recorded ask: got %q", b.cmd, res)
				}
				if grew := env.askCount() - before; grew != 1 {
					t.Fatalf("%s: exactly one permission ask expected, got %d (asks=%v)", b.cmd, grew, env.asks)
				}
			}

			// نشان‌ها باید دست‌نخورده باشند و هیچ اثر جانبی‌ای روی دیسک نباشد
			if got := env.readFile("x"); got != "marker\n" {
				t.Fatalf("marker file x must be untouched: %q", got)
			}
			if got := env.readFile("marker.cs"); got != "marker\n" {
				t.Fatalf("marker file marker.cs must be untouched: %q", got)
			}
			for _, junk := range []string{"out.txt", "f", "node_modules", "..out.txt"} {
				if _, err := os.Stat(filepath.Join(env.ws(), junk)); err == nil {
					t.Fatalf("side effect %q must not appear in the workspace", junk)
				}
			}
			if _, err := os.Stat(parentOut); err == nil {
				t.Fatal("no file may be written outside the workspace")
			}
		})
	}
}

// ---------------------------------------------------------------- سناریوی ۶

// TestScenarioProtectedPathsAskInAutoMode — D.1.6: در حالت auto مسیر عادی
// بدون مودال اعمال می‌شود؛ مسیرهای محافظت‌شده (package.json، .env و
// src/.git/config تودرتو) مودال Diff می‌گیرند و پس از تأیید اعمال می‌شوند.
func TestScenarioProtectedPathsAskInAutoMode(t *testing.T) {
	env := newE2E(t,
		stepTool("c1", "write_file", `{"path":"plain.txt","content":"ordinary\n"}`),
		stepTool("c2", "write_file", `{"path":"package.json","content":"{\"name\":\"pw\"}\n"}`),
		stepTool("c3", "write_file", `{"path":".env","content":"TOKEN=abc\n"}`),
		stepTool("c4", "write_file", `{"path":"src/.git/config","content":"[core]\n"}`),
		stepText("همه ساخته شد."),
		stepText("پس از تذکر: تمام."),
	)
	env.send("این فایل‌ها را بساز")

	// مسیر عادی: بدون هیچ مودال
	if got := env.readFile("plain.txt"); got != "ordinary\n" {
		t.Fatalf("ordinary file must be written without a modal: %q", got)
	}
	// مسیرهای محافظت‌شده: دقیقاً سه مودال Diff، به همان ترتیب
	want := []string{"package.json", ".env", "src/.git/config"}
	if len(env.fileAsks) != len(want) {
		t.Fatalf("expected %d diff modal(s) for protected paths, got %d: %v", len(want), len(env.fileAsks), env.fileAsks)
	}
	for i, p := range want {
		if env.fileAsks[i] != p {
			t.Fatalf("diff modal %d: want %q, got %q", i, p, env.fileAsks[i])
		}
		if got := env.readFile(p); got == "" {
			t.Fatalf("protected file %q must be written after user approval", p)
		}
	}
}

// ---------------------------------------------------------------- سناریوی ۷

// TestScenarioRevertAllRestoresAndConflict — D.1.7: «برگرداندن همه‌ی تغییرات
// نوبت» فایل ساخته‌شده را برمی‌گرداند؛ فایلی که کاربر بعداً عوض کرده به‌عنوان
// تعارض گزارش می‌شود و بازنویسی نمی‌شود.
func TestScenarioRevertAllRestoresAndConflict(t *testing.T) {
	env := newE2E(t,
		stepTool("c1", "write_file", `{"path":"a.txt","content":"created by agent\n"}`),
		stepTool("c2", "read_file", `{"path":"b.txt"}`),
		stepTool("c3", "edit_file", `{"path":"b.txt","old_string":"original","new_string":"agent-edited"}`),
		stepText("هر دو تغییر اعمال شد."),
		stepText("پس از تذکر: تمام."),
	)
	env.writeFile("b.txt", "original content\n")
	env.send("فایل‌ها را بساز و ویرایش کن")

	sm := env.summaries()
	if len(sm) == 0 {
		t.Fatal("no turn summary")
	}
	turnID := sm[len(sm)-1].TurnID
	if turnID <= 0 {
		t.Fatalf("summary must carry the turn id: %+v", sm[len(sm)-1])
	}

	// کاربر بعد از ایجنت b.txt را عوض می‌کند
	env.writeFile("b.txt", "user edited this\n")
	rep := env.app.RevertTurnChanges(env.convID, turnID)

	found := func(list []string, p string) bool {
		for _, s := range list {
			if s == p {
				return true
			}
		}
		return false
	}
	if !found(rep.Reverted, "a.txt") {
		t.Fatalf("agent-created file must be reverted: %+v", rep)
	}
	if !found(rep.Conflicts, "b.txt") {
		t.Fatalf("user-modified file must be reported as a conflict: %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(env.ws(), "a.txt")); !os.IsNotExist(err) {
		t.Fatal("reverted created file must be gone from disk")
	}
	if got := env.readFile("b.txt"); got != "user edited this\n" {
		t.Fatalf("conflicting user edit must win: %q", got)
	}
}

// ---------------------------------------------------------------- سناریوی ۸

// TestScenarioCheckpointDestructiveCommandRestore — D.1.8: نقطه‌ی بازگشتِ
// نوبت → دستورِ تغییردهنده → بازگردانی → درخت بایت‌به‌بایت + سبد زباله‌ی پر.
func TestScenarioCheckpointDestructiveCommandRestore(t *testing.T) {
	env := newE2E(t,
		stepTool("c1", "write_file", `{"path":"new.txt","content":"created by agent\n"}`),
		stepTool("c2", "run_command", `{"command":"echo side-effect > generated.txt"}`),
		stepText("کار تمام است."),
		stepText("پس از تذکر: تمام."),
	)
	env.writeFile("keep.txt", "keep me byte-exact\n")
	env.initGitWorkspace(t)
	env.decide = permRespondAllow // کاربر فیلم‌نامه‌ای دستورِ پرسیده‌شده را اجازه می‌دهد

	env.send("فایل بساز و دستور را اجرا کن")

	sm := env.summaries()
	if len(sm) == 0 {
		t.Fatal("no turn summary")
	}
	turnID := sm[len(sm)-1].TurnID
	// cmd ویندوز echo را با CRLF و یک فاصله پیش از بازالشت می‌نویسد؛ مقصود
	// تست وجودِ اثر جانبی است، نه شکل خط پایان shell.
	if got := strings.TrimSpace(env.readFile("generated.txt")); got != "side-effect" {
		t.Fatalf("command side effect must exist before restore: %q", env.readFile("generated.txt"))
	}

	pv := env.app.PreviewCheckpointRestore(env.convID, turnID)
	if !pv.Available || pv.Error != "" {
		t.Fatalf("checkpoint preview must be available: %+v", pv)
	}
	in := func(list []string, p string) bool {
		for _, s := range list {
			if s == p {
				return true
			}
		}
		return false
	}
	if !in(pv.Trash, "new.txt") || !in(pv.Trash, "generated.txt") {
		t.Fatalf("files created during the turn must be listed for trash: %+v", pv)
	}
	if !in(pv.Unchanged, "keep.txt") {
		t.Fatalf("pre-existing tracked file must be unchanged: %+v", pv)
	}

	rep := env.app.RestoreCheckpoint(env.convID, turnID)
	if len(rep.Errors) > 0 {
		t.Fatalf("restore errors: %v", rep.Errors)
	}
	if !in(rep.Trashed, "new.txt") || !in(rep.Trashed, "generated.txt") {
		t.Fatalf("restore must move the two files to trash: %+v", rep)
	}
	if rep.TrashDir == "" {
		t.Fatal("trash dir must be reported")
	}
	// درخت بایت‌به‌بایت به وضعیت پیش از نوبت برگشته است
	if _, err := os.Stat(filepath.Join(env.ws(), "new.txt")); !os.IsNotExist(err) {
		t.Fatal("new.txt must be gone after restore")
	}
	if _, err := os.Stat(filepath.Join(env.ws(), "generated.txt")); !os.IsNotExist(err) {
		t.Fatal("command side effect must be gone after restore")
	}
	if got, err := os.ReadFile(filepath.Join(env.ws(), "keep.txt")); err != nil || string(got) != "keep me byte-exact\n" {
		t.Fatalf("pre-existing file must survive byte-exact: %q %v", got, err)
	}
	// سبد زباله واقعاً محتوای نجات‌یافته دارد
	entries, err := os.ReadDir(rep.TrashDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("trash dir must contain the rescued files: %v %v", entries, err)
	}
}

// ---------------------------------------------------------------- سناریوی ۱۰

// TestScenarioMidTurnCompactionKeepsTodosAndProtocol — D.1.10: فشرده‌سازی
// خودکارِ میانه‌ی نوبت؛ هر درخواستِ بعدی باید پروتکل‌سالم بماند، نشانگر
// «حذف کانتکست» یکتا باشد و فهرست کارها را زنده نگه دارد.
func TestScenarioMidTurnCompactionKeepsTodosAndProtocol(t *testing.T) {
	todoCheck := func(t *testing.T, req chatRequest) {
		t.Helper()
		// اعتبار پروتکل در هر درخواستِ پس از فشرده‌سازی
		if _, problems := validateHistory(req.Messages); len(problems) > 0 {
			t.Fatalf("outgoing history must stay protocol-valid: %v", problems)
		}
		markers := 0
		for _, m := range req.Messages {
			if isContextDropMarker(m) {
				markers++
				if !strings.Contains(historyContentString(m.Content), "پیاده‌سازی محافظ") {
					t.Fatalf("context-drop marker must carry the todo list: %q", historyContentString(m.Content))
				}
			}
		}
		if markers > 1 {
			t.Fatalf("at most one context-drop marker expected, got %d", markers)
		}
	}
	// متن بزرگ لاتین در «نوبت اول» برای عبور از آستانه‌ی ۸۰٪ بودجه.
	// فاز حذفِ فشرده‌سازی فقط واحدهای «قبل از آخرین پیام کاربر» را حذف
	// می‌کند و حداقل minKeepMessages پیام نگه می‌دارد، پس نوبت اول چند دور
	// ابزارِ کوچک هم دارد تا واحدهای قابل‌حذف وجود داشته باشد؛ todo_write
	// در نوبت اول طرح کار را در گفتگو می‌گذارد تا نشانگر آن را زنده نگه دارد.
	big := strings.Repeat("analysis paragraph with plain ascii words ", 6000) // ≈ 250KB
	env := newE2E(t,
		// — نوبت اول: برنامه‌ریزی + چند دور خواندن + متن سنگین + جمع‌بندی
		stepTool("c1", "todo_write", `{"todos":[{"content":"پیاده‌سازی محافظ خواندن‌پیش‌از‌نوشتن","status":"in_progress"},{"content":"نوشتن تست‌های سناریو","status":"pending"}]}`),
		stepTool("r1", "read_file", `{"path":"notes1.txt"}`),
		stepTool("r2", "read_file", `{"path":"notes2.txt"}`),
		stepTool("r3", "read_file", `{"path":"notes3.txt"}`),
		stepTool("r4", "read_file", `{"path":"notes4.txt"}`),
		stepTool("r5", "read_file", `{"path":"notes5.txt"}`),
		scenarioStep{Text: big, Calls: []scriptCall{{ID: "r6", Name: "read_file", Args: `{"path":"notes6.txt"}`}}},
		stepText("تحلیل اولیه تمام شد."),
		// — نوبت دوم: فشرده‌سازی باید پیش از نخستین درخواست همین نوبت بیاید
		scenarioStep{Text: "فشرده‌سازی رخ داد؛ ادامه می‌دهم.", Calls: []scriptCall{{ID: "c3", Name: "list_dir", Args: `{"path":"."}`}}, Check: todoCheck},
		scenarioStep{Text: "جمع‌بندی نهایی.", Check: todoCheck},
	)
	for i := 1; i <= 6; i++ {
		env.writeFile(fmt.Sprintf("notes%d.txt", i), fmt.Sprintf("notes %d\n", i))
	}
	env.app.mu.Lock()
	env.app.settings.Providers[0].ContextWindow = 100000
	env.app.mu.Unlock()

	env.send("برنامه‌ریزی کن و متن را تحلیل کن") // نوبت اول
	env.send("ادامه بده")                        // نوبت دوم — فشرده‌سازی اینجا

	// خودِ فشرده‌سازی باید در میانه‌ی نوبت رخ داده باشد (اعلان یک‌بار)
	compacted := false
	for _, n := range env.noticesText() {
		if strings.Contains(n, "فشرده شد") {
			compacted = true
		}
	}
	if !compacted {
		t.Fatalf("mid-turn compaction must fire for this scenario (notices: %v)", env.noticesText())
	}
	// و نشانگرِ حامل کارها در حداقل یک درخواست رسیده باشد
	sawMarker := false
	for _, r := range env.model.requests() {
		for _, m := range r.Messages {
			if isContextDropMarker(m) {
				sawMarker = true
			}
		}
	}
	if !sawMarker {
		t.Fatal("context-drop marker must appear in the outgoing requests after compaction")
	}
}

// ---------------------------------------------------------------- سناریوی ۱۱

// TestScenarioStopAtThreeMomentsClean — D.1.11: توقف در سه لحظه‌ی مختلف
// (حین استریم اول، حین استریم بعد از دور ابزار، حین اجرای دستور) — بدون
// نشت گوروتین، بدون فایل نیمه‌نوشته و با گفتگوی معتبر.
func TestScenarioStopAtThreeMomentsClean(t *testing.T) {
	before := runtime.NumGoroutine()

	// — لحظه‌ی ۱: حین استریم پاسخ نخست (هیچ ابزاری هنوز اجرا نشده)
	hold1 := make(chan struct{})
	reached1 := make(chan struct{}, 1)
	envA := newE2E(t, scenarioStep{Text: "پاسخی که هرگز کامل نمی‌شود", Hold: hold1, Reached: reached1})
	if _, err := envA.app.SendMessage(envA.convID, "سلام", nil); err != nil {
		t.Fatal(err)
	}
	waitReached(t, reached1)
	envA.app.StopGeneration()
	close(hold1)
	waitStreamDone(t, envA.app)
	if !envA.lastAborted(t) {
		t.Fatal("moment 1: the turn must end aborted")
	}

	// — لحظه‌ی ۲: حین استریم دوم، بعد از یک دور ابزارِ کامل (write_file)
	full := strings.Repeat("line of complete content\n", 2000) // ≈ 50KB
	hold2 := make(chan struct{})
	reached2 := make(chan struct{}, 1)
	envB := newE2E(t,
		stepTool("c1", "write_file", `{"path":"big.txt","content":`+strconv.Quote(full)+`}`),
		scenarioStep{Text: "ادامه‌ی پاسخ", Hold: hold2, Reached: reached2},
	)
	if _, err := envB.app.SendMessage(envB.convID, "فایل بزرگ بساز", nil); err != nil {
		t.Fatal(err)
	}
	waitReached(t, reached2)
	envB.app.StopGeneration()
	close(hold2)
	waitStreamDone(t, envB.app)
	if got := envB.readFile("big.txt"); got != full {
		t.Fatalf("moment 2: written file must be complete, not half-written (len %d vs %d)", len(got), len(full))
	}
	if _, err := envB.app.loadConversationForTest(envB.convID); err != nil {
		t.Fatalf("moment 2: conversation file must stay loadable: %v", err)
	}

	// — لحظه‌ی ۳: حین اجرای یک دستور واقعی؛ بلافاصله پس از تأییدِ مودال Stop
	// می‌زنیم — لغو باید داخل اجرای sleep فرود بیاید (kill فرایند).
	started := make(chan struct{}, 1)
	envC := newE2E(t,
		stepTool("c1", "run_command", `{"command":"sleep 2"}`),
		stepText("هرگز به اینجا نمی‌رسیم."),
	)
	envC.decide = permRespondAllow
	prevAsk := envC.app.permAskEmit
	envC.app.permAskEmit = func(ask *permissionAsk) {
		prevAsk(ask)
		started <- struct{}{}
	}
	if _, err := envC.app.SendMessage(envC.convID, "یک لحظه صبر کن", nil); err != nil {
		t.Fatal(err)
	}
	waitReached(t, started)
	envC.app.StopGeneration()
	waitStreamDone(t, envC.app)
	if !envC.lastAborted(t) {
		t.Fatal("moment 3: cancelling during a command must abort the turn")
	}
	// فایل نیمه‌نوشته‌ای در پوشه‌ی کاری جا نمانده باشد (دستور فقط خوابید)
	for _, junk := range []string{"out.txt", "sleep.out"} {
		if _, err := os.Stat(filepath.Join(envC.ws(), junk)); err == nil {
			t.Fatalf("moment 3: stray file %q must not exist", junk)
		}
	}

	// — بدون نشت گوروتین: سرورها بسته می‌شوند و شمارش به خط پایه برمی‌گردد
	envA.shutdown()
	envB.shutdown()
	envC.shutdown()
	deadline := time.Now().Add(5 * time.Second)
	after := runtime.NumGoroutine()
	for time.Now().Before(deadline) && after > before+3 {
		time.Sleep(150 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > before+3 {
		t.Fatalf("goroutine leak after three stops: before=%d after=%d", before, after)
	}
}
