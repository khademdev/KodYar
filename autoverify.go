// راستی‌آزمایی خودکار پس از تغییر فایل (گام ۵).
//
// مشکل: ایجنت بعد از ویرایش فایل، اجرای build/test را به «یادِ مدل» می‌سپرد.
// اگر یادش نمی‌رفت، خطای کامپایل/تایپ فقط در نوبت بعدی و با اصرار کاربر
// پیدا می‌شد — عامل اصلی دورهای تکراری و کندی.
//
// راه‌حل: بعد از دوری که فایلی تغییر داده (و راستی‌آزمایی موفقی نداشته)،
// یک دستور ساخت/تایپ‌چکِ **ثابت و از پیش تعیین‌شده** (نه پیشنهاد مدل) اجرا
// می‌شود؛ فقط در صورت شکست، خروجی کران‌دار به‌عنوان پیام به دور بعد داده
// می‌شود تا مدل همان‌جا علت را رفع کند. خروجی موفق فقط ثبت/اعلام می‌شود.
//
// اصول ایمنی:
//   - فقط دستورهای ثابت whitelist (go build/go vet، tsc --noEmit،
//     py_compile روی فایل‌های تغییرکرده) — هیچ ورودی مدل اجرا نمی‌شود.
//   - مهلت ۱۵۰ ثانیه، خروجی حداکثر ۶KB، حداکثر دو دستور در هر نوبت.
//   - اگر ابزار سازنده نصب نباشد، بی‌صدا رد می‌شود (Skipped، یک‌بار لاگ).
//   - هماهنگ با دروازهٔ دسترسی (AllowCommands + قواعد Permissions + لیست سیاه)
//     و با همان سیاست سندباکس run_command اجرا می‌شود؛ نیاز به تأیید → اجرا نشدن.
//   - هیچ باینری‌ای از داخل مخزن (./bin، node_modules/.bin/…) اجرا نمی‌شود.
//   - با سوییچ «راستی‌آزمایی خودکار» (پیش‌فرض روشن) خاموش‌شدنی است.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	autoVerifyTimeout   = 150 * time.Second
	autoVerifyMaxOutput = 6000
	autoVerifyMaxFiles  = 8
)

// autoVerifyRun نتیجهٔ اجرای یک دستور راستی‌آزمایی.
type autoVerifyRun struct {
	Command  string
	ExitCode int
	OK       bool
	Output   string
	Ms       int64
}

// autoVerifyCommands دستورهای راستی‌آزمایی خودکار را از روی نشانه‌های پروژه
// انتخاب می‌کند. هیچ‌چیز اجرا نمی‌شود؛ فقط وجود فایل/ابزار بررسی می‌شود.
func autoVerifyCommands(ws string, changed []string) []string {
	ws = strings.TrimSpace(ws)
	if ws == "" {
		return nil
	}
	exists := func(name string) bool {
		st, err := os.Stat(filepath.Join(ws, name))
		return err == nil && !st.IsDir()
	}
	hasTool := func(name string) bool {
		_, err := exec.LookPath(name)
		return err == nil
	}
	var cmds []string
	// Go: ساخت همه‌ی بسته‌ها؛ در صورت موفقیت vet هم (سبک و سریع با کش).
	if exists("go.mod") && hasTool("go") {
		cmds = append(cmds, "go build ./...", "go vet ./...")
		return cmds
	}
	// TypeScript: فقط tsc نصب‌شده در PATH — عمداً هیچ باینری‌ای از خود مخزن
	// (node_modules/.bin/…) اجرا نمی‌شود، چون کد مخزن ممکن است تغییر کرده
	// باشد (خطر اجرای کد دلخواه) و ست‌آپ‌های ناقص هم خطای گمراه‌کننده می‌دهند.
	if exists("tsconfig.json") {
		if hasTool("tsc") {
			cmds = append(cmds, "tsc --noEmit -p tsconfig.json")
		}
		return cmds
	}
	// Python: فقط فایل‌های تغییرکرده (بدون پیمایش کل مخزن).
	var pyFiles []string
	for _, f := range changed {
		if strings.HasSuffix(strings.ToLower(f), ".py") && len(pyFiles) < autoVerifyMaxFiles {
			pyFiles = append(pyFiles, f)
		}
	}
	if len(pyFiles) > 0 && hasTool("python") {
		quoted := make([]string, 0, len(pyFiles))
		for _, f := range pyFiles {
			quoted = append(quoted, `"`+f+`"`)
		}
		cmds = append(cmds, "python -m py_compile "+strings.Join(quoted, " "))
	}
	return cmds
}

// autoVerifyTurn دستورها را به ترتیب اجرا می‌کند و در نخستین شکست می‌ایستد.
// skipped دلیل «چیزی برای اجرا نبود» است (فارسی، برای اعلان/لاگ).
func (a *App) autoVerifyTurn(ctx context.Context, ws string, changed []string) (runs []autoVerifyRun, skipped string) {
	cmds := autoVerifyCommands(ws, changed)
	if len(cmds) == 0 {
		return nil, "برای این پروژه دستور ساخت/تایپ‌چک خودکاری پیدا نشد"
	}
	for _, cmd := range cmds {
		if ctx.Err() != nil {
			return runs, "لغو شد"
		}
		// همان دروازه‌ی دسترسی ابزار run_command: سوییچ «اجازهٔ اجرای دستور»،
		// قاعده‌های Permissions و لیست سیاه سخت. هیچ مودالی باز نمی‌شود؛ اگر
		// تصمیم «نیاز به تأیید» باشد دستور خودکار اجرا نمی‌شود.
		if ok, why := a.autoVerifyGate(ws, cmd); !ok {
			return runs, why
		}
		start := time.Now()
		// با همان سیاست سندباکس دستورهای عادی (AppContainer، شبکه طبق تنظیم).
		out, code, _ := runShellWithPolicy(ctx, ws, cmd, autoVerifyTimeout, nil,
			a.sandboxPolicyForCommand(ws, cmd))
		run := autoVerifyRun{
			Command:  cmd,
			ExitCode: code,
			OK:       code == 0,
			Output:   cutUTF8(strings.TrimSpace(out), autoVerifyMaxOutput),
			Ms:       time.Since(start).Milliseconds(),
		}
		runs = append(runs, run)
		if !run.OK {
			break
		}
	}
	return runs, ""
}

// autoVerifyGate آیا اجرای خودکار این دستور مجاز است؟ (بدون مودال)
// خروجی دوم: دلیل فارسیِ رد (برای لاگ/اعلان).
func (a *App) autoVerifyGate(ws, command string) (bool, string) {
	a.mu.Lock()
	allowed := a.settings != nil && a.settings.AllowCommands
	a.mu.Unlock()
	if !allowed {
		return false, "اجازهٔ اجرای دستور خاموش است، پس راستی‌آزمایی خودکار اجرا نشد"
	}
	if autoVerifyRepoBinary(ws, command) {
		return false, "دستور به یک باینری داخل مخزن اشاره می‌کند و خودکار اجرا نمی‌شود"
	}
	e := a.evalCommandPermDetailed(command)
	switch e.decision {
	case permDeny:
		return false, "دروازهٔ دسترسی این دستور را رد کرد: " + e.reason
	case permAllow:
		return true, ""
	default:
		return false, "این دستور به تأیید کاربر نیاز دارد و خودکار اجرا نمی‌شود"
	}
}

// autoVerifySkipLogged برای لاگ/اعلان یک‌باره‌ی «چیزی اجرا نشد» — بدون این،
// هر نوبتِ بعدی همان پیام را در لاگ تکرار می‌کرد.
var autoVerifySkipLogged sync.Map

// noteAutoVerifySkip پیام «اجرا نشد» را فقط یک‌بار برای هر (پوشه، دلیل) ثبت
// می‌کند و true برمی‌گرداند اگر تازه ثبت شده باشد.
func (a *App) noteAutoVerifySkip(ws, reason string) bool {
	if reason == "" {
		return false
	}
	key := normalizeToolPath(ws) + "|" + reason
	if _, seen := autoVerifySkipLogged.LoadOrStore(key, true); seen {
		return false
	}
	logAppErrorFor(a, "verify", "skipped: "+reason)
	return true
}

// autoVerifyRepoBinary آیا اولین بخش دستور به فایلی داخل پوشهٔ کاری اشاره
// می‌کند؟ (./bin/x، node_modules/.bin/x، مسیر مطلق داخل مخزن، …)
// اجرای کدِ ساخته‌شده یا نصب‌شده در مخزن را خودکار نمی‌کنیم.
func autoVerifyRepoBinary(ws, command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return true
	}
	first := command
	if i := strings.IndexAny(first, " \t"); i >= 0 {
		first = first[:i]
	}
	first = strings.Trim(first, `"'`)
	if first == "" {
		return true
	}
	lower := strings.ToLower(strings.ReplaceAll(first, `\\`, "/"))
	if strings.Contains(lower, "node_modules/.bin/") || strings.HasPrefix(lower, "./") || strings.HasPrefix(lower, ".\\") {
		return true
	}
	if ws = strings.TrimSpace(ws); ws == "" {
		return false
	}
	if !strings.ContainsAny(first, `/\\`) {
		return false
	}
	// مسیر مطلق یا نسبیِ دارای جداکننده که به داخل پوشهٔ کاری می‌رسد.
	p := first
	if !filepath.IsAbs(p) {
		p = filepath.Join(ws, p)
	}
	absWS, err1 := filepath.Abs(ws)
	absP, err2 := filepath.Abs(p)
	if err1 != nil || err2 != nil {
		return true // محافظه‌کارانه: نامشخص → اجرا نکن
	}
	rel, err := filepath.Rel(absWS, absP)
	if err != nil {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// autoVerifyFailedMessage پیام انگلیسیِ رو به مدل برای شکست راستی‌آزمایی.
func autoVerifyFailedMessage(r autoVerifyRun) string {
	var b strings.Builder
	b.WriteString("AUTO-VERIFY FAILED: `")
	b.WriteString(r.Command)
	b.WriteString("` exited with code ")
	b.WriteString(fmt.Sprint(r.ExitCode))
	b.WriteString(". Fix the cause in the code and keep going; do not just describe the error.\nShip only changes that compile.\n\nOutput:\n")
	if r.Output == "" {
		b.WriteString("(no output)")
	} else {
		b.WriteString(r.Output)
	}
	return b.String()
}

// autoVerifyNotice متن فارسیِ اعلان وضعیت برای UI.
func autoVerifyNotice(runs []autoVerifyRun, skipped string) (text string, ok bool) {
	if len(runs) == 0 {
		return "", true
	}
	last := runs[len(runs)-1]
	if last.OK {
		return "راستی‌آزمایی خودکار موفق بود: " + last.Command, true
	}
	return "راستی‌آزمایی خودکار شکست خورد (" + last.Command + ") — نتیجه به مدل داده شد تا علت را رفع کند", false
}

// shouldAutoVerify شرط‌های اجرای راستی‌آزمایی در حلقه — جدا شده تا تست شود.
func shouldAutoVerify(useTools, droppedTools, alreadyVerified bool, round, maxRounds int,
	rec *turnRecorder, enabled bool) bool {
	if !enabled || !useTools || droppedTools || alreadyVerified {
		return false
	}
	if round+1 >= maxRounds {
		return false
	}
	return rec != nil && rec.filesChanged() && rec.needsVerification()
}
