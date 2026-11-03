// تطبیق‌گر مقاوم edit_file — آبشار استراتژی‌ها برای یافتن old_string.
//
// وقتی تطبیق بایت‌به‌بایت (exact) نتیجه نداد، ویرایش به‌ترتیب با این
// استراتژی‌های سهل‌گیرتر دوباره امتحان می‌شود:
//
// 1. exact — تطبیق دقیق (رفتار همیشگی؛ بدون هیچ تغییری).
// 2. eol — نرمال‌سازی CRLF/LF در هر دو طرف؛ هنگام نوشتن، سبک EOL خودِ
// فایل (و BOM آن) بایت‌به‌بایت حفظ می‌شود.
// 3. trail — به‌علاوه‌ی نادیده‌گرفتن فاصله‌های انتهایی خطوط (هر دو طرف).
// 4. indent — به‌علاوه‌ی نادیده‌گرفتن تورفتگی خطوط؛ «فقط وقتی تطبیق
// یکتا باشد» (حتی با replace_all) — چون جایگزینیِ تورفتگی‌ناشناس در
// چند جا می‌تواند فایل را خراب کند.
//
// پیاده‌سازی: از فایل یک «نمای نرمال‌شده» به‌همراه نگاشتِ آفست به بایت‌های
// اصلی ساخته می‌شود (normView)؛ تطبیق در نما انجام و بازه‌ی معادل در فایل
// اصلی جایگزین می‌شود. به این ترتیب BOM، EOLها و هر چیزی که بیرونِ بازه‌های
// جایگزینی است دست‌نخورده می‌ماند، و همه‌ی رخدادها (حتی با replace_all) در
// یک ساختِ درون‌حافظه‌ای و یک نوشتنِ واحد اعمال می‌شوند — ادیت چندگانه‌ی
// اتمیک: یا همه، یا هیچ.
//
// نکته‌ی زبانی (بخش ۲ پرامپت): پیام‌های این فایل رو به مدل و انگلیسی‌اند؛
// کامنت‌ها فارسی. هیچ نرمال‌سازی ی/ي و ك/ک انجام نمی‌شود — فقط EOL،
// فاصله‌ی انتهایی/ابتداییِ خط و BOMِ ابتدای فایل (که فقط برای «دیدن»
// حذف می‌شود، نه برای نوشتن).
package main

import (
	"bytes"
	"fmt"
	"strings"
)

// editMatchLevel یک سطح از آبشار تطبیق.
type editMatchLevel struct {
	name          string // برچسب گزارش‌دهی (انگلیسی، رو به مدل)
	ignoreTrail   bool   // فاصله‌های انتهایی خط نادیده گرفته شود؟
	ignoreIndent  bool   // تورفتگی ابتدای خط نادیده گرفته شود؟
	requireUnique bool   // این سطح فقط با تطبیق یکتا اجرا شود؟
}

// editMatchLevels آبشار به‌ترتیب سخت‌گیر → سهل‌گیر. هر سطح شامل
// سازوکارهای سطح قبل هم هست (تجمعی).
var editMatchLevels = []editMatchLevel{
	{name: "exact"},
	{name: "line-ending normalization (CRLF/LF)"},
	{name: "trailing-whitespace tolerance", ignoreTrail: true},
	{name: "indentation tolerance", ignoreTrail: true, ignoreIndent: true, requireUnique: true},
}

// editBOM پیشوند BOM احتمالی ابتدای فایل.
const editBOM = "\uFEFF"

// maxFuzzyEditBytes سقف حجم منبع برای سطوح سهل‌گیر (غیر از exact). بالاتر از
// این، تطبیق مقاوم اجرا نمی‌شود و مدل باید old_string دقیق بفرستد — این هم
// حافظه را کران‌دار می‌کند و هم ریسک تطبیقِ اشتباه در فایل غول را کم می‌کند.
// (فایل تا maxEditBytes=5MiB قابل ویرایش است، پس مسیر normal همیشه ≤2MiB است.)
const maxFuzzyEditBytes = 2 * 1024 * 1024

// normView نمای نرمال‌شده‌ی src + نگاشت بازگشتی به بایت‌های اصلی.
//
// هر بایتِ نما یک «واحد» است؛ starts[i]/ends[i] بازه‌ی بایتی src است که
// واحد i از آن ساخته شده. تنها واحد دوبایتی، «\n» حاصل از CRLF است؛
// بایت‌های حذف‌شده (EOL دوم، فاصله‌ی انتهایی/ابتداییِ خط، BOM) به‌صورت
// «شکاف» بین واحدها مدل می‌شوند و در نگاشت بازه دیده می‌شوند.
//
// آفست‌ها int32 هستند (نصفِ حافظه‌ی int64): این ساختار برای هر بایت منبع دو
// آفست نگه می‌دارد، پس با 5MiB و int64 حدود ۸۰MB می‌شد — بیش از سقف ~64MB.
// تابع فقط برای منابعِ کوچک‌تر از maxFuzzyEditBytes صدا زده می‌شود (پیاده‌سازی
// toolEditFile) و آن هم خودش زیر maxEditBytes است، پس int32 کافی است.
type normView struct {
	norm   []byte
	starts []int32
	ends   []int32
}

// buildNormView نمای نرمال‌شده را می‌سازد.
//
// قواعد:
// - BOM ابتدای فایل همیشه از نما حذف می‌شود تا old_stringِ بدون BOM هم
// بتواند ابتدای فایل را ببیند؛ اما چون نگاشت از بعدِ BOM شروع می‌شود،
// بایت‌های BOM هرگز داخل بازه‌ی جایگزینی نمی‌افتند.
// - CRLF و LF هر دو به «\n» واحد تبدیل می‌شوند (سطح ۲ به بعد).
// - ignoreTrail: فاصله‌ها/تب‌های انتهای هر خط از نما حذف می‌شوند.
// - ignoreIndent: فاصله‌ها/تب‌های ابتدای هر خط از نما حذف می‌شوند.
// - «\r» تنها (بدون «\n» بعدش) مثل قبل محتواست — EOL حساب نمی‌شود.
//
// ظرفیت از پیش اختصاص داده می‌شود (نما هرگز بلندتر از src نیست) تا مسیرِ
// fallback بدون تخصیص‌های تکراری بماند.
func buildNormView(src string, ignoreTrail, ignoreIndent bool) *normView {
	v := &normView{
		norm:   make([]byte, 0, len(src)),
		starts: make([]int32, 0, len(src)),
		ends:   make([]int32, 0, len(src)),
	}
	emit := func(b byte, s, e int) {
		v.norm = append(v.norm, b)
		v.starts = append(v.starts, int32(s))
		v.ends = append(v.ends, int32(e))
	}
	i := 0
	n := len(src)
	if strings.HasPrefix(src, editBOM) {
		i = len(editBOM)
	}
	for i < n {
		// تورفتگی ابتدای خط
		if ignoreIndent {
			for i < n && (src[i] == ' ' || src[i] == '\t') {
				i++
			}
		}
		contentStart := i
		// پایان خط: اولین «\n»
		k := i
		for k < n && src[k] != '\n' {
			k++
		}
		// EOL دو بایتی «\r\n»؟ (بایت قبل از «\n»؛ فاصله‌ی انتهایی هرگز
		// «\r» را نمی‌خورد، پس همین شرط هم برای محتوا هم برای واحد EOL
		// درست است — حتی وقتی فاصله‌ها بین محتوا و «\r» حذف شده‌اند.)
		contentStop := k
		crlf := k > contentStart && src[k-1] == '\r'
		if crlf {
			contentStop = k - 1
		}
		// فاصله‌های انتهایی خط
		if ignoreTrail {
			for contentStop > contentStart &&
				(src[contentStop-1] == ' ' || src[contentStop-1] == '\t') {
				contentStop--
			}
		}
		for c := contentStart; c < contentStop; c++ {
			emit(src[c], c, c+1)
		}
		if k >= n {
			break // EOF بدون EOL
		}
		if crlf {
			emit('\n', k-1, k+1)
		} else {
			emit('\n', k, k+1)
		}
		i = k + 1
	}
	return v
}

// normalizeEditNeedle سمت old_string را با همان پرچم‌ها نرمال می‌کند
// (فقط رشته؛ نگاشت لازم ندارد چون متن جایگزین، new_string است).
func normalizeEditNeedle(s string, ignoreTrail, ignoreIndent bool) string {
	if strings.HasPrefix(s, editBOM) {
		s = s[len(editBOM):]
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !ignoreTrail && !ignoreIndent {
		return s
	}
	lines := strings.Split(s, "\n")
	var b strings.Builder
	b.Grow(len(s))
	for idx, ln := range lines {
		if ignoreIndent {
			ln = strings.TrimLeft(ln, " \t")
		}
		if ignoreTrail {
			ln = strings.TrimRight(ln, " \t")
		}
		if idx > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(ln)
	}
	return b.String()
}

// editRegion یک بازه‌ی جایگزینی در بایت‌های اصلی فایل.
type editRegion struct {
	start, end int
}

// findEditRegions رخدادهای ناپیوسته‌ی needle در نما را به بازه‌های src
// تبدیل می‌کند (به‌ترتیب و بدون هم‌پوشانی).
//
// قواعد مرزبندی بازه:
// - شروع: آفست واحد نخستِ تطبیق؛ اگر تطبیق در ابتدای خطِ نما شروع شود و
// ignoreIndent فعال باشد، بازه به ابتدای واقعی خط در src گسترش می‌یابد
// تا تورفتگی اصلی هم جایگزین شود (BOM هرگز بلعیده نمی‌شود).
// - پایان: اگر تطبیق به «\n» ختم شود، دقیقاً پس از بایت‌های همان EOL
// (تورفتگیِ خط بعدی بلعیده نمی‌شود)؛ وگرنه تا واحدِ بعدی — یعنی
// فاصله‌های انتهاییِ حذف‌شده‌ی همان خط داخل بازه می‌آیند.
func findEditRegions(src string, v *normView, needle string, ignoreIndent bool) []editRegion {
	nb := []byte(needle)
	var regions []editRegion
	off := 0
	for {
		rel := bytes.Index(v.norm[off:], nb)
		if rel < 0 {
			break
		}
		s := off + rel
		e := s + len(nb)
		off = e

		start := int(v.starts[s])
		if ignoreIndent && (s == 0 || v.norm[s-1] == '\n') {
			if s > 0 {
				start = int(v.ends[s-1]) // ابتدای واقعی خط در src
			} else if strings.HasPrefix(src, editBOM) {
				start = len(editBOM) // بعد از BOM
			} else {
				start = 0
			}
		}
		end := len(src)
		if v.norm[e-1] == '\n' {
			end = int(v.ends[e-1]) // پایانِ درستِ EOL
		} else if e < len(v.norm) {
			end = int(v.starts[e]) // فاصله‌ی انتهایی حذف‌شده هم داخل بازه
		}
		regions = append(regions, editRegion{start: start, end: end})
	}
	return regions
}

// adaptEditEOL سبک EOL متن جایگزین را با سبک بازه‌ی متناظر هم‌راستا
// می‌کند تا ویرایش، سبک خطوط فایل را تغییر ندهد:
// - بازه‌ی CRLF → new_string با CRLF نوشته می‌شود؛
// - بازه‌ی LF → new_string با LF نوشته می‌شود؛
// - بازه‌ی تک‌خطی (بدون EOL) → سبک غالب کل فایل ملاک است.
func adaptEditEOL(newStr, region, wholeFile string) string {
	crlf := strings.Contains(region, "\r\n")
	if !crlf && !strings.Contains(region, "\n") {
		crlf = strings.Contains(wholeFile, "\r\n")
	}
	norm := strings.ReplaceAll(newStr, "\r\n", "\n")
	if crlf {
		return strings.ReplaceAll(norm, "\n", "\r\n")
	}
	return norm
}

// editFirstLineIndent تورفتگی (فاصله/تب) نخستین خطِ غیرخالی را برمی‌گرداند —
// همان تورفتگی‌ای که مدل در old_string خودش استفاده کرده است.
func editFirstLineIndent(s string) string {
	ind, _ := indentOfFirstContentLine(s)
	return ind
}

// indentOfFirstContentLine تورفتگی نخستین خطِ غیرخالیِ s را برمی‌گرداند
// (خط‌های خالی/فقط‌فاصله رد می‌شوند)؛ ok=false اگر هیچ خطِ غیرخالی‌ای نباشد.
//
// برای هم‌ترازیِ تورفتگی از هر دو طرف استفاده می‌شود: نخستین خطِ غیرخالیِ
// old_string و نخستین خطِ غیرخالیِ بازه‌ی فایل. یعنی خط‌های خالیِ ابتداییِ
// هر دو طرف با هم رد می‌شوند، پس اگر old_string با خط خالی شروع شود، تورفتگی
// از خطِ خالیِ فایل (که تورفتگیِ بی‌معنا دارد) گرفته نمی‌شود.
func indentOfFirstContentLine(s string) (string, bool) {
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSuffix(ln, "\r")
		trimmed := strings.TrimLeft(ln, " \t")
		if trimmed == "" {
			continue // خط خالی/فقط فاصله
		}
		return ln[:len(ln)-len(trimmed)], true
	}
	return "", false
}

// reindentEditBlock متن را از تورفتگی from به to تغییر می‌دهد: هر خط غیرخالی
// که با from آغاز شود، پیشوندش با to عوض می‌شود؛ خط‌های کم‌عمق‌تر (که با from
// شروع نمی‌شوند) و خط‌های خالی دست‌نخورده می‌مانند. هیچ کاراکتری جز فاصله/تب
// تغییر نمی‌کند — حروف فارسی/عربی، ارقام و نیم‌فاصله (U+200C) بایت‌به‌بایت
// می‌مانند. EOL هر خط (LF یا CRLF) هم حفظ می‌شود.
func reindentEditBlock(s, from, to string) string {
	if from == to {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		body, cr := ln, ""
		if strings.HasSuffix(body, "\r") {
			body, cr = body[:len(body)-1], "\r"
		}
		if strings.TrimLeft(body, " \t") == "" {
			continue // خط خالی — تورفتگی ندارد که عوض شود
		}
		if strings.HasPrefix(body, from) {
			lines[i] = to + body[len(from):] + cr
		}
	}
	return strings.Join(lines, "\n")
}

// describeEditIndent توضیح کوتاهِ انگلیسیِ یک تورفتگی برای پیام نتیجه (رو به مدل).
func describeEditIndent(indent string) string {
	if indent == "" {
		return "no indentation"
	}
	spaces := strings.Count(indent, " ")
	tabs := strings.Count(indent, "\t")
	switch {
	case tabs == 0:
		if spaces == 1 {
			return "1 space"
		}
		return fmt.Sprintf("%d spaces", spaces)
	case spaces == 0:
		if tabs == 1 {
			return "1 tab"
		}
		return fmt.Sprintf("%d tabs", tabs)
	default:
		return fmt.Sprintf("mixed indentation (%d spaces + %d tabs)", spaces, tabs)
	}
}

// indentTolerantReplacement جایگزینِ نهاییِ سطح «تحمل تورفتگی» را می‌سازد.
// فقط وقتی اعمال می‌شود که بازه از ابتدای واقعی یک خط شروع شود (تطبیق
// میان‌خط تورفتگی ندارد) و تورفتگی قدیمیِ مدل با تورفتگی همان خط در فایل
// متفاوت باشد؛ در غیر این صورت new_string دست‌نخورده می‌ماند (رفتار قبلی).
//
// خروجی: (متن جایگزین، توضیح from، توضیح to، آیا دوباره‌تورفتگی انجام شد؟)
func indentTolerantReplacement(src, oldStr string, region editRegion, newStr string) (string, string, string, bool) {
	// «ابتدای یک خط» یعنی بازه از ابتدای واقعی خط شروع شود، یا خودِ بازه با
	// EOL شروع شود (تطبیق از «\n» پایانِ خطِ قبلی شروع شده — قدیمی‌ترین حالت
	// وقتی old_string با خط خالی آغاز می‌شود؛ در این صورت خطوط بازه هم ترازند).
	startsWithEOL := region.start < len(src) &&
		(src[region.start] == '\n' ||
			(src[region.start] == '\r' && region.start+1 < len(src) && src[region.start+1] == '\n'))
	atLineStart := region.start == 0 || src[region.start-1] == '\n' || startsWithEOL ||
		(strings.HasPrefix(src, editBOM) && region.start == len(editBOM))
	if !atLineStart {
		return newStr, "", "", false
	}
	// from و to باید از «همان خط منطقی» بیایند: نخستین خطِ غیرخالیِ
	// old_string و نخستین خطِ غیرخالیِ بازه‌ی فایل (خط‌های خالیِ ابتداییِ هر
	// دو طرف با هم رد می‌شوند). اگر old_string با خط خالی شروع شود و تورفتگی
	// از خطِ اولِ بازه (که خودش خطِ خالی است) گرفته شود، نتیجه غلط می‌شود.
	fromIndent, fromOK := indentOfFirstContentLine(oldStr)
	toIndent, toOK := indentOfFirstContentLine(src[region.start:region.end])
	if !fromOK || !toOK {
		// بازه (یا old_string) خط غیرخالی ندارد — چیزی برای هم‌ترازی نیست
		return newStr, "", "", false
	}
	if fromIndent == toIndent {
		return newStr, "", "", false
	}
	return reindentEditBlock(newStr, fromIndent, toIndent),
		describeEditIndent(fromIndent), describeEditIndent(toIndent), true
}

// applyEditRegions خروجی نهایی را می‌سازد: بازه‌ها به‌ترتیب با new_stringِ
// هم‌سبک‌شده جایگزین می‌شوند و هرچه بینشان است دست‌نخورده می‌ماند.
// بازه‌ها باید مرتب و ناهم‌پوشان باشند (خروجی findEditRegions همین‌طور است).
func applyEditRegions(src, newStr string, regions []editRegion) string {
	var b strings.Builder
	b.Grow(len(src) + len(newStr)*len(regions))
	prev := 0
	for _, r := range regions {
		b.WriteString(src[prev:r.start])
		b.WriteString(adaptEditEOL(newStr, src[r.start:r.end], src))
		prev = r.end
	}
	b.WriteString(src[prev:])
	return b.String()
}
