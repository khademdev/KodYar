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

func TestSandboxNulGrantPromisedHelper(t *testing.T) {
	if sandboxNulGrantPromised() {
		t.Fatal("helper must be false without KODYAR_EXPECT_NUL_GRANTED=1")
	}
	t.Setenv("KODYAR_EXPECT_NUL_GRANTED", "1")
	if !sandboxNulGrantPromised() {
		t.Fatal("helper must honor KODYAR_EXPECT_NUL_GRANTED=1")
	}
}
