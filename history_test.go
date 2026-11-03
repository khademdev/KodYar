// تست‌های هرس تاریخچه، فشرده‌سازی، سقف حجم، Fork و Pin (اولویت ۳).
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------- هرس API

func TestCompactToolResult(t *testing.T) {
	small := strings.Repeat("a", 500)
	if compactToolResult(small) != small {
		t.Fatal("small results must not be compacted")
	}
	big := strings.Repeat("line of text\n", 200) // ~2600 chars
	out := compactToolResult(big)
	if len(out) >= len(big) {
		t.Fatalf("compaction must shrink: %d -> %d", len(big), len(out))
	}
	if !strings.Contains(out, "[older tool output pruned") {
		t.Fatalf("compacted marker missing: %q", out[:100])
	}
}

func TestPruningInPrepareStream(t *testing.T) {
	a := newTestApp(t)
	a.settings.ToolResultKeep = 2

	// گفتگو با ۵ نتیجه‌ی ابزار بزرگ + پیام‌های کاربر
	msgs := []Message{}
	for i := 0; i < 5; i++ {
		msgs = append(msgs,
			Message{ID: newID(), Role: "user", Content: "q" + itoa(i)},
			Message{ID: newID(), Role: "assistant", Content: "", ToolCalls: []toolCallRecord{{ID: "c" + itoa(i), Name: "read_file", Args: "{}"}}},
			Message{ID: newID(), Role: "tool", ToolCallID: "c" + itoa(i), Content: strings.Repeat("tool output line\n", 150)},
		)
	}
	conv := &Conversation{ID: newID(), Title: "t", Messages: msgs}
	history, err := a.buildHistoryForTest(conv)
	if err != nil {
		t.Fatal(err)
	}

	full, pruned := 0, 0
	for _, h := range history {
		if h.Role != "tool" {
			continue
		}
		hc, _ := h.Content.(string)
		if strings.Contains(hc, "[older tool output pruned") {
			pruned++
		} else {
			full++
		}
	}
	if full != 2 || pruned != 3 {
		t.Fatalf("expected 2 full + 3 pruned tool results, got %d full + %d pruned", full, pruned)
	}

	// فایل ذخیره‌شده دست‌نخورده مانده باشد (هرس فقط در API)
	a.mu.Lock()
	_ = a.saveConversation(conv)
	c2, _ := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if c2 == nil {
		t.Fatal("conversation not saved")
	}
	bigCount := 0
	for _, m := range c2.Messages {
		if m.Role == "tool" && strings.Contains(contentStr(m.Content), "tool output line") && !strings.Contains(contentStr(m.Content), "pruned") {
			bigCount++
		}
	}
	if bigCount != 5 {
		t.Fatalf("stored conversation must keep all 5 full tool results, got %d", bigCount)
	}
}

func TestPinnedOutsideWindowReturns(t *testing.T) {
	a := newTestApp(t)
	a.settings.MaxHistory = 6
	a.settings.Provider = "ollama"
	a.settings.Model = "llama3.2"

	msgs := []Message{
		{ID: newID(), Role: "user", Content: "پیام مهم سنجاق‌شده", Pinned: true},
	}
	for i := 0; i < 10; i++ {
		msgs = append(msgs, Message{ID: newID(), Role: "user", Content: " filler " + itoa(i)})
	}
	conv := &Conversation{ID: newID(), Title: "t", Messages: msgs}
	history, err := a.buildHistoryForTest(conv)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range history {
		if h.Role == "user" && strings.Contains(contentStr(h.Content), "[pinned by the user") &&
			strings.Contains(contentStr(h.Content), "پیام مهم سنجاق‌شده") {
			found = true
		}
	}
	if !found {
		t.Fatal("pinned message outside window must be re-injected after system prompt")
	}
}

// ---------------------------------------------------------------- Pin / Fork

func TestSetMessagePinned(t *testing.T) {
	a := newTestApp(t)
	conv := &Conversation{ID: newID(), Title: "t", Messages: []Message{
		{ID: newID(), Role: "user", Content: "سلام"},
		{ID: newID(), Role: "assistant", Content: "درود"},
	}}
	a.mu.Lock()
	if err := a.saveConversation(conv); err != nil {
		t.Fatal(err)
	}
	a.mu.Unlock()

	updated, err := a.SetMessagePinned(conv.ID, conv.Messages[1].ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Messages[1].Pinned {
		t.Fatal("message must be pinned in returned conversation")
	}
	// ماندگاری روی دیسک
	a.mu.Lock()
	c2, _ := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if c2 == nil || !c2.Messages[1].Pinned {
		t.Fatal("pin must persist to disk")
	}
	// برداشتن سنجاق
	if _, err := a.SetMessagePinned(conv.ID, conv.Messages[1].ID, false); err != nil {
		t.Fatal(err)
	}
}

func TestForkConversation(t *testing.T) {
	a := newTestApp(t)
	conv := &Conversation{ID: newID(), Title: "اصلی", Messages: []Message{
		{ID: "m1", Role: "user", Content: "پیام اول"},
		{ID: "m2", Role: "assistant", Content: "پاسخ اول"},
		{ID: "m3", Role: "user", Content: "پیام دوم"},
		{ID: "m4", Role: "assistant", Content: "پاسخ دوم"},
	}}
	a.mu.Lock()
	if err := a.saveConversation(conv); err != nil {
		t.Fatal(err)
	}
	a.mu.Unlock()

	fork, err := a.ForkConversation(conv.ID, "m2")
	if err != nil {
		t.Fatal(err)
	}
	if fork.ID == conv.ID {
		t.Fatal("fork must have a new ID")
	}
	if len(fork.Messages) != 2 {
		t.Fatalf("fork must contain messages up to and including m2, got %d", len(fork.Messages))
	}
	if fork.Messages[1].ID != "m2" {
		t.Fatal("fork must include the target message")
	}
	if !strings.Contains(fork.Title, "شاخه") {
		t.Fatal("fork title must be marked")
	}
	// گفتگوی اصلی دست‌نخورده
	a.mu.Lock()
	orig, _ := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if orig == nil || len(orig.Messages) != 4 {
		t.Fatal("source conversation must be untouched")
	}
	// پیام ناموجود
	if _, err := a.ForkConversation(conv.ID, "nope"); err == nil {
		t.Fatal("fork from missing message must fail")
	}
}

// ---------------------------------------------------------------- فشرده‌سازی و سقف

func TestCompactConversation(t *testing.T) {
	a := newTestApp(t)
	a.settings.ToolResultKeep = 1
	msgs := []Message{
		{ID: newID(), Role: "user", Content: "ببین @a.txt", Attached: "<attached_files>\nfile content here\n</attached_files>"},
		{ID: newID(), Role: "assistant", ToolCalls: []toolCallRecord{{ID: "c1", Name: "read_file"}}},
		{ID: newID(), Role: "tool", ToolCallID: "c1", Content: strings.Repeat("big output\n", 200)},
		{ID: newID(), Role: "user", Content: "ادامه"},
		{ID: newID(), Role: "assistant", ToolCalls: []toolCallRecord{{ID: "c2", Name: "read_file"}}},
		{ID: newID(), Role: "tool", ToolCallID: "c2", Content: strings.Repeat("newest output\n", 200)},
	}
	conv := &Conversation{ID: newID(), Title: "t", Messages: msgs}
	a.mu.Lock()
	if err := a.saveConversation(conv); err != nil {
		t.Fatal(err)
	}
	a.mu.Unlock()

	updated, err := a.CompactConversation(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	compactedCount := 0
	for _, m := range updated.Messages {
		if m.Role == "tool" {
			if strings.Contains(contentStr(m.Content), "[older tool output pruned") {
				compactedCount++
			}
		}
		if m.Role == "user" && m.Content == "ببین @a.txt" && m.Attached != "" {
			t.Fatal("old user message Attached must be cleared by compaction")
		}
	}
	if compactedCount != 1 {
		t.Fatalf("expected 1 compacted tool result, got %d", compactedCount)
	}
	// متن اصلی پیام کاربر باید دست‌نخورده بماند
	if updated.Messages[0].Content != "ببین @a.txt" {
		t.Fatal("user content must be untouched")
	}
}

func TestHardSizeCap(t *testing.T) {
	a := newTestApp(t)
	a.settings.MaxConvKB = 2 // سقف خیلی کم برای تست

	msgs := []Message{}
	for i := 0; i < 30; i++ {
		msgs = append(msgs, Message{ID: newID(), Role: "user", Content: strings.Repeat("متن طولانی برای تست سقف حجم ", 200)})
	}
	conv := &Conversation{ID: newID(), Title: "big", Messages: msgs}
	a.mu.Lock()
	if err := a.saveConversation(conv); err != nil {
		t.Fatal(err)
	}
	// بررسی اندازه‌ی فایل نهایی
	dir, _ := a.conversationsDir()
	raw, err := os.ReadFile(filepath.Join(dir, conv.ID+".json"))
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > a.settings.MaxConvKB*1024 {
		// سقف تا کفِ حداقل پیام‌ها اعمال می‌شود؛ اگر هر پیام خیلی بزرگ
		// باشد، حداقل ۱۲ پیام حفظ می‌شود (اگر چه از سقف عبور کند).
		var chk Conversation
		if json.Unmarshal(raw, &chk) != nil || len(chk.Messages) != minKeepMessages {
			t.Fatalf("over-cap file must be trimmed to the %d-message floor: size=%d msgs=%d",
				minKeepMessages, len(raw), len(chk.Messages))
		}
	}
	// حداقل پیام‌ها باید بمانند
	var c2 Conversation
	if json.Unmarshal(raw, &c2) != nil {
		t.Fatal("corrupt conversation after cap enforcement")
	}
	if len(c2.Messages) < minKeepMessages {
		t.Fatalf("must keep at least %d messages, got %d", minKeepMessages, len(c2.Messages))
	}
}

func TestHardSizeCapFitsWhenFloorAllowed(t *testing.T) {
	a := newTestApp(t)
	// سقفی که ۱۲ پیامِ کوچک در آن جا می‌شود → فایل باید زیر سقف بماند
	a.settings.MaxConvKB = 64
	msgs := []Message{}
	for i := 0; i < 60; i++ {
		msgs = append(msgs, Message{ID: newID(), Role: "user", Content: strings.Repeat("data ", 100)})
	}
	conv := &Conversation{ID: newID(), Title: "big", Messages: msgs}
	a.mu.Lock()
	if err := a.saveConversation(conv); err != nil {
		t.Fatal(err)
	}
	dir, _ := a.conversationsDir()
	raw, err := os.ReadFile(filepath.Join(dir, conv.ID+".json"))
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 64*1024 {
		t.Fatalf("file must fit under cap when floor fits: %d > %d", len(raw), 64*1024)
	}
	var c2 Conversation
	if json.Unmarshal(raw, &c2) != nil || len(c2.Messages) > 60 {
		t.Fatal("corrupt conversation")
	}
}

// contentStr محتوای string پیام را برمی‌گرداند (برای تست‌ها).
func contentStr(v interface{}) string {
	s, _ := v.(string)
	return s
}
