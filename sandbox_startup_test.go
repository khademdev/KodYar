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
	for _, want := range []string{"SetKernelObjectSecurity", "DeriveAppContainerSidFromAppContainerName", "MyAI.Sandbox.v1"} {
		if !strings.Contains(sandboxNulScript, want) {
			t.Fatalf("embedded NUL script is missing %q", want)
		}
	}
	cmd := sandboxStartupCmdContent(`C:\Users\x\AppData\Local\MyAI\sandbox\run-sandbox-nul.ps1`)
	if !strings.Contains(cmd, "-File") || !strings.Contains(cmd, `run-sandbox-nul.ps1`) {
		t.Fatalf("startup cmd content is wrong: %q", cmd)
	}
	if !strings.Contains(cmd, "\r\n") {
		t.Fatalf("startup cmd must use CRLF line endings: %q", cmd)
	}
}

func TestSandboxNulTaskArgs(t *testing.T) {
	script := `C:\Users\x\AppData\Local\MyAI\sandbox\grant-sandbox-nul.ps1`
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
	app := `C:\Apps\MyAI\MyAI.exe`
	grant := `C:\Users\x\AppData\Local\MyAI\sandbox\grant-sandbox-nul.ps1`
	launcher := `C:\Users\x\AppData\Local\MyAI\sandbox\run-sandbox-nul.ps1`
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
	if got := psQuote(`C:\O'Brien\MyAI.exe`); got != `'C:\O''Brien\MyAI.exe'` {
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
//	MYAI_LIVE_TASK=1 go test -run TestInstallSandboxNulGrantLive -v .
func TestInstallSandboxNulGrantLive(t *testing.T) {
	if os.Getenv("MYAI_LIVE_TASK") != "1" {
		t.Skip("set MYAI_LIVE_TASK=1 to create the real scheduled task")
	}
	if !sandboxSupported() {
		t.Skip("sandbox not supported")
	}
	if err := installSandboxNulGrant(); err != nil {
		t.Fatalf("install: %v", err)
	}
}
