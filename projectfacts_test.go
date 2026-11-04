// تست‌های تشخیص «واقعیت‌های پروژه» (مأموریت ۳ — A.3).
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFactFile یک فایل در پوشه می‌سازد (helper تست).
func writeFactFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func TestDetectProjectFactsGo(t *testing.T) {
	dir := t.TempDir()
	writeFactFile(t, dir, "go.mod", "module example.com/پروژه\n\ngo 1.22\n")
	f := detectProjectFacts(dir)
	if !hasString(f.Kinds, "Go module example.com/پروژه") {
		t.Fatalf("kinds: %v", f.Kinds)
	}
	if !hasString(f.Commands, "go test ./...") || !hasString(f.Commands, "go build ./...") {
		t.Fatalf("commands: %v", f.Commands)
	}
	block := f.promptBlock()
	if !strings.Contains(block, "Project facts") || !strings.Contains(block, "go test ./...") {
		t.Fatalf("prompt block: %q", block)
	}
}

func TestDetectProjectFactsNode(t *testing.T) {
	dir := t.TempDir()
	writeFactFile(t, dir, "package.json", `{"name":"x","scripts":{"test":"jest","build":"vite build","lint":"eslint ."}}`)
	f := detectProjectFacts(dir)
	if !hasString(f.Kinds, "Node.js (npm)") {
		t.Fatalf("kinds: %v", f.Kinds)
	}
	for _, want := range []string{"npm test", "npm run build", "npm run lint"} {
		if !hasString(f.Commands, want) {
			t.Fatalf("missing %q in %v", want, f.Commands)
		}
	}
	if hasString(f.Commands, "npm run typecheck") {
		t.Fatalf("typecheck script does not exist: %v", f.Commands)
	}
}

func TestDetectProjectFactsMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	writeFactFile(t, dir, "package.json", "{ this is not json ")
	f := detectProjectFacts(dir)
	// نوع پروژه از وجود فایل فهمیده می‌شود؛ دستوری ادعا نمی‌شود
	if !hasString(f.Kinds, "Node.js (npm)") {
		t.Fatalf("kinds: %v", f.Kinds)
	}
	if hasString(f.Commands, "npm test") {
		t.Fatalf("must not invent npm test from a malformed package.json: %v", f.Commands)
	}
}

func TestDetectProjectFactsHugeFileBounded(t *testing.T) {
	dir := t.TempDir()
	// ۵ مگابایت زباله — خواندن باید در ۶۴KB بماند و پنیک نکند
	huge := strings.Repeat("A", 5*1024*1024)
	writeFactFile(t, dir, "pyproject.toml", huge)
	start := time.Now()
	f := detectProjectFacts(dir)
	if time.Since(start) > 3*time.Second {
		t.Fatalf("detection must be bounded, took %v", time.Since(start))
	}
	if !hasString(f.Kinds, "Python") {
		t.Fatalf("kinds: %v", f.Kinds)
	}
}

func TestDetectProjectFactsOtherEcosystems(t *testing.T) {
	cases := []struct {
		files []string
		want  []string
		cmd   []string
	}{
		{[]string{"Cargo.toml"}, []string{"Rust (Cargo)"}, []string{"cargo build", "cargo test"}},
		{[]string{"pytest.ini"}, []string{"Python"}, []string{"python -m pytest"}},
		{[]string{"pom.xml"}, []string{"Java (Maven)"}, []string{"mvn test"}},
		{[]string{"build.gradle.kts"}, []string{"JVM (Gradle)"}, []string{"gradle test"}},
		{[]string{"App.csproj"}, []string{".NET (dotnet)"}, []string{"dotnet build"}},
		{[]string{"solution.sln"}, []string{".NET (dotnet)"}, []string{"dotnet test"}},
		{[]string{"composer.json", ""}, []string{"PHP (Composer)"}, nil},
	}
	for _, c := range cases {
		dir := t.TempDir()
		for _, name := range c.files {
			if name == "" {
				continue
			}
			writeFactFile(t, dir, name, "")
		}
		f := detectProjectFacts(dir)
		for _, want := range c.want {
			if !hasString(f.Kinds, want) {
				t.Fatalf("%v: want kind %q, got %v", c.files, want, f.Kinds)
			}
		}
		for _, cmd := range c.cmd {
			if !hasString(f.Commands, cmd) {
				t.Fatalf("%v: want command %q, got %v", c.files, cmd, f.Commands)
			}
		}
	}
}

func TestDetectProjectFactsComposerScript(t *testing.T) {
	dir := t.TempDir()
	writeFactFile(t, dir, "composer.json", `{"scripts":{"test":"phpunit"}}`)
	f := detectProjectFacts(dir)
	if !hasString(f.Commands, "composer test") {
		t.Fatalf("composer test script must be suggested: %v", f.Commands)
	}
}

func TestDetectProjectFactsMakefile(t *testing.T) {
	dir := t.TempDir()
	writeFactFile(t, dir, "Makefile", "build:\n\tgo build ./...\n\ntest:\n\tgo test ./...\n\n.PHONY: build test\n")
	f := detectProjectFacts(dir)
	if !hasString(f.Kinds, "Makefile") {
		t.Fatalf("kinds: %v", f.Kinds)
	}
	if !hasString(f.Commands, "make test") || !hasString(f.Commands, "make build") {
		t.Fatalf("commands: %v", f.Commands)
	}
	if hasString(f.Commands, "make lint") {
		t.Fatalf("lint target does not exist: %v", f.Commands)
	}
}

func TestDetectProjectFactsEmptyAndPersianPath(t *testing.T) {
	empty := t.TempDir()
	if f := detectProjectFacts(empty); !f.empty() || f.promptBlock() != "" {
		t.Fatalf("empty workspace must produce no facts: %+v", f)
	}
	if f := detectProjectFacts(""); !f.empty() {
		t.Fatal("empty workspace path must be safe")
	}

	// مسیر فارسی با نیم‌فاصله و فاصله
	dir := filepath.Join(t.TempDir(), "پروژه‌ی من ۲")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFactFile(t, dir, "go.mod", "module test\n")
	f := detectProjectFacts(dir)
	if !hasString(f.Commands, "go test ./...") {
		t.Fatalf("Persian path workspace must be detected: %+v", f)
	}
}

// TestProjectFactsCacheInvalidation: کش با mtime (گام ۲ ثانیه — دقت NTFS/FAT).
func TestProjectFactsCacheInvalidation(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	writeFactFile(t, dir, "go.mod", "module one\n")
	if f := a.projectFactsFor(dir); !hasString(f.Kinds, "Go module one") {
		t.Fatalf("initial detection: %v", f.Kinds)
	}

	time.Sleep(2 * time.Second) // دقت mtime فایل‌سیستم‌های قدیمی
	writeFactFile(t, dir, "package.json", `{"scripts":{"test":"jest"}}`)
	f := a.projectFactsFor(dir)
	if !hasString(f.Kinds, "Node.js (npm)") || !hasString(f.Commands, "npm test") {
		t.Fatalf("cache did not invalidate after adding package.json: %+v", f)
	}

	// تغییر محتوای فایل موجود (اسکریپت تازه) هم باید دیده شود
	time.Sleep(2 * time.Second)
	writeFactFile(t, dir, "package.json", `{"scripts":{"test":"jest","build":"vite build"}}`)
	f = a.projectFactsFor(dir)
	if !hasString(f.Commands, "npm run build") {
		t.Fatalf("cache did not invalidate after editing package.json: %+v", f)
	}

	// پوشه‌ی دیگر → کش جدا
	other := t.TempDir()
	writeFactFile(t, other, "Cargo.toml", "")
	if f := a.projectFactsFor(other); !hasString(f.Kinds, "Rust (Cargo)") {
		t.Fatalf("second workspace detection: %+v", f)
	}
	// بازگشت به پوشه‌ی اول → دوباره محاسبه/کش درست
	if f := a.projectFactsFor(dir); !hasString(f.Kinds, "Node.js (npm)") {
		t.Fatalf("switch back detection: %+v", f)
	}
}

// TestProjectFactsPromptBlockBounded: بلوک پرامپت کوتاه و انگلیسی است.
func TestProjectFactsPromptBlockBounded(t *testing.T) {
	dir := t.TempDir()
	writeFactFile(t, dir, "go.mod", "module x\n")
	writeFactFile(t, dir, "package.json", `{"scripts":{"test":"t","build":"b","lint":"l","typecheck":"c"}}`)
	writeFactFile(t, dir, "Cargo.toml", "")
	writeFactFile(t, dir, "pom.xml", "")
	writeFactFile(t, dir, "build.gradle", "")
	writeFactFile(t, dir, "Makefile", "test:\n")
	f := detectProjectFacts(dir)
	block := f.promptBlock()
	// خودِ بلوک تزریق‌شده کران‌دار است (حداکثر projectFactsMaxCommands دستور)
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "- Suggested verification: ") {
			if n := len(strings.Split(strings.TrimPrefix(line, "- Suggested verification: "), " | ")); n > projectFactsMaxCommands {
				t.Fatalf("prompt block lists %d commands (max %d): %q", n, projectFactsMaxCommands, line)
			}
		}
	}
	if len([]rune(block)) > 900 {
		t.Fatalf("prompt block too long: %d runes", len([]rune(block)))
	}
	if strings.ContainsAny(block, "۰۱۲۳۴۵۶۷۸۹") {
		t.Fatal("model-facing block must be English (no Persian digits)")
	}
}
