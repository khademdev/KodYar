//go:build windows

// تشخیص reparse point (symlink/junction/mount) — برای اینکه بازگردانی
// چک‌پوینت هرگز از یک پوشه‌ی پیوندی به بیرون پروژه ننویسد.
package main

import "golang.org/x/sys/windows"

// isReparsePoint آیا مسیر یک reparse point (پیوند/جانکشن) است؟
func isReparsePoint(path string) bool {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return false
	}
	return attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
