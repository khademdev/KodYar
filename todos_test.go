// تست‌های ابزار todo_write — اعتبارسنجی، ماندگاری، Fork، رویداد، رقابت و
// دسترسی. اجرا با `go test -race`.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// todoCallCtx ساخت context با اطلاعات فراخوانی (قرارداد runToolCall) برای
// فراخوانی مستقیم toolTodoWrite در تست‌ها.
func todoCallCtx(convID string) context.Context {
	return withToolCallInfo(context.Background(), convID, "call-test")
}

func mustSaveConv(t *testing.T, a *App, c *Conversation) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.saveConversation(c); err != nil {
		t.Fatal(err)
	}
}

func mustLoadConv(t *testing.T, a *App, id string) *Conversation {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.loadConversation(id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// ---------------------------------------------------------------- اعتبارسنجی

func TestTodoParseValidationTable(t *testing.T) {
	cases := []struct {
		name    string
		raw     interface{}
		wantErr string // زیررشته‌ی خطای انتظاری؛ خالی = باید موفق شود
	}{
		{"not an array", map[string]interface{}{"a": 1}, `field "todos" must be an array`},
		{"string not json array", "hello", `field "todos" must be an array`},
		{"empty list ok", []interface{}{}, ""},
		{"json string array ok", `[{"content":"a","status":"pending"}]`, ""},
		{"too many items", func() interface{} {
			arr := make([]interface{}, maxTodoItems+1)
			for i := range arr {
				arr[i] = map[string]interface{}{"content": fmt.Sprintf("c%d", i), "status": "pending"}
			}
			return arr
		}(), "at most 50 items, got 51"},
		{"item not object", []interface{}{"plain"}, "item 1 must be a JSON object"},
		{"missing content", []interface{}{map[string]interface{}{"status": "pending"}}, `item 1 is missing required field "content"`},
		{"empty content", []interface{}{map[string]interface{}{"content": "   ", "status": "pending"}}, `item 1 field "content" must not be empty`},
		{"content wrong type", []interface{}{map[string]interface{}{"content": []interface{}{}, "status": "pending"}}, `item 1 field "content" must be a string, got array`},
		{"missing status", []interface{}{map[string]interface{}{"content": "x"}}, `item 1 is missing required field "status"`},
		{"bad status", []interface{}{map[string]interface{}{"content": "x", "status": "done"}}, `item 1 field "status" must be one of "pending" | "in_progress" | "completed", got "done"`},
		{"two in_progress", []interface{}{
			map[string]interface{}{"content": "a", "status": "in_progress"},
			map[string]interface{}{"content": "b", "status": "in_progress"},
		}, `at most one item may be "in_progress" (items 1 and 2 both are)`},
		{"duplicate ids", []interface{}{
			map[string]interface{}{"id": "x", "content": "a", "status": "pending"},
			map[string]interface{}{"id": "x", "content": "b", "status": "pending"},
		}, `duplicate id "x" (item 2)`},
	}
	for _, tc := range cases {
		_, _, err := parseTodoItems(tc.raw)
		if tc.wantErr == "" {
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s:\n got: %v\nwant substring: %s", tc.name, err, tc.wantErr)
		}
	}
}

func TestTodoParsePersianZWNJAndRuneTruncation(t *testing.T) {
	// متن فارسی با ZWNJ — برش باید در مرز کاراکتر باشد و ZWNJِ داخل متن حفظ شود
	long := strings.Repeat("می‌رویم", 60) // «می» + ZWNJ + «رویم» — ۷ کاراکتر در هر تکرار
	items, truncated, err := parseTodoItems([]interface{}{
		map[string]interface{}{"content": long, "status": "pending"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("long content must be reported as truncated")
	}
	r := []rune(items[0].Content)
	if len(r) != maxTodoContentRunes {
		t.Fatalf("content must be exactly %d runes, got %d", maxTodoContentRunes, len(r))
	}
	if !utf8.ValidString(items[0].Content) {
		t.Fatal("truncated content must stay valid UTF-8")
	}
	// ZWNJ (U+200C) باید در متن باقی مانده باشد (هیچ نرمال‌سازی‌ای انجام نمی‌شود)
	if !strings.Contains(items[0].Content, "\u200c") {
		t.Fatal("ZWNJ must be preserved byte-for-byte")
	}

	// متن کوتاه فارسی با ZWNJ باید دست‌نخورده بماند
	short := "بررسی فایل‌های پروژه و رفع اشکال"
	items, truncated, err = parseTodoItems([]interface{}{
		map[string]interface{}{"content": short, "status": "in_progress"},
	})
	if err != nil || truncated {
		t.Fatalf("short Persian content: err=%v truncated=%v", err, truncated)
	}
	if items[0].Content != short {
		t.Fatalf("content must be byte-identical, got %q", items[0].Content)
	}
	if !todoItemsValidUTF8(items) {
		t.Fatal("items must be valid UTF-8")
	}
}

func TestTodoParseAutoIDsAndUniqueness(t *testing.T) {
	// شناسهٔ غایب = تخصیص موقعیتی؛ برخورد با شناسهٔ داده‌شده حل می‌شود
	items, _, err := parseTodoItems([]interface{}{
		map[string]interface{}{"id": "1", "content": "keep id", "status": "pending"},
		map[string]interface{}{"content": "auto", "status": "pending"},
		map[string]interface{}{"id": float64(7), "content": "numeric id", "status": "pending"}, // عدد JSON → رشته
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, it := range items {
		if it.ID == "" {
			t.Fatal("no item may keep an empty id")
		}
		if seen[it.ID] {
			t.Fatalf("id %q duplicated", it.ID)
		}
		seen[it.ID] = true
	}
	if items[0].ID != "1" {
		t.Fatalf("provided id must be kept, got %q", items[0].ID)
	}
	if items[2].ID != "7" {
		t.Fatalf("numeric id must coerce to string, got %q", items[2].ID)
	}
	// آیتم دوم باید شناسه‌ی خودکار بدون برخورد با «1» بگیرد
	if items[1].ID == "1" || items[1].ID == "7" {
		t.Fatalf("auto id collided: %q", items[1].ID)
	}
}

// ---------------------------------------------------------------- ماندگاری

func TestTodoWritePersistenceRoundTrip(t *testing.T) {
	a := newTestApp(t)
	conv := &Conversation{ID: newID(), Title: "t", CreatedAt: 1, Messages: []Message{{ID: "m1", Role: "user", Content: "hi"}}}
	mustSaveConv(t, a, conv)

	// ۱) نوشتن از مسیر کامل ابزار (execToolCtx + info در ctx)
	var emitCounts []int
	var emitMu sync.Mutex
	a.setTodosForTest(func(cid string, items []TodoItem) {
		emitMu.Lock()
		emitCounts = append(emitCounts, len(items))
		emitMu.Unlock()
		if cid != conv.ID {
			t.Errorf("event conversationId = %q, want %q", cid, conv.ID)
		}
	})
	res, ok := a.execToolCtx(todoCallCtx(conv.ID), "todo_write", `{"todos":[{"content":"گام یک","status":"completed"},{"content":"گام دو","status":"in_progress"}]}`)
	if !ok || !strings.HasPrefix(res, "OK: todo list updated") {
		t.Fatalf("todo_write failed: %v %v", res, ok)
	}

	// ۲) رفت‌وبرگشت روی دیسک
	got := mustLoadConv(t, a, conv.ID)
	if len(got.Todos) != 2 {
		t.Fatalf("persisted todos = %d, want 2", len(got.Todos))
	}
	if got.Todos[0].Status != "completed" || got.Todos[1].Status != "in_progress" {
		t.Fatalf("statuses not persisted: %+v", got.Todos)
	}
	if got.Todos[0].Content != "گام یک" {
		t.Fatalf("Persian content not byte-exact: %q", got.Todos[0].Content)
	}

	// ۳) پاک‌کردن با آرایه‌ی خالی → فیلد حذف می‌شود (omitempty) و بازگشتی [] است
	res, ok = a.execToolCtx(todoCallCtx(conv.ID), "todo_write", `{"todos":[]}`)
	if !ok || !strings.Contains(res, "0 item(s)") {
		t.Fatalf("clear failed: %v %v", res, ok)
	}
	got = mustLoadConv(t, a, conv.ID)
	if len(got.Todos) != 0 {
		t.Fatalf("todos must be cleared, got %+v", got.Todos)
	}

	// ۴) هر نوشتنِ موفق دقیقاً یک رویداد (اول: ۲ آیتم، دوم: ۰ آیتم)
	emitMu.Lock()
	counts := append([]int(nil), emitCounts...)
	emitMu.Unlock()
	if len(counts) != 2 || counts[0] != 2 || counts[1] != 0 {
		t.Fatalf("event fired with item counts %v, want [2 0]", counts)
	}
}

func TestTodoWriteOldConversationWithoutField(t *testing.T) {
	a := newTestApp(t)
	// فایل گفتگوی قدیمی (قبل از todo_write) — بدون فیلد todos
	dir, err := a.conversationsDir()
	if err != nil {
		t.Fatal(err)
	}
	id := newID()
	old := map[string]interface{}{
		"id": id, "title": "قدیمی", "createdAt": 1, "updatedAt": 1,
		"messages": []map[string]interface{}{
			{"id": "m1", "role": "user", "content": "سلام", "at": 1},
		},
	}
	raw, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(dir, id+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	// بارگذاری گفتگوی قدیمی → بدون خطا و بدون todo
	got := mustLoadConv(t, a, id)
	if got.Todos != nil {
		t.Fatalf("old conversation must load with nil todos, got %+v", got.Todos)
	}

	// نوشتن todo روی همان گفتگو و خواندن دوباره → فیلد ظاهر می‌شود
	if res, ok := a.execToolCtx(todoCallCtx(id), "todo_write", `{"todos":[{"content":"new","status":"pending"}]}`); !ok {
		t.Fatal(res)
	}
	got = mustLoadConv(t, a, id)
	if len(got.Todos) != 1 || got.Todos[0].Content != "new" {
		t.Fatalf("todos must appear after write: %+v", got.Todos)
	}
	// پیام‌های قدیمی هم سر جایشان‌اند
	if len(got.Messages) != 1 || got.Messages[0].Content != "سلام" {
		t.Fatalf("old messages must survive: %+v", got.Messages)
	}
}

func TestTodoForkCopiesItems(t *testing.T) {
	a := newTestApp(t)
	conv := &Conversation{ID: newID(), Title: "t", CreatedAt: 1, Messages: []Message{
		{ID: "m1", Role: "user", Content: "a"},
		{ID: "m2", Role: "assistant", Content: "ok"},
	}}
	conv.Todos = []TodoItem{{ID: "1", Content: "بررسی", Status: "completed"}, {ID: "2", Content: "اعمال", Status: "pending"}}
	mustSaveConv(t, a, conv)

	fork, err := a.ForkConversation(conv.ID, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(fork.Todos) != 2 || fork.Todos[0].Content != "بررسی" || fork.Todos[1].Status != "pending" {
		t.Fatalf("fork must copy todos: %+v", fork.Todos)
	}
	// کپی عمیق: تغییر نسخه‌ی fork نباید به مادر سرایت کند
	fork.Todos[0].Status = "in_progress"
	mustSaveConv(t, a, fork)
	orig := mustLoadConv(t, a, conv.ID)
	if orig.Todos[0].Status != "completed" {
		t.Fatalf("fork leaked into source conversation: %+v", orig.Todos)
	}
}

// ---------------------------------------------------------------- رویداد و رقابت

func TestTodoWriteEventPayloadAndCount(t *testing.T) {
	a := newTestApp(t)
	conv := &Conversation{ID: newID(), Title: "t", CreatedAt: 1, Messages: []Message{{ID: "m1", Role: "user", Content: "x"}}}
	mustSaveConv(t, a, conv)

	var events int
	var lastItems []TodoItem
	var mu sync.Mutex
	a.setTodosForTest(func(cid string, items []TodoItem) {
		mu.Lock()
		defer mu.Unlock()
		events++
		lastItems = items
	})

	for i := 0; i < 3; i++ {
		if res, ok := a.execToolCtx(todoCallCtx(conv.ID), "todo_write",
			fmt.Sprintf(`{"todos":[{"content":"c%d","status":"pending"}]}`, i)); !ok {
			t.Fatal(res)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if events != 3 {
		t.Fatalf("event must fire once per successful write, got %d", events)
	}
	if len(lastItems) != 1 || lastItems[0].Content != "c2" {
		t.Fatalf("last event must carry the last list, got %+v", lastItems)
	}
	// بدون info گفتگو (فراخوانی خارج از نوبت ایجنت) → خطای انگلیسی، بدون رویداد
	if res, ok := a.execToolCtx(context.Background(), "todo_write", `{"todos":[]}`); ok || !strings.Contains(res, "only available inside an agent conversation") {
		t.Fatalf("missing conv info must fail with English error, got: %v %v", res, ok)
	}
	if events != 3 {
		t.Fatalf("failed writes must not emit, got %d events", events)
	}
}

func TestTodoWriteConcurrentRace(t *testing.T) {
	a := newTestApp(t)
	conv := &Conversation{ID: newID(), Title: "t", CreatedAt: 1, Messages: []Message{{ID: "m1", Role: "user", Content: "x"}}}
	mustSaveConv(t, a, conv)

	a.setTodosForTest(func(cid string, items []TodoItem) {})

	// نوشتن هم‌زمان از چند مسیر (شبیه‌سازی فراخوانی هم‌زمان UI/tool):
	// آخرین برنده باید یکی از نوشتن‌ها باشد و فایل همیشه JSON معتبر بماند.
	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := fmt.Sprintf(`{"todos":[{"content":"write-%d","status":"pending"}]}`, i)
			if res, ok := a.execToolCtx(todoCallCtx(conv.ID), "todo_write", args); !ok {
				t.Errorf("concurrent write %d failed: %s", i, res)
			}
		}(i)
	}
	wg.Wait()

	got := mustLoadConv(t, a, conv.ID)
	if len(got.Todos) != 1 || !strings.HasPrefix(got.Todos[0].Content, "write-") {
		t.Fatalf("exactly one winning list expected, got %+v", got.Todos)
	}
	// فایل روی دیسک باید JSON معتبر باشد
	raw, err := os.ReadFile(filepath.Join(mustConvDir(t, a), conv.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var probe Conversation
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("conversation file must stay valid JSON: %v", err)
	}
}

func mustConvDir(t *testing.T, a *App) string {
	t.Helper()
	dir, err := a.conversationsDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// ---------------------------------------------------------------- دسترسی

func TestTodoWritePermissionDefault(t *testing.T) {
	a := newTestApp(t)
	if d := a.toolPermDecisionLocked("todo_write"); d != permAllow {
		t.Fatalf("todo_write default must be allow, got %v", d)
	}
	// override کاربر هنوز کار می‌کند (مثلاً deny)
	a.settings.Permissions.Tools = map[string]string{"todo_write": "deny"}
	if d := a.toolPermDecisionLocked("todo_write"); d != permDeny {
		t.Fatalf("user override must apply, got %v", d)
	}
	// deny واقعی: اجرا بدون مودال رد می‌شود (پیام انگلیسی)
	conv := &Conversation{ID: newID(), Title: "t", CreatedAt: 1, Messages: []Message{{ID: "m1", Role: "user", Content: "x"}}}
	mustSaveConv(t, a, conv)
	if res, ok := a.execToolCtx(todoCallCtx(conv.ID), "todo_write", `{"todos":[]}`); ok || !strings.Contains(res, "disabled by the user's permission settings") {
		t.Fatalf("deny must block, got: %v %v", res, ok)
	}
}

// ---------------------------------------------------------------- تکمیل ثبت

func TestTodoWriteRegisteredEverywhere(t *testing.T) {
	// تعریف ابزار در agentToolDefs
	found := false
	for _, d := range agentToolDefs() {
		if d.Function.Name == "todo_write" {
			found = true
		}
	}
	if !found {
		t.Fatal("todo_write must be in agentToolDefs")
	}
	// در جدول اعتبارسنجی
	if _, ok := toolArgSpecs["todo_write"]; !ok {
		t.Fatal("todo_write must have an argument spec")
	}
	// در پرامپت سیستمی (فهرست ابزارها + قانون چک‌لیست)
	p := agentSystemPrompt("C:\\ws")
	if !strings.Contains(p, "todo_write") || !strings.Contains(p, "Task checklist") {
		t.Fatal("system prompt must mention todo_write and the checklist rule")
	}
	// NOT در فهرست موازی و NOT در allowlist زیرایجنت
	if parallelReadOnlyTools["todo_write"] {
		t.Fatal("todo_write must stay sequential (not in the parallel list)")
	}
	if subagentAllowedTools["todo_write"] {
		t.Fatal("todo_write must not be available to the sub-agent")
	}
}
