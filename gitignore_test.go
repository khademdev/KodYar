// تست‌های تطبیق‌گر.gitignore — نفی (!)، فقط-پوشه (اسلش انتهایی)،
// الگوی لنگرشده، ** و فایل‌های.gitignore تودرتو.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGitignore(t *testing.T, ws, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ws, ".gitignore"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIgnoreMatchingTable(t *testing.T) {
	ws := t.TempDir()
	writeGitignore(t, ws, "*.log\nbuild/\n/rootonly.txt\n!keep.log\n**/gen/*.tmp\n")
	ig := newIgnoreStack()
	ig.push(ws, "")

	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{"x.log", false, true},         // الگوی basename در هر عمق
		{"sub/y.log", false, true},     // در زیرپوشه هم می‌گیرد
		{"keep.log", false, false},     // نفی بعد از تطبیق
		{"build", true, true},          // فقط-پوشه: خود پوشه
		{"build/gen.txt", false, true}, // فرزندِ پوشه‌ی ignore
		{"build/x/deep.txt", false, true},
		{"build.txt", false, false},        // build/ فقط پوشه می‌گیرد نه فایل
		{"rootonly.txt", false, true},      // لنگرشده به ریشه
		{"sub/rootonly.txt", false, false}, // لنگر — در زیرپوشه نمی‌گیرد
		{"a/gen/z.tmp", false, true},       // **/gen/*.tmp
		{"gen/z.tmp", false, true},         // ** با صفر عمق
		{"a/b/gen/z.tmp", false, true},     // ** با چند عمق
		{"a/gen/z.txt", false, false},      // پسوند متفاوت
		{"a/b.txt", false, false},
	}
	for _, c := range cases {
		if got := ig.ignored(c.rel, c.isDir); got != c.want {
			t.Errorf("ignored(%q, isDir=%v) = %v, want %v", c.rel, c.isDir, got, c.want)
		}
	}
}

func TestIgnoreAnchoredPattern(t *testing.T) {
	ws := t.TempDir()
	writeGitignore(t, ws, "docs/secret.md\n")
	ig := newIgnoreStack()
	ig.push(ws, "")
	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{"docs/secret.md", false, true}, // لنگر با اسلش میانی
		{"secret.md", false, false},     // در ریشه نمی‌گیرد (نیازی به / نیست؟ نه — اسلش میانی یعنی لنگر)
		{"other/secret.md", false, false},
	}
	for _, c := range cases {
		if got := ig.ignored(c.rel, c.isDir); got != c.want {
			t.Errorf("ignored(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
}

func TestIgnoreDoubleStarVariants(t *testing.T) {
	ws := t.TempDir()
	writeGitignore(t, ws, "docs/**/*.md\ntmp/**\n")
	ig := newIgnoreStack()
	ig.push(ws, "")
	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{"docs/a.md", false, true}, // ** = صفر پوشه
		{"docs/x/a.md", false, true},
		{"docs/x/y/a.md", false, true},
		{"docs/a.txt", false, false},
		{"other/x/a.md", false, false},          // لنگر به docs
		{"tmp/anything/deep/file", false, true}, // tmp/** همه‌چیز زیر tmp
		{"tmp", true, false},                    // خود tmp (پترن tmp/** خود پوشه را نمی‌گیرد — مثل git)
	}
	for _, c := range cases {
		if got := ig.ignored(c.rel, c.isDir); got != c.want {
			t.Errorf("ignored(%q, isDir=%v) = %v, want %v", c.rel, c.isDir, got, c.want)
		}
	}
}

func TestNestedGitignorePrecedence(t *testing.T) {
	ws := t.TempDir()
	writeGitignore(t, ws, "*.log\n")
	sub := filepath.Join(ws, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGitignore(t, sub, "!important.log\n")

	ig := newIgnoreStack()
	ig.push(ws, "")
	ig.push(sub, "sub")

	if ig.ignored("x.log", false) != true {
		t.Fatal("root rule must ignore x.log")
	}
	// قاعده‌ی پوشه‌ی عمیق‌تر دیرتر push شده → نفی برنده است
	if ig.ignored("sub/important.log", false) != false {
		t.Fatal("nested negation must win for sub/important.log")
	}
	if ig.ignored("sub/other.log", false) != true {
		t.Fatal("root rule still applies in sub")
	}
}

func TestParseIgnoreRuleEdgeCases(t *testing.T) {
	if _, ok := parseIgnoreRule("", ""); ok {
		t.Fatal("empty line must not parse")
	}
	if _, ok := parseIgnoreRule("# comment", ""); ok {
		t.Fatal("comment must not parse")
	}
	r, ok := parseIgnoreRule("!important.log", "")
	if !ok || !r.negated || r.pattern != "important.log" {
		t.Fatalf("negation parse wrong: %+v", r)
	}
	r, ok = parseIgnoreRule("build/", "")
	if !ok || !r.dirOnly || r.pattern != "build" {
		t.Fatalf("dir-only parse wrong: %+v", r)
	}
	r, ok = parseIgnoreRule("/abs.txt", "")
	if !ok || !r.anchored || r.pattern != "abs.txt" {
		t.Fatalf("anchored parse wrong: %+v", r)
	}
	r, ok = parseIgnoreRule("a/b.txt", "")
	if !ok || !r.anchored {
		t.Fatalf("middle-slash must anchor: %+v", r)
	}
}

// TestSearchFilesHonorsGitignore: integration واقعی search_files با.gitignore.
func TestSearchFilesHonorsGitignore(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	writeGitignore(t, ws, "*.log\nbuild/\n/rootonly.txt\n")
	files := map[string]string{
		"keep.txt":      "needle-keep",
		"skip.log":      "needle-skip",
		"build/gen.txt": "needle-build",
		"rootonly.txt":  "needle-root",
	}
	for rel, content := range files {
		p := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, ok := a.execTool("search_files", `{"query":"needle"}`)
	if !ok {
		t.Fatalf("search failed: %s", r)
	}
	if !strings.Contains(r, "keep.txt") {
		t.Fatalf("non-ignored match missing: %s", r)
	}
	for _, banned := range []string{"skip.log", "build/gen.txt", "rootonly.txt"} {
		if strings.Contains(r, banned) {
			t.Fatalf("gitignored file %s appeared in results: %s", banned, r)
		}
	}
}

// TestListDirHonorsGitignoreAndSkips: integration واقعی list_dir.
func TestListDirHonorsGitignoreAndSkips(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	writeGitignore(t, ws, "ignored/\n*.tmp\n")
	for _, d := range []string{"ignored", "visible", "node_modules"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"a.txt", "b.tmp", "ignored/inside.txt"} {
		if err := os.WriteFile(filepath.Join(ws, filepath.FromSlash(f)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, ok := a.execTool("list_dir", `{"path":"."}`)
	if !ok {
		t.Fatalf("list_dir failed: %s", r)
	}
	if !strings.Contains(r, "visible/") || !strings.Contains(r, "a.txt") {
		t.Fatalf("visible entries missing: %s", r)
	}
	for _, banned := range []string{"ignored/", "b.tmp", "node_modules/"} {
		if strings.Contains(r, banned) {
			t.Fatalf("entry %s must be hidden from list_dir: %s", banned, r)
		}
	}
}

// TestGitignoreCapBounded: فایل.gitignore عظیم نباید قاعده‌ی بی‌نهایت بسازد.
func TestGitignoreCapBounded(t *testing.T) {
	ws := t.TempDir()
	huge := strings.Repeat("file*.txt\n", 20000) // ~۱۲۰ هزار خط
	if err := os.WriteFile(filepath.Join(ws, ".gitignore"), []byte(huge), 0o644); err != nil {
		t.Fatal(err)
	}
	ig := newIgnoreStack()
	ig.push(ws, "")
	if len(ig.rules) > gitignoreMaxRules {
		t.Fatalf("rules not capped: %d", len(ig.rules))
	}
	// فایل بزرگ‌تر از سقف بایت کلاً نادیده گرفته می‌شود
	ws2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws2, ".gitignore"), make([]byte, gitignoreMaxFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	ig2 := newIgnoreStack()
	ig2.push(ws2, "")
	if len(ig2.rules) != 0 {
		t.Fatalf("oversized gitignore must be skipped: %d", len(ig2.rules))
	}
}
