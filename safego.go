// جداسازی خرابی — هیچ panic ای نباید برنامه را بکشد.
//
// در Go یک panic مهارنشده در هر goroutine کل فرایند را پایان می‌دهد (از جمله
// کار ذخیره‌نشده‌ی کاربر در پنجره‌ی چت). این فایل تنها نقطه‌ی مهار است:
//   - safeGo: اجرای goroutine با مهار panic؛ خطا + پشته‌ی کران‌دار در لاگ خطاها.
//   - callGuarded: بخش همگام با برگرداندن خطا به‌جای مرگ.
//   - recoverBound / recoverBoundErr: برای متدهای متصل به Wails (فرانت‌اند) —
//     برنامه زنده می‌ماند و در صورت امکان خطای فارسی برمی‌گردد.
//
// هیچ‌جا محتوای فایل یا کلید/رمز لاگ نمی‌شود؛ فقط پیام panic و پشته (کران‌دار).
package main

import (
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
)

// panicStackMaxBytes سقف پشته‌ی ثبت‌شده در لاگ خطاها (بدون رشد بی‌رویه).
const panicStackMaxBytes = 8 * 1024

// errInternalUI خطای فارسیِ قابل‌نمایش به کاربر برای خرابی داخلی.
const errInternalUI = "خطای داخلی رخ داد؛ جزئیات در لاگ خطاها ثبت شد."

// errInternalTool خطای انگلیسیِ نتیجه‌ی ابزار (متن ارسالی به مدل).
const errInternalTool = "ERROR: internal error in tool %s (see the error log)."

// panicRecordHook قلاب تست: اگر تنظیم شود، هر panic مهارشده یک‌بار به آن
// گزارش می‌شود (شمارش قطعی در تست‌ها). فقط پیش از هم‌زمانی‌ها تنظیم شود.
var panicRecordHook func(where string, value interface{})

// recordPanic پیام panic و پشته‌ی کران‌دار را یک‌بار در لاگ خطاها ثبت می‌کند.
// خودش هرگز panic نمی‌کند (لاگ‌گرفتن نباید منشأ خرابی شود).
func recordPanic(a *App, where string, value interface{}) {
	defer func() { _ = recover() }()
	if panicRecordHook != nil {
		panicRecordHook(where, value)
	}
	stack := string(debug.Stack())
	if len(stack) > panicStackMaxBytes {
		// برش در مرز کاراکتر UTF-8 تا لاگ خراب نشود.
		stack = cutUTF8(stack, panicStackMaxBytes)
	}
	var sb strings.Builder
	sb.WriteString(where)
	sb.WriteString(": panic: ")
	sb.WriteString(fmt.Sprint(value))
	sb.WriteString("\n")
	sb.WriteString(stack)
	logAppErrorFor(a, "panic", sb.String())
}

// recoverPanic برای استفاده در defer: panic را مهار و ثبت می‌کند.
// در حالت عادی (بدون panic) هیچ کاری نمی‌کند.
func recoverPanic(a *App, where string) {
	if r := recover(); r != nil {
		recordPanic(a, where, r)
	}
}

// safeGo goroutine را با مهار panic اجرا می‌کند؛ a می‌تواند nil باشد
// (مسیر لاگ از appForLogs استفاده می‌کند).
//
// توجه: اگر goroutine پس از panic باید وضعیتی را بازگرداند (کانال/پرچم)،
// همان بازگردانی را داخل fn و با defer خودش انجام دهید — recoverPanic فقط
// زنده‌ماندن برنامه را تضمین می‌کند.
func safeGo(a *App, where string, fn func()) {
	go func() {
		defer recoverPanic(a, where)
		fn()
	}()
}

// callGuarded یک بخش همگام را اجرا می‌کند؛ panic به خطا تبدیل می‌شود تا
// فراخواننده مسیر خطای عادی را طی کند.
func callGuarded(a *App, where string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			recordPanic(a, where, r)
			err = fmt.Errorf("internal error in %s (see the error log)", where)
		}
	}()
	return fn()
}

// recoverBound محافظ متدهای متصل به Wails که مقدار خطا برنمی‌گردانند:
// panic ثبت می‌شود و برنامه زنده می‌ماند (مقادیر صفر برگردانده می‌شوند).
func (a *App) recoverBound(where string) {
	if r := recover(); r != nil {
		recordPanic(a, where, r)
	}
}

// recoverBoundErr محافظ متدهای متصل به Wails با خروجی error: panic ثبت و
// خطای فارسی برگردانده می‌شود (خطای موجود قبلی دست‌نخورده می‌ماند).
func (a *App) recoverBoundErr(where string, errp *error) {
	if r := recover(); r != nil {
		recordPanic(a, where, r)
		if errp != nil && *errp == nil {
			*errp = errors.New(errInternalUI)
		}
	}
}
