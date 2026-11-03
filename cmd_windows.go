//go:build windows

package main

import (
	"os/exec"
	"strings"
	"syscall"
)

// escapeCmdExeQuotes دابل‌کوت داخل دستور را برای قرارگرفتن در یک رشته‌ی
// نقل‌قول‌شده‌ی cmd.exe خنثی می‌کند (" → "") تا مدل نتواند با " از
// کوتیشن بیرونی بیرون بزند و دستور دیگری تزریق کند.
func escapeCmdExeQuotes(command string) string {
	return strings.ReplaceAll(command, `"`, `""`)
}

// configureShell دستور را در ویندوز با CmdLine خام اجرا می‌کند. روش معمول
// (exec.Command("cmd","/C",command)) کوتیشن‌ها را با \" escape می‌کند که cmd.exe
// نمی‌فهمد و دستورهایی مثل git commit -m "msg" خراب می‌شدند. همچنین پنجره‌ی
// کنسول سیاه نمایش داده نمی‌شود.
func configureShell(cmd *exec.Cmd, command string) {
	safe := escapeCmdExeQuotes(command)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// chcp 65001: خروجی UTF-8 تا متن فارسی خراب نشود
		CmdLine:       `cmd /S /C "chcp 65001 >nul && ` + safe + `"`,
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
