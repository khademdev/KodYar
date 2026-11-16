// نگهبانِ قابلیت کپی/پیست در ویندوز.
//
// Wails به‌صورت پیش‌فرض منوی زمینه‌ی بومی WebView2 را در buildهای production
// خاموش می‌کند؛ بدون EnableDefaultContextMenu، راست‌کلیک هیچ منویی (کپی/برش/
// چسباندن/انتخاب همه) نشان نمی‌دهد. این تست همان قرارداد را ثابت نگه می‌دارد.
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDefaultContextMenuEnabled(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("main.go: %v", err)
	}
	if !strings.Contains(string(raw), "EnableDefaultContextMenu: true") {
		t.Fatal("main.go must set EnableDefaultContextMenu: true so right-click Cut/Copy/Paste works in production")
	}
}

// TestCopyableOutputsAreSelectable — رگرسیون: قاعده‌ی سراسری
// body{user-select:none} متن خروجی ابزارها (<pre class="tool-result">) و
// نمای diff/لاگ را غیرقابل‌انتخاب می‌کرد؛ منوی راست‌کلیک آنجا باز می‌شد ولی
// «کپی» متنِ انتخابی نداشت (فقط .msg-bubble و .md استثنا بودند). این چک
// استثنای CSS را الزامی نگه می‌دارد.
func TestCopyableOutputsAreSelectable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("frontend", "dist", "assets", "css", "app.css"))
	if err != nil {
		t.Fatalf("app.css: %v", err)
	}
	css := string(raw)
	selectable := func(want string) bool {
		for _, m := range reCSSRule.FindAllStringSubmatch(css, -1) {
			if strings.Contains(m[1], want) && reUserSelectText.MatchString(m[2]) {
				return true
			}
		}
		return false
	}
	for _, sel := range []string{".tool-result", ".diff-view", ".log-view"} {
		if !selectable(sel) {
			t.Fatalf("app.css must set user-select:text on %s (body sets user-select:none, so right-click Copy would have no selection there)", sel)
		}
	}
}

var (
	reCSSRule        = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
	reUserSelectText = regexp.MustCompile(`user-select\s*:\s*text`)
)
