// ابزارهای Git برای ایجنت (اولویت ۴) — فقط خواندنی.
//
// چهار ابزار: git_status، git_diff، git_log، git_show + آگاهی مدل از
// branch و وضعیت dirty (تزریق در system prompt).
//
// امنیت:
// - همه‌ی فرمان‌ها بدون shell اجرا می‌شوند (exec مستقیم؛ بدون تزریق)
// - ref کاربر با لیست سفید کاراکتری اعتبارسنجی می‌شود (بدون --option)
// - فقط زیرفرمان‌های خواندنی؛ نوشتن/commit از مسیر run_command و دروازه‌های
// امنیتی آن (لیست سیاه + اجازه‌ی کاربر) عبور می‌کند
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// محدودیت‌های git
const (
	gitTimeoutSec = 20 // مهلت هر فرمان git
	gitMaxLog     = 50 // سقف تعداد کامیت‌های git_log

	// gitCaptureCap سقف بافر گرفتن خروجی git (بایت) — ۱۲۸ برابرِ بودجه‌ی
	// نهایی خروجی ابزار (maxToolOutput = ۱۶K کاراکتر). هدف فقط مهار انفجار
	// حافظه‌ی گذرا روی خروجی‌های پاتولوژیک است (مثلاً git log روی مخزن عظیم)؛
	// خروجی نهایی به‌هرحال در لایه‌ی ارائه توسط truncateToolOutput هرس و
	// علامت‌گذاری می‌شود، پس این سقف هیچ داده‌ی قابل‌مشاهده‌ای برای مدل
	// حذف نمی‌کند (رفع F3).
	gitCaptureCap = 2 << 20 // 2 MiB
)

// cappedGitBuffer نویسنده‌ی کراندار: فقط «limit» بایت اول را نگه می‌دارد و
// بقیه را صرفاً می‌شمارد — حافظه در هر حالتی محدود می‌ماند. Write همیشه
// len(p), nil برمی‌گرداند تا فرایند git به‌خاطر خطای نوشتن کشته نشود؛
// داده‌ی افتاده در لایه‌ی ارائه (truncateToolOutput) با علامت truncation
// خود را نشان می‌دهد.
type cappedGitBuffer struct {
	buf     bytes.Buffer
	limit   int
	dropped int64 // بایت‌هایی که جا نشدند و دور ریخته شدند
}

func newCappedGitBuffer(limit int) *cappedGitBuffer {
	return &cappedGitBuffer{limit: limit}
}

func (w *cappedGitBuffer) Write(p []byte) (int, error) {
	room := w.limit - w.buf.Len()
	if room > len(p) {
		room = len(p)
	}
	if room > 0 {
		w.buf.Write(p[:room])
	}
	w.dropped += int64(len(p) - room)
	return len(p), nil
}

// gitRefSafe فقط شناسه‌های کامیت/برنچ/تگ امن را می‌پذیرد: حرف/رقم/نقطه/
// خط‌تیره/زیرخط/اسلش و «^~» برای بازه. شروع با «-» ممنوع (option injection).
var gitRefSafe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/\-^~]*$`)

// runGitIn هسته‌ی بدون قفل — فراخواننده خودش ws را می‌دهد. برای runGit و
// gitContextLineIn. نباید با a.mu قفل‌شده صدا زده شود (exec فرمان).
// خروجی در حافظه کراندار است (cappedGitBuffer — رفع F3).
func (a *App) runGitIn(ctx context.Context, ws string, args ...string) (string, bool) {
	if ws == "" {
		return "ERROR: workspace directory is not configured.", false
	}

	cctx, cancel := context.WithTimeout(ctx, gitTimeoutSec*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", append([]string{"-C", ws}, args...)...)
	// نویسنده‌های غیر-فایل → exec لوله و goroutine کپی می‌سازد؛ اگر فرزندی
	// از git (credential helper/hook/filter) لوله را باز نگه دارد، Run بعد
	// از کشتنِ خود git هم بلاک می‌ماند. WaitDelay سقف همان انتظار است
	// (همان الگوی shell.go) وگرنه کل نوبت ایجنت قفل می‌شد.
	cmd.WaitDelay = 5 * time.Second
	out, errb := newCappedGitBuffer(gitCaptureCap), newCappedGitBuffer(gitCaptureCap)
	cmd.Stdout = out
	cmd.Stderr = errb
	cmd.Env = gitEnv()
	runErr := cmd.Run()

	combined := out.buf.String()
	if errb.buf.Len() > 0 {
		if combined != "" {
			combined += "\n"
		}
		combined += errb.buf.String()
	}
	if runErr != nil {
		msg := strings.TrimSpace(combined)
		if strings.Contains(msg, "not a git repository") || strings.Contains(msg, "not in a git repository") {
			return "ERROR: the workspace is not a git repository (no .git folder found).", false
		}
		if isGitMissing(runErr) {
			return "ERROR: git executable was not found on this machine.", false
		}
		if cctx.Err() == context.DeadlineExceeded {
			return "ERROR: git command timed out.", false
		}
		return "ERROR: git failed: " + msg, false
	}
	return combined, true
}

// runGit فرمان git را در workspace اجرا می‌کند (بدون shell).
// نباید با a.mu قفل‌شده صدا زده شود.
func (a *App) runGit(ctx context.Context, args ...string) (string, bool) {
	a.mu.Lock()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()
	return a.runGitIn(ctx, ws, args...)
}

// gitEnv متغیرهای محیط حداقلی برای git — بدون ارث‌بری کامل env کاربر تا
// رفتار قابل‌پیش‌بینی بماند (پرچم‌های زبان و رنگ غیرفعال). HOME همیشه
// تنظیم می‌شود تا credential helperها مسیر home را بدانند.
func gitEnv() []string {
	env := []string{
		"GIT_PAGER=cat", "PAGER=cat",
		"GIT_TERMINAL_PROMPT=0", // هرگز ورودی تعاملی نخواهد (ssh)
		"GIT_CONFIG_NOSYSTEM=1",
		"LC_ALL=C", "GIT_COLOR=0",
		"PATH=" + os.Getenv("PATH"),
	}
	home := os.Getenv("HOME")
	if home == "" && isWindows() {
		// ویندوز معمولا HOME ندارد؛ USERPROFILE معادل آن است و برای
		// ssh/credential helperها لازم است.
		home = os.Getenv("USERPROFILE")
	}
	if home != "" {
		env = append(env, "HOME="+home)
	}
	// SystemRoot روی ویندوز برای راه‌اندازی برخی کتابخانه‌های سیستمی
	// (مثل TLS و spawn فرزند) ضروری است؛ نبودنش باعث خطای عمومی در exec می‌شود.
	if sr := os.Getenv("SystemRoot"); sr != "" {
		env = append(env, "SystemRoot="+sr)
	}
	return env
}

// isGitMissing تشخیص «git نصب نیست» بین خطاهای exec.
func isGitMissing(err error) bool {
	var ee *exec.Error
	if errors.As(err, &ee) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "executable file not found") || strings.Contains(msg, "no such file")
}

// ---------------------------------------------------------------- ابزارها

// toolGitStatus وضعیت مخزن — خوانایی برای مدل بهتر از porcelain خام.
func (a *App) toolGitStatus(ctx context.Context) (string, bool) {
	out, ok := a.runGit(ctx, "status", "--porcelain=v1", "-b")
	if !ok {
		return out, false
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var b strings.Builder
	branchLine := ""
	staged, unstaged, untracked := 0, 0, 0
	for _, ln := range lines {
		if ln == "" {
			continue
		}
		if strings.HasPrefix(ln, "##") {
			branchLine = strings.TrimSpace(strings.TrimPrefix(ln, "##"))
			continue
		}
		if len(ln) < 2 {
			continue
		}
		x, y := ln[0], ln[1]
		path := ln[3:]
		switch {
		case x == '?':
			untracked++
			fmt.Fprintf(&b, "?? %s (untracked)\n", path)
		case x != ' ' && y != ' ':
			staged++
			unstaged++
			fmt.Fprintf(&b, "%s %s (staged + unstaged changes)\n", ln[:2], path)
		case x != ' ':
			staged++
			fmt.Fprintf(&b, "%s %s (staged)\n", ln[:2], path)
		default:
			unstaged++
			fmt.Fprintf(&b, "%s %s (unstaged)\n", ln[:2], path)
		}
	}
	var head strings.Builder
	if branchLine != "" {
		head.WriteString("Branch: " + branchLine + "\n")
	} else {
		head.WriteString("Branch: (unknown)\n")
	}
	fmt.Fprintf(&head, "Staged: %d — Unstaged: %d — Untracked: %d\n", staged, unstaged, untracked)
	if b.Len() == 0 {
		head.WriteString("Working tree clean (no changes).")
		return head.String(), true
	}
	return head.String() + "\nFiles:\n" + truncateToolOutput(b.String()), true
}

// toolGitDiff نمایش diff — کل، یک مسیر، یا فقط staged.
func (a *App) toolGitDiff(ctx context.Context, rel string, staged bool) (string, bool) {
	args := []string{"diff"}
	if staged {
		args = append(args, "--cached")
	}
	if rel = strings.TrimSpace(rel); rel != "" {
		p, err := a.resolveWorkspacePath(rel)
		if err != nil {
			return "ERROR: " + err.Error(), false
		}
		a.mu.Lock()
		ws := strings.TrimSpace(a.settings.WorkspacePath)
		a.mu.Unlock()
		args = append(args, "--", relDisplay(ws, p))
	}
	args = append(args, "--unified=3")
	out, ok := a.runGit(ctx, args...)
	if !ok {
		return out, false
	}
	if strings.TrimSpace(out) == "" {
		return "OK: no differences found" + map[bool]string{true: " (nothing staged).", false: " (working tree matches HEAD for this scope)."}[staged], true
	}
	return truncateToolOutput(out), true
}

// toolGitLog تاریخ کامیت‌ها.
func (a *App) toolGitLog(ctx context.Context, rel string, limit int) (string, bool) {
	if limit <= 0 {
		limit = 10
	}
	if limit > gitMaxLog {
		limit = gitMaxLog
	}
	args := []string{"log", fmt.Sprintf("-%d", limit), "--pretty=format:%h %ad %an: %s", "--date=short"}
	if rel = strings.TrimSpace(rel); rel != "" {
		p, err := a.resolveWorkspacePath(rel)
		if err != nil {
			return "ERROR: " + err.Error(), false
		}
		a.mu.Lock()
		ws := strings.TrimSpace(a.settings.WorkspacePath)
		a.mu.Unlock()
		args = append(args, "--", relDisplay(ws, p))
	}
	out, ok := a.runGit(ctx, args...)
	if !ok {
		return out, false
	}
	if strings.TrimSpace(out) == "" {
		return "OK: no commits found.", true
	}
	return truncateToolOutput("Recent commits (newest first):\n" + out), true
}

// toolGitShow نمایش یک کامیت/برنچ/تگ مشخص.
func (a *App) toolGitShow(ctx context.Context, ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "ERROR: ref is required (commit hash, branch name or tag).", false
	}
	if !gitRefSafe.MatchString(ref) {
		return "ERROR: invalid ref (allowed: letters, digits, . _ / - ^ ~ ; must not start with '-').", false
	}
	out, ok := a.runGit(ctx, "show", "--unified=3", "--pretty=format:commit %H%nAuthor: %an <%ae>%nDate:   %ad%n%n    %s%n%n%b", ref)
	if !ok {
		return out, false
	}
	return truncateToolOutput(out), true
}

// ---------------------------------------------------------------- کانتکست پرامپت

// gitContextLine وضعیت فعلی مخزن (برنچ + dirty) برای system prompt.
// خروجی خالی یعنی «repo نیست یا git نیست» — هیچ چیزی تزریق نمی‌شود.
// باید بدون a.mu صدا زده شود (runGit خودش قفل می‌گیرد).
func (a *App) gitContextLine(ctx context.Context) string {
	a.mu.Lock()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()
	return a.gitContextLineIn(ctx, ws)
}

// gitContextLineIn نسخه‌ی بدون قفل با مهلت کوتاه — برای gitContextLine و
// gitContextLineForSend. نباید با a.mu قفل‌شده صدا زده شود (exec فرمان).
func (a *App) gitContextLineIn(parent context.Context, ws string) string {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	out, ok := a.runGitIn(ctx, ws, "status", "--porcelain=v1", "-b")
	if !ok {
		return ""
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	branch := "(unknown)"
	dirty := false
	for _, ln := range lines {
		if strings.HasPrefix(ln, "##") {
			branch = strings.TrimSpace(strings.TrimPrefix(ln, "##"))
			if i := strings.Index(branch, "..."); i > 0 {
				branch = branch[:i]
			}
			continue
		}
		if ln != "" {
			dirty = true
		}
	}
	state := "clean"
	if dirty {
		state = "DIRTY (uncommitted changes present)"
	}
	return fmt.Sprintf("Git repo state — branch: %s, working tree: %s. Use git_status / git_diff / git_log / git_show tools for details; never guess history.", branch, state)
}

// gitContextLineForSend خط کانتکست git را برای نقاط ورودی استریم می‌سازد:
// snapshot تنظیمات با قفل کوتاه خوانده می‌شود و سپس فرمان git «بدون هیچ
// قفلی» اجرا می‌شود. باید قبل از گرفتن genMu/a.mu صدا زده شود — الگوی
// مشابه expandMentions در SendMessage. رفع F1: exec هرگز زیر a.mu نمی‌افتد.
func (a *App) gitContextLineForSend() string {
	a.mu.Lock()
	agentOn := a.settings.AgentMode
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()
	if !agentOn || ws == "" {
		return ""
	}
	return a.gitContextLineIn(context.Background(), ws)
}
