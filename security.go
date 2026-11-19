// لایه‌ی امنیتی دستورات + ابزارهای سفارشی کاربر.
//
// حتی وقتی «اجازه‌ی اجرای دستور» فعال است، دو لایه‌ی محافظتی داریم:
//
// 1. لیست سیاه سخت (hard block): دستورات تخریب‌گر سیستمی — هرگز اجرا
// نمی‌شوند، حتی با تأیید کاربر (rm -rf /، mkfs، dd روی دیسک،
// fork bomb، shutdown و مشابه).
//
// 2. دستورات پرریسک (confirm): تغییرات مخرب در مخزن (git push --force،
// git reset --hard، clean -fd و…) — با رویداد «agent:confirm_command»
// از کاربر تأیید اضافی می‌گیرند؛ همان مودال تأیید تغییر فایل.
//
// ابزارهای سفارشی: کاربر می‌تواند با JSON Schema ابزار تعریف کند که با
// جایگزینی {{arg}} در الگوی دستور، در پوشه‌ی کاری اجرا می‌شود. این
// ابزارها هم از همان لیست سیاه عبور می‌کنند و اگر Confirm داشته باشند
// تأیید می‌گیرند.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
)

// hardBlockedPatterns الگوهایی که هرگز اجازه‌ی اجرا ندارند.
// ورودی قبل از تطبیق normalize می‌شود (حروف کوچک، فاصله‌های تکراری).
var hardBlockedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(^|[;&|]\s*)rm\s+(-[a-z]*[rf][a-z]*\s+)+(/|\*|~|\$HOME)(\s|$|/)`), // rm -rf / و مشابه
	regexp.MustCompile(`rm\s+(-[a-z]*[rf][a-z]*\s+)+/`),                                   // rm -rf /مسیر/مطلق
	regexp.MustCompile(`(^|\s)mkfs(\.|\s)`),                                               // فرمت پارتیشن
	regexp.MustCompile(`(^|\s)dd\s+[^;|&]*of=/dev/(sd|hd|nvme|disk)`),                     // dd روی دیسک فیزیکی
	regexp.MustCompile(`:\(\)\s*\{\s*:\|:&\s*\}\s*;\s*:`),                                 // fork bomb
	regexp.MustCompile(`(^|\s)(shutdown|reboot|halt|poweroff)\b`),                         // خاموش/ریست سیستم
	regexp.MustCompile(`>\s*/dev/sd[a-z]`),                                                // نوشتن مستقیم روی دیسک
	regexp.MustCompile(`(^|\s)chmod\s+(-R\s+)?777\s+/(\s|$)`),                             // chmod 777 /
	regexp.MustCompile(`(^|\s)(format|diskpart)\b`),                                       // ویندوز: فرمت دیسک
	// del/rd ویندوز — فلگ‌ها با فاصله یا بدون فاصله (‎/f/s/q‎)؛ درایوهای همه‌ی حروف.
	// توجه: ورودی از قبل normalize شده و بک‌اسلش‌ها حذف شده‌اند — پس الگو هم
	// نباید بک‌اسلش بخواهد (باگ قدیمی: ‎del /f /s /q C:\‎ هرگز گرفته نمی‌شد).
	regexp.MustCompile(`del\s+(/[fsq][\s/]+)+[a-z]:`), // del /f /s /q C:
	regexp.MustCompile(`rd\s+(/[sq][\s/&]+)+[a-z]:`),  // rd /s /q C:\
	// پاورشل: حذف بازگشتی نیرومند و اجرای دستور از اینترنت
	regexp.MustCompile(`remove-item\s+[^;|&]*(-recurse|-force)`),                   // Remove-Item -Recurse -Force
	regexp.MustCompile(`(^|\s)(iwr|invoke-webrequest|invoke-expression|iex)\b`),    // iwr / iex
	regexp.MustCompile(`(^|\s)curl[^|;&]*\|\s*(sudo\s+)?(ba)?sh\b`),                // curl | sh
	regexp.MustCompile(`(^|\s)wget[^|;&]*\|\s*(sudo\s+)?(ba)?sh\b`),                // wget | sh
	regexp.MustCompile(`[^|;&]*\|\s*(sudo\s+)?(ba|z|da|k)?sh\b`),                   // هر چیز | sh — خروجیِ بالا کد اجرا می‌شود
	regexp.MustCompile(`git\s+push\s+.*(--force|-f)\b.*\s+--(all|mirror)`),         // push --force --all
	regexp.MustCompile(`(^|\s)(cipher|format)\s+[a-z]:`),                           // format C: / cipher
	regexp.MustCompile(`(^|\s)reg\s+(delete|add)\s+hklm`),                          // تغییر رجیستری سیستم
	regexp.MustCompile(`(^|\s)net\s+user\b.*(/add|/delete)`),                       // ساخت/حذف کاربر ویندوز
	regexp.MustCompile(`(^|\s)(start-process|powershell)\b.*-enc(odedcommand)?\b`), // PowerShell encoded
	regexp.MustCompile(`(^|\s)bash\s+-c\b.*rm\s+-`),                                // bash -c 'rm -...'
}

// riskyPatterns دستورات پرریسک که تأیید اضافی می‌خواهند.
var riskyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`git\s+push\s+[^;|&]*(--force|-f)\b`),
	regexp.MustCompile(`git\s+reset\s+--hard`),
	regexp.MustCompile(`git\s+clean\s+-[a-z]*f`),
	regexp.MustCompile(`git\s+checkout\s+--\s`),
	regexp.MustCompile(`git\s+branch\s+-D\b`),
	regexp.MustCompile(`(^|\s)rm\s+(-[a-z]*r[a-z]*f?|-[a-z]*f[a-z]*r?)\s+\S`), // rm -rf هر مسیر
	regexp.MustCompile(`(^|\s)truncate\b|(^|\s)shred\b`),
	regexp.MustCompile(`npm\s+(ci|install)\s+--force|rm\s+-rf\s+node_modules`),
	regexp.MustCompile(`(^|\s)drop\s+(table|database)\b`),  // SQL مخرب
	regexp.MustCompile(`docker\s+(system|volume)\s+prune`), // پاک‌سازی داکر
	regexp.MustCompile(`(^|\s)make\s+clean\b`),
	regexp.MustCompile(`git\s+push\s+[^;|&]*--delete\b`), // حذف remote branch
	regexp.MustCompile(`(^|\s)kubectl\s+delete\b`),       // حذف منابع k8s
}

// ---------------------------------------------------------------- ابزار سفارشی
// توجه: تأیید دستورات پرریسک/دسترسی از به مودال یکپارچه‌ی
// agent:permission_ask (permissions.go) رفته است؛ مودال قدیمی kind=command
// دیگر از بک‌اند emit نمی‌شود.

// customToolDefs توصیف ابزارهای سفارشی برای پارامتر tools.
func customToolDefs(tools []CustomTool) []toolDef {
	var out []toolDef
	for _, t := range tools {
		if t.Name == "" || t.Command == "" {
			continue
		}
		params := map[string]interface{}{}
		if strings.TrimSpace(t.Parameters) != "" {
			var m map[string]interface{}
			if jsonUnmarshalStr(t.Parameters, &m) == nil && m != nil {
				params = m
			}
		}
		if len(params) == 0 {
			params = schemaObj(map[string]interface{}{}, nil)
		}
		// شمای دستی کاربر ممکن است نوع/properties/required نداشته باشد؛
		// سرویس‌دهنده‌های سخت‌گیر کل درخواست را با 400 رد می‌کنند.
		params = normalizeParametersSchema(params)
		out = append(out, toolDef{
			Type: "function",
			Function: toolFunctionDef{
				Name:        "custom_" + t.Name,
				Description: t.Description + " (user-defined tool; runs a local command in the workspace)",
				Parameters:  params,
			},
		})
	}
	return out
}

// jsonUnmarshalStr کمک کوچک (برای خوانایی).
func jsonUnmarshalStr(s string, v interface{}) error {
	return json.Unmarshal([]byte(s), v)
}

// jsonMarshalHelper مارشال با خطای قابل‌بازگشت.
func jsonMarshalHelper(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// escapeShellArg مقدار رشته‌ایِ آمده از مدل را برای درج امن در فرمان shell
// quote می‌کند — تا آرگومان نتواند متاکاراکترها (| & $ ` و…) را تزریق کند.
// ویندوز: داخل دابل‌کوت، متاکاراکترهای cmd معنای literal می‌گیرند.
// یونیکس: تک‌کوت امن‌ترین قالب است؛ خود تک‌کوت با '\” بسته/باز می‌شود.
//
// توجه: اگر الگوی کاربر خودش دور {{arg}} کوتیشن گذاشته باشد، خروجی quote
// اضافه است (ایمن ولی شاید ناخواسته) — قالب بدون کوتیشن توصیه می‌شود.
func escapeShellArg(val string) string {
	val = strings.ReplaceAll(val, "\r", " ")
	val = strings.ReplaceAll(val, "\n", " ")
	if isWindows() {
		// در cmd داخل دابل‌کوت، & | < > ^ literal هستند؛ دابل‌کوتِ
		// خود مقدار با تکرار (") خنثی می‌شود. کاراکتر % هم حذف می‌شود
		// تا نتواند به‌صورت ‎%VAR%‎ گسترش متغیر محیطی ایجاد کند.
		val = strings.ReplaceAll(val, "%", "")
		return `"` + strings.ReplaceAll(val, `"`, `""`) + `"`
	}
	return "'" + strings.ReplaceAll(val, "'", `'\''`) + "'"
}

// execCustomTool اجرای یک ابزار سفارشی با جایگزینی آرگومان‌ها.
// نام ابزار در execTool با پیشوند custom_ می‌آید.
func (a *App) execCustomTool(ctx context.Context, name string, args map[string]interface{}) (string, bool) {
	a.mu.Lock()
	tools := a.settings.CustomTools
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	allow := a.settings.AllowCommands
	a.mu.Unlock()

	short := strings.TrimPrefix(name, "custom_")
	var def *CustomTool
	for i := range tools {
		if tools[i].Name == short || "custom_"+tools[i].Name == name {
			def = &tools[i]
			break
		}
	}
	if def == nil {
		return "ERROR: unknown tool: " + name, false
	}
	if !allow {
		return "ERROR: custom tools need the «allow commands» switch to be enabled by the user.", false
	}
	if ws == "" {
		return "ERROR: workspace directory is not configured.", false
	}

	// ساخت دستور با جایگزینی امن {{arg}} — مقدارها quote می‌شوند تا
	// نتوانند با متاکاراکترهای shell (| & ` $ و…) از قالب فرار کنند.
	command := def.Command
	for k, v := range args {
		placeholder := "{{" + k + "}}"
		val := ""
		switch t := v.(type) {
		case string:
			val = escapeShellArg(t)
		case float64:
			val = itoa(int(t)) // عدد quote نمی‌خواهد
		case bool:
			if t {
				val = "true"
			} else {
				val = "false"
			}
		default:
			if b, err := jsonMarshalHelper(v); err == nil {
				val = escapeShellArg(string(b))
			}
		}
		command = strings.ReplaceAll(command, placeholder, val)
	}
	// آرگومان‌های باقی‌مانده (بدون مقدار) حذف می‌شوند
	command = regexp.MustCompile(`\{\{[a-zA-Z0-9_]+\}\}`).ReplaceAllString(command, "")

	// دسترسی ابزار سفارشی: deny → رد؛ ask → همان رفتار سوییچ Confirm
	a.mu.Lock()
	d := a.toolPermDecisionLocked(name)
	a.mu.Unlock()
	if d == permDeny {
		return "ERROR: tool '" + name + "' is disabled by the user's permission settings.", false
	}
	forceAsk := def.Confirm || d == permAsk

	// دروازه‌ی دسترسی واحد — همان دروازه‌ی run_command
	if ok, why := a.gateCommand(ctx, command, forceAsk, "the user rejected running this command."); !ok {
		return why, false
	}

	timeout := def.TimeoutSec
	if timeout <= 0 {
		timeout = 60
	}
	if timeout > 300 {
		timeout = 300
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	// استریم تدریجی خروجی به UI (chat:tool_output)
	emit, flush := a.shellOutputEmitter(ctx)
	out, _, err := runShellWithPolicy(cctx, ws, command, time.Duration(timeout)*time.Second, emit, a.sandboxPolicyForCommand(ws, command))
	flush()
	// مثل run_command: انتهای خروجی (خطای واقعی) هم نگه داشته می‌شود.
	result := truncateToolOutputKeepTail(strings.TrimRight(out, "\n"))
	if err != nil {
		return commandFailureResult(result, timeout, cctx.Err() == context.DeadlineExceeded, err), false
	}
	if result == "" {
		result = "(command finished successfully, no output)"
	}
	return result, true
}

// SaveCustomTools ذخیره‌ی لیست ابزارهای سفارشی از UI.
func (a *App) SaveCustomTools(tools []CustomTool) error {
	defer a.recoverBound("SaveCustomTools")
	a.mu.Lock()
	defer a.mu.Unlock()
	nameRe := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{1,30}$`)
	seen := map[string]bool{}
	for i := range tools {
		t := &tools[i]
		t.Name = strings.TrimSpace(t.Name)
		t.Command = strings.TrimSpace(t.Command)
		if t.Name == "" && t.Command == "" {
			continue // ردیف خالی
		}
		if !nameRe.MatchString(t.Name) {
			return errors.New("نام ابزار باید با حرف لاتین شروع شود و فقط حرف/عدد/زیرخط باشد: " + t.Name)
		}
		if seen[t.Name] {
			return errors.New("نام ابزار تکراری است: " + t.Name)
		}
		seen[t.Name] = true
		if t.Command == "" {
			return errors.New("برای ابزار «" + t.Name + "» دستور اجرا خالی است")
		}
		if t.ID == "" {
			t.ID = newID()
		}
		if t.TimeoutSec <= 0 || t.TimeoutSec > 300 {
			t.TimeoutSec = 60
		}
	}
	a.settings.CustomTools = tools
	return a.saveSettings()
}

// GetCustomTools لیست ابزارهای سفارشی (مرتب بر اساس نام).
func (a *App) GetCustomTools() []CustomTool {
	defer a.recoverBound("GetCustomTools")
	a.mu.Lock()
	defer a.mu.Unlock()
	out := copyCustomTools(a.settings.CustomTools)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
