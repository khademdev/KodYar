//go:build windows

package main

// تست پیکربندی CI: وعدهٔ امنیتی سندباکس فقط وقتی معنا دارد که مجوز NUL در
// همان job اجباری اجرا شود و تست‌ها هم به‌جای skip، شکست بدهند. اگر کسی
// `continue-on-error` را برگرداند یا env را بردارد، همین تست قرمز می‌شود.

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// actionMajor شمارهٔ major یک ref مثل `v5.0.0` را برمی‌گرداند (۰ اگر نامعتبر).
func actionMajor(ref string) int {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "v")
	n, err := strconv.Atoi(strings.SplitN(ref, ".", 2)[0])
	if err != nil {
		return 0
	}
	return n
}

// node24Actions حداقل نسخه‌ای از هر اکشن که خودش runtimeِ node24 را هدف
// می‌گیرد. اکشن‌های node20 با هشدار «Node.js 20 is deprecated» مجبور به اجرا
// روی Node 24 می‌شوند و با حذف node20 از رانرها از کار می‌افتند.
var node24Actions = []struct {
	action string
	min    int
}{
	{"actions/setup-node@", 5},
	{"actions/cache@", 5},
	{"actions/setup-go@", 6},
	{"actions/upload-artifact@", 6},
}

// TestCIUsesNode24ActionRuntimes — هر دو workflow باید اکشن‌ها را روی نسخه‌ای
// پین کنند که خودش node24 را هدف می‌گیرد؛ نسخهٔ v4/v5 روی node20 اعلام شده
// بود و همین باعث هشدار در هر ران بود.

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

// TestBrowserSmokeInstallsFromLockfile — job اجباری browser-smoke باید با
// `npm ci` از lockfileِ کامیت‌شده نصب کند (نه `npm install` شناور) و کش npm
// را روی همان lockfile کلید بزند؛ وگرنه نسخه‌ی شناور وابستگی می‌تواند این
// job بلاک‌کننده را بی‌دلیل قرمز کند.
func TestBrowserSmokeInstallsFromLockfile(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("ci.yml: %v", err)
	}
	s := string(data)

	// خط‌محور: کامنت‌های YAML که به این فرمان‌ها اشاره می‌کنند، تنظیم واقعی نیستند.
	var usesCleanInstall, usesPlainInstall bool
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, "npm ci") {
			usesCleanInstall = true
		}
		if strings.Contains(line, "npm install") {
			usesPlainInstall = true
		}
	}
	if !usesCleanInstall {
		t.Fatal("ci.yml must install the browser-smoke dependencies with `npm ci`")
	}
	if usesPlainInstall {
		t.Fatal("ci.yml must not use `npm install` for the smoke dependencies; it ignores the lockfile")
	}
	if !strings.Contains(s, "cache-dependency-path: frontend/tests/package-lock.json") {
		t.Fatal("ci.yml must key the npm cache on frontend/tests/package-lock.json")
	}
	// مرورگر کرومیوم (~۱۰۰MB) باید کش شود و کلیدش به نسخهٔ Playwright گره
	// بخورد، وگرنه هر push دوباره دانلود می‌کند یا کش کهنه استفاده می‌شود.
	if !strings.Contains(s, "path: ~/.cache/ms-playwright") {
		t.Fatal("ci.yml must cache ~/.cache/ms-playwright for the smoke job")
	}
	if !strings.Contains(s, "steps.playwright.outputs.version") {
		t.Fatal("the ms-playwright cache key must include the resolved Playwright version")
	}
	lock, err := os.ReadFile("frontend/tests/package-lock.json")
	if err != nil {
		t.Fatalf("committed lockfile for the smoke tests is missing: %v", err)
	}
	// npm روی ویندوز ورودی وابستگی اختیاری macOS (fsevents) را از lockfile
	// حذف می‌کند و بعد `npm ci` با «Missing: fsevents@ from lock file» قرمز
	// می‌شود؛ پس وجود همان ورودی هم جزء قرارداد است.
	if !strings.Contains(string(lock), "\"node_modules/fsevents\"") {
		t.Fatal("frontend/tests/package-lock.json must keep the node_modules/fsevents entry or `npm ci` fails")
	}
}

func TestCIUsesNode24ActionRuntimes(t *testing.T) {
	// اکشن‌های node20 در هر دو workflow هشدار می‌دادند: CI و انتشار.
	for _, path := range []string{".github/workflows/ci.yml", ".github/workflows/release.yml"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}

		seen := map[string]bool{}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			// توضیح‌های YAML تنظیم واقعی نیستند.
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			for _, a := range node24Actions {
				if !strings.Contains(trimmed, "uses: "+a.action) {
					continue
				}
				seen[a.action] = true
				ref := strings.TrimSpace(strings.SplitN(trimmed, a.action, 2)[1])
				if actionMajor(ref) < a.min {
					t.Fatalf("%s: %s%s targets the deprecated node20 runtime; pin v%d.0.0 or newer", path, a.action, ref, a.min)
				}
			}
		}

		// هر فایل فقط اکشن‌هایی را که واقعاً دارد بررسی می‌کند، ولی حذف
		// اکشن‌های حساس باید همین تست را قرمز کند.
		required := []string{"actions/setup-go@"}
		if strings.HasSuffix(path, "ci.yml") {
			required = []string{"actions/setup-node@", "actions/cache@", "actions/setup-go@", "actions/upload-artifact@"}
		}
		for _, action := range required {
			if !seen[action] {
				t.Fatalf("%s no longer uses %s; update this guard if the action was replaced", path, action)
			}
		}
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
