// تست‌های گیت نحوی بعد از نوشتن (writeguard.go).
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestValidateGoAfterWrite(t *testing.T) {
	p := writeTemp(t, "bad.go", "package x\n\nfunc {\n")
	msg := validateWrittenFile(p)
	if !strings.Contains(msg, "line ") {
		t.Fatalf("broken Go must report a line number, got %q", msg)
	}
}

func TestNoWarningForValidGo(t *testing.T) {
	p := writeTemp(t, "ok.go", "package x\n\nfunc A() {}\n")
	if msg := validateWrittenFile(p); msg != "" {
		t.Fatalf("valid Go must pass, got %q", msg)
	}
}

func TestInvalidJSONDetected(t *testing.T) {
	p := writeTemp(t, "bad.json", `{"a":}`)
	if msg := validateWrittenFile(p); !strings.Contains(msg, "invalid JSON") {
		t.Fatalf("broken JSON must be reported, got %q", msg)
	}
	p = writeTemp(t, "ok.json", `{"a":1}`)
	if msg := validateWrittenFile(p); msg != "" {
		t.Fatalf("valid JSON must pass, got %q", msg)
	}
}

func TestValidateSkipsUnknownExt(t *testing.T) {
	p := writeTemp(t, "notes.txt", "func { not code")
	if msg := validateWrittenFile(p); msg != "" {
		t.Fatalf("unknown extensions must not be validated, got %q", msg)
	}
}

// TestWriteFileRejectsBrokenGoEndToEnd — نوشتن Go شکسته از مسیر واقعی ابزار:
// تغییر اعمال می‌شود ولی نتیجه ok=false با متن «no longer parses» است؛ سپس
// نوشتن نسخهٔ سالم موفق می‌شود.
func TestWriteFileRejectsBrokenGoEndToEnd(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath

	res, ok := a.execTool("write_file", `{"path":"broken.go","content":"package p\n\nfunc {\n"}`)
	if ok {
		t.Fatalf("broken Go must fail the tool result: %s", res)
	}
	if !strings.Contains(res, "no longer parses") || !strings.Contains(res, "broken.go") {
		t.Fatalf("error text must explain the syntax gate: %s", res)
	}

	res, ok = a.execTool("edit_file", `{"path":"broken.go","old_string":"func {","new_string":"func A() {}"}`)
	if !ok {
		t.Fatalf("fixing the file must succeed: %s", res)
	}
	if data, err := os.ReadFile(filepath.Join(ws, "broken.go")); err != nil || !strings.Contains(string(data), "func A()") {
		t.Fatalf("fixed content must be on disk: %v %q", err, string(data))
	}
}
