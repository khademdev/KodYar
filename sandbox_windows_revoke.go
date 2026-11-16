//go:build windows

// پس‌گرفتن ACEهای سندباکس هنگام خاموش‌کردن آن.
//
// هر grant موفق یک نشانگر (marker) در sandboxCacheDir می‌نویسد: SID سندباکس و
// مسیری که ACE گرفته است. با خاموش‌کردن سندباکس همان نشانگرها منبع حقیقت
// هستند: ACE اختصاصی همین SID از هر مسیر حذف می‌شود (فقط همان SID؛ ACEهای
// دیگر پوشه دست‌نخورده می‌مانند) و نشانگر هم پاک می‌شود تا اجرای بعدی دوباره
// مجوز بدهد. این کار جلوی باقی‌ماندن دسترسی اضافه‌ی AppContainer را می‌گیرد.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// sandboxRemoveACE همه‌ی ACEهای همین SID را از یک شیء حذف می‌کند.
// /T عمداً نیست: ACEهای ما روی خودِ شیء نوشته می‌شوند و فرزندانشان ارثی
// می‌گیرند؛ حذف ACE والد کافی است و پیمایش درخت را تحمیل نمی‌کند.
func sandboxRemoveACE(sid, path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "icacls", path, "/remove", "*"+sid)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flagNoWindow}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("icacls %s /remove: %w (%s)", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// sandboxRevokeGrants ACEهای ثبت‌شده برای SID سندباکس را پس می‌گیرد.
// بهترین تلاش است: یک مسیر حذف‌شده یا ACEِ از قبل رفته مانع بقیه نمی‌شود،
// اما اولین خطای واقعی برگردانده می‌شود تا در لاگ خطاها دیده شود. کش‌های
// درون‌حافظه‌ای هم پاک می‌شوند تا روشن‌کردن دوباره‌ی سندباکس مجوزها را از نو
// بگیرد (وگرنه «قبلاً داده شده» پنداشته و ACE غایب می‌ماند).
func sandboxRevokeGrants() error {
	sid, _, err := sandboxIdentity()
	if err != nil {
		return err
	}
	var firstErr error
	entries, rerr := os.ReadDir(sandboxCacheDir())
	if rerr == nil {
		seen := map[string]bool{}
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), "grant-") {
				continue
			}
			marker := filepath.Join(sandboxCacheDir(), e.Name())
			b, berr := os.ReadFile(marker)
			if berr != nil {
				continue
			}
			lines := strings.Split(strings.TrimSpace(string(b)), "\n")
			if len(lines) < 2 || !strings.EqualFold(strings.TrimSpace(lines[0]), sid) {
				continue // نشانگرِ مالِ SID دیگری است (پروفایل قدیمی) — دست نزن.
			}
			path := filepath.Clean(strings.TrimSpace(lines[1]))
			key := strings.ToLower(path)
			if seen[key] {
				_ = os.Remove(marker)
				continue
			}
			seen[key] = true
			if st, serr := os.Stat(path); serr == nil && st.IsDir() {
				if derr := sandboxRemoveACE(sid, path); derr != nil {
					if firstErr == nil {
						firstErr = derr
					}
					continue // نشانگر بماند تا تلاش بعدی (روشن/خاموش دوباره) تکرار شود.
				}
			}
			_ = os.Remove(marker)
		}
	}
	sandboxGrantMu.Lock()
	for k := range sandboxGranted {
		delete(sandboxGranted, k)
	}
	for k := range sandboxGrantFailed {
		delete(sandboxGrantFailed, k)
	}
	sandboxGrantMu.Unlock()
	sandboxToolMu.Lock()
	sandboxToolGranted = false
	sandboxToolMu.Unlock()
	return firstErr
}
