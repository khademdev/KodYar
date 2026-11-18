//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// configureShell دستور را در ویندوز با CmdLine خام اجرا می‌کند. روش معمول
// (exec.Command("cmd","/C",command)) کوتیشن‌ها را با \" escape می‌کند که cmd.exe
// نمی‌فهمد و دستورهایی مثل git commit -m "msg" خراب می‌شدند. همچنین پنجره‌ی
// کنسول سیاه نمایش داده نمی‌شود.
//
// نکتهٔ حیاتی: کوتیشن‌های داخل دستور نباید «خنثی» شوند. با /S، cmd فقط
// نخستین و آخرین کوتیشنِ کل رشته را برمی‌دارد و بقیه را دست‌نخورده به
// برنامه می‌دهد؛ دابل‌کردن کوتیشن (" → "") آن‌ها را به دو کاراکتر حرفی
// تبدیل می‌کرد و آرگومان‌های دارای فاصله می‌شکستند — مثلاً
// `python -c "import os; print('ok')"` فقط `import` را اجرا می‌کرد و
// `echo "hello world"` کوتیشن‌ها را چاپ می‌کرد.
func configureShell(cmd *exec.Cmd, command string) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// chcp 65001: خروجی UTF-8 تا متن فارسی خراب نشود
		CmdLine:       `cmd /S /C "chcp 65001 >nul && ` + command + `"`,
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
