// «واقعیت‌های پروژه» — تشخیص نوع پروژه و دستورهای پیشنهادیِ ساخت/تست
// (مأموریت ۳ — مرحله A.3).
//
// هدف: مدل بداند در این پوشه‌ی کاری چه نوع پروژه‌ای است و راستی‌آزمایی را با
// چه دستوری انجام دهد، بدون اینکه چیزی اجرا شود. فقط چند فایل تعریف ساخت
// (حداکثر ۶۴ کیلوبایت از هرکدام) خوانده می‌شود؛ هیچ فرمانی اجرا نمی‌شود و
// هیچ چیزی نوشته نمی‌شود.
//
// کش: یک ورودی به‌ازای پوشه‌ی کاری، با اثر انگشت (حجم + mtime) همان فایل‌ها؛
// تغییر فایل → محاسبه‌ی دوباره. قفلش برگ است (در طول نگه‌داشتنش I/O نمی‌شود).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// projectFactsMaxBytes سقف خواندن هر فایل تعریف ساخت.
const projectFactsMaxBytes = 64 * 1024

// projectFactProbeFiles فایل‌های تعریف ساختِ شناخته‌شده (سطح ریشه).
var projectFactProbeFiles = []string{
	"go.mod", "package.json", "Cargo.toml",
	"pyproject.toml", "pytest.ini", "tox.ini", "setup.cfg",
	"pom.xml", "build.gradle", "build.gradle.kts",
	"composer.json", "Makefile", "makefile", "GNUmakefile",
}

// projectFacts نتیجه‌ی تشخیص (خالص، قابل مقایسه).
type projectFacts struct {
	Kinds    []string `json:"kinds"`    // نوع پروژه (ممکن است چندتایی باشد)
	Commands []string `json:"commands"` // دستورهای پیشنهادی راستی‌آزمایی
}

// empty آیا چیزی تشخیص داده نشد؟
func (f projectFacts) empty() bool {
	return len(f.Kinds) == 0 && len(f.Commands) == 0
}

// سقف‌های بلوک پرامپت — کوتاه بماند.
const (
	projectFactsMaxKinds    = 4
	projectFactsMaxCommands = 6
)

// promptBlock بلوک انگلیسی برای system prompt ("" وقتی چیزی نیست).
func (f projectFacts) promptBlock() string {
	if f.empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("Project facts (detected by reading build/definition files — nothing was executed):\n")
	if len(f.Kinds) > 0 {
		kinds := f.Kinds
		if len(kinds) > projectFactsMaxKinds {
			kinds = kinds[:projectFactsMaxKinds]
		}
		b.WriteString("- Project type: " + strings.Join(kinds, "; ") + "\n")
	}
	if len(f.Commands) > 0 {
		cmds := f.Commands
		if len(cmds) > projectFactsMaxCommands {
			cmds = cmds[:projectFactsMaxCommands]
		}
		b.WriteString("- Suggested verification: " + strings.Join(cmds, " | ") + "\n")
	}
	b.WriteString("- Prefer these commands to verify your changes; if they fail, read the output and fix the cause.\n")
	return b.String()
}

// readHeadBytes حداکثر projectFactsMaxBytes از ابتدای فایل می‌خواند (کران‌دار).
func readHeadBytes(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, projectFactsMaxBytes))
}

// makeTargetRe خط تعریف هدف در Makefile (ابتدای خط، بدون فاصله).
var makeTargetRe = regexp.MustCompile(`^([A-Za-z0-9_.][A-Za-z0-9_.-]*)\s*:`)

// detectProjectFacts نوع پروژه و دستورهای راستی‌آزمایی را از فایل‌های تعریف
// ساخت تشخیص می‌دهد. هیچ چیزی اجرا نمی‌شود؛ خطاها بی‌صدا نادیده گرفته
// می‌شوند (فایل نخواندنی = نشانه‌ای نیست).
func detectProjectFacts(workspace string) projectFacts {
	var f projectFacts
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		return f
	}
	seenCmd := map[string]bool{}
	addKind := func(k string) {
		for _, v := range f.Kinds {
			if v == k {
				return
			}
		}
		f.Kinds = append(f.Kinds, k)
	}
	addCmd := func(c string) {
		if c == "" || seenCmd[c] {
			return
		}
		seenCmd[c] = true
		f.Commands = append(f.Commands, c)
	}
	exists := func(name string) bool {
		st, err := os.Stat(filepath.Join(ws, name))
		return err == nil && !st.IsDir()
	}

	// ---- Go
	if exists("go.mod") {
		kind := "Go module"
		if data, err := readHeadBytes(filepath.Join(ws, "go.mod")); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "module ") {
					if name := strings.TrimSpace(strings.TrimPrefix(line, "module ")); name != "" {
						kind = "Go module " + name
					}
					break
				}
			}
		}
		addKind(kind)
		addCmd("go build ./...")
		addCmd("go test ./...")
	}

	// ---- Node.js (npm)
	if exists("package.json") {
		addKind("Node.js (npm)")
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if data, err := readHeadBytes(filepath.Join(ws, "package.json")); err == nil {
			_ = json.Unmarshal(data, &pkg) // فایل خراب → فقط نوع پروژه
		}
		has := func(name string) bool {
			if pkg.Scripts == nil {
				return false
			}
			_, ok := pkg.Scripts[name]
			return ok
		}
		if has("test") {
			addCmd("npm test")
		}
		for _, s := range []string{"build", "lint", "typecheck"} {
			if has(s) {
				addCmd("npm run " + s)
			}
		}
	}

	// ---- Rust
	if exists("Cargo.toml") {
		addKind("Rust (Cargo)")
		addCmd("cargo build")
		addCmd("cargo test")
	}

	// ---- Python
	if exists("pyproject.toml") || exists("pytest.ini") || exists("tox.ini") || exists("setup.cfg") {
		addKind("Python")
		addCmd("python -m pytest")
	}

	// ---- Java/Maven
	if exists("pom.xml") {
		addKind("Java (Maven)")
		addCmd("mvn test")
	}

	// ---- Gradle
	if exists("build.gradle") || exists("build.gradle.kts") {
		addKind("JVM (Gradle)")
		addCmd("gradle test")
		addCmd("gradle build")
	}

	// ---- .NET (فقط سطح ریشه — پیمایش بازگشتی انجام نمی‌شود)
	if entries, err := os.ReadDir(ws); err == nil {
		sln, csproj := false, false
		for i, e := range entries {
			if i > 2000 {
				break
			}
			if e.IsDir() {
				continue
			}
			name := strings.ToLower(e.Name())
			if strings.HasSuffix(name, ".sln") {
				sln = true
			}
			if strings.HasSuffix(name, ".csproj") {
				csproj = true
			}
		}
		if sln || csproj {
			addKind(".NET (dotnet)")
			addCmd("dotnet build")
			addCmd("dotnet test")
		}
	}

	// ---- PHP
	if exists("composer.json") {
		addKind("PHP (Composer)")
		var cj struct {
			Scripts map[string]interface{} `json:"scripts"`
		}
		if data, err := readHeadBytes(filepath.Join(ws, "composer.json")); err == nil {
			if json.Unmarshal(data, &cj) == nil && cj.Scripts != nil {
				if _, ok := cj.Scripts["test"]; ok {
					addCmd("composer test")
				}
			}
		}
	}

	// ---- Makefile (اهداف test/build/lint)
	for _, name := range []string{"Makefile", "makefile", "GNUmakefile"} {
		p := filepath.Join(ws, name)
		if !exists(name) {
			continue
		}
		addKind("Makefile")
		if data, err := readHeadBytes(p); err == nil {
			targets := map[string]bool{}
			for _, line := range strings.Split(string(data), "\n") {
				if m := makeTargetRe.FindStringSubmatch(line); m != nil {
					targets[strings.ToLower(m[1])] = true
				}
			}
			for _, t := range []string{"test", "build", "lint"} {
				if targets[t] {
					addCmd("make " + t)
				}
			}
		}
		break
	}
	return f
}

// projectFactsStamp اثر انگشت فایل‌های تعریف ساخت (حجم + mtime + mtime پوشه).
// تغییر هر کدام → کش باطل. stat های تکراری ارزان‌اند و هیچ محتوایی خوانده
// نمی‌شود.
func projectFactsStamp(ws string) string {
	var b strings.Builder
	for _, name := range projectFactProbeFiles {
		if st, err := os.Stat(filepath.Join(ws, name)); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", name, st.Size(), st.ModTime().UnixNano())
		} else {
			b.WriteString(name + ":-;")
		}
	}
	if st, err := os.Stat(ws); err == nil {
		fmt.Fprintf(&b, "dir:%d;", st.ModTime().UnixNano())
	}
	return b.String()
}

// projectFactsCache کش تک‌ورودی (یک پوشه‌ی کاری در هر لحظه).
type projectFactsCache struct {
	mu        sync.Mutex
	workspace string
	stamp     string
	facts     projectFacts
	valid     bool
}

// projectFactsFor تشخیص با کش — I/O بیرون از قفل؛ فقط خواندن/نوشتن نقشه
// زیر قفل است.
func (a *App) projectFactsFor(workspace string) projectFacts {
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		return projectFacts{}
	}
	stamp := projectFactsStamp(ws)
	c := &a.factsCache
	c.mu.Lock()
	if c.valid && c.workspace == ws && c.stamp == stamp {
		f := c.facts
		c.mu.Unlock()
		return f
	}
	c.mu.Unlock()

	f := detectProjectFacts(ws)

	c.mu.Lock()
	c.workspace, c.stamp, c.facts, c.valid = ws, stamp, f, true
	c.mu.Unlock()
	return f
}

// projectFactsPromptLine بلوک آماده برای prepareStream ("" وقتی خالی).
// فراخواننده باید این را «قبل از» گرفتن a.mu محاسبه کند (I/O دارد).
func (a *App) projectFactsPromptLine(workspace string) string {
	return a.projectFactsFor(workspace).promptBlock()
}

// sortedKinds فقط برای پیام‌های عیب‌یابی/تست (کپی مرتب).
func (f projectFacts) sortedKinds() []string {
	out := append([]string(nil), f.Kinds...)
	sort.Strings(out)
	return out
}
