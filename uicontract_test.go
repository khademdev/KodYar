// تست قرارداد UI (بند D.2) — از «متن» index.html و JS/CSS ساخته‌شده ثابت
// می‌کند که:
//
//	۱) هر id که JS ارجاع می‌دهد در HTML وجود دارد یا توسط خود JS ساخته
//	   می‌شود (مودال‌هایی که خود JS می‌سازد)؛
//	۲) هر کلاسی که JS برای نمایان‌شدن toggle می‌کند قاعده‌ی CSS دارد و
//	   قاعده‌ی کلیدی «.modal-overlay.open» واقعاً مودال را قابل‌مشاهده و
//	   قابل‌کلیک می‌کند — بازتولید‌کننده‌ی نگهبانِ باگ مودالِ نامرئی ۱.۷.۱؛
//	۳) هر متد Wails که JS صدا می‌زند در bindings دستی
//	   (frontend/wailsjs) و روی App وجود دارد و بالعکس exportها با
//	   App.d.ts و App همگام‌اند؛
//	۴) مودال‌های افزوده‌شده در مأموریت جاری (پیش‌نمایش بازگردانی نقطه‌ی
//	   بازگشت و گزینه‌ی web_host در مودال دسترسی) پوشش دارند و Mock-mode
//	   از آن‌ها پشتیبانی می‌کند.
//
// این تست جای مرورگر را نمی‌گیرد؛ لایه‌ی دوم «دورمرورگری» در
// frontend/tests/ است (Playwright، اختیاری و غیربلاک‌کننده).
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// readFrontend متن فایل‌های فرانت‌اند ساخته‌شده را برمی‌گرداند.
func readFrontend(t *testing.T) (html string, js []namedSrc, css string, bindings, dts string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("frontend", "dist", "index.html"))
	if err != nil {
		t.Fatalf("index.html: %v", err)
	}
	html = string(raw)
	jsFiles, err := filepath.Glob(filepath.Join("frontend", "dist", "assets", "js", "*.js"))
	if err != nil || len(jsFiles) == 0 {
		t.Fatalf("no frontend js files: %v", err)
	}
	sort.Strings(jsFiles)
	for _, f := range jsFiles {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		js = append(js, namedSrc{name: filepath.Base(f), src: string(raw)})
	}
	cssFiles, err := filepath.Glob(filepath.Join("frontend", "dist", "assets", "css", "*.css"))
	if err != nil || len(cssFiles) == 0 {
		t.Fatalf("no frontend css files: %v", err)
	}
	var cb strings.Builder
	for _, f := range cssFiles {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		cb.Write(raw)
		cb.WriteByte('\n')
	}
	css = cb.String()
	b, err := os.ReadFile(filepath.Join("frontend", "wailsjs", "go", "main", "App.js"))
	if err != nil {
		t.Fatalf("App.js bindings: %v", err)
	}
	bindings = string(b)
	d, err := os.ReadFile(filepath.Join("frontend", "wailsjs", "go", "main", "App.d.ts"))
	if err != nil {
		t.Fatalf("App.d.ts bindings: %v", err)
	}
	dts = string(d)
	return html, js, css, bindings, dts
}

type namedSrc struct {
	name string
	src  string
}

var (
	reHTMLID      = regexp.MustCompile(`id="([a-zA-Z][a-zA-Z0-9_-]{1,40})"`)
	reJSIDRef     = regexp.MustCompile(`["']#([a-zA-Z][a-zA-Z0-9_-]{1,40})["']`)
	reJSCreatedID = regexp.MustCompile(`\.id\s*=\s*"([a-zA-Z][a-zA-Z0-9_-]{1,40})"`)
	reToggled     = regexp.MustCompile(`classList\.(?:add|toggle|remove)\(\s*["']([\w-]{2,30})["']`)
	// رنگ‌های hex سه/شش‌رقمی — مثل ‎#7c6cf0‎ — ارجاع id نیستند.
	reHexColor = regexp.MustCompile(`^[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$`)
	// متدهای پشت «.» در JS که با حرف بزرگ شروع می‌شوند؛ در این کدبیس این
	// الگو تقریباً همیشه فراخوانی پل Go است. نام‌های مجازِ زیر توابع
	// زمان‌اجرا/JS و Wails-runtime هستند — نه متدهای App.
	reDotCall  = regexp.MustCompile(`\.\s*([A-Z][A-Za-z0-9_]{2,40})\s*\(`)
	reGoMethod = regexp.MustCompile(`func \(a \*App\) ([A-Z]\w+)\(`)
	reExport   = regexp.MustCompile(`export function ([A-Z]\w+)\(`)
	reCSSClass = regexp.MustCompile(`\.([\w-]+)`)
	reRuleBody = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
)

// uiRuntimeDotCalls نام‌های غیر App — توابع JS/Wails-runtime با فراخوانی نقطه‌ای.
var uiRuntimeDotCalls = map[string]bool{
	"DateTimeFormat":    true, // Intl.DateTimeFormat
	"KodYarResetStreamUI": true, // هوک داخلی فرانت‌اند
	"EventsEmit":        true, // window.runtime.*
	"EventsOff":         true,
	"EventsOn":          true,
	"BrowserOpenURL":    true, // wails runtime
}

func TestUIContract(t *testing.T) {
	html, js, css, bindings, dts := readFrontend(t)
	allJS := &strings.Builder{}
	for _, s := range js {
		allJS.WriteString(s.src)
	}
	jstext := allJS.String()

	// ---------------------------------------------------------- چک ۱: idها
	htmlIDs := map[string]bool{}
	for _, m := range reHTMLID.FindAllStringSubmatch(html, -1) {
		htmlIDs[m[1]] = true
	}
	createdIDs := map[string]bool{}
	for _, m := range reJSCreatedID.FindAllStringSubmatch(jstext, -1) {
		createdIDs[m[1]] = true
	}
	// idهایی که در قالب‌های رشته‌ای JS ساخته می‌شوند (innerHTML با id="…")
	reTemplateID := regexp.MustCompile(`id="([a-zA-Z][a-zA-Z0-9_-]{1,40})"`)
	for _, m := range reTemplateID.FindAllStringSubmatch(jstext, -1) {
		createdIDs[m[1]] = true
	}
	missingIDs := map[string]bool{}
	for _, m := range reJSIDRef.FindAllStringSubmatch(jstext, -1) {
		id := m[1]
		if reHexColor.MatchString(strings.TrimPrefix(id, "#")) || reHexColor.MatchString(id) {
			continue
		}
		if !htmlIDs[id] && !createdIDs[id] {
			missingIDs[id] = true
		}
	}
	if len(missingIDs) > 0 {
		var names []string
		for id := range missingIDs {
			names = append(names, id)
		}
		sort.Strings(names)
		t.Fatalf("JS references id(s) that exist neither in index.html nor are created in JS: %v", names)
	}

	// ------------------------------------------- چک ۲: کلاس‌های toggleشده
	toggled := map[string]bool{}
	for _, m := range reToggled.FindAllStringSubmatch(jstext, -1) {
		toggled[m[1]] = true
	}
	cssClasses := map[string]bool{}
	for _, m := range reCSSClass.FindAllStringSubmatch(css, -1) {
		cssClasses[m[1]] = true
	}
	for c := range toggled {
		if !cssClasses[c] {
			t.Fatalf("JS toggles class %q but no CSS rule mentions it (invisible-state bug class)", c)
		}
	}
	// قاعده‌ی کلیدی مودال‌ها: .modal-overlay.open باید مودال را واقعاً
	// نمایان و کلیک‌پذیر کند (opacity/pointer-events یا display غیر none).
	visibleRule := false
	for _, m := range reRuleBody.FindAllStringSubmatch(css, -1) {
		sel := strings.TrimSpace(m[1])
		body := m[2]
		if !strings.Contains(sel, ".modal-overlay.open") {
			continue
		}
		disp := regexp.MustCompile(`display\s*:\s*([a-z-]+)`).FindStringSubmatch(body)
		opac := regexp.MustCompile(`opacity\s*:\s*([0-9.]+)`).FindStringSubmatch(body)
		pe := regexp.MustCompile(`pointer-events\s*:\s*([a-z]+)`).FindStringSubmatch(body)
		okDisplay := disp != nil && disp[1] != "none"
		okOpacity := opac != nil && opac[1] != "0" && pe != nil && pe[1] != "none"
		if okDisplay || okOpacity {
			visibleRule = true
		}
	}
	if !visibleRule {
		t.Fatal(`CSS has no rule that makes ".modal-overlay.open" visible and clickable — the 1.7.1 invisible-modal regression`)
	}
	// پنج اورلی استاتیک HTML باید در JS هم باز/بسته شوند.
	for _, id := range []string{"settings-overlay", "provider-overlay", "confirm-overlay", "diff-overlay"} {
		if !htmlIDs[id] {
			t.Fatalf("overlay #%s missing from index.html", id)
		}
		if !strings.Contains(jstext, id) {
			t.Fatalf("overlay #%s is never referenced from JS (dead modal)", id)
		}
	}

	// ------------------------------------------------- چک ۳: بایندینگ‌ها
	goMethods := map[string]bool{}
	goFiles, _ := filepath.Glob("*.go")
	for _, f := range goFiles {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range reGoMethod.FindAllStringSubmatch(string(raw), -1) {
			goMethods[m[1]] = true
		}
	}
	exports := map[string]bool{}
	for _, m := range reExport.FindAllStringSubmatch(bindings, -1) {
		exports[m[1]] = true
	}
	dtsNames := map[string]bool{}
	for _, m := range reExport.FindAllStringSubmatch(dts, -1) {
		dtsNames[m[1]] = true
	}
	called := map[string]bool{}
	for _, m := range reDotCall.FindAllStringSubmatch(jstext, -1) {
		if !uiRuntimeDotCalls[m[1]] {
			called[m[1]] = true
		}
	}
	var bad []string
	for c := range called {
		if !exports[c] {
			bad = append(bad, "JS calls App."+c+" but it is missing from frontend/wailsjs/go/main/App.js")
		}
		if !goMethods[c] {
			bad = append(bad, "JS calls App."+c+" but App has no such exported method")
		}
	}
	for e := range exports {
		if !goMethods[e] {
			bad = append(bad, "binding App."+e+" exported but no Go method exists")
		}
		if !dtsNames[e] {
			bad = append(bad, "binding App."+e+" missing from App.d.ts")
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("Wails bindings contract broken:\n  %s", strings.Join(bad, "\n  "))
	}

	// --------------------------------- چک ۴: پوشش سطح‌های UI این نسخه
	// (الف) کارت خلاصه‌ی نوبت — فقط اطلاعات؛ جریان «بازگردانی تغییرات» و
	// پنل تغییرات فایل به‌طور کامل از محصول حذف شده‌اند.
	ts := findSrc(js, "turnsummary.js")
	for _, want := range []string{
		`ts-card`,
		`chat:turn_summary`,
		`GetFileChangeDiff`,
		"خلاصه‌ی واقعی این نوبت",
	} {
		if !strings.Contains(ts, want) {
			t.Fatalf("turn summary contract: turnsummary.js must contain %q", want)
		}
	}
	for _, gone := range []string{"RevertTurnChanges", "RestoreCheckpoint", "PreviewCheckpointRestore", "برگرداندن"} {
		if strings.Contains(ts, gone) {
			t.Fatalf("turnsummary.js must no longer contain the removed revert flow (%q)", gone)
		}
	}
	perm := findSrc(js, "permissions.js")
	if !strings.Contains(perm, "web_host") {
		t.Fatal("permission modal must handle the web_host kind (C.1)")
	}
	if !strings.Contains(perm, "همیشه برای این دامنه") {
		t.Fatal("permission modal must offer per-host always (never tool-wide)")
	}
	// (ج) Mock-mode باید مودال پیش‌نمایش را هم پشتیبانی کند (قاعده‌ی ۱.۴)
	mock := findSrc(js, "mock.js")
	for _, want := range []string{"chat:turn_summary", "SetEditMode", "GetFileChangeDiff"} {
		if !strings.Contains(mock, want) {
			t.Fatalf("mock.js must support %q so the browser smoke test can run", want)
		}
	}
}

func findSrc(js []namedSrc, name string) string {
	for _, s := range js {
		if s.name == name {
			return s.src
		}
	}
	return ""
}

// TestSandboxGitExemptionVisibleInUI — استثنای مستند git باید در UI دیده شود:
// یادداشت در تب پوشه وجود دارد، متنش git/MSYS را توضیح می‌دهد و sandboxui.js
// نمایش آن را با وضعیت سندباکس هم‌گام می‌کند (و CSS پنهان‌بودن را تضمین می‌کند).
func TestSandboxGitExemptionVisibleInUI(t *testing.T) {
	html, err := os.ReadFile(filepath.Join("frontend", "dist", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `id="sandbox-git-note"`) {
		t.Fatal("index.html must contain the #sandbox-git-note hint under the sandbox settings")
	}
	if !strings.Contains(string(html), "خارج از سندباکس اجرا می‌شوند") {
		t.Fatal("the git-exemption note must explain that git runs outside the sandbox")
	}
	js, err := os.ReadFile(filepath.Join("frontend", "dist", "assets", "js", "sandboxui.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), "sandbox-git-note") || !strings.Contains(string(js), "note.hidden") {
		t.Fatal("sandboxui.js must show/hide #sandbox-git-note with the sandbox state")
	}
	css, err := os.ReadFile(filepath.Join("frontend", "dist", "assets", "css", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), "#sandbox-git-note[hidden]") {
		t.Fatal("app.css must keep #sandbox-git-note really hidden when [hidden] is set")
	}
}
