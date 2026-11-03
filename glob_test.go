// تست‌های ابزار glob — الگوها، gitignore، سقف نتیجه و ثبت‌نام ابزار.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeWsFile فایلی را داخل پوشه‌ی کاری تست می‌نویسد (پوشه‌های والد می‌سازد).
func writeWsFile(t *testing.T, ws, rel, content string) {
	t.Helper()
	p := filepath.Join(ws, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// hasLine آیا رشته خطی دقیقاً برابر line دارد؟ (جلوگیری از تطبیق زیررشته‌ای)
func hasLine(s, line string) bool {
	for _, l := range strings.Split(s, "\n") {
		if l == line {
			return true
		}
	}
	return false
}

func TestGlobPatterns(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	for _, f := range []string{
		"a.go", "x.txt", "src/b.go", "src/deep/c.go", "docs/readme.md", "nested/x.txt",
		"سند/فارسی.md", // نام پوشه/فایل فارسی — باید مثل بقیه کار کند
	} {
		writeWsFile(t, ws, f, "x")
	}

	// **/*.go — همه‌ی عمق‌ها
	r, ok := a.execTool("glob", `{"pattern":"**/*.go"}`)
	if !ok {
		t.Fatalf("glob **/*.go failed: %s", r)
	}
	for _, want := range []string{"a.go", "src/b.go", "src/deep/c.go"} {
		if !hasLine(r, want) {
			t.Fatalf("**/*.go must list %s: %s", want, r)
		}
	}
	if hasLine(r, "x.txt") || hasLine(r, "docs/readme.md") {
		t.Fatalf("**/*.go must not list non-go files: %s", r)
	}

	// *.txt بدون اسلش — تطبیق با «نام فایل» در هر عمقی
	r, ok = a.execTool("glob", `{"pattern":"*.txt"}`)
	if !ok {
		t.Fatalf("glob *.txt failed: %s", r)
	}
	if !hasLine(r, "x.txt") || !hasLine(r, "nested/x.txt") {
		t.Fatalf("*.txt must match the file name at any depth: %s", r)
	}
	if hasLine(r, "a.go") {
		t.Fatalf("*.txt must not list .go files: %s", r)
	}

	// الگوی لنگرشده به زیرپوشه
	r, ok = a.execTool("glob", `{"pattern":"src/**/*.go"}`)
	if !ok {
		t.Fatalf("glob src/**/*.go failed: %s", r)
	}
	if !hasLine(r, "src/b.go") || !hasLine(r, "src/deep/c.go") {
		t.Fatalf("src/**/*.go must list both: %s", r)
	}
	if hasLine(r, "a.go") {
		t.Fatalf("src/**/*.go must not list root a.go: %s", r)
	}

	// docs/** — همه‌چیز داخل docs
	r, ok = a.execTool("glob", `{"pattern":"docs/**"}`)
	if !ok {
		t.Fatalf("glob docs/** failed: %s", r)
	}
	if !hasLine(r, "docs/readme.md") || hasLine(r, "a.go") {
		t.Fatalf("docs/** must list only docs contents: %s", r)
	}

	// نام فارسی — الگوی پسوندی باید فایل فارسی را هم بیاورد
	r, ok = a.execTool("glob", `{"pattern":"**/*.md"}`)
	if !ok || !hasLine(r, "سند/فارسی.md") {
		t.Fatalf("Persian file name must be matched and listed verbatim: %v (%v)", r, ok)
	}

	// پارامتر path — جستجو در زیرپوشه؛ خروجی همچنان نسبی به workspace
	r, ok = a.execTool("glob", `{"pattern":"**/*.go","path":"src"}`)
	if !ok {
		t.Fatalf("glob with path=src failed: %s", r)
	}
	if !hasLine(r, "src/b.go") || !hasLine(r, "src/deep/c.go") {
		t.Fatalf("path=src must list src files with workspace-relative output: %s", r)
	}
	if hasLine(r, "a.go") {
		t.Fatalf("path=src must not list root files: %s", r)
	}

	// بدون نتیجه — ok=true با پیام صریح (مثل search_files)
	r, ok = a.execTool("glob", `{"pattern":"*.zzz"}`)
	if !ok || !strings.Contains(r, "No files found matching") {
		t.Fatalf("no-match glob must return ok with a clear message: %v (%v)", r, ok)
	}
}

func TestGlobHonorsGitignoreAndSkips(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	writeGitignore(t, ws, "*.log\nbuild/\n/rootonly.txt\n")
	for _, f := range []string{
		"keep.txt", "skip.log", "build/gen.go", "node_modules/pkg.js", "rootonly.txt",
	} {
		writeWsFile(t, ws, f, "x")
	}
	r, ok := a.execTool("glob", `{"pattern":"**/*"}`)
	if !ok {
		t.Fatalf("glob failed: %s", r)
	}
	if !hasLine(r, "keep.txt") {
		t.Fatalf("non-ignored file missing: %s", r)
	}
	for _, banned := range []string{"skip.log", "build/gen.go", "node_modules/pkg.js", "rootonly.txt"} {
		if hasLine(r, banned) {
			t.Fatalf("ignored/skipped path %s appeared: %s", banned, r)
		}
	}
}

func TestGlobResultCap(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	for i := 0; i < maxGlobResults+5; i++ {
		writeWsFile(t, ws, fmt.Sprintf("f%03d.txt", i), "x")
	}
	r, ok := a.execTool("glob", `{"pattern":"*.txt"}`)
	if !ok {
		t.Fatalf("glob failed: %s", r)
	}
	if !strings.Contains(r, fmt.Sprintf("Found %d file(s)", maxGlobResults)) {
		t.Fatalf("result must be capped at %d: %s", maxGlobResults, r[:200])
	}
	if !strings.Contains(r, "result limit reached") {
		t.Fatalf("cap notice missing: %s", r[:200])
	}
}

// TestGlobSensitiveNamesVisible اسم فایل‌های حساس در glob «دیده» می‌شوند
// (مثل list_dir — فقط نام است) اما محتوایشان هرگز خوانده نمی‌شود.
func TestGlobSensitiveNamesVisible(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	writeWsFile(t, ws, ".env", "SECRET=1")
	r, ok := a.execTool("glob", `{"pattern":"*.env"}`)
	if !ok || !hasLine(r, ".env") {
		t.Fatalf("sensitive file NAME must be visible (contents never read): %v (%v)", r, ok)
	}
}

func TestGlobValidation(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	writeWsFile(t, ws, "a.go", "x")

	// فیلد الزامی
	if r, ok := a.execTool("glob", `{"path":"."}`); ok || !strings.Contains(r, `missing required field "pattern"`) {
		t.Fatalf("missing pattern must fail validation: %v (%v)", r, ok)
	}
	// الگوی خالی/فاصله‌ای
	if r, ok := a.execTool("glob", `{"pattern":"   "}`); ok || !strings.Contains(r, "must not be empty") {
		t.Fatalf("blank pattern must fail: %v (%v)", r, ok)
	}
	// مسیر خارج از پوشه‌ی کاری
	if r, ok := a.execTool("glob", `{"pattern":"*.go","path":"../outside"}`); ok || !strings.Contains(r, "outside the workspace") {
		t.Fatalf("path traversal must fail: %v (%v)", r, ok)
	}
	// مسیر فایل به‌جای پوشه
	if r, ok := a.execTool("glob", `{"pattern":"*.go","path":"a.go"}`); ok || !strings.Contains(r, "must be a directory") {
		t.Fatalf("file path must fail: %v (%v)", r, ok)
	}
	// عدد برای pattern از طریق coerceString به «123» تبدیل می‌شود (رفتار
	// همیشگی gs/A1) — خطای validation نیست؛ الگوی بی‌نتیجه ولی بی‌ضرر است.
	if r, ok := a.execTool("glob", `{"pattern":123}`); !ok || !strings.Contains(r, "No files found matching '123'") {
		t.Fatalf("numeric pattern must coerce to a string pattern: %v (%v)", r, ok)
	}
}

// TestGlobRegistrations ثبت‌نام کامل ابزار glob در همه‌ی فهرست‌ها.
func TestGlobRegistrations(t *testing.T) {
	// عضو مجموعه‌ی موازیِ فقط-خواندنی
	if !isParallelReadOnlyTool("glob") {
		t.Fatal("glob must be a parallel read-only tool")
	}
	// مجوز پیش‌فرض: فقط-خواندنی → allow (حتی با ConfirmFileWrites روشن)
	if d := toolDefaultDecision("glob", true); d != permAllow {
		t.Fatalf("glob default decision = %v, want allow", d)
	}
	// فهرست نمایش در تنظیمات دسترسی‌ها
	found := false
	for _, n := range agentToolNames {
		if n == "glob" {
			found = true
		}
	}
	if !found {
		t.Fatal("glob missing from agentToolNames")
	}
	// شمای ابزار: required هرگز null نیست (تست عمومی موجود هم پوشش می‌دهد)
	for _, d := range agentToolDefs() {
		if d.Function.Name == "glob" {
			return
		}
	}
	t.Fatal("glob missing from agentToolDefs")
}

// ---------------------------------------------------------------- لغو پیمایش (Bug 6)

// cancelAfterErrCtx کانتکستی آزمایشی که Err آن بعد از n فراخوانی غیر-nil
// می‌شود — لغوِ «میان پیمایش» را قطعی و بدون زمان‌سنجی قابل اثبات می‌کند.
type cancelAfterErrCtx struct {
	context.Context
	remaining int
	cancelled *bool
}

func (c *cancelAfterErrCtx) Err() error {
	if c.remaining <= 0 {
		*c.cancelled = true
		return context.Canceled
	}
	c.remaining--
	return nil
}

func TestGlobPreCancelledContext(t *testing.T) {
	a := newTestApp(t)
	writeWsFile(t, a.settings.WorkspacePath, "a.go", "x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, ok := a.toolGlob(ctx, "*.go", "")
	if ok || r != "ERROR: cancelled by the user." {
		t.Fatalf("pre-cancelled ctx must abort immediately: %v (%v)", r, ok)
	}
}

func TestGlobCancelDuringWalk(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	// درخت کوچک اما چندپوشه‌ای؛ کانتکست پس از چند بررسی لغو می‌شود
	for i := 0; i < 20; i++ {
		writeWsFile(t, ws, fmt.Sprintf("d%02d/f.txt", i), "x")
	}
	cancelled := false
	ctx := &cancelAfterErrCtx{Context: context.Background(), remaining: 3, cancelled: &cancelled}
	r, ok := a.toolGlob(ctx, "*.txt", "")
	if ok {
		t.Fatalf("cancelled walk must not return partial results as success: %v", r)
	}
	if r != "ERROR: cancelled by the user." {
		t.Fatalf("unexpected cancel message: %q", r)
	}
	if !cancelled {
		t.Fatal("the walk must actually observe the cancellation")
	}
}

func TestGlobNormalWalkUnaffected(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	writeWsFile(t, ws, "ok.txt", "x")
	r, ok := a.toolGlob(context.Background(), "*.txt", "")
	if !ok || !hasLine(r, "ok.txt") {
		t.Fatalf("normal glob must still work: %v (%v)", r, ok)
	}
}
