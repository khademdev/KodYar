// مدیریت تغییرات فایل توسط ایجنت — پیش‌نمایش Diff، تأیید کاربر، بکاپ و Revert.
//
// جریان کار:
// 1. قبل از write_file / edit_file / move_path / delete_path، محتوای جدید
// بدون نوشتن روی دیسک ساخته می‌شود و diff آن با وضعیت فعلی حساب می‌شود.
// 2. اگر «تأیید تغییرات» فعال باشد، رویداد «agent:confirm» به UI فرستاده
// می‌شود و اجرای ابزار روی پاسخ کاربر (یا timeout / توقف) منتظر می‌ماند.
// 3. پس از اعمال، نسخه‌ی قبلی فایل در پوشه‌ی backups داخل config dir
// ذخیره می‌شود و رکورد تغییر در changes.json ثبت می‌شود.
// 4. Revert از روی بکاپ انجام می‌شود؛ حتی بعد از بستن برنامه معتبر است
// چون همه‌چیز روی دیسک است (پشتیبانی Resume).
//
// امنیت: مسیر بکاپ‌ها داخل config dir است و IDها تصادفی‌اند؛ revert مسیر
// را دوباره از resolveWorkspacePath عبور می‌دهد تا نتواند خارج از workspace بنویسد.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// انواع تغییر فایل
const (
	ChangeCreate = "create"
	ChangeModify = "modify"
	ChangeDelete = "delete"
	ChangeMove   = "move"
)

// محدودیت‌ها
const (
	confirmTimeout = 3 * time.Minute // حداکثر انتظار برای پاسخ کاربر
	maxKeptChanges = 500             // حداکثر رکورد نگه‌داری‌شده
	// سقف فایل changes.json — در appendChangeLocked حجم سریال‌شده‌ی رکوردها
	// با ۴ برابر این مقدار مقایسه می‌شود؛ با عبور از آن، نیمه‌ی اخیر نگه
	// داشته می‌شود و بکاپ‌های نیمه‌ی قدیمی پاک می‌شوند. مقدار به‌قدر کافی
	// بزرگ انتخاب شده تا در استفادهٔ عادی هرگز فعال نشود و حذف رکوردها
	// عملاً همیشه با سقف maxKeptChanges انجام شود.
	maxChangesFileSize = 2 * 1024 * 1024
	// سقف مجموع حجم فایل‌های بکاپ روی دیسک — با عبور از آن، قدیمی‌ترین
	// بکاپ‌ها پاک می‌شوند تا AppData رشد بی‌رویه نکند.
	maxBackupTotalBytes = 200 * 1024 * 1024 // ۲۰۰ مگابایت
)

// FileChange رکورد یک تغییر اعمال‌شده (یا ردشده) روی فایل پروژه.
type FileChange struct {
	ID         string `json:"id"`
	ConvID     string `json:"convId"`
	Path       string `json:"path"`                 // مسیر نسبی داخل workspace
	Kind       string `json:"kind"`                 // create | modify | delete | move
	MovedTo    string `json:"movedTo,omitempty"`    // فقط move
	Added      int    `json:"added"`                // خطوط افزوده
	Removed    int    `json:"removed"`              // خطوط حذف
	Diff       string `json:"diff,omitempty"`       // unified diff (برای نمایش)
	BackupFile string `json:"backupFile,omitempty"` // نام فایل بکاپ (نسبی به پوشه‌ی backups)
	At         int64  `json:"at"`
	Rejected   bool   `json:"rejected,omitempty"` // کاریر تغییر را رد کرد
	Reverted   bool   `json:"reverted,omitempty"` // تغییر برگردانده شد
	RevertedAt int64  `json:"revertedAt,omitempty"`

	// ---- ۱.۸.۰: مدل نوبت و حالت ویرایش (Stage 2) ----
	TurnID        int64  `json:"turnId,omitempty"`        // شناسه‌ی نوبتِ سازنده‌ی تغییر
	Protected     bool   `json:"protected,omitempty"`     // مسیر محافظت‌شده → حتی در auto تأیید می‌خواهد
	HeavyDelete   bool   `json:"heavyDelete,omitempty"`   // حذف سنگین (پوشه/حجیم/بدون بکاپ) → تأیید
	ContentSHA256 string `json:"contentSha256,omitempty"` // هشِ چیزی که ایجنت نوشت (تشخیص تعارض)
	ConfirmNote   string `json:"confirmNote,omitempty"`   // یادداشت فارسیِ دلیل حساسیت برای مودال
}

// pendingConfirm یک درخواست تأیید در انتظار پاسخ کاربر.
// (مهلت واقعی با تایمر داخل requestFileConfirm مدیریت می‌شود؛ این ساختار
// فقط کانال پاسخ را نگه می‌دارد.)
type pendingConfirm struct {
	ch chan bool
}

// changesStore وضعیت تغییرات روی دیسک + تأییدهای در جریان.
// permAsks: مودال‌های دسترسی در جریان — با همان قفل محافظت می‌شوند.
type changesStore struct {
	mu       sync.Mutex
	confirms map[string]*pendingConfirm
	permAsks map[string]chan string
	// cache داخل حافظه برای لیست تغییرات (برای جلوگیری از خواندن مکرر دیسک)
	cached  []FileChange
	cacheOK bool
}

// initChangesStore باید یک‌بار در startup صدا زده شود.
func (a *App) initChangesStore() {
	if a.changes == nil {
		a.changes = &changesStore{
			confirms: map[string]*pendingConfirm{},
			permAsks: map[string]chan string{},
		}
	}
}

func (a *App) backupsDir() (string, error) {
	dir, err := a.configDir()
	if err != nil {
		return "", err
	}
	b := filepath.Join(dir, "backups")
	if err := os.MkdirAll(b, 0o755); err != nil {
		return "", err
	}
	return b, nil
}

func (a *App) changesPath() (string, error) {
	dir, err := a.configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "changes.json"), nil
}

// loadChanges لیست تغییرات را از دیسک می‌خواند (با کش).
// همیشه یک کپی از اسلایس برمی‌گردد تا caller نتواند آرایه‌ی داخلی کش را
// تغییر دهد (جلوگیری از race و خراب‌شدن کش).
func (a *App) loadChanges() []FileChange {
	a.changes.mu.Lock()
	defer a.changes.mu.Unlock()
	if a.changes.cacheOK {
		return append([]FileChange(nil), a.changes.cached...)
	}
	list := a.loadChangesLocked()
	a.changes.cached = list
	a.changes.cacheOK = true
	return append([]FileChange(nil), list...)
}

// deleteBackupFile فایل بکاپ یک تغییر را حذف می‌کند — پس از برگرداندن یا
// حذف رکورد، بکاپ دیگر کاربری ندارد و ماندنش فقط دیسک را پر می‌کند
// (نشت فضای دیسک: هر تغییر اعمال‌شده یک فایل.orig می‌ساخت که هرگز پاک نمی‌شد).
func (a *App) deleteBackupFile(name string) {
	if name == "" {
		return
	}
	bdir, err := a.backupsDir()
	if err != nil {
		return
	}
	_ = os.Remove(filepath.Join(bdir, name))
}

// appendChangeLocked یک رکورد را ذخیره می‌کند. باید با changes.mu قفل‌شده صدا زده شود.
func (a *App) appendChangeLocked(ch FileChange) {
	list := a.changes.cached
	if !a.changes.cacheOK {
		list = a.loadChangesLocked()
	}
	list = append(list, ch)
	if len(list) > maxKeptChanges {
		// رکوردهای بیرون‌مانده از سقف + بکاپ‌هایشان حذف می‌شوند
		dropped := list[:len(list)-maxKeptChanges]
		for _, d := range dropped {
			a.deleteBackupFile(d.BackupFile)
		}
		list = list[len(list)-maxKeptChanges:]
	}
	a.changes.cached = list
	a.changes.cacheOK = true

	p, err := a.changesPath()
	if err != nil {
		return
	}
	raw, merr := json.Marshal(list)
	if merr != nil {
		return
	}
	if len(raw) > maxChangesFileSize*4 {
		// خیلی بزرگ شد — نصف اخیر نگه داشته می‌شود (بکاپ‌های نیمه‌ی حذف‌شده هم پاک می‌شوند)
		half := list[len(list)/2:]
		for _, d := range list[:len(list)/2] {
			a.deleteBackupFile(d.BackupFile)
		}
		raw, _ = json.Marshal(half)
		a.changes.cached = half
	}
	// نوشتن اتمیک با fsync + یک نسل .bak — مثل settings.json و conversations
	// (رفع F2): tmp در همان پوشه → sync → rename؛ پس از کرش داده سالم می‌ماند.
	if werr := atomicWriteFileWithBackup(p, raw, 0o600); werr != nil {
		// رکورد در حافظه حفظ می‌شود؛ کاربر پیام فارسی می‌بیند (بیرون از قفل).
		logAppErrorFor(a, "save", "changes.json: "+werr.Error())
		emitRecoveryNotice(a, "", "ذخیره‌ی فهرست تغییرات روی دیسک ناموفق بود (جزئیات در لاگ خطاها).")
	}
}

// loadChangesLocked مثل loadChanges ولی بدون قفل داخلی.
// بازیابی: فایل اصلی → `.bak` → کنارگذاشتن فایل خراب با اعلان فارسی.
func (a *App) loadChangesLocked() []FileChange {
	p, err := a.changesPath()
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(p)
	fromBackup := false
	if err != nil {
		if bak, bakErr := os.ReadFile(backupPath(p)); bakErr == nil {
			raw, fromBackup = bak, true
		} else {
			return nil
		}
	}
	var list []FileChange
	if json.Unmarshal(raw, &list) != nil {
		// نسخه‌ی پشتیبان سالم؟
		if bak, bakErr := os.ReadFile(backupPath(p)); bakErr == nil {
			var bl []FileChange
			if json.Unmarshal(bak, &bl) == nil {
				moveToCorrupt(p)
				// نسخه‌ی سالم از پشتیبان بازسازی می‌شود تا دفعه‌ی بعد
				// دوباره بازیابی لازم نباشد.
				if raw2, merr := json.Marshal(bl); merr == nil {
					_ = atomicWriteFile(p, raw2, 0o600)
				}
				emitRecoveryNotice(a, "", "فایل تغییرات آسیب دیده بود؛ از نسخه‌ی پشتیبان بازیابی شد.")
				return bl
			}
		}
		moveToCorrupt(p)
		emitRecoveryNotice(a, "", "فایل تغییرات آسیب دیده بود؛ فهرست تغییرات بازنشانی شد.")
		return nil
	}
	if fromBackup {
		if raw2, merr := json.Marshal(list); merr == nil {
			_ = atomicWriteFile(p, raw2, 0o600)
		}
	}
	return list
}

// ---------------------------------------------------------------- تأیید تعاملی

// changeNeedsConfirm آیا این تغییر به تأیید تعاملی نیاز دارد؟ (۴.۱):
// review_each همه‌چیز؛ auto فقط مسیر محافظت‌شده و حذف سنگین.
func (a *App) changeNeedsConfirm(ch *FileChange) bool {
	a.mu.Lock()
	mode := a.editModeLocked()
	a.mu.Unlock()
	return mode == "review_each" || ch.Protected || ch.HeavyDelete
}

// requestFileConfirm رویداد agent:confirm را می‌فرستد و منتظر پاسخ کاربر
// می‌ماند. اگر تأیید در تنظیمات خاموش باشد یا UI در دسترس نباشد، true
// (اجازه) برمی‌گرداند — مگر اینکه timeout یا لغو کاربر رخ دهد.
func (a *App) requestFileConfirm(ctx context.Context, ch *FileChange) bool {
	// حالت ویرایش (۴.۱): «review_each» همه‌چیز را می‌پرسد؛ «auto» فقط
	// مسیرهای محافظت‌شده و حذف‌های سنگین را.
	if !a.changeNeedsConfirm(ch) {
		return true
	}
	// اجازه موقت (این نوبت پاسخ‌دهی / کل نشست) فقط پرسش‌های عادی را رد
	// می‌کند — مسیر محافظت‌شده و حذف سنگین همیشه می‌پرسند.
	if !ch.Protected && !ch.HeavyDelete && (a.genAllowWrites.Load() || a.sessionAllowWrites.Load()) {
		return true
	}
	// بدون UI (تست/هدلس) — منتظر ماندن بی‌معناست؛ رد کن
	if a.ctx == nil {
		return false
	}

	id := ch.ID
	cch := make(chan bool, 1)
	a.changes.mu.Lock()
	a.changes.confirms[id] = &pendingConfirm{ch: cch}
	a.changes.mu.Unlock()
	defer func() {
		a.changes.mu.Lock()
		delete(a.changes.confirms, id)
		a.changes.mu.Unlock()
	}()

	runtimeEmit(a, "agent:confirm", map[string]interface{}{
		"change": ch,
	})

	timer := time.NewTimer(confirmTimeout)
	defer timer.Stop()
	select {
	case ok := <-cch:
		return ok
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

// changeApprovalNote جمله‌ی صادقِ وضعیت تأیید در پیام نتیجه (۴.۱): مودال واقعاً
// پرسیده شده → «approved by the user»؛ auto بدون پرسش → «auto-applied».
func changeApprovalNote(asked bool) string {
	if asked {
		return "approved by the user"
	}
	return "auto-applied (edit mode 'auto'; review the end-of-turn summary)"
}

// RespondFileConfirm پاسخ کاربر به مودال تأیید تغییر را تحویل می‌دهد.
// این متد از فرانت‌اند (Wails bind) صدا زده می‌شود.
func (a *App) RespondFileConfirm(changeID string, allow bool) error {
	defer a.recoverBound("RespondFileConfirm")
	return a.RespondFileConfirmScope(changeID, allow, "")
}

// RespondFileConfirmScope مثل RespondFileConfirm است با دامنهٔ اجازه:
//
//	"" / "once"  — فقط همین تغییر
//	"generation" — تا پایان پاسخ‌دهی جاری دیگر مودال نیاید
//	"session"    — تا بستن برنامه (یا خاموش کردن دستی) مودال نیاید
func (a *App) RespondFileConfirmScope(changeID string, allow bool, scope string) error {
	defer a.recoverBound("RespondFileConfirmScope")
	scope = strings.ToLower(strings.TrimSpace(scope))
	if allow {
		switch scope {
		case "generation", "turn", "gen":
			a.genAllowWrites.Store(true)
		case "session", "always":
			a.sessionAllowWrites.Store(true)
		}
	}
	a.changes.mu.Lock()
	pc := a.changes.confirms[changeID]
	a.changes.mu.Unlock()
	if pc == nil {
		return errors.New("درخواست تأیید پیدا نشد (منقضی یا پاسخ داده شده)")
	}
	select {
	case pc.ch <- allow:
		return nil
	default:
		return errors.New("درخواست تأیید قبلاً پاسخ داده شده است")
	}
}

// clearGenAllowWrites پایان نوبت پاسخ — اجازهٔ «تا پایان این پاسخ» را برمی‌دارد.
func (a *App) clearGenAllowWrites() {
	a.genAllowWrites.Store(false)
}

// ---------------------------------------------------------------- اعمال با ثبت تغییر

// backupOriginal نسخه‌ی فعلی فایل را در پوشه‌ی بکاپ‌ها کپی می‌کند.
// نام فایل بکاپ برمی‌گردد. اگر فایل وجود نداشت (create) رشته‌ی خالی برمی‌گردد.
// برای پوشه‌ها بکاپ محتوایی وجود ندارد (delete_path فقط پوشه‌ی خالی و
// move_path پوشه را یکپارچه جابه‌جا می‌کند) → خالی و بدون خطا.
// پس از نوشتن، اگر مجموع حجم بکاپ‌ها از سقف گذشت، قدیمی‌ترین‌ها پاک می‌شوند.
func (a *App) backupOriginal(absPath string) (string, error) {
	if info, err := os.Stat(absPath); err == nil {
		if info.IsDir() {
			// پوشه: بازگرداندن با rename-back در revertChange انجام
			// می‌شود؛ اینجا محتوایی برای بکاپ نیست.
			return "", nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // فایل تازه است
		}
		return "", err
	}
	bdir, err := a.backupsDir()
	if err != nil {
		return "", err
	}
	name := newID() + ".orig"
	if err := os.WriteFile(filepath.Join(bdir, name), data, 0o600); err != nil {
		return "", err
	}
	a.pruneBackupsIfNeeded(bdir)
	return name, nil
}

// pruneBackupsIfNeeded اگر مجموع حجم فایل‌های.orig از سقف گذشت،
// قدیمی‌ترین فایل‌ها را حذف می‌کند تا AppData بی‌رویه رشد نکند.
func (a *App) pruneBackupsIfNeeded(bdir string) {
	entries, err := os.ReadDir(bdir)
	if err != nil || len(entries) == 0 {
		return
	}
	type fileSize struct {
		name string
		size int64
		mod  int64
	}
	var files []fileSize
	var total int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".orig") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		files = append(files, fileSize{name: e.Name(), size: info.Size(), mod: info.ModTime().Unix()})
		total += info.Size()
	}
	if total <= maxBackupTotalBytes {
		return
	}
	// قدیمی‌ترین اول
	sort.Slice(files, func(i, j int) bool { return files[i].mod < files[j].mod })
	for _, f := range files {
		if total <= maxBackupTotalBytes {
			break
		}
		_ = os.Remove(filepath.Join(bdir, f.name))
		total -= f.size
	}
}

// currentConvID گفتگوی فعالِ پاسخ‌دهی (برای نسبت‌دادن تغییرات به جلسه).
func (a *App) currentConvID() string {
	return a.activeConvID()
}

// writeFileWithConfirm کل چرخه‌ی امن نوشتن فایل:
// محافظ خواندن‌پیش‌از‌نوشتن → محاسبه‌ی diff → درخواست تأیید → بکاپ →
// نوشتن → ثبت رکورد (تغییر + اثر انگشت) → رویداد UI.
// اگر کاربر رد کند، رکوردِ «ردشده» ثبت می‌شود و خطای مناسب به مدل برمی‌گردد.
func (a *App) writeFileWithConfirm(ctx context.Context, rel, afterContent string, intent writeIntent) (string, bool) {
	p, err := a.resolveWorkspacePath(rel)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	if err := validatePathForWrite(p); err != nil {
		return "ERROR: " + err.Error(), false
	}

	// محتوای فعلی (قبل)
	before, rerr := os.ReadFile(p)
	existed := rerr == nil
	if rerr != nil && !os.IsNotExist(rerr) {
		return "ERROR: " + rerr.Error(), false
	}
	beforeStr := string(before)
	// محافظ A.1 — پیش از هر پرسش/نوشتنی: فایل موجود باید خوانده شده و
	// بدون تغییر مانده باشد (هش محتوای فعلی همین‌جا محاسبه شده).
	guard := a.checkWriteGuard(ctx, rel, p, before, existed, intent)
	if guard.Err != "" {
		return guard.Err, false
	}

	// diff
	kind := ChangeModify
	var fromName, toName = "a/" + rel, "b/" + rel
	if !existed {
		kind = ChangeCreate
		fromName, toName = "/dev/null", "b/"+rel
	}
	diffText, added, removed := unifiedDiff(fromName, toName, beforeStr, afterContent)
	if existed && added == 0 && removed == 0 && beforeStr == afterContent {
		return "OK: no changes needed — '" + rel + "' already has the requested content.", true
	}

	convID := a.currentConvID()
	preview := FileChange{
		ID:      newID(),
		ConvID:  convID,
		Path:    rel,
		Kind:    kind,
		Added:   added,
		Removed: removed,
		Diff:    diffText,
		At:      timeNow(),
		TurnID:  turnIDFrom(ctx),
	}
	if preview.Protected = a.pathIsProtected(rel); preview.Protected {
		preview.ConfirmNote = "این مسیر محافظت‌شده است (مخزن/راز/فایل تعریف ساخت) و حتی در حالت ویرایش خودکار تأیید می‌خواهد."
	}

	if !a.requestFileConfirm(ctx, &preview) {
		a.recordRejectedChange(preview)
		return "ERROR: the user REJECTED this change to '" + rel + "'. Ask them why or propose a different approach. Do NOT retry the same write immediately.", false
	}

	// نقطه‌ی بازگشت پروژه (B): پیش از اولین تغییر واقعیِ نوبت — پس از تأیید
	// کاربر و بیرون از هر قفلی.
	a.ensureTurnCheckpoint(ctx)

	// بکاپ نسخه‌ی قبلی
	backupName, berr := a.backupOriginal(p)
	if berr != nil {
		return "ERROR: could not back up the original file: " + berr.Error(), false
	}

	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "ERROR: cannot create parent folders: " + err.Error(), false
	}
	if err := os.WriteFile(p, []byte(afterContent), 0o644); err != nil {
		return "ERROR: " + err.Error(), false
	}

	rec := preview
	rec.BackupFile = backupName
	rec.ContentSHA256 = sha256Hex([]byte(afterContent))
	a.recordAppliedChange(rec)
	turnRecorderFrom(ctx).recordFileChange(rel)
	// فایلِ نوشته‌شده از این پس «خوانده‌شده» است (نوشتن دوباره/bi‌نیاز از
	// خواندن دوباره، چون ایجنت دقیقاً همین محتوا را گذاشته).
	if post, serr := os.Stat(p); serr == nil {
		a.noteFileRead(ctx, a.convIDForTools(ctx), p, fileReadRecord{
			Size:   post.Size(),
			ModNS:  post.ModTime().UnixNano(),
			SHA256: rec.ContentSHA256,
			Full:   true, // کل محتوا را همین ابزار نوشته است
		})
	}
	// گیت نحوی بعد از نوشتن (writeguard.go): پیش از این یک ویرایش خراب تا
	// رسیدن به build/test در پایان نوبت کشف نمی‌شد. تغییر اعمال‌شده می‌ماند
	// (با بکاپ و امکان برگشت)، ولی نتیجه ok=false و متن دقیق به مدل می‌رود.
	//
	// فقط «رگرسیون» خطاست: فایلی که پیش از این ویرایش هم پارس نمی‌شد
	// (قطعه‌کد، قالب، فایل نیمه‌کاره) مانع ادامه نمی‌شود و فقط در پیام موفقیت
	// یادآوری می‌شود — وگرنه ایجنت روی فایل‌های غیربرنامه‌ای گیر می‌کرد.
	parseNote := ""
	if vmsg := validateWrittenFile(p); vmsg != "" {
		// فایل تازه باید از ابتدا سالم باشد؛ فقط برای فایل موجود، «از قبل
		// هم خراب بود» عذر پذیرفته می‌شود (قطعه‌کد/قالب).
		beforeInvalid := existed && validateContent(p, before) != ""
		if !beforeInvalid {
			return "ERROR: the change WAS applied (the file on disk was updated, a backup exists and the «file changes» panel can revert it) but it no longer parses: " + rel + ": " + vmsg + ". Do not rewrite the whole file blindly — fix it with edit_file before continuing.", false
		}
		parseNote = " (note: this file did not parse before this edit either — " + vmsg + ")"
	}

	return fmt.Sprintf("OK: %s '%s' (+%d/-%d lines) — %s; revertible from the «file changes» panel.%s%s",
		map[string]string{ChangeCreate: "created", ChangeModify: "updated"}[kind], rel, added, removed, changeApprovalNote(a.changeNeedsConfirm(&rec)), guard.Note, parseNote), true
}

// movePathWithConfirm جابه‌جایی/تغییرنام با تأیید و بکاپ.
func (a *App) movePathWithConfirm(ctx context.Context, from, to string) (string, bool) {
	src, err := a.resolveWorkspacePath(from)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	dst, err := a.resolveWorkspacePath(to)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	if err := validatePathForWrite(dst); err != nil {
		return "ERROR: " + err.Error(), false
	}
	if _, err := os.Stat(dst); err == nil {
		return "ERROR: destination '" + to + "' already exists.", false
	}
	if _, err := os.Stat(src); err != nil {
		return "ERROR: source not found: " + err.Error(), false
	}

	convID := a.currentConvID()
	preview := FileChange{
		ID:      newID(),
		ConvID:  convID,
		Path:    from,
		Kind:    ChangeMove,
		MovedTo: to,
		At:      timeNow(),
		TurnID:  turnIDFrom(ctx),
	}
	// منبع یا مقصدِ محافظت‌شده → تأیید حتی در auto
	if a.pathIsProtected(from) || a.pathIsProtected(to) {
		preview.Protected = true
		preview.ConfirmNote = "مبدأ یا مقصد جابه‌جایی مسیر محافظت‌شده است و تأیید می‌خواهد."
	}
	preview.Diff = "--- " + from + "\n+++ " + to + "\n(renamed/moved)\n"

	if !a.requestFileConfirm(ctx, &preview) {
		a.recordRejectedChange(preview)
		return "ERROR: the user REJECTED moving '" + from + "' to '" + to + "'.", false
	}
	a.ensureTurnCheckpoint(ctx)
	backupName, berr := a.backupOriginal(src)
	if berr != nil {
		return "ERROR: could not back up: " + berr.Error(), false
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "ERROR: cannot create destination folders: " + err.Error(), false
	}
	if err := os.Rename(src, dst); err != nil {
		return "ERROR: " + err.Error(), false
	}
	rec := preview
	rec.BackupFile = backupName
	// محتوا پس از rename در مقصد است — هش تعارض از dst خوانده می‌شود.
	if data, rerr := os.ReadFile(dst); rerr == nil {
		rec.ContentSHA256 = sha256Hex(data)
		// رکورد خواندن: مبدأ دیگر وجود ندارد، مقصد «دیده‌شده» است.
		convIDRead := a.convIDForTools(ctx)
		a.readStore.forget(convIDRead, src)
		modNS := int64(0)
		if st, serr := os.Stat(dst); serr == nil {
			modNS = st.ModTime().UnixNano()
		}
		a.noteFileRead(ctx, convIDRead, dst, fileReadRecord{Size: int64(len(data)), ModNS: modNS, SHA256: rec.ContentSHA256, Full: true})
	}
	a.recordAppliedChange(rec)
	turnRecorderFrom(ctx).recordFileChange(from)
	turnRecorderFrom(ctx).recordFileChange(to)
	return fmt.Sprintf("OK: moved '%s' to '%s' — %s; revertible from the «file changes» panel.", from, to, changeApprovalNote(a.changeNeedsConfirm(&rec))), true
}

// deletePathWithConfirm حذف با تأیید و بکاپ (حتی برای پوشه‌ی خالی).
func (a *App) deletePathWithConfirm(ctx context.Context, rel string) (string, bool) {
	p, err := a.resolveWorkspacePath(rel)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	info, err := os.Stat(p)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	isDir := info.IsDir()

	convID := a.currentConvID()
	preview := FileChange{
		ID:     newID(),
		ConvID: convID,
		Path:   rel,
		Kind:   ChangeDelete,
		At:     timeNow(),
		TurnID: turnIDFrom(ctx),
	}
	if a.pathIsProtected(rel) {
		preview.Protected = true
		preview.ConfirmNote = "این مسیر محافظت‌شده است و حذفش تأیید می‌خواهد."
	}
	if !isDir {
		preview.Diff = "--- " + rel + "\n+++ /dev/null\n(file deleted)\n"
	}
	// بکاپ «پیش از» تأیید گرفته می‌شود تا شکستِ بکاپ خودش به یک پرسش
	// تبدیل شود (۴.۱: حذفِ بدون‌بکاپ همیشه می‌پرسد، نه اینکه بی‌صدا رد شود).
	// حذفِ پوشه و فایل بزرگ‌تر از ۵ مگابایت هم طبق ۴.۱ همیشه می‌پرسد.
	// (حذف بیش از ۲۰ فایل ساختاراً ناممکن است: حذف بازگشتی مسدود است.)
	backupName, berr := a.backupOriginal(p) // برای پوشه‌ی خالی خالی برمی‌گردد
	if berr != nil {
		preview.HeavyDelete = true
		preview.ConfirmNote = "از این مسیر نسخه‌ی پشتیبان گرفته نشد؛ حذفش بازگشت‌ناپذیر خواهد بود."
		backupName = ""
	} else if isDir {
		preview.HeavyDelete = true
		preview.ConfirmNote = "حذف پوشه همیشه تأیید می‌خواهد."
	} else if info.Size() > 5*1024*1024 {
		preview.HeavyDelete = true
		preview.ConfirmNote = "حجم این فایل بیش از ۵ مگابایت است؛ حذفش تأیید می‌خواهد."
	}

	if !a.requestFileConfirm(ctx, &preview) {
		a.recordRejectedChange(preview)
		return "ERROR: the user REJECTED deleting '" + rel + "'.", false
	}
	// نقطه‌ی بازگشت پروژه (B) — پس از تأیید کاربر، پیش از حذف.
	a.ensureTurnCheckpoint(ctx)
	if err := os.Remove(p); err != nil {
		if isDir {
			return "ERROR: " + err.Error() + " (non-empty directory? recursive deletion is blocked; remove files one by one).", false
		}
		return "ERROR: " + err.Error(), false
	}
	rec := preview
	rec.BackupFile = backupName
	a.recordAppliedChange(rec)
	turnRecorderFrom(ctx).recordFileChange(rel)
	// فایلِ حذف‌شده دیگر رکورد خواندن ندارد (ساخت دوباره‌اش آزاد است).
	a.readStore.forget(a.convIDForTools(ctx), p)
	return fmt.Sprintf("OK: deleted '%s' — %s; revertible from the «file changes» panel.", rel, changeApprovalNote(a.changeNeedsConfirm(&rec))), true
}

// recordAppliedChange رکورد نهایی تغییر + رویداد UI.
func (a *App) recordAppliedChange(ch FileChange) {
	a.changes.mu.Lock()
	a.appendChangeLocked(ch)
	a.changes.mu.Unlock()
	runtimeEmit(a, "agent:change_applied", map[string]interface{}{"change": ch})
}

// recordRejectedChange تغییرِ ردشده را هم ثبت می‌کنیم (برای شفافیت در پنل تغییرات).
func (a *App) recordRejectedChange(ch FileChange) {
	ch.Rejected = true
	ch.Diff = "" // diff ردشده نگه داشته نمی‌شود تا حافظه سبک بماند
	a.changes.mu.Lock()
	a.appendChangeLocked(ch)
	a.changes.mu.Unlock()
	runtimeEmit(a, "agent:change_rejected", map[string]interface{}{"changeId": ch.ID, "path": ch.Path})
}

// ---------------------------------------------------------------- Revert

// GetFileChanges فهرست تغییرات ثبت‌شده (جدیدترین اول) برای UI.
func (a *App) GetFileChanges() []FileChange {
	defer a.recoverBound("GetFileChanges")
	list := a.loadChanges()
	out := make([]FileChange, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		c := list[i]
		c.Diff = "" // لیست سبک؛ diff با GetFileChangeDiff جداگانه خوانده می‌شود
		out = append(out, c)
	}
	return out
}

// GetFileChangeDiff متن diff یک تغییر مشخص.
func (a *App) GetFileChangeDiff(changeID string) (string, error) {
	defer a.recoverBound("GetFileChangeDiff")
	for _, c := range a.loadChanges() {
		if c.ID == changeID {
			if c.Diff == "" {
				return "(بدون پیش‌نمایش — احتمالاً فایل حذف‌شده یا تغییر خیلی بزرگ)", nil
			}
			return c.Diff, nil
		}
	}
	return "", errors.New("تغییر پیدا نشد")
}

// RevertFileChange یک تغییر را برمی‌گرداند و رویداد را بیرون از قفل
// تغییرات منتشر می‌کند (هم‌خانواده‌ی recordAppliedChange که emit را بعد
// از باز شدن قفل انجام می‌دهد — EventsEmit زیر قفل خلاف قاعده‌ی
// معماری است).
func (a *App) RevertFileChange(changeID string) error {
	defer a.recoverBound("RevertFileChange")
	if err := a.revertFileChangeInner(changeID); err != nil {
		return err
	}
	runtimeEmit(a, "agent:change_reverted", map[string]interface{}{"changeId": changeID})
	return nil
}

// revertFileChangeInner بدنه‌ی قفل‌دارِ RevertFileChange (بدون emit).
//
// create → حذف فایل؛ modify → بازگرداندن بکاپ؛ delete → بازگرداندن بکاپ؛
// move → بازگرداندن فایل به مسیر قبلی. revert روی رکورد علامت می‌خورد.
func (a *App) revertFileChangeInner(changeID string) error {
	a.changes.mu.Lock()
	defer a.changes.mu.Unlock()

	list := a.loadChangesLocked()
	idx := -1
	for i := range list {
		if list[i].ID == changeID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errors.New("تغییر پیدا نشد")
	}
	ch := &list[idx]
	if ch.Rejected {
		return errors.New("این تغییر هرگز اعمال نشده که برگردد")
	}
	if ch.Reverted {
		return errors.New("این تغییر قبلاً برگردانده شده است")
	}
	if err := a.revertChange(ch); err != nil {
		logAppErrorFor(a, "revert", ch.Kind+" "+ch.Path+": "+err.Error())
		return err
	}
	ch.Reverted = true
	ch.RevertedAt = timeNow()
	// برگرداندن انجام شد — بکاپ دیگر لازم نیست و حذف می‌شود (جلوگیری از انباشت دیسک)
	a.deleteBackupFile(ch.BackupFile)
	ch.BackupFile = ""
	a.changes.cached = list
	a.changes.cacheOK = true
	if p, err := a.changesPath(); err == nil {
		if raw, merr := json.Marshal(list); merr == nil {
			tmp := p + ".tmp"
			if os.WriteFile(tmp, raw, 0o600) == nil {
				_ = os.Rename(tmp, p)
			}
		}
	}
	return nil
}

// revertChange عملیات برگرداندن را انجام می‌دهد (بدون قفل changes).
func (a *App) revertChange(ch *FileChange) error {
	switch ch.Kind {
	case ChangeCreate:
		p, err := a.resolveWorkspacePath(ch.Path)
		if err != nil {
			return err
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	case ChangeModify, ChangeDelete:
		p, err := a.resolveWorkspacePath(ch.Path)
		if err != nil {
			return err
		}
		if ch.BackupFile == "" {
			// حذف پوشه‌ی خالی → بازسازی همان پوشه
			if ch.Kind == ChangeDelete {
				return os.MkdirAll(p, 0o755)
			}
			return errors.New("بکاپی برای این تغییر وجود ندارد")
		}
		bdir, err := a.backupsDir()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(bdir, ch.BackupFile))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, data, 0o644)
	case ChangeMove:
		// فایل/پوشه از Path به MovedTo رفته؛ برمی‌گرداند
		if ch.MovedTo == "" {
			return errors.New("مقصد جابه‌جایی ثبت نشده است")
		}
		dst, derr := a.resolveWorkspacePath(ch.MovedTo)
		if derr != nil {
			return derr
		}
		// پوشه → بازگرداندن یکپارچه با rename (بکاپ محتوایی ندارد)
		if info, serr := os.Stat(dst); serr == nil && info.IsDir() {
			p, err := a.resolveWorkspacePath(ch.Path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.Rename(dst, p); err != nil {
				return err
			}
			return nil
		}
		if ch.BackupFile == "" {
			return errors.New("بکاپی برای این تغییر وجود ندارد")
		}
		bdir, err := a.backupsDir()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(bdir, ch.BackupFile))
		if err != nil {
			return err
		}
		p, err := a.resolveWorkspacePath(ch.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
		// حذف مقصد فعلی (اگر هست)
		_ = os.Remove(dst)
		return nil
	}
	return errors.New("نوع تغییر ناشناخته است")
}

// RevertAllFileChanges همه‌ی تغییرات اعمال‌شده‌ی قابل‌بازگشت را (از آخر به اول)
// برمی‌گرداند. تعداد موفق را برمی‌گرداند.
func (a *App) RevertAllFileChanges() (int, error) {
	defer a.recoverBound("RevertAllFileChanges")
	list := a.loadChanges()
	done := 0
	var firstErr error
	// از آخر به اول — ترتیب زمانی معکوس
	for i := len(list) - 1; i >= 0; i-- {
		ch := list[i]
		if ch.Rejected || ch.Reverted {
			continue
		}
		if err := a.RevertFileChange(ch.ID); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		done++
	}
	return done, firstErr
}

// pendingChangeCount تعداد تغییرات اعمال‌شده‌ی بازگشت‌پذیر (برای badge در UI).
func (a *App) pendingChangeCount() int {
	n := 0
	for _, c := range a.loadChanges() {
		if !c.Rejected && !c.Reverted {
			n++
		}
	}
	return n
}

// purgeChangesForConversation رکوردهای changes مربوط به یک گفتگو و بکاپ‌هایشان
// را از دیسک پاک می‌کند. فایل‌های واقعی پروژه دست نخورده می‌مانند.
// بیرون از a.mu صدا زده می‌شود (فقط changes.mu می‌گیرد).
func (a *App) purgeChangesForConversation(convID string) {
	if a.changes == nil || strings.TrimSpace(convID) == "" {
		return
	}
	a.changes.mu.Lock()
	defer a.changes.mu.Unlock()
	list := a.loadChangesLocked()
	if len(list) == 0 {
		return
	}
	kept := make([]FileChange, 0, len(list))
	for i := range list {
		if list[i].ConvID == convID {
			a.deleteBackupFile(list[i].BackupFile)
			continue
		}
		kept = append(kept, list[i])
	}
	if len(kept) == len(list) {
		return
	}
	a.changes.cached = kept
	a.changes.cacheOK = true
	if p, err := a.changesPath(); err == nil {
		if raw, merr := json.Marshal(kept); merr == nil {
			tmp := p + ".tmp"
			if os.WriteFile(tmp, raw, 0o600) == nil {
				_ = os.Rename(tmp, p)
			}
		}
	}
}

// purgeAllFileChanges همه‌ی رکوردها و بکاپ‌ها را پاک می‌کند (بعد از حذف همه گفتگوها).
func (a *App) purgeAllFileChanges() {
	if a.changes == nil {
		return
	}
	a.changes.mu.Lock()
	defer a.changes.mu.Unlock()
	list := a.loadChangesLocked()
	for i := range list {
		a.deleteBackupFile(list[i].BackupFile)
	}
	a.changes.cached = nil
	a.changes.cacheOK = true
	if p, err := a.changesPath(); err == nil {
		_ = os.WriteFile(p, []byte("[]"), 0o600)
	}
	// پوشه بکاپ خالی‌تر می‌شود؛ باقیمانده‌های یتیم best-effort
	if bdir, err := a.backupsDir(); err == nil {
		entries, _ := os.ReadDir(bdir)
		for _, e := range entries {
			if !e.IsDir() {
				_ = os.Remove(filepath.Join(bdir, e.Name()))
			}
		}
	}
}
