//go:build !windows

// sandbox_nul_other.go — روی پلتفرم‌های بدون AppContainer، وضعیت NUL معنایی
// ندارد؛ همان متدهای بایندشده با پاسخ «در دسترس نیست» تعریف می‌شوند تا UI
// روی همه‌ی پلتفرم‌ها یکسان کار کند.
package main

import "fmt"

// SandboxNulStatus روی غیرویندوز: سندباکس ویندوزی وجود ندارد.
func (a *App) SandboxNulStatus() map[string]any {
	defer a.recoverBound("SandboxNulStatus")
	return map[string]any{
		"available": false,
		"granted":   false,
		"detail":    "سندباکس AppContainer فقط در ویندوز در دسترس است",
		"task":      "",
	}
}

// SandboxNulEnable روی غیرویندوز: کاری برای انجام نیست.
func (a *App) SandboxNulEnable() (string, error) {
	defer a.recoverBound("SandboxNulEnable")
	return "", fmt.Errorf("سندباکس AppContainer فقط در ویندوز در دسترس است")
}
