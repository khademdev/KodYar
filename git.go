// ابزارهای Git برای ایجنت (اولویت ۴).
//
// شش ابزار: چهار خواندنی (git_status، git_diff، git_log، git_show) و دو
// نوشتنیِ مودال‌دار (git_create_branch، git_commit) + آگاهی مدل از branch و
// وضعیت dirty (تزریق در system prompt). هیچ ابزاری push نمی‌کند.
//
// امنیت:
//   - همه‌ی فرمان‌ها بدون shell اجرا می‌شوند (exec مستقیم؛ بدون تزریق)
//   - ref/نام شاخه با لیست سفید کاراکتری اعتبارسنجی می‌شود (بدون --option)
//   - ابزارهای نوشتنی همیشه مودال تأیید (kind=git_write) دارند و override
//     «allow» نمی‌تواند آن را حذف کند (gitWriteGatedTool).
//   - stage کردن با hash-object --no-filters + update-index انجام می‌شود (همان
//     مسیر نقطه‌ی بازگشت): filterهای .gitattributes/LFS و hookهای مخزن اجرا
//     نمی‌شوند و بایت‌ها دست‌نخورده می‌مانند.
//   - همان پرچم‌های خنثی‌سازی checkpoint به هر فرمان اضافه می‌شود (gitSafeArgs):
//     یک .git/config دستکاری‌شده نباید بتواند با core.fsmonitor / core.hooksPath
//     هنگام git status/diff فرمانی بیرونی اجرا کند
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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

// gitSafeArgs آرگومان‌های کامل git برای ابزارهای خواندنی داخلی: `-C ws` و
// فقط پرچم‌هایی که مسیر اجرای فرمان را می‌بندند (core.fsmonitor،
// core.hooksPath). پرچم‌های CRLF مخصوص checkpoint هستند و اینجا لازم نیستند —
// عوض‌کردنشان فقط خروجی diff را تغییر می‌داد.
//
// چرا: git status/diff تنظیمات مخزن را می‌خوانند؛ اگر فایل .git/config (که
// ایجنت با تأیید کاربر می‌تواند بنویسد) core.fsmonitor یا core.hooksPath را به
// یک فرمان بیرونی اشاره دهد، اجرای آن فرمان همان اجرای کد است. مقدار خط فرمان
// بر config مقدم می‌شود و مسیر اجرایی بسته می‌ماند.
func gitSafeArgs(ws string, args ...string) []string {
	out := make([]string, 0, len(args)+6)
	out = append(out, "-C", ws,
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath="+os.DevNull,
	)
	return append(out, args...)
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
	cmd := exec.CommandContext(cctx, "git", gitSafeArgs(ws, args...)...)
	hideConsoleWindow(cmd) // بدون این، هر فرمان git پنجره‌ی کنسول فلش می‌زد
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
	// --no-ext-diff / --no-textconv: یک .git/config + .gitattributes دستکاری‌شده
	// (diff.<driver>.textconv یا diff.external) نباید هنگام نمایش diff فرمان
	// بیرونی اجرا کند — همان مسیر اجرای کد، فقط از راه دیگر.
	args := []string{"diff", "--no-ext-diff", "--no-textconv"}
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
	args := []string{"log", "--no-ext-diff", "--no-textconv", fmt.Sprintf("-%d", limit), "--pretty=format:%h %ad %an: %s", "--date=short"}
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
	out, ok := a.runGit(ctx, "show", "--no-ext-diff", "--no-textconv", "--unified=3", "--pretty=format:commit %H%nAuthor: %an <%ae>%nDate:   %ad%n%n    %s%n%n%b", ref)
	if !ok {
		return out, false
	}
	return truncateToolOutput(out), true
}

// ---------------------------------------------------------------- ابزارهای نوشتنی

// gitWriteGatedTool آیا این ابزار در مخزن git می‌نویسد؟ (همیشه مودال، بدون
// اجازه‌ی سراسری — همان الگوی web_host.)
func gitWriteGatedTool(name string) bool {
	return name == "git_create_branch" || name == "git_commit"
}

// gitBranchSafe نام شاخه: حرف/رقم در ابتدا و انتها، فقط [A-Za-z0-9._/-].
var gitBranchSafe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._/-]{0,198}[A-Za-z0-9])?$`)

// gitBranchNameError اعتبارسنجی نام شاخه ("" یعنی معتبر).
func gitBranchNameError(name string) string {
	if name == "" {
		return "branch name is required."
	}
	if len(name) > 200 {
		return "branch name is too long (max 200 characters)."
	}
	if !gitBranchSafe.MatchString(name) {
		return "invalid branch name (allowed: letters, digits, . _ / - ; must start and end with a letter or digit)."
	}
	if strings.Contains(name, "..") || strings.Contains(name, "@{") ||
		strings.Contains(name, "//") || strings.HasSuffix(name, ".lock") {
		return "invalid branch name (must not contain '..', '@{', '//' or end with '.lock')."
	}
	return ""
}

// gitWriteEnv محیط git برای ابزارهای نوشتنی: همان gitEnv + خنثی‌سازی
// .gitattributes سیستمی (فیلترها با --no-filters هم بی‌اثرند).
func gitWriteEnv() []string {
	env := gitEnv()
	return append(env, "GIT_ATTR_NOSYSTEM=1")
}

// gitRequestWrite مودال تأیید نوشتن git؛ true فقط با پاسخ صریح غیر-رد.
// پیش‌نمایش دستور و فهرست فایل‌ها هم به UI می‌رود.
func (a *App) gitRequestWrite(ctx context.Context, message, commandPreview string, files []string) bool {
	shown := append([]string(nil), files...)
	if len(shown) > 40 {
		shown = append(shown[:40], fmt.Sprintf("… and %d more file(s)", len(files)-40))
	}
	ask := &permissionAsk{
		ID:       "perm-" + newID(),
		Kind:     "git_write",
		NoAlways: true, // هیچ «همیشه/نشست» — هر بار پرسیده می‌شود
		Command:  commandPreview,
		Detail:   shown,
		Message:  message,
		At:       timeNow(),
	}
	return a.requestPermissionConfirm(ctx, ask) != permRespondReject
}

// gitChangedPathsIn مسیرهای تغییرکرده‌ی مخزن (فقط مسیرها، بدون .git).
// -z و -uall: نام‌های عجیب بدون quote و فایل‌های داخل پوشه‌ی untracked هم
// جداگانه می‌آیند (برای stage کردن تک‌تک لازم است).
func (a *App) gitChangedPathsIn(ctx context.Context, ws string) ([]string, bool) {
	out, ok := a.runGitIn(ctx, ws, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if !ok {
		return nil, false
	}
	var files []string
	recs := strings.Split(out, "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 4 {
			continue
		}
		st := rec[:2]
		if st[0] == 'R' || st[0] == 'C' {
			i++ // رکورد بعدی مسیر مبدأ است (rename/copy)
		}
		p := rec[3:]
		if p == "" || p == ".git" || strings.HasPrefix(strings.ReplaceAll(p, `\`, "/"), ".git/") {
			continue
		}
		files = append(files, p)
	}
	return files, true
}

// gitStagedPathsIn فایل‌هایی که از قبل در ایندکس stage شده‌اند.
func (a *App) gitStagedPathsIn(ctx context.Context, ws string) []string {
	out, ok := a.runGitIn(ctx, ws, "diff", "--cached", "--name-only", "-z")
	if !ok {
		return nil
	}
	var files []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			files = append(files, p)
		}
	}
	return files
}

// gitStageFilesIn فایل‌ها را بدون هیچ فیلتری در ایندکس می‌گذارد.
//
// همان روش checkpoint: blobها با hash-object --no-filters ساخته می‌شوند و
// update-index --index-info آن‌ها را می‌پذیرد؛ core.autocrlf، .gitattributes
// (text/eol/filter) و filterهای LFS اجرا نمی‌شوند. فایل‌های حذف‌شده با
// update-index --remove از ایندکس برداشته می‌شوند.
func (a *App) gitStageFilesIn(ctx context.Context, ws string, files []string) error {
	env := gitWriteEnv()
	var regular, missing, skipped []string
	for _, rel := range files {
		abs := filepath.Join(ws, filepath.FromSlash(rel))
		st, err := os.Lstat(abs)
		if err != nil {
			missing = append(missing, rel)
			continue
		}
		if !st.Mode().IsRegular() {
			skipped = append(skipped, rel)
			continue
		}
		if strings.ContainsAny(rel, "\n\r") {
			skipped = append(skipped, rel)
			continue
		}
		regular = append(regular, rel)
	}
	if len(skipped) > 0 {
		return fmt.Errorf("cannot stage %s: only regular files are staged by git_commit (symlinks/submodules need run_command)", strings.Join(skipped, ", "))
	}
	if len(regular) > 0 {
		out, err := runGitCheckpointInput(ctx, ws, env, checkpointTimeout,
			strings.Join(regular, "\n")+"\n", "hash-object", "-w", "--no-filters", "--stdin-paths")
		if err != nil {
			return err
		}
		lines := strings.Split(strings.TrimRight(out, "\r\n"), "\n")
		if len(lines) != len(regular) {
			return fmt.Errorf("git hash-object returned %d objects for %d files", len(lines), len(regular))
		}
		var info strings.Builder
		for i, rel := range regular {
			sha := strings.TrimSpace(lines[i])
			if len(sha) != 40 && len(sha) != 64 {
				return fmt.Errorf("git hash-object returned an invalid object id for %q", rel)
			}
			mode, ok := checkpointBlobMode(filepath.Join(ws, filepath.FromSlash(rel)))
			if !ok {
				continue // بین فهرست‌گیری و هش حذف/پیوند شد
			}
			info.WriteString(mode)
			info.WriteByte(' ')
			info.WriteString(sha)
			info.WriteByte('\t')
			info.WriteString(rel)
			info.WriteByte(0)
		}
		if info.Len() > 0 {
			if _, err := runGitCheckpointInput(ctx, ws, env, checkpointTimeout,
				info.String(), "update-index", "-z", "--add", "--index-info"); err != nil {
				return err
			}
		}
	}
	for _, rel := range missing {
		if _, err := runGitCheckpointInput(ctx, ws, env, checkpointTimeout, "",
			"update-index", "--remove", "--", rel); err != nil {
			return err
		}
	}
	return nil
}

// toolGitCreateBranch شاخه‌ی تازه می‌سازد و به آن سوییچ می‌کند — پس از مودال.
func (a *App) toolGitCreateBranch(ctx context.Context, name, from string) (string, bool) {
	name = strings.TrimSpace(name)
	if errMsg := gitBranchNameError(name); errMsg != "" {
		return "ERROR: " + errMsg, false
	}
	from = strings.TrimSpace(from)
	if from != "" && !gitRefSafe.MatchString(from) {
		return "ERROR: invalid 'from' ref (allowed: letters, digits, . _ / - ^ ~ ; must not start with '-').", false
	}
	a.mu.Lock()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()
	if ws == "" {
		return "ERROR: workspace directory is not configured.", false
	}
	curOut, ok := a.runGitIn(ctx, ws, "rev-parse", "--abbrev-ref", "HEAD")
	if !ok {
		return curOut, false
	}
	current := strings.TrimSpace(curOut)
	preview := "git checkout -b " + name
	if from != "" {
		preview += " " + from
	}
	message := "شاخهٔ جدید «" + name + "» از «" + current + "» ساخته شود و به آن سوییچ شود؟"
	if !a.gitRequestWrite(ctx, message, preview, nil) {
		return "ERROR: the user rejected creating branch '" + name + "'.", false
	}
	args := []string{"checkout", "-b", name}
	if from != "" {
		args = append(args, from)
	}
	out, ok := a.runGitIn(ctx, ws, args...)
	if !ok {
		return out, false
	}
	return "OK: created and switched to branch '" + name + "' (from " + current + ").\n" + strings.TrimSpace(out), true
}

// toolGitCommit فایل‌های تغییرکرده (یا مسیرهای خواسته‌شده) را stage و کامیت
// می‌کند — پس از مودال. هرگز push نمی‌کند و hookها اجرا نمی‌شوند.
func (a *App) toolGitCommit(ctx context.Context, message string, paths []string) (string, bool) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return "ERROR: commit message is required.", false
	}
	if len(msg) > 4000 {
		return "ERROR: commit message is too long (max 4000 characters).", false
	}
	a.mu.Lock()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()
	if ws == "" {
		return "ERROR: workspace directory is not configured.", false
	}
	if out, ok := a.runGitIn(ctx, ws, "rev-parse", "--git-dir"); !ok {
		return out, false
	}

	var files []string
	if len(paths) == 0 {
		changed, ok := a.gitChangedPathsIn(ctx, ws)
		if !ok {
			return "ERROR: could not read the repository status.", false
		}
		files = changed
	} else {
		for _, p := range paths {
			// مسیر مطلق (شامل «/etc/…» که در ویندوز IsAbs نیست) پذیرفته نمی‌شود.
			if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || filepath.IsAbs(p) || filepath.VolumeName(p) != "" {
				return "ERROR: absolute paths are not allowed; pass a path relative to the workspace.", false
			}
			abs, err := a.resolveWorkspacePath(p)
			if err != nil {
				return "ERROR: " + err.Error(), false
			}
			rel := relDisplay(ws, abs)
			if rel == ".git" || strings.HasPrefix(strings.ReplaceAll(rel, `\`, "/"), ".git/") {
				return "ERROR: refusing to commit inside .git.", false
			}
			files = append(files, rel)
		}
	}
	if len(files) == 0 {
		return "OK: nothing to commit (working tree is clean).", true
	}

	// فایل‌هایی که از قبل stage بوده‌اند هم در این کامیت می‌آیند؛ در پیش‌نمایش
	// نشان داده می‌شوند تا کاربر غافلگیر نشود.
	shown := append([]string(nil), files...)
	for _, p := range a.gitStagedPathsIn(ctx, ws) {
		found := false
		for _, f := range shown {
			if f == p {
				found = true
				break
			}
		}
		if !found {
			shown = append(shown, p)
		}
	}
	branchOut, _ := a.runGitIn(ctx, ws, "rev-parse", "--abbrev-ref", "HEAD")
	branch := strings.TrimSpace(branchOut)
	preview := "git commit -m " + strconv.Quote(msg)
	messageUI := "کامیت " + faDigits(len(shown)) + " فایل روی شاخهٔ «" + branch + "» انجام شود؟"
	if !a.gitRequestWrite(ctx, messageUI, preview, shown) {
		return "ERROR: the user rejected the commit.", false
	}

	if err := a.gitStageFilesIn(ctx, ws, files); err != nil {
		return "ERROR: staging failed: " + err.Error(), false
	}
	if out, err := runGitCheckpointInput(ctx, ws, gitWriteEnv(), checkpointTimeout, "",
		"-c", "commit.gpgsign=false", "commit", "-m", msg); err != nil {
		if strings.Contains(err.Error(), "nothing to commit") {
			return "OK: nothing to commit (the listed files have no staged changes).", true
		}
		return "ERROR: git commit failed: " + err.Error() + strings.TrimSpace(out), false
	}
	hashOut, _ := a.runGitIn(ctx, ws, "rev-parse", "--short", "HEAD")
	hash := strings.TrimSpace(hashOut)
	if hash == "" {
		hash = "(unknown)"
	}
	return fmt.Sprintf("OK: committed %d file(s) on branch %s as %s. Nothing was pushed.\nFiles: %s",
		len(shown), branch, hash, strings.Join(shown, ", ")), true
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
