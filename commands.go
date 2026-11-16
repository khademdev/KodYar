// اجرای دستور در پوشه‌ی کاری — فقط وقتی کاربر اجازه داده باشد.
//
// امنیت: دستورها در پوشه‌ی کاری اجرا می‌شوند و از همان دروازه‌ی دسترسی
// run_command ایجنت می‌گذرند (لیست سیاه سخت → قاعده‌های Permissions →
// الگوهای پرریسک/لیست‌سفید امن) — این متد دیگر مسیر دورزدن نیست.
// اجرای واقعی از طریق runShell (کشتن درخت فرایند + خروجی کران‌دار) است.
package main

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"time"
)

const commandTimeout = 60 * time.Second

// RunCommand یک دستور shell را در پوشه‌ی کاری اجرا می‌کند.
//
// context پایه = context عمر برنامه (startup وایلز) — نه context.Background —
// تا بستن برنامه، تأییدهای در انتظار و دستورهای جاری را هم قطع کند؛ در
// تست‌ها که startup اجرا نشده و a.ctx صفر است، به context.Background
// برمی‌گردیم. مهلت ۶۰ ثانیه‌ی دستور مثل قبل روی همین پایه سوار می‌شود.
func (a *App) RunCommand(command string) (string, error) {
	defer a.recoverBound("RunCommand")
	base := a.appContext()
	command = strings.TrimSpace(command)
	if command == "" {
		return "", errors.New("دستور خالی است")
	}

	a.mu.Lock()
	allowed := a.settings.AllowCommands
	dir := a.settings.WorkspacePath
	a.mu.Unlock()

	if !allowed {
		return "", errors.New("اجازه‌ی اجرای دستور غیرفعال است؛ از تنظیمات یا سایدبار فعالش کنید")
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", errors.New("مسیر پوشه‌ی کاری تنظیم نشده است؛ ابتدا آن را در تنظیمات مشخص کنید")
	}

	// دروازه‌ی دسترسی — همان دروازه‌ی ابزار run_command ایجنت
	// دروازه‌ی دسترسی — همان دروازه‌ی ابزار run_command ایجنت (پیام رد فارسی)
	if ok, why := a.gateCommand(base, command, false, "اجازه‌ی اجرای این دستور داده نشد"); !ok {
		return "", errors.New(strings.TrimPrefix(why, "ERROR: "))
	}

	ctx, cancel := context.WithTimeout(base, commandTimeout)
	defer cancel()

	out, _, err := runShellWithPolicy(ctx, dir, command, commandTimeout, nil, a.sandboxPolicyForCommand(dir, command))
	result := strings.TrimRight(out, "\n")
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return result, errors.New("دستور بیش از حد مجاز طول کشید (۶۰ ثانیه)")
		}
		return result, errors.New(err.Error())
	}
	return result, nil
}

func isWindows() bool {
	return runtime.GOOS == "windows"
}

// appContext context عمر برنامه را برمی‌گرداند (اگر startup وایلز اجرا
// شده باشد) و در نبودش (تست/هدلس) context.Background — خواندن بدون قفل
// امن است: a.ctx فقط یک‌بار در startup نوشته می‌شود.
func (a *App) appContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}
