// «نقطه‌ی بازگشت پروژه» (Checkpoint) — بازگردانی امنِ همه‌ی آثار یک نوبت،
// حتی اثرهای جانبیِ دستورها (مأموریت ۳ — مرحله B).
//
// چرا: بازگردانی تغییرات فایل (Stage 2) فقط چیزی را برمی‌گرداند که از ابزارهای
// فایل گذشته باشد؛ دستورهایی مثل `go generate`، `npm install` یا یک اسکریپت
// مخرب می‌توانند فایل‌هایی بسازند/عوض کنند که هیچ رکوردی ندارند. نقطه‌ی
// بازگشت، کل پوشه‌ی کاری را (با احترام به `.gitignore`) در یک کامیتِ مستقل
// ثبت می‌کند تا «کل پروژه» به قبل از نوبت برگردد.
//
// قواعد سخت:
//   - ساختِ نقطه‌ی بازگشت هرگز HEAD/index/working tree کاربر را لمس نمی‌کند:
//     ایندکسِ موقت (`GIT_INDEX_FILE`)، `write-tree`، `commit-tree` و
//     `update-ref refs/myai/checkpoints/<conv>-<turn>` — بدون checkout، بدون
//     commit روی شاخه، بدون تغییر فایل‌ها.
//   - بازگردانی هرگز داده حذف نمی‌کند: هر فایلی که باید عوض/حذف شود اول به
//     سبد زباله (`<configDir>/trash/<workspace-hash>/<timestamp>/`) منتقل
//     می‌شود تا خودِ بازگردانی هم قابل‌برگشت باشد. فایل‌های ignore‌شده دست
//     نمی‌خورند.
//   - محدودیت زمانی ۱۰ ثانیه؛ در خطا/مهلت بی‌صدا رد می‌شود (نوبت هرگز
//     متوقف نمی‌شود) و یک‌بار به کاربر گفته می‌شود.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	checkpointTimeout     = 10 * time.Second // سقف ساخت نقطه‌ی بازگشت
	checkpointRestoreTime = 60 * time.Second // سقف بازگردانی (فایل‌های زیاد)
	checkpointMaxKeep     = 20               // نگه‌داری جدیدترین‌ها در هر پوشه
	checkpointMaxFiles    = 5000             // سقف فایل در بازگردانی
	checkpointRefPrefix   = "refs/myai/checkpoints/"
	checkpointTrashDays   = 14
	checkpointTrashBytes  = 500 << 20 // ۵۰۰ مگابایت
)

// checkpointRef نام مرجع یک نوبت.
func checkpointRef(convID string, turnID int64) string {
	return checkpointRefPrefix + convID + "-" + strconv.FormatInt(turnID, 10)
}

// ---------------------------------------------------------------- اجرای git

// checkpointGitEnv محیط git برای عملیات نقطه‌ی بازگشت — نویسنده/کامیت‌کننده
// صریح تا به تنظیمات کاربر وابسته نباشیم.
func checkpointGitEnv(indexFile string) []string {
	env := gitEnv()
	env = append(env,
		"GIT_INDEX_FILE="+indexFile,
		"GIT_AUTHOR_NAME=MyAI", "GIT_AUTHOR_EMAIL=myai@localhost",
		"GIT_COMMITTER_NAME=MyAI", "GIT_COMMITTER_EMAIL=myai@localhost",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
	)
	return env
}

// runGitCheckpoint یک فرمان git با محیط/مهلت مشخص اجرا می‌کند (کران‌دار).
func runGitCheckpoint(ctx context.Context, ws string, env []string, timeout time.Duration, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", append([]string{"-C", ws}, args...)...)
	cmd.WaitDelay = 3 * time.Second
	out := newCappedGitBuffer(gitCaptureCap)
	errb := newCappedGitBuffer(gitCaptureCap)
	cmd.Stdout, cmd.Stderr = out, errb
	if env != nil {
		cmd.Env = env
	} else {
		cmd.Env = gitEnv()
	}
	if err := cmd.Run(); err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("git %s timed out", args[0])
		}
		msg := strings.TrimSpace(errb.buf.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return out.buf.String(), nil
}

// ---------------------------------------------------------------- ساخت

// checkpointWorkspace پوشه‌ی کاری فعلی اگر مخزن git باشد.
func (a *App) checkpointWorkspace() (string, bool) {
	a.mu.Lock()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()
	if ws == "" {
		return "", false
	}
	if st, err := os.Stat(filepath.Join(ws, ".git")); err != nil || !st.IsDir() {
		return "", false
	}
	return ws, true
}

// markCheckpointAttempt ثبت «یک‌بار در نوبت» — قفل برگ، بدون I/O.
func (a *App) markCheckpointAttempt(key string) bool {
	a.checkpointMu.Lock()
	defer a.checkpointMu.Unlock()
	if a.checkpointTurns == nil {
		a.checkpointTurns = map[string]bool{}
	}
	if a.checkpointTurns[key] {
		return false
	}
	if len(a.checkpointTurns) > 512 { // کران حافظه — نوبت‌های قدیمی بی‌اهمیت‌اند
		a.checkpointTurns = map[string]bool{}
	}
	a.checkpointTurns[key] = true
	return true
}

// ensureTurnCheckpoint نقطه‌ی بازگشت نوبت را (فقط اولین بار در هر نوبت) می‌سازد.
// عمداً خطا را برنمی‌گرداند: نوبت هرگز به‌خاطر آن متوقف نمی‌شود، فقط یک‌بار
// اعلان فارسی به کاربر می‌رسد.
func (a *App) ensureTurnCheckpoint(ctx context.Context) {
	turnID := turnIDFrom(ctx)
	if turnID <= 0 {
		return // بیرون از نوبت (مثلاً RunCommand دستی)
	}
	convID := a.convIDForTools(ctx)
	if convID == "" {
		return
	}
	ws, ok := a.checkpointWorkspace()
	if !ok {
		// بدون git: فقط تغییرات ابزارهای فایل قابل برگشت است — یک‌بار بگو.
		a.checkpointNoticeOnce("nogit", "نقطه‌ی بازگشت پروژه در دسترس نیست (پوشه‌ی کاری مخزن git نیست)؛ فقط تغییرات ابزارهای فایل قابل برگشت است.")
		return
	}
	if !a.markCheckpointAttempt(convID + "#" + strconv.FormatInt(turnID, 10)) {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, checkpointTimeout)
	defer cancel()
	if _, err := a.createCheckpoint(cctx, ws, convID, turnID); err != nil {
		logAppErrorFor(a, "checkpoint", err.Error())
		a.checkpointNoticeOnce("fail-"+strconv.FormatInt(turnID, 10),
			"ساخت نقطه‌ی بازگشت این نوبت ممکن نشد؛ تغییرات ابزارهای فایل همچنان قابل برگشت است.")
	}
}

// checkpointNoticeOnce اعلان فارسی فقط یک‌بار برای هر کلید در هر اجرا.
func (a *App) checkpointNoticeOnce(key, text string) {
	a.checkpointMu.Lock()
	if a.checkpointNotified == nil {
		a.checkpointNotified = map[string]bool{}
	}
	if a.checkpointNotified[key] {
		a.checkpointMu.Unlock()
		return
	}
	a.checkpointNotified[key] = true
	a.checkpointMu.Unlock()
	runtimeEmit(a, "chat:notice", map[string]interface{}{"text": text})
}

// createCheckpoint کامیتِ مستقلِ وضعیت فعلی را می‌سازد و ref را به‌روز می‌کند.
func (a *App) createCheckpoint(ctx context.Context, ws, convID string, turnID int64) (string, error) {
	dir, err := a.configDir()
	if err != nil {
		return "", err
	}
	tmpDir := filepath.Join(dir, "checkpoints")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return "", err
	}
	indexFile := filepath.Join(tmpDir, "index-"+newID()+".git-index")
	defer os.Remove(indexFile)
	env := checkpointGitEnv(indexFile)

	if _, err := runGitCheckpoint(ctx, ws, env, checkpointTimeout, "add", "-A"); err != nil {
		return "", err
	}
	treeOut, err := runGitCheckpoint(ctx, ws, env, checkpointTimeout, "write-tree")
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(treeOut)
	if tree == "" {
		return "", fmt.Errorf("git write-tree returned an empty tree")
	}
	args := []string{"commit-tree", tree}
	if head, herr := runGitCheckpoint(ctx, ws, nil, checkpointTimeout, "rev-parse", "--verify", "HEAD"); herr == nil {
		if h := strings.TrimSpace(head); h != "" {
			args = append(args, "-p", h)
		}
	}
	args = append(args, "-m", "myai checkpoint "+convID+" turn "+strconv.FormatInt(turnID, 10))
	commitOut, err := runGitCheckpoint(ctx, ws, env, checkpointTimeout, args...)
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(commitOut)
	if commit == "" {
		return "", fmt.Errorf("git commit-tree returned an empty commit")
	}
	if _, err := runGitCheckpoint(ctx, ws, env, checkpointTimeout, "update-ref", checkpointRef(convID, turnID), commit); err != nil {
		return "", err
	}
	a.pruneCheckpoints(ctx, ws, convID)
	return commit, nil
}

// pruneCheckpoints فقط جدیدترین‌ها را نگه می‌دارد (سقف ۲۰ در هر پوشه).
func (a *App) pruneCheckpoints(ctx context.Context, ws, keepConvRef string) {
	out, err := runGitCheckpoint(ctx, ws, nil, checkpointTimeout, "for-each-ref",
		"--sort=creatordate", "--format=%(refname)", checkpointRefPrefix)
	if err != nil {
		return
	}
	var refs []string
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, checkpointRefPrefix) {
			refs = append(refs, ln)
		}
	}
	if len(refs) <= checkpointMaxKeep {
		return
	}
	drop := refs[:len(refs)-checkpointMaxKeep]
	for _, r := range drop {
		if strings.HasSuffix(r, keepConvRef) {
			continue // مرجع تازه‌ی همین نوبت را حذف نکن
		}
		_, _ = runGitCheckpoint(ctx, ws, nil, checkpointTimeout, "update-ref", "-d", r)
	}
}

// checkpointExists آیا برای این نوبت نقطه‌ی بازگشت ثبت شده است؟ (داخلی؛
// UI از PreviewCheckpointRestore استفاده می‌کند تا سطح API کوچک بماند.)
func (a *App) checkpointExists(convID string, turnID int64) bool {
	ws, ok := a.checkpointWorkspace()
	if !ok || turnID <= 0 || strings.TrimSpace(convID) == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkpointTimeout)
	defer cancel()
	_, err := runGitCheckpoint(ctx, ws, nil, checkpointTimeout, "rev-parse", "--verify", checkpointRef(convID, turnID)+"^{commit}")
	return err == nil
}

// ---------------------------------------------------------------- بازگردانی

// CheckpointRestorePreview پیش‌نمایش بازگردانی — فهرست‌های مسیر نسبی.
type CheckpointRestorePreview struct {
	Available bool     `json:"available"`
	Rewrite   []string `json:"rewrite"`   // عوض/بازسازی می‌شوند (به سبد زباله هم می‌روند)
	Trash     []string `json:"trash"`     // فقط سبد زباله (در snapshot نیستند)
	Unchanged []string `json:"unchanged"` // یکسان‌اند
	Skipped   []string `json:"skipped"`   // پشتیبانی نمی‌شوند (symlink و…)
	Error     string   `json:"error,omitempty"`
}

// CheckpointRestoreReport نتیجه‌ی بازگردانی.
type CheckpointRestoreReport struct {
	Restored []string `json:"restored"`
	Trashed  []string `json:"trashed"`
	Skipped  []string `json:"skipped"`
	Errors   []string `json:"errors"`
	TrashDir string   `json:"trashDir,omitempty"`
}

// checkpointEntry یک ورودی snapshot.
type checkpointEntry struct {
	Path string
	Mode string
	Sym  bool
}

// safeCheckpointRel مسیر نسبی امن (بدون فرار از پوشه‌ی کاری).
func safeCheckpointRel(rel string) bool {
	if rel == "" || strings.HasPrefix(rel, "-") {
		return false
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	if strings.HasPrefix(rel, "/") || path.IsAbs(rel) {
		return false
	}
	if len(rel) >= 2 && rel[1] == ':' {
		return false // نامه‌ی درایو ویندوز
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// checkpointSnapshot فایل‌های یک مرجع را به تفکیک نوع برمی‌گرداند.
func checkpointSnapshot(ctx context.Context, ws, ref string) ([]checkpointEntry, error) {
	out, err := runGitCheckpoint(ctx, ws, nil, checkpointTimeout, "ls-tree", "-r", "-z", ref)
	if err != nil {
		return nil, err
	}
	var entries []checkpointEntry
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		// "<mode> <type> <sha>\t<path>"
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			continue
		}
		meta, p := rec[:tab], rec[tab+1:]
		fields := strings.Fields(meta)
		if len(fields) < 2 || fields[1] != "blob" {
			continue // زیرپوشه (tree) یا چیز دیگر
		}
		if !safeCheckpointRel(p) {
			continue // مسیر مشکوک اصلاً استفاده نمی‌شود
		}
		entries = append(entries, checkpointEntry{Path: p, Mode: fields[0], Sym: fields[0] == "120000"})
		if len(entries) > checkpointMaxFiles {
			return nil, fmt.Errorf("checkpoint has more than %d files", checkpointMaxFiles)
		}
	}
	return entries, nil
}

// checkpointCurrentFiles فایل‌های فعلیِ ردیابی‌شده + untracked ولی غیر-ignore.
func checkpointCurrentFiles(ctx context.Context, ws string) ([]string, error) {
	out, err := runGitCheckpoint(ctx, ws, nil, checkpointTimeout, "ls-files", "-z", "-c", "-o", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []string
	for _, p := range strings.Split(out, "\x00") {
		if p == "" || seen[p] || !safeCheckpointRel(p) {
			continue
		}
		seen[p] = true
		files = append(files, p)
		if len(files) > checkpointMaxFiles {
			return nil, fmt.Errorf("workspace has more than %d files", checkpointMaxFiles)
		}
	}
	sort.Strings(files)
	return files, nil
}

// materializeCheckpoint snapshot را در یک پوشه‌ی موقت می‌نویسد (بدون لمس
// working tree) — منبعِ بایت‌به‌بایت محتوای بازگردانی.
func (a *App) materializeCheckpoint(ctx context.Context, ws, ref string) (dir string, cleanup func(), err error) {
	cfg, cerr := a.configDir()
	if cerr != nil {
		return "", nil, cerr
	}
	base := filepath.Join(cfg, "checkpoints")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", nil, err
	}
	dir, err = os.MkdirTemp(base, "restore-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	indexFile := filepath.Join(base, "restore-index-"+newID())
	defer os.Remove(indexFile)
	env := checkpointGitEnv(indexFile)
	if _, err := runGitCheckpoint(ctx, ws, env, checkpointRestoreTime, "read-tree", ref); err != nil {
		cleanup()
		return "", nil, err
	}
	prefix := dir + string(os.PathSeparator)
	if _, err := runGitCheckpoint(ctx, ws, env, checkpointRestoreTime, "checkout-index", "-a", "--prefix="+prefix); err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}

// checkpointPlan مقایسه‌ی snapshot با وضعیت فعلی.
type checkpointPlan struct {
	snapshot  []checkpointEntry
	rewrite   []string
	trash     []string
	unchanged []string
	skipped   []string
}

func buildCheckpointPlan(ctx context.Context, ws, ref string, matDir string) (checkpointPlan, error) {
	var plan checkpointPlan
	entries, err := checkpointSnapshot(ctx, ws, ref)
	if err != nil {
		return plan, err
	}
	current, err := checkpointCurrentFiles(ctx, ws)
	if err != nil {
		return plan, err
	}
	plan.snapshot = entries
	inSnapshot := make(map[string]bool, len(entries))
	for _, e := range entries {
		inSnapshot[e.Path] = true
		if e.Sym {
			plan.skipped = append(plan.skipped, e.Path)
			continue
		}
		cur, cerr := os.ReadFile(filepath.Join(ws, filepath.FromSlash(e.Path)))
		if cerr != nil {
			plan.rewrite = append(plan.rewrite, e.Path) // در snapshot هست، الان نیست → بازسازی
			continue
		}
		want, werr := os.ReadFile(filepath.Join(matDir, filepath.FromSlash(e.Path)))
		if werr != nil {
			plan.skipped = append(plan.skipped, e.Path)
			continue
		}
		if string(cur) == string(want) {
			plan.unchanged = append(plan.unchanged, e.Path)
		} else {
			plan.rewrite = append(plan.rewrite, e.Path)
		}
	}
	for _, p := range current {
		if !inSnapshot[p] {
			plan.trash = append(plan.trash, p)
		}
	}
	sort.Strings(plan.rewrite)
	sort.Strings(plan.trash)
	sort.Strings(plan.unchanged)
	sort.Strings(plan.skipped)
	return plan, nil
}

// PreviewCheckpointRestore پیش‌نمایش بازگردانی کل پروژه به قبل از این نوبت.
func (a *App) PreviewCheckpointRestore(convID string, turnID int64) CheckpointRestorePreview {
	defer a.recoverBound("PreviewCheckpointRestore")
	preview := CheckpointRestorePreview{}
	ws, ok := a.checkpointWorkspace()
	if !ok {
		preview.Error = "نقطه‌ی بازگشت در دسترس نیست (پوشه‌ی کاری مخزن git نیست)."
		return preview
	}
	if turnID <= 0 || strings.TrimSpace(convID) == "" {
		preview.Error = "شناسه‌ی گفتگو/نوبت نامعتبر است."
		return preview
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkpointRestoreTime)
	defer cancel()
	ref := checkpointRef(convID, turnID)
	if _, err := runGitCheckpoint(ctx, ws, nil, checkpointTimeout, "rev-parse", "--verify", ref+"^{commit}"); err != nil {
		preview.Error = "برای این نوبت نقطه‌ی بازگشتی ثبت نشده است."
		return preview
	}
	matDir, cleanup, err := a.materializeCheckpoint(ctx, ws, ref)
	if err != nil {
		preview.Error = "خواندن نقطه‌ی بازگشت ممکن نشد: " + err.Error()
		return preview
	}
	defer cleanup() // مقایسه از همین پوشه می‌خواند؛ پاک‌سازی بعد از آن
	plan, err := buildCheckpointPlan(ctx, ws, ref, matDir)
	if err != nil {
		preview.Error = "مقایسه‌ی پروژه با نقطه‌ی بازگشت ممکن نشد: " + err.Error()
		return preview
	}
	preview.Available = true
	preview.Rewrite = plan.rewrite
	preview.Trash = plan.trash
	preview.Unchanged = plan.unchanged
	preview.Skipped = plan.skipped
	return preview
}

// workspaceTrashDir سبد زباله‌ی این پوشه‌ی کاری (زمان‌دار).
func (a *App) workspaceTrashDir(stamp string) (string, error) {
	dir, err := a.configDir()
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.ToSlash(ws))))
	return filepath.Join(dir, "trash", hex.EncodeToString(sum[:8]), stamp), nil
}

// RestoreCheckpoint بازگردانی کل پروژه به وضعیت قبل از نوبت. هرگز حذف
// نمی‌کند: فایل‌های فعلی که باید عوض شوند اول به سبد زباله می‌روند.
func (a *App) RestoreCheckpoint(convID string, turnID int64) CheckpointRestoreReport {
	defer a.recoverBound("RestoreCheckpoint")
	var report CheckpointRestoreReport
	ws, ok := a.checkpointWorkspace()
	if !ok {
		report.Errors = append(report.Errors, "نقطه‌ی بازگشت در دسترس نیست (پوشه‌ی کاری مخزن git نیست).")
		return report
	}
	if turnID <= 0 || strings.TrimSpace(convID) == "" {
		report.Errors = append(report.Errors, "شناسه‌ی گفتگو/نوبت نامعتبر است.")
		return report
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkpointRestoreTime)
	defer cancel()
	ref := checkpointRef(convID, turnID)
	if _, err := runGitCheckpoint(ctx, ws, nil, checkpointTimeout, "rev-parse", "--verify", ref+"^{commit}"); err != nil {
		report.Errors = append(report.Errors, "برای این نوبت نقطه‌ی بازگشتی ثبت نشده است.")
		return report
	}
	matDir, cleanup, err := a.materializeCheckpoint(ctx, ws, ref)
	if err != nil {
		report.Errors = append(report.Errors, "خواندن نقطه‌ی بازگشت ممکن نشد: "+err.Error())
		return report
	}
	defer cleanup()
	plan, err := buildCheckpointPlan(ctx, ws, ref, matDir)
	if err != nil {
		report.Errors = append(report.Errors, "مقایسه‌ی پروژه ممکن نشد: "+err.Error())
		return report
	}
	report.Skipped = append(report.Skipped, plan.skipped...)

	trashDir, terr := a.workspaceTrashDir(time.Now().Format("2006-01-02_150405.000"))
	if terr != nil {
		report.Errors = append(report.Errors, "ساخت پوشه‌ی سبد زباله ممکن نشد: "+terr.Error())
		return report
	}
	// ۱) فایل‌هایی که در snapshot نیستند → سبد زباله
	for _, rel := range plan.trash {
		if err := moveToTrash(ws, rel, trashDir); err != nil {
			report.Errors = append(report.Errors, rel+": "+err.Error())
			continue
		}
		report.Trashed = append(report.Trashed, rel)
	}
	// ۲) فایل‌های snapshot: نسخه‌ی فعلی (اگر متفاوت است) به سبد، سپس نوشتن
	for _, e := range plan.snapshot {
		if e.Sym {
			continue
		}
		abs, perr := checkpointAbs(ws, e.Path)
		if perr != nil {
			report.Errors = append(report.Errors, e.Path+": "+perr.Error())
			continue
		}
		want, rerr := os.ReadFile(filepath.Join(matDir, filepath.FromSlash(e.Path)))
		if rerr != nil {
			report.Errors = append(report.Errors, e.Path+": "+rerr.Error())
			continue
		}
		cur, cerr := os.ReadFile(abs)
		if cerr == nil && string(cur) == string(want) {
			continue // یکسان — دست نزن
		}
		if cerr == nil {
			if err := moveToTrash(ws, e.Path, trashDir); err != nil {
				report.Errors = append(report.Errors, e.Path+": "+err.Error())
				continue
			}
			report.Trashed = append(report.Trashed, e.Path)
		}
		mode := os.FileMode(0o644)
		if e.Mode == "100755" {
			mode = 0o755
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			report.Errors = append(report.Errors, e.Path+": "+err.Error())
			continue
		}
		if err := atomicWriteFile(abs, want, mode); err != nil {
			report.Errors = append(report.Errors, e.Path+": "+err.Error())
			continue
		}
		report.Restored = append(report.Restored, e.Path)
	}
	if len(report.Trashed) > 0 {
		report.TrashDir = trashDir
	}
	sort.Strings(report.Restored)
	sort.Strings(report.Trashed)
	runtimeEmit(a, "agent:checkpoint_restored", map[string]interface{}{
		"conversationId": convID,
		"turnId":         turnID,
		"report":         report,
	})
	return report
}

// checkpointAbs مسیر مطلق داخل پوشه‌ی کاری (با محافظ فرار مسیر).
func checkpointAbs(ws, rel string) (string, error) {
	if !safeCheckpointRel(rel) {
		return "", fmt.Errorf("unsafe path")
	}
	abs := filepath.Join(ws, filepath.FromSlash(rel))
	if !withinDir(abs, ws) {
		return "", fmt.Errorf("path escapes the workspace")
	}
	return abs, nil
}

// moveToTrash فایل را (با حفظ ساختار مسیر) به سبد زباله منتقل می‌کند.
func moveToTrash(ws, rel, trashDir string) error {
	abs, err := checkpointAbs(ws, rel)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil // چیزی برای جابه‌جایی نیست
	}
	dst, perr := checkpointAbs(trashDir, rel)
	if perr != nil {
		return perr
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Rename(abs, dst); err != nil {
		// مرزهای درایو متفاوت (config روی درایو دیگر) → کپی+حذف
		return copyFileThenRemove(abs, dst)
	}
	return nil
}

// copyFileThenRemove کپی امن فایل (fallback جابه‌جایی بین درایوها).
func copyFileThenRemove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

// ---------------------------------------------------------------- سبد زباله

// sweepCheckpointTrash پاک‌سازی سبد زباله در شروع برنامه: قدیمی‌تر از ۱۴ روز
// یا بیش از ۵۰۰ مگابایت (قدیمی‌ترین‌ها اول).
func sweepCheckpointTrash(cfgDir string) int {
	root := filepath.Join(cfgDir, "trash")
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	removed := 0
	for _, wsEntry := range entries {
		if !wsEntry.IsDir() {
			continue
		}
		wsRoot := filepath.Join(root, wsEntry.Name())
		stamps, _ := os.ReadDir(wsRoot)
		type trashItem struct {
			path string
			mod  time.Time
			size int64
		}
		var items []trashItem
		for _, s := range stamps {
			if !s.IsDir() {
				continue
			}
			p := filepath.Join(wsRoot, s.Name())
			it := trashItem{path: p}
			if info, err := s.Info(); err == nil {
				it.mod = info.ModTime()
			}
			it.size = dirSizeCapped(p, checkpointTrashBytes)
			items = append(items, it)
		}
		sort.Slice(items, func(i, j int) bool { return items[i].mod.Before(items[j].mod) })
		cutoff := time.Now().AddDate(0, 0, -checkpointTrashDays)
		var total int64
		for _, it := range items {
			total += it.size
		}
		for _, it := range items {
			if it.mod.After(cutoff) && total <= checkpointTrashBytes {
				continue
			}
			if os.RemoveAll(it.path) == nil {
				removed++
				total -= it.size
			}
		}
		if rest, _ := os.ReadDir(wsRoot); len(rest) == 0 {
			_ = os.Remove(wsRoot)
		}
	}
	return removed
}

// dirSizeCapped اندازه‌ی پوشه تا سقف (برای کران‌دار نگه‌داشتن sweep).
func dirSizeCapped(dir string, cap int64) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		if total > cap {
			return filepath.SkipAll
		}
		return nil
	})
	return total
}
