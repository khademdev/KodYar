//go:build !windows

// کشتن درخت فرایند در یونیکس: گروه فرایند مستقل (Setpgid) و سیگنال به
// کل گروه — SIGTERM اول، SIGKILL بعد از مهلت کوتاه (در runShell).
package main

import (
	"os/exec"
	"sync"
	"syscall"
)

// procTree هندل درخت فرایند یونیکس (pgid).
// pgid پس از Start نوشته می‌شود و ممکن است هم‌زمان از goroutine لغوِ exec
// (cmd.Cancel) خوانده شود → همه‌ی دسترسی‌ها زیر mu.
type procTree struct {
	mu   sync.Mutex
	pgid int
}

// prepareProcessTree پیش از Start صدا زده می‌شود: فرایند در گروه خودش
// (Setpgid) قرار می‌گیرد تا بتوان با kill(-pgid) کل درخت را سیگنال داد.
func prepareProcessTree(cmd *exec.Cmd) (*procTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &procTree{}, nil
}

// attach پس از Start: pgid = pid خود فرایند (راهبر گروه).
func (t *procTree) attach(cmd *exec.Cmd) error {
	t.mu.Lock()
	t.pgid = cmd.Process.Pid
	t.mu.Unlock()
	return nil
}

// signalGroup سیگنال را به کل گروه فرایند می‌فرستد — با گارد ESRCH (F4):
// اگر گروه خالی است (هیچ عضوی زنده نیست) هیچ سیگنالی فرستاده نمی‌شود تا
// اگر سیستم آن pgid را به فرایند/گروهِ بی‌ربطِ تازه‌ای داده باشد، هدف
// نگرفته شود. پنجره‌ی خطر از «مقیاس ثانیه» (مهلت SIGTERM تا SIGKILL)
// به «فاصله‌ی میکروثانیه‌ایِ بین دو syscall» می‌رسد — باقی TOCTOU ذاتیِ
// POSIX است و به‌عنوان ریسک پذیرفته‌شده ثبت شده.
// خطای مجوز (EPERM) → سیگنال واقعی همچنان تلاش می‌شود (مثل قبل).
// بازگشت: آیا تلاش ارسال سیگنال انجام شد؟
func signalGroup(pgid int, sig syscall.Signal) bool {
	if pgid == 0 {
		return false
	}
	if err := syscall.Kill(-pgid, 0); err == syscall.ESRCH {
		return false // گروه خالی — هیچ چیزی برای کشتن نیست
	}
	_ = syscall.Kill(-pgid, sig)
	return true
}

// terminate سیگنال TERM به کل گروه فرایند.
func (t *procTree) terminate() {
	t.mu.Lock()
	pgid := t.pgid
	t.mu.Unlock()
	signalGroup(pgid, syscall.SIGTERM)
}

// kill سیگنال KILL به کل گروه فرایند.
func (t *procTree) kill() {
	t.mu.Lock()
	pgid := t.pgid
	t.mu.Unlock()
	signalGroup(pgid, syscall.SIGKILL)
}

// close در یونیکس کاری ندارد.
func (t *procTree) close() {}
