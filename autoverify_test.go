// تست‌های راستی‌آزمایی خودکار (autoverify.go).
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutoVerifyCommandsDetection(t *testing.T) {
	// Go: با go.mod (و وجود ابزار go) → build و vet
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module demo\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmds := autoVerifyCommands(ws, nil)
	if _, err := exec.LookPath("go"); err == nil {
		if len(cmds) < 2 || cmds[0] != "go build ./..." || cmds[1] != "go vet ./..." {
			t.Fatalf("go project must verify with build+vet, got %v", cmds)
		}
	} else if len(cmds) != 0 {
		t.Fatalf("without the go toolchain nothing should run, got %v", cmds)
	}

	// TypeScript: فقط tsc از PATH؛ هرگز باینری داخل مخزن (node_modules/.bin).
	ts := t.TempDir()
	os.WriteFile(filepath.Join(ts, "tsconfig.json"), []byte("{}"), 0o600)
	// یک «مخزن» با tsc محلی می‌سازیم تا مطمئن شویم نادیده گرفته می‌شود.
	bin := filepath.Join(ts, "node_modules", ".bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	repoTsc := filepath.Join(bin, "tsc")
	if isWindows() {
		repoTsc += ".cmd"
	}
	os.WriteFile(repoTsc, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	if got := autoVerifyCommands(ts, nil); len(got) != 0 {
		if strings.Contains(strings.Join(got, " "), "node_modules") {
			t.Fatalf("repo binary must never be used, got %v", got)
		}
	}
	// با tsc در PATH → --noEmit روی همان tsconfig، بدون مسیر مخزن.
	shim := t.TempDir()
	shimTsc := filepath.Join(shim, "tsc")
	if isWindows() {
		shimTsc += ".cmd"
	}
	os.WriteFile(shimTsc, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	got := autoVerifyCommands(ts, nil)
	if len(got) != 1 || !strings.Contains(got[0], "--noEmit") || !strings.Contains(got[0], "tsconfig.json") {
		t.Fatalf("PATH tsc must be used with --noEmit, got %v", got)
	}
	if strings.Contains(got[0], "node_modules") {
		t.Fatalf("command must not point inside the repo, got %v", got)
	}

	// Python: فقط فایل‌های تغییرکرده
	py := t.TempDir()
	if _, err := exec.LookPath("python"); err == nil {
		got = autoVerifyCommands(py, []string{"a.py", "sub/b.py", "README.md"})
		if len(got) != 1 || !strings.Contains(got[0], "py_compile") || !strings.Contains(got[0], "a.py") {
			t.Fatalf("python files must be compiled explicitly, got %v", got)
		}
		if strings.Contains(got[0], "README.md") {
			t.Fatalf("non-python changes must be ignored: %v", got)
		}
	}

	// بدون نشانه → چیزی نیست، و دلیل فارسی برمی‌گردد
	empty := t.TempDir()
	a := newTestApp(t)
	runs, skipped := a.autoVerifyTurn(context.Background(), empty, nil)
	if len(runs) != 0 || skipped == "" {
		t.Fatalf("empty project must be skipped with a reason, got runs=%v skipped=%q", runs, skipped)
	}
}

// TestAutoVerifyTurnRealGoProject یک اجرای واقعی: پروژهٔ سالم موفق و پروژهٔ
// خراب ناموفق با خروجی خطای قابل‌استفاده.
func TestAutoVerifyTurnRealGoProject(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	a := newTestApp(t)

	good := t.TempDir()
	os.WriteFile(filepath.Join(good, "go.mod"), []byte("module demo\n\ngo 1.21\n"), 0o600)
	os.WriteFile(filepath.Join(good, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600)
	runs, skipped := a.autoVerifyTurn(context.Background(), good, []string{"main.go"})
	if skipped != "" || len(runs) == 0 {
		t.Fatalf("healthy project must run verification, skipped=%q runs=%v", skipped, runs)
	}
	if !runs[0].OK {
		t.Fatalf("healthy project must pass: %+v", runs[0])
	}

	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, "go.mod"), []byte("module demo\n\ngo 1.21\n"), 0o600)
	os.WriteFile(filepath.Join(bad, "main.go"), []byte("package main\n\nfunc main() { this is not go }\n"), 0o600)
	runs, _ = a.autoVerifyTurn(context.Background(), bad, []string{"main.go"})
	if len(runs) == 0 || runs[0].OK {
		t.Fatalf("broken project must fail verification: %+v", runs)
	}
	if runs[0].Output == "" || runs[0].ExitCode == 0 {
		t.Fatalf("failure must carry exit code and output: %+v", runs[0])
	}
	msg := autoVerifyFailedMessage(runs[0])
	if !strings.Contains(msg, "AUTO-VERIFY FAILED") || !strings.Contains(msg, "go build ./...") {
		t.Fatalf("model message must name the failure: %s", msg)
	}
	t.Logf("auto-verify on broken Go project: %dms, exit=%d, output=%dB", runs[0].Ms, runs[0].ExitCode, len(runs[0].Output))
}

func TestAutoVerifyGating(t *testing.T) {
	rec := &turnRecorder{}
	if shouldAutoVerify(true, false, false, 0, 25, rec, true) {
		t.Fatal("no file change yet → no verification")
	}
	rec.recordFileChange("main.go")
	if !shouldAutoVerify(true, false, false, 0, 25, rec, true) {
		t.Fatal("changed file + no verification → must verify")
	}
	rec.recordCommand("go build ./...", 0, true)
	if shouldAutoVerify(true, false, false, 1, 25, rec, true) {
		t.Fatal("successful verification already recorded → skip")
	}
	rec2 := &turnRecorder{}
	rec2.recordFileChange("a.go")
	for _, c := range []struct {
		name                           string
		use, dropped, already, enabled bool
		round, max                     int
	}{
		{"disabled", true, false, false, false, 0, 25},
		{"chat mode", false, false, false, true, 0, 25},
		{"tools dropped", true, true, false, true, 0, 25},
		{"already ran", true, false, true, true, 0, 25},
		{"last round", true, false, false, true, 24, 25},
	} {
		if shouldAutoVerify(c.use, c.dropped, c.already, c.round, c.max, rec2, c.enabled) {
			t.Fatalf("gating %q must block verification", c.name)
		}
	}
}

func TestAutoVerifyNoticeTexts(t *testing.T) {
	okText, okFlag := autoVerifyNotice([]autoVerifyRun{{Command: "go build ./...", OK: true}}, "")
	if !okFlag || !strings.Contains(okText, "موفق") {
		t.Fatalf("success notice wrong: %q", okText)
	}
	failText, failFlag := autoVerifyNotice([]autoVerifyRun{{Command: "go vet ./...", OK: false, ExitCode: 1}}, "")
	if failFlag || !strings.Contains(failText, "شکست") {
		t.Fatalf("failure notice wrong: %q", failText)
	}
}

// TestAutoVerifyEndToEnd — سناریوی واقعی ایجنت: مدل فایل می‌نویسد،
// راستی‌آزمایی خودکار همان دور خطا را می‌گیرد و خطا به دور بعد داده می‌شود؛
// یعنی نوبت به «دور سوم» یا اصرار کاربر نیاز ندارد.
func TestAutoVerifyEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	sawVerify := false
	env := newE2E(t,
		stepTool("c1", "write_file", `{"path":"main.go","content":"package main\n\nfunc main() { this is broken }\n"}`),
		scenarioStep{
			Text: "خطا را بررسی می‌کنم.",
			Check: func(t *testing.T, req chatRequest) {
				for _, m := range req.Messages {
					c := historyContentString(m.Content)
					if strings.Contains(c, "AUTO-VERIFY FAILED") && strings.Contains(c, "go build ./...") {
						sawVerify = true
					}
				}
			},
		},
	)
	env.writeFile("go.mod", "module demo\n\ngo 1.21\n")
	conv := env.send("یک فایل تک‌خطی بساز")

	if !sawVerify {
		t.Fatalf("next model round must receive the AUTO-VERIFY failure; requests=%d", env.model.count())
	}
	// خودِ راستی‌آزمایی مدل را صدا نمی‌زند: فقط نتیجه‌اش به دور بعد می‌رود.
	// (اگر شکست باقی بماند، تذکر A.2 یک دور دیگر هم اضافه می‌کند.)
	if n := env.model.count(); n > 3 {
		t.Fatalf("verification must not add extra model rounds, got %d calls", n)
	}
	sums := env.summaries()
	if len(sums) == 0 {
		t.Fatal("turn summary expected")
	}
	last := sums[len(sums)-1]
	if last.Verification.OK {
		t.Fatalf("failed verification must not be reported OK: %+v", last.Verification)
	}
	sawCmd := false
	for _, c := range last.Commands {
		if strings.HasPrefix(c.Command, "go build") {
			sawCmd = true
			if c.ExitOK {
				t.Fatalf("failed build must record a non-zero exit: %+v", c)
			}
		}
	}
	if !sawCmd {
		t.Fatalf("summary must list the auto-verify command, got %+v", last.Commands)
	}
	if conv == nil {
		t.Fatal("conversation must be returned")
	}
}

// TestAutoVerifyPythonCommandPassesGate رگرسیونِ لاگ واقعی: فرمان
// تولیدشده برای پروژهٔ پایتون باید از دروازهٔ غیرتعاملی رد شود، وگرنه
// راستی‌آزمایی خودکار همیشه با «نیاز به تأیید» رد می‌شد.
func TestAutoVerifyPythonCommandPassesGate(t *testing.T) {
	if _, err := exec.LookPath("python"); err != nil {
		t.Skip("python not available")
	}
	a := newTestApp(t)
	a.mu.Lock()
	a.settings.AllowCommands = true
	a.mu.Unlock()
	ws := t.TempDir()
	cmds := autoVerifyCommands(ws, []string{"a.py", "sub/b.py"})
	if len(cmds) == 0 {
		t.Skip("no python verification command detected")
	}
	if ok, why := a.autoVerifyGate(ws, cmds[0]); !ok {
		t.Fatalf("python verification command %q must pass the gate, got %q", cmds[0], why)
	}
}

// TestAutoVerifyJSDetection بررسی نحویِ فایل‌های JS تغییرکرده — بازی/UI
// نیمه‌کاره باید همان دور لو برود، نه در نوبت بعد.
func TestAutoVerifyJSDetection(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	ws := t.TempDir()
	found := false
	for _, c := range autoVerifyCommands(ws, []string{"game.js", "README.md"}) {
		if strings.Contains(c, "node --check") && strings.Contains(c, "game.js") {
			found = true
		}
		if strings.Contains(c, "README.md") {
			t.Fatalf("non-JS change must be ignored: %s", c)
		}
	}
	if !found {
		t.Fatalf("changed .js must be syntax-checked")
	}
	for _, c := range autoVerifyCommands(ws, []string{"node_modules/x/y.js", "a.cjs"}) {
		if strings.Contains(c, "node_modules") {
			t.Fatalf("node_modules must be ignored: %s", c)
		}
	}

	a := newTestApp(t)
	if ok, why := a.autoVerifyGate(ws, `node --check "game.js"`); !ok {
		t.Fatalf("node --check must pass the gate, got %q", why)
	}
	// فایل نیمه‌کاره (پرانتز باز) باید شکست بخورد و خروجی‌اش به مدل برسد.
	if err := os.WriteFile(filepath.Join(ws, "broken.js"), []byte("function f(\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runs, skipped := a.autoVerifyTurn(context.Background(), ws, []string{"broken.js"})
	if skipped != "" || len(runs) == 0 || runs[0].OK {
		t.Fatalf("truncated js must fail verification: skipped=%q runs=%+v", skipped, runs)
	}
	if !strings.Contains(autoVerifyFailedMessage(runs[0]), "node --check") {
		t.Fatalf("failure message must name the command")
	}
}

func autoVerifyHasCmd(cmds []string, want string) bool {
	for _, c := range cmds {
		if c == want {
			return true
		}
	}
	return false
}

// TestAutoVerifyRunsProjectTests: کدی که کامپایل می‌شود ولی تست را می‌شکند
// نباید «سبز» تلقی شود — دستور تست همان اکوسیستم هم اجرا می‌شود.
func TestAutoVerifyRunsProjectTests(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	goWS := t.TempDir()
	if err := os.WriteFile(filepath.Join(goWS, "go.mod"), []byte("module demo\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmds := autoVerifyCommands(goWS, nil)
	if len(cmds) != 3 || cmds[0] != "go build ./..." || cmds[1] != "go vet ./..." || cmds[2] != "go test ./..." {
		t.Fatalf("Go project must build, vet, then test; got %v", cmds)
	}

	nodeWS := t.TempDir()
	os.WriteFile(filepath.Join(nodeWS, "package.json"), []byte(`{"scripts":{"test":"node -e 0","build":"node -e 0"}}`), 0o600)
	if _, err := exec.LookPath("npm"); err == nil {
		if got := autoVerifyCommands(nodeWS, nil); !autoVerifyHasCmd(got, "npm test") {
			t.Fatalf("package.json with a test script must run npm test, got %v", got)
		}
	}
	noTest := t.TempDir()
	os.WriteFile(filepath.Join(noTest, "package.json"), []byte(`{"scripts":{"build":"node -e 0"}}`), 0o600)
	if got := autoVerifyCommands(noTest, nil); autoVerifyHasCmd(got, "npm test") {
		t.Fatalf("missing test script must not run npm test, got %v", got)
	}
}

// TestAutoVerifyOutputKeepsTail: خطای واقعی معمولاً انتهای لاگ است؛ بریدن فقط
// از سر باعث می‌شد مدل همان دستور را تکرار کند.
func TestAutoVerifyOutputKeepsTail(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 1500; i++ {
		b.WriteString("noise line that fills the log output\n")
	}
	b.WriteString("FAIL: the real error is at the end\n")
	out := autoVerifyOutput(b.String())
	if len(out) > autoVerifyMaxOutput {
		t.Fatalf("output must be bounded, got %d bytes", len(out))
	}
	if !strings.Contains(out, "the real error is at the end") {
		t.Fatalf("tail of the log (the actual failure) must survive truncation: %q", out)
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("truncation must be marked explicitly")
	}
}

// TestAutoVerifyFailedTestExtraction: نام تست‌های شکست‌خورده جدا می‌شود تا مدل
// مستقیم همان‌ها را هدف بگیرد.
func TestAutoVerifyFailedTestExtraction(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []string
	}{
		{"go", "--- FAIL: TestAlpha (0.00s)\n    x_test.go:9: boom\nFAIL\nFAIL\tdemo\t0.3s\nFAIL", []string{"TestAlpha"}},
		{"pytest", "FAILED tests/test_api.py::test_login - assert 1 == 2\n2 failed", []string{"tests/test_api.py::test_login"}},
		{"jest", "  ✕ renders the header (12 ms)\n  × loads data", []string{"renders the header", "loads data"}},
		{"tap", "not ok 3 - parses input\n", []string{"parses input"}},
		{"none", "ok\tdemo\t0.1s\n", nil},
	}
	for _, c := range cases {
		got := autoVerifyFailedTests(c.out)
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %v want %v", c.name, got, c.want)
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Fatalf("%s: got %v want %v", c.name, got, c.want)
			}
		}
	}
	dup := autoVerifyFailedTests("--- FAIL: TestA (0.00s)\n--- FAIL: TestA (0.00s)")
	if len(dup) != 1 {
		t.Fatalf("failing test names must be deduped, got %v", dup)
	}
}

func TestAutoVerifyMessageListsFailingTest(t *testing.T) {
	msg := autoVerifyFailedMessage(autoVerifyRun{
		Command:     "go test ./...",
		ExitCode:    1,
		Output:      "--- FAIL: TestAlpha (0.00s)\nFAIL\n",
		FailedTests: []string{"TestAlpha"},
	})
	if !strings.Contains(msg, "TestAlpha") || !strings.Contains(msg, "Failing tests") {
		t.Fatalf("message must name the failing test: %s", msg)
	}
	// اگر نام‌ها از خروجی بریده شده باشند، از خود Output استخراج می‌شود.
	msg = autoVerifyFailedMessage(autoVerifyRun{
		Command:  "pytest",
		ExitCode: 1,
		Output:   "FAILED tests/test_api.py::test_login - assert 1 == 2\n",
	})
	if !strings.Contains(msg, "tests/test_api.py::test_login") {
		t.Fatalf("message must recover failing tests from output: %s", msg)
	}
}

// TestAutoVerifyTurnRunsAndReportsFailingTest — اجرای واقعی: build موفق، تست
// شکست‌خورده؛ راستی‌آزمایی باید شکست را با نام تست گزارش کند.
func TestAutoVerifyTurnRunsAndReportsFailingTest(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	a := newTestApp(t)
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module demo\n\ngo 1.21\n"), 0o600)
	os.WriteFile(filepath.Join(ws, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600)
	os.WriteFile(filepath.Join(ws, "main_test.go"), []byte("package main\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) {\n\tt.Fatal(\"nope\")\n}\n"), 0o600)

	runs, skipped := a.autoVerifyTurn(context.Background(), ws, []string{"main_test.go"})
	if skipped != "" {
		t.Fatalf("verification must run, skipped=%q", skipped)
	}
	if len(runs) != 3 {
		t.Fatalf("build, vet, then test must run; got %+v", runs)
	}
	last := runs[len(runs)-1]
	if last.Command != "go test ./..." || last.OK || last.ExitCode == 0 {
		t.Fatalf("failing test must fail verification: %+v", last)
	}
	found := false
	for _, n := range last.FailedTests {
		if n == "TestBroken" {
			found = true
		}
	}
	if !found {
		t.Fatalf("failing test name must be extracted, got %v (output: %s)", last.FailedTests, last.Output)
	}
	if msg := autoVerifyFailedMessage(last); !strings.Contains(msg, "TestBroken") {
		t.Fatalf("model message must name TestBroken: %s", msg)
	}
}

// TestAutoVerifyRecursiveModules: monorepoیی که go.mod در ریشه ندارد نباید
// «راستی‌آزمایی پیدا نشد» بدهد — ماژول‌ها در زیرپوشه پیدا و در همان‌جا اجرا می‌شوند.
func TestAutoVerifyRecursiveModules(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	ws := t.TempDir()
	for _, sub := range []string{"svc/api", "svc/worker"} {
		dir := filepath.Join(ws, filepath.FromSlash(sub))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+sub+"\n\ngo 1.21\n"), 0o600)
	}
	got := autoVerifyCommands(ws, nil)
	joined := strings.Join(got, "\n")
	for _, want := range []string{"cd svc/api && go build ./...", "cd svc/api && go test ./...", "cd svc/worker && go build ./..."} {
		if !strings.Contains(joined, want) {
			t.Fatalf("recursive module detection missing %q; got %v", want, got)
		}
	}
}

// TestAutoVerifyExtraEcosystems: PHP/Python/Rust/.NET هم راستی‌آزمایی نحوی/
// ساخت دارند (قبلاً فقط Go/TS/JS/Python بود).
func TestAutoVerifyExtraEcosystems(t *testing.T) {
	if _, err := exec.LookPath("php"); err == nil {
		got := autoVerifyCommands(t.TempDir(), []string{"a.php", "sub/b.php", "README.md"})
		joined := strings.Join(got, "\n")
		if !strings.Contains(joined, `php -l "a.php"`) || strings.Contains(joined, "README.md") {
			t.Fatalf("php syntax check missing/misapplied: %v", got)
		}
	}
	if _, err := exec.LookPath("cargo"); err == nil {
		ws := t.TempDir()
		os.WriteFile(filepath.Join(ws, "Cargo.toml"), []byte("[package]\nname=\"x\"\n"), 0o600)
		joined := strings.Join(autoVerifyCommands(ws, nil), "\n")
		if !strings.Contains(joined, "cargo build") || !strings.Contains(joined, "cargo test") {
			t.Fatalf("cargo verification missing: %s", joined)
		}
	}
	if _, err := exec.LookPath("dotnet"); err == nil {
		ws := t.TempDir()
		os.WriteFile(filepath.Join(ws, "app.csproj"), []byte("<Project/>"), 0o600)
		joined := strings.Join(autoVerifyCommands(ws, nil), "\n")
		if !strings.Contains(joined, "dotnet build") {
			t.Fatalf("dotnet verification missing: %s", joined)
		}
	}
}

// TestAutoVerifyUnsafeDirNamesNotUsed: نام پوشه‌ی دارای فاصله/metachar نباید
// به دستور cd بدون quoting تبدیل شود.
func TestAutoVerifyUnsafeDirNamesNotUsed(t *testing.T) {
	ws := t.TempDir()
	bad := filepath.Join(ws, "a b & c")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(bad, "go.mod"), []byte("module x\n"), 0o600)
	for _, c := range autoVerifyCommands(ws, nil) {
		if strings.Contains(c, "a b") {
			t.Fatalf("unsafe directory name must never reach a shell command: %q", c)
		}
	}
}

// TestAutoVerifyWatchScriptsAreNotRun: اسکریپت تست watch-mode هرگز خودکار
// اجرا نمی‌شود، وگرنه کل نوبت تا پایان مهلت معطل می‌ماند.
func TestAutoVerifyWatchScriptsAreNotRun(t *testing.T) {
	cases := []struct {
		script string
		watch  bool
	}{
		{"react-scripts test", false}, // حالت watch به TTY مشروط است؛ خروجی pipe است
		{"vitest run", false},
		{"vitest", false},
		{"jest", false},
		{"node --test", false},
		{"jest --watch", true},
		{"vitest --watch", true},
		{"jest --watchAll", true},
		{"jest --watchAll=false", false},
		{"vitest --watch=false", false},
		{"ng test", true},
		{"ng test --watch=false", false},
		{"karma start", true},
		{"karma start --single-run", false},
	}
	for _, c := range cases {
		if got := autoVerifyWatchScript(c.script); got != c.watch {
			t.Fatalf("watch detection for %q = %v, want %v", c.script, got, c.watch)
		}
	}

	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "package.json"), []byte(`{"scripts":{"test":"jest --watch"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := autoVerifyCommands(ws, nil); autoVerifyHasCmd(got, "npm test") {
		t.Fatalf("watch-mode test script must never be scheduled, got %v", got)
	}
	if autoVerifyWatchSkipReason(ws) == "" {
		t.Fatal("watch-mode test script must produce a skip reason for the activity log")
	}

	okWS := t.TempDir()
	os.WriteFile(filepath.Join(okWS, "package.json"), []byte(`{"scripts":{"test":"node --test"}}`), 0o600)
	if autoVerifyWatchSkipReason(okWS) != "" {
		t.Fatal("one-shot test script must not be reported as watch-mode")
	}
}

// TestAutoVerifyTestCommandTimeoutLonger: تست‌ها بیش از build طول می‌کشند و
// مهلت ۱۵۰ ثانیه‌ای build برایشان کم است.
func TestAutoVerifyTestCommandTimeoutLonger(t *testing.T) {
	if autoVerifyTimeoutFor("go build ./...") != autoVerifyTimeout {
		t.Fatal("build keeps the build timeout")
	}
	if autoVerifyTimeoutFor("go test ./...") <= autoVerifyTimeout {
		t.Fatal("test commands need a longer timeout than build")
	}
}
