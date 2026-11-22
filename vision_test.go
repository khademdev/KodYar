// تست‌های Vision — پیام چندوجهی (اولویت ۶).
package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
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

func TestModelSupportsVision(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"gpt-4o", true},
		{"openai/gpt-4.1-mini", true},
		{"gpt-5", true},
		{"claude-3-5-sonnet", true},
		{"claude-sonnet-4-5", true},
		{"gemini-2.5-pro", true},
		{"qwen2.5-vl:7b", true},
		{"llava:13b", true},
		// مدل‌های متنی/ناشناخته نباید تصویر بگیرند (درخواست ۴۰۰).
		{"llama3.2", false},
		{"qwen2.5-coder", false},
		{"deepseek-coder", false},
		{"my-local-llm", false},
		{"o3-mini", false},
		{"o1-mini", false},
		{"o3", true},
		{"", false},
	}
	for _, c := range cases {
		if got := modelSupportsVision(c.model); got != c.want {
			t.Errorf("modelSupportsVision(%q) = %v, want %v", c.model, got, c.want)
		}
	}
}

func TestVisionImagesEnabled(t *testing.T) {
	if !visionImagesEnabled("on", "my-local-llm") {
		t.Fatal("mode on must force images even for an unknown model")
	}
	if visionImagesEnabled("off", "gpt-4o") {
		t.Fatal("mode off must never send images")
	}
	if !visionImagesEnabled("", "gpt-4o") {
		t.Fatal("empty mode is auto and gpt-4o has vision")
	}
	if visionImagesEnabled("auto", "my-local-llm") {
		t.Fatal("auto must stay conservative for unknown models")
	}
	if mode := normalizeVisionMode(" OFF "); mode != "off" {
		t.Fatalf("normalizeVisionMode = %q", mode)
	}
	if mode := normalizeVisionMode("whatever"); mode != "auto" {
		t.Fatalf("unknown mode must fall back to auto, got %q", mode)
	}
}

func TestSplitShotMarker(t *testing.T) {
	in := "OK: screenshot saved to screenshots/a.png (123 bytes, viewport)\n" +
		shotMarkerPrefix + `C:\ws\screenshots\a.png` + "\nThe image is attached to this turn."
	clean, path := splitShotMarker(in)
	if path != `C:\ws\screenshots\a.png` {
		t.Fatalf("path = %q", path)
	}
	if strings.Contains(clean, "KODYAR_SHOT_FILE") {
		t.Fatalf("marker leaked into tool text: %q", clean)
	}
	if !strings.Contains(clean, "OK: screenshot saved") || !strings.Contains(clean, "attached") {
		t.Fatalf("clean text lost content: %q", clean)
	}
	if _, p := splitShotMarker("no marker here"); p != "" {
		t.Fatalf("unexpected path %q", p)
	}
}

func TestVisionImageDataURL(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(good, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	url, err := visionImageDataURL(good)
	if err != nil {
		t.Fatalf("valid png rejected: %v", err)
	}
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("not a png data URL: %.40q", url)
	}

	big := filepath.Join(dir, "big.png")
	if err := os.WriteFile(big, make([]byte, visionMaxBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := visionImageDataURL(big); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized png must be refused with a clear error, got %v", err)
	}

	notPNG := filepath.Join(dir, "shot.jpg")
	if err := os.WriteFile(notPNG, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := visionImageDataURL(notPNG); err == nil {
		t.Fatal("non-png paths must be refused")
	}
}

func TestHistoryAttachesToolScreenshotOnlyForVisionModels(t *testing.T) {
	conv := &Conversation{ID: "t", Messages: []Message{
		{ID: "u1", Role: "user", Content: "check the page"},
		{ID: "a1", Role: "assistant", ToolCalls: []toolCallRecord{{ID: "c1", Name: "browser_screenshot", Args: "{}"}}},
		{ID: "t1", Role: "tool", Content: "OK: screenshot saved", ToolCallID: "c1", ToolName: "browser_screenshot", Images: []string{testPNGData}},
	}}

	a := newTestApp(t)
	a.settings.Providers[0].Models = []string{"gpt-4o"}
	a.settings.Model = "gpt-4o"
	_, _, _, _, hist, err := a.prepareStream(conv, false, "")
	if err != nil {
		t.Fatal(err)
	}
	last := hist[len(hist)-1]
	parts, ok := last.Content.([]contentPart)
	if !ok {
		t.Fatalf("vision model must get an image-bearing message, got %T (%v)", last.Content, last.Content)
	}
	images := 0
	for _, p := range parts {
		if p.Type == "image_url" && p.ImageURL != nil && p.ImageURL.URL == testPNGData {
			images++
		}
	}
	if images != 1 {
		t.Fatalf("expected exactly one attached screenshot, got %d", images)
	}

	// همان تاریخچه با مدل بدون vision: هیچ تصویری ارسال نمی‌شود.
	a.settings.Providers[0].Models = []string{"my-local-llm"}
	a.settings.Model = "my-local-llm"
	_, _, _, _, hist, err = a.prepareStream(conv, false, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range hist {
		if _, isParts := m.Content.([]contentPart); isParts {
			t.Fatalf("text-only model must not receive image parts: %+v", m.Content)
		}
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
