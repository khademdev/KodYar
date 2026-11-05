// تست‌های escape آرگومان شل و ابزارهای سفارشی (جدا شده از فایل رفع‌های مرحله‌ای).
package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestEscapeShellArg(t *testing.T) {
	// انتظارها وابسته به پلتفرم‌اند: روی ویندوز دابل‌کوت (cmd)،
	// روی یونیکس تک‌کوت — همان رفتاری که escapeShellArg با isWindows دارد.
	var cases map[string]string
	if isWindows() {
		cases = map[string]string{
			`hello`:         `"hello"`,
			`x; rm -rf /`:   `"x; rm -rf /"`,
			`a'b`:           `"a'b"`,
			`$(whoami)`:     `"$(whoami)"`,
			"with\nnewline": `"with newline"`,
		}
	} else {
		cases = map[string]string{
			`hello`:         `'hello'`,
			`x; rm -rf /`:   `'x; rm -rf /'`,
			`a'b`:           `'a'\''b'`,
			`$(whoami)`:     `'$(whoami)'`,
			"with\nnewline": `'with newline'`,
		}
	}
	for in, want := range cases {
		if got := escapeShellArg(in); got != want {
			t.Errorf("escapeShellArg(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestExecCustomToolEscaping: مقدار خطرناک مدل نباید بتواند از قالب فرمان فرار کند.
func TestExecCustomToolEscaping(t *testing.T) {
	a := newTestApp(t)
	a.settings.CustomTools = []CustomTool{{
		Name:        "greet",
		Command:     "echo hi {{name}}",
		Description: "test",
		Parameters:  `{"type":"object","properties":{"name":{"type":"string"}}}`,
	}}
	// تلاش برای تزریق: مقدار می‌باید quote شود و دستور دوم اجرا نشود
	out, ok := a.execCustomTool(context.Background(), "custom_greet", map[string]interface{}{
		"name": "x; touch PWNED",
	})
	if !ok {
		t.Fatalf("echo must succeed: %s", out)
	}
	if p := filepath.Join(a.settings.WorkspacePath, "PWNED"); fileExists(p) {
		t.Fatal("injection! file PWNED was created")
	}
	if !strings.Contains(out, "x; touch PWNED") {
		t.Fatalf("value must appear quoted/verbatim in output: %q", out)
	}
}

// ---------------------------------------------------------------- ابزار سفارشی

// TestCustomToolDefsAdvertised: تعریف ابزار سفارشی باید در customToolDefs ظاهر شود.
func TestCustomToolDefsAdvertised(t *testing.T) {
	defs := customToolDefs([]CustomTool{
		{Name: "mytool", Command: "echo {{x}}", Description: "d",
			Parameters: `{"type":"object","properties":{"x":{"type":"string"}}}`},
		{Name: "", Command: "x"}, // نامعتبر — باید حذف شود
	})
	if len(defs) != 1 {
		t.Fatalf("expected 1 def, got %d", len(defs))
	}
	if defs[0].Function.Name != "custom_mytool" {
		t.Fatalf("wrong name: %s", defs[0].Function.Name)
	}
}

// ---------------------------------------------------------------- نمایش
