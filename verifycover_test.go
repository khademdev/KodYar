// تست‌های پوشش راستی‌آزمایی (فاز ۲) — تحلیل دستور، نگه‌داری مسیرها و متن تذکر.
package main

import (
	"strings"
	"testing"
)

func TestParseVerifScopesKinds(t *testing.T) {
	cases := []struct {
		cmd     string
		count   int
		kind    string
		all     bool
		targets []string
	}{
		{"go build ./...", 1, "build", true, nil},
		{"go test ./pkg/a/... ./cmd/myai/...", 1, "test", false, []string{"pkg/a", "cmd/myai"}},
		{"cd sub && go test ./...", 1, "test", true, nil},
		{"go build ./... && go test ./pkg/a/...", 2, "test", false, []string{"pkg/a"}},
		{"npm test", 1, "test", false, nil},
		{"pytest -q tests", 1, "test", false, nil},
		{"go fmt ./...", 0, "", false, nil},
	}
	for _, c := range cases {
		scopes := parseVerifScopes(c.cmd)
		if len(scopes) != c.count {
			t.Fatalf("%q: got %d scopes, want %d (%+v)", c.cmd, len(scopes), c.count, scopes)
		}
		if c.count == 0 {
			continue
		}
		last := scopes[len(scopes)-1]
		if last.Kind != c.kind || last.All != c.all {
			t.Fatalf("%q: kind=%q all=%v, want kind=%q all=%v", c.cmd, last.Kind, last.All, c.kind, c.all)
		}
		if c.targets != nil && strings.Join(last.Targets, ",") != strings.Join(c.targets, ",") {
			t.Fatalf("%q: targets=%v, want %v", c.cmd, last.Targets, c.targets)
		}
	}
}

func TestCoverageGaps(t *testing.T) {
	files := []string{"pkg/a/a.go", "cmd/myai/main.go"}

	// فقط build روی کل مخزن: هر دو پوشه بی‌پوشش، buildOnly=true
	gaps, buildOnly := coverageGaps(files, "go build ./...")
	if len(gaps) != 2 || gaps[0] != "cmd/myai" || gaps[1] != "pkg/a" {
		t.Fatalf("build-only must leave both dirs uncovered: %v", gaps)
	}
	if !buildOnly {
		t.Fatal("build-only must be reported as weak coverage")
	}

	// تست هدفمند یکی را می‌پوشاند
	gaps, buildOnly = coverageGaps(files, "go test ./pkg/a/...")
	if len(gaps) != 1 || gaps[0] != "cmd/myai" || buildOnly {
		t.Fatalf("only cmd/myai must be missing: %v buildOnly=%v", gaps, buildOnly)
	}

	// ./... همه را می‌پوشاند
	if gaps, _ = coverageGaps(files, "go test ./..."); len(gaps) != 0 {
		t.Fatalf("go test ./... must cover everything, got %v", gaps)
	}

	// پوشه‌ی والد، فرزند را می‌پوشاند
	if gaps, _ = coverageGaps([]string{"pkg/a/deep/x.go"}, "go test ./pkg/..."); len(gaps) != 0 {
		t.Fatalf("parent package must cover nested dirs, got %v", gaps)
	}

	// بدون دستور → همه بی‌پوشش ولی buildOnly=false (هیچ چیز اجرا نشده)
	if gaps, buildOnly = coverageGaps(files, ""); len(gaps) != 2 || buildOnly {
		t.Fatalf("empty command: gaps=%v buildOnly=%v", gaps, buildOnly)
	}
}

func TestSuggestTargetedTest(t *testing.T) {
	goCmds := []string{"go build ./...", "go test ./..."}
	if got := suggestTargetedTest(goCmds, []string{"pkg/a"}); got != "go test ./pkg/a/..." {
		t.Fatalf("go suggestion = %q", got)
	}
	if got := suggestTargetedTest(goCmds, []string{".", "pkg/a"}); got != "go test ./..." {
		t.Fatalf("root dir must suggest ./...: %q", got)
	}
	// چند پوشه + سقف چهارتایی
	got := suggestTargetedTest(goCmds, []string{"a", "b", "c", "d", "e", "f"})
	if !strings.HasPrefix(got, "go test ./a/... ./b/... ./c/... ./d/...") || !strings.Contains(got, "…") {
		t.Fatalf("multi-dir suggestion = %q", got)
	}
	// npm مسیر نمی‌پذیرد
	if got := suggestTargetedTest([]string{"npm test"}, []string{"pkg/a"}); got != "npm test" {
		t.Fatalf("npm suggestion = %q", got)
	}
	// بدون دستور تست
	if got := suggestTargetedTest([]string{"go build ./..."}, []string{"pkg/a"}); got != "" {
		t.Fatalf("no test command must suggest nothing, got %q", got)
	}
}

func TestVerificationNudgePayload(t *testing.T) {
	nudge, notice, gaps := verificationNudgePayload(
		[]string{"pkg/a/a.go"}, "go build ./...", []string{"go test ./..."})
	if len(gaps) != 1 || gaps[0] != "pkg/a" {
		t.Fatalf("gaps = %v", gaps)
	}
	// متن پایهٔ قبلی باید دست‌نخورده در ابتدا بماند (قرارداد تست‌های e2e)
	if !strings.HasPrefix(nudge, verificationNudgeText) {
		t.Fatalf("nudge must start with the base text: %q", nudge)
	}
	for _, want := range []string{
		"Changed directories: pkg/a",
		"A build-only check does not cover these changes.",
		"Run a targeted test, e.g.: go test ./pkg/a/...",
	} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("nudge missing %q: %q", want, nudge)
		}
	}
	if !strings.Contains(notice, "pkg/a") || !strings.Contains(notice, "go test ./pkg/a/...") {
		t.Fatalf("notice must name the dir and the suggested command: %q", notice)
	}
}

// TestNudgeCarriesChangedDirsEndToEnd — سیم‌کشی واقعی: مسیر تغییرکرده در
// ثبت نوبت ذخیره شود و در تذکر تزریق‌شده به مدل دیده شود.
func TestNudgeCarriesChangedDirsEndToEnd(t *testing.T) {
	env := newE2E(t,
		stepTool("c1", "write_file", `{"path":"pkg/a/a.go","content":"package a\n\nfunc A() {}\n"}`),
		stepTool("c2", "run_command", `{"command":"go build ./..."}`),
		stepText("ساخته شد."),
		stepText("تذکر را دیدم."),
	)
	env.send("کار کن")
	reqs := env.model.requests()
	if len(reqs) < 4 {
		t.Fatalf("expected a nudge round, got %d requests", len(reqs))
	}
	msg := requestMessages(reqs[3])
	if !strings.Contains(msg, verificationNudgeText) {
		t.Fatalf("nudge missing:\n%s", msg)
	}
	if !strings.Contains(msg, "Changed directories: pkg/a") {
		t.Fatalf("nudge must name the changed directory:\n%s", msg)
	}
}

func TestTurnRecorderTracksChangedFiles(t *testing.T) {
	rec := &turnRecorder{}
	rec.recordFileChange("pkg/a/a.go")
	rec.recordFileChange("./pkg/a/a.go") // تکراری پس از نرمال‌سازی
	rec.recordFileChange(`cmd\myai\main.go`)
	got := rec.changedFiles()
	if len(got) != 2 || got[0] != "pkg/a/a.go" || got[1] != "cmd/myai/main.go" {
		t.Fatalf("changed files = %v", got)
	}
	// فراخوانی بدون مسیر هم باید جواب بدهد (سازگاری عقب‌رو)
	rec.recordFileChange()
	if len(rec.changedFiles()) != 2 {
		t.Fatal("no-arg call must not add a path")
	}
	// سقف
	for i := 0; i < maxTurnFiles*2; i++ {
		rec.recordFileChange("f" + itoaSmall(i) + ".go")
	}
	if len(rec.changedFiles()) > maxTurnFiles {
		t.Fatalf("file list must be capped at %d", maxTurnFiles)
	}
}
