//go:build linux

// backend ایزوله‌سازی لینوکس با bubblewrap.
//
// چرا bwrap: بدون root و بدون کد native اضافه، namespaceهای user/pid/net/ipc/
// uts را می‌سازد و کل فایل‌سیستم را فقط-خواندنی می‌کند به‌جز پوشه‌ی کاری و
// خانه‌ی ایزوله‌ی موقت. اگر bwrap نصب نباشد، سندباکس **fail-closed** است:
// دستور اجرا نمی‌شود و خطای روشن برمی‌گردد (نه اجرای بدون ایزوله).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// sandboxSupported در لینوکس پیاده‌سازی وجود دارد؛ در دسترس‌بودن bwrap هنگام
// اجرا بررسی می‌شود و نبودنش به خطای fail-closed تبدیل می‌شود.
func sandboxSupported() bool { return true }

// sandboxLinuxBwrap مسیر bwrap را برمی‌گرداند؛ نبودنش خطای صریح است.
func sandboxLinuxBwrap() (string, error) {
	p, err := exec.LookPath("bwrap")
	if err != nil || strings.TrimSpace(p) == "" {
		return "", errors.New("linux sandbox: bubblewrap (bwrap) is not installed; install it (e.g. `apt install bubblewrap`) or turn the sandbox off — the command was not run")
	}
	return p, nil
}

// runSandboxedShell دستور را از طریق bwrap در یک محیط ایزوله اجرا می‌کند.
func runSandboxedShell(ctx context.Context, dir, command string, timeout time.Duration, onOutput func([]byte), pol *sandboxPolicy) (string, int, error) {
	if pol == nil {
		return "", -1, errors.New("linux sandbox: missing sandbox policy")
	}
	if strings.TrimSpace(dir) == "" {
		return "", -1, errors.New("linux sandbox: workspace directory is required")
	}
	bwrap, err := sandboxLinuxBwrap()
	if err != nil {
		return "", -1, err
	}
	// timeout<=0 یعنی «بدون مهلت» (اجرای پس‌زمینه/بلندمدت)؛ لغو همچنان از
	// طریق ctx کار می‌کند و watchdog همان درخت فرایند را می‌کشد.
	cctx, cancel := ctx, context.CancelFunc(func() {})
	if timeout > 0 {
		if timeout > shellMaxTimeout {
			timeout = shellMaxTimeout
		}
		cctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	home, err := os.MkdirTemp("", "myai-sandbox-home-")
	if err != nil {
		return "", -1, fmt.Errorf("linux sandbox: isolated home: %w", err)
	}
	defer os.RemoveAll(home)
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0o700); err != nil {
		return "", -1, fmt.Errorf("linux sandbox: isolated home: %w", err)
	}

	env := sandboxLinuxEnvVars(os.Environ(), home, pol.AllowNetwork)
	args := sandboxLinuxBwrapArgs(pol.Workspace, home, pol.AllowNetwork, command, env)

	cmd := exec.CommandContext(cctx, bwrap, args...)
	cmd.Dir = pol.Workspace
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()
	out := buf.String()
	if onOutput != nil && out != "" {
		onOutput([]byte(out))
	}
	if cctx.Err() == context.DeadlineExceeded {
		return out, -1, fmt.Errorf("linux sandbox: command timed out after %s", timeout)
	}
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			code := ee.ExitCode()
			if code < 0 {
				code = 1
			}
			return out, code, nil
		}
		return out, -1, fmt.Errorf("linux sandbox: %w", runErr)
	}
	return out, 0, nil
}

// sandboxPlatformStartup روی لینوکس کار آماده‌سازی اضافه‌ای لازم نیست.
func sandboxPlatformStartup() error { return nil }

// sandboxRevokeGrants روی لینوکس ACE ثبت‌شده‌ای وجود ندارد؛ ایزوله‌سازی از
// طریق namespaceهای موقت bwrap است و با پایان فرایند از بین می‌رود.
func sandboxRevokeGrants() error { return nil }
