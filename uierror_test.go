// تست‌های ثبت خطاهای فرانت‌اند در لاگ مشترک (uierror.go + errorcapture.js).
package main

import (
	"os"
	"strings"
	"testing"
)

func TestReportFrontendErrorWritesToErrorLog(t *testing.T) {
	os.Setenv("MYAI_CONFIG_DIR", t.TempDir())
	a := NewApp()

	a.ReportFrontendError("unhandledrejection", "cannot read properties of null", "at render (app.js:42:7)\nat tick")

	log, err := a.GetErrorLog()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[ui]", "ui/unhandledrejection", "cannot read properties of null", "at render (app.js:42:7)"} {
		if !strings.Contains(log, want) {
			t.Fatalf("error log must contain %q; got:\n%s", want, log)
		}
	}
}

func TestReportFrontendErrorSkipsEmptyAndTruncates(t *testing.T) {
	os.Setenv("MYAI_CONFIG_DIR", t.TempDir())
	a := NewApp()

	before, _ := a.GetErrorLog()
	a.ReportFrontendError("  ", "   ", "")
	after, _ := a.GetErrorLog()
	if strings.Count(after, "[ui]") != strings.Count(before, "[ui]") {
		t.Fatal("empty report must not write anything to the error log")
	}

	long := strings.Repeat("x", 5000) + "TAIL-MARKER"
	a.ReportFrontendError("error", long, "")
	log, err := a.GetErrorLog()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log, "TAIL-MARKER") {
		t.Fatal("oversized frontend message must be truncated before logging")
	}
	if !strings.Contains(log, "ui/error") {
		t.Fatalf("truncated report still must be logged; got:\n%s", log)
	}
}

// TestFrontendErrorCaptureWired — فایل گیرنده باید در index.html پیش از
// app.js بار شود و به بایندینگ بک‌اند وصل باشد.
func TestFrontendErrorCaptureWired(t *testing.T) {
	js, err := os.ReadFile("frontend/dist/assets/js/errorcapture.js")
	if err != nil {
		t.Fatalf("read errorcapture.js: %v", err)
	}
	src := string(js)
	for _, want := range []string{"unhandledrejection", "addEventListener(\"error\"", "ReportFrontendError", "MyAIErrors"} {
		if !strings.Contains(src, want) {
			t.Fatalf("errorcapture.js must contain %q", want)
		}
	}
	if strings.Contains(src, "innerHTML") {
		t.Fatal("errorcapture.js must not touch innerHTML")
	}

	html, err := os.ReadFile("frontend/dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(html)
	i := strings.Index(page, "assets/js/errorcapture.js")
	j := strings.Index(page, "assets/js/app.js")
	if i < 0 {
		t.Fatal("index.html must load assets/js/errorcapture.js")
	}
	if j < 0 || i > j {
		t.Fatal("errorcapture.js must load before app.js so startup errors are captured")
	}
}
