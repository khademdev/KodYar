// تست‌های «نقشهٔ مخزن» و ابزارهای find_symbol / find_references (repomap.go).
package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func repoMapBlock(t *testing.T, a *App) string {
	t.Helper()
	return a.repoMapPromptLine(a.settings.WorkspacePath)
}

func TestRepoMapFindsGoSymbols(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	writeWsFile(t, ws, "svc/user.go", "package svc\n\n"+
		"type User struct{ Name string }\n\n"+
		"func NewUser() *User { return &User{} }\n\n"+
		"func (u *User) Rename(n string) { u.Name = n }\n")

	m := a.repoMapFor(ws)
	if len(m.Files) != 1 {
		t.Fatalf("expected 1 indexed file, got %d: %+v", len(m.Files), m.Files)
	}
	names := make([]string, 0, len(m.Files[0].Decls))
	for _, d := range m.Files[0].Decls {
		names = append(names, d.Name)
	}
	got := strings.Join(names, ",")
	for _, want := range []string{"User", "NewUser", "User.Rename"} {
		if !strings.Contains(got, want) {
			t.Fatalf("decls must contain %q, got %q", want, got)
		}
	}
	if block := repoMapBlock(t, a); !strings.Contains(block, "svc/user.go") || !strings.Contains(block, "Repository map") {
		t.Fatalf("prompt block must name the file and header: %q", block)
	}
}

func TestRepoMapRespectsGitignore(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	writeWsFile(t, ws, ".gitignore", "vendor/\n*.gen.go\n")
	writeWsFile(t, ws, "keep.go", "package p\n\nfunc Keep() {}\n")
	writeWsFile(t, ws, "a.gen.go", "package p\n\nfunc Gen() {}\n")
	writeWsFile(t, ws, "vendor/dep.go", "package dep\n\nfunc Dep() {}\n")

	m := a.repoMapFor(ws)
	if len(m.Files) != 1 || m.Files[0].Path != "keep.go" {
		t.Fatalf("only keep.go must be indexed, got %+v", m.Files)
	}
}

func TestRepoMapBudgetCap(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	for i := 0; i < 80; i++ {
		src := fmt.Sprintf("package p\n\nfunc Fn%d() {}\n\ntype Type%d struct{}\n\nfunc (t *Type%d) Do() {}\n", i, i, i)
		writeWsFile(t, ws, fmt.Sprintf("pkg%d/file%d.go", i%8, i), src)
	}
	// بودجهٔ کوچک صریح تا برش واقعاً رخ دهد.
	small := 120
	block := buildRepoMap(ws, small).promptBlockWithTokens(small)
	limit := small*repoMapCharsPerToken + 400 // سرصفحه/دنبالهٔ کوچک
	if len(block) > limit {
		t.Fatalf("repo map must respect the token budget: %d chars > %d", len(block), limit)
	}
	// و بودجهٔ پیش‌فرض هم هرگز از سقف خودش بزهنمی‌زند.
	def := a.repoMapFor(ws).promptBlock()
	if len(def) > maxRepoTokens*repoMapCharsPerToken+400 {
		t.Fatalf("default repo map exceeded its budget: %d chars", len(def))
	}
	if !strings.Contains(block, "+") {
		t.Fatalf("a truncated map must report the remaining files: %q", block[len(block)-80:])
	}
}

func TestRepoMapDeterministic(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	writeWsFile(t, ws, "a.go", "package p\n\nfunc A() { B() }\n\nfunc B() {}\n")
	writeWsFile(t, ws, "b.go", "package p\n\nfunc C() { B() }\n")
	first := buildRepoMap(ws, maxRepoTokens).promptBlock()
	second := buildRepoMap(ws, maxRepoTokens).promptBlock()
	if first != second {
		t.Fatalf("repo map must be deterministic:\n%s\n---\n%s", first, second)
	}
}

func TestRepoMapRanksReferencedSymbolsFirst(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	writeWsFile(t, ws, "a.go", "package p\n\nfunc Hot() {}\n")
	writeWsFile(t, ws, "b.go", "package p\n\nfunc Cold() {}\n")
	writeWsFile(t, ws, "use.go", "package p\n\nfunc use() { Hot(); Hot(); Hot() }\n")
	m := a.repoMapFor(ws)
	if len(m.Files) < 2 {
		t.Fatalf("expected at least two files, got %+v", m.Files)
	}
	// use.go سه بار Hot را صدا می‌زند؛ باید بالاتر از Cold بیاید.
	indexOf := func(name string) int {
		for i, f := range m.Files {
			if f.Path == name {
				return i
			}
		}
		return -1
	}
	// فایلی که نماد پرکاربرد را تعریف می‌کند باید از فایلی با نماد بی‌استفاده
	// بالاتر بیاید (Refs فایل = مجموع ارجاع نمادهای خودش).
	if indexOf("a.go") > indexOf("b.go") {
		t.Fatalf("the file defining a hot symbol must rank above a cold one: %+v", m.Files)
	}
}

func TestFindSymbolTool(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	writeWsFile(t, ws, "svc/user.go", "package svc\n\ntype User struct{}\n\nfunc NewUser() *User { return &User{} }\n")

	r, ok := a.execTool("find_symbol", `{"query":"user"}`)
	if !ok {
		t.Fatalf("find_symbol failed: %s", r)
	}
	if !strings.Contains(r, "svc/user.go: type User") || !strings.Contains(r, "svc/user.go: func NewUser") {
		t.Fatalf("find_symbol must report both symbols (case-insensitive): %s", r)
	}

	// فیلتر kind
	r, ok = a.execTool("find_symbol", `{"query":"user","kind":"type"}`)
	if !ok || strings.Contains(r, "NewUser") {
		t.Fatalf("kind=type must only return types: %s", r)
	}

	// نبود نتیجه → پیام راهنما، نه خطا
	r, ok = a.execTool("find_symbol", `{"query":"nope_nothing"}`)
	if !ok {
		t.Fatalf("missing symbol must not be an error: %s", r)
	}
	if !strings.Contains(r, "No symbol matched") {
		t.Fatalf("empty result needs a guiding message: %s", r)
	}

	// آرگومان الزامی
	if r, ok := a.execTool("find_symbol", `{}`); ok || !strings.Contains(r, `missing required field "query"`) {
		t.Fatalf("find_symbol must validate query: %s", r)
	}
}

func TestFindReferencesWordBoundary(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	writeWsFile(t, ws, "a.go", "package p\n\nvar foo = 1\nvar fooBar = 2\n\nfunc use() { foo++ }\n")

	r, ok := a.execTool("find_references", `{"symbol":"foo"}`)
	if !ok {
		t.Fatalf("find_references failed: %s", r)
	}
	if !strings.Contains(r, "a.go:3:") || !strings.Contains(r, "a.go:6:") {
		t.Fatalf("word-boundary hits must include lines 3 and 6: %s", r)
	}
	if strings.Contains(r, "fooBar") {
		t.Fatalf("'foo' must not match 'fooBar': %s", r)
	}

	// include فیلتر
	r, ok = a.execTool("find_references", `{"symbol":"foo","include":"*.md"}`)
	if !ok || !strings.Contains(r, "No references") {
		t.Fatalf("include filter must exclude a.go: %s", r)
	}

	if r, ok := a.execTool("find_references", `{}`); ok || !strings.Contains(r, `missing required field "symbol"`) {
		t.Fatalf("find_references must validate symbol: %s", r)
	}
}

// TestRepoMapOnThisRepo — اجرای واقعی روی خود همین پروژه: باید فایل‌های Go
// را پیدا کند، نماد بشناسد و سریع بماند.
func TestRepoMapOnThisRepo(t *testing.T) {
	start := time.Now()
	m := buildRepoMap(".", maxRepoTokens)
	elapsed := time.Since(start)
	if len(m.Files) < 50 {
		t.Fatalf("expected many indexed files in this repo, got %d", len(m.Files))
	}
	found := false
	for _, f := range m.Files {
		if f.Path == "repomap.go" {
			found = true
			names := make([]string, 0, len(f.Decls))
			for _, d := range f.Decls {
				names = append(names, d.Name)
			}
			if !strings.Contains(strings.Join(names, ","), "repoGoDecls") {
				t.Fatalf("repomap.go symbols missing: %v", names)
			}
		}
	}
	if !found {
		t.Fatal("repomap.go must be indexed")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("repo map build took too long: %s", elapsed)
	}
	// فقط برای دیدن اندازه/کیفیت نقشه در اجرای -v
	block := m.promptBlock()
	head := block
	if len(head) > 400 {
		head = head[:400]
	}
	t.Logf("repo map: %d files, %d chars, %s\n%s", len(m.Files), len(block), elapsed, head)
}

// TestRepoMapInjectedIntoPrompt — قرارداد اتصال: نقشه باید هم در پرامپت
// ایجنت اصلی و هم در پرامپت زیرایجنت تزریق شود (تست متنی، هم‌سبک بقیه).
func TestRepoMapInjectedIntoPrompt(t *testing.T) {
	chatSrc, err := os.ReadFile("chat.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(chatSrc), "a.repoMapPromptLine(wsPath)") {
		t.Fatal("chat.go must inject the repo map into the agent system prompt")
	}
	subSrc, err := os.ReadFile("subagent.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(subSrc), "repoMapSubagentBlock") {
		t.Fatal("subagent.go must inject the repo map into the sub-agent prompt")
	}
}

// تست قراردادی رجیستری مخصوص ابزارهای تازه (بقیه در toolregistry_test.go).
func TestFindToolsRegistryWiring(t *testing.T) {
	for _, name := range []string{"find_symbol", "find_references"} {
		if _, ok := toolArgSpecs[name]; !ok {
			t.Fatalf("%s missing from toolArgSpecs", name)
		}
		if !parallelReadOnlyTools[name] {
			t.Fatalf("%s must be allowed in parallel read-only batches", name)
		}
		if !subagentAllowedTools[name] {
			t.Fatalf("%s must be available to the read-only sub-agent", name)
		}
		if !strings.Contains(agentSystemPromptForOS("/ws", "windows"), name) {
			t.Fatalf("%s missing from the system prompt", name)
		}
	}
}
