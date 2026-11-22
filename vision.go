// vision.go — ارسال واقعی تصویر به مدل، فقط وقتی مدل ورودی تصویر دارد.
//
// قاعده: هرگز تصویر را به مدلی که vision ندارد نفرست (درخواست ۴۰۰/۴۲۲
// می‌شود و کل نوبت می‌شکند). سه حالت داریم (Settings.VisionImages):
//   - auto (پیش‌فرض): فقط خانواده‌های شناخته‌شدهٔ تصویری؛ ناشناخته = بدون تصویر
//   - on:  اجبار برای هر مدلی (وقتی می‌دانی Provider تصویر را می‌پذیرد)
//   - off: هرگز
//
// تصویر اسکرین‌شات با یک خطِ نشانه در نتیجهٔ ابزار به حلقه می‌رسد (نه با
// پارس کردن متن آزاد) و حلقه خودش فایل PNG را به data URL تبدیل می‌کند.
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

const (
	visionModeAuto = "auto"
	visionModeOn   = "on"
	visionModeOff  = "off"

	// visionMaxBytes سقف حجم PNG برای پیوست‌کردن — base64 ~۱.۳۳ برابر
	// می‌شود و پیام‌های چندمگابایتی هم کند هستند هم بعضی Providerها را
	// رد می‌کنند. اسکرین‌شات viewport معمولاً خیلی کمتر است.
	visionMaxBytes = 2800 * 1024
	// visionMaxSide سقف هر ضلع تصویر (پیکسل).
	visionMaxSide = 8000
	// visionMaxShots سقف تعداد تصویری که در یک دور به مدل می‌رود.
	visionMaxShots = 3

	// shotMarkerPrefix خطِ نشانه در نتیجهٔ browser_screenshot؛ مسیر مطلق PNG.
	// حلقه آن را از متن ابزار حذف می‌کند و تصویر را ضمیمه می‌کند.
	shotMarkerPrefix = "KODYAR_SHOT_FILE: "
)

// normalizeVisionMode مقدار تنظیمات را به auto|on|off نرمال می‌کند؛ هر
// مقدار ناشناخته (و خالی) = auto.
func normalizeVisionMode(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case visionModeOn:
		return visionModeOn
	case visionModeOff:
		return visionModeOff
	default:
		return visionModeAuto
	}
}

// visionModelHints زیررشته‌های نام مدل‌هایی که ورودی تصویر دارند.
// لیست عمداً محافظه‌کارانه است: مدل ناشناخته تصویر نمی‌گیرد.
var visionModelHints = []string{
	"gpt-4o", "gpt-4.1", "gpt-4.5", "gpt-4-turbo", "gpt-4-vision", "gpt-5", "chatgpt-4o",
	"claude-3", "claude-4", "claude-sonnet", "claude-opus", "claude-haiku",
	"gemini", "gemma-3", "gemma3",
	"llama-3.2", "llama-4", "llama-vision", "llava",
	"qwen-vl", "qwen2-vl", "qwen2.5-vl", "qwen3-vl", "qvq",
	"pixtral", "mistral-small-3", "mistral-medium-3",
	"minicpm-v", "internvl", "glm-4v", "glm-4.5v", "glm-4.6v",
	"deepseek-vl", "kimi-vl", "yi-vl", "phi-3-vision", "phi-4-multimodal",
	"grok-2", "grok-4", "nova",
}

// modelSupportsVision آیا این مدل ورودی تصویر می‌گیرد؟ (تشخیص نام، نه
// پرسیدن از Provider — APIهای سازگار با OpenAI قابلیت‌ها را گزارش نمی‌کنند.)
func modelSupportsVision(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	if i := strings.IndexAny(m, ":@"); i > 0 {
		m = m[:i] // تگ Ollama/کوانتایز: "llava:13b"
	}
	if m == "" {
		return false
	}
	// استثناهای صریح: از سری o فقط نسخه‌های غیر-mini تصویر می‌گیرند.
	for _, no := range []string{"o1-mini", "o3-mini"} {
		if strings.Contains(m, no) {
			return false
		}
	}
	if strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") {
		return true
	}
	for _, h := range visionModelHints {
		if strings.Contains(m, h) {
			return true
		}
	}
	return false
}

// visionImagesEnabled تصمیم نهایی برای این مدل و این تنظیمات.
func visionImagesEnabled(mode, model string) bool {
	switch normalizeVisionMode(mode) {
	case visionModeOn:
		return true
	case visionModeOff:
		return false
	}
	return modelSupportsVision(model)
}

// splitShotMarker خطِ نشانه را از نتیجهٔ ابزار جدا می‌کند و مسیر مطلق PNG را
// برمی‌گرداند (بدون نشانه: مسیر خالی و متن دست‌نخورده).
func splitShotMarker(text string) (clean, path string) {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.HasPrefix(ln, shotMarkerPrefix) {
			if path == "" {
				path = strings.TrimSpace(strings.TrimPrefix(ln, shotMarkerPrefix))
			}
			continue
		}
		kept = append(kept, ln)
	}
	clean = strings.TrimRight(strings.Join(kept, "\n"), "\n")
	return clean, path
}

// visionImageDataURL یک PNG داخل workspace را به data URL تبدیل می‌کند.
// فقط .png، حجم محدود و ابعاد محدود پذیرفته می‌شود تا پیام مدل قابل‌کنترل
// بماند؛ خطاها به مدل می‌رسند تا بداند چرا تصویر ضمیمه نشد.
func visionImageDataURL(path string) (string, error) {
	if !strings.EqualFold(filepath.Ext(path), ".png") {
		return "", fmt.Errorf("%s is not a .png file", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("screenshot file is unreadable: %v", err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("screenshot file is empty")
	}
	if len(data) > visionMaxBytes {
		return "", fmt.Errorf("screenshot is too large to attach (%d KB > %d KB); capture the viewport (full_page=false) instead",
			len(data)/1024, visionMaxBytes/1024)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("screenshot is not a valid PNG: %v", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > visionMaxSide || cfg.Height > visionMaxSide {
		return "", fmt.Errorf("screenshot dimensions %dx%d are outside the attachable range (max %d per side)",
			cfg.Width, cfg.Height, visionMaxSide)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data), nil
}
