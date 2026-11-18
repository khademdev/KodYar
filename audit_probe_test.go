// آزمایش‌های عملیِ پیش از انتشار: هر تست یک سناریوی واقعیِ گفتگو را روی
// همین ماشین اجرا می‌کند و انتظارِ درست را می‌سنجد. تستِ شکست‌خورده یعنی
// باگِ واقعی در مسیر اجرا، نه در خودِ تست.
//
// دستورها از دروازهٔ تأیید (مودال) عبور نمی‌دهند چون در محیط تست رابط
// کاربری وجود ندارد؛ همان لایهٔ اجرا/خروجی/بریدنِ خروجی که run_command
// استفاده می‌کند مستقیم به کار گرفته می‌شود.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// pyBin نام مفسر پایتونِ موجود روی این ماشین ("" یعنی هیچ).
func pyBin() string {
	for _, n := range []string{"python", "python3"} {
		if _, err := exec.LookPath(n); err == nil {
			return n
		}
	}
	return ""
}

// probeCall یک فراخوانی ابزار با آرگومان‌های JSON می‌سازد.
func probeCall(id, name string, args map[string]string) apiToolCall {
	raw, _ := json.Marshal(args)
	var c apiToolCall
	c.ID = id
	c.Type = "function"
	c.Function.Name = name
	c.Function.Arguments = string(raw)
	return c
}

// probeExec همان مسیر toolRunCommand برای اجرا، بریدن خروجی و ساختن پیام
// شکست را طی می‌کند (بدون دروازهٔ تأیید تعاملی).
func probeExec(t *testing.T, ws, cmd string, timeoutSec int) (string, bool) {
	t.Helper()
	d := time.Duration(timeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	out, _, err := runShellWithPolicy(ctx, ws, cmd, d, nil, nil)
	result := truncateToolOutputKeepTail(strings.TrimRight(out, "\n"))
	if err != nil {
		return commandFailureResult(result, timeoutSec, ctx.Err() == context.DeadlineExceeded, err), false
	}
	if result == "" {
		result = "(command finished successfully, no output)"
	}
	return result, true
}

// probeWriteBatch دو نوشتن را در یک دسته اجرا می‌کند و می‌گوید هم‌زمان شدند یا نه.
func probeWriteBatch(t *testing.T, a *App, calls []apiToolCall) (overlap bool, order []string) {
	t.Helper()
	var mu sync.Mutex
	active := 0
	exec := func(ctx context.Context, name, argsJSON string) (string, bool) {
		var args map[string]string
		_ = json.Unmarshal([]byte(argsJSON), &args)
		mu.Lock()
		active++
		if active > 1 {
			overlap = true
		}
		order = append(order, args["path"])
		mu.Unlock()
		time.Sleep(120 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		return "ok", true
	}
	tasks, _, _ := planToolCalls(calls, &toolLoopGuard{})
	a.executeToolCalls(context.Background(), "probe", tasks, false, exec)
	return overlap, order
}

// P1 — دو ویرایش روی یک فایل در یک دسته باید ترتیبی بمانند، حتی وقتی مدل
// یک بار مسیر نسبی و یک بار مسیر مطلق می‌دهد (رفتار واقعی مدل).
func TestProbeParallelWriteSameFileAbsoluteVsRelative(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	abs := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(abs, []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := []apiToolCall{
		probeCall("1", "write_file", map[string]string{"path": "a.txt", "content": "x"}),
		probeCall("2", "write_file", map[string]string{"path": abs, "content": "y"}),
	}
	overlap, order := probeWriteBatch(t, a, calls)
	t.Logf("paths=%v overlap=%v", order, overlap)
	if overlap {
		t.Fatalf("same file written concurrently via relative+absolute path — one edit can be lost")
	}
}

// P2 — دو ویرایش روی یک فایل با مسیر یکسان (حالت رایج) باید ترتیبی بماند.
func TestProbeParallelWriteSameFileSamePath(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := []apiToolCall{
		probeCall("1", "edit_file", map[string]string{"path": "a.txt", "old": "base", "new": "x"}),
		probeCall("2", "edit_file", map[string]string{"path": "a.txt", "old": "x", "new": "y"}),
	}
	overlap, order := probeWriteBatch(t, a, calls)
	t.Logf("paths=%v overlap=%v", order, overlap)
	if overlap {
		t.Fatalf("same path edited concurrently — order of edits is not guaranteed")
	}
}

// P3 — دو فایل مستقل باید هم‌زمان اجرا شوند (رگرسیون سرعت).
func TestProbeParallelWriteIndependentFiles(t *testing.T) {
	a := newTestApp(t)
	calls := []apiToolCall{
		probeCall("1", "write_file", map[string]string{"path": "a.txt", "content": "x"}),
		probeCall("2", "write_file", map[string]string{"path": "b.txt", "content": "y"}),
	}
	start := time.Now()
	overlap, _ := probeWriteBatch(t, a, calls)
	t.Logf("overlap=%v elapsed=%dms", overlap, time.Since(start).Milliseconds())
	if !overlap {
		t.Fatalf("independent files must run in parallel")
	}
}

// P4 — خروجی فارسیِ یک دستور باید سالم برگردد (چاپ Python روی ویندوز).
func TestProbePersianCommandOutput(t *testing.T) {
	py := pyBin()
	if py == "" {
		t.Skip("python not available")
	}
	a := newTestApp(t)
	out, ok := probeExec(t, a.settings.WorkspacePath, py+` -c "print('سلام دنیا — تست')"`, 30)
	t.Logf("ok=%v out=%q", ok, out)
	if !ok {
		t.Skipf("python unavailable: %s", out)
	}
	if !strings.Contains(out, "سلام دنیا") {
		t.Fatalf("Persian output came back unreadable: %q", out)
	}
}

// P5 — کد خروجِ کرشِ native باید قابل‌فهم گزارش شود (0xC0000005)، نه عدد
// دهدهیِ گیج‌کنندهٔ 3221225477. کرش واقعی با TerminateProcess ساخته می‌شود
// (همان کدی که یک برنامهٔ native در access violation برمی‌گرداند).
func TestProbeNativeCrashExitCode(t *testing.T) {
	if !isWindows() {
		t.Skip("NTSTATUS exit codes are a Windows concept")
	}
	py := pyBin()
	if py == "" {
		t.Skip("python not available")
	}
	a := newTestApp(t)
	// 0xC0000005 به‌صورت int32 علامت‌دار = -1073741819
	cmd := py + ` -c "import os; os._exit(-1073741819)"`
	out, ok := probeExec(t, a.settings.WorkspacePath, cmd, 30)
	t.Logf("ok=%v out=%q", ok, out)
	if ok {
		t.Skipf("crash did not reproduce: %s", out)
	}
	if !strings.Contains(out, "0xC0000005") || !strings.Contains(out, "access violation") {
		t.Fatalf("crash code must be shown in readable form: %q", out)
	}
}

// P5b — کدهای خروج معمولی نباید تغییر کنند.
func TestProbeNormalExitCodeUnchanged(t *testing.T) {
	if got := annotateExitStatus(fmt.Errorf("exit status 2"), 2); got == nil || got.Error() != "exit status 2" {
		t.Fatalf("normal exit code must stay as-is, got %v", got)
	}
	if got := annotateExitStatus(fmt.Errorf("exit status 3221225477"), 3221225477); got == nil || !strings.Contains(got.Error(), "0xC0000005") {
		t.Fatalf("NTSTATUS must be annotated, got %v", got)
	}
	// خطای واقعیِ اجرا (نه کد خروج) نباید بازنویسی شود.
	if got := annotateExitStatus(fmt.Errorf("sandbox failed"), 3221225477); got == nil || got.Error() != "sandbox failed" {
		t.Fatalf("real errors must be untouched, got %v", got)
	}
}

// P6 — خروجی طولانی باید انتهایش (جای خطای واقعی) را نگه دارد.
func TestProbeLongOutputKeepsTail(t *testing.T) {
	py := pyBin()
	if py == "" {
		t.Skip("python not available")
	}
	a := newTestApp(t)
	cmd := py + ` -c "` +
		`[print('line-%05d-'%i + 'x'*40) for i in range(6000)]; print('FINAL_ERROR_MARKER: build failed here')"`
	out, _ := probeExec(t, a.settings.WorkspacePath, cmd, 60)
	t.Logf("len=%d hasMarker=%v", len(out), strings.Contains(out, "FINAL_ERROR_MARKER"))
	if !strings.Contains(out, "FINAL_ERROR_MARKER") {
		t.Fatalf("tail of long output was dropped — the model would not see the real error (len=%d)", len(out))
	}
}

// P7 — timeout هم پیام درست بدهد و هم فرایند را واقعاً بکشد.
func TestProbeTimeoutKillsProcess(t *testing.T) {
	py := pyBin()
	if py == "" {
		t.Skip("python not available")
	}
	a := newTestApp(t)
	start := time.Now()
	out, ok := probeExec(t, a.settings.WorkspacePath,
		py+` -c "import time; print('started', flush=True); time.sleep(120)"`, 2)
	elapsed := time.Since(start)
	t.Logf("ok=%v elapsed=%dms out=%q", ok, elapsed.Milliseconds(), out)
	if ok {
		t.Fatalf("timed-out command must fail")
	}
	if elapsed > 20*time.Second {
		t.Fatalf("timeout took too long: %dms", elapsed.Milliseconds())
	}
	if !strings.Contains(out, "timed out") {
		t.Fatalf("timeout message missing: %q", out)
	}
	if !strings.Contains(out, "started") {
		t.Fatalf("partial output before timeout was lost: %q", out)
	}
	if strings.HasPrefix(out, "\n") {
		t.Fatalf("failure message must not start with a blank line: %q", out)
	}
}

// P8 — دستور موفقِ بی‌خروجی باید پیام روشن بدهد، نه رشتهٔ خالی.
func TestProbeSilentSuccessMessage(t *testing.T) {
	py := pyBin()
	if py == "" {
		t.Skip("python not available")
	}
	a := newTestApp(t)
	out, ok := probeExec(t, a.settings.WorkspacePath, py+` -c "pass"`, 30)
	t.Logf("ok=%v out=%q", ok, out)
	if !ok || out == "" {
		t.Fatalf("silent success must produce a clear message: ok=%v out=%q", ok, out)
	}
}

// P9 — گارد حلقه/تکرار باید پیام روشن بدهد (رفتار doom-loop).
func TestProbeRepeatedCommandGuardMessage(t *testing.T) {
	a := newTestApp(t)
	guard := &toolLoopGuard{}
	bad := probeCall("1", "read_file", map[string]string{"path": "missing.txt"})
	var last string
	for i := 0; i < toolRepeatStopRun+1; i++ {
		tasks, _, _ := planToolCalls([]apiToolCall{bad}, guard)
		out := a.executeToolCalls(context.Background(), "probe", tasks, false,
			func(ctx context.Context, name, args string) (string, bool) {
				return "ERROR: no such file", false
			})
		last = out[0].result
	}
	t.Logf("last=%q", last)
	if last == "" {
		t.Fatal("guard produced no message")
	}
}

// P10 — ماتریس نقل‌قول: دستورهایی که داخلشان کوتیشن دوتایی دارند باید
// دقیقاً همان‌طور که مدل نوشته اجرا شوند. این حالت روزمره است (python -c،
// git commit -m "…"، echo "…").
func TestProbeInnerQuotesSurvive(t *testing.T) {
	if !isWindows() {
		t.Skip("cmd.exe quote handling is Windows-specific (sh keeps quotes correctly)")
	}
	py := pyBin()
	if py == "" {
		t.Skip("python not available")
	}
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	cases := []struct {
		cmd  string
		want string
	}{
		{py + ` -c "print(42)"`, "42"},
		{py + ` -c "print('a b')"`, "a b"},
		{py + ` -c "import os; print('ok')"`, "ok"},
		{`echo "hello world"`, "hello world"},
		{py + ` -c "print('x')" && echo done`, "done"},
		{`git 2>&1 | findstr /C:"usage"`, "usage"},
	}
	for _, c := range cases {
		out, ok := probeExec(t, ws, c.cmd, 30)
		t.Logf("cmd=%q ok=%v out=%q", c.cmd, ok, out)
		if !strings.Contains(out, c.want) {
			t.Errorf("quoting broke %q: got %q (want %q)", c.cmd, out, c.want)
		}
	}
}

// P11 — پیام کامیت گیت با کوتیشن و متن فارسی باید بی‌کم‌وکاست ثبت شود.
// (باگ کوتیشن قبلی پیام کامیت را می‌شکست؛ این سناریو مستقیم به تاریخچهٔ
// گیت‌هاب می‌رسد.)
func TestProbeGitCommitMessageIntact(t *testing.T) {
	ws := t.TempDir()
	run := func(cmd string) string {
		out, _ := probeExec(t, ws, cmd, 60)
		return out
	}
	run(`git init -q`)
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(`git add a.txt`)
	// کوتیشن دوتایی داخل آرگومان کوتیشن‌دار را خود cmd حذف می‌کند (محدودیت
	// cmd.exe)؛ پرامپت ایجنت مدل را به کوتیشن تک راهنمایی می‌کند.
	msg := `fix: 'quoted' — پیام فارسی: نسخهٔ ۱.۰.۵`
	run(`git -c user.name=probe -c user.email=probe@example.com commit -q -m "` + msg + `"`)
	out := strings.TrimSpace(run(`git log -1 --format=%s`))
	t.Logf("commit subject=%q", out)
	if out != msg {
		t.Fatalf("commit message mangled: got %q want %q", out, msg)
	}
}

// P12 — مسیر فارسی با فاصله باید در نوشتن و خواندن کار کند.
func TestProbePersianPathCommand(t *testing.T) {
	py := pyBin()
	if py == "" {
		t.Skip("python not available")
	}
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	dir := "پروژه من"
	if err := os.MkdirAll(filepath.Join(ws, dir), 0o755); err != nil {
		t.Fatal(err)
	}
	rel := dir + "/فایل.txt"
	out, ok := probeExec(t, ws, py+` -c "open('`+rel+`','w',encoding='utf-8').write('سلام')"`, 30)
	if !ok {
		t.Fatalf("write to persian path failed: %s", out)
	}
	if _, err := os.Stat(filepath.Join(ws, dir, "فایل.txt")); err != nil {
		t.Fatalf("the file was not created where expected: %v", err)
	}
	out, ok = probeExec(t, ws, py+` -c "print(open('`+rel+`',encoding='utf-8').read())"`, 30)
	if !ok || !strings.Contains(out, "سلام") {
		t.Fatalf("read back persian path failed: %q", out)
	}
	// مسیر ویندوزی با `type` هم باید کار کند (فقط ویندوز).
	if isWindows() {
		out, ok = probeExec(t, ws, `type "`+dir+`\فایل.txt"`, 30)
		t.Logf("type out=%q ok=%v", out, ok)
		if !ok || !strings.Contains(out, "سلام") {
			t.Fatalf("type on persian path failed: %q", out)
		}
	}
}

// P13 — خروجی غیر-ASCIIِ برنامه‌های فرزند نباید به کدپیج ANSI سیستم وابسته
// باشد؛ روی ویندوز با cp1252 خروجی فارسی با UnicodeEncodeError می‌مرد.
func TestProbeUTF8ChildEnvForced(t *testing.T) {
	t.Setenv("PYTHONIOENCODING", "")
	ensureUTF8ChildEnv()
	if got := os.Getenv("PYTHONIOENCODING"); got != "utf-8" {
		t.Fatalf("child output encoding must be forced to UTF-8, got %q", got)
	}
	// انتخاب صریح کاربر نباید بازنویسی شود.
	t.Setenv("PYTHONIOENCODING", "cp1251")
	ensureUTF8ChildEnv()
	if got := os.Getenv("PYTHONIOENCODING"); got != "cp1251" {
		t.Fatalf("user-provided encoding must be kept, got %q", got)
	}
}
