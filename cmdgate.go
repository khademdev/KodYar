// دروازه‌ی ساختاری دستورات (Stage 1).
//
// جایگزینِ «glob روی رشته‌ی نرمال‌شده» برای تصمیمِ allow: پس از تجزیه‌ی
// واقعی (shellparse.go)، یک دستور ساده فقط وقتی «خودکار مجاز» است که
// «همه‌ی» این شرایط برقرار باشد:
//
//	۱. با قطعیت تجزیه شده باشد (ok=true)؛
//	۲. گسترش متغیر/زیرپوسته/پس‌زمینه نداشته باشد؛
//	۳. تغییرمسیر فقط به NUL/‎/dev/null‎ یا شکل fd-dup (‎2>&1‎) باشد؛
//	۴. هر توکن یا پرچمِ مجازِ همان ابزار باشد یا کلمه‌ی ساده‌ی بی‌خطر؛
//	۵. هیچ کلمه‌ای مسیر مطلقِ بیرون از پوشه‌ی کاری یا «..» گذرنده نباشد؛
//	۶. هیچ توکنی (یا مقصد تغییرمسیر) به فایل/پوشه‌ی حساس اشاره نکند.
//
// cat/type/head/tail/wc عمداً از لیست حذف شده‌اند: ایجنت read_file دارد که
// قواعد فایل حساس را اجباری می‌کند؛ خواندن از مسیر shell این قواعد را دور
// می‌زد (D3). توکن‌های اجراکننده‌ی کد/نوشنده‌ی فایل (‎--output‎، ‎-exec‎،
// ‎-toolexec‎، ‎-vettool‎، ‎-o‎، ‎-c‎ git) صریحاً رد می‌شوند (D5).
//
// ترتیب تصمیم برای هر دستور ساده:
//
//	لیست سیاه سخت (deny) ← payload مفسر/زیرپوسته (بازگشتی) ← توکن حساس
//	(حداقل ask، بدون «همیشه») ← قاعده‌ی کاربر (اولین تطبیق؛ allow صریحِ
//	کاربر فقط اگر الگوی خودش توکن پرریسک را صریح نداشته باشد) ← الگوی
//	پرریسک (ask) ← لیست‌سفید ساختاری (allow) ← ask.
//
// سخت‌گیرترین تصمیمِ دستورهای ساده برنده می‌شود.
package main

import (
	"path"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------- مشخصات امن

// سیاست پرچم‌ها.
type flagPolicy int

const (
	flagsWhitelist flagPolicy = iota // فقط پرچم‌های allowed (git/go/npm)
	flagsAny                         // هر پرچم — فرمان فقط-خواندنی است
)

// safeCommandSpec مشخصه‌ی ساختاری یک دستور خودکار-مجاز.
// exact غیرخالی → کل argv باید دقیقاً یکی از دنباله‌ها باشد (مثل
// ‎node --version‎). وگرنه: argv0 + sub اجباری + sub2 اختیاری + بقیه‌ی
// توکن‌ها طبق flags/allowed/denied و allowPositional.
type safeCommandSpec struct {
	argv0           string
	exact           [][]string
	sub             []string
	sub2            []string // توکن سومِ الزامی (npm run <test|lint|…>)
	flags           flagPolicy
	allowed         []string
	denied          []string
	allowPositional bool
}

// gitDenied پرچم‌های git که هر زیرفرمانی را از لیست خارج می‌کنند:
// نوشتن فایل (‎--output‎)، اجرای diff خارجی (‎--ext-diff‎)، تزریق تنظیم/
// اجرای دستور (‎-c‎)، دریافت بسته‌ی خارجی (‎--upload-pack‎)، تعویض دایرکتوری
// مخزن (‎-C‎ و هم‌خانواده‌هایش).
var gitDenied = []string{
	"--output", "--ext-diff", "-c", "--upload-pack", "-C", "--git-dir", "--work-tree",
}

// goDenied پرچم‌های go build/test/vet که برنامه اجرا می‌کنند یا فایل
// می‌نویسند و از جریان diff-confirm عبور نمی‌کنند (D5).
var goDenied = []string{"-exec", "-toolexec", "-vettool", "-o"}

// npmDenied: «--» آرگومان‌ها را به اسکریپت پاس می‌دهد — پاس‌دادنِ آرگومان
// به کد اجراشدنی مجاز نیست.
var npmDenied = []string{"--"}

// safeCommandSpecs لیست‌سفید ساختاری پیش‌فرض (جایگزین defaultSafeCommands).
var safeCommandSpecs = []safeCommandSpec{
	// ---- git: فقط زیرفرمان‌های فقط-خواندنی، لیست پرچم صریح
	{argv0: "git", sub: []string{"status"},
		allowed: []string{"--short", "-s", "--long", "--branch", "-b", "--porcelain", "-u", "--untracked-files",
			"--ignored", "--no-renames", "--find-renames", "-z", "--format", "--ahead-behind", "-v", "--verbose", "-uall",
			"--column", "--no-column", "--"},
		denied: gitDenied, allowPositional: true},
	{argv0: "git", sub: []string{"diff"},
		allowed: []string{"--stat", "--cached", "--staged", "--name-only", "--name-status", "--shortstat",
			"--numstat", "-u", "--unified", "-m", "--find-renames", "--no-renames", "--relative", "--full-index",
			"-z", "--raw", "--patch", "--no-patch", "-s", "--exit-code", "--quiet", "--color", "--no-color",
			"-w", "--ignore-all-space", "--ignore-space-change", "--abbrev", "--text", "-a", "--"},
		denied: gitDenied, allowPositional: true},
	{argv0: "git", sub: []string{"log"},
		allowed: []string{"--oneline", "-n", "--decorate", "--no-decorate", "--graph", "--all", "--pretty",
			"--format", "--since", "--until", "--author", "--grep", "--follow", "-p", "--stat", "--name-only",
			"--name-status", "--shortstat", "--numstat", "--date", "--relative-date", "--color", "--no-color",
			"--no-merges", "--merges", "--reverse", "-z", "--"},
		denied: gitDenied, allowPositional: true},
	{argv0: "git", sub: []string{"show"},
		allowed: []string{"--stat", "--oneline", "--name-only", "--name-status", "--format", "--pretty",
			"--no-patch", "-s", "--abbrev", "--raw", "--color", "--no-color", "--"},
		denied: gitDenied, allowPositional: true},
	{argv0: "git", sub: []string{"branch"}, // فقط حالت فهرست — آرگومانِ نامِ شاخه = ساخت شاخه
		allowed: []string{"-a", "--all", "-v", "-vv", "--list", "-r", "--remotes", "--show-current",
			"--contains", "--merged", "--no-merged", "--format", "-q", "--quiet", "--color", "--no-color"},
		denied: gitDenied},
	{argv0: "git", sub: []string{"rev-parse"},
		allowed: []string{"--short", "--verify", "--abbrev-ref", "--git-dir", "--show-toplevel",
			"--is-inside-work-tree", "--show-prefix", "--git-common-dir", "--absolute-git-dir", "--quiet",
			"-q", "--symbolic", "--symbolic-full-name", "--default", "--"},
		denied: gitDenied, allowPositional: true},
	{argv0: "git", sub: []string{"remote"}, // فقط فهرست/‎-v‎ — «remote add …» آرگومان دارد
		allowed: []string{"-v", "--verbose"}, denied: gitDenied},

	// ---- go: build|test|vet با لیست پرچم مشخصات (D5)
	{argv0: "go", sub: []string{"build", "test", "vet"},
		allowed: []string{"-v", "-run", "-count", "-race", "-short", "-timeout", "-cover", "-bench",
			"-tags", "-p", "--"},
		denied: goDenied, allowPositional: true},

	// ---- npm: test / run test|lint|build|typecheck — بدون پاس‌دادن آرگومان
	{argv0: "npm", sub: []string{"test"},
		flags: flagsWhitelist, allowed: []string{"--silent", "-s", "--no-audit", "--no-fund"},
		denied: npmDenied},
	{argv0: "npm", sub: []string{"run"}, sub2: []string{"test", "lint", "build", "typecheck"},
		flags: flagsWhitelist, allowed: []string{"--silent", "-s", "--no-audit", "--no-fund"},
		denied: npmDenied},

	// ---- node: فقط بررسی نحوی (بدون اجرا) — راستی‌آزمایی فایل‌های JS
	{argv0: "node", sub: []string{"--check"},
		allowed: []string{"--no-warnings"}, allowPositional: true},

	// ---- python: فقط py_compile (راستی‌آزمایی خودکار پروژه‌های پایتون)
	// ماژول با `-m py_compile` ثابت است؛ ‎-c‎/اسکریپت دلخواه همچنان تأیید
	// می‌خواهد. پرچم‌های دیگر (‎-o‎، ‎--help‎، …) در لیست سفید نیستند و
	// تغییرمسیر/گسترش/پس‌زمینه پیش از رسیدن به این‌جا رد می‌شوند.
	{argv0: "python", sub: []string{"-m"}, sub2: []string{"py_compile"},
		allowed: []string{"-q"}, allowPositional: true},
	{argv0: "python3", sub: []string{"-m"}, sub2: []string{"py_compile"},
		allowed: []string{"-q"}, allowPositional: true},

	// ---- فقط-خواندنیِ نمایشی — پرچم آزاد، بدون تغییرمسیر/گسترش (سراسری)
	{argv0: "ls", flags: flagsAny, allowPositional: true},
	{argv0: "dir", flags: flagsAny, allowPositional: true},
	{argv0: "echo", flags: flagsAny, allowPositional: true},
	{argv0: "pwd", flags: flagsAny},
	{argv0: "whoami", flags: flagsAny},
	{argv0: "hostname", flags: flagsAny}, // «hostname <name>» تنظیم می‌کند — بدون positional
	{argv0: "where", flags: flagsAny, allowPositional: true},
	{argv0: "which", flags: flagsAny, allowPositional: true},

	// ---- تطبیق دقیق کامل (بدون هیچ توکن اضافه)
	{argv0: "true", exact: [][]string{{"true"}}},
	{argv0: "false", exact: [][]string{{"false"}}},
	{argv0: "node", exact: [][]string{{"node", "--version"}, {"node", "-v"}}},
	{argv0: "npm", exact: [][]string{{"npm", "--version"}}},
	{argv0: "python", exact: [][]string{{"python", "--version"}, {"python", "-V"}}},
	// توجه: py_compile در بخش «python -m py_compile» بالا آمده است.
	{argv0: "python3", exact: [][]string{{"python3", "--version"}, {"python3", "-V"}}},
	{argv0: "git", exact: [][]string{{"git", "--version"}}},
	{argv0: "go", exact: [][]string{{"go", "version"}}},
	{argv0: "cargo", exact: [][]string{{"cargo", "--version"}, {"cargo", "-V"}}},
	{argv0: "rustc", exact: [][]string{{"rustc", "--version"}, {"rustc", "-V"}}},
	{argv0: "java", exact: [][]string{{"java", "-version"}, {"java", "--version"}}},
	{argv0: "dotnet", exact: [][]string{{"dotnet", "--version"}}},
	{argv0: "docker", exact: [][]string{{"docker", "--version"}}},
}

// ---------------------------------------------------------------- تطبیق مشخصه

// matchSafeSpec مشخصه‌ی منطبق بر argv را برمی‌گرداند (یا nil).
func matchSafeSpec(argv []string) *safeCommandSpec {
	if len(argv) == 0 {
		return nil
	}
	argv0 := strings.ToLower(argv[0])
	for i := range safeCommandSpecs {
		s := &safeCommandSpecs[i]
		if s.argv0 != argv0 {
			continue
		}
		if len(s.exact) > 0 {
			for _, seq := range s.exact {
				if len(argv) == len(seq) {
					eq := true
					for k := range seq {
						if !strings.EqualFold(seq[k], argv[k]) {
							eq = false
							break
						}
					}
					if eq {
						return s
					}
				}
			}
			continue
		}
		if len(s.sub) > 0 {
			if len(argv) < 2 || !containsFold(s.sub, argv[1]) {
				continue
			}
			if len(s.sub2) > 0 && (len(argv) < 3 || !containsFold(s.sub2, argv[2])) {
				continue
			}
			return s
		}
		// بدون sub — خودِ argv0 (ls, echo, …)
		return s
	}
	return nil
}

func containsFold(set []string, s string) bool {
	for _, v := range set {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// globalDeniedFlagSet اجتماع پرچم‌های ممنوع همه‌ی مشخصه‌ها — برای کاهش
// سطح قاعده‌ی allow کاربر وقتی پرچم اجرا/نوشتن ظاهر می‌شود.
var globalDeniedFlagSet = func() map[string]bool {
	m := map[string]bool{}
	for i := range safeCommandSpecs {
		for _, f := range safeCommandSpecs[i].denied {
			m[strings.ToLower(f)] = true
		}
	}
	return m
}()

// isFlagToken آیا توکن در این شله «پرچم» است؟ POSIX: «-…»؛ cmd: «-…» یا «/…».
func isFlagToken(tok string, flavor shellFlavor) bool {
	if tok == "" {
		return false
	}
	if tok[0] == '-' {
		return true
	}
	return flavor == flavorCmd && tok[0] == '/'
}

// flagBase بخش پرچم بدون مقدارِ چسبیده (‎-run=X‎ → ‎-run‎).
func flagBase(tok string) string {
	if idx := strings.Index(tok, "="); idx > 0 {
		return tok[:idx]
	}
	return tok
}

// isNullDevice مقصد تغییرمسیرِ بی‌خطر (NUL ویندوز / ‎/dev/null‎).
func isNullDevice(target string) bool {
	t := strings.ToLower(strings.TrimSpace(target))
	return t == "nul" || t == "nul:" || t == "/dev/null"
}

// isAbsoluteCommandPath مسیر مطلق در این شله؟ (درایو/UNC در cmd؛ «/» در POSIX)
func isAbsoluteCommandPath(tok string, flavor shellFlavor) bool {
	if tok == "" {
		return false
	}
	if tok[0] == '/' || tok[0] == '\\' {
		return true
	}
	if flavor == flavorCmd && len(tok) >= 2 && tok[1] == ':' &&
		((tok[0] >= 'a' && tok[0] <= 'z') || (tok[0] >= 'A' && tok[0] <= 'Z')) {
		return true
	}
	return false
}

// splitPathComps اجزای مسیر با جداکننده‌های / و \ (خالی‌ها حذف).
func splitPathComps(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
}

// relativeEscapesWorkspace آیا «..»ها از سطح شروع پایین‌تر می‌روند؟
// (بدون دانستن پوشه‌ی کاری هم، عمق منفی = خروج.)
func relativeEscapesWorkspace(tok string) bool {
	depth := 0
	for _, c := range splitPathComps(tok) {
		switch c {
		case "..":
			depth--
			if depth < 0 {
				return true
			}
		case ".", "":
			// نادیده
		default:
			depth++
		}
	}
	return false
}

// commandPathInsideWorkspace آیا مسیر مطلق داخل پوشه‌ی کاری است؟
// جداکننده‌ها ابتدا به / یکسان می‌شوند و پاک‌سازی با path.Clean انجام
// می‌شود (نه filepath.Clean که رفتارش به سیستم‌عامل وابسته است: روی
// ویندوز دوباره بک‌اسلش می‌سازد و مقایسه‌ی مسیرِ POSIX را می‌شکند)؛ در
// شله‌ی cmd مقایسه به حروف کوچک است.
func commandPathInsideWorkspace(tok, ws string, flavor shellFlavor) bool {
	if ws == "" {
		return false
	}
	norm := func(p string) string {
		p = strings.ReplaceAll(p, "\\", "/")
		p = path.Clean(p)
		p = strings.TrimSuffix(p, "/")
		if flavor == flavorCmd {
			p = strings.ToLower(p)
		}
		return p
	}
	t, w := norm(tok), norm(ws)
	return t == w || strings.HasPrefix(t, w+"/")
}

// positionalIsSafe کلمه‌ی جایگاهی بی‌خطر است؟ (مطلق فقط داخل پوشه؛ بدون
// «..» گذرنده؛ بدون ~ که به خانه‌ی کاربر اشاره می‌کند)
func positionalIsSafe(tok string, flavor shellFlavor, ws string) bool {
	if tok == "" {
		return true
	}
	if flavor == flavorPOSIX && strings.HasPrefix(tok, "~") {
		return false
	}
	if isAbsoluteCommandPath(tok, flavor) {
		return commandPathInsideWorkspace(tok, ws, flavor)
	}
	return !relativeEscapesWorkspace(tok)
}

// ---------------------------------------------------------------- ارجاع حساس

// credentialDirComps اجزای مسیرِ پوشه‌های اعتبارنامه (۳.۲.۴).
var credentialDirComps = map[string]bool{
	".ssh": true, ".aws": true, ".gnupg": true, ".kube": true,
}

// sensitiveCommandRef آیا این توکن به فایل/پوشه‌ی حساس اشاره می‌کند؟
// الف) basename با الگوهای فایل حساس تطبیق کند؛
// ب) مسیر به پوشه‌های اعتبارنامه (‎.ssh‎، ‎.aws‎، …) یا ‎%APPDATA%‎ یا
//
//	«‎~/‎» اشاره کند.
//
// خروجی: الگوی مطابق (برای پیام) + برخورد.
func sensitiveCommandRef(tok string, globs []string) (string, bool) {
	if tok == "" {
		return "", false
	}
	if g := sensitiveFileReason(tok, globs); g != "" {
		return g, true
	}
	lt := strings.ToLower(tok)
	if strings.Contains(lt, "%appdata%") || strings.Contains(lt, "%userprofile%") {
		return "", true
	}
	comps := splitPathComps(lt)
	for i, c := range comps {
		if credentialDirComps[c] {
			return "", true
		}
		if c == ".config" && i+1 < len(comps) && comps[i+1] == "gcloud" {
			return "", true
		}
	}
	if strings.HasPrefix(lt, "~/") || lt == "~" {
		return "", true
	}
	return "", false
}

// ---------------------------------------------------------------- لیست‌سفید ساختاری

// simpleIsAutoAllowed بررسی شرایط ۱–۵ لیست‌سفید ساختاری برای یک دستور ساده.
// بررسی حساس بودن توکن‌ها (شرط ۶) جداست — چون بدون توجه به نتیجه‌ی این
// تابع، حداقل ask + «بدون همیشه» اعمال می‌شود.
func simpleIsAutoAllowed(sc *simpleCommand, flavor shellFlavor, ws string) bool {
	if len(sc.argv) == 0 {
		return false // فقط تغییرمسیر یا خالی — خطر نوشتن/ساختن فایل
	}
	if sc.hasExpansion || sc.hasSubshell || sc.background {
		return false
	}
	for _, r := range sc.redirects {
		if r.target == "" {
			if strings.Contains(r.op, "&") {
				continue // fd-dup مثل ‎2>&1‎ — بدون فایل
			}
			return false
		}
		if !isNullDevice(r.target) {
			return false // تغییرمسیر به فایل = دورزدن جریان diff-confirm
		}
	}
	spec := matchSafeSpec(sc.argv)
	if spec == nil {
		return false
	}
	// تطابق «دقیق» یعنی کل argv از قبل بررسی شده — توکن اضافه‌ای نیست
	if len(spec.exact) > 0 {
		return true
	}
	// توکن‌های بعد از argv0 (+sub +sub2)
	rest := sc.argv[1:]
	if len(spec.sub) > 0 {
		rest = rest[1:]
		if len(spec.sub2) > 0 {
			rest = rest[1:]
		}
	}
	for _, tok := range rest {
		if isFlagToken(tok, flavor) {
			base := flagBase(tok)
			for _, d := range spec.denied {
				if strings.EqualFold(d, base) {
					return false
				}
			}
			if spec.flags == flagsWhitelist && !containsFold(spec.allowed, base) {
				return false
			}
			continue
		}
		if !spec.allowPositional {
			return false
		}
		if !positionalIsSafe(tok, flavor, ws) {
			return false
		}
	}
	return true
}

// structuralRiskTokens توکن‌هایی که حتی با قاعده‌ی allow کاربر هم «ساختار»
// دستور را خطرناک می‌کنند: تغییرمسیر به فایل، پرچم ممنوع (اجرا/نوشتن)،
// گسترش متغیر، پس‌زمینه. قاعده‌ی کاربر فقط وقتی پذیرفته می‌شود که خودش
// همان توکن را صریح داشته باشد (مثل «git push --force*» یا «echo* > out.txt»).
func structuralRiskTokens(sc *simpleCommand, flavor shellFlavor) []string {
	var toks []string
	for _, r := range sc.redirects {
		if r.target == "" || isNullDevice(r.target) {
			continue // dup (‎2>&1‎) و NUL بی‌خطرند
		}
		toks = append(toks, strings.ToLower(r.target))
	}
	for _, tok := range sc.argv[1:] {
		if isFlagToken(tok, flavor) {
			if base := strings.ToLower(flagBase(tok)); globalDeniedFlagSet[base] {
				toks = append(toks, base)
			}
		}
	}
	if sc.hasExpansion {
		if flavor == flavorCmd {
			toks = append(toks, "%")
		} else {
			toks = append(toks, "$")
		}
	}
	if sc.background {
		toks = append(toks, "&")
	}
	return toks
}

// ---------------------------------------------------------------- payload مفسرها

// interpreterNames مفسرهایی که با پرچم اجرای کد صریح payload می‌گیرند.
var interpreterNames = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
	"cmd": true, "powershell": true, "pwsh": true,
	"python": true, "python3": true, "node": true, "deno": true, "bun": true,
}

// interpreterPayloadFlags پرچم‌هایی که «آرگومان بعدی» را به‌عنوان کد
// اجرا می‌کنند (بازسازی expandShellPayloads روی argv تجزیه‌شده).
var interpreterPayloadFlags = map[string]bool{
	"-c": true, "-e": true, "--eval": true, "--command": true,
	"-command": true, "-enc": true, "-encodedcommand": true, "/c": true,
}

// interpreterPayloads اگر دستور یک مفسر با پرچمِ payload باشد، متن payload
// را برمی‌گرداند تا بازگشتی و با همان قواعد ارزیابی شود (sh -c '…').
func interpreterPayloads(argv []string) []string {
	if len(argv) < 3 {
		return nil
	}
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(argv[0], "\\", "/")))
	name = strings.TrimSuffix(name, ".exe")
	if !interpreterNames[name] {
		return nil
	}
	for i := 1; i < len(argv)-1; i++ {
		if interpreterPayloadFlags[strings.ToLower(argv[i])] {
			return []string{argv[i+1]}
		}
	}
	return nil
}

// ---------------------------------------------------------------- ارزیابی کامل

// commandEval نتیجه‌ی ارزیابی یک دستور (خالص).
type commandEval struct {
	decision  permDecision
	reason    string
	sensitive bool // ارجاع حساس → مودال بدون گزینه‌ی «همیشه اجازه»
}

// merge بدترین (سخت‌گیرترین) نتیجه را نگه می‌دارد.
func (e *commandEval) merge(o commandEval) {
	if o.decision > e.decision {
		e.decision, e.reason = o.decision, o.reason
	}
	e.sensitive = e.sensitive || o.sensitive
}

// hardBlockReason متن فارسی لیست سیاه سخت.
const hardBlockReason = "این دستور در لیست سیاه دستورات خطرناک MyAI است و هرگز اجرا نمی‌شود."

// evaluateCommandDetailed ارزیابی کامل با تجزیه‌ساز واقعی (تابع خالص).
// workspace خالی یعنی «نامعلوم» → مسیرهای مطلق مجاز شمرده نمی‌شوند.
func evaluateCommandDetailed(cmd string, rules []PermissionRule, sensitiveGlobs []string, workspace string) commandEval {
	var out commandEval
	out.decision = permAllow
	if strings.TrimSpace(cmd) == "" {
		return out
	}
	// ۰) پیش‌فیلتر خام روی کل رشته — الگوهای چندجزئی (‎curl … | sh‎، fork
	//    bomb) حتی وقتی تجزیه سازنده شکست بخورد هم اینجا گرفته می‌شوند.
	if fullNorm := normalizeSegment(cmd); fullNorm != "" {
		for _, re := range hardBlockedPatterns {
			if re.MatchString(fullNorm) {
				out.decision, out.reason = permDeny, hardBlockReason
				return out
			}
		}
	}
	pc, ok := parseShellCommand(cmd, currentCommandFlavor())
	if !ok {
		out.decision, out.reason = permAsk, "ساختار دستور قطعی باز نشد (کوتیشن/سینتکس ناقص)."
		return out
	}
	if len(pc.simples) == 0 {
		return out // فقط فاصله/جداکننده
	}
	for i := range pc.simples {
		out.merge(evaluateSimple(&pc.simples[i], rules, sensitiveGlobs, workspace))
	}
	return out
}

// evaluateSimple ارزیابی یک دستور ساده (شامل payloadهای بازگشتی).
func evaluateSimple(sc *simpleCommand, rules []PermissionRule, globs []string, ws string) commandEval {
	var e commandEval
	e.decision = permAllow
	view := sc.view()
	norm := normalizeSegment(view)

	// ۱) لیست سیاه سخت روی نمای این جزء
	if norm != "" {
		for _, re := range hardBlockedPatterns {
			if re.MatchString(norm) {
				e.decision, e.reason = permDeny, hardBlockReason
				return e
			}
		}
	}
	// ۱٫ب) payload مفسر (‎sh -c '…'‎) و زیرپوسته‌ها — بازگشتی
	for _, payload := range interpreterPayloads(sc.argv) {
		e.merge(evaluateCommandDetailed(payload, rules, globs, ws))
	}
	for _, payload := range sc.subshells {
		if strings.TrimSpace(payload) != "" {
			e.merge(evaluateCommandDetailed(payload, rules, globs, ws))
		}
	}

	// ۲) ارجاع حساس در توکن‌ها یا مقصد تغییرمسیر
	sensGlob := ""
	sensitive := false
	for _, tok := range sc.argv {
		if g, hit := sensitiveCommandRef(tok, globs); hit {
			sensGlob, sensitive = g, true
			break
		}
	}
	if !sensitive {
		for _, r := range sc.redirects {
			if g, hit := sensitiveCommandRef(r.target, globs); hit {
				sensGlob, sensitive = g, true
				break
			}
		}
	}

	// ۳) قاعده‌ی کاربر — اولین تطبیق روی نمای این جزء
	segDecision := permDecision(-1)
	segReason := ""
	var matched *PermissionRule
	if norm != "" {
		for i := range rules {
			if commandGlobMatch(strings.ToLower(strings.TrimSpace(rules[i].Pattern)), norm) {
				matched = &rules[i]
				switch normalizePermissionDecision(matched.Decision) {
				case "allow":
					segDecision = permAllow
				case "deny":
					segDecision, segReason = permDeny,
						"این دستور با قاعده‌ی دسترسی شما («"+matched.Pattern+"») مسدود شده است."
				case "ask":
					segDecision, segReason = permAsk, "اجرای این دستور طبق قاعده‌ی شما تأیید می‌خواهد."
				}
				break
			}
		}
	}
	// ۴) بدون قاعده → پرریسک / لیست‌سفید ساختاری / ask
	if segDecision < 0 {
		segDecision = permAsk
		segReason = ""
		for _, re := range riskyPatterns {
			if re.MatchString(norm) {
				segReason = "این دستور پرریسک است (تغییرات مخرب احتمالی در فایل‌ها یا مخزن)."
				break
			}
		}
		if segReason == "" && simpleIsAutoAllowed(sc, currentCommandFlavor(), ws) {
			segDecision = permAllow
		} else if segReason == "" && hasFileRedirect(sc) {
			segReason = "این دستور با تغییرمسیر، فایل می‌نویسد/می‌خواند (خارج از جریان بازبینی تغییرات)."
		}
	} else if segDecision == permAllow && matched != nil {
		// D4(b) + خطر ساختاری: قاعده‌ی allow نمی‌تواند الگوی پرریسک یا ساختارِ
		// خطرناک (تغییرمسیر فایل/پرچم ممنوع/گسترش/پس‌زمینه) را دور بزند مگر
		// این‌که خودِ متن الگوی قاعده همان توکن را صریح داشته باشد.
		blockReason := ""
		for _, re := range riskyPatterns {
			if re.MatchString(norm) && !re.MatchString(normalizeSegment(matched.Pattern)) {
				blockReason = "این دستور پرریسک است و قاعده‌ی شما («" + matched.Pattern + "») آن را صریح پوشش نمی‌دهد."
				break
			}
		}
		if blockReason == "" {
			ruleText := strings.ToLower(matched.Pattern)
			for _, tok := range structuralRiskTokens(sc, currentCommandFlavor()) {
				if tok != "" && !strings.Contains(ruleText, tok) {
					blockReason = "این دستور با قاعده‌ی شما («" + matched.Pattern + "») مجاز شده اما ساختار خطرناکی (تغییرمسیر/پرچم/گسترش) دارد و تأیید می‌خواهد."
					break
				}
			}
		}
		if blockReason != "" {
			segDecision, segReason = permAsk, blockReason
		}
	}

	// ۵) ارجاع حساس → حداقل ask، بدون گزینه‌ی «همیشه» (۳.۲.۴)
	if sensitive {
		e.sensitive = true
		if segDecision == permAllow {
			segDecision = permAsk
		}
		if segDecision == permAsk {
			if sensGlob != "" {
				segReason = "دسترسی به فایل حساس («" + sensGlob + "») در این دستور."
			} else {
				segReason = "دسترسی به فایل حساس در این دستور."
			}
		}
	}

	if segDecision > e.decision {
		e.decision, e.reason = segDecision, segReason
	}
	return e
}

// hasFileRedirect آیا دستور تغییرمسیرِ فایلِ واقعی (غیر dup/NUL) دارد؟
func hasFileRedirect(sc *simpleCommand) bool {
	for _, r := range sc.redirects {
		if r.target != "" && !isNullDevice(r.target) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- الگوی «همیشه»

// alwaysPatternCurated جفت‌های ابزار/زیرفرمانی که الگوی عام ( wildcard )
// «همیشه اجازه» از آن‌ها مشتق می‌شود (۳.۲.۶-ج). برای هر چیز دیگر فقط
// قاعده‌ی «دستورِ دقیق» پیشنهاد می‌شود — هرگز برای push/reset/clean/rm/
// del/curl/wget/powershell/cmd/sh/bash/python -c/node -e/npx/sudo/chmod/
// kill/taskkill الگوی عام ساخته نمی‌شود (هیچ‌کدام در این فهرست نیستند).
var alwaysPatternCurated = []string{
	"git add", "git commit", "git status", "git diff", "git log", "git fetch",
	"npm install", "npm run", "npm test",
	"go build", "go test", "go vet", "go mod tidy",
	"cargo build", "cargo test", "cargo check",
	"dotnet build", "dotnet test",
	"pip install", "pytest",
	"make", // «make <target>» — نیازمند هدف؛ «make» تنها الگو نمی‌سازد
}

// deriveAlwaysPattern الگوی «همیشه اجازه بده» را از دستور می‌سازد:
// فقط برای جفت‌های تأییدشده‌ی فهرست بالا الگوی عام (‎tool sub*‎)؛ برای
// بقیه، خودِ دستورِ اول به‌صورت «دقیق» (بدون wildcard). الگو همیشه در
// مودال به کاربر نشان داده می‌شود.
func deriveAlwaysPattern(command string) string {
	pc, ok := parseShellCommand(command, currentCommandFlavor())
	var toks []string
	if ok && len(pc.simples) > 0 {
		toks = pc.simples[0].argv
	}
	if len(toks) == 0 {
		toks = strings.Fields(strings.ToLower(strings.TrimSpace(command)))
	}
	for i := range toks {
		toks[i] = strings.ToLower(toks[i])
	}
	if len(toks) == 0 {
		return ""
	}
	best := 0
	for _, entry := range alwaysPatternCurated {
		et := strings.Fields(entry)
		if len(et) == 0 || len(et) > len(toks) {
			continue
		}
		match := true
		for k := range et {
			if et[k] != toks[k] {
				match = false
				break
			}
		}
		if match && len(et) > best {
			best = len(et)
		}
	}
	if best > 0 {
		n := best
		// «make» نیازمند هدف است: الگو جفتِ «make <target>» می‌شود؛
		// «make» تنها (بدون هدف) به قاعده‌ی دقیق می‌رود
		if best == 1 && toks[0] == "make" {
			if len(toks) < 2 {
				return normalizeSegment(strings.Join(toks, " "))
			}
			n = 2
		}
		return strings.Join(toks[:n], " ") + "*"

	}
	// «make» تنها (بدون هدف) هم الگوی عام نمی‌سازد — قاعده‌ی دقیق.
	// دستورِ دقیق: نمای نرمال اولین دستور ساده.
	if norm := normalizeSegment(strings.Join(toks, " ")); norm != "" {
		return norm
	}
	return ""
}
