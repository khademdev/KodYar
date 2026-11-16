//go:build windows

// اجرای مستقیم برای «دستورهای ساده» — بدون cmd.exe.
//
// چرا: cmd.exe داخل AppContainer هندل‌های std خود را به فرزندانش (go، git،
// node، …) درست نمی‌رساند؛ اندازه‌گیری شد که فرزند به‌جای pipe والد هندل
// دیگری (NUL) می‌گیرد و خروجی بی‌صدا گم می‌شود (exit 0 با خروجی خالی).
// راه‌حل: اگر دستور یک برنامه‌ی ساده با آرگومان‌ها باشد (بدون |، &&،
// تغییرمسیر، زیرپوسته، گسترش متغیر)، با همان parseShellCommand به argv
// تبدیل و مستقیماً CreateProcess می‌شود؛ آن‌وقت handle list در همان
// CreateProcess اولیه اعمال می‌شود و pipe برای فرزند معتبر است.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// sandboxLaunchTarget اگر دستور ساده باشد، مسیر exe و command line آماده
// را برمی‌گرداند (direct=true). در غیر این صورت (دستور مرکب/تغییرمسیر/…)
// false برمی‌گرداند تا مسیر cmd استفاده شود.
func sandboxLaunchTarget(command, dir string) (exe, cmdLine string, direct bool) {
	pc, ok := parseShellCommand(command, flavorCmd)
	if !ok || len(pc.simples) != 1 {
		return "", "", false
	}
	sc := pc.simples[0]
	if len(sc.argv) == 0 || len(sc.redirects) > 0 ||
		sc.hasExpansion || sc.hasSubshell || sc.background {
		return "", "", false
	}
	name := sc.argv[0]
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".bat") || strings.HasSuffix(lower, ".cmd") {
		return "", "", false // این‌ها فقط با cmd اجرا می‌شوند
	}
	if isCmdBuiltin(name) {
		// دستورهای داخلی cmd (dir، type، echo، …) را نباید از PATH اجرا کرد:
		// بعضی ابزارها (مثل coreutils در Git for Windows) هم‌نام هستند و
		// بیرون از cmd معنای متفاوتی دارند (dir.exe لیست خام می‌دهد و
		// داخل AppContainer با STATUS_DLL_INIT_FAILED می‌میرد).
		return "", "", false
	}
	var resolved string
	switch {
	case filepath.IsAbs(name):
		resolved = name
	case strings.ContainsAny(name, `\/`):
		resolved = filepath.Join(dir, name)
	default:
		p, err := exec.LookPath(name)
		if err != nil {
			return "", "", false
		}
		resolved = p
	}
	if st, err := os.Stat(resolved); err != nil || st.IsDir() {
		return "", "", false
	}
	args := make([]string, 0, len(sc.argv))
	args = append(args, resolved)
	args = append(args, sc.argv[1:]...)
	return resolved, composeWindowsCommandLine(args), true
}

// cmdBuiltins نام دستورهای داخلی cmd.exe که اجرای مستقیم‌شان از PATH اشتباه
// است (یا اصلاً فایل اجرایی ندارند). فهرست محافظه‌کارانه است: هر نامی که در
// آن باشد به cmd می‌رود، پس رفتارش دقیقاً همان cmd است.
var cmdBuiltins = map[string]bool{
	"assoc": true, "break": true, "call": true, "cd": true, "chdir": true,
	"cls": true, "color": true, "copy": true, "date": true, "del": true,
	"dir": true, "echo": true, "endlocal": true, "erase": true, "exit": true,
	"for": true, "ftype": true, "goto": true, "if": true, "md": true,
	"mkdir": true, "mklink": true, "move": true, "path": true, "pause": true,
	"popd": true, "prompt": true, "pushd": true, "rd": true, "rem": true,
	"ren": true, "rename": true, "rmdir": true, "set": true, "setlocal": true,
	"shift": true, "start": true, "time": true, "title": true, "type": true,
	"ver": true, "verify": true, "vol": true,
}

// isCmdBuiltin آیا این نام یک دستور داخلی cmd است؟ نام دارای مسیر یا پسوند
// (مثل «.\dir.exe») قصد صریح اجرای فایل است و داخلی حساب نمی‌شود.
func isCmdBuiltin(name string) bool {
	if strings.ContainsAny(name, `\\/`) {
		return false
	}
	if strings.Contains(name, ".") {
		return false
	}
	return cmdBuiltins[strings.ToLower(name)]
}

// composeWindowsCommandLine آرگومان‌ها را با قواعد نقل‌قول MSVCRT/CreateProcess
// به یک command line تبدیل می‌کند (همان الگوریتم syscall.ComposeCommandLine).
func composeWindowsCommandLine(args []string) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(quoteWindowsArg(a))
	}
	return b.String()
}

func quoteWindowsArg(a string) string {
	if a != "" && !strings.ContainsAny(a, " \t\n\v\"") {
		return a
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range a {
		switch r {
		case '\\':
			slashes++
			continue
		case '"':
			b.WriteString(strings.Repeat("\\", slashes*2+1))
			b.WriteRune(r)
			slashes = 0
			continue
		}
		if slashes > 0 {
			b.WriteString(strings.Repeat("\\", slashes))
			slashes = 0
		}
		b.WriteRune(r)
	}
	if slashes > 0 {
		b.WriteString(strings.Repeat("\\", slashes*2))
	}
	b.WriteByte('"')
	return b.String()
}
