//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideConsoleWindow جلوی فلش‌شدن پنجره‌ی کنسول سیاه را در ویندوز می‌گیرد.
// هر exec.Command بدون این فلگ، هنگام ارسال هر پیام (چک‌پوینت‌های git،
// راه‌اندازی سرورهای MCP و…) یک پنجره‌ی کنسول لحظه‌ای باز و بسته می‌کرد.
// CREATE_NO_WINDOW = 0x08000000. باید بلافاصله پس از ساخت cmd و پیش از
// Start صدا زده شود؛ این helper جایگزین SysProcAttrهای موجود در
// shell_windows.go (configureShell و taskkill) نیست — آن‌ها خودشان این
// فلگ‌ها را با معنی متفاوت (CmdLine/chcp) ست می‌کنند.
func hideConsoleWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
