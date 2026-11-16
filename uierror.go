// ثبت خطاهای فرانت‌اند در همان لاگ خطاهای بک‌اند.
//
// پیش از این، errors.log فقط خطاهای Go (شبکه، ذخیره، ابزار، panic) را داشت؛
// خطای جاوااسکریپت در WebView فقط در کنسولِ نامرئی می‌ماند. حالا
// assets/js/errorcapture.js با window.onerror و unhandledrejection هر خطای
// مهارنشده را به ReportFrontendError می‌فرستد و با همان مُهر زمانی/چرخش در
// errors.log ثبت می‌شود (دسته: ui).
package main

import (
	"runtime"
	"strings"
)

const (
	uiErrorKindMax  = 40
	uiErrorMsgMax   = 2000
	uiErrorStackMax = 6000
)

// ReportFrontendError خطای سمت رابط کاربری را در لاگ خطاها ثبت می‌کند.
// kind: error | unhandledrejection | console | هر برچسب کوتاه دیگر.
// هرگز panic نمی‌کند و مقدار برنمی‌گرداند تا فراخوانی از JS ساده بماند.
func (a *App) ReportFrontendError(kind, message, stack string) {
	defer a.recoverBound("ReportFrontendError")

	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "error"
	}
	message = strings.TrimSpace(message)
	stack = strings.TrimSpace(stack)
	if message == "" && stack == "" {
		return // چیزی برای ثبت نیست
	}
	if len(kind) > uiErrorKindMax {
		kind = cutUTF8(kind, uiErrorKindMax)
	}
	if len(message) > uiErrorMsgMax {
		message = cutUTF8(message, uiErrorMsgMax)
	}
	if len(stack) > uiErrorStackMax {
		stack = cutUTF8(stack, uiErrorStackMax)
	}

	var sb strings.Builder
	sb.WriteString("ui/")
	sb.WriteString(kind)
	sb.WriteString(" (v")
	sb.WriteString(version)
	sb.WriteString("/")
	sb.WriteString(runtime.GOOS)
	sb.WriteString("): ")
	if message != "" {
		sb.WriteString(message)
	}
	if stack != "" {
		if message != "" {
			sb.WriteString("\n")
		}
		sb.WriteString(stack)
	}
	logAppErrorFor(a, "ui", sb.String())
}
