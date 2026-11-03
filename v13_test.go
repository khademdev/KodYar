// تست‌های رگرسیون نسخه‌ی ۱.3.0:
// 1. حذف Providerهای پیش‌فرض (شروع خالی) + مهاجرت یک‌باره‌ی تنظیمات قدیمی
// 2. حذف کامل گفتگو (فایل + ایندکس + حافظه) حتی در حین استریم
// 3. اجازه‌ی حذف آخرین Provider
// 4. OpenSupportPage در محیط تست (بدون Wails) بی‌خطر است
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
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

func TestDeleteConversationRemovesEverything(t *testing.T) {
	a := newTestApp(t)
	c1 := &Conversation{ID: newID(), Title: "یکی", Messages: []Message{{ID: newID(), Role: "user", Content: "سلام"}}}
	c2 := &Conversation{ID: newID(), Title: "دو", Messages: []Message{{ID: newID(), Role: "user", Content: "دوباره"}}}
	a.mu.Lock()
	_ = a.saveConversation(c1)
	_ = a.saveConversation(c2)
	a.mu.Unlock()

	if err := a.DeleteConversation(c1.ID); err != nil {
		t.Fatal(err)
	}

	dir, _ := a.conversationsDir()
	if _, err := os.Stat(convPath(dir, c1.ID)); !os.IsNotExist(err) {
		t.Fatal("conversation file must be deleted from disk")
	}
	// ایندکس هم باید تمیز باشد
	idx := loadConversationIndex(dir)
	if idx != nil {
		for _, m := range idx.Entries {
			if m.ID == c1.ID {
				t.Fatal("index entry must be removed")
			}
		}
	}
	if got := a.GetConversations(); len(got) != 1 || got[0].ID != c2.ID {
		t.Fatalf("GetConversations must return only the remaining conversation, got %+v", got)
	}

	// حذف همه
	if err := a.DeleteAllConversations(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != ".index.json" {
			t.Fatalf("unexpected file left after delete-all: %s", e.Name())
		}
	}
	if got := a.GetConversations(); len(got) != 0 {
		t.Fatalf("all conversations must be gone, got %d", len(got))
	}
}

// TestDeleteConversationDuringStream سناریوی اصلی «حذف فقط از UI»:
// گفتگوی فعال در حال استریم حذف می‌شود؛ ذخیره‌ی نیمه‌کاره‌ی گوروتین
// نباید فایل را دوباره روی دیسک بسازد.
func TestDeleteConversationDuringStream(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for i := 0; i < 500; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"پاسخ \"}}]}\n\n")
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer srv.Close()

	a := newTestApp(t)
	a.settings.Providers[0].BaseURL = srv.URL // هدایت به سرور آزمایشی

	conv, err := a.SendMessage("", "تست حذف در حین استریم", nil)
	if err != nil {
		t.Fatal(err)
	}

	// صبر تا اولین درخواست استریم واقعاً برسد
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&hits) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("stream request never arrived")
	}
	time.Sleep(120 * time.Millisecond) // چند دلتا جاری شود

	// حذف در حین استریم — باید استریم را متوقف و منتظر خروج کامل بماند
	if err := a.DeleteConversation(conv.ID); err != nil {
		t.Fatal(err)
	}

	dir, _ := a.conversationsDir()
	if _, err := os.Stat(convPath(dir, conv.ID)); !os.IsNotExist(err) {
		t.Fatal("conversation file must be deleted despite active stream")
	}
	// گوروتین باید کاملاً خارج شده باشد
	a.genMu.Lock()
	genNil := a.gen == nil
	a.genMu.Unlock()
	if !genNil {
		t.Fatal("stream goroutine must have exited after delete")
	}

	// هیچ ذخیره‌ی دیرهنگامی فایل را بازنگرداند
	time.Sleep(250 * time.Millisecond)
	if _, err := os.Stat(convPath(dir, conv.ID)); !os.IsNotExist(err) {
		t.Fatal("deleted conversation resurrected by late save (memory-leak style race)")
	}
	if got := a.GetConversations(); len(got) != 0 {
		t.Fatalf("conversation list must be empty after delete-during-stream, got %d", len(got))
	}
}
