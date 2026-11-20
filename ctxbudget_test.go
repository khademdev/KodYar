// تست‌های — مدیریت کانتکست آگاه از توکن.
//
// پوشش:
// 1. contextBudget — فرمول بودجه (پنجره − رزرو خروجی − ۱۰٪) و کف/نامعلوم.
// 2. estimateHistoryMessageTokens + historyTokenCache — شمارش با کش، حساب‌شدن
// tool_calls، باطل‌سازی و ایمنی هنگام کوتاه‌شدن تاریخچه.
// 3. validateHistory — ترمیم جفت‌بندی tool_calls/tool، system تکراری/وسط،
// محتوای خالی، role نامعتبر؛ و هم‌دانه‌بودنِ ترمیم.
// 4. compactHistoryForBudget — فشرده‌سازی خودکار: حفظ system/Pin/آخرین پیام
// کاربر، اعتبار جفت‌بندی پس از حذف، کف پیام.
// 5. recoverHistoryFromOverflow با بودجه‌ی نامعلوم — حذف نصف واحدها.
// 6. isContextOverflowError — تشخیص جدولی (بدون تداخل با stream_options/
// tools/max_tokens).
// 7. استریم واقعی: فشرده‌سازی خودکار پیش از اولین درخواست (فایل گفتگو
// دست‌نخورده) و بازیابی از ۴۰۰ِ سرریز با یک تلاش مجدد.
//
// همه‌ی تست‌ها قطعی‌اند: سرورهای httptest محلی، هیچ شبکه‌ی واقعی و مهلت
// دفاعی ۵ ثانیه برای انتظارهای رخدادگون.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// apiCall سازنده‌ی apiToolCall برای تست‌ها (بدون literal ساختار ناشناس).
func apiCall(id, name, args string) apiToolCall {
	var c apiToolCall
	c.ID = id
	c.Type = "function"
	c.Function.Name = name
	c.Function.Arguments = args
	return c
}

// ---------------------------------------------------------------- ۱. بودجه

func TestContextBudget(t *testing.T) {
	for _, c := range []struct {
		cw, maxTokens int
		want          int64
	}{
		{0, 0, 0},                   // پنجره‌ی نامعلوم → بدون بودجه
		{128000, 0, 107008},         // ۱۲۸۰۰۰ − ۸۱۹۲ (پیش‌فرض) − ۱۲۸۰۰
		{128000, 4000, 111200},      // ۱۲۸۰۰۰ − ۴۰۰۰ − ۱۲۸۰۰
		{8192, 8000, 5325},          // رزرو سقف‌خورده به ۲۰۴۸: ۸۱۹۲ − ۲۰۴۸ − ۸۱۹
		{20000, 20000, 13000},       // رزرو سقف‌خورده به ۵۰۰۰
		{2048, 0, 1332},             // ۲۰۴۸ − ۵۱۲ − ۲۰۴
		{1000, 0, minContextBudget}, // کمتر از کف → کف
		{5000, -5, 3250},            // max_tokens منفی = نامحدود
	} {
		if got := contextBudget(c.cw, c.maxTokens); got != c.want {
			t.Fatalf("contextBudget(%d, %d) = %d, want %d", c.cw, c.maxTokens, got, c.want)
		}
	}
}

// ---------------------------------------------------------------- ۲. شمارش با کش

func TestEstimateHistoryMessageTokens(t *testing.T) {
	plain := chatMessage{Role: "user", Content: "hello world"}
	if got, want := estimateHistoryMessageTokens(plain), estimateTokens("hello world")+4; got != want {
		t.Fatalf("plain message: got %d want %d", got, want)
	}
	withCalls := chatMessage{
		Role:      "assistant",
		Content:   "",
		ToolCalls: []apiToolCall{apiCall("c1", "write_file", `{"path":"a.txt","content":"..."}`)},
	}
	got := estimateHistoryMessageTokens(withCalls)
	want := int64(4) + estimateTokens("write_file") + estimateTokens(`{"path":"a.txt","content":"..."}`) + estimateTokens("c1") + 3
	if got != want {
		t.Fatalf("tool_call overhead: got %d want %d", got, want)
	}
	if got <= 4 {
		t.Fatal("tool_calls must be counted")
	}
	// پیام tool: سربار tool_call_id حساب می‌شود
	toolMsg := chatMessage{Role: "tool", ToolCallID: "call_xyz", Content: "r"}
	if got, want := estimateHistoryMessageTokens(toolMsg), estimateTokens("r")+4+5; got != want {
		t.Fatalf("tool message: got %d want %d", got, want)
	}
}

func TestHistoryTokenCache(t *testing.T) {
	h := []chatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "سلام دنیا"},
		{Role: "assistant", ToolCalls: []apiToolCall{apiCall("c1", "list_dir", `{}`)}},
	}
	c := &historyTokenCache{}
	if got := c.totalOf(h[:1]); got == 0 {
		t.Fatal("system message must count")
	}
	first := c.totalOf(h[:1])
	if c.n != 1 || c.total != first {
		t.Fatalf("cache state after first call: n=%d total=%d", c.n, c.total)
	}
	// رشد تاریخچه — فقط پیام‌های تازه حساب می‌شوند (n جلو می‌رود)
	if got := c.totalOf(h); got <= first {
		t.Fatalf("total must grow with history: %d then %d", first, got)
	}
	if c.n != 3 {
		t.Fatalf("all 3 messages must be accounted, n=%d", c.n)
	}
	// هم‌ارزی با شمارش مستقیم
	if c.total != sumHistoryTokens(h) {
		t.Fatalf("cached total %d != direct %d", c.total, sumHistoryTokens(h))
	}
	// فراخوانی مجدد بدون تغییر → همان مقدار بدون بازشماری
	before := c.n
	if c.totalOf(h) != c.total || c.n != before {
		t.Fatal("repeat call must not recount")
	}
	// invalidate → بازشماری کامل
	c.invalidate()
	if c.n != 0 || c.total != 0 {
		t.Fatal("invalidate must clear the cache")
	}
	_ = c.totalOf(h)
	if c.total != sumHistoryTokens(h) {
		t.Fatal("after invalidate the recount must match")
	}
	// کوتاه‌شدن تاریخچه بدون invalidate صریح → شمارش از نو (بدون panic)
	_ = c.totalOf(h[:2])
	if c.n != 2 || c.total != sumHistoryTokens(h[:2]) {
		t.Fatalf("shrink must auto-recount: n=%d total=%d", c.n, c.total)
	}
}

// ---------------------------------------------------------------- ۳. بازبینی/ترمیم

// validPairedHistory یک تاریخچه‌ی سالم نمونه (system + یک راند ابزار + متن).
func validPairedHistory() []chatMessage {
	return []chatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "q1"},
		{Role: "assistant", ToolCalls: []apiToolCall{apiCall("c1", "read_file", `{}`), apiCall("c2", "list_dir", `{}`)}},
		{Role: "tool", ToolCallID: "c1", Content: "r1"},
		{Role: "tool", ToolCallID: "c2", Content: "r2"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "q2"},
	}
}

func TestValidateHistoryCleanPasses(t *testing.T) {
	h := validPairedHistory()
	out, problems := validateHistory(h)
	if len(problems) != 0 {
		t.Fatalf("clean history must have no problems: %v", problems)
	}
	if len(out) != len(h) {
		t.Fatalf("clean history must keep its length: %d vs %d", len(out), len(h))
	}
	for i := range h {
		if out[i].Role != h[i].Role || historyContentString(out[i].Content) != historyContentString(h[i].Content) {
			t.Fatalf("clean message %d altered: %+v vs %+v", i, out[i], h[i])
		}
	}
}

func TestValidateHistoryRepairs(t *testing.T) {
	// ۱) نتیجه‌ی ابزار بی‌صاحب → حذف
	h := []chatMessage{
		{Role: "system", Content: "s"},
		{Role: "tool", ToolCallID: "ghost", Content: "orphan"},
		{Role: "user", Content: "hi"},
	}
	out, problems := validateHistory(h)
	if len(out) != 2 || len(problems) == 0 {
		t.Fatalf("orphan tool must be dropped: %d msgs, problems=%v", len(out), problems)
	}

	// ۲) نتیجه‌ی گمشده → ساخت جایگزین در جای درست (و جفت‌بندی سالم)
	h = []chatMessage{
		{Role: "system", Content: "s"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []apiToolCall{apiCall("c1", "read_file", `{}`)}},
		{Role: "user", Content: "next"},
	}
	out, problems = validateHistory(h)
	if len(out) != 5 || out[3].Role != "tool" || out[3].ToolCallID != "c1" {
		t.Fatalf("missing tool result must be synthesized in place: %+v", out)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "synthesized") {
		t.Fatalf("synthesis must be reported: %v", problems)
	}
	// هم‌دانه‌بودن: اجرای دوم روی خروجی ترمیم‌شده بدون مشکل است
	if _, p2 := validateHistory(out); len(p2) != 0 {
		t.Fatalf("repair must be idempotent: %v", p2)
	}

	// ۳) نتیجه‌ی تکراری → حذف نسخه‌ی دوم
	h = []chatMessage{
		{Role: "system", Content: "s"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []apiToolCall{apiCall("c1", "read_file", `{}`)}},
		{Role: "tool", ToolCallID: "c1", Content: "first"},
		{Role: "tool", ToolCallID: "c1", Content: "dup"},
	}
	out, problems = validateHistory(h)
	tools := 0
	for _, m := range out {
		if m.Role == "tool" {
			tools++
		}
	}
	if tools != 1 || len(problems) == 0 {
		t.Fatalf("duplicate tool result must be dropped once: tools=%d problems=%v", tools, problems)
	}

	// ۴) محتوای خالی user و tool → جانگهدار
	h = []chatMessage{
		{Role: "system", Content: "s"},
		{Role: "user", Content: ""},
		{Role: "assistant", ToolCalls: []apiToolCall{apiCall("c1", "read_file", `{}`)}},
		{Role: "tool", ToolCallID: "c1", Content: ""},
	}
	out, problems = validateHistory(h)
	if historyContentString(out[1].Content) != "(empty)" || historyContentString(out[3].Content) != "(empty)" {
		t.Fatalf("empty contents must be replaced: %+v", out)
	}
	if len(problems) < 2 {
		t.Fatalf("both empty contents must be reported: %v", problems)
	}

	// ۵) role نامعتبر → حذف
	h = []chatMessage{
		{Role: "system", Content: "s"},
		{Role: "function", Content: "weird"},
		{Role: "user", Content: "hi"},
	}
	out, problems = validateHistory(h)
	if len(out) != 2 || len(problems) == 0 {
		t.Fatalf("unknown role must be dropped: %d msgs problems=%v", len(out), problems)
	}

	// ۶) system دومی → ادغام در اولی
	h = []chatMessage{
		{Role: "system", Content: "one"},
		{Role: "user", Content: "hi"},
		{Role: "system", Content: "two"},
	}
	out, problems = validateHistory(h)
	if len(out) != 2 || !strings.Contains(historyContentString(out[0].Content), "two") || out[0].Role != "system" {
		t.Fatalf("second system must merge into the first: %+v", out)
	}

	// ۷) system در میانه (بدون system اول) → انتقال به ابتدا
	h = []chatMessage{
		{Role: "user", Content: "hi"},
		{Role: "system", Content: "late"},
		{Role: "assistant", Content: "ok"},
	}
	out, problems = validateHistory(h)
	if out[0].Role != "system" || !strings.Contains(historyContentString(out[0].Content), "late") {
		t.Fatalf("mid-history system must move to the front: %+v", out)
	}
	if len(problems) == 0 {
		t.Fatal("mid-history system must be reported")
	}

	// ۸) تاریخچه‌ی خالی → مشکل گزارش می‌شود
	if _, problems = validateHistory(nil); len(problems) != 1 {
		t.Fatalf("empty history must be reported: %v", problems)
	}
}

// ---------------------------------------------------------------- ۴. فشرده‌سازی

// bigToolHistory تاریخچه‌ی نمونه با n تبادل user/assistant+tool؛ محتوای
// نتیجه‌ی ابزارها را فراخواننده تنظیم می‌کند (تابع content(i)).
func bigToolHistory(n int, content func(i int) string) []chatMessage {
	h := []chatMessage{{Role: "system", Content: "sys"}}
	for i := 0; i < n; i++ {
		h = append(h,
			chatMessage{Role: "user", Content: "q" + fmt.Sprint(i)},
			chatMessage{Role: "assistant", ToolCalls: []apiToolCall{apiCall(fmt.Sprintf("c%d", i), "read_file", "{}")}},
			chatMessage{Role: "tool", ToolCallID: fmt.Sprintf("c%d", i), Content: content(i)},
		)
	}
	h = append(h, chatMessage{Role: "user", Content: "final question"})
	return h
}

func TestCompactHistoryForBudget(t *testing.T) {
	// ۸ تبادل؛ همه‌ی نتایج بزرگ — فاز A پنج مورد اول را فشرده می‌کند (حفظ
	// ۳ مورد آخر) و چون باز هم بالای هدف مانیم، فاز B واحدهای قدیمی را حذف می‌کند.
	n := 8
	seed := bigToolHistory(n, func(i int) string {
		return strings.Repeat("line of stuff\n", 200) // ~۲۶۰۰ کاراکتر
	})
	// پیام pinشده بعد از system
	pinned := chatMessage{Role: "user", Content: pinnedHistoryMarker + "\n\nمهم"}
	seed = append([]chatMessage{seed[0], pinned}, seed[1:]...)

	budget := int64(5000) // هدف عادی: ۳۰۰۰؛ آستانه: ۴۰۰۰ — فاز B با کف پیام محدود می‌شود و زیر آستانه می‌ماند
	out, changed := compactHistoryForBudget(seed, budget, nil, false)
	if !changed {
		t.Fatal("compaction must change the history")
	}
	// جفت‌بندی پس از فشرده‌سازی سالم است
	if _, problems := validateHistory(out); len(problems) != 0 {
		t.Fatalf("compacted history must stay protocol-valid: %v", problems)
	}
	// ساختارهای محافظت‌شده
	if out[0].Role != "system" {
		t.Fatal("system must survive at the front")
	}
	if !isPinnedHistoryMsg(out[1]) {
		t.Fatalf("pinned message must survive: %+v", out[1])
	}
	if last := out[len(out)-1]; last.Role != "user" || historyContentString(last.Content) != "final question" {
		t.Fatal("the last user message must survive")
	}
	// فاز A + پیامد: پنج نتیجه‌ی قدیمی در فاز A فشرده شدند و بعد
	// فاز B واحدهای قدیمی را حذف کرد؛ مرز حذف روی assistant یتیم افتاد و
	// «قدمِ اصلاح تناوب» همان واحد را هم کنار گذاشت — پس هر ۵ نتیجه‌ی فشرده
	// حذف شده‌اند و فقط ۳ نتیجه‌ی آخر کامل مانده‌اند. پوششِ «نتیجه‌ی فشرده در
	// خروجی» جداگانه در TestStreamChatAutoCompaction هست (آنجا فاز B فعال نمی‌شود).
	pruned, full := 0, 0
	for _, m := range out {
		if m.Role != "tool" {
			continue
		}
		if strings.Contains(historyContentString(m.Content), "pruned to save context") {
			pruned++
		} else {
			full++
		}
	}
	if pruned != 0 || full != hardKeepToolResults {
		t.Fatalf("only the last %d tool results must survive fully, got %d full / %d pruned", hardKeepToolResults, full, pruned)
	}
	// نشانگر حذف کانتکست: دقیقاً یکی، درست بعد از system و pin
	markers := 0
	for i, m := range out {
		if isContextDropMarker(m) {
			markers++
			if i != 2 {
				t.Fatalf("marker must sit right after system+pinned (index 2), got %d", i)
			}
		}
	}
	if markers != 1 {
		t.Fatalf("exactly one context-drop marker expected, got %d", markers)
	}
	// فاز B: پیام‌ها حذف شده‌اند ولی کف رعایت شده
	if len(out) >= len(seed) {
		t.Fatalf("phase B must drop messages: %d -> %d", len(seed), len(out))
	}
	if len(out) < minKeepMessages {
		t.Fatalf("must keep at least %d messages, got %d", minKeepMessages, len(out))
	}
	// زیر آستانه‌ی فعال‌سازی (۸۰٪) — تا دور بعد دوباره فشرده نشود
	if total := sumHistoryTokens(out); total >= budget*compactTriggerPercent/100 {
		t.Fatalf("compacted history must be under the trigger: %d >= %d", total, budget*compactTriggerPercent/100)
	}

	// بودجه‌ی بزرگ → بدون تغییر
	out2, changed2 := compactHistoryForBudget(out, 1<<30, nil, false)
	if changed2 || len(out2) != len(out) {
		t.Fatal("big budget must not change anything")
	}
	// بودجه‌ی نامعلوم + غیرتهاجمی → بدون تغییر
	if _, changed3 := compactHistoryForBudget(out, 0, nil, false); changed3 {
		t.Fatal("unknown budget in normal mode must be a no-op")
	}
}

func TestCompactHistoryKeepsCacheConsistent(t *testing.T) {
	seed := bigToolHistory(8, func(i int) string { return strings.Repeat("x", 2600) })
	counter := &historyTokenCache{}
	before := counter.totalOf(seed)
	out, changed := compactHistoryForBudget(seed, 2000, counter, false)
	if !changed {
		t.Fatal("expected compaction")
	}
	after := counter.totalOf(out)
	if after >= before {
		t.Fatalf("compaction must reduce the cached estimate: %d -> %d", before, after)
	}
	if after != sumHistoryTokens(out) {
		t.Fatalf("cache must be re-validated after compaction: %d vs %d", after, sumHistoryTokens(out))
	}
}

// ---------------------------------------------------------------- ۵. بازیابی بدون بودجه

func TestRecoverHistoryFromOverflowUnknownBudget(t *testing.T) {
	seed := bigToolHistory(8, func(i int) string { return strings.Repeat("x", 2600) })
	seed = append([]chatMessage{seed[0], {Role: "user", Content: pinnedHistoryMarker + "\n\nمهم"}}, seed[1:]...)

	out, shrank := recoverHistoryFromOverflow(seed, 0, nil)
	if !shrank {
		t.Fatal("aggressive recovery with unknown budget must shrink")
	}
	if _, problems := validateHistory(out); len(problems) != 0 {
		t.Fatalf("recovered history must stay valid: %v", problems)
	}
	if out[0].Role != "system" || !isPinnedHistoryMsg(out[1]) {
		t.Fatal("system and pinned must survive")
	}
	if last := out[len(out)-1]; last.Role != "user" {
		t.Fatal("last user must survive")
	}
	// تقریباً نصف پیام‌ها رفته است
	if len(out) >= len(seed) {
		t.Fatalf("recovery must drop messages: %d -> %d", len(seed), len(out))
	}
	if got := countHistoryToolMsgs(out); got < hardKeepToolResults {
		t.Fatalf("recent tool results must be kept, got %d", got)
	}
}

func countHistoryToolMsgs(h []chatMessage) int {
	n := 0
	for _, m := range h {
		if m.Role == "tool" {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- ۶. تشخیص سرریز

func TestIsContextOverflowError(t *testing.T) {
	positive := []struct {
		status int
		body   string
	}{
		{400, `{"error":{"message":"This model's maximum context length is 8192 tokens. However, your messages resulted in 9500 tokens"}}`},
		{413, `{"error":{"message":"request entity too large: prompt is too long"}}`},
		{422, `{"error":{"message":"input tokens exceed the maximum context window"}}`},
		{400, `{"error":{"message":"Please reduce the length of the messages or use a smaller model"}}`},
		{400, `context window exceeded — total tokens too large`},
		{400, `the request is longer than the model's context`},
	}
	for _, c := range positive {
		if !isContextOverflowError(c.status, c.body) {
			t.Fatalf("must detect overflow: %d %s", c.status, c.body)
		}
	}
	negative := []struct {
		status int
		body   string
	}{
		{400, `{"error":{"message":"max_tokens is not supported"}}`},           // خطای پارامتر max_tokens
		{400, `{"error":{"message":"stream_options is not supported"}}`},       // خطای stream_options
		{404, `{"error":{"message":"tools are not supported by this model"}}`}, // خطای ابزارها
		{400, `{"error":{"message":"invalid model id"}}`},                      // خطای نامرتبط
		{429, `{"error":{"message":"rate limited, context length is fine"}}`},  // وضعیت غیرمرتبط
		{500, `{"error":{"message":"internal error: context length"}}`},        // 500 قابل تلاش مجدد است، نه سرریز
		{200, `ok`},
	}
	for _, c := range negative {
		if isContextOverflowError(c.status, c.body) {
			t.Fatalf("must NOT detect overflow: %d %s", c.status, c.body)
		}
	}
}

// ---------------------------------------------------------------- ۷. استریم واقعی

// seededConversation گفتگوی از‌قبل‌ذخیره‌شده روی دیسک می‌سازد و id آن را
// برمی‌گرداند (الگوی history_test.go — ذخیره زیر a.mu).
func seededConversation(t *testing.T, a *App, msgs []Message) string {
	t.Helper()
	conv := &Conversation{ID: newID(), Title: "t", CreatedAt: time.Now().Unix(), Messages: msgs}
	a.mu.Lock()
	if err := a.saveConversation(conv); err != nil {
		a.mu.Unlock()
		t.Fatal(err)
	}
	a.mu.Unlock()
	return conv.ID
}

// parseChatRequest بدنه‌ی درخواست را به chatRequest تبدیل می‌کند.
func parseChatRequest(t *testing.T, body []byte) chatRequest {
	t.Helper()
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("request body is not a chatRequest: %v", err)
	}
	return req
}

// TestStreamChatAutoCompaction فشرده‌سازی خودکار پیش از «اولین» درخواست:
// گفتگوی ذخیره‌شده با نتایج ابزار بزرگ + پنجره‌ی کوچک → درخواست اول باید
// از قبل فشرده/هرس شده باشد و فایل گفتگو کامل بماند.
func TestStreamChatAutoCompaction(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body.Close()
		mu.Lock()
		bodies = append(bodies, raw)
		mu.Unlock()
		sseOK(w, "پاسخ نهایی")
	}))
	defer srv.Close()

	a := chatAppOnServer(t, srv.URL)
	// ترتیب مهم است: اول App، بعد ریدایرکت لاگ (KODYAR_CONFIG_DIR همین تست)
	redirectToTestLog(t)
	// پنجره‌ی کوچک: بودجه = ۴۰۹۶−۱۰۲۴−۴۰۹ = ۲۶۶۳؛ آستانه = ۲۱۳۰؛ هدف = ۱۵۹۷
	a.mu.Lock()
	a.settings.Providers[0].ContextWindow = 4096
	a.settings.SystemPrompt = strings.Repeat("a", 600) // ~۲۲۵ توکن — قطعی
	a.settings.ToolResultKeep = 20                     // هرسِ شمارشی prepareStream غیرفعال — فاز A تنها فشرده‌ساز است
	a.mu.Unlock()

	// ۸ تبادل؛ ۵ نتیجه‌ی اول بزرگ (۱۲۰۰ کاراکتر ≈ ۴۵۰ توکن)، ۳ مورد آخر کوچک.
	// تخمین اولیه ≈ ۲۲۹ + ۵×۴۷۰ + ۳×۲۱ + ۷ ≈ ۲۶۴۹ ≥ آستانه ۲۱۳۰ → فشرده‌سازی.
	var msgs []Message
	for i := 0; i < 8; i++ {
		content := "small"
		if i < 5 {
			content = strings.Repeat("y", 1200)
		}
		msgs = append(msgs,
			Message{ID: newID(), Role: "user", Content: "q" + fmt.Sprint(i)},
			Message{ID: newID(), Role: "assistant", ToolCalls: []toolCallRecord{{ID: fmt.Sprintf("c%d", i), Name: "read_file", Args: "{}"}}},
			Message{ID: newID(), Role: "tool", ToolCallID: fmt.Sprintf("c%d", i), Content: content},
		)
	}
	convID := seededConversation(t, a, msgs)

	conv, err := a.SendMessage(convID, "ادامه بده", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)
	waitConversationUntil(t, a, conv.ID, hasAssistantWith("پاسخ نهایی"))

	mu.Lock()
	reqCount := len(bodies)
	mu.Unlock()
	if reqCount != 1 {
		t.Fatalf("expected exactly 1 request, got %d", reqCount)
	}

	mu.Lock()
	req := parseChatRequest(t, bodies[0])
	mu.Unlock()
	// درخواست از قبل فشرده شده است: ۵ نتیجه‌ی قدیمی هرس و ۳ مورد آخر کامل
	pruned, full := 0, 0
	for _, m := range req.Messages {
		if m.Role != "tool" {
			continue
		}
		if strings.Contains(historyContentString(m.Content), "pruned to save context") {
			pruned++
		} else {
			full++
		}
	}
	if pruned != 5 || full != hardKeepToolResults {
		t.Fatalf("request must carry 5 pruned + 3 full tool results, got %d/%d", pruned, full)
	}
	// مجموع تخمیری درخواست زیر آستانه‌ی فعال‌سازی (۲۱۳۰) است
	if total := sumHistoryTokens(req.Messages); total >= 2130 {
		t.Fatalf("request tokens must be under the compaction trigger (2130): %d", total)
	}
	// ساختار: system اول، آخرین پیام = پیام کاربرِ تازه
	if req.Messages[0].Role != "system" {
		t.Fatal("system must be the first message")
	}
	if last := req.Messages[len(req.Messages)-1]; last.Role != "user" || !strings.Contains(historyContentString(last.Content), "ادامه بده") {
		t.Fatalf("the new user message must be last: %+v", last)
	}

	// فایل گفتگو دست‌نخورده: هر ۵ نتیجه‌ی بزرگ کامل‌اند (بدون نشانگر هرس)
	a.mu.Lock()
	disk, derr := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if derr != nil {
		t.Fatal(derr)
	}
	bigOnDisk := 0
	for _, m := range disk.Messages {
		if m.Role == "tool" && len(m.Content) == 1200 {
			bigOnDisk++
		}
	}
	if bigOnDisk != 5 {
		t.Fatalf("disk conversation must keep all 5 big tool results intact, got %d", bigOnDisk)
	}
	checkToolPairing(t, disk)
}

// TestStreamChatOverflowRecovery بازیابی از ۴۰۰ِ سرریز: اولین درخواست خطای
// «طول کانتکست» می‌گیرد → فشرده‌سازی تهاجمی → تلاش دوم موفق. فایل گفتگو
// کامل می‌ماند و تعداد پیام‌های درخواست دوم کمتر است.
func TestStreamChatOverflowRecovery(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body.Close()
		mu.Lock()
		bodies = append(bodies, raw)
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"This model's maximum context length is 4096 tokens. However, your messages resulted in 6123 tokens. Please reduce the length of the messages."}}`)
			return
		}
		sseOK(w, "پاسخ پس از فشرده‌سازی")
	}))
	defer srv.Close()

	a := chatAppOnServer(t, srv.URL)
	// ترتیب مهم است: اول App، بعد ریدایرکت لاگ (KODYAR_CONFIG_DIR همین تست)
	redirectToTestLog(t)
	var msgs []Message
	for i := 0; i < 6; i++ {
		msgs = append(msgs,
			Message{ID: newID(), Role: "user", Content: "سوال " + fmt.Sprint(i) + " " + strings.Repeat("متن", 50)},
			Message{ID: newID(), Role: "assistant", Content: "پاسخ " + fmt.Sprint(i) + " " + strings.Repeat("پاسخ", 50)},
		)
	}
	convID := seededConversation(t, a, msgs)

	conv, err := a.SendMessage(convID, "بگو چطوره", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)
	waitConversationUntil(t, a, conv.ID, hasAssistantWith("پاسخ پس از فشرده‌سازی"))

	mu.Lock()
	count := len(bodies)
	first := parseChatRequest(t, bodies[0])
	second := parseChatRequest(t, bodies[1])
	mu.Unlock()
	if count != 2 {
		t.Fatalf("overflow must retry exactly once (2 requests), got %d", count)
	}
	if len(second.Messages) >= len(first.Messages) {
		t.Fatalf("retry must send fewer messages: %d -> %d", len(first.Messages), len(second.Messages))
	}
	if _, problems := validateHistory(second.Messages); len(problems) != 0 {
		t.Fatalf("retried request must be protocol-valid: %v", problems)
	}
	if second.Messages[0].Role != "system" {
		t.Fatal("system must be kept in the retry")
	}
	if last := second.Messages[len(second.Messages)-1]; last.Role != "user" {
		t.Fatal("the new user message must be kept in the retry")
	}
	// فایل گفتگو کامل مانده است (۱۲ پیام قدیمی + کاربر + پاسخ نهایی)
	a.mu.Lock()
	disk, derr := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if derr != nil {
		t.Fatal(derr)
	}
	if len(disk.Messages) != 14 {
		t.Fatalf("disk conversation must keep all messages (14), got %d", len(disk.Messages))
	}
}

// TestStreamChatOverflowRetryFails سرریزِ پایدار: پس از یک تلاش مجددِ ناموفق،
// خطا به کاربر اعلام می‌شود (chat:error → errors.log) و حلقه ادامه پیدا نمی‌کند.
func TestStreamChatOverflowRetryFails(t *testing.T) {
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqs, 1)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"maximum context length is 1024 tokens; your prompt is too long"}}`)
	}))
	defer srv.Close()

	a := chatAppOnServer(t, srv.URL)
	// ترتیب مهم است: اول App (تا KODYAR_CONFIG_DIR این تست تنظیم شود)، بعد ریدایرکت لاگ
	logPath := redirectToTestLog(t)
	var msgs []Message
	for i := 0; i < 6; i++ {
		msgs = append(msgs,
			Message{ID: newID(), Role: "user", Content: "سوال " + fmt.Sprint(i)},
			Message{ID: newID(), Role: "assistant", Content: "پاسخ " + fmt.Sprint(i)},
		)
	}
	convID := seededConversation(t, a, msgs)

	if _, err := a.SendMessage(convID, "بگو", nil); err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)

	waitErrorLogContains(t, logPath, "HTTP 400")
	if got := atomic.LoadInt32(&reqs); got != 2 {
		t.Fatalf("exactly one retry is allowed, got %d requests", got)
	}
}

// ---------------------------------------------------------------- ۸. نشانگر حذف کانتکست

// countContextDropMarkers تعداد و آخرین جای نشانگر حذف کانتکست.
func countContextDropMarkers(h []chatMessage) (n, last int) {
	last = -1
	for i, m := range h {
		if isContextDropMarker(m) {
			n++
			last = i
		}
	}
	return
}

// firstNonHeaderIndex جای اولین پیام بعد از بلوک ابتدایی (system + pinها + نشانگر).
func firstNonHeaderIndex(h []chatMessage) int {
	i := 0
	if len(h) > 0 && h[0].Role == "system" {
		i = 1
	}
	for i < len(h) && (isPinnedHistoryMsg(h[i]) || isContextDropMarker(h[i])) {
		i++
	}
	return i
}

func TestContextDropMarkerInsertion(t *testing.T) {
	seed := bigToolHistory(8, func(i int) string { return strings.Repeat("x", 2600) })
	pinned := chatMessage{Role: "user", Content: pinnedHistoryMarker + "\n\nمهم"}
	seed = append([]chatMessage{seed[0], pinned}, seed[1:]...)

	out, changed := compactHistoryForBudget(seed, 2000, nil, false)
	if !changed {
		t.Fatal("expected compaction")
	}
	n, pos := countContextDropMarkers(out)
	if n != 1 {
		t.Fatalf("exactly one marker expected, got %d", n)
	}
	if pos != 2 {
		t.Fatalf("marker must sit right after system+pinned (index 2), got %d", pos)
	}
	if !strings.Contains(historyContentString(out[pos].Content), "removed to fit the context window") {
		t.Fatalf("marker text must be the fixed English notice: %s", historyContentString(out[pos].Content))
	}
	if out[pos].Role != "user" {
		t.Fatalf("marker role must be user, got %s", out[pos].Role)
	}
	if _, problems := validateHistory(out); len(problems) != 0 {
		t.Fatalf("marker must pass validateHistory: %v", problems)
	}

	// فشرده‌سازی دوباره با بودجه‌ی کوچک‌تر: نشانگر نه تکرار می‌شود نه حذف
	out2, _ := compactHistoryForBudget(out, 900, nil, false)
	n2, pos2 := countContextDropMarkers(out2)
	if n2 != 1 {
		t.Fatalf("marker must stay unique after re-compaction, got %d", n2)
	}
	if pos2 != 2 {
		t.Fatalf("marker must stay after system+pinned, got %d", pos2)
	}
	// بودجه‌ی بزرگ → هیچ تغییری (و نشانگر هم دست نمی‌خورد)
	out3, changed3 := compactHistoryForBudget(out2, 1<<30, nil, false)
	if changed3 {
		t.Fatal("big budget must not change anything")
	}
	if n3, _ := countContextDropMarkers(out3); n3 != 1 {
		t.Fatalf("marker must survive no-op compaction, got %d", n3)
	}
}

// TestContextDropMarkerNeverOrphanStart جاروی بودجه‌ها: در هر فشرده‌سازی،
// تاریخچه‌ی بازمانده هرگز بعد از بلوک ابتدایی با assistant/tool شروع نمی‌شود
// و نشانگر همیشه یکتاست (قدمِ اصلاح تناوب.
func TestContextDropMarkerNeverOrphanStart(t *testing.T) {
	h := []chatMessage{{Role: "system", Content: "s"}}
	for i := 0; i < 6; i++ {
		h = append(h,
			chatMessage{Role: "user", Content: fmt.Sprintf("q%d", i)},
			chatMessage{Role: "assistant", ToolCalls: []apiToolCall{apiCall(fmt.Sprintf("c%d", i), "read_file", `{"path":"x"}`)}},
			chatMessage{Role: "tool", Content: strings.Repeat("r", 900), ToolCallID: fmt.Sprintf("c%d", i)},
		)
	}
	h = append(h, chatMessage{Role: "user", Content: "final"})
	orig := cloneHistoryForTest(h)

	for b := int64(200); b < 12000; b += 200 {
		for _, agg := range []bool{false, true} {
			hh := cloneHistoryForTest(orig)
			out, _ := compactHistoryForBudget(hh, b, nil, agg)
			n, _ := countContextDropMarkers(out)
			if n > 1 {
				t.Fatalf("budget %d agg=%v: marker must be unique, got %d", b, agg, n)
			}
			i := firstNonHeaderIndex(out)
			if i < len(out) && (out[i].Role == "assistant" || out[i].Role == "tool") {
				t.Fatalf("budget %d agg=%v: history must not start with orphan %s at %d", b, agg, out[i].Role, i)
			}
			if _, problems := validateHistory(out); len(problems) != 0 {
				t.Fatalf("budget %d agg=%v: must stay protocol-valid: %v", b, agg, problems)
			}
			// آخرین پیام کاربر همیشه می‌ماند
			if last := out[len(out)-1]; last.Role != "user" || historyContentString(last.Content) != "final" {
				t.Fatalf("budget %d agg=%v: last user message must survive: %+v", b, agg, last)
			}
		}
	}
}

// TestContextDropMarkerPersianConversation سناریوی فارسی: ZWNJ در پیام‌های
// بازمانده بایت‌به‌بایت می‌ماند و نشانگر انگلیسی است (رو به مدل).
func TestContextDropMarkerPersianConversation(t *testing.T) {
	h := []chatMessage{{Role: "system", Content: "سیستم"}}
	for i := 0; i < 8; i++ {
		h = append(h,
			chatMessage{Role: "user", Content: "سؤال فارسی " + fmt.Sprint(i) + " می\u200cشود"},
			chatMessage{Role: "assistant", Content: "پاسخ " + fmt.Sprint(i)},
		)
	}
	h = append(h, chatMessage{Role: "user", Content: "پرسش نهایی"})
	orig := cloneHistoryForTest(h)

	out, changed := compactHistoryForBudget(orig, 120, nil, false)
	if !changed {
		t.Fatal("expected compaction with tiny budget")
	}
	n, pos := countContextDropMarkers(out)
	if n != 1 || pos != 1 {
		t.Fatalf("exactly one marker right after system expected, got n=%d pos=%d", n, pos)
	}
	if !strings.HasPrefix(historyContentString(out[pos].Content), "[context notice]") {
		t.Fatal("marker must be English model-facing text")
	}
	// آخرین پیام کاربر فارسی می‌ماند و ZWNJ هیچ‌جا نرمال نمی‌شود
	last := out[len(out)-1]
	if last.Role != "user" || historyContentString(last.Content) != "پرسش نهایی" {
		t.Fatalf("last Persian user message must survive: %+v", last)
	}
	if _, problems := validateHistory(out); len(problems) != 0 {
		t.Fatalf("must stay protocol-valid: %v", problems)
	}
	for _, m := range out {
		c := historyContentString(m.Content)
		if strings.Contains(c, "میشود") && !strings.Contains(c, "می\u200cشود") {
			t.Fatalf("ZWNJ must never be normalized away: %q", c)
		}
	}
}

// ---------------------------------------------------------------- ۹. فشرده‌سازی آرگومان‌های ابزار

func TestCompactToolCallArgsUnit(t *testing.T) {
	big := strings.Repeat("متن\u200cفارسی ", 100) // > 400 نویسه
	small := "می\u200cکند"
	args := `{"path":"a.txt","content":` + strconv.Quote(big) + `,"old_string":` + strconv.Quote(small) + `}`
	out := compactToolCallArgs(args)

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("compacted args must stay valid JSON: %v\n%s", err, out)
	}
	if m["path"] != "a.txt" {
		t.Fatalf("path must survive: %v", m["path"])
	}
	c, ok := m["content"].(string)
	if !ok || !strings.HasPrefix(c, "[") || !strings.Contains(c, "characters omitted from history]") {
		t.Fatalf("big content must be replaced by the placeholder, got %q", c)
	}
	// رشته‌ی فارسی کوتاه با ZWNJ بایت‌به‌بایت می‌ماند
	if m["old_string"] != small {
		t.Fatalf("small Persian string with ZWNJ must round-trip byte-exact: %q vs %q", m["old_string"], small)
	}
	if !strings.Contains(out, "می\u200cکند") {
		t.Fatal("raw ZWNJ bytes must appear in the marshaled JSON")
	}
	// idempotent
	if out2 := compactToolCallArgs(out); out2 != out {
		t.Fatalf("second pass must be a no-op:\n%s\n%s", out, out2)
	}
	// JSON خراب و خالی → بدون تغییر
	for _, bad := range []string{`{"path":"a`, ``, `not json`, `[]`, `null`} {
		if got := compactToolCallArgs(bad); got != bad {
			t.Fatalf("invalid/empty args must be left untouched: %q -> %q", bad, got)
		}
	}
	// زیر آستانه → بدون تغییر
	smallArgs := `{"content":"` + strings.Repeat("x", 100) + `"}`
	if got := compactToolCallArgs(smallArgs); got != smallArgs {
		t.Fatal("content under the threshold must stay")
	}
}

// TestCompactHistoryShrinksToolCallArgs سناریوی «یک نوبتِ بلند ایجنت»:
// همه‌ی فراخوانی‌ها بعد از آخرین پیام کاربرند — قبلاً هیچ چیزی قابل حذف
// نبود؛ حالا فاز A آرگومان‌های قدیمی را فشرده می‌کند و حالت تهاجمی
// واحدهای قدیمیِ نوبت را هم کنار می‌گذارد (به‌جز ۳ نتیجه‌ی آخر).
func TestCompactHistoryShrinksToolCallArgs(t *testing.T) {
	build := func() []chatMessage {
		h := []chatMessage{{Role: "system", Content: "s"}, {Role: "user", Content: "برنامه را بنویس"}}
		big := strings.Repeat("x", 3000)
		for i := 0; i < 8; i++ {
			args := fmt.Sprintf(`{"path":"f%d.txt","content":%q}`, i, big)
			h = append(h,
				chatMessage{Role: "assistant", ToolCalls: []apiToolCall{apiCall(fmt.Sprintf("c%d", i), "write_file", args)}},
				chatMessage{Role: "tool", Content: "ok", ToolCallID: fmt.Sprintf("c%d", i)},
			)
		}
		return h
	}

	// حالت عادی: فقط فاز A — آرگومان‌های ۵ فراخوانی قدیمی فشرده می‌شوند
	out, changed := compactHistoryForBudget(build(), 4000, nil, false)
	if !changed {
		t.Fatal("phase A must compact old tool_call arguments")
	}
	if len(out) != 18 {
		t.Fatalf("normal mode must not drop messages here (all units are after the last user), got %d", len(out))
	}
	compacted, full := 0, 0
	for _, m := range out {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			var v map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &v); err != nil {
				t.Fatalf("arguments must stay valid JSON: %v (%s)", err, tc.Function.Arguments)
			}
			if strings.Contains(tc.Function.Arguments, "omitted from history") {
				compacted++
				if v["path"] == "" {
					t.Fatal("path must survive compaction")
				}
			} else {
				full++
			}
		}
	}
	if compacted != 5 || full != 3 {
		t.Fatalf("want 5 compacted + 3 full argument sets, got %d/%d", compacted, full)
	}
	if _, problems := validateHistory(out); len(problems) != 0 {
		t.Fatalf("must stay protocol-valid: %v", problems)
	}

	// حالت تهاجمی: بودجه‌ی کوچک → واحدهای قدیمیِ بعد از آخرین کاربر حذف می‌شوند
	h2 := build()
	agg, changed2 := compactHistoryForBudget(h2, 800, nil, true)
	if !changed2 {
		t.Fatal("aggressive mode must shrink a long single turn")
	}
	if len(agg) >= len(h2) {
		t.Fatalf("aggressive must drop after-last-user units: %d -> %d", len(h2), len(agg))
	}
	// کاربر می‌ماند، حداکثر ۳ فراخوانی آخر می‌مانند، نشانگر هست
	calls := 0
	for _, m := range agg {
		if m.Role == "assistant" {
			calls += len(m.ToolCalls)
		}
	}
	if calls > 3 {
		t.Fatalf("aggressive must keep at most the last %d tool calls, got %d", hardKeepToolResults, calls)
	}
	hasUser := false
	for _, m := range agg {
		if historyContentString(m.Content) == "برنامه را بنویس" {
			hasUser = true
		}
	}
	if !hasUser {
		t.Fatal("the last user message must survive aggressive compaction")
	}
	if n, _ := countContextDropMarkers(agg); n != 1 {
		t.Fatalf("aggressive drop must leave exactly one marker, got %d", n)
	}
	if _, problems := validateHistory(agg); len(problems) != 0 {
		t.Fatalf("aggressive result must stay protocol-valid: %v", problems)
	}
	if last := agg[len(agg)-1]; last.Role != "tool" {
		t.Fatalf("mid-turn history must still end with the newest tool result, got %+v", last)
	}
}

// cloneHistoryForTest کپی عمیق تاریخچه (ToolCalls) برای تست‌ها — فشرده‌سازی
// روی slice ورودی تغییر درجا می‌دهد؛ برای مقایسه‌ی «قبل/بعد» کپی لازم است.
func cloneHistoryForTest(h []chatMessage) []chatMessage {
	out := make([]chatMessage, len(h))
	copy(out, h)
	for i := range out {
		if len(out[i].ToolCalls) > 0 {
			tc := make([]apiToolCall, len(out[i].ToolCalls))
			copy(tc, out[i].ToolCalls)
			out[i].ToolCalls = tc
		}
	}
	return out
}

// ---------------------------------------------------------------- ۱۰. تست تصادفی aمانت پروتکل

// randomHistoryForIntegrity تاریخچه‌ی تصادفی با همه‌ی عیب‌های ممکن:
// نتایج گمشده/تکراری/یتیم، system شance‌ای، pin بعد از system، محتوای فارسی
// با ZWNJ و لاتین. محتواهای کاربر یکتایند تا مقایسه‌ی «ماندن آخرین کاربر»
// با محتوا قابل‌اعتماد باشد.
func randomHistoryForIntegrity(rnd *rand.Rand) []chatMessage {
	persian := []string{"سلام", "می\u200cشود", "پروژه", "کد", "تست", "برای", "فایل"}
	latin := []string{"hello", "world", "test", "code", "file", "data"}
	rword := func() string {
		if rnd.Intn(2) == 0 {
			return persian[rnd.Intn(len(persian))] + fmt.Sprint(rnd.Intn(10000))
		}
		return latin[rnd.Intn(len(latin))] + fmt.Sprint(rnd.Intn(10000))
	}
	var h []chatMessage
	if rnd.Intn(5) != 0 {
		h = append(h, chatMessage{Role: "system", Content: "sys " + rword()})
	}
	for p := rnd.Intn(3); p > 0; p-- {
		h = append(h, chatMessage{Role: "user", Content: pinnedHistoryMarker + "\n\npin " + fmt.Sprint(rnd.Intn(10000))})
	}
	n := 5 + rnd.Intn(20)
	var callIDs []string
	for i := 0; i < n; i++ {
		switch r := rnd.Intn(100); {
		case r < 35:
			h = append(h, chatMessage{Role: "user", Content: "u" + fmt.Sprint(i) + " " + rword()})
		case r < 55:
			h = append(h, chatMessage{Role: "assistant", Content: "a" + fmt.Sprint(i) + " " + rword()})
		case r < 80:
			k := rnd.Intn(4)
			var calls []apiToolCall
			for c := 0; c < k; c++ {
				id := fmt.Sprintf("call-%d-%d", i, c)
				callIDs = append(callIDs, id)
				calls = append(calls, apiCall(id, "read_file", `{"path":"x"}`))
			}
			content := "t" + fmt.Sprint(i)
			if rnd.Intn(3) == 0 {
				content = "" // محتوای خالی برای دستیارِ دارای فراخوانی مجاز است
			}
			h = append(h, chatMessage{Role: "assistant", Content: content, ToolCalls: calls})
		default:
			if len(callIDs) == 0 || rnd.Intn(6) == 0 {
				h = append(h, chatMessage{Role: "tool", Content: "orphan " + fmt.Sprint(i), ToolCallID: "ghost-" + fmt.Sprint(i)})
			} else {
				idx := rnd.Intn(len(callIDs))
				h = append(h, chatMessage{Role: "tool", Content: "r" + fmt.Sprint(i), ToolCallID: callIDs[idx]})
			}
		}
	}
	if rnd.Intn(4) != 0 {
		h = append(h, chatMessage{Role: "user", Content: "final " + fmt.Sprint(rnd.Intn(10000))})
	}
	return h
}

// randomBudgetForIntegrity بودجه‌ی تصادفی: نامعلوم، خیلی کوچک یا ۲۰٪–۳۰۰٪ کل.
func randomBudgetForIntegrity(rnd *rand.Rand, h []chatMessage) int64 {
	switch rnd.Intn(4) {
	case 0:
		return 0
	case 1:
		return 1 + rnd.Int63n(200)
	default:
		total := sumHistoryTokens(h)
		if total < 1 {
			total = 1
		}
		return int64(float64(total) * (0.2 + rnd.Float64()*2.8))
	}
}

// assertProtocolIntegrityInvariants همه‌ی نادرسته‌های پروتکل را بررسی می‌کند.
func assertProtocolIntegrityInvariants(t *testing.T, seed int64, h []chatMessage) {
	t.Helper()
	nSys := 0
	for i, m := range h {
		switch m.Role {
		case "system":
			nSys++
			if i != 0 {
				t.Fatalf("seed=%d: system must be first, found at %d", seed, i)
			}
		case "user":
			if strings.TrimSpace(historyContentString(m.Content)) == "" {
				t.Fatalf("seed=%d: empty user content at %d", seed, i)
			}
		case "assistant":
			if len(m.ToolCalls) == 0 && strings.TrimSpace(historyContentString(m.Content)) == "" {
				t.Fatalf("seed=%d: empty assistant content at %d", seed, i)
			}
			for j, tc := range m.ToolCalls {
				if i+1+len(m.ToolCalls) > len(h) {
					t.Fatalf("seed=%d: assistant at %d declares %d calls but history ends early", seed, i, len(m.ToolCalls))
				}
				tm := h[i+1+j]
				if tm.Role != "tool" || tm.ToolCallID != tc.ID {
					t.Fatalf("seed=%d: pairing broken at %d call %d (id %q): role=%q toolCallID=%q", seed, i, j, tc.ID, tm.Role, tm.ToolCallID)
				}
			}
		case "tool":
			if i == 0 || h[i-1].Role != "assistant" || len(h[i-1].ToolCalls) == 0 {
				// فقط نتایجِ داخل بلوکِ بعد از دستیار مجازند
				inBlock := false
				if i > 0 && h[i-1].Role == "tool" {
					inBlock = true // ادامه‌ی بلوک — صحتش در گذر دستیار بررسی شد
				}
				if !inBlock {
					t.Fatalf("seed=%d: orphan tool result at %d", seed, i)
				}
			}
		default:
			t.Fatalf("seed=%d: unknown role %q at %d", seed, m.Role, i)
		}
	}
	if nSys > 1 {
		t.Fatalf("seed=%d: system must be unique, got %d", seed, nSys)
	}
}

// TestRandomizedProtocolIntegrity تست تصادفیِ قطعی: تولید
// تاریخچه‌های تصادفی معیوب → validateHistory → فشرده‌سازی عادی و تهاجمی با
// بودجه‌های تصادفی → validateHistory دوباره؛ همه‌ی نادرسته‌های پروتکل باید
// برقرار بمانند. seed هر تکرار در خطای شکست چاپ می‌شود (بازتولیدپذیر) و
// کل تست باید زیر ۲ ثانیه تمام شود.
func TestRandomizedProtocolIntegrity(t *testing.T) {
	const iters = 250
	const baseSeed = int64(20260331)
	start := time.Now()
	for iter := 0; iter < iters; iter++ {
		seed := baseSeed + int64(iter)
		rnd := rand.New(rand.NewSource(seed))
		h := randomHistoryForIntegrity(rnd)

		v1, _ := validateHistory(h)
		if len(v1) == 0 {
			continue
		}
		assertProtocolIntegrityInvariants(t, seed, v1)

		budget := randomBudgetForIntegrity(rnd, v1)
		c1, _ := compactHistoryForBudget(cloneHistoryForTest(v1), budget, nil, false)
		v2, _ := validateHistory(c1)
		assertProtocolIntegrityInvariants(t, seed, v2)
		if n, pos := countContextDropMarkers(v2); n > 1 || (n == 1 && pos != firstNonHeaderIndex(v2)-1) {
			t.Fatalf("seed=%d budget=%d: marker must be unique and right after the header (n=%d pos=%d)", seed, budget, n, pos)
		}
		if n, _ := countContextDropMarkers(v2); n == 1 {
			if i := firstNonHeaderIndex(v2); i < len(v2) && (v2[i].Role == "assistant" || v2[i].Role == "tool") {
				t.Fatalf("seed=%d budget=%d: history after the marker must not start with %s", seed, budget, v2[i].Role)
			}
		}

		c2, _ := compactHistoryForBudget(cloneHistoryForTest(v2), budget, nil, true)
		v3, _ := validateHistory(c2)
		assertProtocolIntegrityInvariants(t, seed, v3)
		if n, _ := countContextDropMarkers(v3); n > 1 {
			t.Fatalf("seed=%d budget=%d: marker must stay unique after aggressive compaction, got %d", seed, budget, n)
		}

		// آخرین پیام کاربر و پیام‌های pinشده‌ی v1 باید در v3 مانده باشند
		lastUserV1 := ""
		for _, m := range v1 {
			if m.Role == "user" && !isPinnedHistoryMsg(m) && !isContextDropMarker(m) {
				lastUserV1 = historyContentString(m.Content)
			}
		}
		if lastUserV1 != "" {
			lastUserV3 := ""
			for _, m := range v3 {
				if m.Role == "user" && !isPinnedHistoryMsg(m) && !isContextDropMarker(m) {
					lastUserV3 = historyContentString(m.Content)
				}
			}
			if lastUserV3 != lastUserV1 {
				t.Fatalf("seed=%d budget=%d: last user message must survive: %q -> %q", seed, budget, lastUserV1, lastUserV3)
			}
		}
		pinnedV1 := map[string]bool{}
		for _, m := range v1 {
			if isPinnedHistoryMsg(m) {
				pinnedV1[historyContentString(m.Content)] = true
			}
		}
		pinnedV3 := map[string]bool{}
		for _, m := range v3 {
			if isPinnedHistoryMsg(m) {
				pinnedV3[historyContentString(m.Content)] = true
			}
		}
		for c := range pinnedV1 {
			if !pinnedV3[c] {
				t.Fatalf("seed=%d budget=%d: pinned message must survive: %q", seed, budget, c)
			}
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("randomized integrity test must run under 2s, took %v", elapsed)
	}
}

// TestCompactHistoryAggressiveMixedRanges حذف دو‌بخشی تهاجمی: هم تبادل‌های
// قدیمیِ قبل از آخرین کاربر و هم واحدهای قدیمیِ نوبتِ جاری (بعد از آخرین
// کاربر) کنار می‌روند؛ خودِ آخرین کاربر و ۳ نتیجه‌ی آخر می‌مانند.
func TestCompactHistoryAggressiveMixedRanges(t *testing.T) {
	build := func() []chatMessage {
		h := []chatMessage{{Role: "system", Content: "s"}}
		// ۳ تبادل قدیمی قبل از آخرین پیام کاربر
		for i := 0; i < 3; i++ {
			h = append(h,
				chatMessage{Role: "user", Content: fmt.Sprintf("قدیم %d", i)},
				chatMessage{Role: "assistant", ToolCalls: []apiToolCall{apiCall(fmt.Sprintf("old%d", i), "read_file", `{"path":"x"}`)}},
				chatMessage{Role: "tool", Content: "old-ok", ToolCallID: fmt.Sprintf("old%d", i)},
			)
		}
		// آخرین پیام کاربر + یک نوبتِ بلند (۵ فراخوانی بعد از آن)
		h = append(h, chatMessage{Role: "user", Content: "آخرین درخواست"})
		for i := 0; i < 5; i++ {
			h = append(h,
				chatMessage{Role: "assistant", ToolCalls: []apiToolCall{apiCall(fmt.Sprintf("new%d", i), "read_file", `{"path":"y"}`)}},
				chatMessage{Role: "tool", Content: "new-ok", ToolCallID: fmt.Sprintf("new%d", i)},
			)
		}
		return h
	}
	h := build() // ۱ + ۹ + ۱ + ۱۰ = ۲۱ پیام

	agg, changed := compactHistoryForBudget(h, 60, nil, true)
	if !changed {
		t.Fatal("aggressive compaction must shrink")
	}
	// آخرین پیام کاربر سر جایش است
	hasLastUser := false
	for _, m := range agg {
		if historyContentString(m.Content) == "آخرین درخواست" {
			hasLastUser = true
		}
	}
	if !hasLastUser {
		t.Fatal("the last user message must survive")
	}
	// هر سه دستیار قدیمی و دو دستیار قدیمیِ نوبتِ جاری حذف شده‌اند؛ ۳ نتیجه‌ی آخر مانده‌اند
	tools := 0
	for _, m := range agg {
		if m.Role == "tool" {
			tools++
		}
	}
	if tools != hardKeepToolResults {
		t.Fatalf("only the last %d tool results must remain, got %d", hardKeepToolResults, tools)
	}
	for _, m := range agg {
		if m.Role == "tool" && m.ToolCallID != "" && !strings.HasPrefix(m.ToolCallID, "new") {
			t.Fatalf("old-exchange tool results must be gone: %+v", m)
		}
	}
	if n, pos := countContextDropMarkers(agg); n != 1 || pos != 1 {
		t.Fatalf("exactly one marker right after system expected, got n=%d pos=%d", n, pos)
	}
	if _, problems := validateHistory(agg); len(problems) != 0 {
		t.Fatalf("must stay protocol-valid: %v", problems)
	}
	if last := agg[len(agg)-1]; last.Role != "tool" {
		t.Fatalf("must end with the newest tool result, got %+v", last)
	}
}
