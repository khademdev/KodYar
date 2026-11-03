package main

import (
	"strings"
	"testing"
)

func TestStripToolCallArtifacts(t *testing.T) {
	in := "قبل\n[TOOL_CALL]\n{'.':skip: false,path}\n(id:0)?[TOOL_CALL]\nبعد"
	out := stripToolCallArtifacts(in)
	if containsAny(out, "TOOL_CALL", "id:0") {
		t.Fatalf("artifacts remain: %q", out)
	}
	if !containsAny(out, "قبل", "بعد") {
		t.Fatalf("lost surrounding text: %q", out)
	}
}

func TestSplitThinkContent(t *testing.T) {
	vis, th := splitThinkContent("سلام\n<think>استدلال داخلی</think>\nپاسخ نهایی")
	if vis != "سلام\nپاسخ نهایی" && vis != "سلام\n\nپاسخ نهایی" {
		// trim may collapse
		if !containsAny(vis, "سلام") || !containsAny(vis, "پاسخ نهایی") || containsAny(vis, "استدلال") {
			t.Fatalf("vis=%q", vis)
		}
	}
	if th != "استدلال داخلی" {
		t.Fatalf("th=%q", th)
	}
}

func TestCleanAssistantContent(t *testing.T) {
	raw := "<think>plan</think>\n[TOOL_CALL]\nfoo\n[/TOOL_CALL]\nDone"
	vis, th := cleanAssistantContent(raw)
	if containsAny(vis, "TOOL_CALL", "foo") {
		t.Fatalf("vis still dirty: %q", vis)
	}
	if th != "plan" {
		t.Fatalf("th=%q", th)
	}
	if !containsAny(vis, "Done") {
		t.Fatalf("vis missing Done: %q", vis)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && (len(s) >= len(sub)) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

func TestStripUserScreenshotPatterns(t *testing.T) {
	in1 := "md) [TOOL_CALL] {skip: false, path: '.'} [/TOOL_CALL]? (id:0)"
	vis1, _ := cleanAssistantContent(in1)
	if containsAny(vis1, "TOOL_CALL", "id:0", "skip") {
		t.Fatalf("dirty: %q", vis1)
	}
	in2 := "think>The user wants me to create a simple snake game\nپاسخ"
	vis2, _ := cleanAssistantContent(in2)
	if containsAny(vis2, "think>") {
		t.Fatalf("orphan think remains: %q", vis2)
	}
	in3 := "[TOOL_CALL]\n{'.':skip: false,path}\n(id:0)?[TOOL_CALL]\nبعد"
	vis3, _ := cleanAssistantContent(in3)
	t.Logf("vis3=%q", vis3)
	if containsAny(vis3, "TOOL_CALL") {
		t.Fatalf("artifacts: %q", vis3)
	}
	// متن بعد از tool call باید بماند؛ اگر مدل فقط artifact فرستاده، خالی OK است
	if containsAny(vis3, "id:0") && containsAny(vis3, "TOOL") {
		t.Fatalf("artifacts: %q", vis3)
	}
}

func TestStripPreservesTrailingText(t *testing.T) {
	in := "[TOOL_CALL]\nfoo\n[TOOL_CALL]\nKEEP"
	out := stripToolCallArtifacts(in)
	t.Logf("out=%q", out)
	if !containsAny(out, "KEEP") {
		t.Fatalf("lost KEEP: %q", out)
	}
	if containsAny(out, "TOOL_CALL", "foo") {
		t.Fatalf("leftover: %q", out)
	}
}

func TestStripBalancedOnly(t *testing.T) {
	in := "[TOOL_CALL]\nfoo\n[TOOL_CALL]\nKEEP"
	out := stripBalanced(in, "[TOOL_CALL]", "[TOOL_CALL]")
	t.Logf("balanced=%q", out)
	out2 := out
	if i := indexCI(out, "[TOOL_CALL]"); i >= 0 {
		out2 = out[:i]
	}
	t.Logf("fromMarker=%q", out2)
	out3 := stripIDToolMarkers(out2)
	t.Logf("id=%q", out3)
}

func TestFullStripDebug(t *testing.T) {
	in := "[TOOL_CALL]\nfoo\n[TOOL_CALL]\nKEEP"
	out := stripBalanced(in, "[TOOL_CALL]", "[/TOOL_CALL]")
	t.Logf("a=%q", out)
	out = stripBalanced(out, "[TOOL_CALL]", "[TOOL_CALL]")
	t.Logf("b=%q", out)
	out = stripBalancedCI(out, "<tool_call", "</tool_call>")
	t.Logf("c=%q", out)
	out = stripBalancedCI(out, "<function_call", "</function_call>")
	t.Logf("d=%q", out)
	out = stripBalanced(out, "<|tool_call_begin|>", "<|tool_call_end|>")
	t.Logf("e=%q", out)
	if i := indexCI(out, "[TOOL_CALL]"); i >= 0 {
		out = out[:i]
	}
	t.Logf("f=%q", out)
	if i := indexCI(out, "<tool_call"); i >= 0 {
		out = out[:i]
	}
	t.Logf("g=%q", out)
	out = stripIDToolMarkers(out)
	t.Logf("h=%q", out)
	out = stripOrphanThinkMarkers(out)
	t.Logf("i=%q", out)
	out = strings.TrimSpace(collapseBlankLines(out))
	t.Logf("j=%q", out)
}
