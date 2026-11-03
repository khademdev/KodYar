// سیستم دسترسی (Permissions) — دروازه‌ی واحد ابزارها و دستورات.
//
// مدل: هر ابزار به یک تصمیم allow | ask | deny نگاشت می‌شود؛ run_command
// علاوه بر آن قاعده‌های الگویی مرتب (اولین تطبیق برنده) دارد.
//
// ترتیب ارزیابی run_command (طبق مشخصات؛ سخت‌گیرترین بخشِ دستور مرکب برنده):
// 1. لیست سیاه سخت (hardBlockedPatterns) — آخرین خط دفاع؛ هرگز با قاعده
// قابل دورزدن نیست.
// 2. قاعده‌های دسترسی کاربر (روی «هر جزءِ» دستور، نه کل رشته — تا
// «git status*» نتواند «git status && rm -rf ~» را تأیید کند).
// 3. الگوهای پرریسک (رiskyPatterns → تأیید) و در نبود آن‌ها لیست‌سفید
// امن → allow؛ در غیر این صورت ask (پیش‌فرض جدید به‌جای «همه مجاز»).
//
// صداقت امنیتی: این لایه defense-in-depth است و جایگزین sandbox نیست —
// نرمال‌سازی regex-محور با هدفهندی می‌تواند دور زده شود؛ اجرای واقعی همچنان
// در پوشه‌ی کاری و با SAME رشته‌ای است که ارزیابی شده (تقسیم quote-آگاه
// پیش از نرمال‌سازی انجام می‌شود تا معنای shell حفظ شود).
package main

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// permDecision تصمیم دسترسی برای یک ابزار/دستور.
type permDecision int

const (
	permAllow permDecision = iota // مجاز
	permAsk                       // نیاز به تأیید کاربر
	permDeny                      // رد
)

// PermissionRule یک قاعده‌ی الگویی دستور (glob روی دستورِ نرمال‌شده‌ی هر جزء).
type PermissionRule struct {
	Pattern  string `json:"pattern"`  // مثل "git status*" یا "cargo build*"
	Decision string `json:"decision"` // allow | ask | deny
}

// PermissionSettings بخش تنظیمات مربوط به دسترسی‌ها.
// مقادیر خالی = پیش‌فرض‌های امن (defaultPermissions).
type PermissionSettings struct {
	Tools          map[string]string `json:"tools,omitempty"`          // نام ابزار → allow|ask|deny
	CommandRules   []PermissionRule  `json:"commandRules,omitempty"`   // مرتب؛ اولین تطبیق برنده
	SensitiveGlobs []string          `json:"sensitiveGlobs,omitempty"` // الگوهای نام فایل حساس (basename glob)
}

// defaultSensitiveGlobs فایل‌هایی که خواندنشان تأیید می‌خواهد.
var defaultSensitiveGlobs = []string{
	".env*", "*.pem", "*.key", "id_rsa*", "*credentials*", "*.pfx",
	".npmrc", ".netrc",
}

// defaultSafeCommands لیست‌سفید امن پیش‌فرض (الگو glob روی جزءِ نرمال‌شده).
// عمداً کوچک و فقط-خواندنی/بی‌خطر نگه داشته شده است.
var defaultSafeCommands = []string{
	"git status*", "git diff*", "git log*", "git show*",
	"go test*", "go build*", "go vet*", "go version",
	"npm test*", "npm run test*", "npx --no-install*",
	"ls*", "dir*", "cat*", "type*",
	"echo*", "pwd", "true", "false",
	"head*", "tail*", "wc*", "which*", "whoami", "hostname",
	"node --version", "python* --version", "git --version", "go version",
	"cargo --version", "rustc --version", "java -version",
}

// normalizePermissionDecision مقدار رشته‌ای تصمیم را اعتبارسنجی می‌کند.
func normalizePermissionDecision(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "allow":
		return "allow"
	case "deny":
		return "deny"
	case "ask":
		return "ask"
	}
	return ""
}

// toolDefaultDecision تصمیم پیش‌فرض هر ابزار (بدون override کاربر).
// عمداً رفتار موجود را بازتولید می‌کند: ابزارهای فقط-خواندنی مجازند؛
// ابزارهای تغییردهنده از سوییچ «تأیید تغییرات» پیروی می‌کنند (جریان diff-confirm
// همان «ask» است)؛ run_command از دروازه‌ی دستور می‌گذرد.
func toolDefaultDecision(tool string, confirmWrites bool) permDecision {
	switch tool {
	case "list_dir", "read_file", "search_files", "glob", "create_dir",
		"git_status", "git_diff", "git_log", "git_show":
		return permAllow
	case "write_file", "edit_file", "move_path", "delete_path":
		if confirmWrites {
			return permAsk // مودال diff-confirm موجود همین نقش را دارد
		}
		return permAllow
	case "run_command":
		return permAsk // دروازه‌ی دستور (لیست‌سفید/قواعد) تصمیم نهایی را می‌دهد
	case "task":
		// — زیرایجنت فقط‌خواندنی است ولی چند فراخوانی مدل و مصرف توکن
		// دارد؛ پیش‌فرض محتاطانه «بپرس» (با گزینه‌ی همیشه‌اجازه در همان مودال).
		return permAsk
	case "todo_write":
		// چک‌لیست کارهای ایجنت فقط وضعیت داخل برنامه را عوض می‌کند — نه
		// فایلی می‌نویسد، نه دستوری اجرا می‌کند؛ نیازی به تأیید نیست.
		return permAllow
	case "web_fetch":
		// درخواست به بیرون از ماشین کاربر است — هر URL با تأیید صریح
		// (مودال، آدرس با ایزوله‌سازی bidi)؛ «همیشه اجازه» مثل بقیه ممکن است.
		return permAsk
	}
	// ابزارهای MCP (mcp_<server>_<tool>) — هر فراخوانی با تأیید صریح؛
	// «همیشه اجازه» از طریق همین سیستم override/تب دسترسی‌ها ممکن است.
	if strings.HasPrefix(tool, "mcp_") {
		return permAsk
	}
	// ابزارهای سفارشی: سوییچ Confirm خودشان جریان ask را می‌سازد
	if strings.HasPrefix(tool, "custom_") {
		return permAllow
	}
	return permAsk // ابزار ناشناخته: محتاطانه بپرس
}

// toolPermDecision تصمیم نهایی ابزار با اعمال override کاربر.
// باید با a.mu قفل‌شده صدا زده شود.
func (a *App) toolPermDecisionLocked(tool string) permDecision {
	d := toolDefaultDecision(tool, a.settings.ConfirmFileWrites)
	if v, ok := a.settings.Permissions.Tools[tool]; ok {
		switch normalizePermissionDecision(v) {
		case "allow":
			d = permAllow
		case "ask":
			d = permAsk
		case "deny":
			d = permDeny
		}
	}
	return d
}

// ---------------------------------------------------------------- تجزیه‌ی دستور مرکب

// splitCommandSegments دستور را به اجزای quote-آگاه تقسیم می‌کند.
// جداکننده‌ها بیرون از کوتیشن: && || ; | و خط جدید.
// محتوای $(...) و backtick جداگانه استخراج و به‌صورت بازگشتی تجزیه می‌شود
// (تا $(echo a; rm -rf /) هم ارزیابی شود).
// ok=false یعنی کوتیشن‌ها متوازن نیستند (→ ask).
// توجه: نرمال‌سازی بعدی برای «تطبیق» است؛ حتی الگوهای داخل کوتیشن هم
// دیده می‌شوند (محافظه‌کارانه — ممکن است false-positive بدهد، هرگز
// false-negative نمی‌دهد).
func splitCommandSegments(cmd string) ([]string, bool) {
	var segs []string
	var cur strings.Builder
	var quote byte // 0 یا '"' یا '\''
	subDepth := 0  // عمق $(...) یا (...) بیرون از کوتیشن
	var subCur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			segs = append(segs, s)
		}
		cur.Reset()
	}
	appendSub := func(content string) bool {
		subSegs, ok := splitCommandSegments(content)
		if !ok {
			return false
		}
		segs = append(segs, subSegs...)
		return true
	}
	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if subDepth > 0 {
			switch {
			case c == '(':
				subDepth++
			case c == ')':
				subDepth--
				if subDepth == 0 {
					content := subCur.String()
					subCur.Reset()
					if !appendSub(content) {
						return nil, false
					}
					continue
				}
			}
			subCur.WriteRune(c)
			continue
		}
		if quote != 0 {
			if c == '\\' && quote == '"' && i+1 < len(runes) {
				cur.WriteRune(c)
				i++
				cur.WriteRune(runes[i])
				continue
			}
			if byte(c) == quote {
				quote = 0
			}
			cur.WriteRune(c)
			continue
		}
		switch {
		case c == '"' || c == '\'':
			quote = byte(c)
			cur.WriteRune(c)
		case c == '`':
			// محتوای backtick تا backtick بعدی
			j := i + 1
			for j < len(runes) && runes[j] != '`' {
				subCur.WriteRune(runes[j])
				j++
			}
			if j >= len(runes) {
				return nil, false // backtick بسته نشد
			}
			if !appendSub(subCur.String()) {
				return nil, false
			}
			subCur.Reset()
			i = j
		case c == '(':
			// فرمان‌جایگزینی $(...) و زیرپوسته‌ی لخت (...) — هر دو محتوایشان
			// جداگانه ارزیابی می‌شوند
			subDepth++
		case c == '&' && i+1 < len(runes) && runes[i+1] == '&':
			flush()
			i++
		case c == '|' && i+1 < len(runes) && runes[i+1] == '|':
			flush()
			i++
		case c == ';' || c == '|' || c == '\n' || c == '\r':
			flush()
		default:
			cur.WriteRune(c)
		}
	}
	if quote != 0 || subDepth > 0 {
		return nil, false
	}
	flush()
	return segs, true
}

// shellPayloadRegex تشخیص مفسر/زبان با پرچم اجرای کد و دریافت payload.
var shellPayloadRegex = regexp.MustCompile(
	`(?i)^(sh|bash|zsh|dash|ksh|cmd|powershell|pwsh|python3?|python\.[0-9]+|node|deno|bun)(\s+(-[^\s]+|/[^\s]+|-[^\s]*))*\s+(-c|-e|-command|-enc|-encodedcommand|--command|--eval)\s+(.+)$`)

// expandShellPayloads اجزای «مفسر -c payload» را به payload خام گسترش می‌دهد.
// هم جزء اصلی و هم payload برگردانده می‌شود (هر دو ارزیابی می‌شوند).
func expandShellPayloads(seg string) []string {
	out := []string{seg}
	// توکن‌های ابتدایی را جدا می‌کنیم (بدون شکستن کوتیشن payload)
	trimmed := strings.TrimSpace(seg)
	m := shellPayloadRegex.FindStringSubmatch(trimmed)
	if m != nil {
		payload := strings.TrimSpace(m[5])
		// حذف کوتیشن سرتاسری payload
		if len(payload) >= 2 &&
			((payload[0] == '"' && payload[len(payload)-1] == '"') ||
				(payload[0] == '\'' && payload[len(payload)-1] == '\'')) {
			payload = payload[1 : len(payload)-1]
		}
		if payload != "" {
			out = append(out, payload)
		}
		return out
	}
	// cmd /c payload (پرچم بدون خط تیره)
	if lt := strings.ToLower(trimmed); strings.HasPrefix(lt, "cmd ") {
		rest := strings.TrimSpace(trimmed[len("cmd"):])
		if strings.HasPrefix(strings.ToLower(rest), "/c ") {
			payload := strings.TrimSpace(rest[len("/c "):])
			if payload != "" {
				out = append(out, payload)
			}
		}
	}
	return out
}

// normalizeSegment نرمال‌سازی برای «تطبیق الگو» (نه اجرا):
// حروف کوچک، ${IFS}/$IFS → فاصله، حذف escape/کوتیشن/$/backtick
// (مقاوم‌سازی در برابر r""m، rm${IFS}-rf و...) و جمع‌کردن فاصله‌ها.
// پرانتزها حفظ می‌شوند — الگوی fork bomb به آن‌ها نیاز دارد؛ محتوای
// فرمان‌جایگزینی از قبل توسط splitCommandSegments جزءِ جدا شده است.
func normalizeSegment(seg string) string {
	s := strings.ToLower(seg)
	s = strings.ReplaceAll(s, "${ifs}", " ")
	s = strings.ReplaceAll(s, "$ifs", " ")
	for _, ch := range []string{"\\", "\"", "'", "$", "`"} {
		s = strings.ReplaceAll(s, ch, "")
	}
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.Join(strings.Fields(s), " ")
	return s
}

// commandGlobMatch تطبیق الگوی قاعده با رشته‌ی نرمال‌شده (* و ? فقط).
func commandGlobMatch(pattern, s string) bool {
	// الگوهای کاربر ساده‌اند؛ فقط * پشتیبانی می‌شود (مثل خود توضیح تنظیمات)
	px, sx := 0, 0
	starPx, starSx, nextPx := -1, -1, 0
	for sx < len(s) {
		if px < len(pattern) {
			switch pattern[px] {
			case '*':
				starPx, starSx = px, sx
				nextPx = px + 1
				px++
				continue
			case '?':
				px++
				sx++
				continue
			default:
				if pattern[px] == s[sx] {
					px++
					sx++
					continue
				}
			}
		}
		if starPx >= 0 {
			starSx++
			sx = starSx
			px = nextPx
			continue
		}
		return false
	}
	for px < len(pattern) && pattern[px] == '*' {
		px++
	}
	return px == len(pattern)
}

// evaluateCommandPermissions ارزیابی کامل دستور (تابع خالص — قابل تست).
// ترتیب برای «هر جزء/پایلود»: ۱) لیست سیاه سخت (بازگشت فوری، هرگز قابل
// override نیست) ۲) قاعده‌ی کاربر — اولین تطبیق، تصمیم نهایی همان جزء
// (allow صریح، الگوی پرریسک را هم دور می‌زند؛ انتخاب آگاهانه‌ی کاربر)
// ۳) بدون قاعده: پرریسک → ask؛ لیست‌سفید امن → allow؛ در غیر این صورت ask.
// سخت‌گیرترین تصمیم بین اجزا برنده می‌شود.
// خروجی: تصمیم + دلیل فارسی برای deny/ask.
func evaluateCommandPermissions(cmd string, rules []PermissionRule) (permDecision, string) {
	segs, ok := splitCommandSegments(cmd)
	if !ok {
		return permAsk, "" // کوتیشن نامتوازن → بپرس
	}
	if len(segs) == 0 {
		return permAllow, ""
	}
	const hardBlockText = "این دستور در لیست سیاه دستورات خطرناک MyAI است و هرگز اجرا نمی‌شود."
	// پیش‌فیلتر: لیست سیاه روی «کل رشته‌ی» نرمال‌شده هم اعمال می‌شود —
	// الگوهای چندجزئی مثل «curl … | sh» با تقسیم اجزا از دست نمی‌روند.
	if fullNorm := normalizeSegment(cmd); fullNorm != "" {
		for _, re := range hardBlockedPatterns {
			if re.MatchString(fullNorm) {
				return permDeny, hardBlockText
			}
		}
	}
	worst := permAllow
	reason := ""
	for _, seg := range segs {
		for _, s := range expandShellPayloads(seg) {
			norm := normalizeSegment(s)
			if norm == "" {
				continue
			}
			// ۱) لیست سیاه سخت
			for _, re := range hardBlockedPatterns {
				if re.MatchString(norm) {
					return permDeny, hardBlockText
				}
			}
			// ۲) قاعده‌ی کاربر — اولین تطبیق، تصمیم نهایی همین جزء
			segDecision := permDecision(-1)
			segReason := ""
			for _, r := range rules {
				if commandGlobMatch(strings.ToLower(strings.TrimSpace(r.Pattern)), norm) {
					switch normalizePermissionDecision(r.Decision) {
					case "allow":
						segDecision = permAllow
					case "deny":
						segDecision = permDeny
						segReason = "این دستور با قاعده‌ی دسترسی شما («" + r.Pattern + "») مسدود شده است."
					case "ask":
						segDecision = permAsk
						segReason = "اجرای این دستور طبق قاعده‌ی شما تأیید می‌خواهد."
					}
					break
				}
			}
			// ۳) بدون قاعده → پرریسک / لیست‌سفید / ask
			if segDecision < 0 {
				segDecision = permAsk
				segReason = ""
				for _, re := range riskyPatterns {
					if re.MatchString(norm) {
						segReason = "این دستور پرریسک است (تغییرات مخرب احتمالی در فایل‌ها یا مخزن)."
						break
					}
				}
				if segReason == "" {
					for _, p := range defaultSafeCommands {
						if commandGlobMatch(p, norm) {
							segDecision = permAllow
							break
						}
					}
				}
			}
			if segDecision > worst {
				worst = segDecision
				reason = segReason
			}
		}
	}
	return worst, reason
}

// deriveAlwaysPattern الگوی «همیشه اجازه بده» را از دستور می‌سازد:
// دو توکن اول + «*» (مثل «cargo build*»). در مودال به کاربر نشان داده می‌شود.
func deriveAlwaysPattern(command string) string {
	norm := normalizeSegment(strings.TrimSpace(command))
	fields := strings.Fields(norm)
	if len(fields) >= 2 {
		return fields[0] + " " + fields[1] + "*"
	}
	if len(fields) == 1 {
		return fields[0] + "*"
	}
	return "*"
}

// ---------------------------------------------------------------- دروازه‌ی اجرا

// evalCommandPerm ارزیابی دستور با قاعده‌های فعلی (a.mu کوتاه گرفته می‌شود).
func (a *App) evalCommandPerm(command string) (permDecision, string) {
	a.mu.Lock()
	rules := make([]PermissionRule, len(a.settings.Permissions.CommandRules))
	copy(rules, a.settings.Permissions.CommandRules)
	a.mu.Unlock()
	return evaluateCommandPermissions(command, rules)
}

// gateCommand دروازه‌ی واحد اجرای دستور — هم برای ابزار ایجنت، هم RunCommand
// و هم ابزارهای سفارشی. forceAsk برای ابزارهای سفارشیِ دارای Confirm است.
// rejectMsg: پیام «رد شدن به‌دست کاربر» — انگلیسی/فنی برای مدل، فارسی برای UI.
// خروجی: (مجاز؟, پیام خطا با پیشوند ERROR:).
func (a *App) gateCommand(ctx context.Context, command string, forceAsk bool, rejectMsg string) (bool, string) {
	decision, why := a.evalCommandPerm(command)
	if forceAsk && decision == permAllow {
		decision = permAsk
	}
	switch decision {
	case permDeny:
		return false, "ERROR: " + why
	case permAsk:
		if !a.requestCommandPermission(ctx, command) {
			return false, "ERROR: " + rejectMsg
		}
	}
	return true, ""
}

// ---------------------------------------------------------------- تأیید تعاملی دسترسی

// پیام‌های تصمیم مودال دسترسی (از فرانت‌اند)
const (
	permRespondAllow  = "allow"  // یک‌بار
	permRespondAlways = "always" // همیشه برای این الگو
	permRespondReject = "reject" // رد
)

// permissionAsk یک درخواست تأیید دسترسی در انتظار پاسخ کاربر.
type permissionAsk struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"` // command | sensitive_read
	Command string `json:"command,omitempty"`
	Pattern string `json:"pattern,omitempty"` // الگوی «همیشه اجازه» (فقط kind=command)
	Path    string `json:"path,omitempty"`    // فایل حساس (فقط kind=sensitive_read)
	URL     string `json:"url,omitempty"`     // آدرس درخواستی (فقط ابزار web_fetch) — نمایش با ایزوله‌سازی bidi
	Message string `json:"message"`           // فارسی — نمایش به کاربر
	At      int64  `json:"at"`
}

// requestPermissionConfirm رویداد agent:permission_ask را می‌فرستد و منتظر
// پاسخ کاربر (RespondPermissionConfirm) می‌ماند. مهلت/لغو = رد.
//
// سریال‌سازی: اجرای موازی ابزارهای فقط‌خواندنی می‌تواند چند تأیید
// هم‌زمان بخواهد (مثلاً دو read_file روی فایل حساس)؛ فرانت‌اند فقط یک مودال
// نشان می‌دهد و ask بعدی قبلی را رد می‌کند — پس اینجا با permAskMu صف
// می‌شوند و کاربر هرگز دو دیالوگ هم‌پوشان نمی‌بیند. این قفل عمداً در طول
// انتظار پاسخ نگه داشته می‌شود (کارش همین است) و در طول آن فقط changes.mu
// لحظه‌ای گرفته می‌شود — ترتیب: permAskMu → changes.mu (ARCHITECTURE.md).
func (a *App) requestPermissionConfirm(ctx context.Context, ask *permissionAsk) string {
	// بدون UI (تست/هدلس) منتظر ماندن بی‌معناست — رد
	if a.ctx == nil {
		return permRespondReject
	}
	// — داخل زیرایجنت هیچ مودالی باز نمی‌شود: askهای پس‌زمینه‌ای
	// (مثلاً خواندن فایل حساس) فوراً رد می‌شوند تا جریان کاربر قطع نشود.
	if subagentNoAsk(ctx) {
		return permRespondReject
	}
	a.permAskMu.Lock()
	defer a.permAskMu.Unlock()
	ch := make(chan string, 1)
	a.changes.mu.Lock()
	if a.changes.permAsks == nil {
		a.changes.permAsks = map[string]chan string{}
	}
	a.changes.permAsks[ask.ID] = ch
	a.changes.mu.Unlock()
	defer func() {
		a.changes.mu.Lock()
		delete(a.changes.permAsks, ask.ID)
		a.changes.mu.Unlock()
	}()

	runtimeEmit(a, "agent:permission_ask", map[string]interface{}{"ask": ask})

	// مشاهده‌گر تستی payload مودال (بدون UI واقعی) — مثل todosEmit باید
	// فقط پیش از هم‌زمانی‌ها تنظیم شود.
	if a.permAskEmit != nil {
		a.permAskEmit(ask)
	}

	timer := time.NewTimer(confirmTimeout)
	defer timer.Stop()
	select {
	case d := <-ch:
		return d
	case <-ctx.Done():
		return permRespondReject
	case <-timer.C:
		return permRespondReject
	}
}

// RespondPermissionConfirm پاسخ کاربر به مودال دسترسی را تحویل می‌دهد.
// decision: allow | always | reject (مقدار نامعتبر = reject).
func (a *App) RespondPermissionConfirm(askID, decision string) error {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case permRespondAllow, permRespondAlways, permRespondReject:
	default:
		return errors.New("تصمیم نامعتبر است (allow | always | reject)")
	}
	a.changes.mu.Lock()
	ch := a.changes.permAsks[askID]
	a.changes.mu.Unlock()
	if ch == nil {
		return errors.New("درخواست دسترسی پیدا نشد (منقضی یا پاسخ داده شده)")
	}
	select {
	case ch <- strings.ToLower(strings.TrimSpace(decision)):
		return nil
	default:
		return errors.New("این درخواست قبلاً پاسخ داده شده است")
	}
}

// requestCommandPermission مودال «اجازه اجرای دستور» را نشان می‌دهد.
// تصمیم always به قاعده‌ی ماندگار تبدیل و ذخیره می‌شود.
func (a *App) requestCommandPermission(ctx context.Context, command string) bool {
	ask := &permissionAsk{
		ID:      "perm-" + newID(),
		Kind:    "command",
		Command: command,
		Pattern: deriveAlwaysPattern(command),
		Message: "اجازه‌ی اجرای این دستور در پوشه‌ی کاری؟",
		At:      timeNow(),
	}
	switch a.requestPermissionConfirm(ctx, ask) {
	case permRespondAllow:
		return true
	case permRespondAlways:
		a.appendCommandRule(ask.Pattern, "allow")
		return true
	default:
		return false
	}
}

// appendCommandRule قاعده‌ی «همیشه اجازه» را ذخیره می‌کند (پایدار).
func (a *App) appendCommandRule(pattern, decision string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// الگوی تکراری به‌روزرسانی می‌شود، نه تکرار
	for i := range a.settings.Permissions.CommandRules {
		if a.settings.Permissions.CommandRules[i].Pattern == pattern {
			a.settings.Permissions.CommandRules[i].Decision = decision
			_ = a.saveSettings()
			return
		}
	}
	a.settings.Permissions.CommandRules = append(a.settings.Permissions.CommandRules,
		PermissionRule{Pattern: pattern, Decision: decision})
	_ = a.saveSettings()
}

// requestSensitiveRead تأیید خواندن فایل حساس (فقط allow/reject — بدون always).
func (a *App) requestSensitiveRead(ctx context.Context, rel, matchedGlob string) bool {
	ask := &permissionAsk{
		ID:      "perm-" + newID(),
		Kind:    "sensitive_read",
		Path:    rel,
		Message: "این فایل حساس است («" + matchedGlob + "»). اجازه‌ی خواندن محتوایش؟",
		At:      timeNow(),
	}
	return a.requestPermissionConfirm(ctx, ask) == permRespondAllow
}

// ---------------------------------------------------------------- مسیرهای حساس

// sensitiveGlobs قاعده‌های فایل حساس از تنظیمات (با پیش‌فرض امن).
func (a *App) sensitiveGlobs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sensitiveGlobsLocked()
}

// sensitiveGlobsLocked باید با a.mu قفل‌شده صدا زده شود.
func (a *App) sensitiveGlobsLocked() []string {
	if len(a.settings.Permissions.SensitiveGlobs) == 0 {
		return defaultSensitiveGlobs
	}
	return a.settings.Permissions.SensitiveGlobs
}

// sensitiveFileReason اگر نام فایل با الگوی حساس مطابقت داشت، الگو را برمی‌گرداند.
func sensitiveFileReason(name string, globs []string) string {
	base := filepath.Base(name)
	for _, g := range globs {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if globMatch(strings.ToLower(g), strings.ToLower(base)) {
			return g
		}
	}
	return ""
}

// ---------------------------------------------------------------- مدیریت دسترسی‌ها (UI)

// agentToolNames ابزارهای داخلی ایجنت به‌ترتیب نمایش در تنظیمات.
var agentToolNames = []string{
	"list_dir", "read_file", "search_files", "glob", "create_dir",
	"git_status", "git_diff", "git_log", "git_show",
	"write_file", "edit_file", "move_path", "delete_path",
	"run_command", "task",
}

// permissionDecisionString نمایش رشته‌ای تصمیم برای UI/API.
func permissionDecisionString(d permDecision) string {
	switch d {
	case permAllow:
		return "allow"
	case permDeny:
		return "deny"
	default:
		return "ask"
	}
}

// toolNameRe نام ابزار معتبر برای override (حروف/رقم/_ و -؛ حداکثر ۶۴ نویسه).
var toolNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// PermissionsView نمای کامل دسترسی‌ها برای تب تنظیمات.
// همه‌ی مقادیر «کپی» برمی‌گردند (تغییرشان وضعیت داخلی را عوض نمی‌کند) و
// فقط با گرفتن کوتاه a.mu ساخته می‌شوند — بدون I/O زیر قفل.
// خروجی:
// - tools: [{name, default, override}] — داخلی‌ها، سفارشی‌ها و overrideهای یتیم
// - commandRules: [{pattern, decision}] به‌ترتیب ذخیره (اولین تطبیق برنده)
// - sensitiveGlobs / defaultSensitiveGlobs (نمایش فقط)
// - confirmFileWrites / allowCommands برای متن راهنمای UI
func (a *App) PermissionsView() map[string]interface{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	confirmWrites := a.settings.ConfirmFileWrites
	tools := make([]map[string]string, 0, len(agentToolNames)+len(a.settings.CustomTools)+len(a.settings.Permissions.Tools))
	seen := make(map[string]bool, len(agentToolNames)+len(a.settings.CustomTools))
	addTool := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		tools = append(tools, map[string]string{
			"name":     name,
			"default":  permissionDecisionString(toolDefaultDecision(name, confirmWrites)),
			"override": a.settings.Permissions.Tools[name], // "" یعنی بدون override
		})
	}
	for _, n := range agentToolNames {
		addTool(n)
	}
	for _, ct := range a.settings.CustomTools {
		addTool("custom_" + ct.Name)
	}
	// overrideهای یتیم (مثلاً ابزار سفارشیِ حذف‌شده) — تا کاربر بتواند پاکشان کند
	for name := range a.settings.Permissions.Tools {
		addTool(name)
	}
	rules := make([]map[string]string, len(a.settings.Permissions.CommandRules))
	for i, r := range a.settings.Permissions.CommandRules {
		rules[i] = map[string]string{"pattern": r.Pattern, "decision": r.Decision}
	}
	globs := make([]string, len(a.sensitiveGlobsLocked()))
	copy(globs, a.sensitiveGlobsLocked())
	return map[string]interface{}{
		"tools":                 tools,
		"commandRules":          rules,
		"sensitiveGlobs":        globs,
		"defaultSensitiveGlobs": defaultSensitiveGlobs,
		"confirmFileWrites":     confirmWrites,
		"allowCommands":         a.settings.AllowCommands,
	}
}

// SetToolPermission override دسترسی یک ابزار را ذخیره می‌کند.
// decision: allow | ask | deny | default («default» = حذف override و بازگشت
// به رفتار پیش‌فرض). اعتبارسنجی نام/تصمیم سخت‌گیرانه است؛ ذخیره فقط با
// گرفتن a.mu (الگوی مرسوم SetAllowCommands و همتایانش).
func (a *App) SetToolPermission(tool, decision string) error {
	tool = strings.TrimSpace(tool)
	if !toolNameRe.MatchString(tool) {
		return errors.New("نام ابزار نامعتبر است")
	}
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "allow", "ask", "deny":
		decision = strings.ToLower(strings.TrimSpace(decision))
	case "default", "":
		decision = "default"
	default:
		return errors.New("تصمیم نامعتبر است (allow | ask | deny | default)")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if decision == "default" {
		delete(a.settings.Permissions.Tools, tool)
		return a.saveSettings()
	}
	if a.settings.Permissions.Tools == nil {
		a.settings.Permissions.Tools = map[string]string{}
	}
	a.settings.Permissions.Tools[tool] = decision
	return a.saveSettings()
}

// RemoveCommandRule یک قاعده‌ی دستور (معمولاً «همیشه اجازه بده») را حذف
// می‌کند تا دستورات متناظر دوباره تأیید بخواهند. حذف بر اساس تطابق دقیق
// الگوست؛ اگر پیدا نشد خطا برمی‌گردد (بازخورد صریح UI).
func (a *App) RemoveCommandRule(pattern string) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return errors.New("الگوی قاعده خالی است")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rules := a.settings.Permissions.CommandRules
	for i := range rules {
		if rules[i].Pattern == pattern {
			a.settings.Permissions.CommandRules = append(rules[:i:i], rules[i+1:]...)
			return a.saveSettings()
		}
	}
	return errors.New("قاعده‌ای با این الگو پیدا نشد")
}

// ResetPermissions همه‌ی دسترسی‌های سفارشی را پاک می‌کند: override ابزارها،
// قاعده‌های دستور و الگوهای حساس به پیش‌فرض امن برمی‌گردند. لیست سیاه سخت
// هرگز بخشی از تنظیمات نیست و دست‌نخورده می‌ماند.
func (a *App) ResetPermissions() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.Permissions = PermissionSettings{
		Tools:          map[string]string{},
		CommandRules:   []PermissionRule{},
		SensitiveGlobs: defaultSensitiveGlobs,
	}
	return a.saveSettings()
}
