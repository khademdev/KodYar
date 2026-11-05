//go:build windows

// تغییر نام فایل موقت روی فایل مقصد در ویندوز می‌تواند گذرا شکست بخورد:
// ویرایشگر، آنتی‌ویروس، ایندکسر یا ابزار بکاپ ممکن است فایل مقصد را
// لحظه‌ای باز نگه داشته باشند (ERROR_ACCESS_DENIED / SHARING_VIOLATION /
// LOCK_VIOLATION). این فایل همان rename را با تلاش محدود تکرار می‌کند.
package main

import (
	"errors"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// renameRetryDelays فاصله‌ی تلاش‌ها: ۲۵ → ۵۰ → ۱۰۰ → ۲۰۰ → ۴۰۰ میلی‌ثانیه
// (۶ تلاش در مجموع، حداکثر ~۰.۸ ثانیه).
var renameRetryDelays = []time.Duration{
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	200 * time.Millisecond,
	400 * time.Millisecond,
}

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

// renameWithRetry تغییر نام را با تلاش محدود انجام می‌دهد؛ خطای نهایی شامل
// نام فایل است.
func renameWithRetry(from, to string) error {
	var err error
	for attempt := 0; ; attempt++ {
		err = os.Rename(from, to)
		if err == nil {
			return nil
		}
		if !isTransientRenameError(err) || attempt >= len(renameRetryDelays) {
			return err
		}
		time.Sleep(renameRetryDelays[attempt])
	}
}
