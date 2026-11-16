// تست‌های پل لاگ Wails (applog.go).
package main

import (
	"os"
	"strings"
	"testing"
)

func TestWailsLoggerPersistsOnlyWarningsAndErrors(t *testing.T) {
	os.Setenv("MYAI_CONFIG_DIR", t.TempDir())
	a := NewApp()
	lg := fileLogger{app: a}

	lg.Info("unique-info-marker")
	lg.Debug("unique-debug-marker")
	lg.Warning("unique-warning-marker")
	lg.Error("unique-error-marker")

	log, err := a.GetErrorLog()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"wails/warning", "wails/error", "unique-warning-marker", "unique-error-marker"} {
		if !strings.Contains(log, want) {
			t.Fatalf("error log must contain %q; got:\n%s", want, log)
		}
	}
	for _, unwanted := range []string{"unique-info-marker", "unique-debug-marker"} {
		if strings.Contains(log, unwanted) {
			t.Fatalf("info/debug noise must not reach the error log (%q)", unwanted)
		}
	}
}
