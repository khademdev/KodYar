// مدل نوبت (Turn) و بازبینی پایان‌نوبت (Stage 2).
//
// هر اجرای ایجنت (یک streamChat) یک TurnID یکتا و صعودی می‌گیرد؛ همه‌ی
// FileChangeهای همان نوبت با آن علامت می‌خورند و دستورات اجراشده در یک
// turnRecorder از طریق ctx جمع می‌شوند. در پایان نوبت، «خلاصه‌ی واقعی» از
// همین رکوردها ساخته می‌شود — هرگز از متن مدل (ground truth).
//
// خلاصه: chat:turn_summary {conversationId, turnId, files:[…], commands:[…],
// verification:{ran, ok, command}, tokens:{prompt, completion}} + نگه‌داریِ
// محدود (آخرین ۵۰ نوبت در هر گفتگو) تا با بازکردن دوباره‌ی گفتگو دیده شود.
//
// قفل‌ها: turnRecorder قفل داخلی خودش را دارد و فقط از ابزارها (داخل
// اجرای ابزار) صدا زده می‌شود؛ emitTurnSummary اول changes.mu (خواندن
// رکوردها) و بعد a.mu (ذخیره‌ی گفتگو) می‌گیرد — هرگز هم‌زمان؛ emit بیرون
// از هر دو (قاعده‌ی معماری).
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"sync"
)

// ---------------------------------------------------------------- زمینه‌ی نوبت

type turnCtxKey struct{}
type recorderCtxKey struct{}

// withTurnContext شناسه‌ی نوبت و ضبط‌کننده را به ctx می‌چسباند (فقط streamChat).
// ctx به همه‌ی ابزارها — از طریق executeToolCalls و حتی زیرایجنت (که ctx
// والد را ارث می‌برد) — می‌رسد.
func withTurnContext(ctx context.Context, turnID int64, rec *turnRecorder) context.Context {
	ctx = context.WithValue(ctx, turnCtxKey{}, turnID)
	return context.WithValue(ctx, recorderCtxKey{}, rec)
}

// turnIDFrom شناسه‌ی نوبت از ctx (۰ = خارج از نوبت، مثل تست‌های قدیمی).
func turnIDFrom(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	id, _ := ctx.Value(turnCtxKey{}).(int64)
	return id
}

// turnRecorderFrom ضبط‌کننده‌ی نوبت از ctx (nil = بی‌خطر، بدون ثبت).
func turnRecorderFrom(ctx context.Context) *turnRecorder {
	if ctx == nil {
		return nil
	}
	rec, _ := ctx.Value(recorderCtxKey{}).(*turnRecorder)
	return rec
}

// nextTurnID شناسه‌ی نوبتِ بعدی — صعودیِ سراسری (atomic، بدون قفل).
func (a *App) nextTurnID() int64 {
	return a.turnCounter.Add(1)
}

// ---------------------------------------------------------------- ضبط‌کننده‌ی نوبت

// turnCommandRecord یک دستور اجراشده در نوبت (برای خلاصه‌ی پایان نوبت).
type turnCommandRecord struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exitCode"`
	ExitOK   bool   `json:"exitOk"`
	Verified bool   `json:"verified"` // الگوی راستی‌آزمایی + خروج موفق
	At       int64  `json:"at"`
}

// سقف دستورات ثبت‌شده در هر نوبت — حافظه‌ی کراندار.
const maxTurnCommands = 200

// turnRecorder وضعیت نوبت جاری: دستورات + توالیِ «آخرین تغییر فایل» و
// «آخرین راستی‌آزمایی موفق» (برای هشدار «بعد از آخرین تغییری تست اجرا نشد»).
type turnRecorder struct {
	mu         sync.Mutex
	seq        int64
	lastModSeq int64
	lastVerSeq int64
	lastVerCmd string
	commands   []turnCommandRecord
}

// recordFileChange یک تغییر فایلِ موفق را ثبت می‌کند (توالی جلو می‌رود).
func (r *turnRecorder) recordFileChange() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.seq++
	r.lastModSeq = r.seq
	r.mu.Unlock()
}

// recordCommand یک دستور اجراشده را ثبت می‌کند؛ اگر الگوی راستی‌آزمایی
// باشد و موفق خارج شده باشد، توالیِ راستی‌آزمایی هم جلو می‌رود.
func (r *turnRecorder) recordCommand(command string, exitCode int, exitOK bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := turnCommandRecord{
		Command:  command,
		ExitCode: exitCode,
		ExitOK:   exitOK,
		Verified: exitOK && isVerificationCommand(command),
		At:       timeNow(),
	}
	if len(r.commands) < maxTurnCommands {
		r.commands = append(r.commands, rec)
	}
	if rec.Verified {
		r.seq++
		r.lastVerSeq = r.seq
		r.lastVerCmd = command
	}
}

// snapshot نسخه‌ی پایدار دستورات + وضعیت راستی‌آزمایی.
// verification.ok یعنی «پس از آخرین تغییر فایل، حداقل یک راستی‌آزماییِ موفق
// اجرا شده» — همان چیزی که هشدار پایان نوبت به آن وابسته است.
func (r *turnRecorder) snapshot() ([]turnCommandRecord, TurnVerification) {
	if r == nil {
		return nil, TurnVerification{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cmds := append([]turnCommandRecord(nil), r.commands...)
	v := TurnVerification{
		Ran:     r.lastVerSeq > 0,
		OK:      r.lastVerSeq > 0 && r.lastVerSeq > r.lastModSeq,
		Command: r.lastVerCmd,
	}
	return cmds, v
}

// TurnVerification وضعیت راستی‌آزمایی نوبت.
type TurnVerification struct {
	Ran     bool   `json:"ran"`               // اصلاً دستور راستی‌آزمایی اجرا شد؟
	OK      bool   `json:"ok"`                // بعد از آخرین تغییر، موفق؟
	Command string `json:"command,omitempty"` // آخرین دستور راستی‌آزمایی
}

// verificationCommandPrefixes پیشوندهای دستورِ «راستی‌آزمایی» (۵.۲ و خلاصه‌ی
// نوبت) — خروج موفقِ این‌ها پس از آخرین تغییر، نوبت را «تأییدشده» می‌کند.
var verificationCommandPrefixes = []string{
	"go build", "go test", "go vet",
	"npm test", "npm run test", "npm run build", "npm run lint", "npm run typecheck",
	"cargo build", "cargo test", "cargo check",
	"dotnet build", "dotnet test",
	"pytest", "python -m pytest", "python3 -m pytest", "python -m unittest", "python3 -m unittest",
	"mvn test", "mvn build", "mvn package",
	"gradle test", "gradle build",
	"make test", "make build", "make check",
	"tsc",
}

// isVerificationCommand آیا دستور (نرمال‌شده) یک راستی‌آزمایی ساخت/تست است؟
func isVerificationCommand(cmd string) bool {
	n := normalizeSegment(cmd)
	if n == "" {
		return false
	}
	for _, p := range verificationCommandPrefixes {
		if n == p || strings.HasPrefix(n, p+" ") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- خلاصه‌ی نوبت

// TurnSummaryFile فایلِ تغییرکرده‌ی نوبت (چکیده‌ی FileChange).
type TurnSummaryFile struct {
	Path      string `json:"path"`
	Op        string `json:"op"` // create | modify | delete | move
	Added     int    `json:"added"`
	Removed   int    `json:"removed"`
	ChangeID  string `json:"changeId"`
	Protected bool   `json:"protected"`
	Reverted  bool   `json:"reverted,omitempty"`
	MovedTo   string `json:"movedTo,omitempty"`
}

// TurnTokens مصرف توکن نوبت.
type TurnTokens struct {
	Prompt     int64 `json:"prompt"`
	Completion int64 `json:"completion"`
}

// TurnSummary خلاصه‌ی واقعیِ یک نوبت — محاسبه‌شده از رکوردهای ابزار.
type TurnSummary struct {
	ConversationID string              `json:"conversationId"`
	TurnID         int64               `json:"turnId"`
	Files          []TurnSummaryFile   `json:"files"`
	Commands       []turnCommandRecord `json:"commands"`
	Verification   TurnVerification    `json:"verification"`
	Tokens         TurnTokens          `json:"tokens"`
	At             int64               `json:"at"`
}

// سقف خلاصه‌های نگه‌داری‌شده در هر گفتگو (۴.۲).
const maxTurnSummariesPerConv = 50

// emitTurnSummary خلاصه‌ی پایان نوبت را از رکوردهای واقعی می‌سازد، نسخه‌ی
// محدودش را در گفتگو ذخیره می‌کند و رویداد chat:turn_summary را می‌فرستد.
// فقط وقتی صدا زده می‌شود که فایلی عوض شده یا دستوری اجرا شده باشد.
func (a *App) emitTurnSummary(convID string, turnID int64, rec *turnRecorder, prompt, completion int64) {
	commands, verification := rec.snapshot()
	files := a.turnFileChanges(convID, turnID)
	if len(files) == 0 && len(commands) == 0 {
		return
	}
	sumFiles := make([]TurnSummaryFile, 0, len(files))
	for _, f := range files {
		sumFiles = append(sumFiles, TurnSummaryFile{
			Path:      f.Path,
			Op:        f.Kind,
			Added:     f.Added,
			Removed:   f.Removed,
			ChangeID:  f.ID,
			Protected: f.Protected,
			Reverted:  f.Reverted,
			MovedTo:   f.MovedTo,
		})
	}
	ts := TurnSummary{
		ConversationID: convID,
		TurnID:         turnID,
		Files:          sumFiles,
		Commands:       commands,
		Verification:   verification,
		Tokens:         TurnTokens{Prompt: prompt, Completion: completion},
		At:             timeNow(),
	}

	// ذخیره‌ی محدود در گفتگو (بازکردن دوباره → نمایش) — a.mu کوتاه.
	a.mu.Lock()
	if conv, err := a.loadConversation(convID); err == nil {
		conv.TurnSummaries = append(conv.TurnSummaries, ts)
		if len(conv.TurnSummaries) > maxTurnSummariesPerConv {
			conv.TurnSummaries = conv.TurnSummaries[len(conv.TurnSummaries)-maxTurnSummariesPerConv:]
		}
		if err := a.saveConversation(conv); err != nil {
			logAppErrorFor(a, "save", "turn summary: "+err.Error())
		}
	}
	a.mu.Unlock()

	runtimeEmit(a, "chat:turn_summary", map[string]interface{}{"summary": ts})
}

// turnFileChanges رکوردهای تغییرِ همان نوبت (بدون diff — سبک برای خلاصه).
func (a *App) turnFileChanges(convID string, turnID int64) []FileChange {
	if turnID <= 0 || strings.TrimSpace(convID) == "" {
		return nil
	}
	var out []FileChange
	for _, c := range a.loadChanges() {
		if c.ConvID == convID && c.TurnID == turnID && !c.Rejected {
			c.Diff = ""
			out = append(out, c)
		}
	}
	return out
}

// ---------------------------------------------------------------- API فرانت‌اند

// GetTurnChanges فهرست تغییرات یک نوبت (برای کارت بازبینی پایان نوبت).
func (a *App) GetTurnChanges(convID string, turnID int64) []FileChange {
	return a.turnFileChanges(convID, turnID)
}

// TurnChangesReport نتیجه‌ی «برگرداندن همه‌ی تغییرات این نوبت».
type TurnChangesReport struct {
	Reverted  []string `json:"reverted"`
	Conflicts []string `json:"conflicts"`
	Errors    []string `json:"errors"`
}

// RevertTurnChanges همه‌ی تغییرات اعمال‌شده‌ی یک نوبت را از آخر به اول
// برمی‌گرداند — با «قاعده‌ی تعارض»: اگر محتوای فعلی فایل با چیزی که ایجنت
// نوشته فرق داشته باشد (کاربر بعداً دستکاری کرده)، فایل بازنویسی نمی‌شود
// و به‌عنوان تعارض گزارش می‌شود تا کاربر خودش تصمیم بگیرد.
func (a *App) RevertTurnChanges(convID string, turnID int64) TurnChangesReport {
	var report TurnChangesReport
	if turnID <= 0 || strings.TrimSpace(convID) == "" {
		report.Errors = append(report.Errors, "شناسه‌ی گفتگو/نوبت نامعتبر است")
		return report
	}

	a.changes.mu.Lock()
	list := a.loadChangesLocked()
	// نوبت را از آخر به اول برمی‌گردانیم
	for i := len(list) - 1; i >= 0; i-- {
		ch := &list[i]
		if ch.ConvID != convID || ch.TurnID != turnID || ch.Rejected || ch.Reverted {
			continue
		}
		if a.turnChangeConflict(ch) {
			report.Conflicts = append(report.Conflicts, ch.Path)
			continue
		}
		if err := a.revertChange(ch); err != nil {
			report.Errors = append(report.Errors, ch.Path+": "+err.Error())
			continue
		}
		ch.Reverted = true
		ch.RevertedAt = timeNow()
		a.deleteBackupFile(ch.BackupFile)
		ch.BackupFile = ""
		report.Reverted = append(report.Reverted, ch.Path)
	}
	a.changes.cached = list
	a.changes.cacheOK = true
	if p, err := a.changesPath(); err == nil {
		if raw, merr := marshalChanges(list); merr == nil {
			tmp := p + ".tmp"
			if os.WriteFile(tmp, raw, 0o600) == nil {
				_ = os.Rename(tmp, p)
			}
		}
	}
	a.changes.mu.Unlock()

	if len(report.Reverted) > 0 {
		runtimeEmit(a, "agent:change_reverted", map[string]interface{}{
			"conversationId": convID,
			"turnId":         turnID,
			"paths":          report.Reverted,
		})
	}
	return report
}

// marshalChanges سریال‌سازی رکوردها (جداشده برای تست‌پذیری).
func marshalChanges(list []FileChange) ([]byte, error) {
	return jsonMarshalHelper(list)
}

// turnChangeConflict آیا برگرداندن این تغییر با کاری که کاربر بعد از ایجنت
// کرده تعارض دارد؟ (مقایسه‌ی هش محتوای فعلی با هشِ چیزی که ایجنت نوشته)
func (a *App) turnChangeConflict(ch *FileChange) bool {
	switch ch.Kind {
	case ChangeCreate, ChangeModify:
		if ch.ContentSHA256 == "" {
			return false // رکورد قدیمی بدون هش — رفتار پیشین (بازگردانی)
		}
		p, err := a.resolveWorkspacePath(ch.Path)
		if err != nil {
			return true
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return false // فایل نیست → دست کاربر باز است؛ برگرداندن بی‌ضرر
		}
		return sha256Hex(data) != ch.ContentSHA256
	case ChangeDelete:
		p, err := a.resolveWorkspacePath(ch.Path)
		if err != nil {
			return true
		}
		if _, serr := os.Stat(p); serr == nil {
			return true // کاربر فایل را دوباره ساخته — بازنویسی نکن
		}
		return false
	case ChangeMove:
		if ch.MovedTo == "" || ch.ContentSHA256 == "" {
			return false
		}
		dst, err := a.resolveWorkspacePath(ch.MovedTo)
		if err != nil {
			return true
		}
		if data, rerr := os.ReadFile(dst); rerr == nil {
			return sha256Hex(data) != ch.ContentSHA256
		}
		return false
	}
	return false
}

// sha256Hex هش SHA-256 باینری‌امن (CRLF/BOM/باینری — هر بایت).
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------- مسیرهای محافظت‌شده

// defaultProtectedGlobs مسیرهایی که حتی در حالت «ویرایش خودکار» هم تأیید
// می‌خواهند (۴.۱): مخزن‌ها/تنظیمات ابزارها، اسرار و «فایل‌های تعریف ساخت»
// که دستکاری‌شان کدِ بعدیِ کاملاً امن را به اجرای کد دلخواه تبدیل می‌کند.
var defaultProtectedGlobs = []string{
	".git/**", ".github/**", ".myai/**", ".vscode/**", ".idea/**",
	".env*", "*.pem", "*.key", "*.pfx",
	"package.json", "Makefile", "makefile", "*.mk", "justfile",
	"Taskfile.y*ml", "*.csproj", "pom.xml", "build.gradle*",
}

// protectedGlobsLocked الگوهای محافظت‌شده (پیش‌فرض در نبود تنظیم). a.mu.
func (a *App) protectedGlobsLocked() []string {
	if len(a.settings.ProtectedGlobs) == 0 {
		return defaultProtectedGlobs
	}
	return a.settings.ProtectedGlobs
}

// pathIsProtected آیا این مسیر نسبی محافظت‌شده است؟ (منبع یا مقصدِ
// write/edit/move/delete)
func (a *App) pathIsProtected(rel string) bool {
	a.mu.Lock()
	globs := a.protectedGlobsLocked()
	a.mu.Unlock()
	return pathMatchesProtectedGlobs(rel, globs)
}

// pathMatchesProtectedGlobs تطبیق مسیر با الگوهای محافظت: الگوی بدون «/»
// با نام فایل تطبیق می‌کند؛ الگوی مسیردار جزء‌به‌جزء با پشتیبانی **.
func pathMatchesProtectedGlobs(rel string, globs []string) bool {
	rel = strings.ToLower(strings.Trim(strings.ReplaceAll(rel, "\\", "/"), "/"))
	if rel == "" {
		return false
	}
	comps := strings.Split(rel, "/")
	base := comps[len(comps)-1]
	for _, g := range globs {
		g = strings.TrimSpace(strings.ToLower(g))
		if g == "" {
			continue
		}
		if !strings.Contains(g, "/") {
			if globMatch(g, base) {
				return true
			}
			continue
		}
		if globCompsMatch(strings.Split(strings.Trim(g, "/"), "/"), comps) {
			return true
		}
	}
	return false
}

// globCompsMatch تطبیق بازگشتی اجزای مسیر؛ «**» صفر یا چند جزء را می‌بلعد.
func globCompsMatch(pat, path []string) bool {
	if len(pat) == 0 {
		return len(path) == 0
	}
	if pat[0] == "**" {
		for k := 0; k <= len(path); k++ {
			if globCompsMatch(pat[1:], path[k:]) {
				return true
			}
		}
		return false
	}
	if len(path) == 0 || !globMatch(pat[0], path[0]) {
		return false
	}
	return globCompsMatch(pat[1:], path[1:])
}
