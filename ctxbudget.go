// مدیریت کانتکست آگاه از توکن.
//
// پنج قطعه:
// 1. contextBudget — بودجه‌ی توکنِ تاریخچه: پنجره‌ی کانتکست Provider منهای
// رزرو خروجی (MaxTokens یا پیش‌فرض) منهای ۱۰٪ حاشیه‌ی ایمنی.
// 2. historyTokenCache — شمارش توکن پیام‌های تاریخچه با کش: هر پیام فقط
// یک‌بار تخمین زده می‌شود؛ فشرده‌سازی/حذف، کش را باطل می‌کند.
// 3. validateHistory — بازبینی قواعد پروتکل OpenAI (جفت‌بندی tool_calls/tool،
// system در ابتدا، محتوای خالی) با ترمیم درجا و گزارش مشکلات برای لاگ.
// 4. compactHistoryForBudget — فشرده‌سازی خودکار وقتی تاریخچه به ≥۸۰٪ بودجه
// رسید (فقط روی کپیِ ارسالی به مدل؛ فایل گفتگو دست‌نخورده می‌ماند).
// 5. isContextOverflowError + recoverHistoryFromOverflow — بازیابی از خطای
// «کانتکست بزرگ‌تر از پنجره» با فشرده‌سازی تهاجمی و یک تلاش مجدد.
//
// نکته‌ی زبانی (بخش ۲ پرامپت): رشته‌های رو‌به‌لاگ انگلیسی‌اند؛ اعلان‌های
// کاربر (chat:notice) فارسی. هیچ نرمال‌سازی حروفی روی متن انجام نمی‌شود و
// هر برشی فقط با cutUTF8 روی مرز کاراکتر است (متن فارسی نصف نمی‌شود).
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// بودجه = پنجره − رزرو خروجی − safetyPercent درصدِ پنجره
	contextSafetyPercent = 10
	// وقتی MaxTokens صفر (نامحدود) است این مقدار برای خروجی رزرو می‌شود
	defaultOutputReserve = 8192
	// کف بودجه — تنظیمات pathological نباید بودجه‌ی صفر/منفی بسازند
	minContextBudget = 1024
	// آستانه‌ی فشرده‌سازی خودکار: ≥۸۰٪ بودجه
	compactTriggerPercent = 80
	// هدف پس از فشرده‌سازی عادی: ۶۰٪ بودجه (تا هر دور دوباره فشرده نشود)
	compactTargetPercent = 60
	// هدف پس از بازیابی سرریز: ۴۰٪ بودجه
	overflowTargetPercent = 40
	// در فشرده‌سازی بودجه‌ای فقط این تعداد نتیجه‌ی ابزار آخر کامل می‌مانند
	// (ToolResultKeep مربوط به هرس شمارشی prepareStream است؛ اینجا تهاجمی‌تر)
	hardKeepToolResults = 3
	// در بازیابی سرریز، این حداقل پیام‌ها باید بماند (سیستم + آخرین کاربر + …)
	aggressiveMinKeepMessages = 2
)

// ---------------------------------------------------------------- بودجه

// contextBudget بودجه‌ی توکنِ تاریخچه را می‌سازد.
// خروجی ۰ یعنی «پنجره‌ی کانتکست نامعلوم» — در این حالت فشرده‌سازی خودکارِ
// دور‌به‌دور انجام نمی‌شود و فقط بازیابی سرریزِ اعلام‌شده‌ی سرویس‌دهنده فعال است.
func contextBudget(contextWindow, maxTokens int) int64 {
	if contextWindow <= 0 {
		return 0
	}
	reserve := maxTokens
	if reserve <= 0 {
		reserve = defaultOutputReserve
	}
	// رزرو خروجی بیش از یک‌چهارم پنجره عملاً بودجه را منفی می‌کند؛ سقف می‌خورد
	if reserve > contextWindow/4 {
		reserve = contextWindow / 4
	}
	b := int64(contextWindow - reserve - contextWindow*contextSafetyPercent/100)
	if b < minContextBudget {
		b = minContextBudget
	}
	return b
}

// ---------------------------------------------------------------- شمارش با کش

// estimateHistoryMessageTokens تخمین توکن یک پیام API شامل سربار نقش،
// tool_calls (نام + آرگومان‌ها) و tool_call_id — قرابت نزدیک‌تری با
// prompt_tokens واقعی نسبت به تخمینِ تنها Content.
func estimateHistoryMessageTokens(m chatMessage) int64 {
	n := estimateContentTokens(m.Content) + 4 // نقش و قالب پیام
	for _, tc := range m.ToolCalls {
		n += estimateTokens(tc.Function.Name) + estimateTokens(tc.Function.Arguments) + estimateTokens(tc.ID) + 3
	}
	if m.ToolCallID != "" {
		n += 5
	}
	return n
}

// historyTokenCache شمارش توکن تاریخچه با کش افزایشی: cached counts).
// مالک: گوروتین streamChat (تک‌نخی) — بدون قفل. تاریخچه در طول یک نوبت فقط
// به انتهایش اضافه می‌شود؛ هر تغییر محتوا/حذف با invalidate اعلام می‌شود.
type historyTokenCache struct {
	n     int     // تعداد پیام‌های حساب‌شده
	total int64   // مجموع تخمین تا پیام n−۱
	est   []int64 // تخمین هر پیام (برای بازبینی تست‌ها)
}

// totalOf مجموع توکن تاریخچه را برمی‌گرداند؛ پیام‌های تازه فقط یک‌بار
// تخمین زده می‌شوند.
func (c *historyTokenCache) totalOf(history []chatMessage) int64 {
	if c.n > len(history) {
		// تاریخچه کوتاه‌تر شده (حذف پیام) — حتی بدون invalidate صریح امن است
		c.n, c.total, c.est = 0, 0, nil
	}
	for c.n < len(history) {
		e := estimateHistoryMessageTokens(history[c.n])
		c.est = append(c.est, e)
		c.total += e
		c.n++
	}
	return c.total
}

// invalidate کش را پس از تغییر محتوای پیام‌ها یا حذف خالی می‌کند.
func (c *historyTokenCache) invalidate() {
	c.n, c.total, c.est = 0, 0, nil
}

// sumHistoryTokens شمارش بدون کش (مسیرهای بدون historyTokenCache).
func sumHistoryTokens(history []chatMessage) int64 {
	var n int64
	for i := range history {
		n += estimateHistoryMessageTokens(history[i])
	}
	return n
}

// tokenTotal شمارش با کش در صورت موجود بودن، وگرنه مستقیم.
func tokenTotal(h []chatMessage, counter *historyTokenCache) int64 {
	if counter != nil {
		return counter.totalOf(h)
	}
	return sumHistoryTokens(h)
}

// ---------------------------------------------------------------- بازبینی/ترمیم

// historyContentString Content رشته‌ای پیام را برمی‌گرداند (بقیه‌ی انواع «»).
func historyContentString(c interface{}) string {
	if s, ok := c.(string); ok {
		return s
	}
	return ""
}

// pinnedHistoryMarker پیشوند پیام‌های pinشده‌ای که prepareStream بعد از
// system دوباره تزریق می‌کند — در حذفِ قدیمی‌ها محافظت می‌شوند.
const pinnedHistoryMarker = "[pinned by the user — keep this in mind]"

// isPinnedHistoryMsg پیام بازتزریق‌شده‌ی pin کاربر است؟
func isPinnedHistoryMsg(m chatMessage) bool {
	return m.Role == "user" && strings.HasPrefix(historyContentString(m.Content), pinnedHistoryMarker)
}

// ---------------------------------------------------------------- نشانگر حذف کانتکست

// contextDropMarker پیشوند ثابت پیام «حذف کانتکست» — مثل pinnedHistoryMarker.
// وقتی فاز B چیزی حذف می‌کند، دقیقاً یک پیام با این پیشوند درست بعد از system
// و بلوک pinها می‌نشیند تا مدل بداند بخش‌های قدیمی رفته‌اند و به‌جای حدس
// زدن، از کاربر بپرسد یا فایل را دوباره بخواند. هرگز حذف نمی‌شود (firstDrop
// از رویش رد می‌شود) و متنش انگلیسی است چون رو به مدل است.
const contextDropMarker = "[context notice]"

// contextDropMarkerText متن کامل نشانگر — کوتاه و قابل‌اقدام.
const contextDropMarkerText = contextDropMarker + " Earlier messages of this conversation were removed to fit the context window. If you need details that are no longer visible, ask the user or re-read the relevant files instead of guessing."

// isContextDropMarker پیام نشانگر حذف کانتکست است؟
func isContextDropMarker(m chatMessage) bool {
	return m.Role == "user" && strings.HasPrefix(historyContentString(m.Content), contextDropMarker)
}

// ensureContextDropMarker دقیقاً یک نشانگر، درست بعد از system و پیام‌های
// pinشده، تضمین می‌کند. idempotent است: هر نشانگر قبلی (در هر جای تاریخچه)
// حذف و نسخه‌ی واحد تازه در جای درست می‌نشیند؛ بقیه‌ی پیام‌ها دست‌نخورده
// می‌مانند. فقط وقتی صدا زده شود که فاز B واقعاً چیزی حذف کرده باشد.
func ensureContextDropMarker(h []chatMessage) []chatMessage {
	pos := 0
	if len(h) > 0 && h[0].Role == "system" {
		pos = 1
	}
	for pos < len(h) && isPinnedHistoryMsg(h[pos]) {
		pos++
	}
	out := make([]chatMessage, 0, len(h)+1)
	out = append(out, h[:pos]...)
	out = append(out, chatMessage{Role: "user", Content: contextDropMarkerText})
	for i := pos; i < len(h); i++ {
		if isContextDropMarker(h[i]) {
			continue // نسخه‌های قبلی حذف می‌شوند — یکی کافی است
		}
		out = append(out, h[i])
	}
	return out
}

// contextDropMarkerTodoLimit سقف کاراکتر فهرست کارها در نشانگر (بند A.5).
const contextDropMarkerTodoLimit = 1200

// enrichContextDropMarker فهرست کارهای جاری را به «تنها» نشانگر حذف کانتکست
// می‌چسباند تا طرح کار پس از فشرده‌سازی از یاد مدل نرود. idempotent است:
// هر بار محتوای نشانگر از نو ساخته می‌شود (نه انباشته)، و اگر نشانگری در
// تاریخچه نباشد یا بلوک خالی باشد تاریخچه دست‌نخورده برمی‌گردد.
func enrichContextDropMarker(h []chatMessage, todosBlock string) []chatMessage {
	if strings.TrimSpace(todosBlock) == "" {
		return h
	}
	block := cutUTF8(todosBlock, contextDropMarkerTodoLimit)
	out := make([]chatMessage, 0, len(h))
	touched := false
	for _, m := range h {
		if isContextDropMarker(m) {
			if touched {
				continue // فقط یک نشانگر می‌ماند
			}
			touched = true
			m.Content = contextDropMarkerText + "\n" + block
		}
		out = append(out, m)
	}
	return out
}

// validateHistory تاریخچه را با قواعد پروتکل OpenAI بازبینی و «ترمیم‌شده»
// برمی‌گرداند. خروجی: (تاریخچه‌ی سالم، فهرست مشکلات انگلیسی برای لاگ).
//
// ترمیم‌ها:
// - role نامعتبر → حذف پیام
// - system دومی یا وسط تاریخچه → ادغام در system اول / انتقال به ابتدا
// - محتوای رشته‌ای خالی user/assistant بدون tool_calls → «(empty)»
// - نتیجه‌ی ابزار بی‌صاحب یا تکراری → حذف
// - نتیجه‌ی گمشده‌ی tool_calls → ساخت پیام tool جایگزین
//
// ترتیب پیام‌های سالم حفظ می‌شود و ترمیم هم‌دانه است (اجرای دوم بدون مشکل).
func validateHistory(history []chatMessage) ([]chatMessage, []string) {
	var problems []string
	if len(history) == 0 {
		return history, []string{"history: empty history"}
	}

	// گذر ۱ — نقش‌ها، system و محتوای خالی
	pass1 := make([]chatMessage, 0, len(history))
	for i, m := range history {
		switch m.Role {
		case "system":
			if len(pass1) == 0 {
				pass1 = append(pass1, m)
				continue
			}
			if pass1[0].Role == "system" {
				pass1[0].Content = historyContentString(pass1[0].Content) + "\n\n" + historyContentString(m.Content)
				problems = append(problems, fmt.Sprintf("history: extra system message at #%d merged into the first", i))
				continue
			}
			// system در میانه — فقط system اول پذیرفته است؛ به ابتدا منتقل می‌شود
			pass1 = append([]chatMessage{m}, pass1...)
			problems = append(problems, fmt.Sprintf("history: mid-history system message at #%d moved to the front", i))
		case "user", "assistant", "tool":
			if m.Role != "assistant" || len(m.ToolCalls) == 0 {
				if _, isStr := m.Content.(string); isStr && strings.TrimSpace(historyContentString(m.Content)) == "" {
					m.Content = "(empty)"
					problems = append(problems, fmt.Sprintf("history: empty %s content at #%d replaced with placeholder", m.Role, i))
				}
			}
			pass1 = append(pass1, m)
		default:
			problems = append(problems, fmt.Sprintf("history: message #%d with unknown role %q dropped", i, m.Role))
		}
	}

	// گذر ۲ — جفت‌بندی tool_calls/tool (الزام پروتکل OpenAI)
	out := make([]chatMessage, 0, len(pass1))
	for i := 0; i < len(pass1); i++ {
		m := pass1[i]
		if !(m.Role == "assistant" && len(m.ToolCalls) > 0) {
			if m.Role == "tool" {
				problems = append(problems, fmt.Sprintf("history: unattached tool result %q dropped", m.ToolCallID))
				continue
			}
			out = append(out, m)
			continue
		}
		// دسته‌ی نتایج این دستیار: همه‌ی پیام‌های tool بعدیِ پیوسته
		declared := map[string]bool{}
		for _, tc := range m.ToolCalls {
			declared[tc.ID] = true
		}
		results := map[string]string{}
		j := i + 1
		for ; j < len(pass1) && pass1[j].Role == "tool"; j++ {
			id := pass1[j].ToolCallID
			if !declared[id] {
				problems = append(problems, fmt.Sprintf("history: orphan tool result %q after assistant #%d dropped", id, i))
				continue
			}
			if _, dup := results[id]; dup {
				problems = append(problems, fmt.Sprintf("history: duplicate tool result %q dropped", id))
				continue
			}
			results[id] = historyContentString(pass1[j].Content)
		}
		out = append(out, m)
		for _, tc := range m.ToolCalls {
			if c, ok := results[tc.ID]; ok {
				out = append(out, chatMessage{Role: "tool", Content: c, ToolCallID: tc.ID})
				continue
			}
			problems = append(problems, fmt.Sprintf("history: missing tool result for %q synthesized", tc.ID))
			out = append(out, chatMessage{
				Role:       "tool",
				Content:    "(tool result unavailable — the history was repaired)",
				ToolCallID: tc.ID,
			})
		}
		i = j - 1
	}
	return out, problems
}

// ---------------------------------------------------------------- فشرده‌سازی آرگومان‌های ابزار

// toolArgCompactRunes آستانه‌ی فشرده‌سازی مقدارهای بزرگ داخل آرگومان‌های
// tool_call (بر حسب کاراکتر، rune-safe).
const toolArgCompactRunes = 400

// toolArgCompactKeys کلیدهای آرگومانی که ممکن است بسیار بزرگ باشند
// (write_file/edit_file — بقیه‌ی فیلدها کوتاه‌اند).
var toolArgCompactKeys = map[string]bool{"content": true, "new_string": true, "old_string": true}

// compactToolCallArgs مقدارهای رشته‌ای بزرگ داخل آرگومان‌های یک فراخوانی
// ابزار را با جای‌نگهدار کوتاه عوض می‌کند:
// - JSON معتبر می‌ماند؛ id/name/جفت‌بندی دست‌نخورده (خودشان بیرونِ رشته‌ی
// arguments هستند)؛
// - متن‌های دیگر بایت‌به‌بایت حفظ می‌شوند (json.Marshal نویسه‌های فارسی و
// ZWNJ را escape نمی‌کند)؛
// - شمارش کاراکتر با RuneCountInString؛ جای‌نگهدار انگلیسی است چون رو به
// مدل است؛
// - آرگومان خالی/JSON خراب → بدون تغییر؛
// - idempotent: جای‌نگهدار (~۴۰ کاراکتر) زیر آستانه است و دوباره عوض نمی‌شود.
func compactToolCallArgs(argsJSON string) string {
	s := strings.TrimSpace(argsJSON)
	if s == "" {
		return argsJSON
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(s), &args); err != nil || args == nil {
		return argsJSON // JSON خراب — دست نمی‌زنیم
	}
	changed := false
	for k, v := range args {
		if !toolArgCompactKeys[k] {
			continue
		}
		str, isStr := v.(string)
		if !isStr {
			continue
		}
		if n := utf8.RuneCountInString(str); n > toolArgCompactRunes {
			args[k] = fmt.Sprintf("[%d characters omitted from history]", n)
			changed = true
		}
	}
	if !changed {
		return argsJSON
	}
	out, err := json.Marshal(args)
	if err != nil {
		return argsJSON
	}
	return string(out)
}

// ---------------------------------------------------------------- فشرده‌سازی

// compactHistoryForBudget تاریخچه‌ی «ارسالی به مدل» را تا زیر هدف کوچک می‌کند.
// فقط همین slice دست می‌خورد؛ فایل گفتگو روی دیسک دست‌نخورده می‌ماند.
//
// فاز A: (۱) متن نتایج ابزار (به‌جز hardKeepToolResults مورد آخر) با
// compactToolResult فشرده می‌شود؛ (۲) آرگومان‌های بزرگِ فراخوانی‌هایی که
// نتیجه‌شان قدیمی است با compactToolCallArgs کوچک می‌شوند —
// جفت‌بندی حفظ می‌شود چون فقط محتوای رشته‌ی arguments عوض می‌شود.
// فاز B: قدیمی‌ترین «واحدها» (پیام + نتایج ابزارش) حذف می‌شوند؛ system،
// پیام‌های pinشده، نشانگر حذف و آخرین پیام کاربر هرگز حذف نمی‌شوند و
// حداقل minKeepMessages پیام (در حالت تهاجمی ۲) باقی می‌ماند. در حالت
// تهاجمی، واحدهای «بعد از آخرین پیام کاربر» هم تا ناحیه‌ی حفاظت‌شده‌ی
// نتایج تازه قابل حذف‌اند تا یک نوبتِ بلند ایجنت بتواند
// کوچک شود. پس از هر حذف، دقیقاً یک نشانگر حذف کانتکست بعد از بلوک
// ابتدایی می‌نشیند و تاریخچه هرگز با assistant/tool یتیم شروع نمی‌شود
// .
//
// aggressive=true (بازیابی سرریز): هدف ۴۰٪ بودجه؛ و اگر تخمین ما از قبل زیر
// هدف باشد هم نصف واحدهای قابل حذف کنار می‌روند (سرویس‌دهنده سرریز را عین
// خطا اعلام کرده — تخمینِ ما می‌تواند کم‌برآورد باشد).
//
// summarize (اختیاری، گام ۸): اگر داده شود، پیش از حذف، خلاصهٔ مدل از
// پیام‌های حذف‌شده گرفته می‌شود و همراه «واقعیت‌های سنجاق‌شده» در نشانگر
// فشرده‌سازی می‌نشیند (best-effort — خطای آن فقط لاگ می‌شود).
func compactHistoryForBudget(history []chatMessage, budget int64, counter *historyTokenCache, aggressive bool, summarize ...func(dropped []chatMessage) string) ([]chatMessage, bool) {
	h := history
	if len(h) == 0 {
		return h, false
	}
	if budget <= 0 && !aggressive {
		return h, false // پنجره‌ی نامعلوم — فشرده‌سازی خودکار معنا ندارد
	}
	changed := false

	// ---------- فاز A: فشرده‌سازی نتایج ابزار و آرگومان‌های قدیمی ----------
	var toolIdx []int
	for i, m := range h {
		if m.Role == "tool" {
			toolIdx = append(toolIdx, i)
		}
	}
	for j := 0; j < len(toolIdx)-hardKeepToolResults; j++ {
		i := toolIdx[j]
		before := historyContentString(h[i].Content)
		if c := compactToolResult(before); c != before {
			h[i].Content = c
			changed = true
		}
	}
	// آرگومان‌های بزرگ فراخوانی‌هایی که نتیجه‌شان قدیمی است — نقشه‌ی
	// id→ایندکس نتیجه تا «قدیمی» بودن به‌ازای هر فراخوانی دیده شود؛
	// فراخوانیِ بدون نتیجه (یتیمِ ترمیم‌نشده) هم قدیمی تلقی می‌شود.
	resultAt := make(map[string]int, len(toolIdx))
	for _, i := range toolIdx {
		if id := h[i].ToolCallID; id != "" {
			if _, ok := resultAt[id]; !ok {
				resultAt[id] = i
			}
		}
	}
	cutoffIdx := len(h)
	if len(toolIdx) > hardKeepToolResults {
		cutoffIdx = toolIdx[len(toolIdx)-hardKeepToolResults]
	}
	for i := range h {
		if h[i].Role != "assistant" {
			continue
		}
		for j := range h[i].ToolCalls {
			id := h[i].ToolCalls[j].ID
			if ri, ok := resultAt[id]; ok && ri >= cutoffIdx {
				continue // نتیجه‌ی تازه — آرگومان کامل می‌ماند
			}
			before := h[i].ToolCalls[j].Function.Arguments
			if after := compactToolCallArgs(before); after != before {
				h[i].ToolCalls[j].Function.Arguments = after
				changed = true
			}
		}
	}
	if changed && counter != nil {
		counter.invalidate()
	}

	target := int64(0)
	if budget > 0 {
		pct := int64(compactTargetPercent)
		if aggressive {
			pct = overflowTargetPercent
		}
		target = budget * pct / 100
		if tokenTotal(h, counter) <= target && !aggressive {
			return h, changed // فاز A کافی بود
		}
	}

	// ---------- فاز B: حذف قدیمی‌ترین واحدها ----------
	lastUser := -1
	for i := len(h) - 1; i >= 0; i-- {
		if h[i].Role == "user" {
			lastUser = i
			break
		}
	}
	if lastUser < 0 {
		return h, changed // بدون پیام کاربر — ساختار نامعمول؛ دست نمی‌زنیم
	}
	firstDrop := 0
	if h[0].Role == "system" {
		firstDrop = 1
	}
	for firstDrop < len(h) && (isPinnedHistoryMsg(h[firstDrop]) || isContextDropMarker(h[firstDrop])) {
		firstDrop++
	}
	// ناحیه‌ی حفاظت‌شده: آخرین hardKeepToolResults نتیجه‌ی ابزار (همان‌هایی
	// که فاز A کامل نگهشان می‌دارد) — واحدهای داخل آن هرگز حذف نمی‌شوند.
	protectFrom := len(h)
	if n := len(toolIdx); n > 0 {
		k := n - hardKeepToolResults
		if k < 0 {
			k = 0
		}
		protectFrom = toolIdx[k]
	}

	// واحدها روی کل تاریخچه ساخته می‌شوند (پیام + نتایج ابزار پیوسته‌اش).
	type hUnit struct{ start, end int }
	var units []hUnit
	for i := firstDrop; i < len(h); {
		if h[i].Role == "tool" {
			i++ // نتیجه‌ی بی‌اعلام‌کننده در همین بازه — جدا حذف‌شدنی نیست
			continue
		}
		e := i + 1
		if h[i].Role == "assistant" && len(h[i].ToolCalls) > 0 {
			for e < len(h) && h[e].Role == "tool" {
				e++
			}
		}
		units = append(units, hUnit{i, e})
		i = e
	}
	// قابل‌حذف = واحدِ «کامل قبل از آخرین پیام کاربر» یا (فقط تهاجمی)
	// واحدِ «بعد از آخرین کاربر تا ناحیه‌ی حفاظت‌شده» — واحدِ خودِ آخرین
	// پیام کاربر با هیچ‌کدام مطابق نیست و همیشه می‌ماند. حذف فقط به‌صورت
	// پیشوندی از ابتدای این فهرست (قدیمی‌ترین اول).
	var droppable []hUnit
	for _, u := range units {
		front := u.end <= lastUser
		tail := aggressive && u.start > lastUser && u.end <= protectFrom
		if !front && !tail {
			continue
		}
		droppable = append(droppable, u)
	}
	if len(droppable) == 0 {
		return h, changed
	}
	nFront := 0
	for nFront < len(droppable) && droppable[nFront].end <= lastUser {
		nFront++
	}

	// بیشینه‌ی واحدهای حذف‌شدنی با رعایت کف پیام
	minKeep := minKeepMessages
	if aggressive {
		minKeep = aggressiveMinKeepMessages
	}
	maxDroppable := 0
	for k := 1; k <= len(droppable); k++ {
		if len(h)-droppable[k-1].end >= minKeep {
			maxDroppable = k
		}
	}

	dropped := 0
	if budget > 0 && tokenTotal(h, counter) > target {
		// حذف طمع‌جویانه از ابتدا تا زیر هدف (شبیه‌سازی با کم‌کردن توکن واحد)
		total := tokenTotal(h, counter)
		for k := 0; k < maxDroppable; k++ {
			var unitTokens int64
			for i := droppable[k].start; i < droppable[k].end; i++ {
				unitTokens += estimateHistoryMessageTokens(h[i])
			}
			total -= unitTokens
			dropped = k + 1
			if total <= target {
				break
			}
		}
	} else if aggressive {
		// تخمین زیر هدف است ولی سرویس‌دهنده سرریز گفته — نصف واحدها حذف می‌شود
		dropped = (len(droppable) + 1) / 2
		if dropped > maxDroppable {
			dropped = maxDroppable
		}
	}
	if dropped > 0 {
		// برشِ جلویی: پایان آخرین واحدِ حذف‌شده از ابتدای تاریخچه؛ اگر
		// nFront واحدِ اول حذف شده باشد، برش تا همان‌جاست وگرنه تا
		// آخرین واحدِ جلویی (واحدهای انتهایی از بعدِ آخرین کاربر
		// جداگانه بریده می‌شوند تا خودِ پیام کاربر بماند).
		frontCut := firstDrop
		if nFront > 0 {
			frontCut = droppable[min(dropped, nFront)-1].end
		}
		// تاریخچه هرگز نباید بعد از بلوک ابتدایی با assistant/tool یتیم
		// شروع شود؛ اگر اولین پیامِ بازمانده چنین است، همان واحدها هم
		// کنار می‌روند. از آخرین پیام کاربر عبور نمی‌کند.
		for frontCut < lastUser && frontCut < len(h) &&
			(h[frontCut].Role == "assistant" || h[frontCut].Role == "tool") {
			frontCut++
		}
		// پیام‌های حذف‌شده را برای خلاصه‌سازی/سنجاق نگه می‌داریم.
		var droppedMsgs []chatMessage
		for k := 0; k < dropped; k++ {
			for i := droppable[k].start; i < droppable[k].end && i < len(h); i++ {
				droppedMsgs = append(droppedMsgs, h[i])
			}
		}
		out := make([]chatMessage, 0, len(h))
		out = append(out, h[:firstDrop]...)
		if dropped <= nFront {
			out = append(out, h[frontCut:]...)
		} else {
			// حذف دو‌بخشی: جلو (تا قبل از آخرین کاربر) + دنباله‌ی
			// قدیمیِ بعد از آخرین کاربر (تا شروع واحدهای محافظت‌شده)
			tailStart := droppable[nFront].start
			out = append(out, h[frontCut:tailStart]...)
			out = append(out, h[droppable[dropped-1].end:]...)
		}
		// نشانگر حذف کانتکست — دقیقاً یکی، بعد از system/pin. پیش از آن
		// واقعیت‌های سنجاق‌شده (قطعی) و در صورت امکان خلاصهٔ مدل.
		h = ensureContextDropMarker(out)
		summary := ""
		if len(summarize) > 0 && summarize[0] != nil {
			summary = summarize[0](droppedMsgs)
		}
		h = enrichContextDropMarkerDetails(h, summary, pinnedFactsFrom(droppedMsgs, compactFactsLimit))
		changed = true
		if counter != nil {
			counter.invalidate()
		}
	}
	return h, changed
}

// recoverHistoryFromOverflow بازیابی از خطای سرریز کانتکست سرویس‌دهنده:
// فشرده‌سازی تهاجمی — هدف ۴۰٪ بودجه، و در نبود بودجه (پنجره‌ی نامعلوم)
// حذف نصف واحدهای قدیمی + فشرده‌سازی همه‌ی نتایج ابزار به‌جز ۳ مورد آخر.
// خروجی دوم می‌گوید چیزی واقعاً کوچک شد یا نه.
func recoverHistoryFromOverflow(history []chatMessage, budget int64, counter *historyTokenCache) ([]chatMessage, bool) {
	return compactHistoryForBudget(history, budget, counter, true)
}

// ---------------------------------------------------------------- تشخیص سرریز

// isContextOverflowError آیا این خطای HTTP «تاریخچه از پنجره‌ی کانتکست
// بزرگ‌تر است»؟ فقط 400/413/422 با اشاره‌ی صریح به طول کانتکست/توکن.
// خطاهای stream_options و tools و max_tokens پیش از این بررسی جدا می‌شوند
// (ترتیب چک‌ها در streamChat)؛ کلیدواژه‌ها طوری انتخاب شده‌اند که با هیچ‌کدام
// از آن پیام‌ها هم‌پوشانی نداشته باشند.
func isContextOverflowError(status int, body string) bool {
	if status != 400 && status != 413 && status != 422 {
		return false
	}
	b := strings.ToLower(body)
	for _, k := range []string{
		"context length", "context window", "context limit", "context_length",
		"maximum context", "exceeds the context", "exceeded the context",
		"too many tokens", "too many input tokens", "input tokens exceed",
		"prompt is too long", "input is too long", "too long for the model",
		"reduce the input", "reduce the prompt", "reduce your prompt",
		"reduce the length", "total tokens", "longer than the model's context",
	} {
		if strings.Contains(b, k) {
			return true
		}
	}
	return false
}

// enrichContextDropMarkerDetails خلاصهٔ مدل و واقعیت‌های سنجاق‌شده را به
// «تنها» نشانگر حذف کانتکست می‌چسباند (گام ۸). دقیقاً برعکس enrichContextDropMarker
// محتوای قبلی نشانگر حفظ می‌شود؛ فقط یک‌بار و به‌صورت کران‌دار می‌نشیند تا
// نشانگرهای تکراری ساخته نشوند.
func enrichContextDropMarkerDetails(h []chatMessage, summary string, pinned []string) []chatMessage {
	summary = strings.TrimSpace(summary)
	if summary == "" && len(pinned) == 0 {
		return h
	}
	var block strings.Builder
	block.WriteString("Context kept from the removed part (do not redo this work):\n")
	if len(pinned) > 0 {
		block.WriteString("Pinned facts:\n")
		for _, p := range pinned {
			block.WriteString("- " + p + "\n")
		}
	}
	if summary != "" {
		block.WriteString("Summary of the removed part:\n" + summary + "\n")
	}
	text := contextDropMarkerText + "\n" + cutUTF8(strings.TrimRight(block.String(), "\n"), compactMarkerPartLimit)
	out := make([]chatMessage, 0, len(h))
	touched := false
	for _, m := range h {
		if isContextDropMarker(m) {
			if touched {
				continue
			}
			touched = true
			// اگر نشانگر از قبل فهرست کارها دارد، آن را حفظ می‌کنیم.
			extra := ""
			if rest := strings.TrimPrefix(historyContentString(m.Content), contextDropMarkerText); strings.TrimSpace(rest) != "" {
				extra = "\n" + strings.TrimSpace(rest)
			}
			m.Content = text + extra
		}
		out = append(out, m)
	}
	return out
}
