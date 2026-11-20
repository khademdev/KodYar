// تست‌های امن‌سازی تنظیمات و ایندکس گفتگو (جدا شده از فایل بازبینی مرحله‌ای).
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsSanitizesMaliciousProviderFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KODYAR_CONFIG_DIR", dir)
	const evilID = `"><img src=x onerror=alert(1)>`
	const evilColor = `red" onmouseover="alert(2)`
	// فیکسچر با Marshal ساخته می‌شود تا کوتیشن‌های مخرب به‌درستی
	// JSON-escape شوند (فایل دست‌کاری‌شده‌ی واقعی همین کار را می‌کند).
	fixture := map[string]interface{}{
		"providers": []map[string]interface{}{
			{"id": evilID, "name": "Evil Cloud", "baseUrl": "https://evil.example/v1", "color": evilColor, "models": []string{"m1"}},
			{"id": "safe-id", "name": "Good Cloud", "baseUrl": "https://good.example/v1", "color": "#3fb984", "models": []string{"m2"}},
			{"id": "ابر-پارسی", "name": "ابر فارسی", "baseUrl": "https://fa.example/v1", "color": "", "models": []string{"m3"}},
		},
		"apiKeys": map[string]string{evilID: "sk-secret"},
	}
	raw, merr := json.Marshal(fixture)
	if merr != nil {
		t.Fatal(merr)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	if err := a.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if len(a.settings.Providers) != 3 {
		t.Fatalf("want 3 providers, got %d", len(a.settings.Providers))
	}
	p0 := a.settings.Providers[0]
	if !providerIDSafe(p0.ID) || strings.ContainsAny(p0.ID, `"><=`) {
		t.Fatalf("malicious ID must be regenerated safe, got %q", p0.ID)
	}
	if p0.ID != "evil-cloud" {
		t.Fatalf("regenerated ID must be the name slug, got %q", p0.ID)
	}
	if p0.Color != "#7c6cf0" {
		t.Fatalf("malicious color must reset to default, got %q", p0.Color)
	}
	if got := a.settings.APIKeys["evil-cloud"]; got != "sk-secret" {
		t.Fatalf("API key must migrate to the regenerated ID, got %q", got)
	}
	if a.settings.Providers[1].ID != "safe-id" || a.settings.Providers[1].Color != "#3fb984" {
		t.Fatal("already-safe provider fields must be preserved")
	}
	// شناسه‌های فارسیِ مشروع (خروجی slugify) نباید قربانی پاک‌سازی شوند.
	if a.settings.Providers[2].ID != "ابر-پارسی" {
		t.Fatalf("legitimate Persian ID must survive, got %q", a.settings.Providers[2].ID)
	}
}

// TestSaveProviderRejectsUnsafeFields — مسیر ذخیره‌سازی هم همان گارد را
// دارد (ویرایشِ ساختگی با شناسه/رنگ مخرب رد می‌شود).
func TestSaveProviderRejectsUnsafeFields(t *testing.T) {
	a := newTestApp(t)
	_, err := a.SaveProvider(Provider{
		ID: `x"><img`, Name: "X", BaseURL: "https://x.example/v1", Models: []string{"m"},
	}, "")
	if err == nil || !strings.Contains(err.Error(), "شناسه") {
		t.Fatalf("malicious provider ID must be rejected, got err=%v", err)
	}
	_, err = a.SaveProvider(Provider{
		Name: "Y", BaseURL: "https://y.example/v1", Color: `red" onmouseover="x`, Models: []string{"m"},
	}, "")
	if err == nil || !strings.Contains(err.Error(), "هگز") {
		t.Fatalf("malicious provider color must be rejected, got err=%v", err)
	}
}

// TestConversationIndexSkipsMaliciousIDs — رگرسیون امنیتی: فایل گفتگوی
// کاشته‌شده با شناسه‌ی مخرب نباید به ایندکس (نوار کناری — data-id بدون
// escape) راه پیدا کند.
func TestConversationIndexSkipsMaliciousIDs(t *testing.T) {
	dir := t.TempDir()
	bad := `{"id": "\"><img src=x onerror=alert(1)>", "title": "evil", "messages": []}`
	if err := os.WriteFile(filepath.Join(dir, "evil.json"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	good := `{"id": "20250101-120000-abcd1234", "title": "ok", "messages": []}`
	if err := os.WriteFile(filepath.Join(dir, "good.json"), []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	idx := buildIndexFromFiles(dir)
	if len(idx.Entries) != 1 || idx.Entries[0].ID != "20250101-120000-abcd1234" {
		t.Fatalf("planted malicious conversation ID must be skipped, got %+v", idx.Entries)
	}
}
