// تست‌های ساخت آرگومان/محیط backend لینوکس (بدون build tag تا روی هر
// پلتفرمی اجرا شوند؛ اجرای واقعی bwrap فقط روی لینوکس است).
package main

import (
	"strings"
	"testing"
)

// hasSeq آیا دنباله‌ی seq به‌ترتیب در args آمده است؟
func hasSeq(args []string, seq ...string) bool {
	if len(seq) == 0 {
		return true
	}
	for i := 0; i+len(seq) <= len(args); i++ {
		match := true
		for j, want := range seq {
			if args[i+j] != want {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

func TestSandboxLinuxBwrapArgs(t *testing.T) {
	env := []string{"HOME=/tmp/h", "PATH=/usr/bin"}
	args := sandboxLinuxBwrapArgs("/work/ws", "/tmp/h", false, "echo hi", env)

	for _, want := range [][]string{
		{"--die-with-parent"},
		{"--new-session"},
		{"--unshare-user"},
		{"--unshare-pid"},
		{"--unshare-net"},
		{"--ro-bind", "/", "/"},
		{"--bind", "/work/ws", "/work/ws"},
		{"--bind", "/tmp/h", "/tmp/h"},
		{"--chdir", "/work/ws"},
		{"--clearenv"},
		{"--setenv", "PATH", "/usr/bin"},
		{"--cap-drop", "ALL"},
	} {
		if !hasSeq(args, want...) {
			t.Fatalf("bwrap args missing %v:\n%v", want, args)
		}
	}
	// دستور باید آخرین چیز و بعد از «--» باشد.
	if got := args[len(args)-3:]; got[0] != "/bin/sh" || got[1] != "-c" || got[2] != "echo hi" {
		t.Fatalf("command must be last as /bin/sh -c <command>: %v", got)
	}
	if !hasSeq(args, "--", "/bin/sh", "-c", "echo hi") {
		t.Fatalf("args must separate the command with --: %v", args)
	}
	// ترتیب امنیتی: ro-bind ریشه قبل از bind قابل‌نوشتن، و clearenv قبل از setenv.
	if indexOf(args, "--ro-bind") > indexOf(args, "--bind") {
		t.Fatal("read-only root must be mounted before the writable workspace bind")
	}
	clear := indexOf(args, "--clearenv")
	firstSetenv := indexOf(args, "--setenv")
	if clear < 0 || firstSetenv < 0 || clear > firstSetenv {
		t.Fatalf("clearenv must precede setenv: %v", args)
	}
	if capIdx, dashIdx := indexOf(args, "--cap-drop"), indexOf(args, "--"); capIdx < 0 || capIdx > dashIdx {
		t.Fatalf("cap-drop must come before --: %v", args)
	}
}

func TestSandboxLinuxBwrapArgsNetwork(t *testing.T) {
	withNet := sandboxLinuxBwrapArgs("/ws", "/h", true, "true", nil)
	if hasSeq(withNet, "--unshare-net") {
		t.Fatalf("network-enabled sandbox must not unshare net: %v", withNet)
	}
	noNet := sandboxLinuxBwrapArgs("/ws", "/h", false, "true", nil)
	if !hasSeq(noNet, "--unshare-net") {
		t.Fatalf("network-disabled sandbox must unshare net: %v", noNet)
	}
}

func TestSandboxLinuxEnvVars(t *testing.T) {
	env := []string{
		"PATH=/usr/bin:/bin",
		"LANG=en_US.UTF-8",
		"OPENAI_API_KEY=sk-secret",
		myTestSecretVar + "=token-value",
		"FOO=bar",
		"EMPTY=",
	}
	got := sandboxLinuxEnvVars(env, "/home/sbx", false)
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		"PATH=/usr/bin:/bin",
		"LANG=en_US.UTF-8",
		"HOME=/home/sbx",
		"TMPDIR=/home/sbx/tmp",
		"KODYAR_SANDBOX=1",
		"KODYAR_SANDBOX_NETWORK=0",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("env missing %q:\n%s", want, joined)
		}
	}
	for _, forbid := range []string{"sk-secret", myTestSecretVar, "FOO=bar", "EMPTY="} {
		if strings.Contains(joined, forbid) {
			t.Fatalf("env must not leak %q:\n%s", forbid, joined)
		}
	}
	if strings.Count(strings.TrimSpace(joined), "\n") != len(got)-1 {
		t.Fatalf("env vars must be one KEY=VALUE per line: %v", got)
	}

	net := sandboxLinuxEnvVars(env, "/home/sbx", true)
	if !strings.Contains(strings.Join(net, "\n"), "KODYAR_SANDBOX_NETWORK=1") {
		t.Fatalf("network flag must be 1 when allowed: %v", net)
	}
}

// myTestSecretVar یک نام متغیر شبیه کلید است تا مطمئن شویم allowlist آن را
// دور می‌ریزد.
const myTestSecretVar = "KODYAR_TEST_SECRET_KEY"
