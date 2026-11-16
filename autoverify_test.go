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
