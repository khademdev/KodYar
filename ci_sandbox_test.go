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

// TestCIHasNoSilentSteps — هیچ مرحله/کاری که بیلد یا امنیت را می‌سنجد نباید
// با continue-on-error بی‌صدا شکست بخورد. تنها استثنا job اختیاری
// «browser-smoke» است (smoke فرانت که به نصب Playwright روی رانر وابسته است).
func TestCIHasNoSilentSteps(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("ci.yml: %v", err)
	}
	lines := strings.Split(string(data), "\n")

	jobKey := func(line string) bool {
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "  #") {
			return false
		}
		body := strings.TrimPrefix(line, "  ")
		return body != "" && !strings.HasPrefix(body, "-") && strings.Contains(body, ":")
	}

	optionalStart, optionalEnd := -1, len(lines)
	for i, line := range lines {
		if optionalStart < 0 && strings.HasPrefix(line, "  browser-smoke:") {
			optionalStart = i
			continue
		}
		if optionalStart >= 0 && i > optionalStart && jobKey(line) {
			optionalEnd = i
			break
		}
	}
	if optionalStart < 0 {
		t.Fatal("ci.yml must contain the browser-smoke job")
	}

	for i, line := range lines {
		// توضیح‌های YAML که به این کلید اشاره می‌کنند، تنظیم واقعی نیستند.
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.Contains(line, "continue-on-error") {
			continue
		}
		if i < optionalStart || i > optionalEnd {
			t.Fatalf("ci.yml line %d: remove continue-on-error so failures are never silent", i+1)
		}
	}

	if !strings.Contains(string(data), "Wails build (windows/amd64)") {
		t.Fatal("ci.yml must keep the Wails build job as a required gate")
	}
}
