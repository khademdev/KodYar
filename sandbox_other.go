//go:build !windows && !linux

package main

import (
	"context"
	"errors"
	"time"
)

// sandboxSupported: سندباکس اجرایی فعلاً فقط برای ویندوز (AppContainer)
// پیاده شده است؛ روی لینوکس/مک اجرای دستور با همان دروازه‌ی دسترسی و
// بدون ایزوله‌سازی سیستمی انجام می‌شود.
func sandboxSupported() bool { return false }

// sandboxRevokeGrants روی پلتفرم‌های بدون سندباکس چیزی برای پس‌گرفتن نیست.
func sandboxRevokeGrants() error { return nil }

// sandboxPlatformStartup روی پلتفرم‌های بدون سندباکس کاری برای انجام نیست.
func sandboxPlatformStartup() error { return nil }

// runSandboxedShell روی پلتفرم‌های غیرویندوز در دسترس نیست. سیاست‌ساز
// (sandboxPolicyForCommand) در این حالت nil برمی‌گرداند، پس این تابع فقط
// برای کامل‌بودن قرارداد است.
func runSandboxedShell(ctx context.Context, dir, command string, timeout time.Duration, onOutput func([]byte), pol *sandboxPolicy) (string, int, error) {
	return "", -1, errors.New("command sandbox is only available on Windows")
}
