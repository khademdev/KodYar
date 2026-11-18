//go:build windows

// sandbox_nul_windows.go — کشف و رفع نیاز \Device\Null برای سندباکس.
//
// چرا: توکن AppContainer طبق مدل امنیتی ویندوز فقط از ACEهایی اجازه می‌گیرد
// که صریحاً برای Package SID (یا Capability SID) نوشته شده باشند؛ SIDهای
// کاربر/Everyone در این access check نادیده گرفته می‌شوند. دستگاه
// \Device\Null هم DACL پیش‌فرضی دارد که چنین ACEای ندارد و آن DACL با هر بوت
// بازنشانی می‌شود. نتیجه: `go`/`git` داخل سندباکس با
// «open NUL: Access is denied» می‌میرند.
//
// تغییر DACL یک شیء کرنل به WRITE_DAC نیاز دارد که بدون Administrator ممکن
// نیست (MS خودش در MXC همین را می‌گوید: یک‌بار در هر بوت از یک بستر مرتفع).
// این فایل سه قطعه را کنار هم می‌گذارد:
//
//	۱) probeSandboxNul — سنجش واقعی: یک دستور کوچک داخل AppContainer اجرا
//	   می‌شود و می‌بینیم آیا نوشتن روی NUL ممکن است (نه خواندن DACL با
//	   توکن کاربر، که رفتار AppContainer را شبیه‌سازی نمی‌کند).
//	۲) SandboxNulStatus / SandboxNulEnable — متدهای بایندشده برای UI.
//	۳) launchElevatedNulEnable — اجرای یک‌بارهٔ helper با UAC که ACE را
//	   اعمال و یک Task سیستمی ONSTART می‌سازد؛ بعد از آن هیچ پرامپتی لازم نیست.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

//go:embed scripts/enable-sandbox-nul.ps1
var sandboxNulEnableScript string

const (
	// sandboxNulSystemTaskName نام Task سیستمی که در هر بوت ACE را برمی‌گرداند.
	sandboxNulSystemTaskName = "MyAI Sandbox NUL (System)"
	// sandboxNulStateName فایل وضعیت نوشته‌شده توسط helper مرتفع.
	sandboxNulStateName = "nul-grant.json"
	// sandboxNulProbeCommand تنها آزمون معتبر: نوشتن روی NUL مثل go/git.
	sandboxNulProbeCommand = `echo myai-nul-probe>NUL`
)

// nulGrantState وضعیت آخرین اجرای helper مرتفع.
type nulGrantState struct {
	OK    bool   `json:"ok"`
	At    string `json:"at,omitempty"`
	Task  string `json:"task,omitempty"`
	Error string `json:"error,omitempty"`
}

var (
	nulProbeMu     sync.Mutex
	nulProbeDone   bool
	nulProbeOK     bool
	nulProbeDetail string
	nulProbeActive bool
)

// nulGrantStatePath مسیر فایل وضعیت helper.
func nulGrantStatePath() string { return filepath.Join(sandboxCacheDir(), sandboxNulStateName) }

// readNulGrantState فایل وضعیت را می‌خواند (نبودِ فایل = هرگز اجرا نشده).
func readNulGrantState(path string) (nulGrantState, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nulGrantState{}, false
	}
	var s nulGrantState
	if json.Unmarshal(b, &s) != nil {
		return nulGrantState{}, false
	}
	return s, true
}

// probeSandboxNul یک دستور کوتاه را داخل AppContainer اجرا می‌کند و مشخص
// می‌کند آیا NUL برای سندباکس قابل نوشتن است. پوشهٔ کاری موقتی است تا
// پوشهٔ کاربر دست‌کاری نشود؛ ACEهای آن پوشه هنگام پاک‌شدن هم می‌روند.
func probeSandboxNul() (bool, string) {
	nulProbeMu.Lock()
	if nulProbeActive {
		// جلوگیری از بازگشت: خودِ اجرای سندباکس‌شده ممکن است راهنما بسازد.
		nulProbeMu.Unlock()
		return false, "در حال بررسی"
	}
	nulProbeActive = true
	nulProbeMu.Unlock()
	defer func() {
		nulProbeMu.Lock()
		nulProbeActive = false
		nulProbeMu.Unlock()
	}()

	ws, err := os.MkdirTemp("", "myai-nul-probe-")
	if err != nil {
		return false, "ساخت پوشهٔ بررسی ناموفق بود: " + err.Error()
	}
	defer os.RemoveAll(ws)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, code, rerr := runSandboxedShell(ctx, ws, sandboxNulProbeCommand, 45*time.Second, nil,
		&sandboxPolicy{Workspace: ws, AllowNetwork: false})
	text := strings.TrimSpace(out)
	low := strings.ToLower(text)

	switch {
	case strings.Contains(low, "access is denied"):
		return false, "نوشتن روی NUL داخل سندباکس رد شد (Access is denied)"
	case code == 0 && rerr == nil:
		return true, "NUL داخل سندباکس باز می‌شود"
	}
	msg := text
	if msg == "" && rerr != nil {
		msg = rerr.Error()
	}
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", code)
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	// سندباکس راه نیفتاد یا خطای دیگری بود: نتیجه «اعمال نشده» است، اما
	// علتش هم گفته می‌شود تا تشخیص سخت نباشد.
	return false, "بررسی ناموفق: " + msg
}

// sandboxNulProbeCached نتیجهٔ سنجش را یک‌بار انجام و کش می‌کند (force = دوباره).
func sandboxNulProbeCached(force bool) (bool, string) {
	nulProbeMu.Lock()
	if nulProbeDone && !force {
		ok, detail := nulProbeOK, nulProbeDetail
		nulProbeMu.Unlock()
		return ok, detail
	}
	nulProbeMu.Unlock()

	ok, detail := probeSandboxNul()

	nulProbeMu.Lock()
	nulProbeDone, nulProbeOK, nulProbeDetail = true, ok, detail
	nulProbeMu.Unlock()
	return ok, detail
}

// invalidateSandboxNulProbe بعد از فعال‌سازی/تغییر وضعیت، سنجش بعدی تازه شود.
func invalidateSandboxNulProbe() {
	nulProbeMu.Lock()
	nulProbeDone = false
	nulProbeMu.Unlock()
}

// sandboxNulDeniedHint اگر خروجی یک دستور نشان دهد NUL بسته است و راه‌حل هم
// اعمال نشده، یک جملهٔ راهنما برمی‌گرداند؛ وگرنه رشتهٔ تهی.
func sandboxNulDeniedHint(text string) string {
	low := strings.ToLower(text)
	if !strings.Contains(low, "access is denied") {
		return ""
	}
	if !strings.Contains(low, "nul") && !strings.Contains(low, "/dev/null") {
		return ""
	}
	nulProbeMu.Lock()
	active, known, granted := nulProbeActive, nulProbeDone, nulProbeOK
	nulProbeMu.Unlock()
	if active {
		return ""
	}
	if known && granted {
		return ""
	}
	return `راهنما: مجوز \\Device\\Null برای سندباکس اعمال نشده است — تنظیمات → پوشه‌ی کاری → «فعال‌سازی دسترسی NUL» را یک‌بار با تأیید Administrator اجرا کنید.`
}

// SandboxNulStatus متد بایندشده: وضعیت مجوز NUL برای UI.
// available=false یعنی سندباکس در این پلتفرم نیست؛ granted=false یعنی
// go/git داخل سندباکس NUL را باز نمی‌توانند کرد.
func (a *App) SandboxNulStatus() map[string]any {
	defer a.recoverBound("SandboxNulStatus")
	res := map[string]any{
		"available": sandboxSupported(),
		"granted":   false,
		"detail":    "",
		"task":      sandboxNulSystemTaskName,
	}
	if !sandboxSupported() {
		res["detail"] = "سندباکس AppContainer فقط در ویندوز در دسترس است"
		return res
	}
	ok, detail := sandboxNulProbeCached(false)
	res["granted"] = ok
	res["detail"] = detail
	if st, found := readNulGrantState(nulGrantStatePath()); found {
		res["lastAttempt"] = st.At
		res["lastOK"] = st.OK
		res["lastError"] = st.Error
	}
	return res
}

// SandboxNulEnable متد بایندشده: اجرای یک‌بارهٔ helper با UAC.
// بعد از تأیید کاربر، helper خودش ACE را اعمال می‌کند، Task سیستمی ONSTART را
// می‌سازد و مسیرهای قدیمی Startup را پاک می‌کند.
func (a *App) SandboxNulEnable() (string, error) {
	defer a.recoverBound("SandboxNulEnable")
	if !sandboxSupported() {
		return "", fmt.Errorf("سندباکس AppContainer فقط در ویندوز در دسترس است")
	}
	if err := launchElevatedNulEnable(); err != nil {
		return "", err
	}
	invalidateSandboxNulProbe()
	return "پنجرهٔ تأیید ویندوز باز شد — پس از تأیید، وضعیت به‌روز می‌شود.", nil
}

// launchElevatedNulEnable اسکریپت‌ها را می‌نویسد و helper را با
// ShellExecute("runas") اجرا می‌کند؛ UAC فقط همین یک‌بار پرسیده می‌شود.
func launchElevatedNulEnable() error {
	base := sandboxCacheDir()
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	grantPath := filepath.Join(base, "grant-sandbox-nul.ps1")
	if err := writeIfChanged(grantPath, []byte(sandboxNulScript), 0o600); err != nil {
		return err
	}
	helperPath := filepath.Join(base, "enable-sandbox-nul.ps1")
	if err := writeIfChanged(helperPath, []byte(sandboxNulEnableScript), 0o600); err != nil {
		return err
	}

	args := "-NoProfile -ExecutionPolicy Bypass -File " + psQuote(helperPath) +
		" -Grant " + psQuote(grantPath) +
		" -StateDir " + psQuote(base) +
		" -TaskName " + psQuote(sandboxNulSystemTaskName) +
		" -LegacyTask " + psQuote(sandboxNulTaskName) +
		" -StartupDir " + psQuote(sandboxStartupDir())

	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(powershell); err != nil {
		powershell = "powershell.exe"
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	filePtr, err := windows.UTF16PtrFromString(powershell)
	if err != nil {
		return err
	}
	argsPtr, err := windows.UTF16PtrFromString(args)
	if err != nil {
		return err
	}
	cwdPtr, err := windows.UTF16PtrFromString(base)
	if err != nil {
		return err
	}
	if err := windows.ShellExecute(windows.Handle(0), verb, filePtr, argsPtr, cwdPtr, windows.SW_HIDE); err != nil {
		if err == windows.ERROR_CANCELLED {
			return fmt.Errorf("درخواست دسترسی Administrator لغو شد — بدون آن مجوز NUL اعمال نمی‌شود")
		}
		return fmt.Errorf("اجرای کمکی دسترسی NUL: %w", err)
	}
	return nil
}
