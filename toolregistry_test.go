// تست سازگاری رجیستری ابزارها — هر ابزار باید در «همه‌ی» نقاط اتصال ثبت
// باشد تا هیچ‌وقت از هم نپاشند (تشخیص خودکار drift):
//
// ۱. تعریف در agentToolDefs
// ۲. تصمیم دسترسی پیش‌فرض معتبر (allow/ask — deny فقط با override کاربر)
// ۳. مشخصه‌ی آرگومان در toolArgSpecs (یا معافیتِ مستند: ابزارهای سفارشی
// که اصلاً در agentToolDefs نیستند)
// ۴. ذکر در پرامپت سیستمی تولیدشده
// ۵. آیکون/برچسب در نقشه‌ی کارت ابزار فرانت‌اند (پارس متنی سورس JS) —
// ابزارهای MCP با پیشوند mcp_ فالبک اختصاصی دارند
// ۶. فهرست موازی و allowlist زیرایجنت فقط ابزار فقط‌خواندنیِ موجود دارند
package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestToolRegistryConsistency(t *testing.T) {
	defs := agentToolDefs()
	if len(defs) == 0 {
		t.Fatal("agentToolDefs must not be empty")
	}
	prompt := agentSystemPromptForOS("/ws", "windows")

	for _, d := range defs {
		name := d.Function.Name

		// ۲. تصمیم پیش‌فرض معتبر — deny هرگز پیش‌فرض نیست
		dAllow := toolDefaultDecision(name, true)
		dAsk := toolDefaultDecision(name, false)
		if dAllow == permDeny || dAsk == permDeny {
			t.Errorf("%s: default decision must never be deny", name)
		}

		// ۳. مشخصه‌ی آرگومان
		if _, ok := toolArgSpecs[name]; !ok {
			t.Errorf("%s: missing argument spec in toolArgSpecs (only custom_* tools are exempt, and they are not in agentToolDefs)", name)
		}

		// ۴. ذکر در پرامپت سیستمی
		if !strings.Contains(prompt, name) {
			t.Errorf("%s: not mentioned in the generated system prompt", name)
		}

		// ۵. شمای پارامترها معتبر است (required هرگز null نمی‌شود)
		if d.Function.Parameters == nil {
			t.Errorf("%s: parameters schema is nil", name)
		}
	}

	// ابزار متحرکِ نماینده‌ی MCP: پیشوند mcp_ → ask + فالبک آیکون فرانت‌اند
	mcpName := mcpToolFullName("fake", "echo")
	if d := toolDefaultDecision(mcpName, false); d != permAsk {
		t.Errorf("representative MCP tool %s: default = %v, want ask", mcpName, d)
	}
	if !strings.HasPrefix(mcpName, "mcp_") {
		t.Fatalf("MCP tool name must start with mcp_: %s", mcpName)
	}
}

// TestToolCardMapCoversRegistry نقشه‌ی آیکون کارت ابزار (app.js مینیفای‌شده)
// را «متنی» پارس می‌کند: هر ابزار داخلی باید entry داشته باشد و ابزارهای
// MCP فالبک پیشوندی اختصاصی.
func TestToolCardMapCoversRegistry(t *testing.T) {
	raw, err := os.ReadFile("frontend/dist/assets/js/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)

	// نقشه‌ی آیکون: name:"icon"
	iconRe := regexp.MustCompile(`([a-zA-Z0-9_]+):"([a-zA-Z0-9]+)"`)
	icons := map[string]string{}
	for _, m := range iconRe.FindAllStringSubmatch(js, -1) {
		icons[m[1]] = m[2]
	}

	for _, d := range agentToolDefs() {
		name := d.Function.Name
		if icons[name] == "" {
			t.Errorf("%s: no icon entry in the frontend tool-card map (app.js)", name)
		}
	}

	// فالبک MCP: mcp_ → bolt (هوک افزوده‌شده در L)
	if !strings.Contains(js, `0===e.indexOf("mcp_")?"bolt"`) {
		t.Error("frontend tool-card icon function must map mcp_* tools to the bolt icon")
	}

	// خلاصه‌ی آرگومان: پیش‌فرض عمومی برای ابزارهای ناشناخته/MCP وجود دارد
	if !strings.Contains(js, `case"todo_write"`) || !strings.Contains(js, `case"web_fetch":return s(n.url)`) {
		t.Error("frontend args-summary switch must cover the new tools")
	}
}

// TestParallelAndSubagentListsAreReadOnly فهرست موازی و allowlist زیرایجنت
// فقط ابزارهای فقط‌خواندنیِ «موجود» را دارند.
func TestParallelAndSubagentListsAreReadOnly(t *testing.T) {
	defs := map[string]bool{}
	for _, d := range agentToolDefs() {
		defs[d.Function.Name] = true
	}

	for name := range parallelReadOnlyTools {
		if !defs[name] {
			t.Errorf("parallel list references unknown tool %q", name)
			continue
		}
		// فقط‌خواندنی = پیش‌فرض allow حتی با ConfirmFileWrites روشن
		if d := toolDefaultDecision(name, true); d != permAllow {
			t.Errorf("parallel tool %q must be read-only (default allow), got %v", name, d)
		}
	}

	for name := range subagentAllowedTools {
		if !defs[name] {
			t.Errorf("sub-agent allow-list references unknown tool %q", name)
			continue
		}
		if d := toolDefaultDecision(name, true); d != permAllow {
			t.Errorf("sub-agent tool %q must be read-only (default allow), got %v", name, d)
		}
		// ابزارهای زیرایجنت باید در فهرست موازی هم مجاز باشند (همه فقط‌خواندنی‌اند)
		if !parallelReadOnlyTools[name] {
			t.Errorf("sub-agent tool %q is read-only and should also be parallel-safe", name)
		}
	}
}
