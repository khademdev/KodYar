//go:build !windows

package main

// installSandboxNulGrant روی پلتفرم‌های غیرویندوز کاری ندارد (سندباکس
// AppContainer فقط ویندوزی است و sandboxSupported هم false است).
func installSandboxNulGrant() error { return nil }
