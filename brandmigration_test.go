// تست‌های رگرسیون مهاجرت نام برند (MyAI → KodYar):
//  1. انتقال یک‌بارهٔ پوشهٔ تنظیمات به نام جدید
//  2. دست‌نزدن به داده‌ها وقتی پوشهٔ جدید از قبل وجود دارد
//  3. خواندن کلید اصلی Keyring از نام سرویس قدیمی
//  4. اولویت کلید موجود زیر نام جدید بر کلید قدیمی
//
// Keyring واقعی هرگز لمس نمی‌شود (memStore تزریق می‌شود).
package main

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// writeFileAs یک فایل آزمایشی می‌نویسد (کمکی).
func writeFileAs(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestConfigDirMigratesLegacyOnce بررسی می‌کند که پوشهٔ تنظیمات قدیمی
// دقیقاً یک‌بار به نام جدید منتقل می‌شود و محتوایش سالم می‌ماند.
func TestConfigDirMigratesLegacyOnce(t *testing.T) {
	base := t.TempDir()
	legacyDir := filepath.Join(base, legacyAppDirName)
	writeFileAs(t, filepath.Join(legacyDir, "settings.json"), `{"provider":"legacy"}`)
	writeFileAs(t, filepath.Join(legacyDir, "conversations", "c1.json"), `{"id":"c1"}`)

	got, err := resolveConfigDir(base)
	if err != nil {
		t.Fatalf("resolveConfigDir: %v", err)
	}
	want := filepath.Join(base, appDirName)
	if got != want {
		t.Fatalf("config dir = %q, want %q", got, want)
	}
	// داده‌ها زیر نام جدید هستند
	if data, err := os.ReadFile(filepath.Join(want, "settings.json")); err != nil || string(data) != `{"provider":"legacy"}` {
		t.Fatalf("settings.json not migrated: %q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(want, "conversations", "c1.json")); err != nil || string(data) != `{"id":"c1"}` {
		t.Fatalf("conversation not migrated: %q %v", data, err)
	}
	// پوشهٔ قدیمی دیگر وجود ندارد (منتقل شده، نه کپی)
	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Fatalf("legacy dir must be gone after migration, stat err = %v", err)
	}

	// فراخوانی دوم: idempotent — چیزی جابه‌جا یا بازنویسی نمی‌شود
	writeFileAs(t, filepath.Join(want, "settings.json"), `{"provider":"after"}`)
	got2, err := resolveConfigDir(base)
	if err != nil {
		t.Fatalf("second resolveConfigDir: %v", err)
	}
	if got2 != want {
		t.Fatalf("second call dir = %q, want %q", got2, want)
	}
	if data, _ := os.ReadFile(filepath.Join(want, "settings.json")); string(data) != `{"provider":"after"}` {
		t.Fatalf("second call must not touch existing data: %q", data)
	}
	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Fatalf("second call must not recreate legacy dir, stat err = %v", err)
	}
}

// TestConfigDirKeepsExistingNewDir بررسی می‌کند وقتی پوشهٔ جدید از قبل
// وجود دارد، هیچ چیزی از پوشهٔ قدیمی جابه‌جا یا کپی نمی‌شود.
func TestConfigDirKeepsExistingNewDir(t *testing.T) {
	base := t.TempDir()
	newDir := filepath.Join(base, appDirName)
	legacyDir := filepath.Join(base, legacyAppDirName)
	writeFileAs(t, filepath.Join(newDir, "settings.json"), `{"provider":"new"}`)
	writeFileAs(t, filepath.Join(legacyDir, "settings.json"), `{"provider":"legacy"}`)
	writeFileAs(t, filepath.Join(legacyDir, "only-legacy.txt"), "legacy data")

	got, err := resolveConfigDir(base)
	if err != nil {
		t.Fatalf("resolveConfigDir: %v", err)
	}
	if got != newDir {
		t.Fatalf("config dir = %q, want %q", got, newDir)
	}
	// دادهٔ جدید دست‌نخورده مانده
	if data, _ := os.ReadFile(filepath.Join(newDir, "settings.json")); string(data) != `{"provider":"new"}` {
		t.Fatalf("existing new data was overwritten: %q", data)
	}
	// پوشه و دادهٔ قدیمی سرِ جای خود مانده (حذف یا انتقال نشده)
	if _, err := os.Stat(filepath.Join(legacyDir, "settings.json")); err != nil {
		t.Fatalf("legacy dir must be untouched: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(legacyDir, "only-legacy.txt")); string(data) != "legacy data" {
		t.Fatalf("legacy data changed: %q", data)
	}
	// هیچ فایلی از پوشهٔ قدیمی به پوشهٔ جدید کپی نشده
	if _, err := os.Stat(filepath.Join(newDir, "only-legacy.txt")); !os.IsNotExist(err) {
		t.Fatal("legacy files must not be copied into the existing new dir")
	}
}

// freshMasterKey یک کلید اصلی ۳۲ بایتی تصادفی + نمایش base64 آن می‌سازد.
func freshMasterKey(t *testing.T) ([]byte, string) {
	t.Helper()
	key := make([]byte, masterKeyLen)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key, base64.StdEncoding.EncodeToString(key)
}

// TestCryptoMigratesLegacyKeyringService بررسی می‌کند کلید اصلی‌ای که
// نسخه‌های پیشین زیر نام سرویس قدیمی ذخیره کرده‌اند خوانده و به نام
// جدید کپی می‌شود؛ بنابراین کلیدهای API رمزشدهٔ قدیمی باز می‌شوند.
func TestCryptoMigratesLegacyKeyringService(t *testing.T) {
	store := &memStore{}
	key, encOld := freshMasterKey(t)
	if err := store.Set(legacyKeyringService, keyringUser, encOld); err != nil {
		t.Fatal(err)
	}
	restore := resetCryptoForTest(store)
	defer restore()

	// ciphertext v2 که نسخهٔ قبلی با همین کلید ساخته بود
	gcm, err := newGCMWithKey(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealGCM(gcm, "sk-old-provider")
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := decryptString(cryptoV2Prefix + sealed); err != nil || plain != "sk-old-provider" {
		t.Fatalf("old ciphertext must still open after rename: %q %v", plain, err)
	}

	// کلید قدیمی زیر نام جدید کپی شده و مقدارش عوض نشده
	if got := store.data[keyringService+"/"+keyringUser]; got != encOld {
		t.Fatalf("master key not copied to new service name: %q (want %q)", got, encOld)
	}

	// رمزنگاری جدید هم با همان کلید ادامه می‌یابد (decrypt با کلید یکسان)
	enc, err := encryptString("sk-new-provider")
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := decryptString(enc); err != nil || plain != "sk-new-provider" {
		t.Fatalf("round trip after migration failed: %q %v", plain, err)
	}
}

// TestCryptoKeepsNewKeyringKeyOverLegacy بررسی می‌کند اگر کلید زیر نام
// جدید از قبل وجود داشته باشد، همان استفاده می‌شود و کلید قدیمی
// جایگزینش نمی‌شود.
func TestCryptoKeepsNewKeyringKeyOverLegacy(t *testing.T) {
	store := &memStore{}
	newKey, encNew := freshMasterKey(t)
	_, encOld := freshMasterKey(t)
	if encNew == encOld {
		t.Fatal("test keys must differ")
	}
	if err := store.Set(keyringService, keyringUser, encNew); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(legacyKeyringService, keyringUser, encOld); err != nil {
		t.Fatal(err)
	}
	restore := resetCryptoForTest(store)
	defer restore()

	// ciphertext ساخته‌شده با کلید جدید باید باز شود …
	gcm, err := newGCMWithKey(newKey)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealGCM(gcm, "sk-new")
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := decryptString(cryptoV2Prefix + sealed); err != nil || plain != "sk-new" {
		t.Fatalf("new master key must win: %q %v", plain, err)
	}
	// … و کلید موجود زیر نام جدید بازنویسی نشده
	if got := store.data[keyringService+"/"+keyringUser]; got != encNew {
		t.Fatalf("existing new key was overwritten: %q", got)
	}
}
