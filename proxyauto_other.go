//go:build !windows

package main

// systemProxyFromRegistry روی سیستم‌های غیرویندوزی منبعی ندارد؛ حالت هوشمند
// فقط از متغیرهای محیطی HTTP(S)_PROXY استفاده می‌کند.
func systemProxyFromRegistry() (spec, override string, ok bool) {
	return "", "", false
}
