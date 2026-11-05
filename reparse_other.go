//go:build !windows

// روی یونیکس تنها پیوند نمادین معنا دارد؛ جانکشن/reparse وجود ندارد.
package main

import "os"

// isReparsePoint آیا مسیر یک پیوند نمادین است؟
func isReparsePoint(path string) bool {
	st, err := os.Lstat(path)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}
