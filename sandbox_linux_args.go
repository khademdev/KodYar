// ساخت آرگومان‌های backend لینوکس (bubblewrap).
//
// این فایل عمداً بدون build tag است تا منطق خالصِ ساخت آرگومان/محیط روی هر
// پلتفرمی (از جمله ماشین توسعه‌ی ویندوز) تست شود؛ اجرای واقعی bwrap فقط در
// sandbox_linux.go و روی لینوکس انجام می‌شود.
package main

import (
	"sort"
	"strings"
)

// sandboxLinuxAllowedEnv متغیرهای محیطی بی‌خطر که به فرایند سندباکس‌شده
// می‌رسند. هیچ کلید/توکنی (OPENAI_API_KEY و مانند آن) عبور نمی‌کند.
var sandboxLinuxAllowedEnv = map[string]bool{
	"PATH": true, "LANG": true, "LANGUAGE": true, "LC_ALL": true,
	"LC_CTYPE": true, "LC_MESSAGES": true, "TERM": true, "TZ": true,
}

// sandboxLinuxEnvVars محیط حداقلی سندباکس را می‌سازد: فقط allowlist، به‌همراه
// HOME/TMPDIR ایزوله و نشانگرهای سندباکس. خروجی مرتب و به شکل KEY=VALUE است.
func sandboxLinuxEnvVars(env []string, home string, allowNetwork bool) []string {
	merged := map[string]string{}
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		k := kv[:i]
		if !sandboxLinuxAllowedEnv[strings.ToUpper(k)] {
			continue
		}
		merged[strings.ToUpper(k)] = kv[i+1:]
	}
	netFlag := "0"
	if allowNetwork {
		netFlag = "1"
	}
	merged["HOME"] = home
	merged["TMPDIR"] = strings.TrimRight(home, "/") + "/tmp"
	merged["MYAI_SANDBOX"] = "1"
	merged["MYAI_SANDBOX_NETWORK"] = netFlag
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+merged[k])
	}
	return out
}

// sandboxLinuxBwrapArgs آرگومان‌های کامل bwrap را می‌سازد:
// کل فایل‌سیستم فقط-خواندنی، پوشه‌ی کاری و خانه‌ی ایزوله قابل نوشتن،
// /tmp به‌صورت tmpfs جدا، شبکه فقط با اجازه، محیط پاک و allowlist‌شده،
// بدون capability، و اجرای «/bin/sh -c command».
func sandboxLinuxBwrapArgs(ws, home string, allowNetwork bool, command string, env []string) []string {
	return sandboxLinuxBwrapExecArgs(ws, home, allowNetwork, []string{"/bin/sh", "-c", command}, env)
}

// sandboxLinuxBwrapExecArgs همان sandboxLinuxBwrapArgs است اما argv دقیق را
// (بدون شل) اجرا می‌کند؛ برای سرورهای MCP بلندمدت لازم است.
func sandboxLinuxBwrapExecArgs(ws, home string, allowNetwork bool, argv []string, env []string) []string {
	args := []string{
		"--die-with-parent",
		"--new-session",
		"--unshare-user",
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
		"--unshare-cgroup-try",
	}
	if !allowNetwork {
		args = append(args, "--unshare-net")
	}
	args = append(args,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
		"--bind", ws, ws,
		"--bind", home, home,
		"--chdir", ws,
		"--clearenv",
	)
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		args = append(args, "--setenv", kv[:i], kv[i+1:])
	}
	args = append(args, "--cap-drop", "ALL", "--")
	return append(args, argv...)
}
