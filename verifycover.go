// پوشش راستی‌آزمایی (فاز ۲): «آیا تستی که اجرا شد، همان بخشی را می‌پوشاند
// که تغییر کرده؟»
//
// پیش از این، هر دستور با پیشوند go build/test «راستی‌آزمایی» شمرده می‌شد؛
// یعنی `go build ./...` روی کل مخزن حتی وقتی فقط یک پکیج عوض شده بود
// «تأیید کامل» حساب می‌شد و تست همان پکیج هرگز اجرا نمی‌شد.
//
// این فایل بدون تغییر معنای needsVerification کار می‌کند؛ فقط تذکر پایان
// نوبت را دقیق‌تر می‌کند: دایرکتوری‌های تغییرکرده را می‌گوید، تفکیک
// «فقط build» از «تست» را می‌داند و یک دستور تست هدفمند پیشنهاد می‌دهد.
//
// طراحی: تحلیل متنیِ دستور است (بدون اجرا). زنجیره‌های && / ; / | شکسته
// می‌شوند و برای هر سگمنت، برنامه/زیرفرمان، نوع (test|build)، پوشش کامل
// (`./...`) و مسیرهای هدف استخراج می‌شود.
package main

import (
	"path"
	"sort"
	"strings"
)

// maxCoverageDirs سقف دایرکتوری‌های نام‌برده‌شده در تذکر (پیام کوتاه بماند).
const maxCoverageDirs = 6

// verifScope یک سگمنتِ دستورِ راستی‌آزمایی.
type verifScope struct {
	Command string   // متن نرمال‌شده‌ی سگمنت
	Kind    string   // "test" | "build"
	All     bool     // ./... یا "all" (کارگو) — همه را می‌پوشاند
	Targets []string // دایرکتوری‌های نسبی نرمال‌شده
}

// verificationCommandTable پیشوندهای شناخته‌شده و نوعشان. ترتیب مهم است:
// بلندترین پیشوند اول بررسی شود (مثل "go test" در برابر "go").
var verificationCommandTable = []struct {
	prefix string
	kind   string
}{
	{"go test", "test"},
	{"go vet", "test"},
	{"go build", "build"},
	{"cargo nextest", "test"},
	{"cargo test", "test"},
	{"cargo check", "build"},
	{"cargo build", "build"},
	{"npm run test", "test"},
	{"npm test", "test"},
	{"pnpm test", "test"},
	{"yarn test", "test"},
	{"bun test", "test"},
	{"python -m pytest", "test"},
	{"python3 -m pytest", "test"},
	{"pytest", "test"},
	{"dotnet test", "test"},
	{"dotnet build", "build"},
	{"mvn test", "test"},
	{"mvn verify", "test"},
	{"gradle test", "test"},
	{"swift test", "test"},
	{"swift build", "build"},
	{"make test", "test"},
	{"just test", "test"},
}

// normalizeChangedPath مسیر نسبی را یکدست می‌کند (مثل toolbatch).
func normalizeChangedPath(rel string) string {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	rel = strings.TrimPrefix(rel, "./")
	if rel == "" || rel == "." || rel == "/" {
		return ""
	}
	return path.Clean(rel)
}

// dirOfPath دایرکتوری یک مسیر نسبی ("." برای فایل‌های ریشه).
func dirOfPath(rel string) string {
	rel = normalizeChangedPath(rel)
	if rel == "" {
		return ""
	}
	d := path.Dir(rel)
	if d == "" {
		return "."
	}
	return d
}

// changedDirs دایرکتوری‌های یکتای فایل‌های تغییرکرده (مرتب و قطعی).
func changedDirs(files []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		d := dirOfPath(f)
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// splitShellSegments دستور را روی && / || / ; / | می‌شکند (بدون تفسیر نقل‌قول).
func splitShellSegments(cmd string) []string {
	var out []string
	var cur strings.Builder
	i := 0
	for i < len(cmd) {
		c := cmd[i]
		switch {
		case c == '&' && i+1 < len(cmd) && cmd[i+1] == '&':
			out = append(out, cur.String())
			cur.Reset()
			i += 2
		case c == '|' && i+1 < len(cmd) && cmd[i+1] == '|':
			out = append(out, cur.String())
			cur.Reset()
			i += 2
		case c == '|' || c == ';':
			out = append(out, cur.String())
			cur.Reset()
			i++
		default:
			cur.WriteByte(c)
			i++
		}
	}
	out = append(out, cur.String())
	return out
}

// normalizeCommandText فاصله‌ها را یکی می‌کند و پیشوندهای محیطی ساده را می‌زند.
func normalizeCommandText(cmd string) string {
	n := strings.Join(strings.Fields(strings.TrimSpace(cmd)), " ")
	for {
		switch {
		case strings.HasPrefix(n, "cd ") && strings.Contains(n, "&&"):
			// "cd X && go test ./..." → بخش بعد از &&
			n = strings.TrimSpace(n[strings.Index(n, "&&")+2:])
			continue
		}
		break
	}
	return n
}

// parseVerifScopes سگمنت‌های راستی‌آزمایی یک دستور را استخراج می‌کند.
func parseVerifScopes(cmd string) []verifScope {
	var out []verifScope
	for _, seg := range splitShellSegments(cmd) {
		n := normalizeCommandText(seg)
		if n == "" {
			continue
		}
		var kind string
		var rest string
		for _, entry := range verificationCommandTable {
			if n == entry.prefix || strings.HasPrefix(n, entry.prefix+" ") {
				kind = entry.kind
				rest = strings.TrimSpace(strings.TrimPrefix(n, entry.prefix))
				break
			}
		}
		if kind == "" {
			continue
		}
		s := verifScope{Command: n, Kind: kind}
		for _, tok := range strings.Fields(rest) {
			switch {
			case tok == "./..." || tok == "..." || tok == "all":
				s.All = true
			case strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, "$"):
				// پرچم یا متغیر — هدف مسیر نیست
			case strings.HasPrefix(tok, "./") || tok == ".":
				s.Targets = append(s.Targets, normalizeTargetDir(tok))
			}
		}
		out = append(out, s)
	}
	return out
}

// normalizeTargetDir "./pkg/a/..." → "pkg/a" و "./" → ".".
func normalizeTargetDir(tok string) string {
	t := strings.TrimPrefix(tok, "./")
	t = strings.TrimSuffix(t, "/...")
	t = strings.TrimSuffix(t, "...")
	t = strings.Trim(t, "/")
	if t == "" || t == "." {
		return "."
	}
	return path.Clean(t)
}

// coverageGaps بخش‌هایی از تغییرات را که هیچ تستی پوشش نمی‌دهد برمی‌گرداند.
// buildOnly=true یعنی بعد از آخرین تغییر فقط ساخت/check اجرا شده بود.
func coverageGaps(files []string, lastCmd string) (missing []string, buildOnly bool) {
	dirs := changedDirs(files)
	lastCmd = strings.TrimSpace(lastCmd)
	if len(dirs) == 0 || lastCmd == "" {
		return dirs, false
	}
	scopes := parseVerifScopes(lastCmd)
	hasTest := false
	for _, s := range scopes {
		if s.Kind == "test" {
			hasTest = true
			break
		}
	}
	covered := func(d string) bool {
		for _, s := range scopes {
			if s.Kind != "test" {
				continue
			}
			if s.All {
				return true
			}
			for _, t := range s.Targets {
				if t == d || strings.HasPrefix(d, t+"/") || strings.HasPrefix(t, d+"/") {
					return true
				}
			}
		}
		return false
	}
	for _, d := range dirs {
		if !covered(d) {
			missing = append(missing, d)
		}
	}
	return missing, !hasTest && len(missing) == len(dirs)
}

// stripPathTargets آرگومان‌های مسیری انتهای دستور را برمی‌دارد تا بتوان
// مسیرهای هدفمند جایشان گذاشت: "go test ./..." → "go test".
func stripPathTargets(cmd string) string {
	fields := strings.Fields(strings.TrimSpace(cmd))
	for len(fields) > 1 {
		last := fields[len(fields)-1]
		if strings.Contains(last, "-") || !(last == "." || strings.HasPrefix(last, "./")) {
			break
		}
		fields = fields[:len(fields)-1]
	}
	return strings.Join(fields, " ")
}

// supportsPathTargets آیا این دستور مسیر/پکیج هدف را می‌پذیرد؟
func supportsPathTargets(base string) bool {
	first := strings.Fields(base)
	if len(first) == 0 {
		return false
	}
	switch first[0] {
	case "go", "cargo", "pytest", "python", "python3":
		return true
	}
	return false
}

// verificationNudgePayload متن انگلیسی تذکر (برای مدل) و متن فارسی اعلان
// (برای UI) را می‌سازد. وقتی پوششی کم نبود، متن پایه برگردانده می‌شود.
func verificationNudgePayload(files []string, lastVerCmd string, cmds []string) (nudge, notice string, gaps []string) {
	gaps, buildOnly := coverageGaps(files, lastVerCmd)
	nudge = verificationNudgeText
	notice = "فایل‌ها تغییر کردند ولی از آخرین تغییر، تست یا ساختی با موفقیت اجرا نشده؛ از مدل خواسته شد آن را اجرا کند."
	if len(gaps) == 0 {
		return nudge, notice, nil
	}
	shown := gaps
	if len(shown) > maxCoverageDirs {
		shown = shown[:maxCoverageDirs]
	}
	nudge += "\nChanged directories: " + strings.Join(shown, ", ")
	if buildOnly {
		nudge += "\nA build-only check does not cover these changes."
	}
	if sug := suggestTargetedTest(cmds, gaps); sug != "" {
		nudge += "\nRun a targeted test, e.g.: " + sug
		notice = "فایل‌ها در " + strings.Join(shown, "، ") + " تغییر کردند ولی تستِ همان بخش اجرا نشد؛ پیشنهاد: " + sug
	} else {
		notice = "فایل‌های تغییرکرده (" + strings.Join(shown, "، ") + ") هنوز تست نشده‌اند؛ از مدل خواسته شد یک تست هدفمند در همان پوشه اجرا کند."
	}
	return nudge, notice, shown
}

// suggestTargetedTest یک دستور تست هدفمند از روی دستورهای تشخیص‌داده‌شده‌ی
// پروژه (projectFacts) و دایرکتوری‌های تغییرکرده می‌سازد.
func suggestTargetedTest(cmds []string, dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}
	testCmd := ""
	for _, c := range cmds {
		if strings.Contains(strings.ToLower(c), "test") {
			testCmd = strings.TrimSpace(c)
			break
		}
	}
	if testCmd == "" {
		return ""
	}
	base := stripPathTargets(testCmd)
	if !supportsPathTargets(base) {
		return base
	}
	for _, d := range dirs {
		if d == "." {
			return base + " ./..."
		}
	}
	var args []string
	for i, d := range dirs {
		if i >= 4 {
			args = append(args, "…")
			break
		}
		args = append(args, "./"+d+"/...")
	}
	return base + " " + strings.Join(args, " ")
}
