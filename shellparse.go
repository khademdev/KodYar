// تجزیه‌گر واقعی دستور shell برای دروازه‌ی دسترسی (Stage 1).
//
// چرا؟ الگوی glob روی رشته‌ی نرمال‌شده («echo*») درباره‌ی معنای واقعی
// دستور برای cmd.exe / sh هیچ چیز نمی‌داند: جداکننده‌ی «&»، تغییرمسیرهای
// خروجی، escape کارت، گسترش %VAR% و $(…) همه از دیدش نامرئی‌اند. این
// ماژول دستور را طبق قواعد واقعی هر shell تجزیه می‌کند تا تصمیمِ
// «اجازه‌ی خودکار» روی ساختار (argv + پرچم‌ها + تغییرمسیرها) گرفته شود،
// نه روی یک الگوی متنی.
//
// اصل طراحی: «عدم قطعیت = بپرس». هر چیزی که تجزیه‌گر نتواند با قطعیت
// تفسیر کند (کوتیشن باز، $( بسته‌نشده، heredoc، سینتکس ناقص) → ok=false
// → تصمیم ask؛ هرگز allow نمی‌شود. لایه‌های regex (لیست سیاه/پرریسک)
// به‌عنوان defense-in-depth سر جایشان می‌مانند و روی «نمایِ» بازسازی‌شده
// از argv تجزیه‌شده اجرا می‌شوند (کوتیشن/escape حل شده، پس r^m دیگر
// از دست regex فرار نمی‌کند).
//
// این کد «اجرا» نمی‌کند؛ فقط می‌خواند. اجرای واقعی همچنان همان رشته‌ی
// خام مدل با runShell است — دستورات sandbox نیستند (README: مدل امنیتی).
package main

import (
	"regexp"
	"runtime"
	"strings"
)

// maxParseCommandLength سقف طول دستورِ قابل تجزیه؛ دستور طولانی‌تر از این
// (کپی/پیست خروجی‌ها و…) با قطعیت قابل قضاوت نیست → ask.
const maxParseCommandLength = 65536

// shellFlavor شله‌ی shellای که واقعاً دستور را اجرا می‌کند.
type shellFlavor int

const (
	flavorCmd   shellFlavor = iota // Windows: cmd /S /C (shell.go)
	flavorPOSIX                    // سایر: sh -c
)

// commandFlavorForGOOS نگاشت سیستم‌عامل → شله (تابع خالص، قابل تست).
func commandFlavorForGOOS(goos string) shellFlavor {
	if goos == "windows" {
		return flavorCmd
	}
	return flavorPOSIX
}

// currentCommandFlavor شله‌ی فعال دروازه‌ی دستور — متغیر سطح پکیج تا تست‌ها
// بتوانند هر دو شله را روی هر سیستم‌عاملی اجرا کنند. تست‌ها باید آن را
// بازنشانی کنند (تست‌های این پکیج موازی نیستند؛ قرارداد todosEmit).
var currentCommandFlavor = func() shellFlavor {
	return commandFlavorForGOOS(runtime.GOOS)
}

// ---------------------------------------------------------------- انواع خروجی

// redirect یک تغییرمسیر تجزیه‌شده. op شکل عملگر است (">", ">>", "<", "2>",
// "2>&1", ">&2", …)؛ target کلمه‌ی مقصد (بدون کوتیشن) است و در شکل‌های
// fd-dup مثل «2>&1» خالی است (target خالی + «&» در op = dup بدون فایل).
type redirect struct {
	op     string
	target string
}

// simpleCommand یک دستور ساده در خط مرکب. argv توکن‌های بدون کوتیشن است؛
// متن خامِ گسترش‌ها ($VAR، ${…}، $(…)، %VAR%) داخل توکن حفظ می‌شود تا
// لایه‌ی regex آن را ببیند و payload زیرپوسته‌ها جداگانه ارزیابی شود.
type simpleCommand struct {
	argv         []string
	redirects    []redirect
	hasExpansion bool     // $VAR / ${} / $((…)) / %VAR% / !VAR! / هر % یا ! در cmd
	hasSubshell  bool     // $(…) / backtick / (…) / <(…) / >(…)
	background   bool     // «&» در POSIX (cmd شلِ پس‌زمینه ندارد)
	subshells    []string // متن خام payloadها برای ارزیابی بازگشتی
}

// parsedCommand نتیجه‌ی تجزیه‌ی یک خط کامل. seps[i] جداکننده‌ی بعد از
// simples[i] است (آخرین عنصر «» است) — فقط برای بازسازی/تشخیص.
type parsedCommand struct {
	simples []simpleCommand
	seps    []string
}

// view نمای متنیِ این دستور ساده برای تطبیق الگو/regex: argv به‌همراه
// تغییرمسیرها با فاصله چسبیده‌اند. کوتیشن‌ها و escapeها در argv حل شده‌اند؛
// کاراکترهای گسترش ($، %، پرانتز) عمداً می‌مانند تا normalizeSegment و
// الگوهای لیست سیاه مثل قبل روی همان منظر کار کنند.
func (sc *simpleCommand) view() string {
	parts := make([]string, 0, len(sc.argv)+2*len(sc.redirects))
	parts = append(parts, sc.argv...)
	for _, r := range sc.redirects {
		if r.target != "" {
			parts = append(parts, r.op+r.target)
		} else {
			parts = append(parts, r.op)
		}
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------- ابزارهای مشترک

// cmdVarExpansionRe الگوی گسترش متغیر cmd.exe: ‎%VAR%‎ (حتی داخل کوتیشن)
// و !VAR! (delayed expansion). ‎%%‎ و «!…!» با نام خالی تطبیق نمی‌خورند.
var cmdVarExpansionRe = regexp.MustCompile(`%[A-Za-z_][A-Za-z0-9_-]*%|![A-Za-z_][A-Za-z0-9_-]*!`)

// isAllDigits آیا رشته فقط رقم ASCII و غیرخالی است؟ (پیشوند fd تغییرمسیر)
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isDigitRune رقم ASCII؟
func isDigitRune(c rune) bool { return c >= '0' && c <= '9' }

// isNameRune نویسه‌ی مجاز نام متغیر shell ([A-Za-z0-9_]).
func isNameRune(c rune) bool {
	return c == '_' || isDigitRune(c) || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isShellSpace فاصله‌ی سفید جداکننده‌ی توکن (به‌جز خط جدید که خودش
// جداکننده‌ی دستور است).
func isShellSpace(c rune) bool {
	return c == ' ' || c == '\t' || c == '\v' || c == '\f'
}

// scanBalanced از rs[at] (باید open باشد) تا کاراکتر close «متوازن» را
// می‌خواند و متن داخل پرانتز + ایندکسِ بعد از close را برمی‌گرداند.
// کوتیشن‌ها داخل بازه دیده می‌شوند (تا «)» داخل رشته به‌عنوان بسته‌شدن
// حساب نشود) و در POSIX بک‌اسلشِ بیرون/داخل دابل‌کوت معتبر است.
// ok=false یعنی بازه بسته نشد → کل تجزیه نامطمئن.
func scanBalanced(rs []rune, at int, open, close rune, flavor shellFlavor) (payload string, next int, ok bool) {
	depth := 0
	quote := rune(0)
	for i := at; i < len(rs); i++ {
		c := rs[i]
		if quote != 0 {
			if flavor == flavorPOSIX && quote == '"' && c == '\\' && i+1 < len(rs) {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"':
			quote = '"'
		case '\'':
			if flavor == flavorPOSIX {
				quote = '\''
			}
		case '\\':
			if flavor == flavorPOSIX && i+1 < len(rs) {
				i++
			}
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return string(rs[at+1 : i]), i + 1, true
			}
		}
	}
	return "", 0, false
}

// ---------------------------------------------------------------- خواندن یک کلمه

// scanWord یک کلمهٔ کامل را از rs[start] می‌خواند (کوتیشن‌ها، escapeها،
// گسترش‌ها و زیرپوسته‌ها را حل می‌کند) و در اولین نویسهٔ پایان‌دهنده
// (فاصله، جداکننده، تغییرمسیر، «)» یتیم در POSIX) متوقف می‌شود.
// خروجی: محتوای بدون کوتیشن + پرچم‌های گسترش/زیرپوسته + ایندکس توقف.
// ok=false یعنی ساختار باز (کوتیشن/بک‌تیک بسته نشد) → تجزیه نامطمئن.
//
// نکته: محتوای خالی (مثل ‎""‎) هم یک توکن معتبر است؛ فراخواننده باید
// «پیشرفت ایندکس» را ملاک ثبت توکن بگیرد نه طول محتوا را.
func scanWord(rs []rune, start int, flavor shellFlavor) (content []rune, next int, expand, sub bool, payloads []string, ok bool) {
	i := start
	for i < len(rs) {
		c := rs[i]
		switch {
		case c == '"': // هر دو شله
			j := i + 1
			for j < len(rs) {
				if rs[j] == '"' {
					if flavor == flavorCmd && j+1 < len(rs) && rs[j+1] == '"' {
						content = append(content, '"') // cmd: "" داخل کوتیشن = "
						j += 2
						continue
					}
					break
				}
				if flavor == flavorPOSIX && rs[j] == '\\' && j+1 < len(rs) {
					switch rs[j+1] {
					case '"', '\\', '$', '`':
						content = append(content, rs[j+1])
						j += 2
						continue
					case '\n':
						j += 2 // ادامه‌ی خط داخل کوتیشن
						continue
					}
					content = append(content, rs[j])
					j++
					continue
				}
				if flavor == flavorPOSIX && rs[j] == '$' {
					var np []string
					var ni int
					var nok, nexp, nsub bool
					content, ni, nexp, nsub, np, nok = scanDollar(rs, j, content)
					if !nok {
						return nil, 0, false, false, nil, false
					}
					expand, sub = expand || nexp, sub || nsub
					payloads = append(payloads, np...)
					j = ni
					continue
				}
				if flavor == flavorPOSIX && rs[j] == '`' {
					k := j + 1
					for k < len(rs) && rs[k] != '`' {
						k++
					}
					if k >= len(rs) {
						return nil, 0, false, false, nil, false
					}
					payloads = append(payloads, string(rs[j+1:k]))
					content = append(content, rs[j:k+1]...)
					sub = true
					j = k + 1
					continue
				}
				if flavor == flavorCmd && (rs[j] == '%' || rs[j] == '!') {
					expand = true // گسترش متغیر cmd حتی داخل کوتیشن
				}
				content = append(content, rs[j])
				j++
			}
			if j >= len(rs) {
				return nil, 0, false, false, nil, false // کوتیشن باز
			}
			i = j + 1

		case c == '\'' && flavor == flavorPOSIX:
			j := i + 1
			for j < len(rs) && rs[j] != '\'' {
				j++
			}
			if j >= len(rs) {
				return nil, 0, false, false, nil, false
			}
			content = append(content, rs[i+1:j]...)
			i = j + 1

		case c == '\\' && flavor == flavorPOSIX:
			if i+1 < len(rs) {
				if rs[i+1] == '\n' {
					i += 2 // ادامه‌ی خط — کلمه ادامه می‌یابد
					continue
				}
				content = append(content, rs[i+1])
				i += 2
				continue
			}
			content = append(content, '\\')
			i++

		case c == '^' && flavor == flavorCmd:
			if i+1 < len(rs) {
				if rs[i+1] == '\n' {
					i += 2 // ^ در انتهای خط = ادامه
					continue
				}
				content = append(content, rs[i+1])
				i += 2
				continue
			}
			content = append(content, '^')
			i++

		case c == '$' && flavor == flavorPOSIX:
			var np []string
			var ni int
			var nok, nexp, nsub bool
			content, ni, nexp, nsub, np, nok = scanDollar(rs, i, content)
			if !nok {
				return nil, 0, false, false, nil, false
			}
			expand, sub = expand || nexp, sub || nsub
			payloads = append(payloads, np...)
			i = ni

		case c == '`' && flavor == flavorPOSIX:
			k := i + 1
			for k < len(rs) && rs[k] != '`' {
				k++
			}
			if k >= len(rs) {
				return nil, 0, false, false, nil, false
			}
			payloads = append(payloads, string(rs[i+1:k]))
			content = append(content, rs[i:k+1]...)
			sub = true
			i = k + 1

		case c == '(':
			// زیرپوسته/گروه در هر دو شله — محتوای بازگشتی ارزیابی می‌شود.
			payload, nxt, bok := scanBalanced(rs, i, '(', ')', flavor)
			if !bok {
				return nil, 0, false, false, nil, false
			}
			payloads = append(payloads, payload)
			content = append(content, rs[i:nxt]...)
			sub = true
			i = nxt

		case c == ')' && flavor == flavorPOSIX:
			// «)» یتیم در سطح بالا = خطای سینتکس sh؛ کلمه اینجا تمام می‌شود
			// و حلقه‌ی اصلی تجزیه را نامطمئن می‌کند.
			return content, i, expand, sub, payloads, true

		case isShellSpace(c) || c == '\n' || c == '\r':
			return content, i, expand, sub, payloads, true

		case c == '&' || c == '|' || (c == ';' && flavor == flavorPOSIX):
			return content, i, expand, sub, payloads, true

		case c == '<' || c == '>':
			return content, i, expand, sub, payloads, true

		default:
			if flavor == flavorCmd && (c == '%' || c == '!') {
				expand = true // محافظه‌کارانه: هر % یا ! در cmd
			}
			content = append(content, c)
			i++
		}
	}
	return content, len(rs), expand, sub, payloads, true
}

// scanDollar گسترش‌های ‎$‎ را از rs[i] (خودِ ‎$‎) می‌خواند و «متن خام» را
// به content می‌چسباند (تا لایه‌ی regex همان را ببیند):
//
//	$(cmd)  → sub=true + payload بازگشتی
//	$((…))  → expand؛ payload محاسباتی هم «محافظه‌کارانه» به‌عنوان زیرپوسته
//	          ارزیابی می‌شود (در شل واقعی اجرا نمی‌شود اما ارزیابی اضافه
//	          ضرری ندارد و در برابر شل‌های خاص سخت‌گیرتر است)
//	${…}    → expand (بازه‌ی متوازن {})
//	$NAME   → expand
//	$$ $? … → expand (دو نویسه)
//	$"…" $'…' → expand؛ خود کوتیشن در تکرار بعدی حل می‌شود
func scanDollar(rs []rune, i int, content []rune) (out []rune, next int, expand, sub bool, payloads []string, ok bool) {
	out = content
	if i+1 >= len(rs) {
		return append(out, '$'), i + 1, true, false, nil, true
	}
	switch rs[i+1] {
	case '(':
		if i+2 < len(rs) && rs[i+2] == '(' {
			// ‎$(( … ))‎ محاسباتی — از دومین پرانتز متوازن می‌خوانیم
			payload, nxt, bok := scanBalanced(rs, i+2, '(', ')', flavorPOSIX)
			if !bok || nxt >= len(rs) || rs[nxt] != ')' {
				return nil, 0, false, false, nil, false
			}
			return append(out, rs[i:nxt+1]...), nxt + 1, true, true, []string{payload}, true
		}
		payload, nxt, bok := scanBalanced(rs, i+1, '(', ')', flavorPOSIX)
		if !bok {
			return nil, 0, false, false, nil, false
		}
		return append(out, rs[i:nxt]...), nxt, true, true, []string{payload}, true
	case '{':
		payload, nxt, bok := scanBalanced(rs, i+1, '{', '}', flavorPOSIX)
		if !bok {
			return nil, 0, false, false, nil, false
		}
		_ = payload
		return append(out, rs[i:nxt]...), nxt, true, false, nil, true
	case '\'', '"':
		// ‎$'…'‎ / ‎$"…"‎ — کوتیشن در تکرار بعدی scanWord خوانده می‌شود
		return append(out, '$'), i + 1, true, false, nil, true
	default:
		if isNameRune(rs[i+1]) {
			j := i + 1
			for j < len(rs) && isNameRune(rs[j]) {
				j++
			}
			return append(out, rs[i:j]...), j, true, false, nil, true
		}
		// ‎$$‎ ‎$?‎ ‎$!‎ … — نویسه‌ی خاص همراه $
		return append(out, rs[i:i+2]...), i + 2, true, false, nil, true
	}
}

// ---------------------------------------------------------------- تجزیه‌گر اصلی

// parseShellCommand دستور را طبق شله‌ی داده‌شده تجزیه می‌کند.
//
// قواعد پیاده‌شده:
//   - cmd: فقط کوتیشن دابل (‎""…""‎ = کوتیشن literal)؛ caret بیرون کوتیشن
//     escape است؛ «%VAR%»/«!VAR!» حتی داخل کوتیشن گسترش می‌یابد؛ جداکننده‌ها:
//     && || | & و خط جدید. «;» در cmd جداکننده نیست (نویسه‌ی عادی است —
//     برخلاف تقسیم‌کننده‌ی قبلی که اشتباهاً «;» را هم می‌شکست).
//   - POSIX: ' و " و بک‌اسلش؛ $(…) و backtick و (…) و <(…)/>(…)؛ ${} و
//     $VAR و $((…))؛ جداکننده‌ها: && || | |& ; & و خط جدید؛ «&» = پس‌زمینه.
//   - تغییرمسیرها: < > >> n> 2>&1 >&n & در op — مقصد به‌صورت کلمه‌ی
//     کوتیشن‌آگاه خوانده می‌شود. heredoc (<<) → عدم قطعیت → ok=false.
//   - هر سینتکس مبهم/ناقص → ok=false (تصمیم ask، هرگز allow).
//
// توجه: خطاهای سینتکسیِ «بی‌خطر» (دستور خالی بین جداکننده‌ها، جداکننده‌ی
// پایانی) سخت‌گیرانه اما بدون شکست جمع می‌شوند — چون تصمیم نهایی
// «سخت‌گیرترینِ» دستورهای ساده است و اجرای واقعی با خطای sh خودش
// fail-closed می‌شود؛ فقط ساختارهای «باز» (مرز نامشخص) شکست می‌خورند.
func parseShellCommand(cmd string, flavor shellFlavor) (parsedCommand, bool) {
	var pc parsedCommand
	if len(cmd) > maxParseCommandLength {
		return pc, false
	}
	rs := []rune(cmd)
	n := len(rs)

	cur := &simpleCommand{}
	var tok []rune
	tokOpen := false // توکنی در حال ساخت است (حتی خالی، مثل «""»)
	tokExpand, tokSub := false, false
	var tokPayloads []string

	curNonEmpty := func() bool { return len(cur.argv) > 0 || len(cur.redirects) > 0 }

	flushTok := func() {
		if tokOpen || len(tok) > 0 {
			cur.argv = append(cur.argv, string(tok))
			if tokExpand {
				cur.hasExpansion = true
			}
			if tokSub {
				cur.hasSubshell = true
			}
			cur.subshells = append(cur.subshells, tokPayloads...)
			tok = tok[:0]
			tokOpen, tokExpand, tokSub = false, false, false
			tokPayloads = nil
		}
	}
	// endSimple دستور جاری را می‌بندد؛ bg فقط در POSIX برای «&» معنا دارد.
	endSimple := func(sep string, bg bool) {
		flushTok()
		if curNonEmpty() {
			if bg {
				cur.background = true
			}
			pc.simples = append(pc.simples, *cur)
			pc.seps = append(pc.seps, sep)
			cur = &simpleCommand{}
		}
		// دستور خالی بین جداکننده‌ها: این جداکننده بی‌اثر جمع می‌شود؛
		// تصمیم‌گیری per-simple است پس معنا عوض نمی‌شود.
	}

	i := 0
	for i < n {
		c := rs[i]
		switch {
		case isShellSpace(c):
			flushTok()
			i++
		case c == '\r':
			flushTok() // \r تنها؛ حالت \r\n در case بعدی با \n گرفته می‌شود
			i++
		case c == '\n':
			flushTok()
			endSimple("\n", false)
			i++
		case c == '&' && i+1 < n && rs[i+1] == '&':
			flushTok()
			endSimple("&&", false)
			i += 2
		case c == '&' && flavor == flavorPOSIX && i+1 < n && rs[i+1] == '>':
			// ‎cmd &> file‎ (bashism) — محافظه‌کارانه: پایان دستور با bg؛
			// «>» در تکرار بعدی تغییرمسیرِ دستور بعدی (خالی) می‌شود.
			flushTok()
			endSimple("&", true)
			i++
		case c == '&':
			flushTok()
			endSimple("&", flavor == flavorPOSIX)
			i++
		case c == '|' && i+1 < n && rs[i+1] == '|':
			flushTok()
			endSimple("||", false)
			i += 2
		case c == '|' && i+1 < n && rs[i+1] == '&' && flavor == flavorPOSIX:
			flushTok()
			endSimple("|&", false)
			i += 2
		case c == '|':
			flushTok()
			endSimple("|", false)
			i++
		case c == ';' && flavor == flavorPOSIX:
			flushTok()
			endSimple(";", false)
			i++
		case c == '#' && flavor == flavorPOSIX && !tokOpen && len(tok) == 0:
			// توضیح تا پایان خط (فقط در آغاز کلمه — ‎foo#bar‎ literal است)
			for i < n && rs[i] != '\n' {
				i++
			}
		case (c == '^' && flavor == flavorCmd || c == '\\' && flavor == flavorPOSIX) && i+1 < n && rs[i+1] == '\n' && !tokOpen && len(tok) == 0:
			// ادامه‌ی خط در آغاز کلمه — بدون ساخت توکن خالی رد می‌شود
			// (ادامه‌ی وسط کلمه داخل scanWord handled می‌شود)
			i += 2
		case c == '(':
			// زیرپوسته/گروه در آغاز توکن — متن خام داخل توکن می‌ماند
			payload, nxt, bok := scanBalanced(rs, i, '(', ')', flavor)
			if !bok {
				return parsedCommand{}, false
			}
			tok = append(tok, rs[i:nxt]...)
			tokOpen, tokSub = true, true
			tokPayloads = append(tokPayloads, payload)
			i = nxt
		case c == ')' && flavor == flavorPOSIX:
			return parsedCommand{}, false // «)» یتیم
		case c == '<' || c == '>':
			// جایگذاری پروسه (POSIX) — ‎<(cmd)‎ / ‎>(cmd)‎
			if flavor == flavorPOSIX && i+1 < n && rs[i+1] == '(' {
				payload, nxt, bok := scanBalanced(rs, i+1, '(', ')', flavor)
				if !bok {
					return parsedCommand{}, false
				}
				tok = append(tok, rs[i:nxt]...)
				tokOpen, tokSub = true, true
				tokPayloads = append(tokPayloads, payload)
				i = nxt
				continue
			}
			// تغییرمسیر — پیشوند fd؟ (توکن فعلی فقط رقم باشد: ‎2>‎)
			fd := ""
			if tokOpen && isAllDigits(string(tok)) {
				fd = string(tok)
				tok = tok[:0]
				tokOpen = false
			} else {
				flushTok()
			}
			j := i + 1
			op := string(c)
			if j < n && rs[j] == c {
				op += string(c) // >> یا <<
				j++
			}
			if op == "<<" {
				return parsedCommand{}, false // heredoc — عدم قطعیت
			}
			if j < n && rs[j] == '&' {
				// شکل fd-dup مثل ‎2>&1‎ / ‎>&2‎ — رقم‌ها بخشی از op می‌شوند
				k := j + 1
				for k < n && isDigitRune(rs[k]) {
					k++
				}
				if k == j+1 {
					return parsedCommand{}, false // «&» بدون رقم — مبهم
				}
				if k < n && !(isShellSpace(rs[k]) || rs[k] == '\n' || rs[k] == '\r' ||
					rs[k] == '&' || rs[k] == '|' || rs[k] == ';' || rs[k] == '<' || rs[k] == '>') {
					return parsedCommand{}, false // رقم چسبیده به کلمه — مبهم
				}
				cur.redirects = append(cur.redirects, redirect{op: fd + op + "&" + string(rs[j+1:k])})
				i = k
				continue
			}
			// مقصد: یک کلمه (کوتیشن‌آگاه)
			for j < n && isShellSpace(rs[j]) {
				j++
			}
			if j >= n || rs[j] == '\n' || rs[j] == '\r' || rs[j] == '&' || rs[j] == '|' ||
				(flavor == flavorPOSIX && rs[j] == ';') || rs[j] == '<' || rs[j] == '>' {
				return parsedCommand{}, false // مقصد غایب
			}
			tcontent, tnext, texp, tsub, tpay, tok2 := scanWord(rs, j, flavor)
			if !tok2 || tnext == j {
				return parsedCommand{}, false
			}
			cur.redirects = append(cur.redirects, redirect{op: fd + op, target: string(tcontent)})
			if texp {
				cur.hasExpansion = true
			}
			if tsub {
				cur.hasSubshell = true
				cur.subshells = append(cur.subshells, tpay...)
			}
			i = tnext
		default:
			wcontent, wnext, wexp, wsub, wpay, wok := scanWord(rs, i, flavor)
			if !wok {
				return parsedCommand{}, false
			}
			tok = append(tok, wcontent...)
			tokOpen = true
			tokExpand, tokSub = tokExpand || wexp, tokSub || wsub
			tokPayloads = append(tokPayloads, wpay...)
			i = wnext
		}
	}
	flushTok()
	if curNonEmpty() {
		pc.simples = append(pc.simples, *cur)
		pc.seps = append(pc.seps, "")
	}
	return pc, true
}
