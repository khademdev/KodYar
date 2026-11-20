// تست‌های — سخت‌سازی فراخوانی ابزار و ایمنی حلقه.
//
// پوشش:
// 1. parseToolArgs: JSON برش‌خورده/نامعتبر با پیام دقیق انگلیسی (شامل جاروی
// مرزهای بایتی متن فارسی — utf8.ValidString روی «همه» پیشوندها).
// 2. validateToolArgs: فیلدهای الزامی/نوع‌ها برای ابزارهای داخلی.
// 3. canonicalToolSignature + toolLoopGuard: شمارش تکرار یکسانِ متوالی؛
// حفظ ZWNJ و عدم نرمال‌سازی حروف عربی/فارسی در امضا.
// 4. محافظ حلقه در استریم واقعی: بار ۳/۴ اجرا نمی‌شود، بار ۵ نوبت با
// اعلان فارسی متوقف می‌شود و جفت‌بندی tool_calls/tool سالم می‌ماند.
// 5. maxAgentRounds قابل‌تنظیم (clamp ۵–۲۰۰، فایل تنظیمات قدیمی، UI view)
// و رفتار استریم با مقدار سفارشی.
// 6. stream_options.include_usage: ارسال، عقب‌نشینی یک‌باره روی ۴۰۰، حافظه‌ی
// per-provider و استفاده از usage واقعی.
//
// همه‌ی تست‌ها قطعی‌اند: هیچ شبکه‌ی واقعی، هیچ خواب طولانی (انتظارهای
// رخدادگاه ۵–۱۰ms با مهلت دفاعی ۵s) و سرورهای httptest محلی.
package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// ---------------------------------------------------------------- کمک‌های تست

// agentStreamApp یک App آزمایشی در حالت ایجنت که Provider آن به سرور داده‌شده
// اشاره می‌کند (ابزارها فعال و مسیر کاری از newTestApp آماده است).
func agentStreamApp(t *testing.T, srvURL string) *App {
	t.Helper()
	a := chatAppOnServer(t, srvURL)
	a.mu.Lock()
	a.settings.AgentMode = true
	a.mu.Unlock()
	return a
}

// sseToolCall یک پاسخ استریم با یک فراخوانی ابزار می‌فرستد.
func sseToolCall(w http.ResponseWriter, callID, name, args, finish string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"function\":{\"name\":%q,\"arguments\":%q}}]},\"finish_reason\":null}]}\n\n", callID, name, args)
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":%q}]}\n\n", finish)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// sseFinalWithUsage پاسخ متنی نهایی + chunk آخر usage (فرمت include_usage).
func sseFinalWithUsage(w http.ResponseWriter, text string, prompt, completion int64) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", text)
	fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d}}\n\n", prompt, completion)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// checkToolPairing هر پیام assistant دارای tool_calls باید بلافاصله و به همان
// ترتیب با len(tool_calls) پیام tool با ToolCallID منطبق دنبال شود (الزام
// پروتکل OpenAI — پیش‌نیاز validateHistory رسمیِ.
func checkToolPairing(t *testing.T, conv *Conversation) {
	t.Helper()
	for i, m := range conv.Messages {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		if i+1+len(m.ToolCalls) > len(conv.Messages) {
			t.Fatalf("assistant msg %d declares %d tool calls but conversation ends early", i, len(m.ToolCalls))
		}
		for j, tc := range m.ToolCalls {
			tm := conv.Messages[i+1+j]
			if tm.Role != "tool" || tm.ToolCallID != tc.ID {
				t.Fatalf("pairing broken at msg %d call %d: want tool result for %q, got role=%q toolCallID=%q", i, j, tc.ID, tm.Role, tm.ToolCallID)
			}
		}
	}
}

// countRoundsWithToolCalls تعداد پیام‌های assistant دارای tool_calls.
func countRoundsWithToolCalls(conv *Conversation) int {
	n := 0
	for _, m := range conv.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- ۱. parseToolArgs

func TestParseToolArgsTruncatedPersian(t *testing.T) {
	// JSON نیمه‌کاره وسط رشته‌ی فارسی — دقیقاً حالت finish_reason=length
	broken := `{"path": "پروژه من/فایل.go", "content": "سلام`
	_, err := parseToolArgs(broken, true)
	if err == nil {
		t.Fatal("truncated JSON must fail")
	}
	msg := err.Error()
	for _, want := range []string{"ERROR:", "truncated", "finish_reason=length", "smaller payload", "split the file"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("truncation message missing %q: %s", want, msg)
		}
	}
	if !utf8.ValidString(msg) {
		t.Fatal("error message must be valid UTF-8 (Persian excerpt inside)")
	}

	// همان JSON بدون hint برش → پیام «invalid» بدون ادعای truncation
	_, err = parseToolArgs(broken, false)
	if err == nil || strings.Contains(err.Error(), "finish_reason=length") {
		t.Fatalf("non-hinted variant must be a plain invalid-JSON error: %v", err)
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("plain variant must say not valid JSON: %v", err)
	}
}

func TestParseToolArgsPersianBoundarySweep(t *testing.T) {
	// بخش ۲: هر برش بایتی ممکن نباید پیام خطای نامعتبر UTF-8 بسازد.
	s := `{"path":"پروژه‌ی من/فایل.go","content":"سلام دنیا ` + "\u200c" + `پایان"`
	for n := 0; n <= len(s); n++ {
		_, err := parseToolArgs(s[:n], true)
		if err == nil {
			continue // پیشوندهای معتبر (مثل "{}" کامل) خطا ندارند
		}
		if !utf8.ValidString(err.Error()) {
			t.Fatalf("prefix len %d produced invalid UTF-8 error: %q", n, err.Error())
		}
	}
}

func TestParseToolArgsVariants(t *testing.T) {
	// خالی/فاصله → map خالی بدون خطا (ابزارهای بدون آرگومان)
	args, err := parseToolArgs("   ", false)
	if err != nil || len(args) != 0 {
		t.Fatalf("empty args must parse to empty map: %v %v", args, err)
	}
	// آبجکت معتبر
	args, err = parseToolArgs(`{"path":"a"}`, false)
	if err != nil || args["path"] != "a" {
		t.Fatalf("valid object must parse: %v %v", args, err)
	}
	// آرایه/رشته/null → خطای «باید آبجکت باشد»
	for _, bad := range []string{`[1,2]`, `"str"`, `null`, `42`} {
		_, err := parseToolArgs(bad, false)
		if err == nil || !strings.Contains(err.Error(), "must be a JSON object") {
			t.Fatalf("%q must fail with object requirement: %v", bad, err)
		}
	}
}

// ---------------------------------------------------------------- ۲. اعتبارسنجی

func TestValidateToolArgsTable(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string // زیررشته‌ی مورد انتظار در خطا؛ خالی یعنی باید بدون خطا بگذرد
	}{
		{"read_file", `{"start_line":1}`, `missing required field "path"`},
		{"read_file", `{"path":"a.txt","start_line":1}`, ""},
		//: رشته‌ی عددی («3») حالا پذیرفته می‌شود — همان چیزی که gi اجرا می‌کند
		{"read_file", `{"path":"a.txt","start_line":"3"}`, ""},
		{"read_file", `{"path":"a.txt","start_line":"  7 "}`, ""}, // trim قبل از Atoi
		{"read_file", `{"path":"a.txt","start_line":"abc"}`, `field "start_line" must be an integer`},
		{"read_file", `{"path":"a.txt","start_line":1.5}`, `field "start_line" must be an integer`},
		{"read_file", `{"path":[1,2]}`, `field "path" must be a string`}, // آرایه هنوز رد می‌شود
		{"read_file", `{"path":42}`, ""},                                 // عدد برای فیلد رشته‌ای — gs آن را «42» می‌کند
		{"read_file", `{"path":"  "}`, `field "path" must not be empty`},
		{"write_file", `{"path":"a.txt"}`, `missing required field "content"`},
		{"write_file", `{"path":"a.txt","content":""}`, ""},                    // فایل خالی مجاز
		{"edit_file", `{"path":"a.txt","old_string":"x","new_string":""}`, ""}, // حذف متن مجاز
		{"edit_file", `{"path":"a.txt","old_string":"","new_string":"y"}`, `field "old_string" must not be empty`},
		{"move_path", `{"from":{"a":1},"to":"b"}`, `field "from" must be a string`}, // آبجکت هنوز رد می‌شود
		{"move_path", `{"from":123,"to":"b"}`, ""},                                  // عدد → «123» (مثل gs)
		{"search_files", `{"path":"."}`, `missing required field "query"`},
		{"run_command", `{"timeout_sec":30}`, `missing required field "command"`},
		{"run_command", `{"command":"go build","timeout_sec":"abc"}`, `field "timeout_sec" must be an integer`},
		{"git_show", `{"ref":"  "}`, `field "ref" must not be empty`},
		{"git_status", `{}`, ""}, // بدون آرگومان
		{"list_dir", `{}`, ""},   // path اختیاری
		{"git_diff", `{"staged":true}`, ""},
		{"git_diff", `{"staged":"true"}`, ""},  // رشته‌ی بولی — مثل gb
		{"git_diff", `{"staged":"FALSE"}`, ""}, // غیرحساس به case
		{"git_diff", `{"staged":1}`, ""},       // عدد ناصفر = true
		{"git_diff", `{"staged":0}`, ""},       // صفر = false
		{"git_diff", `{"staged":"yes"}`, `field "staged" must be a boolean`},
		{"git_log", `{"limit":10}`, ""},
		{"git_log", `{"limit":"5"}`, ""},            // رشته‌ی عددی
		{"custom_mytool", `{"anything":[1,2]}`, ""}, // ابزار سفارشی: شمای آزاد
	}
	for _, c := range cases {
		var args map[string]interface{}
		if err := jsonUnmarshalTest(c.args, &args); err != nil {
			t.Fatalf("%s: fixture JSON broken: %v", c.name, err)
		}
		err := validateToolArgs(c.name, args)
		if c.want == "" {
			if err != nil {
				t.Fatalf("%s %s: unexpected error: %v", c.name, c.args, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.HasPrefix(err.Error(), "ERROR: invalid arguments for "+c.name+": ") {
			t.Fatalf("%s %s: want error containing %q with consistent prefix, got: %v", c.name, c.args, c.want, err)
		}
	}
}

// jsonUnmarshalTest کمک کوچک (جدا از jsonUnmarshalStr تا وابستگی تست‌ها به
// helperهای production minimizing شود).
func jsonUnmarshalTest(s string, v *map[string]interface{}) error {
	if strings.TrimSpace(s) == "" {
		*v = map[string]interface{}{}
		return nil
	}
	return jsonUnmarshalStr(s, v)
}

func TestExecToolCtxArgValidation(t *testing.T) {
	a := newTestApp(t)

	// فیلد الزامی غایب — پیش از هر دروازه‌ی دیگر (حتی run_command)
	r, ok := a.execTool("read_file", `{"start_line":1}`)
	if ok || !strings.Contains(r, `missing required field "path"`) {
		t.Fatalf("read_file without path: %v (%v)", r, ok)
	}
	// نوع اشتباه (آرایه — عدد از به‌بعد مجاز است چون gs می‌پذیردش)
	r, ok = a.execTool("move_path", `{"from":[1],"to":"x"}`)
	if ok || !strings.Contains(r, `field "from" must be a string`) {
		t.Fatalf("move_path array from: %v (%v)", r, ok)
	}
	// run_command با آرگومان ناقص → خطای اعتبارسنجی (نه خطای دروازه‌ی دستور)
	r, ok = a.execTool("run_command", `{"timeout_sec":30}`)
	if ok || !strings.Contains(r, `missing required field "command"`) {
		t.Fatalf("run_command without command: %v (%v)", r, ok)
	}
	// JSON خراب → پیام دقیق جدید (نه پیام عمومی قبلی)
	r, ok = a.execTool("write_file", `{"path":"a.txt","content":"سلام`)
	if ok || !strings.Contains(r, "not valid JSON") || !strings.Contains(r, "split the file into several writes") {
		t.Fatalf("broken JSON must give actionable error: %v", r)
	}
	if !utf8.ValidString(r) {
		t.Fatal("error must stay valid UTF-8 with Persian excerpt")
	}
	// اجرای واقعی همچنان کار می‌کند: ویرایش با new_string خالی (حذف متن)
	if _, ok := a.execTool("write_file", `{"path":"e.txt","content":"hello"}`); !ok {
		t.Fatal("write_file setup failed")
	}
	if r, ok = a.execTool("edit_file", `{"path":"e.txt","old_string":"hello","new_string":""}`); !ok {
		t.Fatalf("edit_file to empty must be allowed: %v", r)
	}
	data, _ := os.ReadFile(filepath.Join(a.settings.WorkspacePath, "e.txt"))
	if string(data) != "" {
		t.Fatalf("file must be empty after deletion edit, got %q", data)
	}
}

// ---------------------------------------------------------------- ۳. امضا و محافظ

func TestCanonicalToolSignature(t *testing.T) {
	// ترتیب کلیدها/فاصله‌گذاری متفاوت → همان امضا
	a := canonicalToolSignature("t", `{"b":1,"a":"x"}`)
	b := canonicalToolSignature("t", `{ "a" : "x", "b" : 1 }`)
	if a != b {
		t.Fatalf("key order/whitespace must not change signature: %s vs %s", a, b)
	}
	// مقدار متفاوت → امضای متفاوت
	if canonicalToolSignature("t", `{"a":"x"}`) == canonicalToolSignature("t", `{"a":"y"}`) {
		t.Fatal("different values must differ")
	}
	// نام ابزار در امضا هست
	if canonicalToolSignature("t1", `{"a":"x"}`) == canonicalToolSignature("t2", `{"a":"x"}`) {
		t.Fatal("different tool names must differ")
	}
	// ZWNJ (U+200C) باید حفظ شود — حذفش امضا را عوض می‌کند (عدم نرمال‌سازی)
	withZWNJ := canonicalToolSignature("t", `{"content":"می`+"\u200c"+`خواهم"}`)
	withoutZWNJ := canonicalToolSignature("t", `{"content":"میخواهم"}`)
	if withZWNJ == withoutZWNJ {
		t.Fatal("ZWNJ must be preserved byte-for-byte in the signature")
	}
	// ي عربی (U+064A) ≠ ی فارسی (U+06CC) — هیچ نرمال‌سازی حروفی
	arabicY := canonicalToolSignature("t", "{\"content\":\"\u064a\"}")
	persianY := canonicalToolSignature("t", "{\"content\":\"\u06CC\"}")
	if arabicY == persianY {
		t.Fatal("Arabic yeh and Persian yeh must NOT be normalized into each other")
	}
	// اعداد هم‌ارزش (1 و 1.0) → همان امضا (JSON عددی یکسان parse می‌شود)
	if canonicalToolSignature("t", `{"n":1}`) != canonicalToolSignature("t", `{"n":1.0}`) {
		t.Fatal("numerically equal JSON numbers should canonicalize equal")
	}
	// JSON خراب → امضای پایدار روی رشته‌ی خام
	if canonicalToolSignature("t", `{"broken`) != canonicalToolSignature("t", `{"broken`) {
		t.Fatal("raw fallback must be deterministic")
	}
	if canonicalToolSignature("t", `{"broken`) == canonicalToolSignature("t", `{"broken2`) {
		t.Fatal("different broken payloads must differ")
	}
}

func TestToolLoopGuardObserve(t *testing.T) {
	g := &toolLoopGuard{}
	// پنج بار یکسان متوالی → 1..5
	for want := 1; want <= 5; want++ {
		if got := g.observe("list_dir", `{"path":"."}`); got != want {
			t.Fatalf("run %d: got %d", want, got)
		}
	}
	// فراخوانی متفاوت → شمارنده از نو
	if got := g.observe("list_dir", `{"path":"sub"}`); got != 1 {
		t.Fatalf("different args must reset run, got %d", got)
	}
	// همان فراخوانی قبلی دوباره → 2 (متوالی با تازگی)
	if got := g.observe("list_dir", `{"path":"sub"}`); got != 2 {
		t.Fatalf("second consecutive must be 2, got %d", got)
	}
	// ابزار دیگر → از نو
	if got := g.observe("read_file", `{"path":"sub"}`); got != 1 {
		t.Fatalf("different tool must reset run, got %d", got)
	}
	// آرگومان هم‌ارز (ترتیب کلید متفاوت) → ادامه‌ی همان اجرای متوالی
	eq1 := g.observe("search_files", `{"query":"x","path":"."}`)
	eq2 := g.observe("search_files", `{"path":".","query":"x"}`)
	if eq1 != 1 || eq2 != 2 {
		t.Fatalf("canonically-equal args must continue the run: %d %d", eq1, eq2)
	}
	// نام آخرین ابزار ثبت می‌شود (برای اعلان فارسی)
	if g.lastName != "search_files" {
		t.Fatalf("lastName must track last call, got %q", g.lastName)
	}
}

func TestGuardMessagesLanguage(t *testing.T) {
	// پیام مسدودسازی: انگلیسی و قابل‌اقدام
	blocked := repeatBlockedToolError("create_dir", 3)
	for _, want := range []string{"ERROR:", "identical repeated tool call blocked", "create_dir", "3 times", "DIFFERENT approach"} {
		if !strings.Contains(blocked, want) {
			t.Fatalf("blocked message missing %q: %s", want, blocked)
		}
	}
	// اعلان توقف: فارسی، UTF-8 معتبر، با نام ابزار و تعداد
	notice := doomLoopStopNotice("create_dir", faDigits(5))
	for _, want := range []string{"create_dir", "۵ بار", "حلقه‌ی بی\u200cپایان", "متوقف شد"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("stop notice missing %q: %s", want, notice)
		}
	}
	if !utf8.ValidString(notice) {
		t.Fatal("Persian notice must be valid UTF-8")
	}
	// پیام نتیجه‌ی پس از توقف: انگلیسی و روشن
	if !strings.Contains(toolTurnStoppedError, "loop guard") {
		t.Fatalf("turn-stopped error must explain the loop guard: %s", toolTurnStoppedError)
	}
}

// ---------------------------------------------------------------- ۴. محافظ حلقه در استریم

func TestStreamChatDoomLoopBlocksAndStops(t *testing.T) {
	redirectToTestLog(t)
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqs, 1)
		// مدل «گیر کرده»: هر دور دقیقاً همان فراخوانی را تکرار می‌کند —
		// آرگومان فارسی با ZWNJ تا عدم‌نرمال‌سازی هم در مسیر واقعی ثابت بماند.
		sseToolCall(w, "call-x", "create_dir", `{"path":"پروژه‌ی تکرار"}`, "tool_calls")
	}))
	defer srv.Close()

	a := agentStreamApp(t, srv.URL)
	conv, err := a.SendMessage("", "یک پوشه بساز و گیر کن", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)

	// انتظار: ۵ دور (اجرا، اجرا، مسدود، مسدود، توقف) — نه ۲۵ دورِ سقف.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&reqs) < 5 {
		time.Sleep(5 * time.Millisecond)
	}
	waitConversationUntil(t, a, conv.ID, hasAssistantWith("حلقه‌ی بی\u200cپایان"))
	time.Sleep(150 * time.Millisecond) // فرصتِ دورِ اضافی اگر باگ باشد
	if got := atomic.LoadInt32(&reqs); got != 5 {
		t.Fatalf("doom loop must stop after 5 rounds, got %d requests", got)
	}

	a.mu.Lock()
	loaded, lerr := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if lerr != nil {
		t.Fatal(lerr)
	}
	if n := countRoundsWithToolCalls(loaded); n != 5 {
		t.Fatalf("want 5 assistant tool rounds, got %d", n)
	}
	checkToolPairing(t, loaded)

	executed, blocked, stopped := 0, 0, 0
	for _, m := range loaded.Messages {
		if m.Role != "tool" || m.ToolName != "create_dir" {
			continue
		}
		switch {
		case strings.Contains(m.Content, "loop guard"):
			stopped++
		case strings.Contains(m.Content, "identical repeated tool call blocked"):
			blocked++
		default:
			executed++
		}
	}
	if executed != 2 || blocked != 2 || stopped != 1 {
		t.Fatalf("want executed=2 blocked=2 stopped=1, got %d/%d/%d", executed, blocked, stopped)
	}
	// پیام نهایی فارسی، نه abort
	var final *Message
	for i := range loaded.Messages {
		if m := &loaded.Messages[i]; m.Role == "assistant" && len(m.ToolCalls) == 0 && strings.Contains(m.Content, "حلقه‌ی") {
			final = m
		}
	}
	if final == nil {
		t.Fatal("final Persian notice message missing")
	}
	if final.Aborted {
		t.Fatal("doom-loop stop is not an abort")
	}
	if !strings.Contains(final.Content, "create_dir") || !strings.Contains(final.Content, "۵ بار") {
		t.Fatalf("notice must name the tool and the Persian repeat count: %s", final.Content)
	}
	// پوشه فقط دو بارِ اجرای واقعی ساخته شده — یک بار موفق کافی است
	if _, err := os.Stat(filepath.Join(a.settings.WorkspacePath, "پروژه‌ی تکرار")); err != nil {
		t.Fatalf("first real execution must have created the dir: %v", err)
	}
}

// ---------------------------------------------------------------- ۵. maxAgentRounds

func TestMaxAgentRoundsClampAndSettingsLifecycle(t *testing.T) {
	for _, c := range []struct{ in, want int }{
		{0, 25}, {-3, 25}, {3, 5}, {5, 5}, {25, 25}, {40, 40}, {200, 200}, {1000, 200},
	} {
		if got := clampMaxAgentRounds(c.in); got != c.want {
			t.Fatalf("clamp(%d) = %d, want %d", c.in, got, c.want)
		}
	}

	// فایل تنظیمات «قدیمی» بدون فیلد جدید → پیش‌فرض ۲۵ (سازگاری)
	dir := t.TempDir()
	os.Setenv("KODYAR_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"provider":"x","model":"m","temperature":0.7}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	if err := a.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if a.settings.MaxAgentRounds != defaultMaxAgentRounds {
		t.Fatalf("old settings must default to %d, got %d", defaultMaxAgentRounds, a.settings.MaxAgentRounds)
	}
	// مقدار خارج از محدوده در فایل → clamp
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"maxAgentRounds":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a2 := NewApp()
	if err := a2.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if a2.settings.MaxAgentRounds != 5 {
		t.Fatalf("out-of-range file value must clamp to 5, got %d", a2.settings.MaxAgentRounds)
	}
	// متد bound: ذخیره، ماندگاری روی دیسک و clamp
	if err := a2.SetMaxAgentRounds(50); err != nil {
		t.Fatal(err)
	}
	a3 := NewApp()
	if err := a3.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if a3.settings.MaxAgentRounds != 50 {
		t.Fatalf("SetMaxAgentRounds must persist 50, got %d", a3.settings.MaxAgentRounds)
	}
	if err := a3.SetMaxAgentRounds(999); err != nil {
		t.Fatal(err)
	}
	if a3.settings.MaxAgentRounds != 200 {
		t.Fatalf("SetMaxAgentRounds must clamp to 200, got %d", a3.settings.MaxAgentRounds)
	}
	// نمای UI
	v := a3.SettingsView()
	if got, _ := v["maxAgentRounds"].(int); got != 200 {
		t.Fatalf("SettingsView must expose clamped maxAgentRounds, got %v", v["maxAgentRounds"])
	}
}

func TestStreamChatMaxAgentRoundsConfigurable(t *testing.T) {
	redirectToTestLog(t)
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&reqs, 1))
		// هر دور آرگومان متفاوت تا محافظ حلقه‌ی تکرار (تست جداگانه‌ی خودش)
		// فعال نشود و فقط سقفِ دورها سنجیده شود.
		sseToolCall(w, "call-y", "list_dir", fmt.Sprintf(`{"path":"round-%d"}`, n), "tool_calls")
	}))
	defer srv.Close()

	a := agentStreamApp(t, srv.URL)
	a.mu.Lock()
	a.settings.MaxAgentRounds = 6
	a.mu.Unlock()

	conv, err := a.SendMessage("", "مدام فهرست بده", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)

	waitConversationUntil(t, a, conv.ID, hasAssistantWith("۶ دور"))
	a.mu.Lock()
	loaded, lerr := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if lerr != nil {
		t.Fatal(lerr)
	}
	if n := countRoundsWithToolCalls(loaded); n != 6 {
		t.Fatalf("custom limit 6 must allow exactly 6 tool rounds, got %d", n)
	}
	if got := atomic.LoadInt32(&reqs); got != 7 { // ۶ دور ابزار + دورِ هفتم که به سقف می‌خورد
		t.Fatalf("want 7 requests (6 tool rounds + limit round), got %d", got)
	}
	checkToolPairing(t, loaded)
}

// TestStreamChatTruncatedToolCallPersian سناریوی اصلیِ finish_reason=length:
// آرگومان‌های ابزار وسط رشته‌ی فارسی برش خورده‌اند. باید (۱) ابزار اجرا نشود
// (بدون عارضه‌ی جانبی)، (۲) مدل پیام خطای دقیق انگلیسیِ «برش‌خورده» بگیرد،
// (۳) نوبت باقی بماند و دور بعد پاسخ نهایی عادی تمام شود.
func TestStreamChatTruncatedToolCallPersian(t *testing.T) {
	redirectToTestLog(t)
	var reqs int32
	brokenArgs := `{"path": "پروژه من/فایل.go", "content": "سلام`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&reqs, 1) == 1 {
			sseToolCall(w, "call-t", "write_file", brokenArgs, "length")
			return
		}
		sseOK(w, "الان کوچک‌تر می‌فرستم")
	}))
	defer srv.Close()

	a := agentStreamApp(t, srv.URL)
	conv, err := a.SendMessage("", "این فایل را بنویس", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)

	waitConversationUntil(t, a, conv.ID, hasAssistantWith("الان کوچک‌تر می‌فرستم"))
	a.mu.Lock()
	loaded, lerr := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if lerr != nil {
		t.Fatal(lerr)
	}
	// نتیجه‌ی ابزار: خطای دقیق برش — انگلیسی، UTF-8 معتبر، بدون اجرای write
	var toolMsg *Message
	for i := range loaded.Messages {
		if m := &loaded.Messages[i]; m.Role == "tool" && m.ToolName == "write_file" {
			toolMsg = m
		}
	}
	if toolMsg == nil {
		t.Fatal("write_file tool result missing")
	}
	if toolMsg.ToolOK {
		t.Fatal("truncated call must not report success")
	}
	for _, want := range []string{"truncated", "finish_reason=length", "smaller payload", "split the file"} {
		if !strings.Contains(toolMsg.Content, want) {
			t.Fatalf("tool error missing %q: %s", want, toolMsg.Content)
		}
	}
	if !utf8.ValidString(toolMsg.Content) {
		t.Fatal("tool error must be valid UTF-8 (Persian args embedded)")
	}
	// بدون عارضه‌ی جانبی: پوشه‌ی کاری همچنان خالی است
	entries, _ := os.ReadDir(a.settings.WorkspacePath)
	if len(entries) != 0 {
		t.Fatalf("truncated write must not create files, found %d", len(entries))
	}
	checkToolPairing(t, loaded)
	if got := atomic.LoadInt32(&reqs); got != 2 {
		t.Fatalf("turn must continue after the precise error (2 rounds), got %d requests", got)
	}
}

// TestStreamChatMalformedToolCallNotTruncated همان JSON خراب ولی با
// finish_reason=tool_calls → پیام «not valid JSON» بدون ادعای برش.
func TestStreamChatMalformedToolCallNotTruncated(t *testing.T) {
	redirectToTestLog(t)
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&reqs, 1) == 1 {
			sseToolCall(w, "call-m", "read_file", `{"path": "فایل`, "tool_calls")
			return
		}
		sseOK(w, "درست شد")
	}))
	defer srv.Close()

	a := agentStreamApp(t, srv.URL)
	conv, err := a.SendMessage("", "بخوان", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)

	waitConversationUntil(t, a, conv.ID, hasAssistantWith("درست شد"))
	a.mu.Lock()
	loaded, lerr := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if lerr != nil {
		t.Fatal(lerr)
	}
	for _, m := range loaded.Messages {
		if m.Role != "tool" || m.ToolName != "read_file" {
			continue
		}
		if !strings.Contains(m.Content, "not valid JSON") {
			t.Fatalf("non-truncated malformed args must say not valid JSON: %s", m.Content)
		}
		if strings.Contains(m.Content, "finish_reason=length") {
			t.Fatalf("must not claim truncation without the length hint: %s", m.Content)
		}
		if !utf8.ValidString(m.Content) {
			t.Fatal("tool error must be valid UTF-8")
		}
	}
	checkToolPairing(t, loaded)
}

// ---------------------------------------------------------------- ۶. stream_options

func TestIsStreamOptionsUnsupported(t *testing.T) {
	if !isStreamOptionsUnsupported(400, `{"error":{"message":"stream_options is not supported"}}`) {
		t.Fatal("400 mentioning stream_options must be detected")
	}
	if !isStreamOptionsUnsupported(400, "Include_Usage unknown") {
		t.Fatal("detection must be case-insensitive")
	}
	if isStreamOptionsUnsupported(422, `{"error":{"message":"stream_options bad"}}`) {
		t.Fatal("only 400 qualifies per spec")
	}
	if isStreamOptionsUnsupported(400, `{"error":{"message":"max_tokens is not supported"}}`) {
		t.Fatal("unrelated 400 must not match")
	}
}

func TestProviderStreamUsageMemory(t *testing.T) {
	old := resetStreamUsageMemoryForTest()
	defer restoreStreamUsageMemoryForTest(old)

	if !providerSupportsStreamUsage("http://x/v1/") {
		t.Fatal("fresh provider must support stream_options")
	}
	markStreamUsageBroken("http://x/v1/")
	if providerSupportsStreamUsage("http://x/v1") { // کلید بدون اسلش انتهایی یکسان
		t.Fatal("trailing slash must be normalized in the memory key")
	}
	if !providerSupportsStreamUsage("http://y/v1") {
		t.Fatal("other providers must be unaffected")
	}
}

func TestStreamChatUsageOptionFallbackAndMemory(t *testing.T) {
	redirectToTestLog(t)
	old := resetStreamUsageMemoryForTest()
	defer restoreStreamUsageMemoryForTest(old)

	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		if bytes.Contains(raw, []byte(`"stream_options"`)) {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"stream_options is not supported by this endpoint"}}`)
			return
		}
		sseFinalWithUsage(w, "پاسخ نهایی", 111, 7)
	}))
	defer srv.Close()

	a := agentStreamApp(t, srv.URL)
	a.mu.Lock()
	usedBefore := a.settings.TokensUsed
	a.mu.Unlock()

	conv, err := a.SendMessage("", "با usage واقعی جواب بده", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)

	waitConversationUntil(t, a, conv.ID, hasAssistantWith("پاسخ نهایی"))

	mu.Lock()
	first := len(bodies)
	mu.Unlock()
	if first != 2 {
		t.Fatalf("first send must be 400-fallback then success (2 requests), got %d", first)
	}
	mu.Lock()
	b0, b1 := bodies[0], bodies[1]
	mu.Unlock()
	if !strings.Contains(b0, `"stream_options"`) || !strings.Contains(b0, `"include_usage":true`) {
		t.Fatalf("first request must carry stream_options.include_usage: %s", b0)
	}
	if strings.Contains(b1, "stream_options") {
		t.Fatal("fallback retry must omit stream_options")
	}

	// usage واقعی باید ثبت شده باشد: توکن پیام نهایی = ۷ و TokensUsed += ۱۱۸
	a.mu.Lock()
	loaded, lerr := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if lerr != nil {
		t.Fatal(lerr)
	}
	var final *Message
	for i := range loaded.Messages {
		if m := &loaded.Messages[i]; m.Role == "assistant" && strings.Contains(m.Content, "پاسخ نهایی") {
			final = m
		}
	}
	if final == nil || final.Tokens != 7 {
		t.Fatalf("real usage must set final message tokens to 7, got %+v", final)
	}
	a.mu.Lock()
	usedAfter := a.settings.TokensUsed
	a.mu.Unlock()
	if usedAfter-usedBefore != 111+7 {
		t.Fatalf("TokensUsed must increase by real usage 118, got delta %d", usedAfter-usedBefore)
	}

	// حافظه‌ی per-provider: پیام دوم همان Provider را بدون ۴۰۰ و بدون
	// stream_options می‌فرستد (یک درخواست).
	conv2, err := a.SendMessage("", "دوباره", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitConversationUntil(t, a, conv2.ID, hasAssistantWith("پاسخ نهایی"))
	mu.Lock()
	total := len(bodies)
	last := bodies[total-1]
	mu.Unlock()
	if total != 3 {
		t.Fatalf("second send must be a single request (memory works), got %d total", total)
	}
	if strings.Contains(last, "stream_options") {
		t.Fatal("remembered provider must not receive stream_options anymore")
	}
}

// ---------------------------------------------------------------- ۷. تبدیل نوع مشترک

// TestCoerceHelpers تست واحد توابع مشترک تبدیل نوع — مرجعِ واحدِ اعتبارسنجی
// و مجری. فارسی با ZWNJ باید بایت‌به‌بایت برگردد (عدم نرمال‌سازی).
func TestCoerceHelpers(t *testing.T) {
	// رشته‌ی فارسی با ZWNJ — دست‌نخورده
	z := "می\u200cخواهم"
	if s, ok := coerceString(z); !ok || s != z {
		t.Fatalf("Persian string with ZWNJ must round-trip byte-exact: %q %v", s, ok)
	}
	// عدد/بول برای فیلد رشته‌ای — همان تبدیل قدیمی gs
	if s, ok := coerceString(60.0); !ok || s != "60" {
		t.Fatalf("number 60 must coerce to \"60\", got %q %v", s, ok)
	}
	if s, ok := coerceString(true); !ok || s != "true" {
		t.Fatalf("bool true must coerce to \"true\", got %q %v", s, ok)
	}
	if _, ok := coerceString([]interface{}{1}); ok {
		t.Fatal("array must not coerce to string")
	}
	if _, ok := coerceString(nil); ok {
		t.Fatal("nil must not coerce to string")
	}

	// اعداد صحیح — شکل‌های مجاز و رد‌شده‌ها
	for _, c := range []struct {
		v    interface{}
		want int
		ok   bool
	}{
		{60.0, 60, true}, {"60", 60, true}, {"  60\t", 60, true}, {0.0, 0, true},
		{1.5, 0, false}, {"abc", 0, false}, {true, 0, false}, {"", 0, false}, {nil, 0, false},
	} {
		n, ok := coerceInt(c.v)
		if ok != c.ok || (ok && n != c.want) {
			t.Fatalf("coerceInt(%v) = (%d,%v), want (%d,%v)", c.v, n, ok, c.want, c.ok)
		}
	}

	// بول‌ها — شکل‌های مجاز و رد‌شده‌ها
	for _, c := range []struct {
		v    interface{}
		want bool
		ok   bool
	}{
		{true, true, true}, {false, false, true},
		{"true", true, true}, {"FALSE", false, true}, {" Yes ", false, false}, // فقط «true»/«false» (با trim)؛ «Yes» رد می‌شود
		{"yes", false, false}, {"1", false, false},
		{1.0, true, true}, {0.0, false, true}, {2.5, true, true},
		{nil, false, false},
	} {
		b, ok := coerceBool(c.v)
		if ok != c.ok || (ok && b != c.want) {
			t.Fatalf("coerceBool(%v) = (%v,%v), want (%v,%v)", c.v, b, ok, c.want, c.ok)
		}
	}
}

// TestExecToolCtxLooseArgsExecute اجرای واقعی با آرگومان‌های loose-typed —
// همان سناریوی مدل‌های GLM/متن‌باز: «60» به‌جای 60 و «true» به‌جای true.
// اعتبارسنجی نباید مانع اجرای چیزی شود که مجری همیشه می‌توانست انجام دهد.
func TestExecToolCtxLooseArgsExecute(t *testing.T) {
	a := newTestApp(t)

	if _, ok := a.execTool("write_file", `{"path":"loose.txt","content":"خط یک\nخط دو\n"}`); !ok {
		t.Fatal("setup write failed")
	}
	// start_line/end_line به‌صورت رشته‌ی عددی
	r, ok := a.execTool("read_file", `{"path":"loose.txt","start_line":"2","end_line":2}`)
	if !ok || !strings.Contains(r, "خط دو") {
		t.Fatalf("read_file with string line numbers must work: %v (%v)", r, ok)
	}
	// replace_all به‌صورت رشته‌ی بولی — ویرایش واقعی
	if r, ok = a.execTool("edit_file", `{"path":"loose.txt","old_string":"خط دو","new_string":"خط ۲","replace_all":"TRUE"}`); !ok {
		t.Fatalf("edit_file with string boolean must work: %v", r)
	}
	if data, err := os.ReadFile(filepath.Join(a.settings.WorkspacePath, "loose.txt")); err != nil || !strings.Contains(string(data), "خط ۲") {
		t.Fatalf("edit must have been applied: %v %q", err, data)
	}
	// search_files با regex رشته‌ای و max_results رشته‌ای
	if r, ok = a.execTool("search_files", `{"query":"خط","regex":"false","max_results":"5"}`); !ok || !strings.Contains(r, "loose.txt") {
		t.Fatalf("search_files with loose types must work: %v (%v)", r, ok)
	}
	// امضای 60 و «60» (بدون کرش؛ متفاوت بودنش مجاز است)
	s1 := canonicalToolSignature("run_command", `{"command":"x","timeout_sec":60}`)
	s2 := canonicalToolSignature("run_command", `{"command":"x","timeout_sec":"60"}`)
	if s1 == "" || s2 == "" {
		t.Fatal("signatures must compute")
	}
	// مقادیر فارسی با ZWNJ در آرگومان‌ها از فشرده‌سازی/اعتبارسنجی بی‌آسیب می‌گذرند
	z := "می\u200cخواهم"
	if s, ok := coerceString(z); !ok || s != z {
		t.Fatal("ZWNJ must survive coercion")
	}
}

// ---------------------------------------------------------------- ۸. عقب‌نشینی‌ها دور نمی‌سوزانند

// TestStreamChatUsageFallbackDoesNotConsumeRounds عقب‌نشینی stream_options
// نباید از سقف maxAgentRounds کم کند: با سقف ۲، بعد از یک عقب‌نشینی هنوز
// «دو» دور ابزار کامل اجرا می‌شود و پاسخ نهایی می‌آید.
func TestStreamChatUsageFallbackDoesNotConsumeRounds(t *testing.T) {
	redirectToTestLog(t)
	old := resetStreamUsageMemoryForTest()
	defer restoreStreamUsageMemoryForTest(old)

	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch n := int(atomic.AddInt32(&reqs, 1)); n {
		case 1:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"stream_options is not supported by this endpoint"}}`)
		case 2:
			sseToolCall(w, "fb1", "list_dir", `{"path":"round-one"}`, "tool_calls")
		case 3:
			sseToolCall(w, "fb2", "list_dir", `{"path":"round-two"}`, "tool_calls")
		default:
			sseOK(w, "پایان کار")
		}
	}))
	defer srv.Close()

	a := agentStreamApp(t, srv.URL)
	a.mu.Lock()
	a.settings.MaxAgentRounds = 2
	a.mu.Unlock()

	conv, err := a.SendMessage("", "کار را تمام کن", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)
	waitConversationUntil(t, a, conv.ID, hasAssistantWith("پایان کار"))

	// بدون اصلاحِ دورها: عقب‌نشینی دورِ ۰ را می‌خورد و بعد از یک دور ابزار
	// پیام «حداکثر تعداد مراحل» می‌آمد؛ حالا باید هر دو دور اجرا شده باشند.
	a.mu.Lock()
	loaded, lerr := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if lerr != nil {
		t.Fatal(lerr)
	}
	if n := countRoundsWithToolCalls(loaded); n != 2 {
		t.Fatalf("fallback must not consume agent rounds; want 2 tool rounds, got %d", n)
	}
	if got := atomic.LoadInt32(&reqs); got != 4 { // عقب‌نشینی + دو دور ابزار + پاسخ نهایی
		t.Fatalf("want 4 requests (fallback + 2 tool rounds + final), got %d", got)
	}
	checkToolPairing(t, loaded)
}

// TestStreamChatOverflowRetryDoesNotConsumeRounds تلاش مجددِ سرریز کانتکست
// هم دور نمی‌سوزاند: درخواست اول ۴۰۰ِ سرریز می‌گیرد (فشرده‌سازی تهاجمی +
// نشانگر حذف) و بعد با سقف ۱، همان یک دور ابزار کامل اجرا می‌شود.
func TestStreamChatOverflowRetryDoesNotConsumeRounds(t *testing.T) {
	redirectToTestLog(t)
	var mu sync.Mutex
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body.Close()
		mu.Lock()
		bodies = append(bodies, raw)
		n := len(bodies)
		mu.Unlock()
		switch n {
		case 1:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"This model's maximum context length is 4096 tokens. However, your messages resulted in 6123 tokens."}}`)
		case 2:
			sseToolCall(w, "ov1", "list_dir", `{"path":"overflow-round"}`, "tool_calls")
		default:
			sseOK(w, "انجام شد")
		}
	}))
	defer srv.Close()

	a := agentStreamApp(t, srv.URL)
	a.mu.Lock()
	a.settings.MaxAgentRounds = 1
	a.mu.Unlock()

	// گفتگوی از‌قبلذخیره‌شده تا فشرده‌سازی تهاجمی چیزی برای حذف داشته باشد
	var msgs []Message
	for i := 0; i < 6; i++ {
		msgs = append(msgs,
			Message{ID: newID(), Role: "user", Content: "سؤال " + fmt.Sprint(i) + " " + strings.Repeat("متن", 40)},
			Message{ID: newID(), Role: "assistant", Content: "پاسخ " + fmt.Sprint(i) + " " + strings.Repeat("پاسخ", 40)},
		)
	}
	convID := seededConversation(t, a, msgs)

	conv, err := a.SendMessage(convID, "حالا کار را بکن", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)
	waitConversationUntil(t, a, conv.ID, hasAssistantWith("انجام شد"))

	mu.Lock()
	count := len(bodies)
	second := string(bodies[1])
	mu.Unlock()
	if count != 3 {
		t.Fatalf("want 3 requests (overflow + tool round + final), got %d", count)
	}
	// درخواست دوم پس از فشرده‌سازی تهاجمی: نشانگر حذف کانتکست داخلش هست
	if !strings.Contains(second, "[context notice]") {
		t.Fatal("the retried request must carry the context-drop marker")
	}
	a.mu.Lock()
	loaded, lerr := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if lerr != nil {
		t.Fatal(lerr)
	}
	if n := countRoundsWithToolCalls(loaded); n != 1 {
		t.Fatalf("overflow retry must not consume the only agent round, got %d tool rounds", n)
	}
	checkToolPairing(t, loaded)
}
