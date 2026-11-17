// تست هم‌خوانی نسخه — ضعف P2 #۹ (نسخه ناهمگام) نباید برگردد.
package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// changelogLatestVersion اولین سرفصل نسخه در CHANGELOG.md را برمی‌گرداند.
func changelogLatestVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatalf("CHANGELOG.md: %v", err)
	}
	re := regexp.MustCompile(`(?m)^## (\d+\.\d+(?:\.\d+)?)`)
	m := re.FindStringSubmatch(string(data))
	if m == nil {
		t.Fatal("no version heading (## X.Y) found in CHANGELOG.md")
	}
	return m[1]
}

func TestVersionMatchesChangelog(t *testing.T) {
	want := changelogLatestVersion(t)
	// نسخهٔ کامل (1.13.0) یا دو‌بخشی (1.13) هر دو پذیرفته‌اند.
	if version != want && !strings.HasPrefix(version, want+".") {
		t.Fatalf("main.version = %q but CHANGELOG.md starts at %q — bump one of them", version, want)
	}
}

func TestWailsProductVersionMatches(t *testing.T) {
	data, err := os.ReadFile("wails.json")
	if err != nil {
		t.Fatalf("wails.json: %v", err)
	}
	re := regexp.MustCompile(`"productVersion"\s*:\s*"([^"]+)"`)
	m := re.FindStringSubmatch(string(data))
	if m == nil {
		t.Fatal("productVersion missing from wails.json")
	}
	if m[1] != version {
		t.Fatalf("wails.json productVersion = %q but main.version = %q", m[1], version)
	}
}

func TestBuildScriptAndCLIUseTheSameVersion(t *testing.T) {
	for _, f := range []string{"build.sh", ".github/workflows/ci.yml"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if !strings.Contains(string(data), version) {
			t.Fatalf("%s does not carry the current version %q (build artifacts would report a different version)", f, version)
		}
	}
}
