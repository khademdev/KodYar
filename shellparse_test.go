// تست‌های تجزیه‌گر واقعی shell (Stage 1 — shellparse.go).
//
// هر دو شله (cmd و POSIX) روی هر سیستم‌عاملی از طریق currentCommandFlavor
// قابل تزریق‌اند؛ تست‌ها قبل و بعد آن را بازنشانی می‌کنند (تست‌های این
// پکیج موازی نیستند — قرارداد todosEmit).
package main

import (
	"strings"
	"testing"
)

// withFlavor شله‌ی فعال را برای یک تست عوض می‌کند و در پایان بازمی‌گرداند.
func withFlavor(t *testing.T, f shellFlavor) {
	t.Helper()
	old := currentCommandFlavor
	currentCommandFlavor = func() shellFlavor { return f }
	t.Cleanup(func() { currentCommandFlavor = old })
}

// mustParse اگر تجزیه شکست بخورد تست را می‌بُرد.
func mustParse(t *testing.T, cmd string, f shellFlavor) parsedCommand {
	t.Helper()
	pc, ok := parseShellCommand(cmd, f)
	if !ok {
		t.Fatalf("parse(%q, %v) failed, want ok", cmd, f)
	}
	return pc
}

// argvAll نمای argv همه‌ی دستورهای ساده را با « | » جمع می‌کند.
func argvAll(pc parsedCommand) string {
	var parts []string
	for i := range pc.simples {
		parts = append(parts, strings.Join(pc.simples[i].argv, " "))
	}
	return strings.Join(parts, " | ")
}

func TestParseBasicWordsAndSeparators(t *testing.T) {
	cases := []struct {
		cmd    string
		flavor shellFlavor
		want   string
	}{
		{"echo hi", flavorPOSIX, "echo hi"},
		{"echo hi", flavorCmd, "echo hi"},
		{"echo a && echo b", flavorPOSIX, "echo a | echo b"},
		{"echo a && echo b", flavorCmd, "echo a | echo b"},
		{"echo a || echo b", flavorPOSIX, "echo a | echo b"},
		{"echo a; echo b", flavorPOSIX, "echo a | echo b"},
		{"echo a | wc -l", flavorPOSIX, "echo a | wc -l"},
		{"echo a\necho b", flavorPOSIX, "echo a | echo b"},
		{"echo a & echo b", flavorPOSIX, "echo a | echo b"},
		{"echo a & echo b", flavorCmd, "echo a | echo b"},
		{"echo a |& grep x", flavorPOSIX, "echo a | grep x"},
		// «;» در cmd نویسه‌ی عادی است (نه جداکننده)
		{"echo a;b", flavorCmd, "echo a;b"},
		{"echo a;b", flavorPOSIX, "echo a | b"},
		// دستورهای خالی بین جداکننده‌ها جمع می‌شوند
		{"echo a ; ; echo b", flavorPOSIX, "echo a | echo b"},
		{"\n\necho a\n\n", flavorPOSIX, "echo a"},
		// ادامه‌ی خط بعد از جداکننده‌ی سخت
		{"echo a &&\necho b", flavorPOSIX, "echo a | echo b"},
		{"echo a | \n echo b", flavorPOSIX, "echo a | echo b"},
		{"echo a ^\n echo b", flavorCmd, "echo a echo b"}, // caret در انتهای خط = ادامه
		{"echo \\\n hi", flavorPOSIX, "echo hi"},          // بک‌اسلش-خط‌جدید
		// Persian
		{"echo سلام دنیا", flavorPOSIX, "echo سلام دنیا"},
		{"echo سلام && echo خوبی", flavorCmd, "echo سلام | echo خوبی"},
		{"git status --porcelain", flavorPOSIX, "git status --porcelain"},
	}
	for _, c := range cases {
		pc := mustParse(t, c.cmd, c.flavor)
		if got := argvAll(pc); got != c.want {
			t.Errorf("parse(%q, %v) argv = %q, want %q", c.cmd, c.flavor, got, c.want)
		}
	}
}

func TestParseQuoting(t *testing.T) {
	// POSIX
	pc := mustParse(t, `echo 'a b' "c d" e`, flavorPOSIX)
	want := []string{"echo", "a b", "c d", "e"}
	if strings.Join(pc.simples[0].argv, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("posix quoting: got %q", pc.simples[0].argv)
	}
	// کوتیشن خالی = توکن خالی
	pc = mustParse(t, `echo "" x`, flavorPOSIX)
	if len(pc.simples[0].argv) != 3 || pc.simples[0].argv[1] != "" {
		t.Errorf("empty-quote token: got %q", pc.simples[0].argv)
	}
	// جداکننده داخل کوتیشن
	pc = mustParse(t, `echo "a; b | c"`, flavorPOSIX)
	if len(pc.simples) != 1 || pc.simples[0].argv[1] != "a; b | c" {
		t.Errorf("sep inside quotes: got %q", pc.simples[0].argv)
	}
	// بک‌اسلش بیرون کوتیشن
	pc = mustParse(t, `echo a\ b`, flavorPOSIX)
	if pc.simples[0].argv[1] != "a b" {
		t.Errorf("backslash escape: got %q", pc.simples[0].argv)
	}
	// بک‌اسلش داخل دابل‌کوت فقط برای "\ $ ` معتبر است
	pc = mustParse(t, `echo "a\nb"`, flavorPOSIX)
	if pc.simples[0].argv[1] != `a\nb` {
		t.Errorf("posix dquote backslash-n literal: got %q", pc.simples[0].argv[1])
	}
	pc = mustParse(t, `echo "a\"b"`, flavorPOSIX)
	if pc.simples[0].argv[1] != `a"b` {
		t.Errorf("posix dquote escaped quote: got %q", pc.simples[0].argv[1])
	}
	// cmd: فقط دابل‌کوت؛ ‎""…""‎ = کوتیشن literal؛ تک‌کوت literal است
	pc = mustParse(t, `echo "say ""hi"""`, flavorCmd)
	if pc.simples[0].argv[1] != `say "hi"` {
		t.Errorf("cmd doubled quote: got %q", pc.simples[0].argv[1])
	}
	pc = mustParse(t, `echo 'a b'`, flavorCmd)
	if pc.simples[0].argv[1] != "'a" || pc.simples[0].argv[2] != "b'" {
		t.Errorf("cmd single quote literal: got %q", pc.simples[0].argv)
	}
	// مسیر ویندوزی با فاصله داخل کوتیشن
	pc = mustParse(t, `type "C:\My Files\a.txt"`, flavorCmd)
	if pc.simples[0].argv[1] != `C:\My Files\a.txt` {
		t.Errorf("cmd quoted windows path: got %q", pc.simples[0].argv)
	}
	// مسیر فارسی با فاصله
	pc = mustParse(t, `echo "پوشه من"`, flavorPOSIX)
	if pc.simples[0].argv[1] != "پوشه من" {
		t.Errorf("persian quoted path: got %q", pc.simples[0].argv)
	}
}

func TestParseUnclosedFails(t *testing.T) {
	bad := []struct {
		cmd    string
		flavor shellFlavor
	}{
		{`echo "unclosed`, flavorPOSIX},
		{`echo "unclosed`, flavorCmd},
		{"echo 'unclosed", flavorPOSIX},
		{"echo $(unclosed", flavorPOSIX},
		{"echo ${unclosed", flavorPOSIX},
		{"echo `unclosed", flavorPOSIX},
		{"echo \"a`b\"", flavorPOSIX}, // بک‌تیک بسته نشد (داخل دابل‌کوت)
		{"echo (unclosed", flavorPOSIX},
		{"echo (unclosed", flavorCmd},
		{"echo hi >", flavorPOSIX},  // مقصد غایب
		{"echo hi > ", flavorCmd},   // مقصد غایب
		{"cat << EOF", flavorPOSIX}, // heredoc
		{"cat <<EOF", flavorPOSIX},  // heredoc بدون فاصله
		{"echo )", flavorPOSIX},     // «)» یتیم
		{"echo >&", flavorPOSIX},    // & بدون رقم
		{"echo 2>&x", flavorPOSIX},  // رقم چسبیده به کلمه
	}
	for _, c := range bad {
		if _, ok := parseShellCommand(c.cmd, c.flavor); ok {
			t.Errorf("parse(%q, %v) = ok, want fail", c.cmd, c.flavor)
		}
	}
}

func TestParseRedirects(t *testing.T) {
	// «echo x > f» → یک دستور ساده با تغییرمسیر
	pc := mustParse(t, "echo x > f.txt", flavorPOSIX)
	if len(pc.simples) != 1 || len(pc.simples[0].redirects) != 1 {
		t.Fatalf("redirect count: %+v", pc.simples)
	}
	r := pc.simples[0].redirects[0]
	if r.op != ">" || r.target != "f.txt" {
		t.Errorf("redirect: got %+v", r)
	}
	if got := pc.simples[0].view(); got != "echo x >f.txt" {
		t.Errorf("view: got %q", got)
	}
	// fd-dup
	pc = mustParse(t, "go test ./... 2>&1", flavorPOSIX)
	r = pc.simples[0].redirects[0]
	if r.op != "2>&1" || r.target != "" {
		t.Errorf("2>&1: got %+v", r)
	}
	// fd-dup بدون fd
	pc = mustParse(t, "echo hi >&2", flavorPOSIX)
	r = pc.simples[0].redirects[0]
	if r.op != ">&2" || r.target != "" {
		t.Errorf(">&2: got %+v", r)
	}
	// پیشوند fd با فضای قبل از >
	pc = mustParse(t, "echo x 2> err.txt", flavorPOSIX)
	r = pc.simples[0].redirects[0]
	if r.op != "2>" || r.target != "err.txt" {
		t.Errorf("2>: got %+v", r)
	}
	if len(pc.simples[0].argv) != 2 { // «2» آرگومان نیست
		t.Errorf("fd must not be argv: %q", pc.simples[0].argv)
	}
	// بدون فاصله: ‎echo 2>x‎ → fd=2
	pc = mustParse(t, "echo 2>x", flavorCmd)
	r = pc.simples[0].redirects[0]
	if r.op != "2>" || r.target != "x" {
		t.Errorf("cmd 2>x: got %+v", r)
	}
	// «2» جدا (با فاصله) آرگومان است
	pc = mustParse(t, "echo 2 > x", flavorCmd)
	if len(pc.simples[0].argv) != 2 || pc.simples[0].argv[1] != "2" {
		t.Errorf("cmd spaced 2: %q", pc.simples[0].argv)
	}
	// >> و <
	pc = mustParse(t, "cat a >> ~/.bashrc", flavorPOSIX)
	r = pc.simples[0].redirects[0]
	if r.op != ">>" || r.target != "~/.bashrc" {
		t.Errorf("append: got %+v", r)
	}
	pc = mustParse(t, "sort < in.txt", flavorPOSIX)
	r = pc.simples[0].redirects[0]
	if r.op != "<" || r.target != "in.txt" {
		t.Errorf("input: got %+v", r)
	}
	// مقصد کوتیشن‌دار با فاصله
	pc = mustParse(t, `echo hi > "out file.txt"`, flavorCmd)
	r = pc.simples[0].redirects[0]
	if r.target != "out file.txt" {
		t.Errorf("cmd quoted target: got %+v", r)
	}
	// مسیر مقصد با کاراکترهای فارسی
	pc = mustParse(t, "echo hi > خروجی.txt", flavorPOSIX)
	if pc.simples[0].redirects[0].target != "خروجی.txt" {
		t.Errorf("persian redirect target: got %+v", pc.simples[0].redirects[0])
	}
}

func TestParseCmdCaretAndExpansion(t *testing.T) {
	// caret escape: ‎r^m‎ → rm
	pc := mustParse(t, "r^m -rf /", flavorCmd)
	if pc.simples[0].argv[0] != "rm" {
		t.Errorf("caret: got %q", pc.simples[0].argv)
	}
	// caret قبل از جداکننده = literal → جداکننده نمی‌شود
	pc = mustParse(t, "echo a ^& b", flavorCmd)
	if len(pc.simples) != 1 || pc.simples[0].argv[2] != "&" {
		t.Errorf("caret-escaped &: got %q simples=%d", pc.simples[0].argv, len(pc.simples))
	}
	// هر % یا ! در cmd → گسترش
	for _, cmd := range []string{"echo %PATH%", "echo !VAR!", `echo "%COMSPEC%" /c del`, "echo 100%"} {
		pc = mustParse(t, cmd, flavorCmd)
		if !pc.simples[0].hasExpansion {
			t.Errorf("cmd expansion flag missing for %q", cmd)
		}
	}
	// در POSIX گسترش فقط با ‎$‎
	pc = mustParse(t, "echo %PATH%", flavorPOSIX)
	if pc.simples[0].hasExpansion {
		t.Errorf("posix must not flag %% as expansion")
	}
}

func TestParsePosixExpansionsAndSubshells(t *testing.T) {
	// $(cmd)
	pc := mustParse(t, "echo $(rm -rf /)", flavorPOSIX)
	sc := &pc.simples[0]
	if !sc.hasSubshell || !sc.hasExpansion {
		t.Errorf("cmd-sub flags: %+v", sc)
	}
	if len(sc.subshells) != 1 || sc.subshells[0] != "rm -rf /" {
		t.Errorf("cmd-sub payload: %q", sc.subshells)
	}
	if sc.argv[1] != "$(rm -rf /)" {
		t.Errorf("cmd-sub raw token: %q", sc.argv[1])
	}
	// backtick
	pc = mustParse(t, "echo `rm -rf /`", flavorPOSIX)
	sc = &pc.simples[0]
	if !sc.hasSubshell || len(sc.subshells) != 1 || sc.subshells[0] != "rm -rf /" {
		t.Errorf("backtick: %+v %q", sc, sc.subshells)
	}
	// ${…}
	pc = mustParse(t, "rm${IFS}-rf /", flavorPOSIX)
	sc = &pc.simples[0]
	if !sc.hasExpansion || sc.argv[0] != "rm${IFS}-rf" {
		t.Errorf("brace expansion: %+v %q", sc, sc.argv)
	}
	// $VAR
	pc = mustParse(t, "echo $HOME", flavorPOSIX)
	if !pc.simples[0].hasExpansion || pc.simples[0].argv[1] != "$HOME" {
		t.Errorf("var expansion: %+v", pc.simples[0])
	}
	// $((…)) محاسباتی: expansion؛ payload هم محافظه‌کارانه ارزیابی می‌شود
	pc = mustParse(t, "echo $((1+2))", flavorPOSIX)
	sc = &pc.simples[0]
	if !sc.hasExpansion {
		t.Errorf("arithmetic expansion flag: %+v", sc)
	}
	if len(sc.subshells) != 1 || sc.subshells[0] != "1+2" {
		t.Errorf("arithmetic conservative payload: %q", sc.subshells)
	}
	// جایگذاری پروسه
	pc = mustParse(t, "diff <(sort a) <(sort b)", flavorPOSIX)
	sc = &pc.simples[0]
	if !sc.hasSubshell || len(sc.subshells) != 2 {
		t.Errorf("procsub: %+v %q", sc, sc.subshells)
	}
	// زیرپوسته‌ی لخت
	pc = mustParse(t, "(cd /tmp && rm x)", flavorPOSIX)
	sc = &pc.simples[0]
	if !sc.hasSubshell || len(pc.simples) != 1 {
		t.Errorf("bare subshell: %+v (%d simples)", sc, len(pc.simples))
	}
	// توضیح
	pc = mustParse(t, "echo hi # comment here", flavorPOSIX)
	if len(pc.simples[0].argv) != 2 {
		t.Errorf("comment: %q", pc.simples[0].argv)
	}
	pc = mustParse(t, "echo a#b", flavorPOSIX)
	if pc.simples[0].argv[1] != "a#b" {
		t.Errorf("# inside word must be literal: %q", pc.simples[0].argv)
	}
	// در cmd توضیح نیست
	pc = mustParse(t, "echo # hi", flavorCmd)
	if pc.simples[0].argv[1] != "#" {
		t.Errorf("cmd # literal: %q", pc.simples[0].argv)
	}
}

func TestParseBackgroundFlag(t *testing.T) {
	// POSIX: «a & b» → a پس‌زمینه
	pc := mustParse(t, "sleep 1 & echo done", flavorPOSIX)
	if !pc.simples[0].background || pc.simples[1].background {
		t.Errorf("posix bg: %v %v", pc.simples[0].background, pc.simples[1].background)
	}
	// انتهایی
	pc = mustParse(t, "sleep 1 &", flavorPOSIX)
	if !pc.simples[0].background {
		t.Errorf("posix trailing bg: %v", pc.simples[0].background)
	}
	// در cmd «&» فقط جداکننده است
	pc = mustParse(t, "echo a & echo b", flavorCmd)
	if pc.simples[0].background {
		t.Errorf("cmd must not set background")
	}
}

func TestParseForkBomb(t *testing.T) {
	// fork bomb باید با موفقیت تجزیه شود (تا نمای regex آن را ببیند) و
	// payload زیرپوسته هم جدا شود.
	pc := mustParse(t, ":(){ :|:& };:", flavorPOSIX)
	if len(pc.simples) < 2 {
		t.Fatalf("fork bomb simples: %d", len(pc.simples))
	}
	if !pc.simples[0].hasSubshell {
		t.Errorf("fork bomb subshell flag missing: %+v", pc.simples[0])
	}
	joined := argvAll(pc)
	if !strings.Contains(joined, "(){ :|:& }") && !strings.Contains(pc.simples[0].argv[0], "(){") {
		t.Errorf("fork bomb raw not preserved: %q", joined)
	}
}

func TestParseInterpreterPayloadDetection(t *testing.T) {
	// sh -c 'payload' — payload به‌صورت آرگومانِ کوتیشن‌شده می‌ماند و
	// interpreterPayloads آن را جدا می‌کند.
	pc := mustParse(t, "sh -c 'rm -rf /'", flavorPOSIX)
	if got := interpreterPayloads(pc.simples[0].argv); len(got) != 1 || got[0] != "rm -rf /" {
		t.Errorf("sh -c payload: %q", got)
	}
	pc = mustParse(t, `cmd /c "del /q x"`, flavorCmd)
	if got := interpreterPayloads(pc.simples[0].argv); len(got) != 1 || got[0] != "del /q x" {
		t.Errorf("cmd /c payload: %q", got)
	}
	pc = mustParse(t, "node -e 'console.log(1)'", flavorPOSIX)
	if got := interpreterPayloads(pc.simples[0].argv); len(got) != 1 || got[0] != "console.log(1)" {
		t.Errorf("node -e payload: %q", got)
	}
	// بدون پرچم payload
	pc = mustParse(t, "bash script.sh", flavorPOSIX)
	if got := interpreterPayloads(pc.simples[0].argv); got != nil {
		t.Errorf("no payload expected: %q", got)
	}
}

func TestParseBoundAndEdgeCases(t *testing.T) {
	// دستور خالی/فقط فاصله
	for _, cmd := range []string{"", "   ", "\n\n", " ; "} {
		if _, ok := parseShellCommand(cmd, flavorPOSIX); !ok {
			t.Errorf("parse(%q) must be ok", cmd)
		}
	}
	// دستور خیلی طولانی → عدم قطعیت
	long := strings.Repeat("a", maxParseCommandLength+1)
	if _, ok := parseShellCommand(long, flavorPOSIX); ok {
		t.Error("oversized command must fail")
	}
	// ZWNJ (U+200C) در متن فارسی باید بایت‌به‌بایت حفظ شود
	zwnj := "echo نیم‌فاصله"
	pc := mustParse(t, zwnj, flavorPOSIX)
	if pc.simples[0].argv[1] != "نیم‌فاصله" {
		t.Errorf("ZWNJ not preserved: %q", pc.simples[0].argv[1])
	}
	// تغییرمسیرِ تنها = دستور ساده‌ی بدون argv (مثل ‎> file‎)
	pc = mustParse(t, "> /tmp/x", flavorPOSIX)
	if len(pc.simples) != 1 || len(pc.simples[0].argv) != 0 || len(pc.simples[0].redirects) != 1 {
		t.Errorf("redirect-only simple: %+v", pc.simples)
	}
}
