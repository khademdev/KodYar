// بستهٔ عیب‌یابی (P2 #۱۵) — یک zip خودکفا برای اشکال‌زدایی، بدون هیچ راز.
//
// هدف: وقتی کاربر مشکلی گزارش می‌کند، به‌جای «چند اسکرین‌شات و کپی‌دستی
// لاگ‌ها» یک فایل بفرستد که همهٔ زمینهٔ لازم را دارد:
//
//	info.txt                 نسخه، سیستم‌عامل، معماری، زمان، مسیرها، شمارش‌ها
//	settings.redacted.json   تنظیمات با کلیدهای API و env پاک‌شده
//	errors.log               انتهای لاگ خطاها (کران‌دار)
//	app.log                  انتهای لاگ برنامه (اگر وجود داشته باشد)
//	README.txt               توضیح محتوا و نکات حریم خصوصی
//
// اصول ایمنی:
//   - هیچ کلید API، توکن یا مقدار env داخل بسته نمی‌رود (پاک‌سازی ساختاری + الگویی).
//   - محتوای گفتگوها، فایل‌های پروژه و diffها هرگز جمع نمی‌شوند.
//   - لاگ‌ها فقط «انتهای» فایل و کران‌دار (۶۴KB) کپی می‌شوند.
//   - مسیر خروجی: <configDir>/diagnostics/KodYar-diag-<timestamp>.zip
package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	diagnosticLogBytes  = 64 * 1024 // سقف هر فایل لاگ داخل بسته
	diagnosticInfoBytes = 16 * 1024
)

// secretPattern الگوهای رایج کلید/توکن برای پاک‌سازی نهایی متن.
var secretPattern = regexp.MustCompile(`(?i)\b(sk-[A-Za-z0-9_\-]{6,}|AIza[0-9A-Za-z_\-]{10,}|gsk_[A-Za-z0-9_\-]{6,}|sk-or-[A-Za-z0-9_\-]{6,}|xox[baprs]-[A-Za-z0-9\-]{6,}|ghp_[A-Za-z0-9]{10,}|Bearer\s+[A-Za-z0-9._\-]{12,})`)

// scrubSecrets هر الگوی شبیه‌کلید را در متن با [redacted] عوض می‌کند.
func scrubSecrets(s string) string {
	return secretPattern.ReplaceAllString(s, "[redacted]")
}

// tailBytes انتهای یک فایل را تا سقف max برمی‌گرداند (مسیر ناموجود = خالی).
func tailBytes(path string, max int) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	size := info.Size()
	off := int64(0)
	if size > int64(max) {
		off = size - int64(max)
	}
	if _, err := f.Seek(off, 0); err != nil {
		return nil
	}
	buf := make([]byte, size-off)
	n, _ := f.Read(buf)
	return buf[:n]
}

// diagnosticSettingsJSON تنظیمات را با پاک‌سازی رازها به JSON تبدیل می‌کند:
// کلیدهای API حذف، مقادیر env سرورهای MCP ماسک، و در پایان scrubSecrets.
func (a *App) diagnosticSettingsJSON() string {
	a.mu.Lock()
	if a.settings == nil {
		a.mu.Unlock()
		return "{}"
	}
	cp := *a.settings
	a.mu.Unlock()

	cp.APIKeys = map[string]string{}
	if len(cp.MCPServers) > 0 {
		servers := make([]MCPServerConfig, len(cp.MCPServers))
		copy(servers, cp.MCPServers)
		for i := range servers {
			if len(servers[i].Env) > 0 {
				env := make(map[string]string, len(servers[i].Env))
				for k := range servers[i].Env {
					env[k] = "***"
				}
				servers[i].Env = env
			}
			servers[i].Command = scrubSecrets(servers[i].Command)
			for j := range servers[i].Args {
				servers[i].Args[j] = scrubSecrets(servers[i].Args[j])
			}
		}
		cp.MCPServers = servers
	}
	for i := range cp.CustomTools {
		cp.CustomTools[i].Command = scrubSecrets(cp.CustomTools[i].Command)
	}
	cp.SystemPrompt = cutUTF8(cp.SystemPrompt, 2000)

	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return "{}"
	}
	return scrubSecrets(string(data))
}

// diagnosticInfoText متن info.txt — زمینهٔ اجرا و شمارش‌ها (بدون راز).
func (a *App) diagnosticInfoText() string {
	a.mu.Lock()
	ws := ""
	provider, model := "", ""
	sandbox := false
	allowCmd := false
	autoVerify := true
	promptCache := true
	if a.settings != nil {
		ws = strings.TrimSpace(a.settings.WorkspacePath)
		provider = a.settings.Provider
		model = a.settings.Model
		sandbox = sandboxEnabledLocked(a.settings)
		allowCmd = a.settings.AllowCommands
		autoVerify = !a.settings.AutoVerifyOff
		promptCache = a.settings.PromptCache || !a.settings.PromptCacheTouched
	}
	a.mu.Unlock()
	// شمارش گفتگوها از ایندکس سبک (بدون خواندن محتوای گفتگوها).
	convs := len(a.GetConversations())

	cfgDir, _ := a.configDir()
	var b strings.Builder
	fmt.Fprintf(&b, "KodYar diagnostic bundle\n")
	fmt.Fprintf(&b, "generated_at: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "version: %s\n", version)
	fmt.Fprintf(&b, "go_version: %s\n", runtime.Version())
	fmt.Fprintf(&b, "os: %s/%s (num_cpu=%d, gomaxprocs=%d)\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.GOMAXPROCS(0))
	fmt.Fprintf(&b, "config_dir: %s\n", cfgDir)
	fmt.Fprintf(&b, "workspace: %s\n", wsOrNone(ws))
	fmt.Fprintf(&b, "provider: %s\n", wsOrNone(provider))
	fmt.Fprintf(&b, "model: %s\n", wsOrNone(model))
	fmt.Fprintf(&b, "conversations: %d\n", convs)
	fmt.Fprintf(&b, "sandbox: %v (available=%v)\n", sandbox, sandboxSupported())
	fmt.Fprintf(&b, "allow_commands: %v\n", allowCmd)
	fmt.Fprintf(&b, "auto_verify: %v\n", autoVerify)
	fmt.Fprintf(&b, "prompt_cache: %v\n", promptCache)
	if last := a.GetLastTurnTiming(); last.TotalMs > 0 {
		fmt.Fprintf(&b, "last_turn: ttft=%dms rounds=%d total=%dms tools=%d tool_ms=%d\n",
			last.TTFTMs, last.Rounds, last.TotalMs, last.Tools, last.ToolMs)
	}
	return scrubSecrets(cutUTF8(b.String(), diagnosticInfoBytes))
}

// wsOrNone برای فیلدهای خالی متن «(unset)» می‌گذارد.
func wsOrNone(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(unset)"
	}
	return v
}

// diagnosticReadmeText توضیح فارسی محتوای بسته (برای کاربر/پشتیبانی).
func diagnosticReadmeText() string {
	var b strings.Builder
	b.WriteString("بستهٔ عیب‌یابی KodYar\n==================\n\n")
	b.WriteString("این فایل برای بررسی مشکل ساخته شده و هیچ راز یا محتوای گفتگویی ندارد:\n\n")
	b.WriteString("- info.txt: نسخه، سیستم‌عامل، معماری، مسیر پوشهٔ کاری/تنظیمات، شمارش گفتگوها و وضعیت سوییچ‌ها\n")
	b.WriteString("- settings.redacted.json: تنظیمات برنامه با کلیدهای API و مقادیر env پاک‌شده\n")
	b.WriteString("- errors.log: انتهای لاگ خطاها (حداکثر ۶۴ کیلوبایت)\n")
	b.WriteString("- app.log: انتهای لاگ برنامه، اگر وجود داشته باشد\n\n")
	b.WriteString("آنچه در بسته نیست: کلیدهای API، توکن‌ها، مقادیر env، متن پیام‌ها، محتوای فایل‌های پروژه و diff تغییرات.\n")
	b.WriteString("برای ارسال به پشتیبانی فقط همین zip را بفرستید.\n")
	return b.String()
}

// ExportDiagnosticBundle یک zip عیب‌یابی می‌سازد و مسیر آن را برمی‌گرداند.
func (a *App) ExportDiagnosticBundle() (string, error) {
	defer a.recoverBound("ExportDiagnosticBundle")

	dir, err := a.configDir()
	if err != nil {
		return "", fmt.Errorf("پوشهٔ تنظیمات در دسترس نیست: %w", err)
	}
	outDir := filepath.Join(dir, "diagnostics")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("ساخت پوشهٔ بستهٔ عیب‌یابی ناموفق بود: %w", err)
	}
	name := "KodYar-diag-" + time.Now().Format("20060102-150405") + ".zip"
	path := filepath.Join(outDir, name)

	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("ساخت فایل بسته ناموفق بود: %w", err)
	}
	zw := zip.NewWriter(f)
	add := func(entry string, data []byte) error {
		w, err := zw.Create(entry)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	fail := func(e error) (string, error) {
		_ = zw.Close()
		_ = f.Close()
		_ = os.Remove(path)
		return "", e
	}

	if err := add("README.txt", []byte(diagnosticReadmeText())); err != nil {
		return fail(err)
	}
	if err := add("info.txt", []byte(a.diagnosticInfoText())); err != nil {
		return fail(err)
	}
	if err := add("settings.redacted.json", []byte(a.diagnosticSettingsJSON())); err != nil {
		return fail(err)
	}
	if logPath, perr := resolveErrorLogPath(a); perr == nil {
		if data := tailBytes(logPath, diagnosticLogBytes); len(data) > 0 {
			if err := add("errors.log", []byte(scrubSecrets(string(data)))); err != nil {
				return fail(err)
			}
		}
	}
	for _, cand := range []string{"app.log", "kodyar.log"} {
		if data := tailBytes(filepath.Join(dir, cand), diagnosticLogBytes); len(data) > 0 {
			if err := add(cand, []byte(scrubSecrets(string(data)))); err != nil {
				return fail(err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}
