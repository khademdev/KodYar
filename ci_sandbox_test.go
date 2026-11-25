//go:build windows

package main

// تست پیکربندی CI: وعدهٔ امنیتی سندباکس فقط وقتی معنا دارد که مجوز NUL در
// همان job اجباری اجرا شود و تست‌ها هم به‌جای skip، شکست بدهند. اگر کسی
// `continue-on-error` را برگرداند یا env را بردارد، همین تست قرمز می‌شود.

import (
	"os"
	"strings"
	"testing"
)

func TestCISandboxNulGrantIsEnforced(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("ci.yml: %v", err)
	}
	s := string(data)

	if !strings.Contains(s, "grant-sandbox-nul.ps1") {
		t.Fatal("ci.yml must run scripts/grant-sandbox-nul.ps1 on the Windows runner")
	}

	// ادامه‌دادنِ بی‌صدا روی همان مرحله ممنوع است: grant باید یا موفق شود یا
	// job را قرمز کند.
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "grant-sandbox-nul.ps1") {
			continue
		}
		for j := i; j >= 0 && j > i-8; j-- {
			if strings.Contains(lines[j], "continue-on-error") {
				t.Fatalf("ci.yml line %d: the NUL grant step must not be continue-on-error", j+1)
			}
		}
	}

	// تست‌ها باید بدانند مجوز وعده داده شده تا skip محیطی به شکست تبدیل شود.
	if n := strings.Count(s, "KODYAR_EXPECT_NUL_GRANTED"); n < 2 {
		t.Fatalf("ci.yml must set KODYAR_EXPECT_NUL_GRANTED for the test and proof steps (found %d)", n)
	}
}

// TestCIHasNoSilentSteps — هیچ مرحله یا jobی در CI نباید با continue-on-error
// بی‌صدا شکست بخورد: هر گامی که تست، بیلد یا امنیت را می‌سنجد باید CI را قرمز
// کند. استثنایی وجود ندارد (حتی browser-smoke).
func TestCIHasNoSilentSteps(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("ci.yml: %v", err)
	}
	s := string(data)

	for i, line := range strings.Split(s, "\n") {
		// توضیح‌های YAML که به این کلید اشاره می‌کنند، تنظیم واقعی نیستند.
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, "continue-on-error") {
			t.Fatalf("ci.yml line %d: remove continue-on-error so failures are never silent", i+1)
		}
	}

	// jobهای اجباری نباید حذف شوند.
	for _, gate := range []string{
		"Wails build (windows/amd64)",
		"Browser smoke (frontend)",
	} {
		if !strings.Contains(s, gate) {
			t.Fatalf("ci.yml must keep the %q job as a required gate", gate)
		}
	}
}
