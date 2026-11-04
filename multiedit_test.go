// تست‌های شکل چندویرایشی edit_file (آرایه‌ی edits) — وابستگی ترتیبی،
// اتمیک‌بودن (همه یا هیچ)، CRLF/BOM، محتوای فارسی با ZWNJ، سقف‌ها و
// تحمل نوع ضعیف.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAppWsFile(t *testing.T, a *App, rel, content string) {
	t.Helper()
	p := filepath.Join(a.settings.WorkspacePath, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readAppWsFile(t *testing.T, a *App, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(a.settings.WorkspacePath, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestMultiEditThreeDependentEdits(t *testing.T) {
	a := newTestApp(t)
	writeAppWsFile(t, a, "app.go", "package main\n\nfunc main() {\n\toldOne()\n\toldTwo()\n\toldThree()\n}\n")

	// سه ویرایش وابسته: دومی متنی را می‌گذارد که سومی همان را برمی‌دارد
	res, ok := a.execTool("edit_file", `{"path":"app.go","edits":[
		{"old_string":"oldOne()","new_string":"stepOne()"},
		{"old_string":"stepOne()","new_string":"stepOneDone()"},
		{"old_string":"oldThree()","new_string":"stepThree()"}
	]}`)
	if !ok || !strings.Contains(res, "applied 3 edit(s)") {
		t.Fatalf("multi-edit failed: %v %v", res, ok)
	}
	got := readAppWsFile(t, a, "app.go")
	want := "package main\n\nfunc main() {\n\tstepOneDone()\n\toldTwo()\n\tstepThree()\n}\n"
	if got != want {
		t.Fatalf("dependent edits not applied sequentially:\n got: %q\nwant: %q", got, want)
	}
}

func TestMultiEditAllOrNothingSecondFails(t *testing.T) {
	a := newTestApp(t)
	orig := "line A\nline B\nline C\n"
	writeAppWsFile(t, a, "f.txt", orig)

	// ویرایش دوم old_string ندارد → کل عملیات باید بی‌اثر بماند
	res, ok := a.execTool("edit_file", `{"path":"f.txt","edits":[
		{"old_string":"line A","new_string":"LINE A"},
		{"old_string":"NOPE missing","new_string":"X"}
	]}`)
	if ok {
		t.Fatalf("second edit must fail, got success: %v", res)
	}
	if !strings.Contains(res, "edit #2 (of 2)") || !strings.Contains(res, "no changes were written") {
		t.Fatalf("error must name the failing edit index: %v", res)
	}
	if !strings.Contains(res, "old_string not found") {
		t.Fatalf("error must carry the inner reason: %v", res)
	}
	// فایل باید بایت‌به‌بایت دست‌نخورده مانده باشد
	if got := readAppWsFile(t, a, "f.txt"); got != orig {
		t.Fatalf("file must stay byte-identical on failure:\n got: %q\nwant: %q", got, orig)
	}
}

func TestMultiEditAmbiguityNamesIndex(t *testing.T) {
	a := newTestApp(t)
	writeAppWsFile(t, a, "dup.txt", "v\nv\nv\n")

	res, ok := a.execTool("edit_file", `{"path":"dup.txt","edits":[
		{"old_string":"v","new_string":"w"},
		{"old_string":"v","new_string":"x"}
	]}`)
	if ok {
		t.Fatalf("ambiguous first edit must fail: %v", res)
	}
	// ابهام در ویرایش ۱ (بدون replace_all) — شماره‌ی همان ویرایش باید در خطا باشد
	if !strings.Contains(res, "edit #1 (of 2)") || !strings.Contains(res, "appears 3 time(s)") {
		t.Fatalf("ambiguity error must name index and count: %v", res)
	}
	if got := readAppWsFile(t, a, "dup.txt"); got != "v\nv\nv\n" {
		t.Fatalf("file must be untouched, got %q", got)
	}

	// ابهام در ویرایش دوم (اولی موفق) — باز هم هیچ‌چیز نوشته نمی‌شود
	res, ok = a.execTool("edit_file", `{"path":"dup.txt","edits":[
		{"old_string":"v\nv\nv","new_string":"first block"},
		{"old_string":"v","new_string":"w"}
	]}`)
	if ok || !strings.Contains(res, "edit #2 (of 2)") {
		t.Fatalf("second-edit ambiguity must name edit #2: %v", res)
	}
	if got := readAppWsFile(t, a, "dup.txt"); got != "v\nv\nv\n" {
		t.Fatalf("file must still be untouched after partial plan, got %q", got)
	}
}

func TestMultiEditCRLFAndBOMPreserved(t *testing.T) {
	a := newTestApp(t)
	// BOM + CRLF — آبشار تطبیق باید هر دو را بایت‌به‌بایت حفظ کند
	orig := "\uFEFFfirst line\r\nsecond line\r\nthird line\r\n"
	writeAppWsFile(t, a, "win.txt", orig)

	res, ok := a.execTool("edit_file", `{"path":"win.txt","edits":[
		{"old_string":"first line","new_string":"خط اول"},
		{"old_string":"third line","new_string":"سوم"}
	]}`)
	if !ok {
		t.Fatalf("CRLF+BOM multi-edit failed: %v", res)
	}
	got := readAppWsFile(t, a, "win.txt")
	want := "\uFEFFخط اول\r\nsecond line\r\nسوم\r\n"
	if got != want {
		t.Fatalf("BOM/CRLF not preserved:\n got: %q\nwant: %q", got, want)
	}
}

func TestMultiEditPersianZWNJContent(t *testing.T) {
	a := newTestApp(t)
	// متن فارسی با نیم‌فاصله — هیچ نرمال‌سازی‌ای نباید اتفاق بیفتد
	orig := "تابع‌های اصلی\nپردازش‌گر داده\nپایان\n"
	writeAppWsFile(t, a, "fa.txt", orig)

	res, ok := a.execTool("edit_file", `{"path":"fa.txt","edits":[
		{"old_string":"تابع‌های اصلی","new_string":"توابع اصلیِ بازنویسی‌شده"},
		{"old_string":"پایان","new_string":"پایانِ کار"}
	]}`)
	if !ok {
		t.Fatalf("Persian multi-edit failed: %v", res)
	}
	got := readAppWsFile(t, a, "fa.txt")
	if !strings.Contains(got, "توابع اصلیِ بازنویسی‌شده") || !strings.Contains(got, "پایانِ کار") {
		t.Fatalf("Persian content with ZWNJ must be preserved byte-for-byte, got %q", got)
	}
	if strings.Contains(got, "وظایف") {
		t.Fatal("unexpected replacement")
	}
	// ZWNJ باید هنوز در فایل باشد
	if !strings.Contains(got, "\u200c") {
		t.Fatal("ZWNJ (U+200C) must survive the edit")
	}
}

func TestMultiEditSizeCapAndLimits(t *testing.T) {
	a := newTestApp(t)
	writeAppWsFile(t, a, "cap.txt", "hello\n")

	// بیشتر از ۲۰ ویرایش → خطا پیش از اجرا
	var edits strings.Builder
	edits.WriteByte('[')
	for i := 0; i < maxEditOps+1; i++ {
		if i > 0 {
			edits.WriteByte(',')
		}
		fmt.Fprintf(&edits, `{"old_string":"h","new_string":"h%d"}`, i)
	}
	edits.WriteByte(']')
	res, ok := a.execTool("edit_file", `{"path":"cap.txt","edits":`+edits.String()+`}`)
	if ok || !strings.Contains(res, fmt.Sprintf("at most %d edit(s), got %d", maxEditOps, maxEditOps+1)) {
		t.Fatalf("edit count cap must be enforced: %v", res)
	}

	// خالی → خطا
	if res, ok = a.execTool("edit_file", `{"path":"cap.txt","edits":[]}`); ok || !strings.Contains(res, "at least one edit") {
		t.Fatalf("empty edits must fail: %v", res)
	}

	// حداکثر مجاز (۲۰ ویرایش) باید کار کند — زنجیره‌ی ترتیبی با نشانگرهای یکتا:
	// ویرایش i مقدار «step i-1» را می‌بیند و به «step i» تبدیل می‌کند.
	writeAppWsFile(t, a, "chain.txt", "step 0\n")
	edits.Reset()
	edits.WriteByte('[')
	for i := 0; i < maxEditOps; i++ {
		if i > 0 {
			edits.WriteByte(',')
		}
		fmt.Fprintf(&edits, `{"old_string":"step %d","new_string":"step %d"}`, i, i+1)
	}
	edits.WriteByte(']')
	if res, ok = a.execTool("edit_file", `{"path":"chain.txt","edits":`+edits.String()+`}`); !ok {
		t.Fatalf("20 sequential edits must succeed: %v", res)
	}
	if got := readAppWsFile(t, a, "chain.txt"); got != fmt.Sprintf("step %d\n", maxEditOps) {
		t.Fatalf("sequential chain result wrong: %q", got)
	}

	// سقف حجم خروجی: ویرایشی که فایل را از maxEditBytes بزرگ‌تر می‌کند
	writeAppWsFile(t, a, "big.txt", "mark\n")
	huge := strings.Repeat("y", maxEditBytes+1024)
	if res, ok = a.execTool("edit_file", `{"path":"big.txt","edits":[{"old_string":"mark","new_string":`+fmt.Sprintf("%q", huge)+`}]}`); ok {
		t.Fatalf("result size cap must abort: %v", res)
	} else if !strings.Contains(res, "too large") {
		t.Fatalf("size cap error must say too large: %v", res)
	}
	if got := readAppWsFile(t, a, "big.txt"); got != "mark\n" {
		t.Fatalf("file must be untouched after size-cap abort, got %q", got)
	}
}

func TestMultiEditLooseTypingJSONString(t *testing.T) {
	a := newTestApp(t)
	writeAppWsFile(t, a, "loose.txt", "aaa\nbbb\n")

	// آرایه به‌صورت رشتهٔ JSON (مدل‌های متن‌باز) — باید پذیرفته شود
	editsJSON := `[{"old_string":"aaa","new_string":"AAA"},{"old_string":"bbb","new_string":"BBB"}]`
	res, ok := a.execTool("edit_file", fmt.Sprintf(`{"path":"loose.txt","edits":%q}`, editsJSON))
	if !ok || !strings.Contains(res, "applied 2 edit(s)") {
		t.Fatalf("JSON-string edits must be accepted: %v %v", res, ok)
	}
	if got := readAppWsFile(t, a, "loose.txt"); got != "AAA\nBBB\n" {
		t.Fatalf("JSON-string edits not applied: %q", got)
	}

	// رشتهٔ غیر JSON → خطای دقیق
	if res, ok = a.execTool("edit_file", `{"path":"loose.txt","edits":"not json"}`); ok || !strings.Contains(res, `field "edits" must be an array`) {
		t.Fatalf("non-JSON string edits must fail precisely: %v", res)
	}
}

func TestMultiEditMutualExclusionAndValidation(t *testing.T) {
	a := newTestApp(t)
	writeAppWsFile(t, a, "mx.txt", "content\n")

	// هر دو شکل با هم → خطای روشن
	res, ok := a.execTool("edit_file", `{"path":"mx.txt","old_string":"content","new_string":"x","edits":[{"old_string":"content","new_string":"y"}]}`)
	if ok || !strings.Contains(res, "not both") {
		t.Fatalf("both forms must be rejected: %v", res)
	}

	// هیچ‌کدام → خطای روشن
	if res, ok = a.execTool("edit_file", `{"path":"mx.txt"}`); ok || !strings.Contains(res, `missing required field "old_string"`) {
		t.Fatalf("neither form must be rejected: %v", res)
	}

	// آیتم بدون old_string → شماره‌ی آیتم در خطا
	if res, ok = a.execTool("edit_file", `{"path":"mx.txt","edits":[{"new_string":"y"}]}`); ok || !strings.Contains(res, `edits[0] is missing required field "old_string"`) {
		t.Fatalf("item validation must name the index: %v", res)
	}

	// آیتم با نوع اشتباه (آرایه) → شماره‌ی آیتم در خطا
	if res, ok = a.execTool("edit_file", `{"path":"mx.txt","edits":[{"old_string":"content","new_string":["a"]}]}`); ok || !strings.Contains(res, `edits[0] field "new_string" must be a string`) {
		t.Fatalf("item type validation must name the index: %v", res)
	}

	// فایل دست‌نخورده مانده
	if got := readAppWsFile(t, a, "mx.txt"); got != "content\n" {
		t.Fatalf("file must be untouched by invalid calls, got %q", got)
	}
}

func TestMultiEditSingleFormBackwardCompatible(t *testing.T) {
	a := newTestApp(t)
	writeAppWsFile(t, a, "single.txt", "alpha beta\n")
	// محافظ A.1: ویرایشگر فایلِ خوانده‌شده پیامِ یادآوری نمی‌گیرد — این تست
	// پیام «بدون تغییر» را می‌سنجد، پس فایل اول خوانده می‌شود.
	if r, ok := a.execTool("read_file", `{"path":"single.txt"}`); !ok {
		t.Fatal(r)
	}

	// شکل تک‌ویرایش دقیقاً مثل قبل — شامل پیام نتیجه
	res, ok := a.execTool("edit_file", `{"path":"single.txt","old_string":"alpha","new_string":"gamma"}`)
	if !ok || res != "OK: replaced 1 occurrence(s) in 'single.txt' (+change recorded, revertible)." {
		t.Fatalf("single-form message changed: %q", res)
	}
	// حذف متن با new_string خالی همچنان مجاز است
	if res, ok = a.execTool("edit_file", `{"path":"single.txt","old_string":" beta","new_string":""}`); !ok {
		t.Fatalf("delete-via-empty-new_string must still work: %v", res)
	}
	if got := readAppWsFile(t, a, "single.txt"); got != "gamma\n" {
		t.Fatalf("unexpected content: %q", got)
	}
	// خطای ابهام تک‌ویرایش — همان پیام قبلی
	writeAppWsFile(t, a, "dup2.txt", "z\nz\n")
	if res, ok = a.execTool("edit_file", `{"path":"dup2.txt","old_string":"z","new_string":"y"}`); ok || res != "ERROR: old_string appears 2 time(s) in 'dup2.txt'. Include more surrounding lines to make it unique, or pass replace_all=true." {
		t.Fatalf("single-form ambiguity message changed: %q", res)
	}
}

func TestMultiEditUsesTolerantCascadePerEdit(t *testing.T) {
	a := newTestApp(t)
	// تورفتگی مدل (تب) با فایل (۸ فاصله) فرق دارد و تطبیق دقیق ناممکن است —
	// سطح «تحمل تورفتگی» باید داخل همان ویرایش چندگانه کار بیفتد و متن
	// جایگزین هم با تورفتگی واقعی فایل هم‌راستا شود.
	writeAppWsFile(t, a, "indent.py", "def f():\n        return 1\n\ndef g():\n        return 2\n")

	res, ok := a.execTool("edit_file", `{"path":"indent.py","edits":[
		{"old_string":"\treturn 1","new_string":"\treturn 10"},
		{"old_string":"\treturn 2","new_string":"\treturn 20"}
	]}`)
	if !ok {
		t.Fatalf("indent-tolerant multi-edit failed: %v", res)
	}
	if !strings.Contains(res, "indentation tolerance") {
		t.Fatalf("strategy must be reported: %v", res)
	}
	got := readAppWsFile(t, a, "indent.py")
	// تورفتگی واقعی فایل (۸ فاصله) باید حفظ شود — فقط مقدار عوض شده
	if !strings.Contains(got, "\n        return 10\n") || !strings.Contains(got, "\n        return 20\n") {
		t.Fatalf("file indentation must be preserved with tolerant matching:\n%s", got)
	}
	if strings.Contains(got, "\t") {
		t.Fatalf("replacement must be re-indented to the file's spaces, got tab:\n%s", got)
	}
}
