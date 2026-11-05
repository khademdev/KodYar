//go:build !windows

// روی یونیکس rename اتمیک و بدون قفل‌های ویندوز است؛ تلاش مجدد لازم نیست.
package main

import "os"

// renameWithRetry همان os.Rename است (بدون تلاش مجدد).
func renameWithRetry(from, to string) error { return os.Rename(from, to) }
