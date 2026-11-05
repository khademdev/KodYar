// محافظ «خواندن پیش از نوشتن» و «فایل کهنه» (مأموریت ۳ — مرحله A.1).
//
// هر read_file موفق، اثر انگشت فایل (حجم، mtime، SHA-256 کامل محتوا و اینکه
// خواندن کل فایل را پوشش داد یا نه) را در
// یک حافظه‌ی LRU به‌ازای هر گفتگو ثبت می‌کند. write_file روی فایلِ موجود فقط
// وقتی مجاز است که ایجنت قبلاً همان فایل را در همین گفتگو خوانده (یا خودش
// ساخته) باشد و محتوای روی دیسک از آن زمان تغییر نکرده باشد. edit_file هم
// اگر رکورد داشته باشد کهنه‌بودن را می‌بیند (تطبیق دقیق old_string خودش
// محافظ دوم است) و اگر رکوردی نباشد اجازه می‌دهد ولی در نتیجه یادآوری می‌کند.
//
// چرا: قبل از این، مدل می‌توانست فایلی را که هرگز ندیده با محتوای خیالی
// بازنویسی کند و تغییرات کاربر یا ابزارهای دیگر را بی‌صدا از بین ببرد.
//
// قفل: fileReadStore.mu یک قفل برگ است — در طول نگه‌داشتنش هیچ قفل دیگری
// گرفته نمی‌شود، هیچ I/O ای انجام نمی‌شود و runtimeEmit صدا زده نمی‌شود
// (سند ترتیب قفل‌ها: ARCHITECTURE.md).
package main

import (
	"context"
	"sort"
	"sync"
)

// fileReadRecord اثر انگشت فایل در لحظه‌ی خواندن (یا نوشتن) موفق.
//
// Full یعنی خواندن کل فایل را پوشش داد و خروجی بریده نشد. write_file روی
// فایل موجود فقط با رکورد Full مجاز است (Stage 4.1)؛ edit_file با رکورد
// جزئی هم کار می‌کند چون تطبیق دقیق old_string محافظ آن است.
type fileReadRecord struct {
	Size   int64  `json:"size"`
	ModNS  int64  `json:"modNs"`
	SHA256 string `json:"sha256"`
	Full   bool   `json:"full"`
}

// محدوده‌های حافظه‌ی رکوردها — کران‌دار تا در گفتگوهای طولانی نشت نکند.
const (
	maxReadRecordsPerConv = 500 // LRU هر گفتگو
	maxReadRecordConvs    = 16  // سقف گفتگوهای نگه‌داری‌شده (کل)
)

// convReads رکوردهای یک گفتگو + ترتیب LRU (کم‌مصرف‌ترین اول).
type convReads struct {
	entries map[string]fileReadRecord
	order   []string // قدیمی‌ترین → جدیدترین
}

// fileReadStore حافظه‌ی رکوردهای خواندن؛ مقدار صفر آن قابل استفاده است
// (نقشه به‌صورت تنبل ساخته می‌شود) تا App نیازی به سازنده‌ی جدا نداشته باشد.
type fileReadStore struct {
	mu    sync.Mutex
	convs map[string]*convReads
	order []string // LRU گفتگوها (قدیمی‌ترین اول)
}

// touch رشته را به انتهای ترتیب LRU منتقل می‌کند (بدون تخصیص اضافه برای
// حالت معمول «همین آخر بوده»).
func touch(order []string, key string) []string {
	if n := len(order); n > 0 && order[n-1] == key {
		return order
	}
	for i, v := range order {
		if v == key {
			copy(order[i:], order[i+1:])
			order[len(order)-1] = key
			return order
		}
	}
	return append(order, key)
}

// remove رشته را از ترتیب حذف می‌کند.
func removeKey(order []string, key string) []string {
	for i, v := range order {
		if v == key {
			return append(order[:i:i], order[i+1:]...)
		}
	}
	return order
}

// note یک رکورد را ثبت می‌کند (convID خالی = خارج از گفتگو، در همان سبد "").
func (s *fileReadStore) note(convID, path string, rec fileReadRecord) {
	if s == nil || path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.convs == nil {
		s.convs = map[string]*convReads{}
	}
	cr := s.convs[convID]
	if cr == nil {
		cr = &convReads{entries: map[string]fileReadRecord{}}
		s.convs[convID] = cr
	}
	cr.entries[path] = rec
	cr.order = touch(cr.order, path)
	if len(cr.order) > maxReadRecordsPerConv {
		drop := len(cr.order) - maxReadRecordsPerConv
		for _, p := range cr.order[:drop] {
			delete(cr.entries, p)
		}
		cr.order = append(make([]string, 0, maxReadRecordsPerConv), cr.order[drop:]...)
	}
	s.order = touch(s.order, convID)
	// سقف گفتگوها: قدیمی‌ترین‌ها کامل دور ریخته می‌شوند (نشت حافظه ندارد).
	for len(s.order) > maxReadRecordConvs {
		old := s.order[0]
		s.order = s.order[1:]
		if old != convID {
			delete(s.convs, old)
		}
	}
}

// lookup رکورد را برمی‌گرداند و LRU را به‌روز می‌کند.
func (s *fileReadStore) lookup(convID, path string) (fileReadRecord, bool) {
	if s == nil || path == "" {
		return fileReadRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cr := s.convs[convID]
	if cr == nil {
		return fileReadRecord{}, false
	}
	rec, ok := cr.entries[path]
	if ok {
		cr.order = touch(cr.order, path)
		s.order = touch(s.order, convID)
	}
	return rec, ok
}

// forget یک مسیر را فراموش می‌کند (حذف/جابه‌جایی فایل).
func (s *fileReadStore) forget(convID, path string) {
	if s == nil || path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cr := s.convs[convID]
	if cr == nil {
		return
	}
	delete(cr.entries, path)
	cr.order = removeKey(cr.order, path)
}

// dropConversation همه‌ی رکوردهای یک گفتگو را پاک می‌کند.
func (s *fileReadStore) dropConversation(convID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.convs, convID)
	s.order = removeKey(s.order, convID)
}

// reset پاک‌سازی کامل (حذف همه‌ی گفتگوها).
func (s *fileReadStore) reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.convs = nil
	s.order = nil
}

// size تعداد رکوردهای فعلی (برای تست کران‌داری).
func (s *fileReadStore) size() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, cr := range s.convs {
		n += len(cr.entries)
	}
	return n
}

// convSize تعداد رکوردهای یک گفتگو.
func (s *fileReadStore) convSize(convID string) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cr := s.convs[convID]; cr != nil {
		return len(cr.entries)
	}
	return 0
}

// convIDs فهرست مرتب‌شده‌ی شناسه‌های نگه‌داری‌شده (تست/عیب‌یابی).
func (s *fileReadStore) convIDs() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.convs))
	for id := range s.convs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------- ادغام با App

// convIDForTools شناسه‌ی گفتگوی همین فراخوانی ابزار (از ctx؛ در نبود آن از
// پاسخ‌دهی فعال). بدون قفل‌های دیگر — ctxKeyToolCall یک مقدار ساده است.
func (a *App) convIDForTools(ctx context.Context) string {
	if ctx != nil {
		if info, ok := ctx.Value(ctxKeyToolCall{}).(*toolCallCtxInfo); ok && info != nil {
			if info.convID != "" {
				return info.convID
			}
		}
	}
	return a.currentConvID()
}

// noteFileRead رکورد خواندن/نوشتن موفق را ثبت می‌کند. خواندن‌های زیرایجنت
// عمداً ثبت نمی‌شوند (فقط‌خواندنی و جدا از گفتگوی اصلی).
func (a *App) noteFileRead(ctx context.Context, convID, absPath string, rec fileReadRecord) {
	if ctx != nil && subagentNoAsk(ctx) {
		return
	}
	a.readStore.note(convID, absPath, rec)
}

// intent نوشتن: write_file روی فایل موجود سخت‌گیر است (باید خوانده شده باشد)،
// edit_file فقط کهنه‌بودن را می‌سنجد.
type writeIntent int

const (
	intentOverwrite writeIntent = iota // write_file
	intentEdit                         // edit_file / multi-edit
)

// editUnreadNote یادآوری غیرمسدودکننده‌ی edit_file روی فایلِ خوانده‌نشده —
// توابع edit آن را در پیام نهایی خود نگه می‌دارند (commitEditFile و
// toolEditFileMulti پیام را بازنویسی می‌کنند، پس اینجا ثابت مشترک است).
const editUnreadNote = " Note: this file had not been read in this conversation; the edit was applied because old_string matched the current content exactly."

// writeGuard نتیجه‌ی بررسی «خواندن پیش از نوشتن/کهنه‌بودن».
type writeGuard struct {
	Err  string // غیرخالی → نوشتن رد می‌شود (متن انگلیسی برای مدل)
	Note string // یادآوری غیرمسدودکننده که به نتیجه‌ی موفق اضافه می‌شود
}

// checkWriteGuard فایلِ موجود را با رکورد خواندن مقایسه می‌کند. current
// محتوای فعلیِ روی دیسک است (فراخواننده از قبل خوانده — I/O اضافه نداریم).
func (a *App) checkWriteGuard(ctx context.Context, rel, abs string, current []byte, existed bool, intent writeIntent) writeGuard {
	if !existed {
		return writeGuard{} // ساخت فایل جدید همیشه آزاد است
	}
	convID := a.convIDForTools(ctx)
	rec, ok := a.readStore.lookup(convID, abs)
	if ok {
		if sha256Hex(current) != rec.SHA256 {
			return writeGuard{Err: "ERROR: '" + rel + "' changed on disk since you last read it (the content hash no longer matches). " +
				"Call read_file again and base your change on the current content — do not overwrite changes you have not seen."}
		}
		if intent == intentOverwrite && !rec.Full {
			// خواندن جزئی/بریده: بازنویسی کل فایل می‌تواند بخش دیده‌نشده را
			// نابود کند؛ مدل باید کل فایل را بخواند یا از edit_file استفاده کند.
			return writeGuard{Err: "ERROR: '" + rel + "' was only partially read (or read_file's output was truncated), so overwriting the whole file could destroy the part you have not seen. " +
				"Read the entire file first (read_file without start_line/end_line), or use edit_file for a targeted change."}
		}
		return writeGuard{}
	}
	if intent == intentEdit {
		return writeGuard{Note: editUnreadNote}
	}
	return writeGuard{Err: "ERROR: '" + rel + "' already exists and has not been read in this conversation. " +
		"Call read_file first, then write the full new content; for a targeted change use edit_file with an exact old_string."}
}
