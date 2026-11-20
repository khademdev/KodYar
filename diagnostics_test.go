// تست‌های ابزار diagnostics و کلاینت LSP — بدون نیاز به نصب gopls یا
// typescript-language-server (فریمینگ و مسیرها با دادهٔ ساختگی آزموده می‌شوند).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// nopWriteCloser بافر تست را به io.WriteCloser تبدیل می‌کند.
type nopWriteCloser struct{ *bytes.Buffer }

func (nopWriteCloser) Close() error { return nil }

func frameLSP(t *testing.T, buf *bytes.Buffer, v interface{}) {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(buf, "Content-Length: %d\r\n\r\n%s", len(body), body)
}

// TestLSPReadLoopFraming: publishDiagnostics ذخیره می‌شود، پاسخ درخواست
// معلق تحویل داده می‌شود و درخواست سرور→کلاینت با نتیجهٔ null پاسخ می‌گیرد.
func TestLSPReadLoopFraming(t *testing.T) {
	c := newLSPClient("test")
	var written bytes.Buffer
	c.stdin = nopWriteCloser{&written}

	pending := make(chan lspMessage, 1)
	c.pending[5] = pending

	var in bytes.Buffer
	frameLSP(t, &in, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params": map[string]interface{}{
			"uri": "file:///a.go",
			"diagnostics": []map[string]interface{}{{
				"range":    map[string]interface{}{"start": map[string]int{"line": 2, "character": 4}},
				"severity": 1,
				"message":  "undefined: x",
				"source":   "compiler",
			}},
		},
	})
	frameLSP(t, &in, map[string]interface{}{
		"jsonrpc": "2.0", "id": 99, "method": "window/workDoneProgress/create",
		"params": map[string]interface{}{},
	})
	frameLSP(t, &in, map[string]interface{}{
		"jsonrpc": "2.0", "id": 5, "result": map[string]interface{}{"ok": true},
	})

	c.readLoop(&in) // با EOF برمی‌گردد

	select {
	case msg := <-pending:
		if string(msg.Result) != `{"ok":true}` {
			t.Errorf("pending response = %s", msg.Result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending request was never delivered")
	}

	c.mu.Lock()
	diags := c.diags["file:///a.go"]
	c.mu.Unlock()
	if len(diags) != 1 || diags[0].Severity != 1 || diags[0].Range.Start.Line != 2 {
		t.Fatalf("publishDiagnostics not stored: %+v", diags)
	}

	out := written.String()
	if !strings.Contains(out, `"id":99`) || !strings.Contains(out, `"result":null`) {
		t.Errorf("server->client request was not answered with null: %q", out)
	}
}

// TestDiagLangFor: تنها پسوندهای پشتیبانی‌شده زبان دارند.
func TestDiagLangFor(t *testing.T) {
	cases := map[string]string{
		"a.go": "go", "a.TS": "ts", "a.tsx": "ts", "b.js": "ts", "x.mjs": "ts",
		"readme.md": "", "noext": "", "a.css": "",
	}
	for p, want := range cases {
		if got := diagLangFor(p); got != want {
			t.Errorf("diagLangFor(%q) = %q, want %q", p, got, want)
		}
	}
	if got := diagLanguageID("a.tsx"); got != "typescriptreact" {
		t.Errorf("languageId for .tsx = %q", got)
	}
}

// TestCollectDiagTargets: پوشه‌های نویز رد می‌شوند و سقف فایل رعایت می‌شود.
func TestCollectDiagTargets(t *testing.T) {
	dir := t.TempDir()
	must := func(p, body string) {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("a.go", "package a\n")
	must("web/b.ts", "export const b = 1;\n")
	must("node_modules/c.ts", "export const c = 1;\n")
	must("docs/d.md", "# d\n")

	got := collectDiagTargets(dir, true)
	if len(got) != 2 {
		t.Fatalf("targets = %v, want a.go and web/b.ts", got)
	}
	for _, p := range got {
		if strings.Contains(filepath.ToSlash(p), "node_modules") || strings.HasSuffix(p, ".md") {
			t.Errorf("unexpected target %s", p)
		}
	}

	// فایل تکی: فقط خودش
	if one := collectDiagTargets(filepath.Join(dir, "a.go"), false); len(one) != 1 {
		t.Errorf("single file target = %v", one)
	}
	// پسوند بی‌ربط: هیچ
	if none := collectDiagTargets(filepath.Join(dir, "docs/d.md"), false); len(none) != 0 {
		t.Errorf("unsupported file produced targets: %v", none)
	}
}

// TestDiagnosticsToolMissingServer: بدون سرور زبان، پیام راهنمای نصب
// برمی‌گردد و ابزار خطا اعلام می‌کند (ok=false).
func TestDiagnosticsToolMissingServer(t *testing.T) {
	orig := lspLookup
	lspLookup = func(string) (string, error) { return "", os.ErrNotExist }
	defer func() { lspLookup = orig; diagMgr.stopAll() }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &App{}
	a.settings = &Settings{WorkspacePath: dir}

	out, ok := a.toolDiagnostics(context.Background(), "a.go")
	if ok {
		t.Errorf("expected failure when no server is installed, got %q", out)
	}
	if !strings.Contains(out, "gopls") {
		t.Errorf("install hint missing from %q", out)
	}

	tsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tsDir, "b.ts"), []byte("export const b = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &App{}
	b.settings = &Settings{WorkspacePath: tsDir}
	out, ok = b.toolDiagnostics(context.Background(), "")
	if ok || !strings.Contains(out, "typescript-language-server") {
		t.Errorf("expected ts hint for workspace root, got ok=%v %q", ok, out)
	}
}

// TestDiagnosticsLiveGopls آزمون واقعی با gopls است (اگر نصب باشد؛ در CI
// بدون gopls رد می‌شود). یک ماژول موقت با خطای تایپی واقعی می‌سازد.
func TestDiagnosticsLiveGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	// پوشهٔ موقت دستی: gopls (و فرزندهای go) ممکن است لحظه‌ای هندل نگه دارند و
	// t.TempDir پاک‌سازی را به شکست تست تبدیل می‌کند؛ اینجا best-effort است.
	dir, err := os.MkdirTemp("", "kodyar-diag-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		diagMgr.stopAll()
		time.Sleep(200 * time.Millisecond)
		_ = os.RemoveAll(dir)
	})
	files := map[string]string{
		"go.mod":  "module example.com/broken\n\ngo 1.21\n",
		"main.go": "package main\n\nfunc main() {\n\tvar n int = \"not an int\"\n\t_ = n\n}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{settings: &Settings{WorkspacePath: dir}}
	out, ok := a.toolDiagnostics(context.Background(), "main.go")
	if !ok {
		t.Fatalf("live diagnostics failed: %s", out)
	}
	if !strings.Contains(out, "cannot use") && !strings.Contains(out, "main.go:4") {
		t.Errorf("expected a type error for the broken line, got: %s", out)
	}
}

// TestDiagnosticsRegistry: ابزار فقط‌خواندنی است، argspec دارد و در پرامپت
// سیستم آمده (قرارداد رجیستری ابزارها).
func TestDiagnosticsRegistry(t *testing.T) {
	if d := toolDefaultDecision(diagToolName, true); d != permAllow {
		t.Errorf("diagnostics default decision = %v, want allow (read-only)", d)
	}
	if _, ok := toolArgSpecs[diagToolName]; !ok {
		t.Errorf("diagnostics missing from toolArgSpecs")
	}
	if !strings.Contains(agentSystemPromptForOS("/ws", "windows"), diagToolName) {
		t.Errorf("diagnostics not mentioned in the system prompt")
	}
	// آرگومان عددی (float64 مثل JSON) به رشته تبدیل می‌شود؛ نوع نامعتبر رد می‌شود.
	if err := validateToolArgs(diagToolName, map[string]interface{}{"path": float64(42)}); err != nil {
		t.Errorf("numeric path should coerce to string: %v", err)
	}
	if err := validateToolArgs(diagToolName, map[string]interface{}{"path": []string{"a"}}); err == nil {
		t.Errorf("array path must be rejected")
	}
}
