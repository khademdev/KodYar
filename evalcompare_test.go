// مقایسهٔ دو گزارش JSON هارنس ارزیابی (evalsuite_test.go).
//
// چرا: یک عدد تنها نمی‌گوید بهتر یا بدتر شد. اینجا دو گزارش (مثلاً قبل/بعد
// یک تغییر در پرامپت یا ابزارها، یا مدل نمایش‌نامه‌ای در برابر مدل واقعی)
// کنار هم گذاشته می‌شوند و تفاوت نرخ موفقیت، دور، توکن و ابزار اشتباه
// گزارش می‌شود.
//
// اجرا:
//
//	KODYAR_EVAL_COMPARE=old.json,new.json go test -run TestAgentEvalCompare -v .
//
// گزارش‌ها را با KODYAR_EVAL_OUT بسازید (نگاه کنید به evalsuite_test.go).
// TestEvalCompareReports همیشه اجرا می‌شود و ریاضیِ تفاضل را با دادهٔ ساختگی
// می‌سنجد تا این ابزار خودش بی‌تست نماند.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// readEvalReport یک گزارش JSON را می‌خواند و شکل آن را بررسی می‌کند.
func readEvalReport(path string) (evalReportFile, error) {
	var rep evalReportFile
	data, err := os.ReadFile(path)
	if err != nil {
		return rep, err
	}
	if err := json.Unmarshal(data, &rep); err != nil {
		return rep, fmt.Errorf("%s is not an eval report: %w", path, err)
	}
	if rep.Scenarios == 0 && len(rep.Rows) == 0 {
		return rep, fmt.Errorf("%s looks empty: no scenarios or rows", path)
	}
	if rep.Scenarios == 0 {
		rep.Scenarios = len(rep.Rows)
	}
	return rep, nil
}

// evalReportLabel سرصفحهٔ کوتاه یک گزارش.
func evalReportLabel(rep evalReportFile) string {
	label := "mode=" + rep.Mode
	if rep.Model != "" {
		label += " model=" + rep.Model
	}
	if rep.GeneratedAt != "" {
		label = rep.GeneratedAt + " " + label
	}
	return fmt.Sprintf("%s scenarios=%d", label, rep.Scenarios)
}

// signed عدد صحیح با علامت (برای ستون delta).
func signed(v int64) string {
	if v > 0 {
		return "+" + itoa64(v)
	}
	return itoa64(v)
}

// signedF عدد اعشاری با علامت.
func signedF(v float64) string {
	if v > 0 {
		return fmt.Sprintf("+%.1f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

// itoa64 تبدیل int64 به رشته بدون وابستگی اضافه.
func itoa64(v int64) string { return fmt.Sprintf("%d", v) }

// compareEvalReports جدول متریک‌ها + تغییر وضعیت هر سناریو را برمی‌گرداند.
// delta = B − A.
func compareEvalReports(a, b evalReportFile) string {
	var s strings.Builder
	s.WriteString("AGENT EVAL COMPARISON (delta = B - A)\n\n")
	fmt.Fprintf(&s, "A: %s\n", evalReportLabel(a))
	fmt.Fprintf(&s, "B: %s\n\n", evalReportLabel(b))

	rate := func(r evalReportFile) float64 {
		if r.Scenarios == 0 {
			return 0
		}
		return float64(r.Passed) * 100 / float64(r.Scenarios)
	}

	fmt.Fprintf(&s, "%-20s %-24s %-24s %s\n", "metric", "A", "B", "delta")
	s.WriteString(strings.Repeat("-", 88) + "\n")
	fmt.Fprintf(&s, "%-20s %-24s %-24s %s\n", "success",
		fmt.Sprintf("%d/%d (%.1f%%)", a.Passed, a.Scenarios, rate(a)),
		fmt.Sprintf("%d/%d (%.1f%%)", b.Passed, b.Scenarios, rate(b)),
		fmt.Sprintf("%s scenario(s), %s pp", signed(int64(b.Passed-a.Passed)), signedF(rate(b)-rate(a))))
	fmt.Fprintf(&s, "%-20s %-24s %-24s %s\n", "avg rounds",
		fmt.Sprintf("%.1f", a.AvgRounds), fmt.Sprintf("%.1f", b.AvgRounds),
		signedF(b.AvgRounds-a.AvgRounds))
	fmt.Fprintf(&s, "%-20s %-24s %-24s %s\n", "total tokens",
		itoa64(a.Tokens), itoa64(b.Tokens), signed(b.Tokens-a.Tokens))
	fmt.Fprintf(&s, "%-20s %-24s %-24s %s\n", "wrong tool calls",
		itoa64(int64(a.WrongTools)), itoa64(int64(b.WrongTools)),
		signed(int64(b.WrongTools-a.WrongTools)))
	fmt.Fprintf(&s, "%-20s %-24s %-24s %s\n", "tool errors",
		itoa64(int64(a.ToolErrors)), itoa64(int64(b.ToolErrors)),
		signed(int64(b.ToolErrors-a.ToolErrors)))

	ra := map[string]evalReportRow{}
	for _, r := range a.Rows {
		ra[r.Name] = r
	}
	rb := map[string]evalReportRow{}
	for _, r := range b.Rows {
		rb[r.Name] = r
	}
	names := make([]string, 0, len(ra)+len(rb))
	seen := map[string]bool{}
	for _, r := range a.Rows {
		if !seen[r.Name] {
			seen[r.Name] = true
			names = append(names, r.Name)
		}
	}
	for _, r := range b.Rows {
		if !seen[r.Name] {
			seen[r.Name] = true
			names = append(names, r.Name)
		}
	}
	sort.Strings(names)

	var lines []string
	for _, name := range names {
		x, inA := ra[name]
		y, inB := rb[name]
		switch {
		case inA && !inB:
			lines = append(lines, fmt.Sprintf("  %-34s only in A (%s)", name, passWord(x.Passed)))
		case !inA && inB:
			lines = append(lines, fmt.Sprintf("  %-34s only in B (%s)", name, passWord(y.Passed)))
		case x.Passed != y.Passed:
			lines = append(lines, fmt.Sprintf("  %-34s %s -> %s   rounds %d -> %d   tokens %d -> %d",
				name, passWord(x.Passed), passWord(y.Passed), x.Rounds, y.Rounds, x.Tokens, y.Tokens))
		default:
			continue
		}
	}
	if len(lines) > 0 {
		s.WriteString("\nscenario changes:\n")
		s.WriteString(strings.Join(lines, "\n") + "\n")
	}

	// سناریوهای مشترکی که در هر دو شکست خورده‌اند، برای رسیدگی أولویت دارند.
	var stillFailing []string
	for _, name := range names {
		x, inA := ra[name]
		y, inB := rb[name]
		if inA && inB && !x.Passed && !y.Passed {
			stillFailing = append(stillFailing, name)
		}
	}
	if len(stillFailing) > 0 {
		s.WriteString("\nstill failing in both:\n")
		for _, name := range stillFailing {
			s.WriteString("  - " + name + "\n")
		}
	}

	// سناریوهایی که در A نبوده‌اند در شمارش نرخ موفقیت B اثر می‌گذارند؛
	// این یادداشت جلوی مقایسهٔ گمراه‌کننده را می‌گیرد.
	if a.Scenarios != b.Scenarios {
		fmt.Fprintf(&s, "\nnote: reports cover different scenario counts (%d vs %d); compare per-scenario rows, not only the totals.\n",
			a.Scenarios, b.Scenarios)
	}
	return s.String()
}

// passWord برچسب وضعیت.
func passWord(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// TestAgentEvalCompare با KODYAR_EVAL_COMPARE="old.json,new.json" دو گزارش را
// مقایسه می‌کند (بدون آن skip).
func TestAgentEvalCompare(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("KODYAR_EVAL_COMPARE"))
	if raw == "" {
		t.Skip("report comparison skipped: set KODYAR_EVAL_COMPARE=old.json,new.json")
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' })
	if len(parts) != 2 {
		t.Fatalf("KODYAR_EVAL_COMPARE needs exactly two paths (A,B), got %q", raw)
	}
	a, err := readEvalReport(strings.TrimSpace(parts[0]))
	if err != nil {
		t.Fatalf("read report A: %v", err)
	}
	b, err := readEvalReport(strings.TrimSpace(parts[1]))
	if err != nil {
		t.Fatalf("read report B: %v", err)
	}
	t.Log("\n" + compareEvalReports(a, b))
}

// TestEvalCompareReports ریاضیِ مقایسه را با دادهٔ ساختگی می‌سنجد و همیشه
// اجرا می‌شود (بدون شبکه، بدون فایل بیرونی).
func TestEvalCompareReports(t *testing.T) {
	a := evalReportFile{
		GeneratedAt: "2026-01-01T00:00:00Z", Mode: "mock", Scenarios: 2, Passed: 2,
		SuccessRate: 100, AvgRounds: 4, Tokens: 1000, ToolErrors: 5,
		Rows: []evalReportRow{
			{Name: "same-both-pass", Passed: true, Rounds: 4, Tokens: 600},
			{Name: "regressed", Passed: true, Rounds: 4, Tokens: 400},
		},
	}
	b := evalReportFile{
		GeneratedAt: "2026-01-02T00:00:00Z", Mode: "live", Model: "m1", Scenarios: 2, Passed: 1,
		SuccessRate: 50, AvgRounds: 6, Tokens: 2500, WrongTools: 1, ToolErrors: 7,
		Rows: []evalReportRow{
			{Name: "same-both-pass", Passed: true, Rounds: 8, Tokens: 2000},
			{Name: "regressed", Passed: false, Rounds: 4, Tokens: 500},
		},
	}
	out := compareEvalReports(a, b)
	for _, want := range []string{
		"mode=live model=m1",
		"-1 scenario(s), -50.0 pp",
		"+2.0",  // avg rounds
		"+1500", // tokens
		"+1",    // wrong tool calls
		"regressed", "PASS -> FAIL",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("comparison must contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "note: reports cover different scenario counts") {
		t.Fatalf("equal scenario counts must not warn:\n%s", out)
	}

	// شمارش نابرابر → یادداشت + «only in» برای سناریوهای یک‌طرفه.
	b.Scenarios = 3
	b.Rows = append(b.Rows, evalReportRow{Name: "new-only", Passed: false})
	if out := compareEvalReports(a, b); !strings.Contains(out, "only in B (FAIL)") ||
		!strings.Contains(out, "note: reports cover different scenario counts") {
		t.Fatalf("one-sided scenarios must be reported:\n%s", out)
	}
}

// TestReadEvalReportJSON خواندن گزارش ذخیره‌شده را می‌سنجد (قالب مشترک با
// writeEvalJSON).
func TestReadEvalReportJSON(t *testing.T) {
	dir := t.TempDir()
	path := dir + string(os.PathSeparator) + "report.json"
	rep := evalReportFile{
		GeneratedAt: "2026-01-01T00:00:00Z", Mode: "live", Model: "m1", Scenarios: 1, Passed: 1,
		SuccessRate: 100, AvgRounds: 3, Tokens: 42,
		Rows: []evalReportRow{{Name: "x", Passed: true, Rounds: 3, Tokens: 42}},
	}
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readEvalReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != "live" || got.Model != "m1" || got.Scenarios != 1 || len(got.Rows) != 1 {
		t.Fatalf("report round-trip mismatch: %+v", got)
	}

	badPath := dir + string(os.PathSeparator) + "bad.json"
	if err := os.WriteFile(badPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readEvalReport(badPath); err == nil {
		t.Fatal("an empty report must be rejected")
	}
}
