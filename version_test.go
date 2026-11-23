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

// TestSettingsViewCarriesVersion — نسخهٔ اجراشده در UI دیده می‌شود (همان مقدار
// ldflags) و پرچم «این Provider reasoning_effort را رد کرده» هم می‌آید.
func TestSettingsViewCarriesVersion(t *testing.T) {
	a := newTestApp(t)
	view := a.SettingsView()
	if got, _ := view["version"].(string); got != version {
		t.Fatalf("SettingsView version = %q, want %q", got, version)
	}
	if limited, ok := view["reasoningEffortLimited"].(bool); !ok || limited {
		t.Fatalf("reasoningEffortLimited must be present and false, got %v", view["reasoningEffortLimited"])
	}
	a.noteReasoningEffortRejected("http://localhost:11434/v1")
	if limited, _ := a.SettingsView()["reasoningEffortLimited"].(bool); !limited {
		t.Fatal("rejected provider must be reported to the UI")
	}
}

// TestReleaseWorkflowUsesChangelog — توضیح انتشار از CHANGELOG استخراج می‌شود،
// نه از فایل دستیِ جدا که ممکن است همگام بماند.
func TestReleaseWorkflowUsesChangelog(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatalf("release.yml: %v", err)
	}
	s := string(data)
	for _, want := range []string{"CHANGELOG.md", "release-notes.md", "bash build.sh"} {
		if !strings.Contains(s, want) {
			t.Fatalf("release.yml must use %q for the release notes/build", want)
		}
	}
	if strings.Contains(s, "RELEASE_NOTES.md") {
		t.Fatal("release.yml must not depend on the manual RELEASE_NOTES.md")
	}
}
