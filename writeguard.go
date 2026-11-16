// گیت نحویِ بعد از نوشتن فایل (writeguard.go).
//
// چرا: پیش از این، یک edit_file/write_file که فایل را از نظر نحوی خراب می‌کرد
// تا رسیدن به build/test در «پایان» نوبت کشف نمی‌شد و دورهای زیادی صرف
// اشتباهِ سادهٔ سینتکس می‌شد. اینجا بعد از هر نوشتنِ موفق، بدون اجرای هیچ
// فرآیندی، فقط با پارسرِ stdlib بررسی می‌شود:
//
// - .go  → go/parser (خطای اول با شماره خط)
// - .json → encoding/json (اعتبار ساختاری)
// - بقیه  → هیچ حدسی زده نمی‌شود (خالی)
//
// سقف ۲ مگابایت: فایل‌های بزرگ‌تر بررسی نمی‌شوند تا نوشتن کند نشود.
package main

import (
	"encoding/json"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// maxValidateBytes سقف حجم فایل برای بررسی نحوی بعد از نوشتن.
const maxValidateBytes = 2 * 1024 * 1024

// validateWrittenFile فایل تازه‌نوشته‌شده را بررسی می‌کند. خروجی "" یعنی
// سالم؛ در غیر این صورت متن کوتاه «line N: msg» برای پیام ابزار.
func validateWrittenFile(absPath string) string {
	st, err := os.Stat(absPath)
	if err != nil || st.Size() > maxValidateBytes {
		return ""
	}
	src, rerr := os.ReadFile(absPath)
	if rerr != nil {
		return ""
	}
	return validateContent(absPath, src)
}

// validateContent همان بررسی است روی محتوای داده‌شده — تا فراخواننده بتواند
// «وضعیت قبل از ویرایش» را هم با همان قاعده بسنجد و فقط «رگرسیون» را خطا\n// بشمارد (فایل‌هایی که از قبل هم پارس نمی‌شدند، مثل قطعه‌کدها/قالب‌ها،
// نباید ایجنت را از کار بیندازند).
func validateContent(absPath string, src []byte) string {
	ext := strings.ToLower(filepath.Ext(absPath))
	switch ext {
	case ".go":
		fset := token.NewFileSet()
		if _, perr := parser.ParseFile(fset, absPath, src, parser.AllErrors); perr != nil {
			return goParseErrorText(perr)
		}
	case ".json":
		if !json.Valid(src) {
			return "invalid JSON (the file no longer parses)"
		}
	}
	return ""
}

// goParseErrorText اولین خطای پارس را به «line N: msg» تبدیل می‌کند.
func goParseErrorText(err error) string {
	switch e := err.(type) {
	case scanner.ErrorList:
		if len(e) > 0 {
			return "line " + itoaSmall(e[0].Pos.Line) + ": " + e[0].Msg
		}
	case *scanner.Error:
		return "line " + itoaSmall(e.Pos.Line) + ": " + e.Msg
	case interface{ Error() string }:
		// متن خام پارسر ممکن است خودش «file:line:col: msg» داشته باشد؛
		// فقط بخش بعد از آخرین دونقطه‌ی موقعیت را نگه می‌داریم.
		s := e.Error()
		if i := strings.Index(s, ": "); i > 0 {
			s = s[i+2:]
		}
		return s
	}
	return "it no longer parses"
}

// itoaSmall تبدیل ارزان int به رشته (بدون strconv برای مسیر داغ نیست).
func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
