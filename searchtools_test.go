// تست‌های بهبودهای ابزار جستجو/خروجی: برش head+tail خروجی دستور،
// فیلتر include و حساسیت به حروف و context در search_files، و شمای
// چندویرایشی edit_file.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// ---------------------------------------------------------------- برش خروجی

func TestTruncateToolOutputKeepsTail(t *testing.T) {
	// خروجی طولانی شبیه بیلد: هزاران خط هشدار و خطای واقعی در انتها
	var b strings.Builder
	for i := 0; i < 3000; i++ {
		b.WriteString("warning: unused variable in module pkg/foo (line ")
		b.WriteString(strings.Repeat("x", 20))
		b.WriteString(")\n")
	}
	b.WriteString("BUILD FAILED: undefined: fooBar in main.go:42\n")
	long := b.String()

	out := truncateToolOutputKeepTail(long)
	if len(out) > maxToolOutput {
		t.Fatalf("output must stay within %d bytes, got %d", maxToolOutput, len(out))
	}
	if !strings.Contains(out, "BUILD FAILED: undefined: fooBar") {
		t.Fatal("the tail (the real error) must be kept, not truncated away")
	}
	if !strings.Contains(out, "truncated") {
		t.Fatal("a truncation marker must be present")
	}
	if !utf8.ValidString(out) {
		t.Fatal("truncated output must stay valid UTF-8")
	}
	// متن کوتاه باید دست‌نخورده برگردد
	short := "ok\nBUILD FAILED: x\n"
	if got := truncateToolOutputKeepTail(short); got != short {
		t.Fatalf("short output must be unchanged, got %q", got)
	}

	// برش روی متن فارسی چندبایتی هم نباید کاراکتر را نصف کند
	persian := strings.Repeat("بررسی کارهای ایجنت و رفع اشکال\n", 3000)
	out = truncateToolOutputKeepTail(persian)
	if len(out) > maxToolOutput || !utf8.ValidString(out) {
		t.Fatalf("persian truncation invalid: len=%d valid=%v", len(out), utf8.ValidString(out))
	}
}

// ---------------------------------------------------------------- include

func TestSearchFilesIncludeFilter(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	write := func(rel, content string) {
		full := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "needle in go\n")
	write("b.txt", "needle in text\n")
	write("sub/c.go", "needle in sub go\n")

	// الگوی بدون / → نام فایل در هر عمقی
	r, ok := a.execTool("search_files", `{"query":"needle","include":"*.go"}`)
	if !ok {
		t.Fatal(r)
	}
	if !strings.Contains(r, "a.go:1:") || !strings.Contains(r, "sub/c.go:1:") {
		t.Fatalf("include '*.go' must match go files at any depth: %v", r)
	}
	if strings.Contains(r, "b.txt") {
		t.Fatalf("include '*.go' must exclude b.txt: %v", r)
	}

	// الگوی دارای / → لنگرشده از ریشه‌ی workspace
	r, ok = a.execTool("search_files", `{"query":"needle","include":"sub/**/*.go"}`)
	if !ok {
		t.Fatal(r)
	}
	if !strings.Contains(r, "sub/c.go:1:") || strings.Contains(r, "a.go:1:") {
		t.Fatalf("include 'sub/**/*.go' must be anchored: %v", r)
	}

	// چند الگو با کاما
	r, ok = a.execTool("search_files", `{"query":"needle","include":"*.txt,*.go"}`)
	if !ok {
		t.Fatal(r)
	}
	if !strings.Contains(r, "Found 3 match(es)") {
		t.Fatalf("comma-separated include must union patterns: %v", r)
	}
}

// ---------------------------------------------------------------- case

func TestSearchFilesCaseSensitivity(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	if err := os.WriteFile(filepath.Join(ws, "f.txt"), []byte("Needle here\nneedle there\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// پیش‌فرض: غیرحساس به حروف (هم متن، هم regex) — رفتار یکدست
	r, ok := a.execTool("search_files", `{"query":"needle"}`)
	if !ok || !strings.Contains(r, "Found 2 match(es)") {
		t.Fatalf("literal search must be case-insensitive by default: %v", r)
	}
	r, ok = a.execTool("search_files", `{"query":"NEEDLE","regex":true}`)
	if !ok || !strings.Contains(r, "Found 2 match(es)") {
		t.Fatalf("regex search must be case-insensitive by default: %v", r)
	}

	// case_sensitive=true → فقط تطبیق دقیق
	r, ok = a.execTool("search_files", `{"query":"Needle","case_sensitive":true}`)
	if !ok || !strings.Contains(r, "Found 1 match(es)") || !strings.Contains(r, "f.txt:1:") {
		t.Fatalf("case_sensitive must match exact case only: %v", r)
	}
	r, ok = a.execTool("search_files", `{"query":"needle","case_sensitive":true}`)
	if !ok || !strings.Contains(r, "f.txt:2:") || strings.Contains(r, "f.txt:1:") {
		t.Fatalf("case_sensitive must skip the other casing: %v", r)
	}
	// رشته‌ای (تحمل نوع ضعیف مدل‌ها)
	if r, ok = a.execTool("search_files", `{"query":"Needle","case_sensitive":"true"}`); !ok || !strings.Contains(r, "Found 1 match(es)") {
		t.Fatalf("loose boolean must work: %v", r)
	}
}

// ---------------------------------------------------------------- context

func TestSearchFilesContextLines(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	content := "line1\nline2\nmatch here\nline4\nline5\n"
	if err := os.WriteFile(filepath.Join(ws, "c.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r, ok := a.execTool("search_files", `{"query":"match","context":1}`)
	if !ok {
		t.Fatal(r)
	}
	if !strings.Contains(r, "Found 1 match(es)") {
		t.Fatalf("context lines must not count as matches: %v", r)
	}
	if !strings.Contains(r, "c.txt:3: match here") {
		t.Fatalf("match line format must stay 'path:line: text': %v", r)
	}
	if !strings.Contains(r, "c.txt-2- line2") || !strings.Contains(r, "c.txt-4- line4") {
		t.Fatalf("before/after context lines must be printed as 'path-line- text': %v", r)
	}
	if strings.Contains(r, "c.txt-1-") || strings.Contains(r, "c.txt-5-") {
		t.Fatalf("context must stay within the requested distance: %v", r)
	}

	// سقف context بزرگ‌تر از حد مجاز نباید خطا بدهد (clamp)
	r, ok = a.execTool("search_files", `{"query":"match","context":99}`)
	if !ok || !strings.Contains(r, "c.txt:3:") {
		t.Fatalf("context above the cap must clamp, not fail: %v", r)
	}
}

// ---------------------------------------------------------------- شمای ابزارها

func TestEditFileSchemaExposesMultiEdit(t *testing.T) {
	var def *toolDef
	defs := agentToolDefs()
	for i := range defs {
		if defs[i].Function.Name == "edit_file" {
			def = &defs[i]
			break
		}
	}
	if def == nil {
		t.Fatal("edit_file missing from agentToolDefs")
	}
	params, ok := def.Function.Parameters.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected parameters type %T", def.Function.Parameters)
	}
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("properties missing")
	}
	if _, ok := props["edits"]; !ok {
		t.Fatal("edit_file schema must advertise the multi-edit field \"edits\" (the executor supports it)")
	}
	edits, _ := props["edits"].(map[string]interface{})
	items, _ := edits["items"].(map[string]interface{})
	if items == nil {
		t.Fatal("edits must describe its item schema")
	}
	req, _ := items["required"].([]string)
	if len(req) != 2 || req[0] != "old_string" || req[1] != "new_string" {
		t.Fatalf("each edit must require old_string+new_string, got %v", req)
	}
	topReq, _ := params["required"].([]string)
	if len(topReq) != 1 || topReq[0] != "path" {
		t.Fatalf("only \"path\" may be required (either/or of single vs multi form), got %v", topReq)
	}
}

func TestRunCommandBackgroundArgValidated(t *testing.T) {
	// background اکنون در toolArgSpecs است: نوع نامعتبر باید خطای دقیق بدهد
	err := validateToolArgs("run_command", map[string]interface{}{"command": "echo hi", "background": "maybe"})
	if err == nil || !strings.Contains(err.Error(), `field "background" must be a boolean`) {
		t.Fatalf("invalid background must be rejected with a precise error, got %v", err)
	}
	if err := validateToolArgs("run_command", map[string]interface{}{"command": "echo hi", "background": true}); err != nil {
		t.Fatalf("valid background must pass: %v", err)
	}
}

func TestSearchFilesArgsValidated(t *testing.T) {
	// context عدد صحیح است؛ رشته‌ی عددی هم پذیرفته می‌شود (coerce)
	if err := validateToolArgs("search_files", map[string]interface{}{"query": "x", "context": "2"}); err != nil {
		t.Fatalf("numeric string context must pass: %v", err)
	}
	if err := validateToolArgs("search_files", map[string]interface{}{"query": "x", "context": 1.5}); err == nil {
		t.Fatal("fractional context must be rejected")
	}
	if err := validateToolArgs("search_files", map[string]interface{}{"query": "x", "case_sensitive": "nope"}); err == nil {
		t.Fatal("invalid case_sensitive must be rejected")
	}
}
