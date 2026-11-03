// تست‌های رگرسیون رفع باگ‌ها (نسخهٔ 1.4.1):
// - F-A: تگ بستهٔ یتیم </tool_call> / </function_call> باید از خروجی
// قابل‌نمایش حذف شود (پیش‌تر باقی می‌ماند).
// - F-B: فایل‌های «.min.js» باید باینری/غیرقابل‌الصاق تشخیص داده شوند
// (پیش‌تر «.min.js» در binaryExt بود که filepath.Ext هرگز برنمی‌گرداند).
package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestOrphanCloseToolCallTagRemoved(t *testing.T) {
	closer := "</tool" + "_call>"
	cases := []string{
		"x " + closer + " y",
		"متن " + closer + " ادامه",
		"a </function" + "_call> b",
	}
	for _, in := range cases {
		if out := stripToolCallArtifacts(in); strings.Contains(out, "tool_call") || strings.Contains(out, "function_call") {
			t.Fatalf("orphan close tag not removed: in=%q out=%q", in, out)
		}
	}
	// متن اطراف باید حفظ شود
	if out := stripToolCallArtifacts("x " + closer + " y"); !strings.Contains(out, "x") || !strings.Contains(out, "y") {
		t.Fatalf("surrounding text lost: %q", out)
	}
}

func TestWellFormedToolCallStillFullyRemoved(t *testing.T) {
	open := "<tool" + "_call>"
	close := "</tool" + "_call>"
	in := "before " + open + `{` + `"a":1` + `}` + close + " after"
	vis, _ := cleanAssistantContent(in)
	if strings.Contains(vis, `"a":1`) || strings.Contains(vis, "tool_call") {
		t.Fatalf("well-formed block not removed: %q", vis)
	}
	if !strings.Contains(vis, "before") || !strings.Contains(vis, "after") {
		t.Fatalf("surrounding text lost: %q", vis)
	}
}

// TestConversationContextTokensExposed: عدد نمایشی نوار مصرف (پر شدن پنجرهٔ
// کانتکست) باید از سمت سرور همراه گفتگو بیاید. پیش‌تر UI خودش فقط m.tokens
// را جمع می‌زد و پیام‌های tool و دورهای میانی ایجنت (که Tokens صفر دارند)
// را نادیده می‌گرفت → کم‌برآورد شدید. همچنین باید از TokensUsed (مصرف API)
// جدا بماند.
func TestConversationContextTokensExposed(t *testing.T) {
	a := newTestApp(t)
	conv := &Conversation{
		ID:    newID(),
		Title: "usage",
		Messages: []Message{
			{ID: newID(), Role: "user", Content: strings.Repeat("پرسش کاربر ", 20), Tokens: 100},
			{ID: newID(), Role: "assistant", Content: "بررسی می‌کنم", ToolCalls: []toolCallRecord{{ID: "c1", Name: "read_file", Args: `{"path":"a.go"}`}}},
			{ID: newID(), Role: "tool", ToolCallID: "c1", ToolName: "read_file", Content: strings.Repeat("tool output line\n", 100)},
			{ID: newID(), Role: "assistant", Content: strings.Repeat("پاسخ نهایی ", 30), Tokens: 40},
		},
		TokensUsed: 5000,
	}
	if err := a.saveConversation(conv); err != nil {
		t.Fatal(err)
	}

	// فایل ذخیره‌شده هم باید عدد بازمحاسبه‌شده را داشته باشد (نه مقدار کهنه).
	dir, err := a.conversationsDir()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(convPath(dir, conv.ID))
	if err != nil {
		t.Fatal(err)
	}
	var stored Conversation
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.ContextTokens != conversationContextTokens(&stored) {
		t.Fatalf("saved contextTokens stale: got %d want %d", stored.ContextTokens, conversationContextTokens(&stored))
	}

	got, err := a.GetConversation(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := conversationContextTokens(got)
	if got.ContextTokens != want {
		t.Fatalf("contextTokens mismatch: got %d want %d", got.ContextTokens, want)
	}
	// پیام tool توکن ذخیره‌شده ندارد؛ جمع سادهٔ m.tokens فقط ۱۴۰ می‌داد.
	if got.ContextTokens <= 140 {
		t.Fatalf("contextTokens must include tool/assistant estimates: %d", got.ContextTokens)
	}
	if got.ContextTokens == got.TokensUsed {
		t.Fatalf("contextTokens must not reuse TokensUsed (%d)", got.TokensUsed)
	}
}

func TestIsBinaryAssetMinJS(t *testing.T) {
	if !isBinaryAsset("app.min.js") {
		t.Fatal("app.min.js should be detected as binary (minified bundle)")
	}
	if !isBinaryAsset("Vendor.MIN.JS") {
		t.Fatal("suffix check must be case-insensitive")
	}
	if isBinaryAsset("app.js") {
		t.Fatal("plain .js must remain attachable")
	}
	if !isBinaryAsset("logo.png") {
		t.Fatal("png must be binary")
	}
}
