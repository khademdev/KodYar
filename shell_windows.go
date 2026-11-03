//go:build windows

// کشتن درخت فرایند در ویندوز: Job Object با JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE.
// با بسته‌شدن هندل Job (پایان runShell) یا TerminateJobObject (timeout/لغو)
// کل درخت فرایند — shell و همه‌ی فرزندانش — خاتمه می‌یابند.
// اگر ساخت Job ممکن نبود، به taskkill /T /F /PID برمی‌گردیم.
package main

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procTree هندل درخت فرایند ویندوز (Job Object یا taskkill-fallback).
// job/useTaskkill پیش از Start (فقط‌نوشتن) تنظیم می‌شوند؛ pid پس از Start
// نوشته می‌شود و ممکن است هم‌زمان از goroutine لغوِ exec خوانده شود →
// دسترسی‌ها زیر mu.
type procTree struct {
	mu          sync.Mutex
	job         windows.Handle
	pid         int
	useTaskkill bool
}

// prepareProcessTree پیش از Start صدا زده می‌شود: Job Object می‌سازد.
// اگر شکست خورد، fallback با taskkill فعال می‌شود (خطا برنمی‌گرداند تا
// اجرای دستور هرگز به‌خاطر Job نبودن از کار نیفتد).
func prepareProcessTree(cmd *exec.Cmd) (*procTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return &procTree{useTaskkill: true}, nil
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, serr := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if serr != nil {
		_ = windows.CloseHandle(job)
		return &procTree{useTaskkill: true}, nil
	}
	return &procTree{job: job}, nil
}

// attach پس از Start: فرایند به Job ملحق می‌شود.
// هندل فرایند از OpenProcess گرفته می‌شود (os.Process هندل را export نمی‌کند).
func (t *procTree) attach(cmd *exec.Cmd) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.useTaskkill {
		t.pid = cmd.Process.Pid
		return nil
	}
	if t.job == 0 {
		return fmt.Errorf("job object not initialized")
	}
	const access = windows.PROCESS_SET_QUOTA | windows.PROCESS_TERMINATE
	h, err := windows.OpenProcess(access, false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("open process: %w", err)
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(t.job, h)
}

// terminate خاتمه‌ی درخت: TerminateJobObject (یا taskkill /T /F).
// در ویندوز خاتمه‌ی نرم مفهوم هم‌ارز ندارد؛ همان خاتمه‌ی قطعی انجام می‌شود.
func (t *procTree) terminate() {
	t.kill()
}

// kill خاتمه‌ی قطعی درخت فرایند.
func (t *procTree) kill() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.useTaskkill {
		if t.pid != 0 {
			// بدون shell و با argv مستقیم — بدون تزریق
			c := exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprint(t.pid))
			c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
			_ = c.Run()
		}
		return
	}
	if t.job != 0 {
		_ = windows.TerminateJobObject(t.job, 1)
	}
}

// close بستن هندل Job — با KILL_ON_JOB_CLOSE فرزندانِ جامانده هم می‌میرند.
func (t *procTree) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job != 0 {
		_ = windows.CloseHandle(t.job)
		t.job = 0
	}
}
