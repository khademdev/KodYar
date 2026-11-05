// تست‌های رگرسیون نسخه‌ی ۱.3.0:
// 1. حذف Providerهای پیش‌فرض (شروع خالی) + مهاجرت یک‌باره‌ی تنظیمات قدیمی
// 2. حذف کامل گفتگو (فایل + ایندکس + حافظه) حتی در حین استریم
// 3. اجازه‌ی حذف آخرین Provider
// 4. OpenSupportPage در محیط تست (بدون Wails) بی‌خطر است
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------- Providerها

func TestNoDefaultProviders(t *testing.T) {
	s := defaultSettings()
	if len(s.Providers) != 0 {
		t.Fatalf("fresh settings must start with zero providers, got %d", len(s.Providers))
	}
	if s.Provider != "" || s.Model != "" {
		t.Fatalf("fresh settings must have empty active provider/model, got %q/%q", s.Provider, s.Model)
	}
}

func TestFreshLoadSettingsHasNoProviders(t *testing.T) {
	os.Setenv("MYAI_CONFIG_DIR", t.TempDir())
	a := NewApp()
	if err := a.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if len(a.settings.Providers) != 0 {
		t.Fatalf("expected zero providers on fresh install, got %d", len(a.settings.Providers))
	}
	if !a.settings.DefaultsRemoved {
		t.Fatal("DefaultsRemoved must be true after first load")
	}
}

func TestBuiltinProvidersMigration(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("MYAI_CONFIG_DIR", dir)

	// تنظیمات قدیمی: ۵ Provider پیش‌فرض + یک Provider سفارشی با کلید
	old := map[string]interface{}{
		"provider": "openai",
		"model":    "gpt-4o",
		"providers": []map[string]interface{}{
			{"id": "openai", "name": "OpenAI", "baseUrl": "https://api.openai.com/v1", "models": []string{"gpt-4o"}, "contextWindow": 200000},
			{"id": "groq", "name": "Groq", "baseUrl": "https://api.groq.com/openai/v1", "models": []string{"llama-3.3-70b-versatile"}, "contextWindow": 128000},
			{"id": "hormoz", "name": "Hormoz", "baseUrl": "https://api.hormoz.ai/v1", "models": []string{"gpt-4o"}, "contextWindow": 200000},
			{"id": "ollama", "name": "Ollama", "baseUrl": "http://localhost:11434/v1", "models": []string{"llama3.2"}, "contextWindow": 128000, "noKey": true},
			{"id": "mistral", "name": "Mistral AI", "baseUrl": "https://api.mistral.ai/v1", "models": []string{"mistral-large-latest"}, "contextWindow": 128000},
			{"id": "mycustom", "name": "MyCustom", "baseUrl": "https://api.mycustom.example/v1", "models": []string{"custom-model"}, "contextWindow": 100000},
		},
		"apiKeys":      map[string]string{"openai": "enc-openai", "mycustom": "enc-custom"},
		"temperature":  0.7,
		"tokenLimit":   200000,
		"maxHistory":   30,
		"maxConvKb":    2048,
		"contextFiles": 4,
	}
	raw, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewApp()
	if err := a.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if len(a.settings.Providers) != 1 {
		t.Fatalf("migration must keep only the custom provider, got %d", len(a.settings.Providers))
	}
	if a.settings.Providers[0].ID != "mycustom" {
		t.Fatalf("custom provider must survive, got %q", a.settings.Providers[0].ID)
	}
	if a.settings.Provider != "mycustom" {
		t.Fatalf("active provider must switch to remaining custom one, got %q", a.settings.Provider)
	}
	if a.settings.APIKeys["openai"] != "" {
		t.Fatal("builtin provider key must be removed")
	}
	if a.settings.APIKeys["mycustom"] != "enc-custom" {
		t.Fatal("custom provider key must survive")
	}
	if !a.settings.DefaultsRemoved {
		t.Fatal("migration flag must be set")
	}

	// فلگ روی دیسک ذخیره شده باشد؛ بارگذاری دوم نباید چیزی تغییر دهد
	a2 := NewApp()
	if err := a2.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if len(a2.settings.Providers) != 1 || a2.settings.Providers[0].ID != "mycustom" {
		t.Fatalf("second load must be stable, got %+v", a2.settings.Providers)
	}

	// پس از مهاجرت، Providerی که کاربر خودش با امضای builtin اضافه کند
	// دیگر نباید حذف شود (فلگ یک‌باره است).
	a2.settings.Providers = append(a2.settings.Providers, Provider{
		ID: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Models: []string{"gpt-4o"},
	})
	if err := a2.saveSettings(); err != nil {
		t.Fatal(err)
	}
	a3 := NewApp()
	if err := a3.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if len(a3.settings.Providers) != 2 {
		t.Fatalf("user-readded lookalike provider must NOT be stripped after migration, got %d", len(a3.settings.Providers))
	}
}

func TestCustomizedBuiltinProviderSurvives(t *testing.T) {
	s := defaultSettings()
	s.Provider = "openai"
	s.Providers = []Provider{
		{ID: "openai", Name: "OpenAI", BaseURL: "https://my-proxy.example.com/v1", Models: []string{"gpt-4o"}},
	}
	s.APIKeys = map[string]string{"openai": "enc"}
	if s.stripBuiltinProviders() {
		t.Fatal("provider with custom BaseURL must NOT be treated as builtin")
	}
	if len(s.Providers) != 1 || s.Provider != "openai" {
		t.Fatal("customized provider must survive untouched")
	}
}

func TestDeleteLastProviderAllowed(t *testing.T) {
	a := newTestApp(t)
	a.settings.Provider = "ollama"
	if err := a.DeleteProvider("ollama"); err != nil {
		t.Fatalf("deleting the last provider must be allowed: %v", err)
	}
	if len(a.settings.Providers) != 0 {
		t.Fatalf("providers must be empty, got %d", len(a.settings.Providers))
	}
	if a.settings.Provider != "" || a.settings.Model != "" {
		t.Fatalf("active provider must be cleared, got %q/%q", a.settings.Provider, a.settings.Model)
	}
	// بدون Provider، SendMessage باید پیام راهنمای فارسی بدهد نه کرش
	if _, err := a.SendMessage("", "سلام", nil); err == nil {
		t.Fatal("SendMessage without provider must fail with guidance")
	}
}

func TestSaveProviderRequiresModel(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.SaveProvider(Provider{Name: "X", BaseURL: "https://x.example/v1"}, ""); err == nil {
		t.Fatal("saving provider without any model must be rejected")
	}
}

func TestOpenSupportPageSafeWithoutUI(t *testing.T) {
	a := newTestApp(t)  // ctx == nil
	a.OpenSupportPage() // نباید پنیک کند
	if supportURL != "https://www.coffeebede.com/khademdev" {
		t.Fatal("support URL changed")
	}
}

// ---------------------------------------------------------------- حذف گفتگو
