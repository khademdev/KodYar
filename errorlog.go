// لاگ شفاف خطاها — همه‌ی خطاهای مهم (ذخیره، شبکه، ابزار، برگرداندن)
// با مُهر زمانی در فایل errors.log داخل پوشه‌ی config ثبت می‌شوند و
// کاربر می‌تواند از تنظیمات ← «لاگ خطاها» آن‌ها را ببیند، کپی کند یا پاک کند.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// سقف حجم لاگ — برای جلوگیری از رشد بی‌رویه، قدیمی‌ها حذف می‌شوند.
const errorLogMaxBytes = 128 * 1024

var (
	errLogMu   sync.Mutex // محافظ نوشتن/چرخش فایل لاگ
	errLogPath string     // مسیر کش‌شده (یک‌بار محاسبه می‌شود)
)

// resolveErrorLogPath مسیر فایل لاگ را برمی‌گرداند (بدون قفل — از بیرونِ
// errLogMu صدا زده می‌شود تا خودقفل‌گیری رخ ندهد). appForLogs برای
// فراخوانی‌های بدون گیرنده App در دسترس است.
func resolveErrorLogPath(a *App) (string, error) {
	errLogMu.Lock()
	cached := errLogPath
	errLogMu.Unlock()
	if cached != "" {
		// اگر پوشه‌ی کش‌شده دیگر وجود ندارد (کاربر حذف کرده یا پوشه‌ی
		// موقت تست پاک شده)، کش باطل و مسیر تازه حل می‌شود؛ وگرنه لاگ
		// بی‌صدا از دست می‌رفت.
		if _, statErr := os.Stat(filepath.Dir(cached)); statErr == nil {
			return cached, nil
		}
		errLogMu.Lock()
		if errLogPath == cached {
			errLogPath = ""
		}
		errLogMu.Unlock()
	}
	var app *App = a
	if app == nil {
		app = appForLogs
	}
	if app == nil {
		return "", fmt.Errorf("app not ready")
	}
	dir, err := app.configDir()
	if err != nil {
		return "", err
	}
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return "", mkErr
	}
	p := filepath.Join(dir, "errors.log")
	errLogMu.Lock()
	errLogPath = p
	errLogMu.Unlock()
	return p, nil
}

// logAppError یک رخداد خطا را با مُهر زمانی به لاگ اضافه می‌کند.
// kind دسته‌ی خطا است: chat | save | tool | revert | app
// هرگز panic/error برنمی‌گرداند — لاگ‌گرفتن نباید خودش منشأ خطا شود.
func logAppError(kind, details string) {
	logAppErrorFor(nil, kind, details)
}

// logAppErrorFor نسخه‌ای با دسترسی به App برای پیدا کردن مسیر لاگ.
func logAppErrorFor(a *App, kind, details string) {
	defer func() { _ = recover() }() // دفاع نهایی: لاگ هرگز برنامه را نکشد

	now := time.Now()
	line := fmt.Sprintf("[%s] [%s] %s\n",
		now.Format("2006-01-02 15:04:05"), kind, strings.TrimSpace(details))
	if len(line) > 4000 {
		line = cutUTF8(line, 4000) + "\n"
	}

	p, err := resolveErrorLogPath(a)
	if err != nil || p == "" {
		return // nowhere to log — نادیده گرفته می‌شود
	}

	errLogMu.Lock()
	defer errLogMu.Unlock()

	// چرخش ساده: اگر از سقف گذشت، نیمه‌ی جدید (انتهای فایل) نگه داشته می‌شود
	// و بخش قدیمی دور ریخته می‌شود تا خطاهای اخیر در دسترس بمانند.
	if info, statErr := os.Stat(p); statErr == nil && info.Size() > errorLogMaxBytes {
		if kept := int(info.Size() / 2); kept > 0 {
			if raw, readErr := os.ReadFile(p); readErr == nil {
				// نگه‌داشتن انتهای فایل؛ برش در مرز کاراکتر UTF-8 و اولین خط کامل
				tail := string(raw)
				if len(tail) > kept {
					tail = tail[len(tail)-kept:]
				}
				// برش بایتی ممکن است وسط خط (یا کاراکتر) بیفتد؛
				// کمی عقب می‌رویم تا از شروع یک خط کامل کنیم.
				if idx := strings.IndexByte(tail, '\n'); idx >= 0 {
					tail = tail[idx+1:]
				}
				_ = os.WriteFile(p, []byte(tail), 0o600)
			}
		}
	}

	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

// appForLogs نمونه‌ی فعلی App برای مسیر لاگ در فراخوانی‌های بدون گیرنده.
var appForLogs *App

// errorLogPath متد عمومی برای گرفتن مسیر لاگ.
func (a *App) errorLogPath() (string, error) {
	return resolveErrorLogPath(a)
}

// GetErrorLog محتوای لاگ خطاها را (حداکثر آخرین ۳۲KB) برمی‌گرداند.
func (a *App) GetErrorLog() (string, error) {
	defer a.recoverBound("GetErrorLog")
	p, err := resolveErrorLogPath(a)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // خالی — هنوز خطایی ثبت نشده
		}
		return "", err
	}
	text := string(raw)
	if len(text) > 32*1024 {
		// فقط آخرین ۳۲KB نگه داشته می‌شود (قدیمی‌ها حذف می‌شوند)؛
		// شروع از مرز کاراکتر UTF-8 تا حرف فارسی نیمه نماند.
		start := len(text) - 32*1024
		for start > 0 && !utf8.RuneStart(text[start]) {
			start--
		}
		text = "… (قدیمی‌ها حذف شدند)\n" + text[start:]
		// خط اول نیمه‌بریده را دور بریزیم تا شروع تمیز باشد
		if idx := strings.IndexByte(text, '\n'); idx >= 0 {
			text = text[idx+1:]
		}
	}
	return text, nil
}

// ClearErrorLog فایل لاگ را پاک می‌کند.
func (a *App) ClearErrorLog() error {
	defer a.recoverBound("ClearErrorLog")
	p, err := resolveErrorLogPath(a)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
