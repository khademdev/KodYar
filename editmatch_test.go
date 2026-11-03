// تست‌های آبشار تطبیق مقاوم edit_file — CRLF/LF، فاصله‌ی انتهایی،
// تورفتگی، حفظ BOM/EOL و اتمیک‌بودن ویرایش چندگانه.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// editSetup فایلی را داخل پوشه‌ی کاری تست می‌نویسد و مسیر نسبی‌اش را برمی‌گرداند.
func editSetup(t *testing.T, a *App, rel, content string) {
	t.Helper()
	writeWsFile(t, a.settings.WorkspacePath, rel, content)
}

// editRead محتوای فعلی فایل را از پوشه‌ی کاری می‌خواند.
func editRead(t *testing.T, a *App, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(a.settings.WorkspacePath, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEditExactStillWorks(t *testing.T) {
	a := newTestApp(t)
	editSetup(t, a, "a.txt", "hello\nworld\n")
	r, ok := a.toolEditFile(nil, "a.txt", "world", "there", false)
	if !ok {
		t.Fatalf("exact edit failed: %s", r)
	}
	if got := editRead(t, a, "a.txt"); got != "hello\nthere\n" {
		t.Fatalf("file = %q", got)
	}
	// پیام سطح exact همان پیام همیشگی است — بدون پسوند استراتژی
	if !strings.Contains(r, "replaced 1 occurrence(s)") || strings.Contains(r, "matched with") {
		t.Fatalf("exact message wrong: %s", r)
	}
}

func TestEditCRLFFileLFNeedle(t *testing.T) {
	a := newTestApp(t)
	editSetup(t, a, "crlf.txt", "a\r\nb\r\nc")
	// مدل با LF فرستاده؛ فایل CRLF است → تطبیق با نرمال‌سازی EOL
	r, ok := a.toolEditFile(nil, "crlf.txt", "a\nb", "X\nY", false)
	if !ok {
		t.Fatalf("eol-normalized edit failed: %s", r)
	}
	if got := editRead(t, a, "crlf.txt"); got != "X\r\nY\r\nc" {
		t.Fatalf("CRLF must be preserved, got %q", got)
	}
	if !strings.Contains(r, "line-ending normalization") {
		t.Fatalf("strategy must be reported: %s", r)
	}
}

func TestEditLFFileCRLFNeedle(t *testing.T) {
	a := newTestApp(t)
	editSetup(t, a, "lf.txt", "a\nb\nc")
	// مدل با CRLF فرستاده؛ فایل LF است → متن جدید به LF تنزل می‌کند
	r, ok := a.toolEditFile(nil, "lf.txt", "a\r\nb", "X\r\nY", false)
	if !ok {
		t.Fatalf("eol-normalized edit failed: %s", r)
	}
	if got := editRead(t, a, "lf.txt"); got != "X\nY\nc" {
		t.Fatalf("LF must be preserved, got %q", got)
	}
}

func TestEditEOLONonLinearOutsideRegionUntouched(t *testing.T) {
	a := newTestApp(t)
	// فایل با EOL مخلوط: فقط بازه‌ی ویرایش‌شده CRLF می‌شود؛ بقیه دست‌نخورده
	editSetup(t, a, "mix.txt", "a\r\nb\nc\r\n")
	r, ok := a.toolEditFile(nil, "mix.txt", "a\nb", "X\nY", false)
	if !ok {
		t.Fatalf("edit failed: %s", r)
	}
	if got := editRead(t, a, "mix.txt"); got != "X\r\nY\nc\r\n" {
		t.Fatalf("mixed EOL outside the region must stay untouched, got %q", got)
	}
}

func TestEditTrailingWhitespaceTolerance(t *testing.T) {
	a := newTestApp(t)
	editSetup(t, a, "trail.txt", "foo   \nbar   \nbaz")
	r, ok := a.toolEditFile(nil, "trail.txt", "foo\nbar", "FOO\nBAR", false)
	if !ok {
		t.Fatalf("trailing-ws edit failed: %s", r)
	}
	if got := editRead(t, a, "trail.txt"); got != "FOO\nBAR\nbaz" {
		t.Fatalf("file = %q", got)
	}
	if !strings.Contains(r, "trailing-whitespace tolerance") {
		t.Fatalf("strategy must be reported: %s", r)
	}
}

func TestEditTrailingWhitespaceReplaceAll(t *testing.T) {
	a := newTestApp(t)
	// دو تطبیق در سطح تحمل فاصله‌ی انتهایی + replace_all → هر دو اتمیک اعمال می‌شوند
	editSetup(t, a, "trail2.txt", "a  \nmid  \nz\na  \nmid  \nend")
	r, ok := a.toolEditFile(nil, "trail2.txt", "a\nmid", "N\nM", true)
	if !ok {
		t.Fatalf("trailing-ws replace_all failed: %s", r)
	}
	if got := editRead(t, a, "trail2.txt"); got != "N\nM\nz\nN\nM\nend" {
		t.Fatalf("file = %q", got)
	}
	if !strings.Contains(r, "replaced 2 occurrence(s)") || !strings.Contains(r, "trailing-whitespace tolerance") {
		t.Fatalf("message wrong: %s", r)
	}
	// همان فایل بدون replace_all → خطای چندتایی
	editSetup(t, a, "trail3.txt", "a  \nmid  \nz\na  \nmid  \nend")
	if r, ok = a.toolEditFile(nil, "trail3.txt", "a\nmid", "N", false); ok || !strings.Contains(r, "appears 2 time(s)") {
		t.Fatalf("multi-match without replace_all must fail: %v (%v)", r, ok)
	}
	if got := editRead(t, a, "trail3.txt"); got != "a  \nmid  \nz\na  \nmid  \nend" {
		t.Fatalf("file must be untouched after the error, got %q", got)
	}
}

func TestEditIndentationToleranceUnique(t *testing.T) {
	a := newTestApp(t)
	// مدل تورفتگی را حذف کرده؛ فقط با تطبیق یکتا پذیرفته می‌شود.
	// (انتظار این تست عمداً تغییر کرده — Bug 5: متن جایگزین دیگر عیناً با
	// تورفتگی مدل نوشته نمی‌شود، بلکه با تورفتگی واقعیِ همان بلوک هماهنگ می‌شود.)
	editSetup(t, a, "ind.go", "    if x {\n        work()\n    }")
	r, ok := a.toolEditFile(nil, "ind.go", "if x {\n    work()\n}", "if y {\n    work()\n}", false)
	if !ok {
		t.Fatalf("indent-tolerant edit failed: %s", r)
	}
	if got := editRead(t, a, "ind.go"); got != "    if y {\n        work()\n    }" {
		t.Fatalf("file = %q", got)
	}
	if !strings.Contains(r, "indentation tolerance") {
		t.Fatalf("strategy must be reported: %s", r)
	}
	if !strings.Contains(r, "re-indented from no indentation to 4 spaces") {
		t.Fatalf("re-indentation must be reported: %s", r)
	}
}

func TestEditIndentationMultipleMatchesFails(t *testing.T) {
	a := newTestApp(t)
	src := "  ab\n  cd\nq\n  ab\n  cd\nz"
	editSetup(t, a, "multi.go", src)
	// دو تطبیق تورفتگی‌ناشناس → حتی با replace_all هم نباید اجرا شود
	for _, ra := range []bool{false, true} {
		r, ok := a.toolEditFile(nil, "multi.go", "ab\ncd", "X\nY", ra)
		if ok || !strings.Contains(r, "UNIQUE") {
			t.Fatalf("indent fallback must require a unique match (replace_all=%v): %v (%v)", ra, r, ok)
		}
	}
	if got := editRead(t, a, "multi.go"); got != src {
		t.Fatalf("file must be untouched, got %q", got)
	}
}

func TestEditBOMPreserved(t *testing.T) {
	a := newTestApp(t)
	// مسیر exact: BOM بخشی از src است و کنار می‌ماند
	editSetup(t, a, "bom1.txt", "\uFEFFhello\nworld")
	if r, ok := a.toolEditFile(nil, "bom1.txt", "hello", "HELLO", false); !ok {
		t.Fatalf("exact edit on BOM file failed: %s", r)
	}
	if got := editRead(t, a, "bom1.txt"); got != "\uFEFFHELLO\nworld" {
		t.Fatalf("BOM must survive exact edit, got %q", got)
	}

	// مسیر fallback: تطبیق از بعدِ BOM شروع می‌شود و BOM در پیشوند می‌ماند
	editSetup(t, a, "bom2.txt", "\uFEFFa\r\nb")
	if r, ok := a.toolEditFile(nil, "bom2.txt", "a\nb", "X\nY", false); !ok {
		t.Fatalf("eol edit on BOM file failed: %s", r)
	}
	if got := editRead(t, a, "bom2.txt"); got != "\uFEFFX\r\nY" {
		t.Fatalf("BOM must survive fallback edit, got %q", got)
	}
}

func TestEditNotFoundReportsStrategies(t *testing.T) {
	a := newTestApp(t)
	editSetup(t, a, "nn.txt", "one\ntwo\n")
	r, ok := a.toolEditFile(nil, "nn.txt", "zzz", "y", false)
	if ok || !strings.Contains(r, "not found") {
		t.Fatalf("not-found must fail clearly: %v (%v)", r, ok)
	}
	if !strings.Contains(r, "exact") || !strings.Contains(r, "indentation tolerance") {
		t.Fatalf("error must list the tried strategies: %s", r)
	}
	if got := editRead(t, a, "nn.txt"); got != "one\ntwo\n" {
		t.Fatalf("file must be untouched, got %q", got)
	}
}

func TestEditPersianZWNJPreserved(t *testing.T) {
	a := newTestApp(t)
	// متن فارسی با نیم‌فاصله (ZWNJ) — هیچ نرمال‌سازی متنی نباید انجام شود؛
	// بایت‌به‌بایت حفظ می‌شود (بخش ۲ پرامپت).
	editSetup(t, a, "fa.txt", "می\u200cروم به خانه\nخط دوم")
	r, ok := a.toolEditFile(nil, "fa.txt", "می\u200cروم", "برمی\u200cگردم", false)
	if !ok {
		t.Fatalf("persian exact edit failed: %s", r)
	}
	if got := editRead(t, a, "fa.txt"); got != "برمی\u200cگردم به خانه\nخط دوم" {
		t.Fatalf("ZWNJ must survive byte-for-byte, got %q", got)
	}

	// سطح تحمل فاصله‌ی انتهایی با متن فارسی (چندبایتی)
	editSetup(t, a, "fa2.txt", "سلام   \nدنیا")
	r, ok = a.toolEditFile(nil, "fa2.txt", "سلام\nدنیا", "درود\nدنیا", false)
	if !ok {
		t.Fatalf("persian trailing-ws edit failed: %s", r)
	}
	if got := editRead(t, a, "fa2.txt"); got != "درود\nدنیا" {
		t.Fatalf("file = %q", got)
	}
	if !strings.Contains(r, "trailing-whitespace tolerance") {
		t.Fatalf("strategy must be reported: %s", r)
	}
}

func TestEditResultingFileTooLarge(t *testing.T) {
	a := newTestApp(t)
	editSetup(t, a, "big.txt", "x y x")
	// دو جایگزینی ۴ مگابایتی → خروجی ~۸MB > maxEditBytes → هیچ نوشته نمی‌شود
	big := strings.Repeat("N", 4*1024*1024)
	r, ok := a.toolEditFile(nil, "big.txt", " ", big, true)
	if ok || !strings.Contains(r, "too large") {
		t.Fatalf("oversized result must be rejected: %v (%v)", r[:min(len(r), 120)], ok)
	}
	if got := editRead(t, a, "big.txt"); got != "x y x" {
		t.Fatal("file must be untouched after the size rejection")
	}
}

// TestNormViewMapping واحد-تستِ نگاشت نما → بایت‌های اصلی (editmatch.go).
func TestNormViewMapping(t *testing.T) {
	// فاصله‌ی انتهایی + CRLF: بازه‌ی تطبیق باید فاصله‌های حذف‌شده را هم ببلعد
	src := "a  \r\nb"
	v := buildNormView(src, true, false)
	if string(v.norm) != "a\nb" {
		t.Fatalf("norm = %q", v.norm)
	}
	regions := findEditRegions(src, v, "a\nb", false)
	if len(regions) != 1 || regions[0] != (editRegion{start: 0, end: len(src)}) {
		t.Fatalf("region = %+v", regions)
	}

	// BOM: از نما حذف می‌شود اما بیرونِ بازه‌ی جایگزینی می‌ماند
	src = "\uFEFFa\nb"
	v = buildNormView(src, false, false)
	if string(v.norm) != "a\nb" || v.starts[0] != 3 {
		t.Fatalf("norm=%q starts0=%d", v.norm, v.starts[0])
	}
	regions = findEditRegions(src, v, "a\nb", false)
	if len(regions) != 1 || regions[0].start != 3 {
		t.Fatalf("region must start after the BOM: %+v", regions)
	}

	// تورفتگی: شروع تطبیق در ابتدای خطِ نما → بازه به ابتدای واقعی خط می‌رسد
	src = "  ab\ncd"
	v = buildNormView(src, true, true)
	if string(v.norm) != "ab\ncd" {
		t.Fatalf("norm = %q", v.norm)
	}
	regions = findEditRegions(src, v, "ab\ncd", true)
	if len(regions) != 1 || regions[0] != (editRegion{start: 0, end: len(src)}) {
		t.Fatalf("region must swallow the original indentation: %+v", regions)
	}

	// سوزن: نرمال‌سازی CRLF + فاصله‌ها + تورفتگی + حذف BOM
	if got := normalizeEditNeedle("  x  \r\n  y  ", true, true); got != "x\ny" {
		t.Fatalf("needle = %q", got)
	}
	if got := normalizeEditNeedle("\uFEFFa\r\nb", false, false); got != "a\nb" {
		t.Fatalf("needle level2 = %q", got)
	}

	// تطبیقِ منتهی به EOL نباید تورفتگیِ خط بعد را ببلعد
	src = "keep\n    next"
	v = buildNormView(src, true, true)
	regions = findEditRegions(src, v, "keep\n", true)
	if len(regions) != 1 || regions[0].end != 5 {
		t.Fatalf("match ending with EOL must end right after it: %+v", regions)
	}
}

// ----------------------------------------------------------------: هم‌راستایی تورفتگی

// TestEditIndentReindentPythonNested: بلوک تودرتوی پایتون — مدل ۴ فاصله، فایل
// ۸ فاصله؛ متن جایگزین باید با تورفتگی واقعی بلوک نوشته شود (ساختار حفظ شود).
func TestEditIndentReindentPythonNested(t *testing.T) {
	a := newTestApp(t)
	src := "def f():\n        if x:\n                work()\n        done()\n"
	editSetup(t, a, "p.py", src)
	r, ok := a.toolEditFile(nil, "p.py", "    if x:\n        work()", "    if y:\n        work2()", false)
	if !ok {
		t.Fatalf("indent-tolerant edit failed: %s", r)
	}
	// بلوکِ مدل ۴ فاصله بود و فرزندش ۸ (یعنی +۴)؛ روی پایه‌ی ۸ فاصله، فرزند ۱۲ می‌شود.
	want := "def f():\n        if y:\n            work2()\n        done()\n"
	if got := editRead(t, a, "p.py"); got != want {
		t.Fatalf("nested block must be re-indented from 4 to 8 spaces:\nwant %q\ngot  %q", want, got)
	}
	for _, want := range []string{"indentation tolerance", "re-indented from 4 spaces to 8 spaces"} {
		if !strings.Contains(r, want) {
			t.Fatalf("result message missing %q: %s", want, r)
		}
	}
}

// TestEditIndentReindentTabsToSpaces: فایل با تب، مدل با ۴ فاصله.
func TestEditIndentReindentTabsToSpaces(t *testing.T) {
	a := newTestApp(t)
	editSetup(t, a, "tabs.go", "\tif x {\n\t\twork()\n\t}\n")
	r, ok := a.toolEditFile(nil, "tabs.go", "    if x {\n        work()\n    }", "    if y {\n        done()\n    }", false)
	if !ok {
		t.Fatalf("indent-tolerant edit failed: %s", r)
	}
	// قاعده‌ی مشخص‌شده: فقط پیشوندِ تورفتگی خط اول (۴ فاصله) با تورفتگی فایل
	// (یک تب) عوض می‌شود؛ پس ادامه‌ی تورفتگی هر خط همان می‌ماند (+۴ فاصله).
	want := "\tif y {\n\t    done()\n\t}\n"
	if got := editRead(t, a, "tabs.go"); got != want {
		t.Fatalf("want tabs as base indent, got %q", got)
	}
	if !strings.Contains(r, "re-indented from 4 spaces to 1 tab") {
		t.Fatalf("re-indentation must be reported: %s", r)
	}
}

// TestEditIndentReindentSpacesToTabsIsPreserved: مدل تب دارد، فایل فاصله.
func TestEditIndentReindentSpacesToTabsIsPreserved(t *testing.T) {
	a := newTestApp(t)
	editSetup(t, a, "spaces.go", "    if x {\n        work()\n    }\n")
	r, ok := a.toolEditFile(nil, "spaces.go", "\tif x {\n\t\twork()\n\t}", "\tif y {\n\t\tdone()\n\t}", false)
	if !ok {
		t.Fatalf("indent-tolerant edit failed: %s", r)
	}
	// پایه‌ی فایل ۴ فاصله است؛ پیشوندِ یک تبِ مدل با همان ۴ فاصله عوض می‌شود.
	want := "    if y {\n    \tdone()\n    }\n"
	if got := editRead(t, a, "spaces.go"); got != want {
		t.Fatalf("spaces style must be the base indent, got %q", got)
	}
}

// TestEditIndentHelpersEqualIndentUntouched: تورفتگی برابر → هیچ تغییری (رفتار
// قبلی) و helper گزارش نمی‌دهد که دوباره‌تورفتگی کرده است.
func TestEditIndentHelpersEqualIndentUntouched(t *testing.T) {
	src := "    a()\n    b()\n"
	region := editRegion{start: 0, end: len(src)}
	out, _, _, reindented := indentTolerantReplacement(src, "    a()\n    b()", region, "    X()\n    Y()")
	if reindented {
		t.Fatal("equal indentation must not be re-indented")
	}
	if out != "    X()\n    Y()" {
		t.Fatalf("replacement must stay verbatim, got %q", out)
	}
	if got := reindentEditBlock("  a\n", "  ", "  "); got != "  a\n" {
		t.Fatalf("reindentEditBlock with equal indents must be a no-op, got %q", got)
	}
	if got := editFirstLineIndent("\n\t\n  x\n"); got != "  " {
		t.Fatalf("first non-blank line indent = %q", got)
	}
	if got := describeEditIndent("\t\t"); got != "2 tabs" {
		t.Fatalf("describeEditIndent = %q", got)
	}
	if got := describeEditIndent(""); got != "no indentation" {
		t.Fatalf("describeEditIndent(empty) = %q", got)
	}
}

// TestEditIndentReindentKeepsDedentAndBlankLines: خط اول عمیق‌تر از خطوط بعدی
// (dedent نسبی) و خط خالی حفظ می‌شوند.
func TestEditIndentReindentKeepsDedentAndBlankLines(t *testing.T) {
	a := newTestApp(t)
	editSetup(t, a, "dedent.txt", "        a\n            b\n        c\n")
	r, ok := a.toolEditFile(nil, "dedent.txt", "    a\n        b", "    X\n        Y\n\n    Z", false)
	if !ok {
		t.Fatalf("indent-tolerant edit failed: %s", r)
	}
	want := "        X\n            Y\n\n        Z\n        c\n"
	if got := editRead(t, a, "dedent.txt"); got != want {
		t.Fatalf("dedent/blank lines mishandled:\nwant %q\ngot  %q", want, got)
	}
}

// TestEditIndentReindentCRLF: فایل CRLF — هم‌سبک‌سازی EOL بعد از تورفتگی.
func TestEditIndentReindentCRLF(t *testing.T) {
	a := newTestApp(t)
	editSetup(t, a, "crlf.py", "        a\r\n            b\r\n")
	r, ok := a.toolEditFile(nil, "crlf.py", "    a\n        b", "    X\n        Y", false)
	if !ok {
		t.Fatalf("indent-tolerant edit failed: %s", r)
	}
	want := "        X\r\n            Y\r\n"
	if got := editRead(t, a, "crlf.py"); got != want {
		t.Fatalf("CRLF must be preserved with new indentation, got %q", got)
	}
}

// TestEditIndentReindentPersianZWNJ: متن فارسی با نیم‌فاصله و حروف ک/ی باید
// بایت‌به‌بایت حفظ شود؛ فقط فاصله‌های تورفتگی عوض می‌شوند.
func TestEditIndentReindentPersianZWNJ(t *testing.T) {
	a := newTestApp(t)
	editSetup(t, a, "fa.py", "        # می\u200cخواهم ك\u200cکار كنم\n            خط دوم\n")
	r, ok := a.toolEditFile(nil, "fa.py", "    # می\u200cخواهم ك\u200cکار كنم\n        خط دوم", "    # می\u200cخواهم ك\u200cکار تازه\n        خط سوم", false)
	if !ok {
		t.Fatalf("indent-tolerant edit failed: %s", r)
	}
	want := "        # می\u200cخواهم ك\u200cکار تازه\n            خط سوم\n"
	if got := editRead(t, a, "fa.py"); got != want {
		t.Fatalf("persian text/ZWNJ must survive byte-exact with new indentation:\nwant %q\ngot  %q", want, got)
	}
}

// TestEditIndentMidLineNoReindent: تطبیق از میان خط (نه ابتدای خط) تورفتگی
// ندارد و نباید دوباره‌تورفتگی شود.
func TestEditIndentMidLineNoReindent(t *testing.T) {
	a := newTestApp(t)
	editSetup(t, a, "mid.txt", "  x = foo(a)\r\n  bar\r\n")
	r, ok := a.toolEditFile(nil, "mid.txt", "foo(a)\nbar", "  X\n  Y", false)
	if !ok {
		t.Fatalf("eol-tolerant edit failed: %s", r)
	}
	// بدون دوباره‌تورفتگی: متن تطبیق (میان‌خط) عیناً جایگزین می‌شود.
	want := "  x =   X\r\n  Y\r\n"
	if got := editRead(t, a, "mid.txt"); got != want {
		t.Fatalf("mid-line match must not be re-indented, got %q", got)
	}
	if strings.Contains(r, "re-indented from") {
		t.Fatalf("mid-line match must not report re-indentation: %s", r)
	}
}

// TestEditIndentReindentBOM: فایل با BOM — BOM حفظ می‌شود و بازه از بعدِ BOM
// تورفتگی را هم‌راستا می‌کند.
func TestEditIndentReindentBOM(t *testing.T) {
	a := newTestApp(t)
	editSetup(t, a, "bom.py", "\uFEFF        a\n            b\n")
	r, ok := a.toolEditFile(nil, "bom.py", "    a\n        b", "    X\n        Y", false)
	if !ok {
		t.Fatalf("indent-tolerant edit failed: %s", r)
	}
	want := "\uFEFF        X\n            Y\n"
	if got := editRead(t, a, "bom.py"); got != want {
		t.Fatalf("BOM must survive re-indenting, got %q", got)
	}
}

// ---------------------------------------------------------------- سقف حافظه‌ی تطبیق (Bug 7)

// fuzzyEditFixture فایلی با یک بلوک هدف + پرکننده می‌سازد تا حجم دقیقاً
// targetBytes شود (پرکننده "p\n" است — هر واحد ۲ بایت).
func fuzzyEditFixture(t *testing.T, a *App, rel string, targetBytes int) {
	t.Helper()
	head := "        a\n            b\n"
	pad := targetBytes - len(head)
	if pad < 0 {
		t.Fatalf("target size too small: %d", targetBytes)
	}
	writeWsFile(t, a.settings.WorkspacePath, rel, head+strings.Repeat("p\n", pad/2))
}

// TestEditFuzzySkippedAboveCap: منبع بزرگ‌تر از سقف → سطوح سهل‌گیر اجرا نمی‌شوند
// و خطای «dقیق بفرست» + راهنمای حجم برمی‌گردد؛ تطبیق exact مثل قبل کار می‌کند.
func TestEditFuzzySkippedAboveCap(t *testing.T) {
	a := newTestApp(t)
	fuzzyEditFixture(t, a, "huge.txt", maxFuzzyEditBytes+512)
	r, ok := a.toolEditFile(nil, "huge.txt", "    a\n        b", "    X\n        Y", false)
	if ok {
		t.Fatal("tolerant matching must be skipped above maxFuzzyEditBytes")
	}
	for _, want := range []string{"too large", "exact old_string", "exact byte-for-byte"} {
		if !strings.Contains(r, want) {
			t.Fatalf("error must explain the size limit (%q): %s", want, r)
		}
	}
	// تطبیق دقیق روی همان فایل بزرگ باید دست‌نخورده کار کند
	r, ok = a.toolEditFile(nil, "huge.txt", "        a", "        A", false)
	if !ok {
		t.Fatalf("exact edit on a large file must still work: %s", r)
	}
	if got := editRead(t, a, "huge.txt"); !strings.HasPrefix(got, "        A\n            b\n") {
		t.Fatalf("exact edit on large file produced %q", got[:40])
	}
}

// TestEditFuzzyWorksBelowCap: درست زیر سقف، تطبیق مقاوم تورفتگی مثل قبل کار
// می‌کند (با دوباره‌تورفتگی Bug 5).
func TestEditFuzzyWorksBelowCap(t *testing.T) {
	a := newTestApp(t)
	fuzzyEditFixture(t, a, "big.txt", maxFuzzyEditBytes-256)
	r, ok := a.toolEditFile(nil, "big.txt", "    a\n        b", "    X\n        Y", false)
	if !ok {
		t.Fatalf("tolerant matching must still work below the cap: %s", r)
	}
	if !strings.Contains(r, "indentation tolerance") {
		t.Fatalf("strategy must be reported: %s", r)
	}
	if got := editRead(t, a, "big.txt"); !strings.HasPrefix(got, "        X\n            Y\n") {
		t.Fatalf("re-indented result expected, got %q", got[:40])
	}
}

// ---------------------------------------------------------------- old_string با خط خالیِ ابتدایی

// TestEditIndentLeadingBlankLines: وقتی old_string با خط خالی شروع می‌شود،
// تورفتگیِ مبدأ و مقصد باید از «همان خط منطقی» گرفته شوند (نخستین خطِ غیرخالیِ
// old_string ↔ همان خط در بازه‌ی فایل) — نه از خطِ خالیِ اولِ فایل.
func TestEditIndentLeadingBlankLines(t *testing.T) {
	cases := []struct {
		name string
		file string
		old  string
		new  string
		want string
	}{
		{
			name: "one leading blank line (python, 4 vs 8 spaces)",
			file: "def f():\n\n        if x:\n                work()\n        done()\n",
			old:  "\n    if x:\n        work()",
			new:  "\n    if y:\n        work2()",
			want: "def f():\n\n        if y:\n            work2()\n        done()\n",
		},
		{
			// خطِ خالی فقط در old_string است (فایل آنجا خطِ خالی ندارد): بازه از
			// EOL خطِ قبلی شروع می‌شود و باز هم باید هم‌تراز شود.
			name: "blank line only in old_string",
			file: "def f():\n        if x:\n                work()\n        done()\n",
			old:  "\n    if x:\n        work()",
			new:  "\n    if y:\n        work2()",
			want: "def f():\n        if y:\n            work2()\n        done()\n",
		},
		{
			// دو خطِ خالی در old_string و دو خطِ خالی در فایل
			name: "two leading blank lines",
			file: "def f():\n\n\n        if x:\n                work()\n",
			old:  "\n\n    if x:\n        work()",
			new:  "\n\n    if y:\n        work()",
			want: "def f():\n\n\n        if y:\n            work()\n",
		},
		{
			// خطِ خالیِ دارای فاصله در old_string: خطوط خالی دست‌نخورده می‌مانند
			// (فقط خطوط غیرخالی دوباره‌تورفتگی می‌گیرند)
			name: "whitespace-only leading line",
			file: "def f():\n\n        if x:\n                work()\n        done()\n",
			old:  "   \n    if x:\n        work()",
			new:  "   \n    if y:\n        work2()",
			want: "def f():\n   \n        if y:\n            work2()\n        done()\n",
		},
		{
			name: "CRLF with a real blank line",
			file: "def f():\r\n\r\n        if x:\r\n                work()\r\n",
			old:  "\r\n    if x:\r\n        work()",
			new:  "\r\n    if y:\r\n        work2()",
			want: "def f():\r\n\r\n        if y:\r\n            work2()\r\n",
		},
		{
			// نیم‌فاصله (U+200C) و حروف فارسی باید بایت‌به‌بایت بمانند
			name: "persian comment with ZWNJ after the blank line",
			file: "def f():\n\n        # سلام\u200cدنیا\n        work()\n",
			old:  "\n    # سلام\u200cدنیا\n        work()",
			new:  "\n    # سلام\u200cدنیا (ویرایش)\n        done()",
			want: "def f():\n\n        # سلام\u200cدنیا (ویرایش)\n            done()\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			editSetup(t, a, "x.py", tc.file)
			r, ok := a.toolEditFile(nil, "x.py", tc.old, tc.new, false)
			if !ok {
				t.Fatalf("indent-tolerant edit failed: %s", r)
			}
			if got := editRead(t, a, "x.py"); got != tc.want {
				t.Fatalf("wrong re-indent:\nwant %q\ngot  %q", tc.want, got)
			}
		})
	}
}

// TestIndentTolerantReplacementBlankRegionUntouched: اگر خطِ غیرخالی‌ای برای
// هم‌ترازی نباشد (بازه فقط خطِ خالی دارد)، new_string دست‌نخورده می‌ماند.
func TestIndentTolerantReplacementBlankRegionUntouched(t *testing.T) {
	src := "a\n\n\nb"
	got, from, to, changed := indentTolerantReplacement(src, "\n", editRegion{start: 2, end: 3}, "\nX")
	if got != "\nX" || from != "" || to != "" || changed {
		t.Fatalf("blank-only region must stay untouched, got %q (%q→%q, changed=%v)", got, from, to, changed)
	}
}
