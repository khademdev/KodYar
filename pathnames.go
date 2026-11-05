// اعتبارسنجی نام فایل و مسیر برای ویندوز.
//
// نام‌های رزروشده (CON، NUL، COM1…)، نقطه/فاصله‌ی پایانی و نویسه‌های ممنوع
// روی ویندوز باعث خطاهای مبهم یا رفتار غافلگیرکننده (دستگاه‌های شبه‌فایل،
// نام‌های بریده) می‌شوند. این توابع فقط در ویندوز اعمال می‌شوند، اما روی
// همه‌ی سکوها تست‌پذیرند (validatePathName تابع خالص است).
package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// windowsMaxPath حداکثر طول مسیر مطلق در ویندوز بدون پیشوند \\?\.
const windowsMaxPath = 259

// windowsReservedNames نام‌های دستگاهیِ ممنوع (بدون توجه به بزرگی/کوچکی و
// پسوند: CON.txt هم ممنوع است).
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// validatePathName یک جزء مسیر (نام فایل/پوشه) را برای ویندوز بررسی می‌کند.
// خطا انگلیسی و روشن است تا هم مدل و هم لاگ آن را بفهمند.
func validatePathName(name string) error {
	if name == "" {
		return fmt.Errorf("invalid path: empty file name")
	}
	if strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
		return fmt.Errorf("invalid file name %q: Windows does not allow names ending with a space or a dot", name)
	}
	if i := strings.IndexAny(name, `<>:"|?*`); i >= 0 {
		return fmt.Errorf("invalid file name %q: character %q is not allowed on Windows", name, string(name[i]))
	}
	for _, r := range name {
		if r < 0x20 {
			return fmt.Errorf("invalid file name %q: control characters are not allowed", name)
		}
	}
	base := name
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	upper := strings.ToUpper(strings.TrimRight(base, " "))
	if windowsReservedNames[upper] {
		return fmt.Errorf("invalid file name %q: %q is a reserved device name on Windows", name, base)
	}
	return nil
}

// validatePathForWrite مسیر مقصد نوشتن را بررسی می‌کند: طول مسیر ویندوز و
// همه‌ی اجزای آن. روی سکوهای دیگر فقط نام جزء آخر بررسی می‌شود (نویسه‌های
// ممنوع ویندوز هنوز برای پرونده‌های قابل‌انتقال معنا دارند).
func validatePathForWrite(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("invalid path: empty")
	}
	if !isWindows() {
		// قواعد نام/طول ویندوز روی سکوهای دیگر معنا ندارد و نباید نوشتن
		// پرونده‌های قانونی POSIX را رد کند؛ validatePathName جداگانه تست می‌شود.
		return nil
	}
	if len(path) > windowsMaxPath {
		return fmt.Errorf("path is too long (%d characters; Windows allows at most %d without the \\\\?\\ prefix)", len(path), windowsMaxPath)
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			continue
		}
		if len(part) == 2 && part[1] == ':' {
			continue // نامه‌ی درایو
		}
		if err := validatePathName(part); err != nil {
			return err
		}
	}
	return validatePathName(filepath.Base(path))
}
