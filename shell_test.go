// تست‌های runShell — کشتن درخت فرایند، بافر کران‌دار، لغو سریع،
// نبود نشت گوروتین و استریم خروجی. فقط مسیر یونیکس اجرا می‌شود
// (ویندوز در این محیط فقط cross-vet می‌شود — در گزارش ذکر شده).
//go:build !windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// processAlive بررسی زنده بودن فرایند با سیگنال ۰.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func TestRunShellOutputAndExitCode(t *testing.T) {
	out, code, err := runShell(context.Background(), t.TempDir(), "echo hi", 10*time.Second, nil)
	if err != nil || code != 0 || !strings.Contains(out, "hi") {
		t.Fatalf("simple: out=%q code=%d err=%v", out, code, err)
	}

	out, code, err = runShell(context.Background(), t.TempDir(), "echo hi; exit 7", 10*time.Second, nil)
	if err == nil || code != 7 {
		t.Fatalf("exit code: code=%d err=%v", code, err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("output before failure must be kept: %q", out)
	}
}

func TestRunShellRunsInDir(t *testing.T) {
	dir := t.TempDir()
	real, _ := filepath.EvalSymlinks(dir)
	out, _, err := runShell(context.Background(), dir, "pwd", 10*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.TrimSpace(out), real) {
		t.Fatalf("pwd=%q want dir %q", out, real)
	}
}

// TestRunShellTimeoutKillsChildTree: بعد از timeout، فرزندِ پس‌زمینه‌ای که
// shell ساخته هم باید مرده باشد (پیش از این فقط shell کشته می‌شد).
func TestRunShellTimeoutKillsChildTree(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	cmd := "sleep 30 & echo $! > " + pidFile + "; wait"

	start := time.Now()
	out, _, err := runShell(context.Background(), dir, cmd, 500*time.Millisecond, nil)
	if err == nil {
		t.Fatalf("expected timeout error, out=%q", out)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("timeout handling too slow: %v", d)
	}

	data, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("child pid not recorded: %v (out=%q)", rerr, out)
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
	if perr != nil {
		t.Fatalf("bad pid %q", data)
	}
	// فرزند باید ظرف حداکثر ۲ ثانیه کاملاً مرده باشد
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return // ✓ درخت کامل کشته شد
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("child process %d survived the tree kill", pid)
}

// TestRunShellContextCancelPrompt: لغو context باید سریع برگردد.
func TestRunShellContextCancelPrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, _, err := runShell(ctx, t.TempDir(), "sleep 30", 60*time.Second, nil)
	if err == nil {
		t.Fatal("expected error after cancel")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("cancel was not prompt: %v", d)
	}
}

// TestRunShellBoundedOutput: خروجی بزرگ باید کران‌دار (سر+دنباله+نشانگر) باشد.
func TestRunShellBoundedOutput(t *testing.T) {
	// seq 1 200000 ≈ ۱.۳MB — خیلی بیشتر از سقف ۶۴KB
	out, _, err := runShell(context.Background(), t.TempDir(), "seq 1 200000", 30*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > shellHeadBytes+shellTailBytes+256 {
		t.Fatalf("output not capped: %d bytes", len(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("truncation marker missing: %q", out[:100])
	}
	if !strings.HasPrefix(out, "1\n") {
		t.Fatalf("head lost: %q", out[:40])
	}
	if !strings.Contains(out, "200000") {
		t.Fatal("tail lost")
	}
}

// TestRunShellNoGoroutineLeak: گوروتین‌های runShell باید کامل خارج شوند.
func TestRunShellNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		_, _, _ = runShell(context.Background(), t.TempDir(), "echo x", 5*time.Second, nil)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}

// TestRunShellOnOutput: callback باید تکه‌های واقعی خروجی را بگیرد.
func TestRunShellOnOutput(t *testing.T) {
	var mu sync.Mutex
	var got []string
	onOutput := func(p []byte) {
		mu.Lock()
		got = append(got, string(p))
		mu.Unlock()
	}
	out, _, err := runShell(context.Background(), t.TempDir(), "echo streamed", 10*time.Second, onOutput)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "streamed") {
		t.Fatalf("final output wrong: %q", out)
	}
	mu.Lock()
	joined := strings.Join(got, "")
	mu.Unlock()
	if !strings.Contains(joined, "streamed") {
		t.Fatalf("onOutput did not receive output: %v", got)
	}
}
