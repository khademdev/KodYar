//go:build windows

// تغییر نام فایل موقت روی فایل مقصد در ویندوز می‌تواند گذرا شکست بخورد:
// ویرایشگر، آنتی‌ویروس، ایندکسر یا ابزار بکاپ ممکن است فایل مقصد را
// لحظه‌ای باز نگه داشته باشند (ERROR_ACCESS_DENIED / SHARING_VIOLATION /
// LOCK_VIOLATION). این فایل همان rename را با تلاش محدود تکرار می‌کند.
//
// نکته‌ی مهم (رفع باگ هم‌زمانی): وقتی چند نویسنده هم‌زمان مقصد یکسانی را
// replace می‌کنند، MoveFileEx می‌تواند در هر تلاش ERROR_ACCESS_DENIED بدهد و
// با فاصله‌های ثابت، همه‌ی نویسنده‌ها در یک لحظه دوباره تلاش می‌کنند
// (thundering herd) و بودجه‌ی تلاش‌ها می‌سوزد. حالا فاصله‌ها نمایی و همراهِ
// jitter است و کل تلاش‌ها یک بودجه‌ی زمانی دارند — هم در رقابت سنگین
// (تست هم‌زمانی) و هم در قفلِ گذرای آنتی‌ویروس کار می‌کند.
package main

import (
	"errors"
	"math/rand"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// renameRetryBudget سقف کل زمان تلاش مجدد برای یک rename.
const renameRetryBudget = 2 * time.Second

// isTransientRenameError آیا خطای rename گذرا و قابل‌تلاش‌مجدد است؟
func isTransientRenameError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, syscall.Errno(32)) || // sharing violation
		errors.Is(err, syscall.Errno(33)) || // lock violation
		errors.Is(err, syscall.Errno(5)) // access denied
}

// renameWithRetry تغییر نام را با تلاش محدود و فاصله‌ی نماییِ jitterدار
// انجام می‌دهد تا نویسنده‌های هم‌زمان روی هم نیفتند؛ خطای نهایی شامل نام
// فایل است.
func renameWithRetry(from, to string) error {
	deadline := time.Now().Add(renameRetryBudget)
	delay := 5 * time.Millisecond
	var err error
	for {
		err = os.Rename(from, to)
		if err == nil {
			return nil
		}
		if !isTransientRenameError(err) {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return err
		}
		// jitter: نصف تا یک‌ونیمِ تأخیر فعلی — تلاش‌های هم‌زمان پخش می‌شوند.
		sleep := delay/2 + time.Duration(rand.Int63n(int64(delay)+1))
		if sleep > remaining {
			sleep = remaining
		}
		time.Sleep(sleep)
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
}
