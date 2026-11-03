//go:build !windows

// تست کشتن «درخت فرایند» سرورهای MCP روی یونیکس — فرزندِ فرزند (sleep)
// هم باید با کشتن گروه فرایند از بین برود. معادل ویندوزی (Job Object)
// روی ماشین ویندوزی قابل اجراست؛ این‌جا روی یونیکس اثبات می‌شود.
// probing با syscall.Kill(-pgid, 0) است: فقط با ESRCH یعنی گروه کاملاً
// خالی شده (ابزار kill بیرونی برای pgid نامطمئن است — تستِ دستی نشان داد).
package main

import (
	"syscall"
	"testing"
	"time"
)

func TestMCPShutdownKillsProcessTree(t *testing.T) {
	a := newFakeMCPApp(t, "fake-spawn", true)
	c := ensureMCPStarted(t, a)

	// فرزندِ سرور (sleep 120) باید زنده باشد؛ pid آن از pgid قابل دیدن است.
	// ما pid فرزند را از خروجی سرور نمی‌گیریم — به‌جایش اثبات را با «پروسه‌ی
	// سرور» و «هیچ بازمانده‌ای نماند» انجام می‌دهیم:
	childPID := c.cmd.Process.Pid
	if !processAlive(childPID) {
		t.Fatal("fake server should be running before shutdown")
	}

	a.mcp.stopAll()

	// گروه فرایند (شامل فرزندِ فرزندِ sleep) باید کاملاً خالی شود — نه فقط
	// راهبر گروه. probing با syscall.Kill(-pgid, 0): فقط وقتی ESRCH می‌دهد
	// که هیچ عضوی زنده نمانده (ابزار kill بیرونی برای pgid نامطمئن است).
	groupEmpty := func() bool {
		return syscall.Kill(-childPID, 0) != nil
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !groupEmpty() {
		time.Sleep(100 * time.Millisecond)
	}
	if !groupEmpty() {
		t.Fatalf("process group %d still has live members (child-of-child leaked)", childPID)
	}
	if processAlive(childPID) {
		t.Fatalf("server process %d must be killed on shutdown", childPID)
	}
}
