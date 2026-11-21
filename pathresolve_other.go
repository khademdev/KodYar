//go:build !windows

// روی سکوهای غیرویندوزی، filepath.EvalSymlinks همه‌ی symlinkها را حل می‌کند
// و junction/reparse point معنا ندارد؛ پس قلابِ مسیر نهایی لازم نیست.
package main

func finalPathForCompare(p string) (string, bool) { return "", false }
