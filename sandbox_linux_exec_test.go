//go:build linux

// تست‌های اجرایی backend سندباکس لینوکس: اجرای واقعی bwrap (نه فقط ساخت
// آرگومان) و بررسی fail-closed وقتی bwrap نصب نیست.
//
// اجرا: go test -run 'TestLinuxSandbox' -v .
// اگر bwrap نصب نباشد، تست اجرای واقعی skip می‌شود (و تست‌های fail-closed
// همیشه اجرا می‌شوند). اگر bwrap نصب باشد ولی این محیط اجازه‌ی namespace
// ندهد (مثلاً داخل کانتینر بدون userns)، همان هم با پیام روشن skip می‌شود.
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// linuxSandboxTestWorkspace پوشه‌ی کاری موقت با یک فایل آماده.
func linuxSandboxTestWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "hello.txt"), []byte("hello-sandbox\n"), 0o644); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	return ws
}

// requireWorkingBwrap اگر bwrap نصب/قابل‌استفاده نباشد تست را skip می‌کند
// (نبودنش در تست جداگانه fail-closed بررسی می‌شود).
func requireWorkingBwrap(t *testing.T) string {
	t.Helper()
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bubblewrap not installed on this machine; the real-exec test needs it (fail-closed is covered separately)")
	}
	// آیا این محیط اجازه‌ی ساخت namespace می‌دهد؟
	probe := exec.Command(bwrap, "--unshare-user", "--unshare-pid", "--ro-bind", "/", "/", "--", "true")
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("bwrap is installed but this environment cannot create namespaces (%v): %s", err, strings.TrimSpace(string(out)))
	}
	return bwrap
}

// TestLinuxSandboxRealBwrapRunsAndIsolates اجرای واقعی bwrap: دستور در پوشه‌ی
// کاری اجرا می‌شود، نوشتن داخل آن مجاز است و نوشتن بیرون از آن رد می‌شود.
func TestLinuxSandboxRealBwrapRunsAndIsolates(t *testing.T) {
	requireWorkingBwrap(t)
	ws := linuxSandboxTestWorkspace(t)
	pol := &sandboxPolicy{Workspace: ws, AllowNetwork: false}
	ctx := context.Background()

	out, code, err := runSandboxedShell(ctx, ws, "cat hello.txt", 20*time.Second, nil, pol)
	if err != nil || code != 0 {
		t.Fatalf("sandboxed cat failed: code=%d err=%v out=%q", code, err, out)
	}
	if !strings.Contains(out, "hello-sandbox") {
		t.Fatalf("workspace file was not readable inside the sandbox: %q", out)
	}

	// خانه‌ی ایزوله است (نه HOME میزبان).
	hostHome, _ := os.UserHomeDir()
	out, code, err = runSandboxedShell(ctx, ws, `printf '%s' "$HOME"; printf '\nmyai-sandbox=%s' "$MYAI_SANDBOX"`, 20*time.Second, nil, pol)
	if err != nil || code != 0 {
		t.Fatalf("sandboxed env probe failed: code=%d err=%v out=%q", code, err, out)
	}
	if strings.TrimSpace(hostHome) != "" && strings.Contains(out, hostHome) {
		t.Fatalf("HOME leaked from the host into the sandbox: %q", out)
	}
	if !strings.Contains(out, "myai-sandbox-home-") || !strings.Contains(out, "myai-sandbox=1") {
		t.Fatalf("sandbox must use an isolated home and set MYAI_SANDBOX=1: %q", out)
	}

	// نوشتن داخل پوشه‌ی کاری مجاز است.
	if out, code, err = runSandboxedShell(ctx, ws, "echo inside > inside.txt", 20*time.Second, nil, pol); err != nil || code != 0 {
		t.Fatalf("write inside the workspace must succeed: code=%d err=%v out=%q", code, err, out)
	}
	if b, err := os.ReadFile(filepath.Join(ws, "inside.txt")); err != nil || !strings.Contains(string(b), "inside") {
		t.Fatalf("write inside the workspace did not land on disk: %v %q", err, b)
	}

	// نوشتن بیرون از پوشه‌ی کاری رد می‌شود (کل ریشه فقط-خواندنی است).
	escape := filepath.Join("/", "myai-sandbox-escape-"+filepath.Base(ws)+".txt")
	out, code, err = runSandboxedShell(ctx, ws, "echo escaped > "+escape+" 2>&1", 20*time.Second, nil, pol)
	if err == nil && code == 0 {
		t.Fatalf("writing outside the workspace must be denied, got code=0 out=%q", out)
	}
	if _, statErr := os.Stat(escape); statErr == nil {
		_ = os.Remove(escape)
		t.Fatalf("a file escaped the sandbox at %s", escape)
	}

	// کد خروج دستور حفظ می‌شود (نه اینکه همه‌چیز موفق گزارش شود).
	out, code, err = runSandboxedShell(ctx, ws, "echo boom 1>&2; exit 3", 20*time.Second, nil, pol)
	if err != nil {
		t.Fatalf("non-zero exit must not be reported as an exec error: %v (out %q)", err, out)
	}
	if code != 3 || !strings.Contains(out, "boom") {
		t.Fatalf("exit code/stderr must propagate: code=%d out=%q", code, out)
	}
}

// TestLinuxSandboxRealBwrapRespectsContext توقف از طریق ctx حتی بدون مهلت
// (حالت پس‌زمینه) هم کار می‌کند و فرایند را رها نمی‌کند.
func TestLinuxSandboxRealBwrapRespectsContext(t *testing.T) {
	requireWorkingBwrap(t)
	ws := linuxSandboxTestWorkspace(t)
	pol := &sandboxPolicy{Workspace: ws}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, code, err := runSandboxedShell(ctx, ws, "sleep 30", 0, nil, pol)
	if time.Since(start) > 10*time.Second {
		t.Fatalf("cancelling the sandboxed command took too long")
	}
	if err == nil && code == 0 {
		t.Fatalf("cancelled command must not report success (code=%d err=%v)", code, err)
	}
}

// TestLinuxSandboxMissingBwrapFailsClosed نبودن bwrap باید یعنی «اجرا نشد»،
// نه اجرای بدون ایزوله.
func TestLinuxSandboxMissingBwrapFailsClosed(t *testing.T) {
	ws := linuxSandboxTestWorkspace(t)
	emptyPath := t.TempDir()
	t.Setenv("PATH", emptyPath)
	if _, err := exec.LookPath("bwrap"); err == nil {
		t.Fatal("test setup: bwrap must not be resolvable with an empty PATH")
	}
	marker := filepath.Join(ws, "ran-unsandboxed.txt")
	pol := &sandboxPolicy{Workspace: ws}

	out, code, err := runSandboxedShell(context.Background(), ws, "echo ran > ran-unsandboxed.txt", 10*time.Second, nil, pol)
	if err == nil {
		t.Fatalf("missing bwrap must return an error, got code=%d out=%q", code, out)
	}
	if !strings.Contains(err.Error(), "bubblewrap (bwrap) is not installed") {
		t.Fatalf("error must name the missing dependency: %v", err)
	}
	if !strings.Contains(err.Error(), "the command was not run") {
		t.Fatalf("error must state that the command was not run: %v", err)
	}
	if code >= 0 {
		t.Fatalf("fail-closed must not report a command exit code, got %d", code)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("command ran unsandboxed although bwrap is missing (%s exists)", marker)
	}
}

// TestLinuxSandboxMissingBwrapFailsClosedInBackground همان fail-closed در
// مسیر پس‌زمینه: شروع می‌شود ولی فرایند باید با خطای روشن شکست بخورد و
// هیچ فرمانی اجرا نشود.
func TestLinuxSandboxMissingBwrapFailsClosedInBackground(t *testing.T) {
	ws := linuxSandboxTestWorkspace(t)
	t.Setenv("PATH", t.TempDir())
	a := newTestApp(t)
	pol := &sandboxPolicy{Workspace: ws}
	marker := filepath.Join(ws, "ran-unsandboxed.txt")

	p, err := a.startBackgroundProcess(ws, "echo ran > ran-unsandboxed.txt", pol, nil, nil)
	if err != nil {
		t.Fatalf("startBackgroundProcess must accept the job (failure is reported async): %v", err)
	}
	select {
	case <-p.wait:
	case <-time.After(15 * time.Second):
		t.Fatalf("background process never finished: %s", p.status())
	}
	if status := p.status(); !strings.Contains(status, "bwrap") {
		t.Fatalf("background status must explain the sandbox failure, got %q", status)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("background command ran unsandboxed although bwrap is missing (%s exists)", marker)
	}
	// خروجی ابزار هم باید خطا را نشان دهد و خواندن/توقف تمیز کار کند.
	out, ok := a.toolProcessOutput(p.id, 0, 0)
	if !ok || !strings.Contains(out, "bwrap") || !strings.Contains(out, "failed") {
		t.Fatalf("process_output must surface the failure: ok=%v %q", ok, out)
	}
	if stopOut, _ := a.toolProcessStop(p.id); !strings.Contains(stopOut, "stopped") {
		t.Fatalf("stopping a failed process must still work: %q", stopOut)
	}
}
