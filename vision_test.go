// تست‌های Vision — پیام چندوجهی (اولویت ۶).
package main

import (
	"strings"
	"testing"
)

const testPNGData = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

func TestSanitizeImages(t *testing.T) {
	// فقط data URL تصویری معتبر
	in := []string{
		testPNGData,
		"data:image/jpeg;base64,AAAA",
		"http://evil.example.com/x.png", // URL خارجی — رد
		"data:text/plain;base64,AAAA",   // غیر تصویری — رد
		"not-a-data-url",
	}
	out := sanitizeImages(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 valid images, got %d: %v", len(out), out)
	}
	// سقف ۳ تصویر
	out = sanitizeImages([]string{testPNGData, testPNGData, testPNGData, testPNGData})
	if len(out) != 3 {
		t.Fatalf("max 3 images, got %d", len(out))
	}
}

func TestVisionMultimodalHistory(t *testing.T) {
	a := newTestApp(t)
	a.settings.Provider = "ollama"
	a.settings.Model = "llama3.2"

	// آخرین پیام کاربر تصویر دارد → content باید آرایه‌ی multimodal باشد
	conv := &Conversation{ID: newID(), Title: "t", Messages: []Message{
		{ID: "u1", Role: "user", Content: "این را ببین", Images: []string{testPNGData}},
		{ID: "a1", Role: "assistant", Content: "دیدم"},
		{ID: "u2", Role: "user", Content: "و این یکی", Images: []string{testPNGData}},
	}}
	history, err := a.buildHistoryForTest(conv)
	if err != nil {
		t.Fatal(err)
	}
	// آخرین پیام کاربر (u2) باید آرایه باشد
	var lastUser *chatMessage
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			lastUser = &history[i]
			break
		}
	}
	if lastUser == nil {
		t.Fatal("no user message in history")
	}
	parts, ok := lastUser.Content.([]contentPart)
	if !ok || len(parts) != 2 {
		t.Fatalf("last user message must be multimodal [text+image], got %T len=%v", lastUser.Content, len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "و این یکی" {
		t.Fatalf("text part wrong: %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil || !strings.HasPrefix(parts[1].ImageURL.URL, "data:image/") {
		t.Fatalf("image part wrong: %+v", parts[1])
	}

	// پیام کاربرِ قدیمی‌تر (u1) → متنی ساده (تصاویر فقط برای آخرین)
	foundOld := false
	for _, h := range history {
		if h.Role == "user" {
			if s, isStr := h.Content.(string); isStr && s == "این را ببین" {
				foundOld = true
			}
		}
	}
	if !foundOld {
		t.Fatal("older user message must stay plain text")
	}
}

func TestCompactionStripsOldImages(t *testing.T) {
	a := newTestApp(t)
	conv := &Conversation{ID: newID(), Title: "t", Messages: []Message{
		{ID: "u1", Role: "user", Content: "اول", Images: []string{testPNGData}},
		{ID: "a1", Role: "assistant", Content: "پاسخ"},
		{ID: "u2", Role: "user", Content: "دوم", Images: []string{testPNGData}},
	}}
	a.mu.Lock()
	a.compactConversationLocked(conv)
	a.mu.Unlock()
	if len(conv.Messages[0].Images) != 0 {
		t.Fatal("compaction must strip old images")
	}
	if len(conv.Messages[2].Images) != 1 {
		t.Fatal("last user message must keep its images")
	}
}
