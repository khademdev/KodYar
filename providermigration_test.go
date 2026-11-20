// تست‌های رگرسیون نسخه‌ی ۱.3.0:
// 1. حذف Providerهای پیش‌فرض (شروع خالی) + مهاجرت یک‌باره‌ی تنظیمات قدیمی
// 2. حذف کامل گفتگو (فایل + ایندکس + حافظه) حتی در حین استریم
// 3. اجازه‌ی حذف آخرین Provider
// 4. OpenSupportPage در محیط تست (بدون Wails) بی‌خطر است
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	os.Setenv("KODYAR_CONFIG_DIR", t.TempDir())
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
	os.Setenv("KODYAR_CONFIG_DIR", dir)

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

// Provider فقط با نام و کلید API ساخته می‌شود: آدرس پایه از نام تشخیص داده
// می‌شود و فهرست مدل‌ها و پنجره‌ی کانتکست از خود سرویس‌دهنده خوانده می‌شود.
func TestSaveProviderAutoConfiguresFromService(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"zeta-chat","context_window":64000},{"id":"text-embedding-3","context_window":8000}]}`))
	}))
	defer srv.Close()

	a := newTestApp(t)
	p, err := a.SaveProvider(Provider{Name: "Local Gateway", BaseURL: srv.URL + "/v1"}, "")
	if err != nil {
		t.Fatalf("auto-configured SaveProvider failed: %v", err)
	}
	if p.BaseURL != srv.URL+"/v1" {
		t.Fatalf("base URL must be stored, got %q", p.BaseURL)
	}
	if len(p.Models) != 2 || p.Models[0] != "zeta-chat" {
		t.Fatalf("models must come from the service (chat models first), got %v", p.Models)
	}
	if p.ContextWindow != 64000 {
		t.Fatalf("context window must come from discovery, got %d", p.ContextWindow)
	}
	if !p.NoKey {
		t.Fatal("a provider discovered without an API key must be marked NoKey")
	}
	if gotAuth != "" {
		t.Fatalf("no Authorization header may be sent without a key, got %q", gotAuth)
	}
}

// تشخیص آدرس پایه فقط از نام/کلید — بدون شبکه.
func TestInferProviderBaseURL(t *testing.T) {
	cases := []struct{ name, key, want string }{
		{"OpenRouter", "", "https://openrouter.ai/api/v1"},
		{"Groq", "", "https://api.groq.com/openai/v1"},
		{"My Gateway", "AIzaSyTest", "https://generativelanguage.googleapis.com/v1beta"},
		{"OpenRouter", "sk-or-v1-abc", "https://openrouter.ai/api/v1"},
		{"api.example.com/v1", "", "https://api.example.com/v1"},
		{"localhost:1234", "", "http://localhost:1234/v1"},
		{"MyCorp", "", ""},
		// نام نمایشی آزاد (تنها ورودی فرم جدید) هم باید تشخیص داده شود.
		{"OpenRouter (حساب شخصی)", "", "https://openrouter.ai/api/v1"},
		{"Groq — کلید دوم", "", "https://api.groq.com/openai/v1"},
		{"سرور LM Studio من", "", "http://localhost:1234/v1"},
		{"DeepSeek حساب شخصی", "", "https://api.deepseek.com/v1"},
		{"کلید OpenRouter من", "sk-or-v1-abc", "https://openrouter.ai/api/v1"},
		// دامنه‌ی داخل نام آزاد (سرویس سفارشی) — بدون فیلد آدرس.
		{"سرور من — api.example.com/v1", "", "https://api.example.com/v1"},
		{"موتور محلی localhost:1234", "", "http://localhost:1234/v1"},
		{"سرور LAN 192.168.1.50:8080", "", "https://192.168.1.50:8080/v1"},
		{"مثلاً نسخه ۱.۲", "", ""},
	}
	for _, c := range cases {
		if got := inferProviderBaseURL(c.name, c.key); got != c.want {
			t.Errorf("inferProviderBaseURL(%q,%q)=%q want %q", c.name, c.key, got, c.want)
		}
	}
}

// TestSaveProviderOnlyDisplayName — فرم جدید فقط «نام سرویس» و «کلید API»
// دارد؛ Name فنی از همان نام ساخته می‌شود و شناسه/مدل‌ها خودکار درمی‌آیند.
func TestSaveProviderOnlyDisplayName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"chat-a","context_window":96000},{"id":"chat-b"}]}`))
	}))
	defer srv.Close()

	a := newTestApp(t)
	// نام آزاد بدون دامنه/نام سرویس شناخته‌شده → خطای راهنما (نه پیام «نام الزامی»).
	if _, err := a.SaveProvider(Provider{DisplayName: "My Gateway"}, "sk-test-key"); err == nil || !strings.Contains(err.Error(), "شناسایی نشد") {
		t.Fatalf("unknown display name must ask for the service name/API address, got %v", err)
	}

	p, err := a.SaveProvider(Provider{DisplayName: "My Gateway", BaseURL: srv.URL + "/v1"}, "sk-test-key")
	if err != nil {
		t.Fatalf("display-name-only SaveProvider failed: %v", err)
	}
	if p.Name != "My Gateway" {
		t.Fatalf("technical name must be derived from the display name, got %q", p.Name)
	}
	if p.DisplayName != "My Gateway" {
		t.Fatalf("display name must be preserved, got %q", p.DisplayName)
	}
	if p.ID != "my-gateway" {
		t.Fatalf("provider id must be a slug of the name, got %q", p.ID)
	}
	if len(p.Models) != 2 || p.Models[0] != "chat-a" || p.ContextWindow != 96000 {
		t.Fatalf("models/context must be discovered automatically: %+v", p)
	}

	// نام خالی (نه فنی، نه نمایشی) → خطای روشن، بدون تماس شبکه.
	if _, err := a.SaveProvider(Provider{}, ""); err == nil || !strings.Contains(err.Error(), "نام سرویس‌دهنده") {
		t.Fatalf("empty name must be rejected with a clear message, got %v", err)
	}
}

// TestProviderFormUsesBaseURLAndDisplayName — فرم Provider «آدرس سرویس
// (Base URL)» را به‌عنوان شناسه می‌گیرد و یک «نام نمایشی» اختیاری دارد؛ فیلد
// دستی برای مدل‌ها/کانتکست هنوز وجود ندارد (خودکار شناسایی می‌شوند).
func TestProviderFormUsesBaseURLAndDisplayName(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("frontend", "dist", "index.html"))
	if err != nil {
		t.Fatalf("index.html: %v", err)
	}
	html := string(raw)
	for _, want := range []string{
		`id="prov-name"`, `id="prov-key"`, `id="prov-display"`,
		"آدرس سرویس (Base URL)", "نام نمایشی",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("provider form must keep %q", want)
		}
	}
	for _, unwanted := range []string{`id="prov-models"`, `id="prov-context"`} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("provider form must not expose manual field %q", unwanted)
		}
	}
	js, err := os.ReadFile(filepath.Join("frontend", "dist", "assets", "js", "modals.js"))
	if err != nil {
		t.Fatalf("modals.js: %v", err)
	}
	src := string(js)
	if !strings.Contains(src, "baseUrl:url") {
		t.Fatal("the URL field must be sent as baseUrl (it is the service identity)")
	}
	if !strings.Contains(src, "displayName:disp||host") {
		t.Fatal("the optional display name must fall back to the URL host")
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
