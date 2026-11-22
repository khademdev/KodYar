// تست زندهٔ بینایی — اختیاری و فقط با Provider واقعی.
//
//	default go test ./...  → skip (بدون شبکه)
//	KODYAR_VISION_BASE_URL=https://api.openai.com/v1 \
//	KODYAR_VISION_API_KEY=sk-... \
//	KODYAR_VISION_MODEL=gpt-4o \
//	go test -run TestVisionLiveImageReachesModel -v .
//
// این تست همان مسیری را می‌رود که در اپ اجرا می‌شود: پیام tool با تصویرِ
// ذخیره‌شده در تاریخچه → prepareStream (ساخت پیام multimodal) → درخواست واقعی.
// اگر تصویر واقعاً به مدل نرسد، پاسخ دربارهٔ رنگ تصویر اشتباه/نامربوط می‌شود.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVisionLiveImageReachesModel(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("KODYAR_VISION_BASE_URL"))
	apiKey := strings.TrimSpace(os.Getenv("KODYAR_VISION_API_KEY"))
	model := strings.TrimSpace(os.Getenv("KODYAR_VISION_MODEL"))
	if baseURL == "" || apiKey == "" || model == "" {
		t.Skip("live vision test: set KODYAR_VISION_BASE_URL, KODYAR_VISION_API_KEY and KODYAR_VISION_MODEL")
	}

	// تصویر یک‌رنگ قرمز — پاسخ درست فقط با دیدن تصویر ممکن است.
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 96, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 96; x++ {
			img.Set(x, y, color.RGBA{R: 220, G: 20, B: 30, A: 255})
		}
	}
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}

	a := newTestApp(t)
	a.settings.Providers = []Provider{{
		ID: "vision-live", Name: "vision-live", BaseURL: baseURL,
		NoKey: true, ContextWindow: 128000, Models: []string{model},
	}}
	a.settings.Provider = "vision-live"
	a.settings.Model = model
	a.settings.VisionImages = visionModeOn
	a.settings.AgentMode = true
	shotPath := filepath.Join(a.settings.WorkspacePath, "screenshots", "red.png")
	if err := os.MkdirAll(filepath.Dir(shotPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shotPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	dataURL, err := visionImageDataURL(shotPath)
	if err != nil {
		t.Fatalf("prepare screenshot data URL: %v", err)
	}

	conv := &Conversation{ID: "live", Messages: []Message{
		{ID: "u1", Role: "user", Content: "What color is the screenshot I captured?"},
		{ID: "a1", Role: "assistant",
			ToolCalls: []toolCallRecord{{ID: "c1", Name: "browser_screenshot", Args: `{}`}}},
		{ID: "t1", Role: "tool", Content: "OK: screenshot saved", ToolCallID: "c1",
			ToolName: "browser_screenshot", ToolOK: true, Images: []string{dataURL}},
	}}

	_, _, _, _, history, err := a.prepareStream(conv, false, "")
	if err != nil {
		t.Fatalf("prepareStream: %v", err)
	}
	images := 0
	for _, m := range history {
		if parts, ok := m.Content.([]contentPart); ok {
			for _, p := range parts {
				if p.Type == "image_url" && p.ImageURL != nil {
					images++
				}
			}
		}
	}
	if images != 1 {
		t.Fatalf("transport bug: history carries %d image part(s), want 1", images)
	}

	body, err := json.Marshal(map[string]interface{}{
		"model":      model,
		"messages":   history,
		"stream":     false,
		"max_tokens": 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("live request failed: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode provider reply: %v (%s)", err, strings.TrimSpace(string(raw)))
	}
	if out.Error != nil && out.Error.Message != "" {
		t.Fatalf("provider error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		t.Fatalf("provider returned no choices: %s", strings.TrimSpace(string(raw)))
	}
	answer := strings.ToLower(out.Choices[0].Message.Content)
	if !strings.Contains(answer, "red") && !strings.Contains(answer, "قرمز") && !strings.Contains(answer, "سرخ") {
		t.Fatalf("model did not describe the red image — the image likely never reached it.\nanswer: %s", answer)
	}
}
