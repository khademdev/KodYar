//go:build windows

// نصب خودکار دسترسی NUL برای سندباکس AppContainer.
//
// چرا: توکن AppContainer به \Device\Null دسترسی ندارد و go/git (که NUL را
// باز می‌کنند) داخل سندباکس می‌میرند. اسکریپت scripts/grant-sandbox-nul.ps1
// این ACE را اضافه می‌کند، اما DACL دستگاه NUL با هر ری‌استارت بازنشانی
// می‌شود؛ پس یک Scheduled Task کاربری (At log on) ثبت می‌شود.
//
// Task به‌جای خودِ اسکریپت، یک launcher تولیدشده را اجرا می‌کند که:
//   - اگر فایل اجرایی KodYar دیگر وجود نداشته باشد (حذف/جابه‌جایی برنامه)،
//     خودِ Task و اسکریپت‌ها را حذف می‌کند — یعنی بعد از حذف برنامه، اولین
//     ورود به ویندوز پاک‌سازی کامل انجام می‌شود؛
//   - در غیر این صورت ACE دستگاه NUL را بازمی‌گرداند.
//
// هنگام هر شروع برنامه، launcher با مسیر فعلی exe بازنویسی و Task دوباره
// ثبت می‌شود؛ بنابراین به‌روزرسانی/جابه‌جایی برنامه هم خودکار اصلاح می‌شود.
// اگر ساخت Task مجاز نباشد، همان launcher از پوشه‌ی Startup کاربر اجرا می‌شود
// (که آن هم به exe اشاره دارد و رفتار self-clean یکسان دارد).
//
// شکل این fallback یک فایل .vbs است، نه .cmd: فایل .cmd را cmd.exe اجرا
// می‌کند و @echo off/تغییرمسیر پنجرهٔ کنسول را نمی‌بندد، پس هنگام هر روشن‌شدن
// ویندوز یک پنجرهٔ سیاه کوتاه می‌آمد. wscript.exe یک برنامهٔ GUI است و با
// مقدار 0 در sh.Run هیچ پنجره‌ای باز نمی‌کند. نسخه‌های پیش از 1.0.3 از .cmd
// استفاده می‌کردند؛ آن فایل هنگام نصب/به‌روزرسانی پاک می‌شود.
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
const sandboxNulTaskName = "KodYar Sandbox NUL"

// psQuote رشته را برای درج داخل تک‌کوتیشن PowerShell امن می‌کند.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// sandboxNulAppPath مسیر فایل اجرایی KodYar که launcher باید مراقبش باشد.
// KODYAR_NUL_GUARD_APP فقط برای تست/طراحی است.
func sandboxNulAppPath() string {
	if p := strings.TrimSpace(os.Getenv("KODYAR_NUL_GUARD_APP")); p != "" {
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
		"# ساخته‌شده توسط KodYar — دستی ویرایش نکنید.",
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

// sandboxStartupScriptName نام ورودی پوشه‌ی Startup (fallback).
const sandboxStartupScriptName = "KodYar-sandbox-nul.vbs"

// sandboxLegacyStartupScriptName نام fallback قدیمی (نسخه‌های < 1.0.3) که با
// cmd اجرا می‌شد و پنجرهٔ کنسول باز می‌کرد.
const sandboxLegacyStartupScriptName = "KodYar-sandbox-nul.cmd"

// vbsQuote رشته را به یک literal معتبر VBScript تبدیل می‌کند (گیومه دوتایی).
func vbsQuote(s string) string {
	return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
}

// sandboxStartupVbsContent محتوای ورودی پوشه‌ی Startup است: launcher را با
// wscript و پنجرهٔ مخفی اجرا می‌کند و اگر فایل اجرایی KodYar دیگر نباشد، خودش را
// هم از Startup حذف می‌کند (همان رفتار self-clean بقیه‌ی زنجیره).
func sandboxStartupVbsContent(vbsPath, launcherPath, appPath string) string {
	cmd := `powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "` + launcherPath + `"`
	lines := []string{
		"' ساخته‌شده توسط KodYar — دستی ویرایش نکنید.",
		"' اجرا با wscript تا هیچ پنجرهٔ کنسولی باز نشود.",
		"Option Explicit",
		"Dim sh, fso, appPath",
		"Set sh = CreateObject(\"WScript.Shell\")",
		"Set fso = CreateObject(\"Scripting.FileSystemObject\")",
		"appPath = " + vbsQuote(appPath),
		"' آرگومان دوم 0 = پنجرهٔ مخفی، True = منتظر ماندن تا ACE اعمال شود.",
		"sh.Run " + vbsQuote(cmd) + ", 0, True",
	}
	if appPath != "" {
		lines = append(lines,
			"' اگر KodYar حذف/جابه‌جا شده باشد، این ورودی هم نباید در Startup بماند.",
			"If Not fso.FileExists(appPath) Then",
			"\tfso.DeleteFile "+vbsQuote(vbsPath)+", True",
			"End If",
		)
	}
	return strings.Join(lines, "\r\n") + "\r\n"
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

	startupDir := sandboxStartupDir()
	startupVbs := ""
	if startupDir != "" {
		startupVbs = filepath.Join(startupDir, sandboxStartupScriptName)
	}

	// safeToDropLegacyCmd فقط وقتی true می‌شود که یک مسیر جایگزین واقعاً برقرار
	// شده باشد؛ وگرنه حذف .cmd قدیمی یعنی از دست دادن مجوز NUL بعد از بوت.
	safeToDropLegacyCmd := false
	var registerErr error
	if err := registerSandboxNulTask(launcherPath); err != nil {
		registerErr = err
		if startupVbs != "" {
			content := sandboxStartupVbsContent(startupVbs, launcherPath, sandboxNulAppPath())
			if werr := writeIfChanged(startupVbs, []byte(content), 0o600); werr != nil {
				registerErr = fmt.Errorf("%v; startup fallback: %w", err, werr)
			} else {
				safeToDropLegacyCmd = true
			}
		}
	} else if startupVbs != "" {
		// Task کار می‌کند (بی‌پنجره)؛ ورودی Startup لازم نیست.
		_ = os.Remove(startupVbs)
		safeToDropLegacyCmd = true
	}
	if safeToDropLegacyCmd && startupDir != "" {
		// مهاجرت < 1.0.3: .cmd قدیمی هنگام هر ورود یک کنسول باز می‌کرد.
		_ = os.Remove(filepath.Join(startupDir, sandboxLegacyStartupScriptName))
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
