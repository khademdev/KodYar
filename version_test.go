// تست هم‌خوانی نسخه — ضعف P2 #۹ (نسخه ناهمگام) نباید برگردد.
package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// canonicalVersion محتوای فایل VERSION (تک‌منبع نسخه) را می‌خواند.
func canonicalVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatalf("VERSION: %v", err)
	}
	v := strings.TrimSpace(string(data))
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(v) {
		t.Fatalf("VERSION = %q must be MAJOR.MINOR.PATCH", v)
	}
	return v
}

// TestVersionSingleSource — نسخه در یک فایل است و هیچ مصرف‌کننده‌ای عدد دستی
// نگه نمی‌دارد؛ ناهماهنگی در CI شکست می‌خورد (پیش‌تر ۵ فایل دستی هماهنگ می‌شد).
func TestVersionSingleSource(t *testing.T) {
	want := canonicalVersion(t)

	if version != want {
		t.Errorf("main.version = %q but VERSION = %q", version, want)
	}

	wailsJSON, err := os.ReadFile("wails.json")
	if err != nil {
		t.Fatalf("wails.json: %v", err)
	}
	m := regexp.MustCompile(`"productVersion"\s*:\s*"([^"]+)"`).FindStringSubmatch(string(wailsJSON))
	if m == nil {
		t.Fatal("productVersion missing from wails.json")
	}
	if m[1] != want {
		t.Errorf("wails.json productVersion = %q but VERSION = %q", m[1], want)
	}
}

// TestVersionConsumersReadTheFile — اسکریپت بیلد، CI و README باید از VERSION
// بخوانند، نه عدد ثابت.
func TestVersionConsumersReadTheFile(t *testing.T) {
	for _, f := range []string{"build.sh", ".github/workflows/ci.yml", "README.md"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		s := string(data)
		if !strings.Contains(s, "cat VERSION") {
			t.Errorf("%s must read the version from VERSION (cat VERSION)", f)
		}
		if loc := regexp.MustCompile(`build\.sh \d+\.\d+`).FindString(s); loc != "" {
			t.Errorf("%s hardcodes a version (%q); read VERSION instead", f, loc)
		}
	}
	// build.sh نباید پیش‌فرض عددی داشته باشد.
	build, err := os.ReadFile("build.sh")
	if err != nil {
		t.Fatalf("build.sh: %v", err)
	}
	if regexp.MustCompile(`VERSION="\$\{1:-\d`).MatchString(string(build)) {
		t.Error(`build.sh default version must come from VERSION, not a number`)
	}
}

// TestVersionMatchesChangelog — آخرین سرفصل شماره‌دار CHANGELOG با VERSION یکی
// است؛ یک بخش Unreleased می‌تواند بالاتر باشد.
func TestVersionMatchesChangelog(t *testing.T) {
	want := canonicalVersion(t)
	data, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatalf("CHANGELOG.md: %v", err)
	}
	headings := regexp.MustCompile(`(?m)^## (\d+\.\d+\.\d+)`).FindAllStringSubmatch(string(data), -1)
	if len(headings) == 0 {
		t.Fatal("no version heading (## X.Y.Z) found in CHANGELOG.md")
	}
	if got := headings[0][1]; got != want {
		t.Fatalf("CHANGELOG.md latest version = %q but VERSION = %q — bump one of them", got, want)
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
