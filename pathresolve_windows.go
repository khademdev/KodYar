//go:build windows

// حل مسیر نهایی روی ویندوز — برای گاردِ محدوده‌ی workspace.
//
// چرا لازم است: filepath.EvalSymlinks در این نسخه از Go، junctionها
// (reparse point از نوع MOUNT_POINT) را حل نمی‌کند و os.Lstat هم آن‌ها را
// symlink گزارش نمی‌دهد. نتیجه: یک junction داخل workspace که به بیرون اشاره
// کند، هم از evalExisting رد می‌شد و هم گاردِ رشته‌ای را پاس می‌کرد — یعنی
// خواندن/نوشتن از راه آن از پوشه‌ی کاری بیرون می‌رفت. GetFinalPathNameByHandle
// مسیر واقعیِ پس از عبور از همه‌ی reparse pointها را می‌دهد.
package main

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	pathKernel32                 = syscall.NewLazyDLL("kernel32.dll")
	pathGetFinalPathNameByHandle = pathKernel32.NewProc("GetFinalPathNameByHandleW")
)

const (
	fileFlagBackupSemantics = 0x02000000
)

// finalPathOf موجودِ p را با هندل باز می‌کند و مسیر نهایی‌اش را برمی‌گرداند.
func finalPathOf(p string) (string, bool) {
	p16, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return "", false
	}
	h, err := syscall.CreateFile(
		p16,
		0, // فقط متادیتا
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		fileFlagBackupSemantics, // اجازه‌ی باز کردن پوشه
		0,
	)
	if err != nil {
		return "", false
	}
	defer syscall.CloseHandle(h)

	buf := make([]uint16, syscall.MAX_PATH)
	for {
		n, _, _ := pathGetFinalPathNameByHandle.Call(
			uintptr(h),
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(len(buf)),
			0, // VOLUME_NAME_DOS → «\\?\C:\...»
		)
		if n == 0 {
			return "", false
		}
		if int(n) < len(buf) {
			return trimLongPathPrefix(syscall.UTF16ToString(buf[:n])), true
		}
		if int(n) > 32*1024 { // سقف محافظتی
			return "", false
		}
		buf = make([]uint16, int(n)+1)
	}
}

// trimLongPathPrefix پیشوند طولانی‌مسیر را حذف می‌کند تا مقایسه با مسیرهای
// معمولی درست باشد.
func trimLongPathPrefix(p string) string {
	if strings.HasPrefix(p, `\\?\UNC\`) {
		return `\\` + p[len(`\\?\UNC\`):]
	}
	if strings.HasPrefix(p, `\\?\`) {
		return p[len(`\\?\`):]
	}
	return p
}

// finalPathForCompare مسیر نهاییِ نزدیک‌ترین والدِ موجود را برمی‌گرداند و
// اجزای باقی‌مانده (فایل هنوز ساخته‌نشده) را دوباره می‌چسباند.
// ok=false یعنی هیچ والدی قابل باز کردن نبود.
func finalPathForCompare(p string) (string, bool) {
	var rest []string
	cur := filepath.Clean(p)
	for {
		if fin, ok := finalPathOf(cur); ok {
			for i := len(rest) - 1; i >= 0; i-- {
				fin = filepath.Join(fin, rest[i])
			}
			return fin, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}
