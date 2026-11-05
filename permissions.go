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
// تجزیه‌ساز واقعی (shellparse.go) + لیست‌سفید ساختاری (cmdgate.go) هدفهندی
// را بسیار سخت‌تر کرده اما اجرای واقعی همچنان با همان رشته‌ای است که ارزیابی
// شده، روی ماشین کاربر و بدون sandbox (README: مدل امنیتی).
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
	// WebHosts اجازه‌ی ماندگار web_fetch به‌ازای دامنه (C.1): Pattern نام
	// میزبان یا *.domain است. اجازه‌ی سراسری برای این ابزار وجود ندارد.
	WebHosts []PermissionRule `json:"webHosts,omitempty"`
}

// defaultSensitiveGlobs فایل‌هایی که خواندنشان تأیید می‌خواهد.
var defaultSensitiveGlobs = []string{
	".env*", "*.pem", "*.key", "id_rsa*", "*credentials*", "*.pfx",
	".npmrc", ".netrc",
}

// لیست‌سفید دستورات از ۱.۸.۰ «ساختاری» است (cmdgate.go: safeCommandSpecs):
// تصمیم allow روی argv/پرچم/تغییرمسیرِ تجزیه‌شده گرفته می‌شود، نه glob
// روی رشته‌ی نرمال. cat/type/head/tail/wc حذف شده‌اند (ایجنت read_file دارد
// که قواعد فایل حساس را اجباری می‌کند — D3).

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
		// درخواست به بیرون از ماشین کاربر است — هر دامنه با تأیید صریح
		// (مودال، آدرس با ایزوله‌سازی bidi). اجازه‌ی ماندگار فقط per-host
		// است (webfetchperm.go) — override سراسری «allow» پذیرفته نمی‌شود
		// چون این ابزار می‌تواند راز را در URL به بیرون ببرد.
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
			// web_fetch هرگز اجازه‌ی سراسری نمی‌گیرد (فایل قدیمی/دستکاری)
			if tool == webFetchToolName {
				d = permAsk
				break
			}
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
// از ۱.۸.۰ تجزیه‌ی واقعی در shellparse.go است (parseShellCommand) و
// ارزیابی ساختاری در cmdgate.go (evaluateCommandDetailed). splitCommandSegments
// و expandShellPayloads حذف شدند — هر دو با تجزیه‌ساز واقعی جایگزین شده‌اند.

// normalizeSegment نرمال‌سازی برای «تطبیق الگو» (نه اجرا):
// حروف کوچک، ${IFS}/$IFS → فاصله، حذف escape/کوتیشن/$/backtick
// (مقاوم‌سازی در برابر r""m، rm${IFS}-rf و...) و جمع‌کردن فاصله‌ها.
// پرانتزها حفظ می‌شوند — الگوی fork bomb به آن‌ها نیاز دارد؛ محتوای
// فرمان‌جایگزینی از قبل توسط parseShellCommand جدا و بازگشتی ارزیابی می‌شود.
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

// evaluateCommandPermissions ارزیابی کامل دستور (نقطه‌ی ورود خالص — امضای
// قبلی حفظ شده تا فراخوانی‌ها/تست‌ها کار کنند). جزئیات و قواعد ترتیب در
// cmdgate.go (evaluateCommandDetailed) است: لیست سیاه سخت روی رشته‌ی خام و
// روی نمای هر دستور ساده؛ ارجاع حساس → حداقل ask بدون «همیشه»؛ قاعده‌ی
// کاربر (اولین تطبیق؛ allow صریح فقط با پوشش صریح توکن پرریسک)؛ پرریسک →
// ask؛ لیست‌سفید ساختاری → allow؛ غیر این → ask. سخت‌گیرترین بخش برنده است.
// خروجی: تصمیم + دلیل فارسی برای deny/ask.
func evaluateCommandPermissions(cmd string, rules []PermissionRule) (permDecision, string) {
	e := evaluateCommandDetailed(cmd, rules, defaultSensitiveGlobs, "")
	return e.decision, e.reason
}

// deriveAlwaysPattern الگوی «همیشه اجازه بده» — پیاده‌سازی جدید در
// cmdgate.go: فقط برای جفت‌های تأییدشده الگوی عام، برای بقیه قاعده‌ی
// «دستورِ دقیق» (۳.۲.۶-ج؛ هیچ‌وقت برای push/reset/clean/… الگوی عام).

// ---------------------------------------------------------------- دروازه‌ی اجرا

// evalCommandPerm ارزیابی دستور با قاعده‌های فعلی (a.mu کوتاه گرفته می‌شود).
func (a *App) evalCommandPerm(command string) (permDecision, string) {
	e := a.evalCommandPermDetailed(command)
	return e.decision, e.reason
}

// evalCommandPermDetailed ارزیابی کامل با الگوهای حساسِ کاربر، پوشه‌ی کاری
// و قاعده‌های نشست (حافظه‌ای) — wrapper ای‌ برای evaluateCommandDetailed.
func (a *App) evalCommandPermDetailed(command string) commandEval {
	a.mu.Lock()
	rules := make([]PermissionRule, 0, len(a.settings.Permissions.CommandRules)+len(a.sessionRules))
	rules = append(rules, a.settings.Permissions.CommandRules...)
	rules = append(rules, a.sessionRules...)
	globs := a.sensitiveGlobsLocked()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()
	return evaluateCommandDetailed(command, rules, globs, ws)
}

// gateCommand دروازه‌ی واحد اجرای دستور — هم برای ابزار ایجنت، هم RunCommand
// و هم ابزارهای سفارشی. forceAsk برای ابزارهای سفارشیِ دارای Confirm است.
// rejectMsg: پیام «رد شدن به‌دست کاربر» — انگلیسی/فنی برای مدل، فارسی برای UI.
// خروجی: (مجاز؟, پیام خطا با پیشوند ERROR:).
func (a *App) gateCommand(ctx context.Context, command string, forceAsk bool, rejectMsg string) (bool, string) {
	e := a.evalCommandPermDetailed(command)
	decision := e.decision
	if forceAsk && decision == permAllow {
		decision = permAsk
	}
	switch decision {
	case permDeny:
		return false, "ERROR: " + e.reason
	case permAsk:
		if !a.requestCommandPermission(ctx, command, e.sensitive) {
			return false, "ERROR: " + rejectMsg
		}
		// نقطه‌ی بازگشت پروژه (B): دستورِ غیرِ لیست‌سفید ممکن است فایل
		// بسازد/عوض کند — پس از تأیید کاربر و پیش از اجرا از کل پروژه
		// snapshot می‌گیریم. دستورهای خودکارِ فقط‌خواندنی (go test) آن را
		// نمی‌سازند تا هزینه‌ی بی‌مورد نپردازیم.
		a.ensureTurnCheckpoint(ctx)
	}
	return true, ""
}

// ---------------------------------------------------------------- تأیید تعاملی دسترسی

// پیام‌های تصمیم مودال دسترسی (از فرانت‌اند)
const (
	permRespondAllow   = "allow"   // یک‌بار
	permRespondAlways  = "always"  // همیشه برای این الگو (ماندگار)
	permRespondSession = "session" // تا پایان این نشست (حافظه‌ای)
	permRespondReject  = "reject"  // رد
)

// permissionAsk یک درخواست تأیید دسترسی در انتظار پاسخ کاربر.
type permissionAsk struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // command | sensitive_read
	Command  string `json:"command,omitempty"`
	Pattern  string `json:"pattern,omitempty"`  // الگوی «همیشه اجازه» (فقط kind=command)
	NoAlways bool   `json:"noAlways,omitempty"` // حساس: بدون گزینه‌ی «همیشه/نشست»
	Path     string `json:"path,omitempty"`     // فایل حساس (فقط kind=sensitive_read)
	URL      string `json:"url,omitempty"`      // آدرس درخواستی (فقط ابزار web_fetch) — نمایش با ایزوله‌سازی bidi
	Message  string `json:"message"`            // فارسی — نمایش به کاربر
	At       int64  `json:"at"`
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
// decision: allow | always | session | reject (مقدار نامعتبر = reject).
func (a *App) RespondPermissionConfirm(askID, decision string) error {
	defer a.recoverBound("RespondPermissionConfirm")
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case permRespondAllow, permRespondAlways, permRespondSession, permRespondReject:
	default:
		return errors.New("تصمیم نامعتبر است (allow | always | session | reject)")
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
// sens=true (ارجاع حساس): بدون الگو و بدون گزینه‌های «همیشه/نشست» (۳.۲.۴).
// پاسخ‌ها: allow یک‌بار؛ session قاعده‌ی حافظه‌ایِ همین نشست؛ always قاعده‌ی
// ماندگار با مشتق امن الگو (فقط جفت‌های تأییدشده — cmdgate.go).
func (a *App) requestCommandPermission(ctx context.Context, command string, sens bool) bool {
	pattern := ""
	if !sens {
		pattern = deriveAlwaysPattern(command)
	}
	ask := &permissionAsk{
		ID:       "perm-" + newID(),
		Kind:     "command",
		Command:  command,
		Pattern:  pattern,
		NoAlways: sens,
		Message:  "اجازه‌ی اجرای این دستور در پوشه‌ی کاری؟",
		At:       timeNow(),
	}
	switch a.requestPermissionConfirm(ctx, ask) {
	case permRespondAllow:
		return true
	case permRespondSession:
		if pattern != "" {
			a.appendSessionCommandRule(pattern)
		}
		return true
	case permRespondAlways:
		if pattern != "" {
			a.appendCommandRule(pattern, "allow")
		}
		return true
	default:
		return false
	}
}

// appendSessionCommandRule قاعده‌ی «اجازه تا پایان نشست» را در حافظه ذخیره
// می‌کند (بدون نوشتن دیسک؛ با بستن برنامه محو می‌شود). محافظت: a.mu.
func (a *App) appendSessionCommandRule(pattern string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.sessionRules {
		if a.sessionRules[i].Pattern == pattern {
			a.sessionRules[i].Decision = "allow"
			return
		}
	}
	a.sessionRules = append(a.sessionRules, PermissionRule{Pattern: pattern, Decision: "allow"})
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
	defer a.recoverBound("PermissionsView")
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
	// دامنه‌های مجاز web_fetch (C.1) — فقط خواندنی برای تب تنظیمات
	hosts := make([]map[string]string, len(a.settings.Permissions.WebHosts))
	for i, r := range a.settings.Permissions.WebHosts {
		hosts[i] = map[string]string{"pattern": r.Pattern, "decision": r.Decision}
	}
	sessHosts := make([]map[string]string, len(a.sessionWebHosts))
	for i, r := range a.sessionWebHosts {
		sessHosts[i] = map[string]string{"pattern": r.Pattern, "decision": r.Decision}
	}
	return map[string]interface{}{
		"tools":                 tools,
		"commandRules":          rules,
		"webHosts":              hosts,
		"sessionWebHosts":       sessHosts,
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
	defer a.recoverBound("SetToolPermission")
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
	if tool == webFetchToolName && decision == "allow" {
		// اجازه‌ی سراسری web_fetch = کانال نشت داده؛ فقط per-host مجاز است
		return errors.New("برای web_fetch اجازه‌ی سراسری وجود ندارد؛ دامنه‌ها را هنگام دریافت تأیید کنید")
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
	defer a.recoverBound("RemoveCommandRule")
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
	defer a.recoverBound("ResetPermissions")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.Permissions = PermissionSettings{
		Tools:          map[string]string{},
		CommandRules:   []PermissionRule{},
		SensitiveGlobs: defaultSensitiveGlobs,
		WebHosts:       []PermissionRule{},
	}
	a.sessionWebHosts = nil
	return a.saveSettings()
}
