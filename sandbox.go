// sandbox.go — سیاست ایزوله‌سازی اجرای دستورها (سندباکس).
//
// اجرای دستور در MyAI سه لایه دارد و این فایل لایه‌ی سوم را تعریف می‌کند:
//
//	۱) دروازه‌ی دسترسی (permissions.go/cmdgate.go): «چه دستوری؟» → allow/ask/deny
//	۲) بازبینی و تأیید کاربر (مودال اجازه، قاعده‌های ماندگار)
//	۳) سندباکس اجرا (این فایل + sandbox_windows.go): «با چه اختیاری اجرا شود؟»
//
// سندباکس واقعی روی ویندوز با AppContainer پیاده شده است: فرایند دستور داخل
// یک کانتینر سیستم‌عامل (SID اختصاصی AppContainer) اجرا می‌شود، به پوشه‌ی
// کاری دسترسی نوشتن دارد، بقیه‌ی فایل‌های کاربر فقط-خواندنی/ممنوع است و
// شبکه اختیاری است. اگر سندباکس روشن باشد ولی راه‌اندازی‌اش شکست بخورد،
// دستور **اجرا نمی‌شود** (fail-closed) — هرگز بی‌صدا بدون سندباکس اجرا نمی‌شود.
package main

import (
	"path/filepath"
	"strings"
	"sync"
)

// sandboxPolicy تصمیم اجرای یک دستور داخل سندباکس.
type sandboxPolicy struct {
	// Workspace پوشه‌ی کاری است که فرایند داخل سندباکس اجازه‌ی خواندن و
	// نوشتن کامل در آن را دارد (همان پوشه‌ی کاری ابزار).
	Workspace string
	// AllowNetwork اگر false باشد، توکن AppContainer قابلیت internetClient
	// نمی‌گیرد و هیچ اتصال شبکه‌ای از داخل سندباکس برقرار نمی‌شود.
	AllowNetwork bool
}

// sandboxEnabledLocked وضعیت سوییچ سندباکس را برمی‌گرداند. باید با a.mu
// نگه داشته شود.
//
// پیش‌فرض روشن است؛ فقط "off" صریح آن را خاموش می‌کند. ابزارهای نیازمند
// دستگاه NUL (go/git) به ACE روی \Device\Null نیاز دارند که با هر بوت
// بازنشانی می‌شود؛ اپ آن را در Startup کاربر ثبت و هنگام شروع اجرا می‌کند
// (sandbox_startup_windows.go).
func sandboxEnabledLocked(s *Settings) bool {
	if s == nil {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(s.SandboxMode), "off")
}

// sandboxExemptOnce برای لاگ‌کردن یک‌باره‌ی استثنای git.
var sandboxExemptOnce sync.Once

// sandboxGitSafeCompanion فقط دستورهای read-only را به‌عنوان همراه git مجاز
// می‌داند (مثل `git log | findstr x`). دستورهای داخلی مخرب cmd (del/rd/move/
// mklink/start/…) عمداً این‌جا نیستند تا استثنای git راهی برای اجرای
// دستورهای تغییردهنده بیرون از سندباکس نشود.
var sandboxGitSafeCompanion = map[string]bool{
	"echo": true, "dir": true, "type": true, "ver": true,
	"findstr": true, "find": true, "more": true, "sort": true, "where": true,
}

// sandboxCygpathReadOnly آیا کل دستور یک فراخوانی تنها و read-only از
// cygpath است؟ cygpath هیچ پرچم نویسنده‌ای ندارد، اما هر تغییرمسیر/pipe/
// زیرپوسته/پس‌زمینه یا segment دوم رد می‌شود تا استثنا راهی برای نوشتن یا
// اجرای دستور دیگر بیرون از سندباکس نشود.
func sandboxCygpathReadOnly(pc parsedCommand) bool {
	if len(pc.simples) != 1 || len(pc.seps) > 1 {
		return false
	}
	sc := pc.simples[0]
	if len(sc.argv) == 0 || len(sc.redirects) > 0 ||
		sc.hasExpansion || sc.hasSubshell || sc.background {
		return false
	}
	name := strings.ToLower(filepath.Base(strings.Trim(strings.TrimSpace(sc.argv[0]), `"`)))
	return strings.TrimSuffix(name, ".exe") == "cygpath"
}

// sandboxMSYSExempt آیا این دستور باید به‌خاطر ناسازگاری MSYS با AppContainer
// خارج از سندباکس اجرا شود؟
//
// ریشه‌ی مشکل (اندازه‌گیری‌شده): runtime‌ی MSYS (که Git for Windows بر آن
// سوار است) هنگام راه‌اندازی سعی می‌کند شیء نام‌دار حافظه‌ی مشترکش را در
// \BaseNamedObjects بسازد؛ AppContainer فقط به
// \Sessions\<n>\AppContainerNamedObjects\<SID> دسترسی دارد و نتیجه
// NtCreateDirectoryObject(...)=0xC0000022 است. این محدودیت خودِ ویندوز است
// و با ACL قابل رفع نیست. بنابراین git (و ترکیب‌های صرفاً git) و همچنین
// cygpathِ تنهای read-only (برای تبدیل مسیرها) آگاهانه و به‌صورت مستند خارج
// از AppContainer اجرا می‌شوند؛ دروازه‌ی دسترسی و مودال اجازه همچنان برقرار
// است. هر دستور دیگری (go/node/npm/… و هر cygpathِ همراه با تغییرمسیر یا
// دستور دیگر) سندباکس می‌ماند.
func sandboxMSYSExempt(command string) bool {
	pc, ok := parseShellCommand(command, flavorCmd)
	if !ok || len(pc.simples) == 0 {
		return false
	}
	// cygpath فقط به‌صورت تک‌دستور و بدون تغییرمسیر معاف است؛ تبدیل مسیر
	// باید داخل سندباکس هم ممکن باشد و نوشتن فایل بیرون از آن نه.
	if sandboxCygpathReadOnly(pc) {
		return true
	}
	hasGit := false
	for _, sc := range pc.simples {
		if len(sc.argv) == 0 {
			continue
		}
		if sc.hasExpansion || sc.hasSubshell || sc.background {
			return false
		}
		name := strings.ToLower(filepath.Base(strings.TrimSpace(sc.argv[0])))
		name = strings.TrimSuffix(name, ".exe")
		switch {
		case name == "git":
			hasGit = true
		case sandboxGitSafeCompanion[name]:
		default:
			return false
		}
	}
	return hasGit
}

func logSandboxMSYSExempt(a *App) {
	sandboxExemptOnce.Do(func() {
		logAppErrorFor(a, "sandbox", "git and read-only cygpath commands run outside AppContainer because the MSYS runtime cannot create its named objects inside one (Windows AppContainer limitation)")
	})
}

// sandboxPolicyForCommand سیاست سندباکس برای یک دستور در پوشه‌ی dir را
// می‌سازد؛ nil یعنی دستور باید بدون سندباکس اجرا شود (خاموش، پلتفرم بدون
// پیاده‌سازی، یا استثنای مستند git/MSYS). هنگام فراخوانی نباید a.mu را در
// دست داشته باشید.
func (a *App) sandboxPolicyForCommand(dir, command string) *sandboxPolicy {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	on := sandboxEnabledLocked(a.settings)
	noNet := a.settings != nil && a.settings.SandboxNoNetwork
	a.mu.Unlock()
	if !on || !sandboxSupported() {
		return nil
	}
	if sandboxMSYSExempt(command) {
		logSandboxMSYSExempt(a)
		return nil
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	return &sandboxPolicy{Workspace: dir, AllowNetwork: !noNet}
}

// mcpSandboxPolicy سیاست سندباکس برای سرورهای MCP را برمی‌گرداند.
// سرور MCP یک فرایند بلندمدت با stdio دوطرفه است و در پوشه‌ی کاری اجرا
// می‌شود؛ روشن‌بودن سندباکس یعنی MCP هم داخل AppContainer بالا می‌آید و
// اگر آماده‌سازی شکست بخورد، سرور اجرا نمی‌شود (fail-closed).
func (a *App) mcpSandboxPolicy() (*sandboxPolicy, string, bool) {
	if a == nil {
		return nil, "", false
	}
	a.mu.Lock()
	on := sandboxEnabledLocked(a.settings)
	noNet := a.settings != nil && a.settings.SandboxNoNetwork
	ws := ""
	if a.settings != nil {
		ws = strings.TrimSpace(a.settings.WorkspacePath)
	}
	a.mu.Unlock()
	if !on || !sandboxSupported() || ws == "" {
		return nil, "", false
	}
	return &sandboxPolicy{Workspace: ws, AllowNetwork: !noNet}, ws, true
}

// SetSandbox روشن/خاموش کردن سندباکس و اجازه‌ی شبکه را ذخیره می‌کند.
// این متد از UI تنظیمات (تب پوشه) صدا زده می‌شود. روشن‌کردن آگاهانه است
// (opt-in) چون پیاده‌سازی AppContainer هنوز روی همه‌ی نصب‌ها با ابزارهای
// خارجی سازگار نیست.
func (a *App) SetSandbox(enabled, allowNetwork bool) error {
	defer a.recoverBound("SetSandbox")
	a.mu.Lock()
	if enabled {
		a.settings.SandboxMode = "on"
	} else {
		a.settings.SandboxMode = "off"
	}
	a.settings.SandboxNoNetwork = !allowNetwork
	err := a.saveSettings()
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if !enabled && sandboxSupported() {
		// وقتی کاربر سندباکس را خاموش می‌کند، ACEهایی که برای SID سندباکس
		// روی پوشه‌ی کاری/مسیر ابزارها اضافه شده بود پس گرفته می‌شوند تا
		// دسترسی اضافه باقی نماند. شکست پس‌گرفتن مانع خاموش‌شدن نمی‌شود،
		// اما در لاگ خطاها ثبت می‌شود.
		if rerr := sandboxRevokeGrants(); rerr != nil {
			logAppErrorFor(a, "sandbox", "revoking sandbox ACEs: "+rerr.Error())
		}
	}
	return nil
}
