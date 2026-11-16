// تست‌های آزادسازی زیرایجنت: اجرای دستورهای راستی‌آزمایی (با همان دروازه و
// سندباکس) و موازی‌سازی زیرکارها.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestAutoVerifyRepoBinaryGuard هیچ باینری‌ای از داخل مخزن اجرا نمی‌شود.
func TestAutoVerifyRepoBinaryGuard(t *testing.T) {
	ws := t.TempDir()
	inside := filepath.Join(ws, "bin", "tool")
	for _, c := range []struct {
		cmd  string
		want bool
	}{
		{"go build ./...", false},
		{"go vet ./...", false},
		{"tsc --noEmit -p tsconfig.json", false},
		{"python -m py_compile a.py", false},
		{`"./bin/tool" --x`, true},
		{"node_modules/.bin/tsc --noEmit", true},
		{`".` + string(filepath.Separator) + `bin` + string(filepath.Separator) + `tool"`, true},
		{"bin/tool", true},
		{inside + " --x", true},
		// خارج از مخزن: دستور مسیردار است ولی به داخل مخزن نمی‌رسد.
		{filepath.Join(t.TempDir(), "x") + " --v", false},
	} {
		if got := autoVerifyRepoBinary(ws, c.cmd); got != c.want {
			t.Fatalf("autoVerifyRepoBinary(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

// TestAutoVerifyGateRespectsPermissions دروازهٔ دسترسی/سندباکس هم‌راستا با
// run_command است: خاموش‌بودن اجازه یا نیاز به تأیید → اجرا نمی‌شود.
func TestAutoVerifyGateRespectsPermissions(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.settings.AllowCommands = false
	a.mu.Unlock()
	if ok, why := a.autoVerifyGate(t.TempDir(), "go build ./..."); ok || !strings.Contains(why, "خاموش") {
		t.Fatalf("disabled commands must block auto-verify, got ok=%v why=%q", ok, why)
	}

	a.mu.Lock()
	a.settings.AllowCommands = true
	a.mu.Unlock()
	if ok, why := a.autoVerifyGate(t.TempDir(), "go build ./..."); !ok {
		t.Fatalf("safe verification command must pass the gate, got %q", why)
	}

	// دستورِ پرریسک (نیازمند تأیید یا رد) هرگز خودکار اجرا نمی‌شود.
	for _, risky := range []string{"rm -rf build", "git reset --hard"} {
		if ok, _ := a.autoVerifyGate(t.TempDir(), risky); ok {
			t.Fatalf("%q must not run automatically", risky)
		}
	}

	// سندباکس: سیاست دستور عادی برای راستی‌آزمایی هم اعمال می‌شود.
	a.mu.Lock()
	a.settings.SandboxMode = "on"
	a.settings.SandboxNoNetwork = true
	a.mu.Unlock()
	if p := a.sandboxPolicyForCommand(t.TempDir(), "go build ./..."); p != nil && p.AllowNetwork {
		t.Fatal("no-network setting must be honored by auto-verify runs")
	}
}

// TestAutoVerifySkipLoggedOnce لاگ «اجرا نشد» تکرار نمی‌شود.
func TestAutoVerifySkipLoggedOnce(t *testing.T) {
	a := newTestApp(t)
	ws := t.TempDir()
	if !a.noteAutoVerifySkip(ws, "برای این پروژه دستور ساخت/تایپ‌چک خودکاری پیدا نشد") {
		t.Fatal("first skip must be logged")
	}
	if a.noteAutoVerifySkip(ws, "برای این پروژه دستور ساخت/تایپ‌چک خودکاری پیدا نشد") {
		t.Fatal("second identical skip must not be logged again")
	}
	if !a.noteAutoVerifySkip(ws, "دلیل دیگر") {
		t.Fatal("a different reason must still be logged")
	}
}

// TestSubagentOnlyVerificationCommands زیرایجنت فقط build/test/vet/… را
// می‌تواند اجرا کند؛ تغییردهنده/نصب/پس‌زمینه رد می‌شوند.
func TestSubagentOnlyVerificationCommands(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.settings.AllowCommands = true
	a.mu.Unlock()

	for _, cmd := range []string{"rm -rf .", "npm install", "git commit -m x", "curl http://x", "./run.sh"} {
		out, ok := a.execSubagentTool(context.Background(), "run_command", fmt.Sprintf(`{"command":%q}`, cmd))
		if ok {
			t.Fatalf("%q must be rejected in a sub-agent, got ok", cmd)
		}
		if !strings.Contains(out, "ERROR:") {
			t.Fatalf("%q rejection must be an ERROR message, got %q", cmd, out)
		}
	}

	out, ok := a.execSubagentTool(context.Background(), "run_command", `{"command":"go build ./...","background":true}`)
	if ok || !strings.Contains(out, "background") {
		t.Fatalf("background commands must be rejected, got ok=%v out=%q", ok, out)
	}

	// گفتگوی عادی (بدون ctx زیرایجنت) برای راستی‌آزمایی واقعی.
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module demo\n\ngo 1.21\n"), 0o600)
	os.WriteFile(filepath.Join(ws, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600)
	a.mu.Lock()
	a.settings.WorkspacePath = ws
	a.mu.Unlock()

	out, ok = a.execSubagentTool(context.Background(), "run_command", `{"command":"go build ./..."}`)
	if !ok || !strings.Contains(out, "exit=0") {
		t.Fatalf("verification command must run and report exit=0, got ok=%v out=%q", ok, out)
	}
}

// TestSubagentNoVerificationWhenCommandsDisabled سوییچ کاربر رعایت می‌شود.
func TestSubagentNoVerificationWhenCommandsDisabled(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.settings.AllowCommands = false
	a.mu.Unlock()
	out, ok := a.execSubagentTool(context.Background(), "run_command", `{"command":"go test ./..."}`)
	if ok || !strings.Contains(out, "disabled") {
		t.Fatalf("disabled commands must be refused, got ok=%v out=%q", ok, out)
	}
}

// TestParallelTaskLimitBounds سقف زیرکارها کم و کراندار است.
func TestParallelTaskLimitBounds(t *testing.T) {
	if n := parallelTaskLimit(); n < 2 || n > 3 {
		t.Fatalf("parallelTaskLimit must stay in [2,3], got %d", n)
	}
}

// TestParallelTaskCallsRunTogether دو فراخوانی task در یک دور هم‌زمان اجرا
// می‌شوند (نه ترتیبی) و خروجی‌ها به ترتیب اصلی می‌مانند.
func TestParallelTaskCallsRunTogether(t *testing.T) {
	a := newTestApp(t)
	old := parallelTaskLimitFn
	parallelTaskLimitFn = func() int { return 3 }
	t.Cleanup(func() { parallelTaskLimitFn = old })

	var running, peak atomic.Int32
	exec := func(ctx context.Context, name, argsJSON string) (string, bool) {
		cur := running.Add(1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		time.Sleep(60 * time.Millisecond)
		running.Add(-1)
		return "report:" + argsJSON, true
	}
	tasks := []toolBatchTask{
		{call: batchCall("t1", "task", `"first"`), exec: true},
		{call: batchCall("t2", "task", `"second"`), exec: true},
		{call: batchCall("t3", "task", `"third"`), exec: true},
	}
	start := time.Now()
	out := a.executeToolCalls(context.Background(), "conv", tasks, false, exec)
	elapsed := time.Since(start)

	if peak.Load() < 2 {
		t.Fatalf("task calls must overlap, peak concurrency=%d", peak.Load())
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("3 subtasks × 60ms must not run serially, took %v", elapsed)
	}
	for i, want := range []string{"first", "second", "third"} {
		if !strings.Contains(out[i].result, want) {
			t.Fatalf("outcome order broken at %d: %+v", i, out)
		}
	}
	t.Logf("3 parallel task calls: %dms (serial would be 180ms), peak=%d", elapsed.Milliseconds(), peak.Load())
}

// TestParallelTaskSerialWithOthers taskهای جدا از هم (با ابزار دیگر بین
// آن‌ها) سگمنت نمی‌سازند — ترتیب معنایی حفظ می‌شود.
func TestParallelTaskSerialWithOthers(t *testing.T) {
	a := newTestApp(t)
	var mu sync.Mutex
	var order []string
	exec := func(ctx context.Context, name, argsJSON string) (string, bool) {
		mu.Lock()
		order = append(order, name)
		mu.Unlock()
		return "ok", true
	}
	tasks := []toolBatchTask{
		{call: batchCall("t1", "task", `"a"`), exec: true},
		{call: batchCall("w1", "write_file", `{"path":"a.txt","content":"x"}`), exec: true},
		{call: batchCall("t2", "task", `"b"`), exec: true},
	}
	out := a.executeToolCalls(context.Background(), "conv", tasks, false, exec)
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || len(out) != 3 {
		t.Fatalf("all calls must run once: order=%v outcomes=%d", order, len(out))
	}
}
