// بازیابی داده‌های آسیب‌دیده — گفتگو و changes.json هرگز بی‌صدا از دست نمی‌روند.
//
// قواعد:
//   - پیش از جایگزینی فایل سالم، یک نسل `.bak` (rename-based) نگه داشته می‌شود؛
//     ترتیب چرخش طوری است که هرگز حالتی که «نه فایل اصلی و نه .bak» باشد
//     پیش نیاید که لودر پیدا نکند (فایل اصلی به .bak تغییر نام می‌دهد، بعد
//     فایل تازه rename می‌شود).
//   - در خرابی JSON: اول `.bak`، بعد بازیابی پیام‌های کامل با json.Decoder.
//   - فایل خراب هرگز حذف نمی‌شود؛ به `<name>.corrupt-<timestamp>` تغییر نام
//     می‌دهد (حداکثر ۵ نسخه به ازای هر فایل نگه داشته می‌شود).
//   - یک اعلان فارسی کوتاه به UI می‌رود: «گفتگو آسیب دیده بود؛ n پیام بازیابی شد.»
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// maxCorruptFiles سقف نسخه‌های `.corrupt-*` هر فایل.
const maxCorruptFiles = 5

// backupPath مسیر نسخه‌ی پشتیبان یک‌نسلی.
func backupPath(path string) string { return path + ".bak" }

// rotateToBackup فایل موجود را (اگر باشد) به `.bak` منتقل می‌کند.
// قدیمی‌ترین `.bak` اول حذف می‌شود (حفظ فقط یک نسل).
func rotateToBackup(path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil // چیزی برای پشتیبان‌گیری نیست
	}
	bak := backupPath(path)
	_ = os.Remove(bak) // ویندوز rename روی فایل موجود را رد می‌کند
	return renameWithRetry(path, bak)
}

// atomicWriteFileWithBackup مثل atomicWriteFile است، ولی پیش از جایگزینیِ
// فایل موجود، یک نسل `.bak` نگه می‌دارد.
func atomicWriteFileWithBackup(path string, data []byte, perm os.FileMode) error {
	tmp, err := writeTempFile(path, data, perm)
	if err != nil {
		return err
	}
	if err := rotateToBackup(path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup %s: %w", filepath.Base(path), err)
	}
	if err := renameWithRetry(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// corruptPath نام یکتای `<name>.corrupt-<timestamp>` می‌سازد.
func corruptPath(path string) string {
	base := path + ".corrupt-" + time.Now().Format("20060102-150405")
	p := base
	for i := 1; ; i++ {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
		p = fmt.Sprintf("%s-%d", base, i)
	}
}

// moveToCorrupt فایل خراب را کنار می‌گذارد و نسخه‌های قدیمی را هرس می‌کند.
// هرگز فایل را حذف نمی‌کند (داده‌ی کاربر).
func moveToCorrupt(path string) {
	if _, err := os.Stat(path); err != nil {
		return
	}
	_ = os.Rename(path, corruptPath(path))
	pruneCorruptFiles(path)
}

// pruneCorruptFiles حداکثر maxCorruptFiles نسخه‌ی خراب نگه می‌دارد.
func pruneCorruptFiles(path string) {
	matches, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(matches) <= maxCorruptFiles {
		return
	}
	sort.Strings(matches)
	for _, old := range matches[:len(matches)-maxCorruptFiles] {
		_ = os.Remove(old)
	}
}

// salvageConversation تا آخرین پیام کاملِ یک JSON نیمه‌خراب را بازیابی
// می‌کند. شمارش پیام‌های سالم برگردانده می‌شود؛ هیچ خطای panic ممکن نیست.
func salvageConversation(raw []byte) (*Conversation, int) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, 0
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, 0
	}
	c := &Conversation{}
	count := 0
	for dec.More() {
		kt, kerr := dec.Token()
		if kerr != nil {
			break
		}
		key, _ := kt.(string)
		if key == "messages" {
			open, terr := dec.Token()
			if terr != nil {
				return c, count
			}
			if d, ok := open.(json.Delim); !ok || d != '[' {
				return c, count
			}
			for dec.More() {
				var m Message
				if derr := dec.Decode(&m); derr != nil {
					return c, count
				}
				c.Messages = append(c.Messages, m)
				count++
			}
			if _, err := dec.Token(); err != nil { // ']'
				return c, count
			}
			continue
		}
		var val json.RawMessage
		if derr := dec.Decode(&val); derr != nil {
			// مقدار نیمه‌کاره — ادامه ممکن نیست؛ تا همین‌جا کافی است.
			break
		}
		switch key {
		case "id":
			_ = json.Unmarshal(val, &c.ID)
		case "title":
			_ = json.Unmarshal(val, &c.Title)
		case "createdAt":
			_ = json.Unmarshal(val, &c.CreatedAt)
		case "updatedAt":
			_ = json.Unmarshal(val, &c.UpdatedAt)
		}
	}
	return c, count
}

// conversationRecoveredNotice متن اعلان فارسی بازیابی گفتگو.
func conversationRecoveredNotice(n int) string {
	return "گفتگو آسیب دیده بود؛ " + faDigits(n) + " پیام بازیابی شد."
}

// emitRecoveryNotice اعلان بازیابی را (بیرون از قفل‌ها) می‌فرستد.
func emitRecoveryNotice(a *App, convID, text string) {
	safeGo(a, "conv.recovery_notice", func() {
		runtimeEmit(a, "chat:notice", map[string]interface{}{
			"conversationId": convID,
			"text":           text,
		})
	})
}
