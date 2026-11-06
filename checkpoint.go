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
	"bufio"
	"bytes"
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
	checkpointRestoreTime = 60 * time.Second // سقف بازگردانی (فایل‌های زیاد)
	checkpointMaxKeep     = 20               // نگه‌داری جدیدترین‌ها در هر پوشه
	checkpointRefPrefix   = "refs/myai/checkpoints/"
	checkpointTrashDays   = 14
	checkpointTrashBytes  = 500 << 20 // ۵۰۰ مگابایت
)

// checkpointTimeout سقف هر فراخوانی git در ساخت نقطه‌ی بازگشت (var تا تستِ
// تزریق خرابی Stage 5 بتواند مهلت کوتاه تزریق کند؛ در runtime تغییر نمی‌کند).
var checkpointTimeout = 10 * time.Second

// checkpointMaxFiles سقف فایل در snapshot و بازگردانی (var تا تست بتواند
// کران را کوچک کند؛ در runtime تغییر نمی‌کند).
var checkpointMaxFiles = 5000

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
		"GIT_CONFIG_NOSYSTEM=1", "GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
	)
	return env
}

// checkpointGitConfig پرچم‌هایی که هر فراخوانی git نقطه‌ی بازگشت را از
// تبدیل‌های کاربر جدا می‌کند: CRLF، fsmonitor و hookها. (فیلترهای
// .gitattributes/LFS هم با `--no-filters` در hash-object و خواندن خام
// cat-file بی‌اثر می‌شوند.)
func checkpointGitConfig() []string {
	return []string{
		"-c", "core.autocrlf=false",
		"-c", "core.safecrlf=false",
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=" + os.DevNull,
	}
}

// checkpointArgs آرگومان‌های کامل git با تنظیمات خنثی‌شده.
func checkpointArgs(ws string, args ...string) []string {
	out := make([]string, 0, len(args)+len(checkpointGitConfig())+2)
	out = append(out, "-C", ws)
	out = append(out, checkpointGitConfig()...)
	return append(out, args...)
}

// runGitCheckpoint یک فرمان git با محیط/مهلت مشخص اجرا می‌کند (کران‌دار).
func runGitCheckpoint(ctx context.Context, ws string, env []string, timeout time.Duration, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", checkpointArgs(ws, args...)...)
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

// runGitCheckpointInput مثل runGitCheckpoint است ولی ورودی stdin را هم
// می‌دهد (hash-object --stdin-paths و update-index --index-info).
func runGitCheckpointInput(ctx context.Context, ws string, env []string, timeout time.Duration, input string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", checkpointArgs(ws, args...)...)
	cmd.WaitDelay = 3 * time.Second
	cmd.Stdin = strings.NewReader(input)
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

	files, err := checkpointCurrentFiles(ctx, ws)
	if err != nil {
		return "", err
	}
	tree, err := checkpointBuildTree(ctx, ws, env, files)
	if err != nil {
		return "", err
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

// checkpointBlobMode حالت index برای یک فایل: اجراپذیر یا معمولی. فایل‌های
// غیرمعمول (پیوند، پوشه، دستگاه) پذیرفته نمی‌شوند.
func checkpointBlobMode(path string) (string, bool) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	if st.Mode()&0o111 != 0 {
		return "100755", true
	}
	return "100644", true
}

// checkpointBuildTree درخت snapshot را **بدون هیچ فیلتری** می‌سازد.
//
// `git add -A` ایندکس را از محتوا پر می‌کند و core.autocrlf، .gitattributes
// (text/eol) و filter‌ها (Git LFS) را اعمال می‌کند — نتیجه اینکه بازگردانی
// دیگر بایت‌به‌بایت نیست. اینجا blobها با `hash-object --no-filters`
// ساخته می‌شوند و ایندکس موقت با `update-index --index-info` پر می‌شود؛
// هیچ فیلتری اجرا نمی‌شود.
func checkpointBuildTree(ctx context.Context, ws string, env []string, files []string) (string, error) {
	// مسیرهای حاوی newline با --stdin-paths قابل انتقال نیستند (بسیار نادر).
	hashing := make([]string, 0, len(files))
	for _, p := range files {
		if strings.ContainsAny(p, "\n\r") {
			continue
		}
		hashing = append(hashing, p)
	}
	if len(hashing) == 0 {
		out, err := runGitCheckpointInput(ctx, ws, env, checkpointTimeout, "", "mktree")
		if err != nil {
			return "", err
		}
		if tree := strings.TrimSpace(out); tree != "" {
			return tree, nil
		}
		return "", fmt.Errorf("git mktree returned an empty tree")
	}
	out, err := runGitCheckpointInput(ctx, ws, env, checkpointTimeout,
		strings.Join(hashing, "\n")+"\n", "hash-object", "-w", "--no-filters", "--stdin-paths")
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(out, "\r\n"), "\n")
	if len(lines) != len(hashing) {
		return "", fmt.Errorf("git hash-object returned %d objects for %d files", len(lines), len(hashing))
	}
	var info strings.Builder
	for i, rel := range hashing {
		sha := strings.TrimSpace(lines[i])
		if len(sha) != 40 && len(sha) != 64 {
			return "", fmt.Errorf("git hash-object returned an invalid object id for %q", rel)
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
	if info.Len() == 0 {
		out, err := runGitCheckpointInput(ctx, ws, env, checkpointTimeout, "", "mktree")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(out), nil
	}
	if _, err := runGitCheckpointInput(ctx, ws, env, checkpointTimeout,
		info.String(), "update-index", "-z", "--add", "--index-info"); err != nil {
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
	return tree, nil
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
	SHA  string
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
		if len(fields) < 3 || fields[1] != "blob" {
			continue // زیرپوشه (tree) یا چیز دیگر
		}
		if !safeCheckpointRel(p) {
			continue // مسیر مشکوک اصلاً استفاده نمی‌شود
		}
		entries = append(entries, checkpointEntry{Path: p, Mode: fields[0], SHA: fields[2], Sym: fields[0] == "120000"})
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

// checkpointBlobChunk اندازه‌ی تکه‌ی مقایسه/کپی جریانی (Stage 4.3).
const checkpointBlobChunk = 64 * 1024

// blobBatch یک فرمان پایدار `git cat-file --batch` است: blobها را **خام**
// (بدون smudge/filter/eol) و جریانی می‌خواند تا نه حافظه و نه دیسک با کل
// snapshot درگیر شود.
type blobBatch struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	errB   *cappedGitBuffer
	cancel context.CancelFunc
	remain int64
	size   int64
}

func newBlobBatch(ctx context.Context, ws string) (*blobBatch, error) {
	cctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cctx, "git", checkpointArgs(ws, "cat-file", "--batch")...)
	cmd.WaitDelay = 3 * time.Second
	cmd.Env = gitEnv()
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		cancel()
		return nil, err
	}
	errB := newCappedGitBuffer(gitCaptureCap)
	cmd.Stderr = errB
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	return &blobBatch{cmd: cmd, in: in, out: bufio.NewReaderSize(outPipe, checkpointBlobChunk), errB: errB, cancel: cancel}, nil
}

// open شیء درخواستی را باز می‌کند و اندازه‌اش را برمی‌گرداند.
func (b *blobBatch) open(sha string) (int64, error) {
	if _, err := io.WriteString(b.in, sha+"\n"); err != nil {
		return 0, err
	}
	line, err := b.out.ReadString('\n')
	if err != nil {
		return 0, fmt.Errorf("git cat-file: %w (%s)", err, strings.TrimSpace(b.errB.buf.String()))
	}
	fields := strings.Fields(line)
	if len(fields) != 3 || fields[1] != "blob" {
		return 0, fmt.Errorf("git cat-file: unexpected header %q", strings.TrimSpace(line))
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return 0, fmt.Errorf("git cat-file: bad size in %q", strings.TrimSpace(line))
	}
	b.size, b.remain = size, size
	return size, nil
}

// Read فقط تا اندازه‌ی blob جاری می‌خواند (newline پایانی جزء محتوا نیست).
func (b *blobBatch) Read(p []byte) (int, error) {
	if b.remain <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > b.remain {
		p = p[:b.remain]
	}
	n, err := b.out.Read(p)
	b.remain -= int64(n)
	return n, err
}

// finish باقی محتوا را دور می‌ریزد و جریان را برای شیء بعدی هم‌تراز می‌کند.
func (b *blobBatch) finish() error {
	if b.remain > 0 {
		if _, err := io.CopyN(io.Discard, b.out, b.remain); err != nil {
			return err
		}
		b.remain = 0
	}
	_, err := b.out.ReadByte() // newline پس از محتوا
	return err
}

// Close فرآیند را می‌بندد (کران‌دار، بدون نشت).
func (b *blobBatch) Close() {
	if b == nil {
		return
	}
	_ = b.in.Close()
	_ = b.cmd.Wait()
	b.cancel()
}

// compareBlobToFile مقایسه‌ی جریانی blob با فایل: اول اندازه، بعد تکه‌های
// ۶۴KB با bytes.Equal — هیچ‌کدام کامل در حافظه نمی‌آید.
func compareBlobToFile(b *blobBatch, abs string, size int64) (bool, error) {
	f, err := os.Open(abs)
	if err != nil {
		_ = b.finish()
		return false, nil // از دست رفت بین stat و باز کردن → بازسازی
	}
	defer f.Close()
	bufA := make([]byte, checkpointBlobChunk)
	bufB := make([]byte, checkpointBlobChunk)
	remaining := size
	for remaining > 0 {
		n := int64(len(bufA))
		if n > remaining {
			n = remaining
		}
		if _, err := io.ReadFull(b, bufA[:n]); err != nil {
			return false, err
		}
		if _, err := io.ReadFull(f, bufB[:n]); err != nil {
			_ = b.finish()
			return false, nil
		}
		if !bytes.Equal(bufA[:n], bufB[:n]) {
			_ = b.finish()
			return false, nil
		}
		remaining -= n
	}
	return true, b.finish()
}

// restoreBlobToFile بازگردانی یک فایل از blob خام با فایل موقت + rename اتمیک.
func restoreBlobToFile(b *blobBatch, sha, abs string, perm os.FileMode) error {
	size, err := b.open(sha)
	if err != nil {
		return err
	}
	if err := atomicWriteStream(abs, b, size, perm); err != nil {
		return err
	}
	return b.finish()
}

// restoreFS توابع تزریق‌پذیر مسیریاب ایمن (تست خالص بدون symlink واقعی).
type restoreFS struct {
	evalSymlinks func(string) (string, error)
	lstat        func(string) (os.FileInfo, error)
	reparse      func(string) bool
}

var defaultRestoreFS = restoreFS{
	evalSymlinks: filepath.EvalSymlinks,
	lstat:        os.Lstat,
	reparse:      isReparsePoint,
}

// resolveRestorePath مسیر مطلق ایمن برای نوشتن/جابه‌جایی (Stage 4.3).
//
// بررسی لفظی `withinDir` کافی نیست: اگر یک پوشه‌ی داخل پروژه بعد از snapshot
// به symlink/junction در بیرون تبدیل شده باشد، نوشتن از پروژه خارج می‌شود.
// علاوه بر مسیر لفظی، ریشه‌ی واقعی و نزدیک‌ترین والد موجود با EvalSymlinks
// حل می‌شوند و هیچ جزء پیوندی پذیرفته نمی‌شود.
func resolveRestorePath(ws, rel string, fs restoreFS) (string, error) {
	if !safeCheckpointRel(rel) {
		return "", fmt.Errorf("unsafe path")
	}
	abs := filepath.Join(ws, filepath.FromSlash(rel))
	if !withinDir(abs, ws) {
		return "", fmt.Errorf("path escapes the workspace")
	}
	if err := validatePathForWrite(abs); err != nil {
		return "", err
	}
	parts := strings.Split(strings.ReplaceAll(rel, "\\", "/"), "/")
	for i := 1; i < len(parts); i++ {
		comp := filepath.Join(ws, filepath.FromSlash(strings.Join(parts[:i], "/")))
		if _, err := fs.lstat(comp); err != nil {
			break // باقی مسیر هنوز ساخته نشده
		}
		if fs.reparse(comp) {
			return "", fmt.Errorf("path component %q is a symbolic link or junction", strings.Join(parts[:i], "/"))
		}
	}
	realWS, err := fs.evalSymlinks(ws)
	if err != nil {
		return "", fmt.Errorf("cannot resolve workspace: %w", err)
	}
	probe := filepath.Dir(abs)
	for {
		if _, err := fs.lstat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", fmt.Errorf("path has no existing parent inside the workspace")
		}
		probe = parent
	}
	realParent, err := fs.evalSymlinks(probe)
	if err != nil {
		return "", fmt.Errorf("cannot resolve parent: %w", err)
	}
	if !withinDir(realParent, realWS) {
		return "", fmt.Errorf("path resolves outside the workspace (symlink/junction)")
	}
	if st, err := fs.lstat(abs); err == nil {
		if st.Mode()&os.ModeSymlink != 0 || fs.reparse(abs) {
			return "", fmt.Errorf("target is a symbolic link")
		}
	}
	return abs, nil
}

// checkpointPlan مقایسه‌ی snapshot با وضعیت فعلی.
type checkpointPlan struct {
	snapshot  []checkpointEntry
	rewrite   []string
	trash     []string
	unchanged []string
	skipped   []string
	refused   map[string]string // مسیر → دلیل رد ایمنی (symlink/junction)
}

// buildCheckpointPlan پروژه را با snapshot مقایسه می‌کند — کاملاً جریانی و
// **پیش از هر تغییری**. فایل‌های بزرگ نه در حافظه خوانده می‌شوند و نه در
// دیسک موادی می‌شوند (Stage 4.3).
func buildCheckpointPlan(ctx context.Context, ws, ref string) (checkpointPlan, error) {
	var plan checkpointPlan
	entries, err := checkpointSnapshot(ctx, ws, ref)
	if err != nil {
		return plan, err
	}
	current, err := checkpointCurrentFiles(ctx, ws)
	if err != nil {
		return plan, err
	}
	batch, err := newBlobBatch(ctx, ws)
	if err != nil {
		return plan, err
	}
	defer batch.Close()

	plan.snapshot = entries
	plan.refused = map[string]string{}
	inSnapshot := make(map[string]bool, len(entries))
	for _, e := range entries {
		inSnapshot[e.Path] = true
		if e.Sym {
			plan.skipped = append(plan.skipped, e.Path)
			continue
		}
		abs, rerr := resolveRestorePath(ws, e.Path, defaultRestoreFS)
		if rerr != nil {
			plan.skipped = append(plan.skipped, e.Path)
			plan.refused[e.Path] = rerr.Error()
			continue
		}
		st, serr := os.Lstat(abs)
		if serr != nil {
			plan.rewrite = append(plan.rewrite, e.Path) // در snapshot هست، الان نیست → بازسازی
			continue
		}
		if !st.Mode().IsRegular() {
			plan.skipped = append(plan.skipped, e.Path)
			plan.refused[e.Path] = "not a regular file"
			continue
		}
		size, berr := batch.open(e.SHA)
		if berr != nil {
			return plan, berr
		}
		if size != st.Size() { // اول اندازه — بدون خواندن محتوا
			plan.rewrite = append(plan.rewrite, e.Path)
			_ = batch.finish()
			continue
		}
		same, cerr := compareBlobToFile(batch, abs, size)
		if cerr != nil {
			return plan, cerr
		}
		if same {
			plan.unchanged = append(plan.unchanged, e.Path)
		} else {
			plan.rewrite = append(plan.rewrite, e.Path)
		}
	}
	for _, p := range current {
		if inSnapshot[p] {
			continue
		}
		abs := filepath.Join(ws, filepath.FromSlash(p))
		if st, err := os.Lstat(abs); err != nil || !st.Mode().IsRegular() {
			plan.skipped = append(plan.skipped, p)
			continue
		}
		plan.trash = append(plan.trash, p)
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
	plan, err := buildCheckpointPlan(ctx, ws, ref)
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
	plan, err := buildCheckpointPlan(ctx, ws, ref)
	if err != nil {
		report.Errors = append(report.Errors, "مقایسه‌ی پروژه ممکن نشد: "+err.Error())
		return report
	}
	report.Skipped = append(report.Skipped, plan.skipped...)
	// بودجه/مهلت **پیش از نخستین نوشتن** بررسی می‌شود تا بازگردانی نیمه‌کاره
	// ناشی از تمام‌شدن مهلت رخ ندهد (Stage 4.3).
	if err := ctx.Err(); err != nil {
		report.Errors = append(report.Errors, "مهلت/لغو پیش از تغییر فایل‌ها؛ هیچ فایلی تغییر نکرد.")
		return report
	}

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
	// ۲) فقط فایل‌های تغییر‌یافته (از پیش محاسبه‌شده): نسخه‌ی فعلی (اگر هست)
	// به سبد، سپس کپی جریانی blob با فایل موقت + rename اتمیک.
	batch, berr := newBlobBatch(ctx, ws)
	if berr != nil {
		report.Errors = append(report.Errors, "خواندن نقطه‌ی بازگشت ممکن نشد: "+berr.Error())
		return report
	}
	defer batch.Close()
	byPath := make(map[string]checkpointEntry, len(plan.snapshot))
	for _, e := range plan.snapshot {
		byPath[e.Path] = e
	}
	for _, rel := range plan.rewrite {
		e := byPath[rel]
		if reason, refused := plan.refused[rel]; refused {
			report.Errors = append(report.Errors, rel+": "+reason)
			continue
		}
		abs, perr := resolveRestorePath(ws, rel, defaultRestoreFS)
		if perr != nil {
			report.Errors = append(report.Errors, rel+": "+perr.Error())
			continue
		}
		if _, err := os.Lstat(abs); err == nil {
			if err := moveToTrash(ws, rel, trashDir); err != nil {
				report.Errors = append(report.Errors, rel+": "+err.Error())
				continue
			}
			report.Trashed = append(report.Trashed, rel)
		}
		mode := os.FileMode(0o644)
		if e.Mode == "100755" {
			mode = 0o755
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			report.Errors = append(report.Errors, rel+": "+err.Error())
			continue
		}
		if err := restoreBlobToFile(batch, e.SHA, abs, mode); err != nil {
			report.Errors = append(report.Errors, rel+": "+err.Error())
			break // جریان cat-file ممکن است هم‌تراز نباشد — ادامه نده
		}
		report.Restored = append(report.Restored, rel)
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

// checkpointAbs مسیر مطلق ایمن داخل پوشه‌ی کاری: فرار لفظی، نام/طول ویندوز،
// و پیوند/جانکشنی که به بیرون اشاره کند (Stage 4.3).
func checkpointAbs(ws, rel string) (string, error) {
	return resolveRestorePath(ws, rel, defaultRestoreFS)
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
	if err := os.MkdirAll(trashDir, 0o700); err != nil { // ریشه پیش از حل مسیر واقعی
		return err
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
