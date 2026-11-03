//go:build !windows

// تست‌های رگرسیون — بخش F4: گارد ESRCH در کشتن گروه فرایند یونیکس.
//
// پیش از اصلاح، terminate/kill بی‌شرط به -pgid سیگنال می‌زد؛ اگر همه‌ی
// اعضای گروه مرده بودند و سیستم pgid را به فرایند بی‌ربطی داده بود،
// سیگنال آن فرایند را می‌کشت. اکنون signalGroup اول با سیگنال ۰ زنده‌بودن
// گروه را می‌سنجد و فقط در صورت وجود عضو، سیگنال واقعی می‌فرستد.
//
// (این فایل فقط یونیکس — به syscall.Setpgid و سیگنال‌ها وابسته است.
// رگرسیون یکپارچه‌ی «کشتن کامل درخت فرزند» از قبل در
// TestRunShellTimeoutKillsChildTree پوشش شده و نباید با گارد جدید بشکند.)
package main

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestSignalGroupLiveGroupGetsSignaled گروه زنده: سیگنال واقعاً فرستاده
// می‌شود (بازگشت true) و فرایند گروه‌راهبر می‌میرد.
func TestSignalGroupLiveGroupGetsSignaled(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	cmd := exec.Command("sh", "-c", "sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := cmd.Process.Pid
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	if !signalGroup(pgid, syscall.SIGKILL) {
		t.Fatal("live group must be signaled (return true)")
	}

	// Wait = reap؛ فرایندِ با سیگنال کشته‌شده *ExitError می‌دهد (منتظره).
	// تا reap نشود، زامبی هسته هنوز /proc دارد و processAlive true می‌دهد.
	_ = cmd.Wait()

	// پس از reap، فرایند باید برای همیشه رفته باشد
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pgid) {
			return // ✓ سیگنال واقعاً به گروه زنده رسید و کار کرد
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("group leader %d survived SIGKILL", pgid)
}

// TestSignalGroupEmptyGroupNotSignaled اثباتِ F4: پس از مرگ و reap کاملِ
// تنها عضو گروه، signalGroup باید false بدهد — یعنی حتی اگر سیستم این
// pgid را به فرایند بی‌ربطِ تازه‌ای داده باشد، سیگنال ارسال نمی‌شود.
func TestSignalGroupEmptyGroupNotSignaled(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	cmd := exec.Command("sh", "-c", "exit 0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("shell must exit cleanly: %v", err)
	}

	// گروه حالا خالی است (تنها عضو reap شده) — هیچ سیگنالی نباید فرستاده شود
	if signalGroup(pgid, syscall.SIGKILL) {
		t.Fatal("empty group must NOT be signaled (ESRCH guard) — F4 regression")
	}
	if signalGroup(pgid, syscall.SIGTERM) {
		t.Fatal("empty group must NOT be signaled (ESRCH guard) — TERM variant")
	}
}

// TestSignalGroupZeroPgid pgid صفر (قبل از attach): بدون syscall و بدون
// پنیک، false.
func TestSignalGroupZeroPgid(t *testing.T) {
	if signalGroup(0, syscall.SIGKILL) {
		t.Fatal("pgid 0 must never be signaled")
	}
	if signalGroup(0, syscall.SIGTERM) {
		t.Fatal("pgid 0 must never be signaled (TERM)")
	}
}

// TestProcTreeTerminateKillUseGuard یکپارچگی: procTree (مسیر واقعی runShell)
// روی pgid صفر (هرگز attach نشده) هم terminate و هم kill را بی‌اثر صدا می‌زند.
func TestProcTreeTerminateKillUseGuard(t *testing.T) {
	tree := &procTree{} // pgid = 0 — شبیه attach ناموفق
	tree.terminate()    // نباید پنیک/سیگنال بدهد
	tree.kill()
}
