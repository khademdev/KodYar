// رمزنگاری کلیدهای API — AES-GCM با کلید اصلی ذخیره‌شده در Keyring سیستم‌عامل.
//
// ساختار (v2): کلید اصلی تصادفی ۳۲ بایتی در Credential Manager ویندوز /
// Keychain مک / Secret Service لینوکس (سرویس «MyAI»، کاربر «settings-key»)
// نگه‌داری می‌شود و برای AES-GCM به کار می‌رود. متن رمزشده با پیشوند «v2:»
// از نسخه‌ی قدیمی (مشتق از نام کاربر+میزبان) متمایز می‌شود.
//
// مهاجرت تنبل: هنگام رمزگشایی، اگر مقدار با فرمت قدیمی بود و با کلید قدیمی
// باز شد، همان‌جا با کلید جدید رمز و ذخیره می‌شود (در decryptKey).
// اگر Keyring در دسترس نباشد (مثلاً لینوکسِ بدون dbus)، به روش قدیمی
// برمی‌گردیم و یک‌بار در لاگ خطاها هشدار دیده می‌شود — استارتاپ هرگز
// به‌خاطر Keyring نمی‌شکند.
//
// در تست‌ها keyringBackend (اینترفیس secretStore) با پیاده‌سازی ساختگی
// جایگزین می‌شود — Keyring واقعی هرگز لمس نمی‌شود.
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"

	keyring "github.com/zalando/go-keyring"
)

const (
	cryptoSalt     = "MyAI::v1::local-secret" // فقط مسیر legacy
	cryptoV2Prefix = "v2:"                    // نشانگر فرمت جدید
	keyringService = "MyAI"
	keyringUser    = "settings-key"
	masterKeyLen   = 32
)

// secretStore اینترفیس Keyring — برای تزریق در تست‌ها.
type secretStore interface {
	Get(service, user string) (string, error)
	Set(service, user, pass string) error
}

// osKeyring پیاده‌سازی واقعی با go-keyring.
type osKeyring struct{}

func (osKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (osKeyring) Set(service, user, pass string) error {
	return keyring.Set(service, user, pass)
}

// keyringBackend قابل جایگزینی در تست‌ها (تست‌ها موازی نیستند).
var keyringBackend secretStore = osKeyring{}

// masterKeyState کلید اصلی کش‌شده + وضعیت legacy.
type masterKeyState struct {
	once   sync.Once
	key    []byte
	legacy bool // Keyring در دسترس نیست → مسیر قدیمی
	warned bool // هشدار «یک‌بار» ثبت شده است
}

func newMasterKeyState() *masterKeyState { return &masterKeyState{} }

// activeCrypto وضعیت فعال رمزنگاری (در تست‌ها عوض می‌شود).
var activeCrypto = newMasterKeyState()

// resolveOnce کلید اصلی را یک‌بار محاسبه می‌کند:
// از Keyring می‌خواند؛ اگر نبود، تصادفی می‌سازد و ذخیره می‌کند؛ اگر
// Keyring خطا داد → حالت legacy (کلید مشتق قدیمی).
func (m *masterKeyState) resolveOnce() {
	enc, err := keyringBackend.Get(keyringService, keyringUser)
	if err == nil && enc != "" {
		if raw, derr := base64.StdEncoding.DecodeString(enc); derr == nil && len(raw) == masterKeyLen {
			m.key = raw
			return
		}
		// مقدار خراب → مثل نبودن رفتار می‌کنیم و بازسازی می‌کنیم
	}
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		// Keyring در دسترس نیست — fallback به روش قدیمی
		m.legacy = true
		return
	}
	// تولید کلید تصادفی و ذخیره در Keyring
	fresh := make([]byte, masterKeyLen)
	if _, rerr := rand.Read(fresh); rerr != nil {
		m.legacy = true
		return
	}
	if serr := keyringBackend.Set(keyringService, keyringUser, base64.StdEncoding.EncodeToString(fresh)); serr != nil {
		m.legacy = true
		return
	}
	m.key = fresh
}

// resolve کلید اصلی (و پرچم legacy) را برمی‌گرداند — فقط یک‌بار کار می‌کند.
func (m *masterKeyState) resolve() (key []byte, legacy bool) {
	m.once.Do(m.resolveOnce)
	return m.key, m.legacy
}

// initCrypto در startup صدا زده می‌شود تا I/O مربوط به Keyring «بیرون از
// قفل‌ها» انجام شود (decryptKey بعداً فقط از کش می‌خواند). اگر Keyring
// نبود، یک هشدار یک‌بار مصرف در لاگ خطاها ثبت می‌شود.
func initCrypto() {
	_, legacy := activeCrypto.resolve()
	if legacy && !activeCrypto.warned {
		activeCrypto.warned = true
		logAppError("app", "Keyring سیستم‌عامل در دسترس نیست؛ کلیدهای API با روش قدیمی (مشتق از نام کاربر/میزبان) رمز می‌شوند — سطح محافظت پایین‌تر.")
	}
}

// resetCryptoForTest حالت رمزنگاری را برای تست بازنشانی می‌کند (فقط تست).
func resetCryptoForTest(store secretStore) func() {
	oldStore, oldCrypto := keyringBackend, activeCrypto
	keyringBackend = store
	activeCrypto = newMasterKeyState()
	return func() { keyringBackend, activeCrypto = oldStore, oldCrypto }
}

// ---------------------------------------------------------------- مسیر legacy

// deriveKey یک کلید ۳۲ بایتی از مشخصات ماشین می‌سازد (مسیر قدیمی).
func deriveKey() []byte {
	userName := ""
	if cu, err := user.Current(); err == nil {
		userName = cu.Username
	}
	host, _ := os.Hostname()
	seed := strings.Join([]string{userName, host, cryptoSalt}, "|")
	sum := sha256.Sum256([]byte(seed))
	return sum[:]
}

func newGCMWithKey(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// legacyEncryptString رمزنگاری با کلید مشتق قدیمی (همان فرمت نسخه‌های
// پیشین — بدون پیشوند) — فقط برای حالت نبودِ Keyring.
func legacyEncryptString(plain string) (string, error) {
	gcm, err := newGCMWithKey(deriveKey())
	if err != nil {
		return "", err
	}
	return sealGCM(gcm, plain)
}

// legacyDecryptString رمزگشایی با کلید مشتق قدیمی — فقط برای مهاجرت.
func legacyDecryptString(encoded string) (string, error) {
	gcm, err := newGCMWithKey(deriveKey())
	if err != nil {
		return "", err
	}
	return openGCM(gcm, encoded)
}

func sealGCM(gcm cipher.AEAD, plain string) (string, error) {
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func openGCM(gcm cipher.AEAD, encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	nonce, body := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plain), nil
}

// ---------------------------------------------------------------- API عمومی

// encryptString متن دلخواه را رمزنگاری می‌کند: با کلید Keyring (فرمت v2)
// و در نبود آن با مسیر قدیمی.
func encryptString(plain string) (string, error) {
	key, legacy := activeCrypto.resolve()
	if legacy || len(key) == 0 {
		return legacyEncryptString(plain)
	}
	gcm, err := newGCMWithKey(key)
	if err != nil {
		return "", err
	}
	sealed, err := sealGCM(gcm, plain)
	if err != nil {
		return "", err
	}
	return cryptoV2Prefix + sealed, nil
}

// decryptString متن رمزشده را باز می‌کند: v2 با کلید Keyring؛ در غیر این
// صورت مسیر legacy (کاندید مهاجرت — فراخوانی‌کننده مسئول re-encrypt است).
func decryptString(encoded string) (string, error) {
	if strings.HasPrefix(encoded, cryptoV2Prefix) {
		key, legacy := activeCrypto.resolve()
		if legacy || len(key) == 0 {
			return "", errors.New("keyring master key unavailable")
		}
		gcm, err := newGCMWithKey(key)
		if err != nil {
			return "", err
		}
		return openGCM(gcm, strings.TrimPrefix(encoded, cryptoV2Prefix))
	}
	return legacyDecryptString(encoded)
}

// isLegacyCiphertext آیا مقدار با فرمت قدیمی است (کاندید مهاجرت)؟
func isLegacyCiphertext(encoded string) bool {
	return encoded != "" && !strings.HasPrefix(encoded, cryptoV2Prefix)
}

// ---------------------------------------------------------------- نوشتن اتمیک

// atomicWriteFile نوشتن اتمیک با fsync: فایل موقت یکتا در همان پوشه →
// sync → rename. خواننده‌ی هم‌زمان هرگز نیمی‌نوشته نمی‌بیند و فایل مقصد
// هرگز truncate نمی‌شود؛ در خطا، فایل موقت پاک می‌شود و فایل قبلی دست‌نخورده
// می‌ماند. روی ویندوز renameِ گذرا (قفل ویرایشگر/آنتی‌ویروس) با تلاش محدود
// تکرار می‌شود.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := writeTempFile(path, data, perm)
	if err != nil {
		return err
	}
	if err := renameWithRetry(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// atomicWriteStream مثل atomicWriteFile برای جریان با اندازه‌ی معلوم است:
// محتوا را تکه‌تکه در فایل موقت یکتا می‌نویسد (بدون بافر کل محتوا در حافظه)
// و در پایان rename اتمیک می‌کند — برای بازگردانی فایل‌های بزرگ چک‌پوینت
// (Stage 4.3). هر خطا فقط فایل موقت را پاک می‌کند.
func atomicWriteStream(path string, r io.Reader, size int64, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	fail := func(e error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return e
	}
	if err := f.Chmod(perm); err != nil {
		return fail(err)
	}
	if _, err := io.CopyN(f, r, size); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := renameWithRetry(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// atomicWriteHook قلاب تست برای تزریق خطای نوشتن (مثلاً ENOSPC). فقط پیش از
// هم‌زمانی‌ها تنظیم شود.
var atomicWriteHook func(path string) error

// writeTempFile داده را در فایل موقتِ یکتا (پسوند .tmp — جاروی راه‌اندازی
// هر دو سبک نام را می‌شناسد) می‌نویسد و مسیر موقت را برمی‌گرداند. هیچ
// خطایی فایل مقصد را لمس نمی‌کند.
func writeTempFile(path string, data []byte, perm os.FileMode) (string, error) {
	if atomicWriteHook != nil {
		if err := atomicWriteHook(path); err != nil {
			return "", err
		}
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	fail := func(e error) (string, error) {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", e
	}
	if err := f.Chmod(perm); err != nil {
		return fail(err)
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}
