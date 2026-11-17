//go:build windows

// پیاده‌سازی داخلی «dir» برای سندباکس.
//
// چرا: cmd.exe برای سرصفحه‌ی dir (برچسب/سریال درایو) به
// GetVolumeInformation روی ریشه‌ی درایو نیاز دارد و توکن AppContainer بدون
// دسترسی Administrator به ACL ریشه راه ندارد؛ نتیجه «Access is denied.»
// حتی برای `dir /b` و حتی روی C:\Windows. این فایل همان لیست را در خود
// فرایند MyAI می‌سازد، اما مسیر را سخت به پوشه‌ی کاری محدود می‌کند تا
// ایزوله‌سازی دور زده نشود.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// sandboxDirOptions گزینه‌های پشتیبانی‌شده‌ی dir.
type sandboxDirOptions struct {
	bare    bool // /b: فقط نام‌ها
	recurse bool // /s: زیرپوشه‌ها
	path    string
}

// parseSandboxDirArgs آرگومان‌های dir را می‌خواند؛ پرچم‌های ناشناخته
// نادیده گرفته می‌شوند (رفتار محافظه‌کارانه: خروجی کامل لیست می‌شود).
func parseSandboxDirArgs(argv []string) sandboxDirOptions {
	opts := sandboxDirOptions{}
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "/") || strings.HasPrefix(a, "-") {
			flag := strings.ToLower(strings.TrimLeft(a, "/-"))
			if i := strings.IndexByte(flag, ':'); i >= 0 {
				flag = flag[:i]
			}
			switch flag {
			case "b":
				opts.bare = true
			case "s":
				opts.recurse = true
			}
			continue
		}
		if opts.path == "" {
			opts.path = a
		}
	}
	return opts
}

// sandboxRealDir مسیر را برای مقایسه‌ی «داخل پوشه‌ی کاری» حل می‌کند:
// EvalSymlinks مؤلفه‌های کوتاه 8.3 (C:\Users\RUNNER~1\…) و junction را به
// مسیر بلند واقعی برمی‌گرداند. اگر حل نشد، همان مسیر تمیزشده کافی است.
func sandboxRealDir(path string) string {
	if rp, err := filepath.EvalSymlinks(path); err == nil {
		return rp
	}
	return filepath.Clean(path)
}

// sandboxDirTarget مسیر هدف را حل و محدود به pwd می‌کند.
func sandboxDirTarget(pwd string, opts sandboxDirOptions) (string, error) {
	p := strings.TrimSpace(opts.path)
	switch {
	case p == "" || p == ".":
		p = pwd
	case !filepath.IsAbs(p):
		p = filepath.Join(pwd, p)
	}
	p = filepath.Clean(p)
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("File Not Found")
	}
	// هر دو طرف مقایسه باید حل‌شده باشند. پیش‌تر real (مسیر بلند) با pwd
	// خام مقایسه می‌شد؛ روی رانرهای ویندوز که Temp شان مؤلفه‌ی کوتاه 8.3
	// دارد (C:\Users\RUNNER~1\…) همین باعث می‌شد `dir` داخل پوشه‌ی کاریِ
	// خودش «Access is denied.» بگیرد (CI ویندوز آن را نشان داد).
	if !withinDir(real, sandboxRealDir(pwd)) {
		return "", fmt.Errorf("Access is denied.")
	}
	st, err := os.Stat(real)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("File Not Found")
	}
	return real, nil
}

// sandboxDirList یک پوشه (و با /s زیرپوشه‌ها) را چاپ می‌کند و آمار را
// برمی‌گرداند. خروجی شبیه cmd است اما بدون اطلاعات volume.
func sandboxDirList(b *strings.Builder, root string, opts sandboxDirOptions, top bool) (files, dirs int, bytes int64) {
	entries, err := os.ReadDir(root)
	if err != nil {
		// دلایل غیرمجوز (مسیر ناپیدا/نوع نادرست) نباید پشت متن گمراه‌کننده‌ی
		// «Access is denied.» پنهان شوند؛ روی CI فقط همین متن دیده می‌شد و
		// عیب‌یابی را غیرممکن می‌کرد.
		if errors.Is(err, fs.ErrPermission) {
			b.WriteString("Access is denied.\r\n")
		} else {
			b.WriteString(err.Error() + "\r\n")
		}
		return 0, 0, 0
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	if !opts.bare {
		fmt.Fprintf(b, " Directory of %s\r\n\r\n", root)
	}
	for _, e := range entries {
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if e.IsDir() {
			dirs++
			if opts.bare {
				b.WriteString(e.Name() + "\r\n")
			} else {
				fmt.Fprintf(b, "%s    <DIR>          %s\r\n", info.ModTime().Format("01/02/2006  03:04 PM"), e.Name())
			}
			continue
		}
		files++
		bytes += info.Size()
		if opts.bare {
			b.WriteString(e.Name() + "\r\n")
		} else {
			fmt.Fprintf(b, "%s %17d %s\r\n", info.ModTime().Format("01/02/2006  03:04 PM"), info.Size(), e.Name())
		}
	}
	if opts.recurse {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			f, d, by := sandboxDirList(b, filepath.Join(root, e.Name()), opts, false)
			files += f
			dirs += d
			bytes += by
		}
	}
	if top && !opts.bare {
		if opts.recurse || true {
			b.WriteString("\r\n")
		}
		fmt.Fprintf(b, "%16d File(s) %14d bytes\r\n", files, bytes)
		fmt.Fprintf(b, "%16d Dir(s)\r\n", dirs)
	}
	return files, dirs, bytes
}

// sandboxDirRedirect مقصد تغییرمسیر stdoutِ dir را حل می‌کند.
//
// supported=false یعنی این شکل را پشتیبانی نمی‌کنیم و دستور باید به cmd برود؛
// denied=true یعنی مقصد بیرون از پوشه‌ی کاری است و باید صریحاً رد شود.
// فقط «>» و «>>» (و شکل صریح «1>»/«1>>») پشتیبانی می‌شوند تا نوشتن فایل داخل
// پوشه‌ی کاری، با همان مرز امنیتی dir، انجام شود.
func sandboxDirRedirect(pwd string, rs []redirect) (path string, appendMode, supported, denied bool) {
	if len(rs) != 1 {
		return "", false, false, false
	}
	op := strings.TrimSpace(rs[0].op)
	switch op {
	case ">", "1>":
		appendMode = false
	case ">>", "1>>":
		appendMode = true
	default:
		return "", false, false, false
	}
	t := strings.TrimSpace(strings.Trim(rs[0].target, `"`))
	if t == "" || strings.ContainsAny(t, "$%`") {
		return "", false, false, false
	}
	if !filepath.IsAbs(t) {
		t = filepath.Join(pwd, t)
	}
	t = filepath.Clean(t)
	// پوشه‌ی والد باید وجود داشته باشد و (پس از حل symlink) داخل pwd بماند؛
	// خود فایل ممکن است هنوز ساخته نشده باشد.
	realParent, err := filepath.EvalSymlinks(filepath.Dir(t))
	if err != nil {
		return "", appendMode, true, true
	}
	realPwd := sandboxRealDir(pwd)
	if !withinDir(realParent, realPwd) {
		return "", appendMode, true, true
	}
	// اگر فایل از قبل symlink است، مقصد واقعی‌اش هم باید داخل pwd باشد.
	if fi, lerr := os.Lstat(t); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		real, rerr := filepath.EvalSymlinks(t)
		if rerr != nil || !withinDir(real, realPwd) {
			return "", appendMode, true, true
		}
		t = real
	}
	return t, appendMode, true, false
}

// sandboxBuiltinDir اگر کل دستور یک dir ساده باشد آن را خودش اجرا می‌کند و
// handled=true برمی‌گرداند. تغییرمسیر stdout به فایل («>»/«>>») هم پشتیبانی
// می‌شود، اما فقط داخل پوشه‌ی کاری؛ دستورهای مرکب دیگر به cmd می‌روند.
func sandboxBuiltinDir(command, pwd string) (out string, code int, handled bool, err error) {
	pc, ok := parseShellCommand(command, flavorCmd)
	if !ok || len(pc.simples) != 1 {
		return "", 0, false, nil
	}
	return sandboxDirSegment(&pc.simples[0], pwd)
}

// sandboxDirSegment همان اجرای dir برای یک segment تجزیه‌شده است (با
// تغییرمسیرهای همان segment)؛ زنجیره‌ها هم از همین استفاده می‌کنند.
func sandboxDirSegment(sc *simpleCommand, pwd string) (out string, code int, handled bool, err error) {
	if len(sc.argv) == 0 ||
		sc.hasExpansion || sc.hasSubshell || sc.background {
		return "", 0, false, nil
	}
	if strings.ToLower(strings.TrimSpace(sc.argv[0])) != "dir" {
		return "", 0, false, nil
	}
	redirectPath := ""
	appendMode := false
	if len(sc.redirects) > 0 {
		p, app, supported, denied := sandboxDirRedirect(pwd, sc.redirects)
		if !supported {
			return "", 0, false, nil
		}
		if denied {
			return "Access is denied.\r\n", 1, true, fmt.Errorf("exit status 1")
		}
		redirectPath, appendMode = p, app
	}
	opts := parseSandboxDirArgs(sc.argv)
	target, terr := sandboxDirTarget(pwd, opts)
	b := &strings.Builder{}
	if terr != nil {
		b.WriteString(terr.Error() + "\r\n")
		if redirectPath != "" {
			return "", 1, true, fmt.Errorf("exit status 1")
		}
		return b.String(), 1, true, fmt.Errorf("exit status 1")
	}
	if !opts.bare {
		b.WriteString(" Volume in drive has no label.\r\n Volume Serial Number is 0000-0000\r\n\r\n")
	}
	sandboxDirList(b, target, opts, true)
	if redirectPath != "" {
		flags := os.O_CREATE | os.O_WRONLY
		if appendMode {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
		}
		f, werr := os.OpenFile(redirectPath, flags, 0o600)
		if werr != nil {
			return "Access is denied.\r\n", 1, true, fmt.Errorf("exit status 1")
		}
		_, werr = f.WriteString(b.String())
		cerr := f.Close()
		if werr != nil || cerr != nil {
			return "Access is denied.\r\n", 1, true, fmt.Errorf("exit status 1")
		}
		// cmd در حالت تغییرمسیر چیزی روی stdout چاپ نمی‌کند.
		return "", 0, true, nil
	}
	return b.String(), 0, true, nil
}
