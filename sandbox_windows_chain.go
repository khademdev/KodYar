//go:build windows

// اجرای زنجیره‌های مرکب در سندباکس بدون سپردن «dir» به cmd.
//
// چرا: cmd برای dir به اطلاعات volume ریشه‌ی درایو نیاز دارد که توکن
// AppContainer ندارد؛ پس dir داخل «dir && go build» هم شکست می‌خورد. این
// فایل زنجیره‌های &&/||/& را که حداقل یک dir دارند خودش اجرا می‌کند:
// dir/echo/type را داخلی (با محدودیت مسیر) و بقیه‌ی segmentها را از همان
// مسیر مستقیم/سندباکس. pipe و تغییرمسیر همچنان به cmd می‌روند.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sandboxChainSegmentName نام نرمال‌شده‌ی segment را برمی‌گرداند.
func sandboxChainSegmentName(sc *simpleCommand) string {
	name := strings.Trim(strings.TrimSpace(sc.argv[0]), `"`)
	name = strings.ToLower(filepath.Base(name))
	return strings.TrimSuffix(name, ".exe")
}

// sandboxEchoBuiltin رفتار ساده‌ی echo cmd را تقلید می‌کند.
func sandboxEchoBuiltin(argv []string) string {
	if len(argv) == 1 {
		return "ECHO is on.\r\n"
	}
	arg := strings.Join(argv[1:], " ")
	if arg == "." {
		return "\r\n"
	}
	if strings.EqualFold(arg, "off") || strings.EqualFold(arg, "on") {
		return ""
	}
	return arg + "\r\n"
}

// sandboxTypeBuiltin مثل type ولی فقط داخل پوشه‌ی کاری (مرز امنیتی سندباکس).
func sandboxTypeBuiltin(pwd string, argv []string) (string, int, error) {
	if len(argv) < 2 {
		return "The syntax of the command is incorrect.\r\n", 1, fmt.Errorf("exit status 1")
	}
	p := strings.Trim(strings.TrimSpace(argv[1]), `"`)
	if !filepath.IsAbs(p) {
		p = filepath.Join(pwd, p)
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "File Not Found\r\n", 1, fmt.Errorf("exit status 1")
	}
	if !withinDir(real, pwd) {
		return "Access is denied.\r\n", 1, fmt.Errorf("exit status 1")
	}
	b, err := os.ReadFile(real)
	if err != nil {
		return "Access is denied.\r\n", 1, fmt.Errorf("exit status 1")
	}
	return string(b), 0, nil
}

// sandboxRunChain زنجیره‌ی &&/||/& را اگر همه‌ی segmentها قابل‌اجرا باشند و
// حداقل یکی dir باشد، خودش اجرا می‌کند (handled=true).
func sandboxRunChain(ctx context.Context, dir, command string, timeout time.Duration, pol *sandboxPolicy) (string, int, bool, error) {
	pc, ok := parseShellCommand(command, flavorCmd)
	if !ok || len(pc.simples) < 2 {
		return "", 0, false, nil
	}
	hasDir := false
	for i := range pc.simples {
		sc := &pc.simples[i]
		if len(sc.argv) == 0 ||
			sc.hasExpansion || sc.hasSubshell || sc.background {
			return "", 0, false, nil
		}
		if i < len(pc.simples)-1 {
			sep := ""
			if i < len(pc.seps) {
				sep = pc.seps[i]
			}
			if sep != "&&" && sep != "||" && sep != "&" {
				return "", 0, false, nil // pipe یا جداکننده‌ی نامعلوم → cmd
			}
		}
		switch name := sandboxChainSegmentName(sc); name {
		case "dir":
			hasDir = true
			// تغییرمسیر stdout فقط وقتی پشتیبانی می‌شود که «>»/«>>» و داخل
			// پوشه‌ی کاری باشد؛ وگرنه کل زنجیره به cmd می‌رود.
			if len(sc.redirects) > 0 {
				if _, _, supported, _ := sandboxDirRedirect(dir, sc.redirects); !supported {
					return "", 0, false, nil
				}
			}
		case "echo", "type":
			if len(sc.redirects) > 0 {
				return "", 0, false, nil
			}
		default:
			if len(sc.redirects) > 0 {
				return "", 0, false, nil
			}
			if _, _, direct := sandboxLaunchTarget(composeWindowsCommandLine(sc.argv), dir); !direct {
				return "", 0, false, nil
			}
		}
	}
	if !hasDir {
		return "", 0, false, nil
	}

	var b strings.Builder
	last := 0
	for i := range pc.simples {
		sc := &pc.simples[i]
		if i > 0 {
			switch pc.seps[i-1] {
			case "&&":
				if last != 0 {
					return b.String(), last, true, nil
				}
			case "||":
				if last == 0 {
					continue
				}
			}
		}
		segCmd := composeWindowsCommandLine(sc.argv)
		var text string
		var segErr error
		switch name := sandboxChainSegmentName(sc); name {
		case "dir":
			text, last, _, segErr = sandboxDirSegment(sc, dir)
		case "echo":
			text = sandboxEchoBuiltin(sc.argv)
			last = 0
		case "type":
			text, last, segErr = sandboxTypeBuiltin(dir, sc.argv)
		default:
			text, last, segErr = runSandboxedShell(ctx, dir, segCmd, timeout, nil, pol)
		}
		b.WriteString(text)
		if segErr != nil && last == 0 {
			last = 1
		}
	}
	return b.String(), last, true, nil
}
