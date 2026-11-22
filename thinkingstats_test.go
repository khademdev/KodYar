package main

import (
	"strings"
	"testing"
)

// TestPersistAgentRoundStoresThinkingStats — مدت و توکن تقریبی «فکر کردن»
// روی خودِ پیام ذخیره می‌شوند تا بعد از بستن/بازکردن دوبارهٔ گفتگو هم
// خلاصه‌ی «فکر کردن · ۲.۴s · ~N توکن» باقی بماند.
func TestPersistAgentRoundStoresThinkingStats(t *testing.T) {
	a := newTestApp(t)
	conv := &Conversation{
		ID:        "thinkstats",
		Title:     "t",
		CreatedAt: 1,
		Messages:  []Message{{ID: "u1", Role: "user", Content: "بررسی کن", At: 1}},
	}
	a.mu.Lock()
	err := a.saveConversation(conv)
	a.mu.Unlock()
	if err != nil {
		t.Fatalf("save conversation: %v", err)
	}

	if !a.persistAgentRound("thinkstats", "نتیجه\n<thinking>بررسی اولیهٔ ساختار پوشه</thinking>", nil, nil, 2400) {
		t.Fatal("persistAgentRound failed")
	}

	got, err := a.loadConversation("thinkstats")
	if err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	var assistant *Message
	for i := range got.Messages {
		if got.Messages[i].Role == "assistant" {
			assistant = &got.Messages[i]
		}
	}
	if assistant == nil {
		t.Fatal("assistant message was not persisted")
	}
	if assistant.ThinkingMs != 2400 {
		t.Fatalf("ThinkingMs = %d, want 2400", assistant.ThinkingMs)
	}
	if assistant.ThinkingTokens <= 0 {
		t.Fatalf("ThinkingTokens = %d, want > 0", assistant.ThinkingTokens)
	}
	if !strings.Contains(assistant.Thinking, "بررسی اولیه") {
		t.Fatalf("Thinking = %q, want stored reasoning text", assistant.Thinking)
	}

	// بی‌استدلال → هیچ آمار فکری ذخیره نمی‌شود (فیلدهای خالی در JSON نمی‌مانند).
	if !a.persistAgentRound("thinkstats", "فقط پاسخ", nil, nil, 0) {
		t.Fatal("second persistAgentRound failed")
	}
	got, err = a.loadConversation("thinkstats")
	if err != nil {
		t.Fatalf("reload conversation: %v", err)
	}
	last := got.Messages[len(got.Messages)-1]
	if last.ThinkingMs != 0 || last.ThinkingTokens != 0 {
		t.Fatalf("no-thinking message got stats: ms=%d tokens=%d", last.ThinkingMs, last.ThinkingTokens)
	}
}
