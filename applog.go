// پل لاگِ رانتایم Wails به فایل «لاگ خطاها».
//
// بدون این فایل، پیام‌های خود Wails/WebView2 (مثلاً «WebView2 not installed»,
// خطای بارگذاری دارایی‌ها یا خطای بایندینگ) فقط روی stderr می‌رفت؛ در نسخهٔ
// GUI بدون کنسول، هیچ‌کس آن‌ها را نمی‌دید.
//
// سیاست: فقط warning/error/fatal در errors.log ثبت می‌شوند تا لاگ شلوغ نشود؛
// trace/debug/info دور ریخته می‌شوند (معادل رفتار پیش‌فرض ساکت).
package main

import (
	"fmt"
	"os"
	"strings"
)

// fileLogger پیاده‌سازی logger.Logger برای Wails v2.
type fileLogger struct {
	app *App
}

func (l fileLogger) write(level, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	switch level {
	case "warning", "error", "fatal":
		logAppErrorFor(l.app, "wails/"+level, message)
	default:
		return // trace/debug/info — نویز است، ثبت نمی‌شود
	}
	if level == "error" || level == "fatal" {
		// برای اجرای ترمینالی/دیباگ هم قابل مشاهده بماند.
		fmt.Fprintf(os.Stderr, "MyAI %s: %s\n", level, message)
	}
}

func (l fileLogger) Print(message string)   { l.write("info", message) }
func (l fileLogger) Trace(message string)   { l.write("trace", message) }
func (l fileLogger) Debug(message string)   { l.write("debug", message) }
func (l fileLogger) Info(message string)    { l.write("info", message) }
func (l fileLogger) Warning(message string) { l.write("warning", message) }
func (l fileLogger) Error(message string)   { l.write("error", message) }
func (l fileLogger) Fatal(message string)   { l.write("fatal", message) }
