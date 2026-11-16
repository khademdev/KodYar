// MyAI — تست‌های: ابزار task (زیرایجنت فقط‌خواندنی با کانتکست تازه).
//
// پوشش:
//
// ۱. تعریف ابزار و اعتبارسنجی آرگومان‌ها و تصمیم پیش‌فرض دسترسی
// ۲. allowlist زیرایجنت: ابزار تغییردهنده/run_command/task تودرتو رد
// ۳. فایل حساس داخل زیرایجنت بدون مودال رد می‌شود (نشانگر کانتکست)
// ۴. حلقه‌ی کامل headless روی سرور SSE آزمایشی: چنددوره‌ای + گزارش آماری
// ۵. سقف دورهای زیرایجنت (تنظیم + clamp)
// ۶. محافظ حلقه‌ی تکرار داخل زیرایجنت
// ۷. لغو از طریق کانتکست والد
// ۸. تنظیمات: پیش‌فرض/نرمال‌سازی/SettingsView/SetMaxSubagentRounds
// ۹. جریان کامل والد: مدل اصلی task صدا می‌زند، مودال اجازه، گزارش برمی‌گردد
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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------- ۱. تعریف و دروازه‌ها

func TestTaskToolDefPresentAndValid(t *testing.T) {
	found := false
	for _, d := range agentToolDefs() {
		if d.Function.Name == "task" {
			found = true
			if d.Type != "function" {
				t.Fatalf("task def type = %q", d.Type)
			}
			m, _ := d.Function.Parameters.(map[string]interface{})
			props, _ := m["properties"].(map[string]interface{})
			if _, ok := props["description"]; !ok {
				t.Fatal("task def must declare a 'description' property")
			}
		}
	}
	if !found {
		t.Fatal("task missing from agentToolDefs()")
	}
	// زیرایجنت فقط زیرمجموعه‌ی فقط‌خواندنی را معرفی می‌کند
	for _, d := range subagentToolDefs() {
		if !subagentAllowedTools[d.Function.Name] {
			t.Fatalf("subagent defs leak non-readonly tool %q", d.Function.Name)
		}
	}
	if len(subagentToolDefs()) != len(subagentAllowedTools) {
		t.Fatalf("subagent defs count = %d, want %d", len(subagentToolDefs()), len(subagentAllowedTools))
	}
}

func TestValidateToolArgsTask(t *testing.T) {
	if err := validateToolArgs("task", map[string]interface{}{}); err == nil {
		t.Fatal("missing description must fail")
	}
	if err := validateToolArgs("task", map[string]interface{}{"description": "   "}); err == nil {
		t.Fatal("blank description must fail")
	}
	if err := validateToolArgs("task", map[string]interface{}{"description": "بررسی جریان لاگین"}); err != nil {
		t.Fatalf("valid description must pass: %v", err)
	}
	// عدد مجاز نیست — description باید رشته باشد (مثل بقیه‌ی فیلدهای متنی)
	if err := validateToolArgs("task", map[string]interface{}{"description": 42}); err == nil {
		t.Fatal("numeric description must be rejected")
	}
}

func TestTaskDefaultPermissionIsAsk(t *testing.T) {
	for _, confirm := range []bool{false, true} {
		if got := toolDefaultDecision("task", confirm); got != permAsk {
			t.Fatalf("task default decision (confirm=%v) = %v, want ask", confirm, got)
		}
	}
	// در فهرست ابزارهای تنظیمات دسترسی هم هست
	found := false
	for _, n := range agentToolNames {
		if n == "task" {
			found = true
		}
	}
	if !found {
		t.Fatal("task missing from agentToolNames (permissions tab)")
	}
}

func TestParallelReadOnlyToolsExcludesTask(t *testing.T) {
	if isParallelReadOnlyTool("task") {
		t.Fatal("task must stay a sequential barrier (it is long-running and asks for permission)")
	}
}

// ---------------------------------------------------------------- ۲/۳. اجرای ابزار داخل زیرایجنت

func TestExecSubagentToolAllowlist(t *testing.T) {
	a := newTestApp(t)
	ctx := withSubagentNoAsk(context.Background())
	for _, name := range []string{"write_file", "edit_file", "move_path", "delete_path", "create_dir", "task", "custom_thing", "unknown"} {
		out, ok := a.execSubagentTool(ctx, name, `{}`)
		if ok {
			t.Fatalf("%s must be rejected inside sub-agents", name)
		}
		if !strings.Contains(out, "not available to sub-agents") {
			t.Fatalf("%s: unexpected message %q", name, out)
		}
	}
	// run_command مجاز است ولی داخل زیرایجنت هرگز مودال نمی‌گیرد: با
	// دستورِ خاموش/نیازمند تأیید، بی‌صدا اجرا نمی‌شود.
	if out, ok := a.execSubagentTool(ctx, "run_command", `{"command":"go build ./..."}`); ok {
		t.Fatalf("run_command must not run without permission inside a sub-agent, got %q", out)
	}
	// فقط‌خواندنی مجاز است (پوشه‌ی خالی)
	out, ok := a.execSubagentTool(ctx, "list_dir", `{}`)
	if !ok || !strings.Contains(out, "item(s)") {
		t.Fatalf("list_dir must work inside sub-agent, got ok=%v out=%q", ok, out)
	}
}

func TestSubagentSensitiveReadDeniedWithoutAsk(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=xyz"), 0o600); err != nil {
		t.Fatal(err)
	}
	// مسیر ask فعال است (a.ctx!= nil) ولی کانتکست زیرایجنت باید بی‌مودال رد کند
	a.ctx = context.Background()
	ctx := withSubagentNoAsk(context.Background())
	out, ok := a.execSubagentTool(ctx, "read_file", `{"path":".env"}`)
	if ok {
		t.Fatal("sensitive read inside sub-agent must be denied")
	}
	if !strings.Contains(out, "denied") {
		t.Fatalf("unexpected message: %q", out)
	}
	// هیچ ask ثبت‌نشده‌ای نمانده است
	a.changes.mu.Lock()
	n := len(a.changes.permAsks)
	a.changes.mu.Unlock()
	if n != 0 {
		t.Fatalf("sub-agent must not leave pending asks, got %d", n)
	}
}

func TestRequestPermissionConfirmSubagentCtxRejected(t *testing.T) {
	a := newTestApp(t)
	a.ctx = context.Background()
	ask := &permissionAsk{ID: "perm-x", Kind: "tool", Command: "read_file", At: timeNow()}
	if got := a.requestPermissionConfirm(withSubagentNoAsk(context.Background()), ask); got != permRespondReject {
		t.Fatalf("subagent ctx must fast-reject, got %q", got)
	}
}

// ---------------------------------------------------------------- ۴. حلقه‌ی headless

// subagentApp اپ ایجنتِ متصل به سرور داده‌شده + فایل نمونه در پوشه‌ی کاری.
func subagentApp(t *testing.T, srvURL string) *App {
	t.Helper()
	a := agentStreamApp(t, srvURL)
	if err := os.WriteFile(filepath.Join(a.settings.WorkspacePath, "hello.txt"), []byte("subagent-says-hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	return a
}

// isSubagentRequest درخواست‌های والد و زیرایجنت را از روی tools تفکیک می‌کند:
// زیرایجنت فقط ۸ ابزار فقط‌خواندنی دارد و task در آن نیست.
func isSubagentRequest(t *testing.T, r *http.Request) bool {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	var req struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	for _, tl := range req.Tools {
		if tl.Function.Name == "task" {
			return false // والد
		}
	}
	return len(req.Tools) > 0
}

func TestRunSubagentMultiRoundReport(t *testing.T) {
	redirectToTestLog(t)
	var subReqs, parentStyleReqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSubagentRequest(t, r) {
			switch atomic.AddInt32(&subReqs, 1) {
			case 1:
				sseToolCallsBatch(w, []apiToolCall{
					batchCall("sub-1", "read_file", `{"path":"hello.txt"}`),
				}, "tool_calls")
			default:
				sseFinalWithUsage(w, "Findings: hello.txt contains 'subagent-says-hi'.", 100, 20)
			}
			return
		}
		atomic.AddInt32(&parentStyleReqs, 1)
		sseOK(w, "never")
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	res, ok := a.runSubagent(context.Background(), "Read hello.txt and report its content.")
	if !ok {
		t.Fatalf("sub-agent must succeed, got: %s", res)
	}
	for _, want := range []string{"Sub-agent report", "1 tool call(s) in 1 round(s)", "subagent-says-hi"} {
		if !strings.Contains(res, want) {
			t.Fatalf("report missing %q:\n%s", want, res)
		}
	}
	if got := atomic.LoadInt32(&subReqs); got != 2 {
		t.Fatalf("sub-agent must make exactly 2 LLM calls, got %d", got)
	}
}

func TestRunSubagentRoundLimit(t *testing.T) {
	redirectToTestLog(t)
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqs, 1)
		sseToolCallsBatch(w, []apiToolCall{
			batchCall("c"+string(rune('0'+atomic.LoadInt32(&reqs))), "list_dir", `{}`),
		}, "tool_calls")
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	a.mu.Lock()
	a.settings.MaxSubagentRounds = 3
	a.mu.Unlock()

	res, ok := a.runSubagent(context.Background(), "keep listing forever")
	if !ok {
		t.Fatalf("round-limit hit must still return a usable report, got: %s", res)
	}
	if !strings.Contains(res, "ROUND LIMIT") {
		t.Fatalf("report must mention the round limit:\n%s", res)
	}
	// دور ۰..۲ ابزار اجرا می‌کنند؛ دورِ سوم به سقف می‌خورد و یک درخواست
	// نهاییِ بدون ابزار برای گرفتن گزارش می‌رود (رفتار عمداً تغییر کرده — Bug 2).
	if got := atomic.LoadInt32(&reqs); got != 5 {
		t.Fatalf("want 5 LLM calls (3 tool rounds + limit round + final tools-less round), got %d", got)
	}
}

func TestRunSubagentDoomLoopGuard(t *testing.T) {
	redirectToTestLog(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseToolCallsBatch(w, []apiToolCall{
			batchCall("same-call", "list_dir", `{}`),
		}, "tool_calls")
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	res, ok := a.runSubagent(context.Background(), "loop forever")
	if !ok {
		t.Fatalf("doom-loop stop must still return ok=true with a report, got: %s", res)
	}
	if !strings.Contains(res, "repeated too many times") {
		t.Fatalf("report must mention the loop guard:\n%s", res)
	}
}

func TestRunSubagentParentCancel(t *testing.T) {
	redirectToTestLog(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// درخواست اول آهسته است؛ والد در میانه لغو می‌کند
		time.Sleep(300 * time.Millisecond)
		sseToolCallsBatch(w, []apiToolCall{batchCall("c1", "list_dir", `{}`)}, "tool_calls")
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var res string
	var ok bool
	go func() {
		defer close(done)
		res, ok = a.runSubagent(ctx, "slow exploration")
	}()
	time.Sleep(80 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sub-agent did not return after parent cancel")
	}
	if ok {
		t.Fatalf("cancelled sub-agent must return ok=false: %s", res)
	}
	if !strings.Contains(res, "cancelled") {
		t.Fatalf("unexpected cancel message: %q", res)
	}
}

func TestRunSubagentToolErrorsGoBackToModel(t *testing.T) {
	redirectToTestLog(t)
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&reqs, 1) {
		case 1:
			// مسیر خارج از پوشه‌ی کاری → خطای امنیتی به مدل
			sseToolCallsBatch(w, []apiToolCall{
				batchCall("c1", "read_file", `{"path":"../../etc/passwd"}`),
			}, "tool_calls")
		default:
			sseFinalWithUsage(w, "The path was rejected; nothing to report.", 50, 10)
		}
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	res, ok := a.runSubagent(context.Background(), "try to escape the workspace")
	if !ok || !strings.Contains(res, "nothing to report") {
		t.Fatalf("sub-agent must recover from tool errors, ok=%v res=%s", ok, res)
	}
}

func TestTruncateHeadTailKeepsEnds(t *testing.T) {
	s := strings.Repeat("a", 5000) + "TAIL"
	out := truncateHeadTail(s, 100)
	if len(out) > 200+len("...[output truncated]...\n") {
		t.Fatalf("too long: %d", len(out))
	}
	if !strings.HasPrefix(out, "aaa") || !strings.HasSuffix(out, "TAIL") {
		t.Fatal("head and tail must survive")
	}
	if truncateHeadTail("short", 100) != "short" {
		t.Fatal("short input must pass through")
	}
}

// ---------------------------------------------------------------- ۸. تنظیمات

func TestClampMaxSubagentRounds(t *testing.T) {
	cases := map[int]int{
		0: defaultMaxSubagentRounds, -5: defaultMaxSubagentRounds,
		1: minMaxSubagentRounds, 2: minMaxSubagentRounds, 3: 3,
		8: 8, 50: 50, 51: maxMaxSubagentRounds, 9999: maxMaxSubagentRounds,
	}
	for in, want := range cases {
		if got := clampMaxSubagentRounds(in); got != want {
			t.Fatalf("clamp(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestSubagentRoundsSettingsLifecycle(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	def := a.settings.MaxSubagentRounds
	a.mu.Unlock()
	view := a.SettingsView() // خودش a.mu را می‌گیرد — بیرون از قفل صدا زده شود
	if def != defaultMaxSubagentRounds {
		t.Fatalf("default = %d, want %d", def, defaultMaxSubagentRounds)
	}
	if got := view["maxSubagentRounds"]; got != defaultMaxSubagentRounds {
		t.Fatalf("SettingsView.maxSubagentRounds = %v", got)
	}
	if err := a.SetMaxSubagentRounds(2); err != nil {
		t.Fatal(err)
	}
	if got := a.settings.MaxSubagentRounds; got != minMaxSubagentRounds {
		t.Fatalf("SetMaxSubagentRounds(2) must clamp to %d, got %d", minMaxSubagentRounds, got)
	}
	if err := a.SetMaxSubagentRounds(500); err != nil {
		t.Fatal(err)
	}
	if got := a.settings.MaxSubagentRounds; got != maxMaxSubagentRounds {
		t.Fatalf("SetMaxSubagentRounds(500) must clamp to %d, got %d", maxMaxSubagentRounds, got)
	}
	// فایل قدیمی بدون فیلد → نرمال‌سازی به پیش‌فرض
	s := defaultSettings()
	s.MaxSubagentRounds = 0
	if got := clampMaxSubagentRounds(s.MaxSubagentRounds); got != defaultMaxSubagentRounds {
		t.Fatal("zero must normalize to default")
	}
}

// ---------------------------------------------------------------- ۹. جریان کامل والد → task → گزارش

func TestParentStreamCallsTaskEndToEnd(t *testing.T) {
	redirectToTestLog(t)
	var parentReqs, subReqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSubagentRequest(t, r) {
			switch atomic.AddInt32(&subReqs, 1) {
			case 1:
				sseToolCallsBatch(w, []apiToolCall{
					batchCall("s1", "glob", `{"pattern":"*.txt"}`),
				}, "tool_calls")
			default:
				sseFinalWithUsage(w, "Findings: one text file (hello.txt).", 80, 15)
			}
			return
		}
		switch atomic.AddInt32(&parentReqs, 1) {
		case 1:
			sseToolCallsBatch(w, []apiToolCall{
				batchCall("p1", "task", `{"description":"Find all text files and summarize."}`),
			}, "tool_calls")
		default:
			sseFinalWithUsage(w, "گزارش زیرایجنت: یک فایل متنی پیدا شد.", 200, 30)
		}
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	a.ctx = context.Background() // مسیر ask فعال
	conv, err := a.SendMessage("", "فایل‌های متنی را پیدا کن", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)

	// مودال اجازه‌ی task → allow (فقط یک‌بار)
	id := waitPendingAskID(t, a, "")
	if err := a.RespondPermissionConfirm(id, "allow"); err != nil {
		t.Fatal(err)
	}

	loaded := waitConversationUntil(t, a, conv.ID, hasAssistantWith("یک فایل متنی"))
	checkToolPairing(t, loaded)

	// نتیجه‌ی ابزار task باید گزارش زیرایجنت را به والد برگردانده باشد
	var taskResult string
	for _, m := range loaded.Messages {
		if m.Role == "tool" && m.ToolName == "task" {
			taskResult = m.Content
		}
	}
	if taskResult == "" {
		t.Fatal("task tool result missing from conversation")
	}
	for _, want := range []string{"Sub-agent report", "hello.txt"} {
		if !strings.Contains(taskResult, want) {
			t.Fatalf("task result missing %q:\n%s", want, taskResult)
		}
	}
	if got := atomic.LoadInt32(&subReqs); got != 2 {
		t.Fatalf("sub-agent calls = %d, want 2", got)
	}
	if got := atomic.LoadInt32(&parentReqs); got != 2 {
		t.Fatalf("parent calls = %d, want 2", got)
	}
}

func TestParentStreamTaskDeniedStopsGracefully(t *testing.T) {
	redirectToTestLog(t)
	var parentReqs, subReqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSubagentRequest(t, r) {
			atomic.AddInt32(&subReqs, 1)
			sseOK(w, "sub")
			return
		}
		switch atomic.AddInt32(&parentReqs, 1) {
		case 1:
			sseToolCallsBatch(w, []apiToolCall{
				batchCall("p1", "task", `{"description":"Anything."}`),
			}, "tool_calls")
		default:
			sseFinalWithUsage(w, "باشه، خودم ادامه می‌دهم.", 100, 10)
		}
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	a.ctx = context.Background()
	conv, err := a.SendMessage("", "بررسی کن", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)

	id := waitPendingAskID(t, a, "")
	if err := a.RespondPermissionConfirm(id, "reject"); err != nil {
		t.Fatal(err)
	}

	loaded := waitConversationUntil(t, a, conv.ID, hasAssistantWith("خودم ادامه"))
	if got := atomic.LoadInt32(&subReqs); got != 0 {
		t.Fatalf("rejected task must not spawn a sub-agent, got %d calls", got)
	}
	var taskResult string
	for _, m := range loaded.Messages {
		if m.Role == "tool" && m.ToolName == "task" {
			taskResult = m.Content
		}
	}
	if !strings.Contains(taskResult, "denied") {
		t.Fatalf("task result must carry the denial, got: %q", taskResult)
	}
}

// ---------------------------------------------------------------- ۱۰.: تصمیم‌های محافظ حلقه

// subagentRequestBody بدنه‌ی درخواست مدل را برای بازرسی پارس می‌کند (تست‌ها).
type subagentRequestBody struct {
	Tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
	Messages []struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
		ToolCallID string `json:"tool_call_id"`
	} `json:"messages"`
}

// readSubagentReq بدنه‌ی درخواست را می‌خواند و برای همان handler بازمی‌گرداند.
func readSubagentReq(t *testing.T, r *http.Request) subagentRequestBody {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	var out subagentRequestBody
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("parse request body: %v", err)
	}
	return out
}

// checkSubagentPairing هر assistant دارای tool_calls باید بلافاصله و به همان
// ترتیب len(tool_calls) پیام tool با tool_call_id منطبق داشته باشد.
func checkSubagentPairing(t *testing.T, req subagentRequestBody) {
	t.Helper()
	for i, m := range req.Messages {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		if i+1+len(m.ToolCalls) > len(req.Messages) {
			t.Fatalf("assistant msg %d declares %d tool calls but the history ends early", i, len(m.ToolCalls))
		}
		for j, tc := range m.ToolCalls {
			tm := req.Messages[i+1+j]
			if tm.Role != "tool" || tm.ToolCallID != tc.ID {
				t.Fatalf("pairing broken at msg %d call %d: want tool for %q, got role=%q toolCallID=%q", i, j, tc.ID, tm.Role, tm.ToolCallID)
			}
		}
	}
}

// TestRunSubagentBlockedRepeatsNotExecuted: سه فراخوانی یکسان در یک پیام →
// سومی توسط محافظ مسدود می‌شود و نباید اجرا شود (پیام tool آن دقیقاً خطای
// مسدودسازی است، نه خروجی ابزار).
func TestRunSubagentBlockedRepeatsNotExecuted(t *testing.T) {
	redirectToTestLog(t)
	var reqs int32
	var secondReq subagentRequestBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&reqs, 1)
		if n == 1 {
			call := batchCall("dup", "list_dir", `{}`)
			sseToolCallsBatch(w, []apiToolCall{
				batchCall("dup-1", "list_dir", `{}`),
				batchCall("dup-2", "list_dir", `{}`),
				batchCall("dup-3", "list_dir", `{}`),
				// شناسه‌ها متفاوت‌اند ولی «امضا»ی فراخوانی (ابزار+آرگومان) یکی است
				call,
			}, "tool_calls")
			return
		}
		secondReq = readSubagentReq(t, r)
		sseFinalWithUsage(w, "Findings: listing repeated.", 10, 5)
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	res, ok := a.runSubagent(context.Background(), "list the workspace four times")
	if !ok {
		t.Fatalf("sub-agent must finish, got: %s", res)
	}
	checkSubagentPairing(t, secondReq)

	var toolMsgs []string
	for _, m := range secondReq.Messages {
		if m.Role == "tool" {
			toolMsgs = append(toolMsgs, m.Content)
		}
	}
	if len(toolMsgs) != 4 {
		t.Fatalf("want 4 tool messages (one per call), got %d", len(toolMsgs))
	}
	// اولی و دومی اجرا می‌شوند؛ سومی (run=3) و چهارمی (run=4) ازپیش مسدودند.
	for i := 0; i < 2; i++ {
		if !strings.Contains(toolMsgs[i], "item(s)") {
			t.Fatalf("call %d must have been executed (list_dir output), got %q", i+1, toolMsgs[i])
		}
	}
	if toolMsgs[2] != repeatBlockedToolError("list_dir", 3) {
		t.Fatalf("3rd identical call must NOT be executed (blocked message), got %q", toolMsgs[2])
	}
	if toolMsgs[3] != repeatBlockedToolError("list_dir", 4) {
		t.Fatalf("4th identical call must carry the pre-computed block message, got %q", toolMsgs[3])
	}
	// فقط فراخوانی‌های اجراشده شمرده می‌شوند
	if !strings.Contains(res, "2 tool call(s)") {
		t.Fatalf("executed tool calls must be counted (2), report: %s", res)
	}
}

// TestRunSubagentStopCountsOnlyExecuted: تکرار بی‌پایان همان فراخوانی →
// دومی اجرا، سوم و چهارم مسدود و پنجمی نوبت را متوقف می‌کند؛ شمارش «ابزار
// اجراشده» باید ۲ بماند و همه‌ی فراخوانی‌ها پیام tool داشته باشند.
func TestRunSubagentStopCountsOnlyExecuted(t *testing.T) {
	redirectToTestLog(t)
	var reqs int32
	var lastReq subagentRequestBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastReq = readSubagentReq(t, r)
		atomic.AddInt32(&reqs, 1)
		sseToolCallsBatch(w, []apiToolCall{batchCall("same", "list_dir", `{}`)}, "tool_calls")
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	res, ok := a.runSubagent(context.Background(), "loop forever")
	if !ok {
		t.Fatalf("guard stop must still return a report, got: %s", res)
	}
	if !strings.Contains(res, "2 tool call(s)") {
		t.Fatalf("only the two executed calls must be counted:\n%s", res)
	}
	if !strings.Contains(res, "repeated too many times") {
		t.Fatalf("stop notice missing:\n%s", res)
	}
	if got := atomic.LoadInt32(&reqs); got != 5 {
		t.Fatalf("want 5 LLM calls (2 executed + 2 blocked + stop round), got %d", got)
	}
	checkSubagentPairing(t, lastReq)
}

// ---------------------------------------------------------------- ۱۱.: سقف دورها

// TestRunSubagentRoundLimitFinalReport: با پر شدن سقف دورها، مدل یک درخواست
// نهاییِ «بدون ابزار» می‌گیرد و متن همان پاسخ، گزارش والد است (Bug 2).
func TestRunSubagentRoundLimitFinalReport(t *testing.T) {
	redirectToTestLog(t)
	var finalReqs int32
	var finalBody subagentRequestBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := readSubagentReq(t, r)
		if len(req.Tools) == 0 {
			atomic.AddInt32(&finalReqs, 1)
			finalBody = req
			sseFinalWithUsage(w, "Findings: a.txt and b.txt exist; no further detail.", 40, 12)
			return
		}
		sseToolCallsBatch(w, []apiToolCall{batchCall("c1", "list_dir", `{}`)}, "tool_calls")
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	a.mu.Lock()
	a.settings.MaxSubagentRounds = 3
	a.mu.Unlock()

	res, ok := a.runSubagent(context.Background(), "keep exploring forever")
	if !ok {
		t.Fatalf("round limit must still return a report, got: %s", res)
	}
	if !strings.Contains(res, "Findings: a.txt and b.txt exist") {
		t.Fatalf("the final tools-less answer must be the report:\n%s", res)
	}
	if !strings.Contains(res, "ROUND LIMIT") {
		t.Fatalf("report must flag the round limit:\n%s", res)
	}
	if got := atomic.LoadInt32(&finalReqs); got != 1 {
		t.Fatalf("the final request must happen exactly once, got %d", got)
	}
	checkSubagentPairing(t, finalBody)
	// آخرین پیام باید همان درخواست گزارش باشد
	last := finalBody.Messages[len(finalBody.Messages)-1]
	if last.Role != "user" || last.Content != subagentFinalReportPrompt {
		t.Fatalf("last message must be the report prompt, got role=%q content=%q", last.Role, last.Content)
	}
	// هر فراخوانیِ اجرانشده پیام tool مخصوصِ سقف دورها دارد
	found := false
	for _, m := range finalBody.Messages {
		if m.Role == "tool" && m.Content == subagentRoundLimitToolError {
			found = true
		}
	}
	if !found {
		t.Fatalf("unexecuted calls must get the round-limit tool error:\n%+v", finalBody.Messages)
	}
}

// TestRunSubagentRoundLimitFallback: اگر درخواست نهاییِ گزارش شکست بخورد،
// گزارش جایگزینِ قطعی فهرست کارهای بررسی‌شده را (کران‌دار) برمی‌گرداند.
func TestRunSubagentRoundLimitFallback(t *testing.T) {
	redirectToTestLog(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := readSubagentReq(t, r)
		if len(req.Tools) == 0 {
			// ۴۰۰ (بازگشت‌ناپذیر) — درخواست نهایی شکست می‌خورد
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"no tools-less requests"}}`)
			return
		}
		sseToolCallsBatch(w, []apiToolCall{
			batchCall("c1", "read_file", `{"path":"hello.txt"}`),
			batchCall("c2", "glob", `{"pattern":"*.go"}`),
		}, "tool_calls")
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	a.mu.Lock()
	a.settings.MaxSubagentRounds = 3
	a.mu.Unlock()

	res, ok := a.runSubagent(context.Background(), "explore")
	if !ok {
		t.Fatalf("fallback must still be a usable report, got: %s", res)
	}
	for _, want := range []string{"ROUND LIMIT", "read_file(hello.txt)", "glob(*.go)"} {
		if !strings.Contains(res, want) {
			t.Fatalf("fallback report missing %q:\n%s", want, res)
		}
	}
	if strings.Contains(res, "(the sub-agent produced no final text)") {
		t.Fatalf("fallback must not be the empty placeholder:\n%s", res)
	}
}

// TestRunSubagentRoundLimitFinalCancel: لغو کاربر در میانه‌ی درخواست نهاییِ
// گزارش → خطای لغو (نه گزارش نصفه).
func TestRunSubagentRoundLimitFinalCancel(t *testing.T) {
	redirectToTestLog(t)
	finalStarted := make(chan struct{})
	var once atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := readSubagentReq(t, r)
		if len(req.Tools) == 0 {
			if once.CompareAndSwap(false, true) {
				close(finalStarted)
			}
			<-r.Context().Done() // تا لغوِ کلاینت معطل می‌ماند
			return
		}
		sseToolCallsBatch(w, []apiToolCall{batchCall("c1", "list_dir", `{}`)}, "tool_calls")
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	a.mu.Lock()
	a.settings.MaxSubagentRounds = 3
	a.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var res string
	var ok bool
	go func() {
		defer close(done)
		res, ok = a.runSubagent(ctx, "keep going")
	}()
	select {
	case <-finalStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the final report request never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sub-agent did not return after cancel during the final request")
	}
	if ok {
		t.Fatalf("cancelled final request must return ok=false: %s", res)
	}
	if !strings.Contains(res, "cancelled") {
		t.Fatalf("unexpected cancel message: %q", res)
	}
}

// TestRunSubagentPersianTaskRoundTrip: متن فارسیِ مأموریت (با نیم‌فاصله) باید
// بایت‌به‌بایت به مدل برسد — بدون نرمال‌سازی یا افتادن ZWNJ.
func TestRunSubagentPersianTaskRoundTrip(t *testing.T) {
	redirectToTestLog(t)
	task := "فایل‌های «می‌خواهم» را در پوشه‌ی سند بررسی کن و خلاصه بده"
	var firstBody subagentRequestBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstBody = readSubagentReq(t, r)
		sseFinalWithUsage(w, "Findings: nothing to add.", 5, 3)
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	if _, ok := a.runSubagent(context.Background(), task); !ok {
		t.Fatal("sub-agent must finish")
	}
	userMsg := ""
	for _, m := range firstBody.Messages {
		if m.Role == "user" {
			userMsg = m.Content
		}
	}
	if userMsg != task {
		t.Fatalf("persian task must round-trip byte-exact:\nwant %q\ngot  %q", task, userMsg)
	}
}

// ---------------------------------------------------------------- ۱۲. آمار مصرف (Bug 8)

// TestSubagentUsageTallyAccumulates: آمار زیرایجنت در accumulator همین نوبت
// جمع می‌شود؛ بدون accumulator (ctx مستقل) هم nil-safe است.
func TestSubagentUsageTallyAccumulates(t *testing.T) {
	redirectToTestLog(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseFinalWithUsage(w, "Findings: done.", 33, 7)
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	if usageTallyFrom(context.Background()) != nil {
		t.Fatal("a plain context must not carry a tally")
	}
	var tally usageTally
	if _, ok := a.runSubagent(withUsageTally(context.Background(), &tally), "summarize"); !ok {
		t.Fatal("sub-agent must finish")
	}
	if p, c := tally.snapshot(); p != 33 || c != 7 {
		t.Fatalf("tally = (%d,%d), want (33,7)", p, c)
	}
	// nil-safe helpers
	var nilTally *usageTally
	nilTally.add(5, 5)
	if p, c := nilTally.snapshot(); p != 0 || c != 0 {
		t.Fatal("nil tally must be a no-op")
	}
}

// TestSubagentUsageFoldedIntoParentTotals: مصرف زیرایجنت به آمار گفتگو (که
// chat:done و settings.TokensUsed نشان می‌دهند) اضافه می‌شود.
func TestSubagentUsageFoldedIntoParentTotals(t *testing.T) {
	redirectToTestLog(t)
	var subReqs, parentReqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSubagentRequest(t, r) {
			atomic.AddInt32(&subReqs, 1)
			sseFinalWithUsage(w, "Findings: one file.", 60, 20)
			return
		}
		switch atomic.AddInt32(&parentReqs, 1) {
		case 1:
			sseToolCallsBatch(w, []apiToolCall{
				batchCall("p1", "task", `{"description":"Find the files."}`),
			}, "tool_calls")
		default:
			sseFinalWithUsage(w, "تمام شد.", 100, 10)
		}
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	a.ctx = context.Background()
	conv, err := a.SendMessage("", "بررسی کن", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)

	id := waitPendingAskID(t, a, "")
	if err := a.RespondPermissionConfirm(id, "allow"); err != nil {
		t.Fatal(err)
	}
	waitConversationUntil(t, a, conv.ID, hasAssistantWith("تمام"))

	// والد (۱۰۰+۱۰) + زیرایجنت (۶۰+۲۰) — بدون شمارش دوباره
	a.mu.Lock()
	used := a.settings.TokensUsed
	a.mu.Unlock()
	if used != 190 {
		t.Fatalf("TokensUsed = %d, want 190 (parent 110 + sub-agent 80)", used)
	}
}

// TestSubagentUsageIncludesRoundLimitFinalRequest: آمارِ درخواست نهاییِ بدون
// ابزار (که کل تاریخچه را دارد و معمولاً بزرگ‌ترین درخواست است) هم باید در
// tally والد و در سرایند گزارش دیده شود — هر دو دقیقاً یکی.
func TestSubagentUsageIncludesRoundLimitFinalRequest(t *testing.T) {
	redirectToTestLog(t)
	var normalReqs, finalReqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := readSubagentReq(t, r)
		if len(req.Tools) == 0 {
			atomic.AddInt32(&finalReqs, 1)
			sseFinalWithUsage(w, "Findings: only a.txt.", 40, 12)
			return
		}
		atomic.AddInt32(&normalReqs, 1)
		sseToolCallsBatchWithUsage(w, []apiToolCall{batchCall("c1", "list_dir", `{}`)}, "tool_calls", 10, 2)
	}))
	defer srv.Close()

	a := subagentApp(t, srv.URL)
	a.mu.Lock()
	a.settings.MaxSubagentRounds = 3
	a.mu.Unlock()

	var tally usageTally
	ctx := withUsageTally(context.Background(), &tally)
	res, ok := a.runSubagent(ctx, "keep exploring forever")
	if !ok {
		t.Fatalf("round limit must still return a report, got: %s", res)
	}
	// ۴ درخواست عادی (۱۰/۲) + درخواست نهایی (۴۰/۱۲)
	if n := atomic.LoadInt32(&normalReqs); n != 4 {
		t.Fatalf("normal rounds = %d, want 4", n)
	}
	if n := atomic.LoadInt32(&finalReqs); n != 1 {
		t.Fatalf("final rounds = %d, want 1", n)
	}
	wantPrompt, wantCompletion := int64(4*10+40), int64(4*2+12)
	p, c := tally.snapshot()
	if p != wantPrompt || c != wantCompletion {
		t.Fatalf("tally = (%d,%d), want (%d,%d) — the final request is missing", p, c, wantPrompt, wantCompletion)
	}
	// سرایند گزارش باید همان اعداد را نشان دهد (~۱۰۰ توکن)
	if want := "~" + strconv.FormatInt(wantPrompt+wantCompletion, 10) + " tokens"; !strings.Contains(res, want) {
		t.Fatalf("report header must show the same totals (%q):\n%s", want, res)
	}
}

// sseToolCallsBatchWithUsage مثل sseToolCallsBatch ولی با chunk پایانیِ usage —
// برای سنجش آمار مصرف دورهای عادیِ زیرایجنت.
func sseToolCallsBatchWithUsage(w http.ResponseWriter, calls []apiToolCall, finish string, prompt, completion int64) {
	w.Header().Set("Content-Type", "text/event-stream")
	for i, c := range calls {
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":%d,\"id\":%q,\"function\":{\"name\":%q,\"arguments\":%q}}]},\"finish_reason\":null}]}\n\n",
			i, c.ID, c.Function.Name, c.Function.Arguments)
	}
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":%q}]}\n\n", finish)
	fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d}}\n\n", prompt, completion)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}
