//go:build windows

package main

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// systemProxyFromRegistry پروکسی سیستمی ویندوز (WinINET) را می‌خواند — همان
// تنظیمی که نرم‌افزارهای VPN/پروکسی با گزینه‌ی «System Proxy» تغییر می‌دهند و
// مرورگرها استفاده می‌کنند، اما ProxyFromEnvironment گو آن را نمی‌بیند.
// خروجی: spec رشته‌ی ProxyServer، override رشته‌ی ProxyOverride، ok = فعال.
func systemProxyFromRegistry() (spec, override string, ok bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return "", "", false
	}
	defer k.Close()
	enable, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || enable == 0 {
		return "", "", false
	}
	spec, _, err = k.GetStringValue("ProxyServer")
	if err != nil || strings.TrimSpace(spec) == "" {
		// فقط PAC تنظیم شده (AutoConfigURL) — ارزیابی PAC پشتیبانی نمی‌شود؛
		// به محیط و بعد مستقیم برمی‌گردیم.
		return "", "", false
	}
	override, _, _ = k.GetStringValue("ProxyOverride")
	return spec, override, true
}
