// تست‌های کش پرامپت (فاز ۲): نشانه‌ها، تشخیص رد شدن و تنظیم opt-in.
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPromptCacheHintsAppliedToStableBlocks(t *testing.T) {
	req := chatRequest{
		Model: "m",
		Messages: []chatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hi"},
		},
		Tools: []toolDef{{Type: "function"}, {Type: "function"}},
	}
	got, applied := withPromptCacheHints(req)
	if !applied {
		t.Fatal("hints must be applied")
	}
	// تاریخچه/ابزار اصلی نباید آلوده شود (کپی)
	if req.Messages[0].CacheControl != nil || req.Tools[1].CacheControl != nil {
		t.Fatal("original request must stay untouched")
	}
	if got.Messages[0].CacheControl == nil || got.Messages[0].CacheControl.Type != "ephemeral" {
		t.Fatal("system message must carry the hint")
	}
	if got.Messages[1].CacheControl != nil {
		t.Fatal("only the system message may carry a hint")
	}
	if got.Tools[1].CacheControl == nil || got.Tools[0].CacheControl != nil {
		t.Fatal("only the last tool may carry a hint")
	}
	body, _ := json.Marshal(got)
	if strings.Count(string(body), `"cache_control":{"type":"ephemeral"}`) != 2 {
		t.Fatalf("beta hints missing from JSON: %s", body)
	}
	// بدون پیام system یا ابزار، چیزی اعمال نمی‌شود
	if _, applied := withPromptCacheHints(chatRequest{Messages: []chatMessage{{Role: "user", Content: "x"}}}); applied {
		t.Fatal("no system message → no hints")
	}
}

func TestPromptCacheUnsupportedDetection(t *testing.T) {
	cases := []struct {
		status int
		reason string
		want   bool
	}{
		{400, `{"error":"unknown field cache_control"}`, true},
		{422, "prompt caching is not supported", true},
		{400, "invalid model", false},
		{500, "cache_control unsupported", false},
		{401, "unauthorized", false},
	}
	for _, c := range cases {
		if got := isPromptCacheUnsupported(c.status, c.reason); got != c.want {
			t.Fatalf("status=%d reason=%q → %v, want %v", c.status, c.reason, got, c.want)
		}
	}
}

func TestPromptCachePerProviderMemory(t *testing.T) {
	old := resetPromptCacheMemoryForTest()
	t.Cleanup(func() { restorePromptCacheMemoryForTest(old) })

	base := "http://cache.example/v1"
	if !providerSupportsPromptCache(base) {
		t.Fatal("fresh provider must support caching")
	}
	markPromptCacheBroken(base)
	if providerSupportsPromptCache(base) {
		t.Fatal("marked provider must be remembered")
	}
	// نرمال‌سازی اسلش انتهایی هم باید یکی حساب شود
	if providerSupportsPromptCache(base + "/") {
		t.Fatal("baseURL normalization must match")
	}
	if !providerSupportsPromptCache("http://other.example/v1") {
		t.Fatal("other providers must stay unaffected")
	}
	restorePromptCacheMemoryForTest(old)
	if !providerSupportsPromptCache(base) {
		t.Fatal("restore must clear the memory")
	}
}

// کش پرامپت از ۱.۹ پیش‌فرض روشن است (سرعت/هزینهٔ هر دور) و انتخاب کاربر
// (حتی «خاموش») ماندگار می‌ماند.
func TestPromptCacheSettingDefaultsOnAndPersists(t *testing.T) {
	if !defaultSettings().PromptCache {
		t.Fatal("prompt cache must be on by default")
	}
	a := newTestApp(t)
	if !a.promptCacheEnabled() {
		t.Fatal("fresh app must start with prompt caching enabled")
	}
	if v, ok := a.SettingsView()["promptCache"].(bool); !ok || !v {
		t.Fatalf("SettingsView must expose promptCache=true by default, got %#v", a.SettingsView()["promptCache"])
	}
	// تنظیمات قدیمی که فیلد را ندارند هم روشن محسوب می‌شوند.
	a.settings.PromptCache = false
	a.settings.PromptCacheTouched = false
	if !a.promptCacheEnabled() {
		t.Fatal("legacy settings without the touched flag must default to enabled")
	}
	if err := a.SetPromptCache(true); err != nil {
		t.Fatalf("SetPromptCache: %v", err)
	}
	if !a.promptCacheEnabled() {
		t.Fatal("setting must turn caching on")
	}
	view := a.SettingsView()
	if v, ok := view["promptCache"].(bool); !ok || !v {
		t.Fatalf("SettingsView must expose promptCache=true, got %#v", view["promptCache"])
	}
	if err := a.SetPromptCache(false); err != nil {
		t.Fatalf("SetPromptCache: %v", err)
	}
	if a.promptCacheEnabled() {
		t.Fatal("setting must turn caching off")
	}
}
