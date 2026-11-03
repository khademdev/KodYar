// تست‌های صحت پلتفرمی (مرحلهٔ پلتفرم) — پرامپت سیستمی وابسته به
// سیستم‌عامل/shell و پیوند RunCommand به context عمر برنامه.
package main

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSystemPromptWindows(t *testing.T) {
	p := agentSystemPromptForOS("C:\\Projects\\App", "windows")
	for _, want := range []string{
		"native Windows desktop app",
		"Windows CMD (cmd.exe)",
		"`dir`", "`type`", // hint‌های ویندوزی
		"`\\` as separator", // جداکننده‌ی مسیر (در source دو تا \\ دارد)
		"%VAR%",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("windows prompt must contain %q:\n%.900s", want, p)
		}
	}
	// هیچ اشاره‌ای به POSIX نباید باشد
	if strings.Contains(p, "`ls`") || strings.Contains(p, "`cat`") || strings.Contains(p, "$VAR") {
		t.Error("windows prompt must not contain POSIX shell hints")
	}
}

func TestSystemPromptLinux(t *testing.T) {
	p := agentSystemPromptForOS("/home/user/app", "linux")
	for _, want := range []string{
		"native Linux desktop app",
		"`sh -c`",
		"`ls`", "`cat`", // hint‌های POSIX
		"`/`", "$VAR",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("linux prompt must contain %q:\n%.900s", want, p)
		}
	}
	if strings.Contains(p, "Windows CMD") || strings.Contains(p, "%VAR%") || strings.Contains(p, "`type`") {
		t.Error("linux prompt must not contain Windows CMD hints")
	}
}

func TestSystemPromptDarwin(t *testing.T) {
	p := agentSystemPromptForOS("/Users/x/app", "darwin")
	for _, want := range []string{
		"native macOS desktop app",
		"`sh -c`",
		"BSD", // تفاوت sed -i در مک ذکر می‌شود
	} {
		if !strings.Contains(p, want) {
			t.Errorf("darwin prompt must contain %q:\n%.900s", want, p)
		}
	}
}

func TestSystemPromptProductionUsesRuntimeGOOS(t *testing.T) {
	// تابع production باید از runtime.GOOS پیروی کند — روی این ماشین لینوکس
	// (سندباکس) باید برچسب لینوکس بیاید؛ روی ویندوزِ توسعه‌دهنده برچسب ویندوز.
	p := agentSystemPrompt("/ws")
	plat := shellInfoForOS(runtime.GOOS)
	if !strings.Contains(p, "native "+plat.osLabel+" desktop app") {
		t.Fatalf("production prompt must use runtime.GOOS=%s label (%q)", runtime.GOOS, plat.osLabel)
	}
	// جمله‌ی rule 3 از plat.shellUse ساخته می‌شود — باید نام shell واقعی در آن باشد
	if !strings.Contains(p, plat.shellUse) {
		t.Fatalf("production prompt must embed the real shell sentence %q", plat.shellUse)
	}
}

// shellInfoForOS باید با runShell هم‌قدم بماند: ویندوز = cmd، بقیه = sh -c.
func TestShellInfoMatchesRunShell(t *testing.T) {
	if si := shellInfoForOS("windows"); !strings.Contains(si.shell, "cmd.exe") {
		t.Fatalf("windows shell info = %q, want cmd.exe", si.shell)
	}
	for _, goos := range []string{"linux", "darwin", "freebsd"} {
		if si := shellInfoForOS(goos); !strings.Contains(si.shell, "sh -c") {
			t.Fatalf("%s shell info = %q, want sh -c", goos, si.shell)
		}
	}
}

// TestRunCommandAppContextCancelAbortsPendingAsk — بستن برنامه (لغو context
// عمر) باید تأیید در انتظار را قطع کند؛ قبلاً context.Background بود و
// مودال تا مهلت کامل معلق می‌ماند.
func TestRunCommandAppContextCancelAbortsPendingAsk(t *testing.T) {
	a := newTestApp(t)
	a.settings.AllowCommands = true

	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.ctx = lifetime // شبیه startup وایلز

	type res struct {
		out string
		err error
	}
	done := make(chan res, 1)
	go func() {
		out, err := a.RunCommand("git reset --hard") // الگوی پرریسک → مودال ask
		done <- res{out, err}
	}()

	// تا ثبت ask صبر کن (بدون UI، همان‌جا منتظر پاسخ می‌ماند)
	askID := waitPendingAskID(t, a, "")
	if askID == "" {
		t.Fatal("risky command must produce a pending permission ask")
	}

	// «بستن برنامه» — لغو context عمر
	cancel()

	select {
	case r := <-done:
		if r.err == nil {
			t.Fatalf("cancelled app context must abort the pending ask, got success: %q", r.out)
		}
		if !strings.Contains(r.err.Error(), "اجازه") {
			t.Fatalf("expected the Persian rejection error, got: %v", r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunCommand did not return after the app context was cancelled (stuck on the modal wait)")
	}
}
