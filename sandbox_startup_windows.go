//go:build windows

// نصب خودکار دسترسی NUL برای سندباکس AppContainer.
//
// چرا: توکن AppContainer به \Device\Null دسترسی ندارد و go/git (که NUL را
// باز می‌کنند) داخل سندباکس می‌میرند. اسکریپت scripts/grant-sandbox-nul.ps1
// این ACE را اضافه می‌کند، اما DACL دستگاه NUL با هر ری‌استارت بازنشانی
// می‌شود؛ پس یک Scheduled Task کاربری (At log on) ثبت می‌شود.
//
// Task به‌جای خودِ اسکریپت، یک launcher تولیدشده را اجرا می‌کند که:
//   - اگر فایل اجرایی MyAI دیگر وجود نداشته باشد (حذف/جابه‌جایی برنامه)،
//     خودِ Task و اسکریپت‌ها را حذف می‌کند — یعنی بعد از حذف برنامه، اولین
//     ورود به ویندوز پاک‌سازی کامل انجام می‌شود؛
//   - در غیر این صورت ACE دستگاه NUL را بازمی‌گرداند.
//
// هنگام هر شروع برنامه، launcher با مسیر فعلی exe بازنویسی و Task دوباره
// ثبت می‌شود؛ بنابراین به‌روزرسانی/جابه‌جایی برنامه هم خودکار اصلاح می‌شود.
// اگر ساخت Task مجاز نباشد، همان launcher از پوشه‌ی Startup کاربر اجرا می‌شود
// (که آن هم به exe اشاره دارد و رفتار self-clean یکسان دارد).
package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

//go:embed scripts/grant-sandbox-nul.ps1
var sandboxNulScript string

// sandboxNulTaskName نام Scheduled Task کاربری برای بازگرداندن ACE دستگاه NUL.
const sandboxNulTaskName = "MyAI Sandbox NUL"

// psQuote رشته را برای درج داخل تک‌کوتیشن PowerShell امن می‌کند.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// sandboxNulAppPath مسیر فایل اجرایی MyAI که launcher باید مراقبش باشد.
// MYAI_NUL_GUARD_APP فقط برای تست/طراحی است.
func sandboxNulAppPath() string {
	if p := strings.TrimSpace(os.Getenv("MYAI_NUL_GUARD_APP")); p != "" {
		return p
	}
	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		return ""
	}
	return exe
}

// sandboxNulLauncherScript متن launcher: پاک‌سازی خودکار اگر exe رفته باشد،
// وگرنه اجرای اسکریپت NUL.
func sandboxNulLauncherScript(appPath, grantPath, launcherPath string) string {
	if appPath == "" {
		appPath = grantPath // بدون مسیر exe همیشه grant را اجرا کن (fail-open برای ابزار)
	}
	return strings.Join([]string{
		"# ساخته‌شده توسط MyAI — دستی ویرایش نکنید.",
		"$ErrorActionPreference = 'SilentlyContinue'",
		"$app = " + psQuote(appPath),
		"if (-not (Test-Path -LiteralPath $app)) {",
		"\t& schtasks.exe /Delete /TN " + psQuote(sandboxNulTaskName) + " /F | Out-Null",
		"\tRemove-Item -LiteralPath " + psQuote(launcherPath) + " -Force",
		"\tRemove-Item -LiteralPath " + psQuote(grantPath) + " -Force",
		"\texit 0",
		"}",
		"& " + psQuote(grantPath),
		"",
	}, "\r\n")
}

// sandboxStartupCmdContent محتوای فایل .cmd پوشه‌ی Startup (fallback).
func sandboxStartupCmdContent(launcherPath string) string {
	return "@echo off\r\npowershell -NoProfile -ExecutionPolicy Bypass -File \"" + launcherPath + "\" >nul 2>&1\r\n"
}

// sandboxTaskLaunchCommand دستوری که Scheduled Task اجرا می‌کند.
func sandboxTaskLaunchCommand(launcherPath string) string {
	return `powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "` + launcherPath + `"`
}

// sandboxTaskCreateArgs آرگومان‌های schtasks برای ساخت Task در ورود کاربر.
func sandboxTaskCreateArgs(launcherPath string) []string {
	return []string{
		"/Create", "/TN", sandboxNulTaskName,
		"/SC", "ONLOGON",
		"/TR", sandboxTaskLaunchCommand(launcherPath),
		"/F",
	}
}

// sandboxStartupDir پوشه‌ی Startup کاربر جاری (بدون نیاز به Administrator).
func sandboxStartupDir() string {
	appdata := strings.TrimSpace(os.Getenv("APPDATA"))
	if appdata == "" {
		return ""
	}
	return filepath.Join(appdata, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
}

// writeIfChanged فایل را فقط وقتی می‌نویسد که محتوا فرق کند (idempotent).
func writeIfChanged(path string, data []byte, perm os.FileMode) error {
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, perm)
}

// runSandboxHidden فرایند را بی‌پنجره و با timeout کوتاه اجرا می‌کند.
func runSandboxHidden(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd.CombinedOutput()
}

// registerSandboxNulTask Task ورود کاربر را می‌سازد و با Query تأیید می‌کند.
func registerSandboxNulTask(launcherPath string) error {
	if out, err := runSandboxHidden("schtasks", sandboxTaskCreateArgs(launcherPath)...); err != nil {
		return fmt.Errorf("schtasks create: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if out, err := runSandboxHidden("schtasks", "/Query", "/TN", sandboxNulTaskName); err != nil {
		return fmt.Errorf("schtasks query: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// sandboxPlatformStartup آماده‌سازی مخصوص ویندوز: ACE دستگاه NUL با هر بوت
// بازنشانی می‌شود، پس ثبت/اجرای grant در Startup کاربر لازم است.
func sandboxPlatformStartup() error { return installSandboxNulGrant() }

// installSandboxNulGrant اسکریپت و launcher را می‌نویسد، Scheduled Task را
// ثبت/به‌روزرسانی می‌کند (با fallback به Startup) و ACE را همین حالا اعمال
// می‌کند. خطا مانع اجرای برنامه نمی‌شود (خودِ سندباکس fail-closed است).
func installSandboxNulGrant() error {
	base := sandboxCacheDir()
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	grantPath := filepath.Join(base, "grant-sandbox-nul.ps1")
	launcherPath := filepath.Join(base, "run-sandbox-nul.ps1")
	if err := writeIfChanged(grantPath, []byte(sandboxNulScript), 0o600); err != nil {
		return err
	}
	launcher := sandboxNulLauncherScript(sandboxNulAppPath(), grantPath, launcherPath)
	if err := writeIfChanged(launcherPath, []byte(launcher), 0o600); err != nil {
		return err
	}

	var registerErr error
	if err := registerSandboxNulTask(launcherPath); err != nil {
		registerErr = err
		if dir := sandboxStartupDir(); dir != "" {
			startupCmd := filepath.Join(dir, "MyAI-sandbox-nul.cmd")
			if werr := writeIfChanged(startupCmd, []byte(sandboxStartupCmdContent(launcherPath)), 0o600); werr != nil {
				registerErr = fmt.Errorf("%v; startup fallback: %w", err, werr)
			}
		}
	} else if dir := sandboxStartupDir(); dir != "" {
		// مهاجرت: نسخه‌ی قبلی از پوشه‌ی Startup استفاده می‌کرد.
		_ = os.Remove(filepath.Join(dir, "MyAI-sandbox-nul.cmd"))
	}

	out, err := runSandboxHidden("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", launcherPath)
	if err != nil {
		runErr := fmt.Errorf("nul grant: %w (%s)", err, strings.TrimSpace(string(out)))
		if registerErr != nil {
			return fmt.Errorf("%v; %w", registerErr, runErr)
		}
		return runErr
	}
	return registerErr
}
