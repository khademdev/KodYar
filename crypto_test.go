// تست‌های رمزنگاری کلیدها — رفت رفت v2 (Keyring)، مهاجرت از فرمت قدیمی،
// ciphertext دستکاری‌شده، fallback بدون Keyring و نوشتن اتمیک.
// Keyring واقعی هرگز لمس نمی‌شود (store ساختگی تزریق می‌شود).
package main

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	keyring "github.com/zalando/go-keyring"
)

// memStore پیاده‌سازی ساختگی secretStore (فقط در حافظه).
type memStore struct {
	data map[string]string
}

func (m *memStore) Get(service, user string) (string, error) {
	if m.data == nil {
		m.data = map[string]string{}
	}
	v, ok := m.data[service+"/"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (m *memStore) Set(service, user, pass string) error {
	if m.data == nil {
		m.data = map[string]string{}
	}
	m.data[service+"/"+user] = pass
	return nil
}

// failingStore همیشه خطا می‌دهد → شبیه‌سازی «Keyring در دسترس نیست».
type failingStore struct{}

func (failingStore) Get(service, user string) (string, error) {
	return "", errors.New("secret service unavailable")
}
func (failingStore) Set(service, user, pass string) error {
	return errors.New("secret service unavailable")
}

func TestCryptoV2RoundTrip(t *testing.T) {
	restore := resetCryptoForTest(&memStore{})
	defer restore()

	enc, err := encryptString("sk-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, cryptoV2Prefix) {
		t.Fatalf("must be v2 format: %q", enc)
	}
	plain, err := decryptString(enc)
	if err != nil || plain != "sk-test-secret" {
		t.Fatalf("round trip failed: %q %v", plain, err)
	}
	// فرمت جدید با کلید Keyring باز می‌شود؛ مسیر legacy نه
	if _, err := legacyDecryptString(enc); err == nil {
		t.Fatal("v2 ciphertext must NOT open with the legacy key")
	}
}

func TestCryptoLegacyMigration(t *testing.T) {
	restore := resetCryptoForTest(&memStore{})
	defer restore()

	legacy, err := legacyEncryptString("old-api-key")
	if err != nil {
		t.Fatal(err)
	}
	if isLegacyCiphertext(legacy) != true {
		t.Fatal("legacy ciphertext flag wrong")
	}
	// مسیر legacy هنوز باز می‌کند (برای مهاجرت)
	plain, err := decryptString(legacy)
	if err != nil || plain != "old-api-key" {
		t.Fatalf("legacy decrypt failed: %q %v", plain, err)
	}

	// چرخه‌ی کامل مهاجرت از طریق decryptKey (مثل prepareStream زیر a.mu)
	a := newTestApp(t)
	a.settings.APIKeys = map[string]string{"p1": legacy}
	a.mu.Lock()
	got := a.decryptKey("p1")
	a.mu.Unlock()
	if got != "old-api-key" {
		t.Fatalf("decryptKey value wrong: %q", got)
	}
	migrated := a.settings.APIKeys["p1"]
	if !strings.HasPrefix(migrated, cryptoV2Prefix) {
		t.Fatalf("key not migrated to v2: %q", migrated)
	}
	if v, err := decryptString(migrated); err != nil || v != "old-api-key" {
		t.Fatalf("migrated key does not decrypt: %q %v", v, err)
	}
	// روی دیسک هم v2 ذخیره شده
	a2 := NewApp()
	if err := a2.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if enc := a2.settings.APIKeys["p1"]; !strings.HasPrefix(enc, cryptoV2Prefix) {
		t.Fatalf("disk copy not migrated: %q", enc)
	}
}

func TestCryptoTamperedRejected(t *testing.T) {
	restore := resetCryptoForTest(&memStore{})
	defer restore()

	enc, err := encryptString("sk-secret")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(enc, cryptoV2Prefix))
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xFF // دستکاری بایت آخر
	tampered := cryptoV2Prefix + base64.StdEncoding.EncodeToString(raw)
	if _, err := decryptString(tampered); err == nil {
		t.Fatal("tampered ciphertext must be rejected (GCM auth)")
	}
}

func TestCryptoKeyringUnavailableFallback(t *testing.T) {
	a := newTestApp(t)
	oldAppForLogs := appForLogs
	appForLogs = a
	// کش سراسری مسیر لاگ باید هم قبل و هم بعد بازنشانی شود تا تست‌های
	// بعدی به پوشه‌ی موقتیِ (حذف‌شده‌ی) این تست ننویسند.
	resetErrLogPathCache()
	defer func() {
		appForLogs = oldAppForLogs
		resetErrLogPathCache()
	}()

	restore := resetCryptoForTest(failingStore{})
	defer restore()

	// اولین initCrypto → هشدار یک‌بار در لاگ خطاها
	initCrypto()
	enc, err := encryptString("legacy-mode")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(enc, cryptoV2Prefix) {
		t.Fatal("without keyring the legacy format (no prefix) must be used")
	}
	if v, err := decryptString(enc); err != nil || v != "legacy-mode" {
		t.Fatalf("legacy fallback round trip failed: %q %v", v, err)
	}
	// فرمت قدیمی همچنان با decryptString باز می‌شود (سازگاری داده)
	if isLegacyCiphertext(enc) != true {
		t.Fatal("must be flagged legacy")
	}

	log1, _ := a.GetErrorLog()
	if n := strings.Count(log1, "Keyring"); n != 1 {
		t.Fatalf("exactly one keyring warning expected, got %d", n)
	}
	// فراخوانی دوم نباید هشدار تکراری بدهد
	initCrypto()
	log2, _ := a.GetErrorLog()
	if strings.Count(log2, "Keyring") != 1 {
		t.Fatalf("warning must be logged only once, log: %q", log2)
	}
}

func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if err := atomicWriteFile(p, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil || string(data) != `{"a":1}` {
		t.Fatalf("content wrong: %q %v", data, err)
	}
	// فایل موقت باقی نمانده
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file left behind")
	}
	// بازنویسی موفق روی فایل موجود
	if err := atomicWriteFile(p, []byte(`{"a":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(p); string(data) != `{"a":2}` {
		t.Fatal("overwrite failed")
	}
}

// resetErrLogPathCache کش سراسری مسیر لاگ خطا را خالی می‌کند (فقط تست).
func resetErrLogPathCache() {
	errLogMu.Lock()
	errLogPath = ""
	errLogMu.Unlock()
}
