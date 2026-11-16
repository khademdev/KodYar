//go:build windows

// تست استثنای مستند git/MSYS: git خارج از AppContainer (ولی همچنان تحت
// دروازه‌ی دسترسی) و بقیه‌ی دستورها سندباکس‌شده.
package main

import "testing"

func TestSandboxMSYSExemption(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}
	app := &App{settings: &Settings{SandboxMode: "on"}}
	ws := t.TempDir()
	if p := app.sandboxPolicyForCommand(ws, "git status --short"); p != nil {
		t.Fatal("git must be exempt from AppContainer (MSYS cannot create named objects there)")
	}
	if p := app.sandboxPolicyForCommand(ws, `"C:\Program Files\Git\cmd\git.exe" log -1`); p != nil {
		t.Fatal("full-path git must be exempt too")
	}
	if p := app.sandboxPolicyForCommand(ws, "git status && git diff"); p != nil {
		t.Fatal("a pure-git composite must be exempt")
	}
	if p := app.sandboxPolicyForCommand(ws, "go version"); p == nil {
		t.Fatal("go must stay sandboxed")
	}
	if p := app.sandboxPolicyForCommand(ws, "git status && go version"); p == nil {
		t.Fatal("mixed git+other commands must stay sandboxed")
	}
	// cygpath فقط به‌عنوان دستور تنها و read-only معاف است.
	if p := app.sandboxPolicyForCommand(ws, "cygpath -w ."); p != nil {
		t.Fatal("read-only cygpath must be exempt so paths can be converted")
	}
	if p := app.sandboxPolicyForCommand(ws, `cygpath -u "C:\\Program Files"`); p != nil {
		t.Fatal("read-only cygpath with quotes must be exempt")
	}
	if p := app.sandboxPolicyForCommand(ws, `cygpath -w . && del /q out.txt`); p == nil {
		t.Fatal("cygpath combined with a mutating command must stay sandboxed")
	}
	if p := app.sandboxPolicyForCommand(ws, `cygpath -w . > out.txt`); p == nil {
		t.Fatal("cygpath with a redirect must stay sandboxed")
	}
	if p := app.sandboxPolicyForCommand(ws, "del /q out.txt"); p == nil {
		t.Fatal("mutating commands must stay sandboxed")
	}
}

func TestSandboxMSYSExemptDetection(t *testing.T) {
	cases := map[string]bool{
		"git status":                    true,
		"git log | findstr fix":         true,
		"git.exe commit -m x":           true,
		"go version":                    false,
		"git status && go build ./...":  false,
		"git status & rm -rf build":     false,
		"git checkout -- . && del /q x": false,
		// cygpath: فقط تک‌دستورِ بدون تغییرمسیر/pipe معاف است.
		"cygpath -w .":               true,
		"cygpath.exe -u C:\\Windows": true,
		`"C:\\Program Files\\Git\\usr\\bin\\cygpath.exe" -m .`: true,
		"cygpath -w . > out.txt":                               false,
		"cygpath -w . >> out.txt":                              false,
		"cygpath -w . && del /q out.txt":                       false,
		"cygpath -w . & rd /s /q build":                        false,
		"cygpath -w . | findstr x":                             false,
		"cygpath -w . && git status":                           false,
		"del /q x":                                             false,
	}
	for cmd, want := range cases {
		if got := sandboxMSYSExempt(cmd); got != want {
			t.Fatalf("sandboxMSYSExempt(%q) = %v, want %v", cmd, got, want)
		}
	}
}
