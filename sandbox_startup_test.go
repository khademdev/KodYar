//go:build windows

package main

import (
	"os"
	"strings"
	"testing"
)

func TestSandboxDefaultOn(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported")
	}
	if !sandboxEnabledLocked(&Settings{}) {
		t.Fatal("sandbox must be on by default (empty mode)")
	}
	if !sandboxEnabledLocked(&Settings{SandboxMode: "on"}) {
		t.Fatal("explicit on must stay on")
	}
	if sandboxEnabledLocked(&Settings{SandboxMode: "off"}) {
		t.Fatal("explicit off must stay off")
	}
	if sandboxEnabledLocked(nil) {
		t.Fatal("nil settings must not enable the sandbox")
	}
}

func TestSandboxNulBootstrapEmbedded(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported")
	}
	if len(sandboxNulScript) < 500 {
		t.Fatalf("embedded NUL script looks empty (%d bytes)", len(sandboxNulScript))
	}
	for _, want := range []string{"SetKernelObjectSecurity", "DeriveAppContainerSidFromAppContainerName", "KodYar.Sandbox.v1"} {
		if !strings.Contains(sandboxNulScript, want) {
			t.Fatalf("embedded NUL script is missing %q", want)
		}
	}
	vbs := sandboxStartupVbsContent(
		`C:\Users\x\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Startup\KodYar-sandbox-nul.vbs`,
		`C:\Users\x\AppData\Local\KodYar\sandbox\run-sandbox-nul.ps1`,
		`C:\Apps\KodYar\KodYar.exe`)
	if !strings.Contains(vbs, "-File") || !strings.Contains(vbs, `run-sandbox-nul.ps1`) {
		t.Fatalf("startup vbs content is wrong: %q", vbs)
	}
	if !strings.Contains(vbs, "\r\n") {
		t.Fatalf("startup vbs must use CRLF line endings: %q", vbs)
	}
}

// TestSandboxNulStartupVbsIsWindowless — رگرسیون ‹پنجرهٔ cmd هنگام روشن شدن
// کامپیوتر›: ورودی Startup باید با wscript و پنجرهٔ مخفی اجرا شود، هرگز با
// cmd/@echo off (که کنسول را نمی‌بندد).
func TestSandboxNulStartupVbsIsWindowless(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported")
	}
	vbsPath := `C:\Users\x\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Startup\KodYar-sandbox-nul.vbs`
	launcher := `C:\Users\x\AppData\Local\KodYar\sandbox\run-sandbox-nul.ps1`
	vbs := sandboxStartupVbsContent(vbsPath, launcher, `C:\Apps\KodYar\KodYar.exe`)

	if strings.Contains(vbs, "@echo off") || strings.Contains(vbs, ".cmd") {
		t.Fatalf("startup entry must not be a batch script (it flashes a console): %q", vbs)
	}
	for _, want := range []string{
		`CreateObject("WScript.Shell")`,
		`CreateObject("Scripting.FileSystemObject")`,
		"-WindowStyle Hidden",
		", 0, True",
		`"` + launcher + `"`,
	} {
		if !strings.Contains(vbs, want) {
			t.Fatalf("windowless startup vbs missing %q:\n%s", want, vbs)
		}
	}
	// متغیرها اعلام می‌شوند تا با Option Explicit در wscript خطا ندهد.
	for _, name := range []string{"sh", "fso", "appPath"} {
		if !strings.Contains(vbs, "Dim ") || !strings.Contains(vbs, name) {
			t.Fatalf("vbs must declare %q:\n%s", name, vbs)
		}
	}
}

// خود-پاک‌سازی: اگر exe نباشد، ورودی Startup خودش را حذف می‌کند؛ و وقتی مسیر
// exe معلوم نیست، هیچ حذفی انجام نمی‌شود (fail-open مثل launcher).
func TestSandboxNulStartupVbsSelfCleans(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported")
	}
	vbsPath := `C:\Startup\KodYar-sandbox-nul.vbs`
	launcher := `C:\Users\x\AppData\Local\KodYar\sandbox\run-sandbox-nul.ps1`
	body := sandboxStartupVbsContent(vbsPath, launcher, `C:\Apps\KodYar\KodYar.exe`)
	for _, want := range []string{
		`If Not fso.FileExists(appPath) Then`,
		`fso.DeleteFile "` + vbsPath + `", True`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("self-clean block missing %q:\n%s", want, body)
		}
	}
	if got := sandboxStartupVbsContent(vbsPath, launcher, ""); strings.Contains(got, "FileExists") {
		t.Fatalf("unknown app path must not delete the startup entry:\n%s", got)
	}
	if got := vbsQuote(`C:\O"Brien\KodYar.exe`); got != `"C:\O""Brien\KodYar.exe"` {
		t.Fatalf("vbsQuote escaping wrong: %s", got)
	}
}

func TestSandboxNulTaskArgs(t *testing.T) {
	script := `C:\Users\x\AppData\Local\KodYar\sandbox\grant-sandbox-nul.ps1`
	joined := strings.Join(sandboxTaskCreateArgs(script), " ")
	for _, want := range []string{"/Create", "/TN", sandboxNulTaskName, "/SC", "ONLOGON", "/TR", script, "-File", "/F"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("schtasks args missing %q: %v", want, sandboxTaskCreateArgs(script))
		}
	}
	if !strings.Contains(sandboxTaskLaunchCommand(script), "-WindowStyle Hidden") {
		t.Fatalf("task command must hide the window: %q", sandboxTaskLaunchCommand(script))
	}
}

func TestSandboxNulLauncherSelfCleans(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported")
	}
	app := `C:\Apps\KodYar\KodYar.exe`
	grant := `C:\Users\x\AppData\Local\KodYar\sandbox\grant-sandbox-nul.ps1`
	launcher := `C:\Users\x\AppData\Local\KodYar\sandbox\run-sandbox-nul.ps1`
	body := sandboxNulLauncherScript(app, grant, launcher)
	for _, want := range []string{
		"Test-Path -LiteralPath $app",
		"schtasks.exe /Delete /TN '" + sandboxNulTaskName + "' /F",
		"Remove-Item -LiteralPath '" + launcher + "' -Force",
		"Remove-Item -LiteralPath '" + grant + "' -Force",
		"& '" + grant + "'",
		"'" + app + "'",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("launcher missing %q:\n%s", want, body)
		}
	}
	if got := psQuote(`C:\O'Brien\KodYar.exe`); got != `'C:\O''Brien\KodYar.exe'` {
		t.Fatalf("psQuote escaping wrong: %s", got)
	}
	fallback := sandboxNulLauncherScript("", grant, launcher)
	if !strings.Contains(fallback, "$app = '"+grant+"'") || !strings.Contains(fallback, "& '"+grant+"'") {
		t.Fatalf("empty app path must fall back to the grant path:\n%s", fallback)
	}
}

// TestInstallSandboxNulGrantLive عمداً پیش‌فرض Skip است چون یک Scheduled Task
// واقعی روی ماشین کاربر می‌سازد؛ برای بررسی دستی:
//
//	KODYAR_LIVE_TASK=1 go test -run TestInstallSandboxNulGrantLive -v .
func TestInstallSandboxNulGrantLive(t *testing.T) {
	if os.Getenv("KODYAR_LIVE_TASK") != "1" {
		t.Skip("set KODYAR_LIVE_TASK=1 to create the real scheduled task")
	}
	if !sandboxSupported() {
		t.Skip("sandbox not supported")
	}
	if err := installSandboxNulGrant(); err != nil {
		t.Fatalf("install: %v", err)
	}
}
