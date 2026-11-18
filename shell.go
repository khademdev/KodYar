// اجراکننده‌ی یکپارچه‌ی shell — تنها مسیر اجرای دستور در MyAI.
//
// همه‌ی سه نقطه‌ی اجرای دستور (App.RunCommand، toolRunCommand ایجنت و
// execCustomTool) از runShell عبور می‌کنند تا:
// - کشتن درخت فرایند (نه فقط shell) روی timeout/لغو یکسان باشد
// (ویندوز: Job Object با KILL_ON_JOB_CLOSE + taskkill جایگزین؛
// یونیکس: گروه فرایند با Setpgid و SIGTERM→SIGKILL)
// - خروجی در بافر کران‌دار (سر+دنباله با نشانگر برش) نگه‌داری شود —
// هرگز CombinedOutput بدون سقف
// - خروجی به‌صورت تدریجی با callback به UI استریم شود (chat:tool_output)
package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// محدودیت‌های اجرای دستور (مطابق رفتار قبلی: ۶۰ ثانیه پیش‌فرض، ۳۰۰ سقف)
const (
	shellDefaultTimeout = 60 * time.Second
	shellMaxTimeout     = 300 * time.Second
	shellGracePeriod    = 2 * time.Second // یونیکس: فاصله‌ی SIGTERM تا SIGKILL

	// بافر کران‌دار خروجی: سر و دنباله هر یک ۳۲KB
	shellHeadBytes = 32 * 1024
	shellTailBytes = 32 * 1024
)

// runShell دستور را در dir (پوشه‌ی کاری) با shell سیستم اجرا می‌کند.
//
// onOutput (اختیاری) به‌صورت تدریجی با تکه‌های stdout/stderr صدا زده می‌شود؛
// ممکن است هم‌زمان از دو goroutine کپی‌کننده‌ی exec فراخوانی شود → باید
// thread-safe باشد (خروجی shellOutputEmitter همین‌طور است)؛ nil مجاز است.
//
// خروجی: متن کران‌دار (سر+نشانگر+دنباله)، کد خروج فرایند (-1 اگر با سیگنال
// کشته شده یا Start شکست خورده) و خطای Wait (nil فقط در موفقیت کامل).
// رفتار خطا/متن دقیقاً مثل CombinedOutput قبلی حفظ شده است.
// runShellWithPolicy اجرای دستور با سیاست سندباکس. nil = بدون سندباکس
// (خاموش، پلتفرم بدون پیاده‌سازی، یا محیط تست). اگر سندباکس روشن باشد و
// راه‌اندازی‌اش شکست بخورد، دستور اجرا نمی‌شود (fail-closed) — هیچ مسیر
// بی‌صدا بدون ایزوله‌سازی اجرا نمی‌کند.
func runShellWithPolicy(ctx context.Context, dir, command string, timeout time.Duration, onOutput func([]byte), sb *sandboxPolicy) (string, int, error) {
	if sb == nil {
		return runShell(ctx, dir, command, timeout, onOutput)
	}
	out, code, err := runSandboxedShell(ctx, dir, command, timeout, onOutput, sb)
	if err != nil && code < 0 {
		return out, code, fmt.Errorf("command was not run: sandbox failed: %w (disable the sandbox in Settings → Folder to run it unsandboxed)", err)
	}
	return out, code, err
}

func runShell(ctx context.Context, dir, command string, timeout time.Duration, onOutput func([]byte)) (string, int, error) {
	if timeout <= 0 {
		timeout = shellDefaultTimeout
	}
	if timeout > shellMaxTimeout {
		timeout = shellMaxTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return runShellCtx(cctx, dir, command, onOutput)
}

// runShellBackground مثل runShell است اما بدون مهلت: فرایند تا وقتی خودش
// تمام شود یا ctx لغو شود (stop/خروج برنامه) زنده می‌ماند. کشتن درخت فرایند
// روی لغو همان مسیر runShell است (watchdog → terminate → kill).
func runShellBackground(ctx context.Context, dir, command string, onOutput func([]byte)) (string, int, error) {
	return runShellCtx(ctx, dir, command, onOutput)
}

// runShellWithPolicyBackground همان runShellWithPolicy برای فرایند بلندمدت:
// اگر سندباکس سیاست داشته باشد، داخل آن و بدون مهلت اجرا می‌شود؛ شکست
// آماده‌سازی سندباکس = اجرا نمی‌شود (fail-closed).
func runShellWithPolicyBackground(ctx context.Context, dir, command string, onOutput func([]byte), sb *sandboxPolicy) (string, int, error) {
	if sb == nil {
		return runShellBackground(ctx, dir, command, onOutput)
	}
	out, code, err := runSandboxedShell(ctx, dir, command, 0, onOutput, sb)
	if err != nil && code < 0 {
		return out, code, fmt.Errorf("command was not run: sandbox failed: %w (disable the sandbox in Settings → Folder to run it unsandboxed)", err)
	}
	return out, code, err
}

// runShellCtx بدنه‌ی اجرا با ctx آماده (مهلت یا لغو، تصمیم فراخواننده).
func runShellCtx(cctx context.Context, dir, command string, onOutput func([]byte)) (string, int, error) {
	var cmd *exec.Cmd
	if isWindows() {
		cmd = exec.CommandContext(cctx, "cmd")
		configureShell(cmd, command) // CmdLine خام + chcp 65001 + CREATE_NO_WINDOW
	} else {
		cmd = exec.CommandContext(cctx, "sh", "-c", command)
	}
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.WaitDelay = 5 * time.Second // همان رفتار قبلی: سقف انتظار برای pipeهای باز

	// راه‌اندازی کشتن درخت فرایند (پیش از Start — Setpgid باید قبل باشد)
	tree, terr := prepareProcessTree(cmd)
	if terr != nil {
		// ویندوز بدون Job Object هم باید کار کند → fallback داخل prepare
		return "", -1, fmt.Errorf("process tree setup: %w", terr)
	}
	// لغوی نرم: به‌جای SIGKILLِ فقطِ shell توسط exec، کل گروه/Job با SIGTERM
	// خاتمه می‌یابد؛ watcher بعد از مهلت، SIGKILL (یا TerminateJobObject) می‌زند.
	cmd.Cancel = func() error { tree.terminate(); return nil }

	out := &boundedOutput{onOutput: onOutput}
	cmd.Stdout = out
	cmd.Stderr = out // نکته: با writer غیر-File یکسان، exec نوشتن‌ها را سریال می‌کند

	if err := cmd.Start(); err != nil {
		tree.close()
		return "", -1, err
	}
	if err := tree.attach(cmd); err != nil {
		// بدون attach نمی‌توان درخت را کشت → همین فرایند را بکش و رها کن
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		tree.close()
		return "", -1, fmt.Errorf("attach process tree: %w", err)
	}
	if cctx.Err() != nil {
		// ctx پیش از attach لغو شده بود — درخت را هم‌ان بکش
		tree.terminate()
		tree.kill()
	}

	// Wait در goroutine خودش؛ watcher روی لغو/timeout درخت را می‌کشد.
	waitCh := make(chan error, 1)
	// panic در Wait نباید فرایند را بکشد و نباید فراخواننده را برای همیشه
	// منتظر بگذارد؛ پس نتیجه همیشه (حتی در صورت panic) فرستاده می‌شود.
	go func() {
		var werr error
		if gerr := callGuarded(nil, "shell.wait", func() error { werr = cmd.Wait(); return nil }); gerr != nil {
			werr = gerr
		}
		waitCh <- werr
	}()

	watchStop := make(chan struct{})
	safeGo(nil, "shell.watchdog", func() {
		select {
		case <-cctx.Done():
			tree.terminate() // SIGTERM گروه / TerminateJobObject
			grace := time.NewTimer(shellGracePeriod)
			defer grace.Stop()
			select {
			case <-watchStop:
			case <-grace.C:
				tree.kill() // SIGKILL گروه — تضمین مرگ کامل
			}
		case <-watchStop:
		}
	})

	werr := <-waitCh
	close(watchStop)
	tree.close() // بستن Job Handle (KILL_ON_JOB_CLOSE برای فرزندانِ جامانده)

	exit := -1
	if cmd.ProcessState != nil {
		exit = cmd.ProcessState.ExitCode()
	}
	return out.String(), exit, annotateExitStatus(werr, exit)
}

// annotateExitStatus کد خروجِ کرشِ native ویندوز (NTSTATUS، مثل ‎0xC0000005‎)
// را کنار عدد دهدهی با نام خوانا نشان می‌دهد. عدد خالصِ 3221225477 در لاگ و
// نتیجهٔ ابزار هیچ معنایی برای مدل/کاربر ندارد، در حالی که همان کد در ۹۹٪
// موارد یعنی «برنامه در سطح سیستم‌عامل کرش کرد»، نه «برنامه خطا برگرداند».
func annotateExitStatus(err error, code int) error {
	if err == nil || code <= 0 {
		return err
	}
	u := uint32(code)
	if u < 0xC0000000 {
		return err
	}
	// فقط خطای «کد خروج» بازنویسی می‌شود؛ خطاهای واقعی اجرا دست‌نخورده‌اند.
	if !strings.HasPrefix(err.Error(), "exit status ") {
		return err
	}
	name := ntStatusName(u)
	return fmt.Errorf("exit status %d (0x%08X: %s)", code, u, name)
}

// ntStatusName نام خوانای کدهای NTSTATUS پرتکرار در کرش برنامه‌های native.
func ntStatusName(code uint32) string {
	switch code {
	case 0xC0000005:
		return "access violation"
	case 0xC00000FD:
		return "stack overflow"
	case 0xC0000135:
		return "DLL not found"
	case 0xC0000142:
		return "DLL initialization failed"
	case 0xC0000374:
		return "heap corruption"
	case 0xC0000409:
		return "stack buffer overrun / fail-fast"
	}
	return "fatal system error"
}

// ---------------------------------------------------------------- بافر کران‌دار

// boundedOutput خروجی stdout+stderr را با سقف جمع می‌کند: ۳۲KB اول (سر) +
// ۳۲KB آخر (دنباله) + نشانگر تعداد بایت‌های حذف‌شده. هرگز بی‌نهایت رشد نمی‌کند.
type boundedOutput struct {
	mu       sync.Mutex
	onOutput func([]byte)
	head     []byte
	tail     []byte
	dropped  int64
}

// Write رابط io.Writer — از goroutineهای کپی‌کننده‌ی exec صدا زده می‌شود.
func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.onOutput != nil {
		b.onOutput(p) // فراخوانی زیر قفل → سریال بین stdout/stderr
	}
	if room := shellHeadBytes - len(b.head); room > 0 {
		take := n
		if take > room {
			take = room
		}
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		b.tail = append(b.tail, p...)
		if over := len(b.tail) - shellTailBytes; over > 0 {
			b.dropped += int64(over)
			// جابه‌جایی درجا (memmove) — آرایه هیچ‌وقت از ۲×tail بزرگ‌تر نمی‌شود
			b.tail = append(b.tail[:0], b.tail[over:]...)
		}
	}
	return n, nil
}

// String خروجی نهایی با نشانگر برش.
func (b *boundedOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dropped == 0 {
		return string(b.head) + string(b.tail)
	}
	return string(b.head) +
		fmt.Sprintf("\n…[truncated %d bytes]…\n", b.dropped) +
		string(b.tail)
}

// ---------------------------------------------------------------- استریم خروجی به UI

// toolCallCtxInfo شناسه‌ی گفتگو/فراخوانی ابزار برای رویداد chat:tool_output —
// از طریق context از حلقه‌ی ایجنت به لایه‌ی runShell می‌رسد.
type toolCallCtxInfo struct {
	convID string
	callID string
}

type ctxKeyToolCall struct{}

// withToolCallInfo قرارداد درج اطلاعات فراخوانی در context ابزار.
func withToolCallInfo(ctx context.Context, convID, callID string) context.Context {
	return context.WithValue(ctx, ctxKeyToolCall{}, &toolCallCtxInfo{convID: convID, callID: callID})
}

// throttledEmitter ارسال‌کننده‌ی کران‌دار نرخ: تکه‌ها را انباشته و حداکثر
// یکی در minInterval می‌فرستد؛ flush باقی‌مانده‌ی آخر را می‌فرستد.
// send از خود فراخوانی‌کننده فراخوانی می‌شود — باید thread-safe باشد یا
// همیشه از emit/flush (زیر قفل) صدا زده شود.
type throttledEmitter struct {
	mu          sync.Mutex
	send        func(chunk string)
	minInterval time.Duration
	maxBuffer   int
	buf         []byte
	last        time.Time
}

func newThrottledEmitter(minInterval time.Duration, maxBuffer int, send func(chunk string)) *throttledEmitter {
	return &throttledEmitter{send: send, minInterval: minInterval, maxBuffer: maxBuffer}
}

// emit یک تکه‌ی خروجی را می‌پذیرد؛ در صورت رسیدن نرخ مجاز، ارسال می‌کند.
func (t *throttledEmitter) emit(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.buf)+len(p) > t.maxBuffer {
		keep := t.maxBuffer - len(p)
		if keep < 0 {
			keep = 0
		}
		tail := p
		if len(tail) > t.maxBuffer {
			tail = tail[len(tail)-t.maxBuffer:]
		}
		t.buf = append(t.buf[:keep], tail...)
	} else {
		t.buf = append(t.buf, p...)
	}
	if time.Since(t.last) >= t.minInterval {
		t.sendNow()
	}
}

// flush باقی‌مانده‌ی انباشته را می‌فرستد (پایان فرایند).
func (t *throttledEmitter) flush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sendNow()
}

func (t *throttledEmitter) sendNow() {
	if len(t.buf) == 0 {
		return
	}
	chunk := string(t.buf)
	t.buf = t.buf[:0]
	t.last = time.Now()
	t.send(chunk)
}

// shellOutputEmitter خروجی تدریجی run_command را با نرخ حداکثر ۱۰ رویداد
// در ثانیه به UI می‌فرستد (chat:tool_output). بدون UI/اطلاعات فراخوانی → هیچ.
func (a *App) shellOutputEmitter(ctx context.Context) (emit func([]byte), flush func()) {
	info, _ := ctx.Value(ctxKeyToolCall{}).(*toolCallCtxInfo)
	if info == nil || info.convID == "" || info.callID == "" {
		return nil, func() {}
	}
	e := newThrottledEmitter(100*time.Millisecond, 8*1024, func(chunk string) {
		runtimeEmit(a, "chat:tool_output", map[string]interface{}{
			"conversationId": info.convID,
			"callId":         info.callID,
			"chunk":          chunk,
		})
	})
	return e.emit, e.flush
}
