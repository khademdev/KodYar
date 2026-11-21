// فرایندهای پس‌زمینه (dev server، watch، build طولانی).
//
// run_command با background=true فرایند را در پس‌زمینه شروع می‌کند و شناسه
// برمی‌گرداند؛ سه ابزار دیگر همان فرایند را مدیریت می‌کنند:
//   - process_output: خواندن افزایشی لاگ (offset/next_offset + وضعیت)
//   - process_stop:   خاتمه‌ی تمیز کل درخت فرایند و برگرداندن وضعیت نهایی
//   - process_list:   فهرست فرایندهای این نشست با وضعیت
//
// تضمین‌ها:
//   - فرایند به نوبت/گفتگو گره نمی‌خورد (تا وقتی خودش تمام شود یا متوقف/برنامه
//     بسته شود زنده می‌ماند)؛ ctx نوبت عمداً استفاده نمی‌شود.
//   - خروجی در بافر حلقه‌ای کران‌دار (۲۵۶KB آخر) نگه داشته می‌شود و خواندن
//     افزایشی است (offset مطلق)؛ فراخوانی no-arg فقط لاگ تازه را می‌دهد.
//   - خاتمه همان مسیر runShell است: اجرای فرایند زیر runShellWithPolicyBackground
//     → لغو ctx → watchdog → terminate/kill درخت فرایند (Job Object در ویندوز،
//     pgid در یونیکس). هیچ پروسه‌ی فرزندی جامانده نمی‌ماند.
//   - سندباکس (وقتی روشن است) برای فرایند پس‌زمینه هم اعمال می‌شود؛ شکست
//     آماده‌سازی سندباکس = اجرا نمی‌شود (fail-closed).
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	bgMaxBuffer     = 256 * 1024      // آخرین ۲۵۶KB خروجی هر فرایند
	bgMaxProcesses  = 8               // سقف فرایندهای هم‌زمانِ در حال اجرا
	bgKeepProcesses = 16              // سقف کل رکوردهای نگه‌داری‌شده در نشست
	bgStopGrace     = 5 * time.Second // انتظار برای خروج پس از kill
	bgDefaultChars  = 12000           // سقف پیش‌فرض هر خواندن لاگ
	bgMaxChars      = 60000           // سقف مطلق هر خواندن
	bgTailOnStop    = 4000            // حجم لاگ نهایی که با stop برمی‌گردد
)

// bgProcess یک فرایند پس‌زمینه‌ی مدیریت‌شده.
type bgProcess struct {
	id      string
	command string
	dir     string
	started time.Time

	mu     sync.Mutex
	buf    []byte // آخرین bgMaxBuffer بایت
	base   int64  // offset مطلق buf[0]
	total  int64  // کل بایت‌های تولیدشده
	read   int64  // offset آخرین خواندن از طریق ابزار
	done   bool
	exit   int
	errTxt string
	cancel context.CancelFunc
	wait   chan struct{}
	// stdin برای CLIهای تعاملی (process_input). در سندباکس nil است.
	// عمداً *os.File است: exec با Stdin غیر-File یک goroutine کپی می‌سازد و
	// Wait تا EOF آن منتظر می‌ماند (deadlock)؛ فایل مستقیم این مشکل را ندارد.
	stdin *os.File
}

// append خروجی تازه را در بافر حلقه‌ای می‌ریزد (thread-safe؛ از دو
// goroutine کپی‌کننده‌ی stdout/stderr صدا زده می‌شود).
func (p *bgProcess) append(b []byte) {
	if len(b) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf = append(p.buf, b...)
	p.total += int64(len(b))
	if over := len(p.buf) - bgMaxBuffer; over > 0 {
		p.buf = append(p.buf[:0], p.buf[over:]...)
		p.base += int64(over)
	}
}

// finish وضعیت پایانی را ثبت می‌کند.
func (p *bgProcess) finish(code int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done = true
	p.exit = code
	switch {
	case err != nil:
		p.errTxt = err.Error()
	case code != 0:
		p.errTxt = fmt.Sprintf("exit status %d", code)
	}
}

// status وضعیت خوانا: running / exited(0) / failed.
func (p *bgProcess) status() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.statusLocked()
}

func (p *bgProcess) statusLocked() string {
	switch {
	case !p.done:
		return "running"
	case p.errTxt != "":
		if p.exit >= 0 {
			return fmt.Sprintf("exited(%d): %s", p.exit, p.errTxt)
		}
		return "failed: " + p.errTxt
	default:
		return fmt.Sprintf("exited(%d)", p.exit)
	}
}

// snapshot قطعه‌ی خروجی از offset را برمی‌گرداند و وضعیت را هم می‌دهد.
// gap=true یعنی offset قدیمی‌تر از بافر نگه‌داری‌شده بود و بخشی از دست رفته.
func (p *bgProcess) snapshot(offset int64, maxChars int) (text string, from, next, total int64, status string, gap bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if offset < p.base {
		offset = p.base
		gap = true
	}
	if offset > p.total {
		offset = p.total
	}
	text = string(p.buf[offset-p.base:])
	next = p.total
	if maxChars > 0 {
		if r := []rune(text); len(r) > maxChars {
			// آخرین بخش (تازه‌ترین لاگ) نگه داشته می‌شود و offset بعدی
			// دقیقاً بعد از همان بخشِ برگشتی است (بقیه از دست می‌رود).
			text = string(r[len(r)-maxChars:])
			next = p.total - int64(len(text))
			gap = true
		}
	}
	return text, offset, next, p.total, p.statusLocked(), gap
}

// ---------------------------------------------------------------- مدیریت در App

// bgAdd فرایند را ثبت می‌کند؛ سقف فرایندهای در حال اجرا و کل رکوردها
// رعایت می‌شود (قدیمی‌ترین رکوردهای تمام‌شده هرس می‌شوند).
func (a *App) bgAdd(p *bgProcess) bool {
	a.bgMu.Lock()
	defer a.bgMu.Unlock()
	if a.bgProcs == nil {
		a.bgProcs = map[string]*bgProcess{}
	}
	running := 0
	for _, q := range a.bgProcs {
		if q.status() == "running" {
			running++
		}
	}
	if running >= bgMaxProcesses {
		return false
	}
	if len(a.bgProcs) >= bgKeepProcesses {
		var finished []*bgProcess
		for _, q := range a.bgProcs {
			if q.status() != "running" {
				finished = append(finished, q)
			}
		}
		sort.Slice(finished, func(i, j int) bool { return finished[i].started.Before(finished[j].started) })
		for _, q := range finished {
			if len(a.bgProcs) < bgKeepProcesses {
				break
			}
			delete(a.bgProcs, q.id)
		}
	}
	a.bgProcs[p.id] = p
	return true
}

func (a *App) bgGet(id string) *bgProcess {
	a.bgMu.Lock()
	defer a.bgMu.Unlock()
	return a.bgProcs[strings.TrimSpace(id)]
}

func (a *App) bgList() []*bgProcess {
	a.bgMu.Lock()
	defer a.bgMu.Unlock()
	out := make([]*bgProcess, 0, len(a.bgProcs))
	for _, p := range a.bgProcs {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].started.Before(out[j].started) })
	return out
}

func (a *App) bgRemove(id string) {
	a.bgMu.Lock()
	defer a.bgMu.Unlock()
	delete(a.bgProcs, id)
}

// startBackgroundProcess فرایند را (با همان دروازه/سندباکس run_command)
// شروع می‌کند و بلافاصله برمی‌گردد. emit اختیاری است (استریم زنده‌ی UI).
func (a *App) startBackgroundProcess(dir, command string, pol *sandboxPolicy, emit func([]byte), flush func()) (*bgProcess, error) {
	id := "proc-" + newID()
	runCtx, cancel := context.WithCancel(context.Background())
	p := &bgProcess{
		id:      id,
		command: command,
		dir:     dir,
		started: time.Now(),
		read:    0,
		cancel:  cancel,
		wait:    make(chan struct{}),
	}
	var stdinReader io.Reader
	var stdinReadEnd *os.File
	if pol == nil {
		pr, pw, perr := os.Pipe()
		if perr != nil {
			cancel()
			return nil, fmt.Errorf("create stdin pipe: %w", perr)
		}
		p.stdin = pw // قبل از bgAdd تا هیچ خواننده‌ی هم‌زمانی آن را نبیند
		stdinReader = pr
		stdinReadEnd = pr
	}
	if !a.bgAdd(p) {
		cancel()
		return nil, fmt.Errorf("too many running background processes (max %d); stop one with process_stop first", bgMaxProcesses)
	}
	onOutput := func(b []byte) {
		p.append(b)
		if emit != nil {
			emit(b)
		}
	}
	// stdin فقط بدون سندباکس: مسیر sandboxed stdin ندارد (صریح، نه کمین).
	if pol == nil {
		safeGo(a, "process.run", func() {
			defer close(p.wait)
			defer func() {
				_ = p.stdin.Close()
				_ = stdinReadEnd.Close()
			}()
			var code int
			var rerr error
			func() {
				defer func() {
					if r := recover(); r != nil {
						recordPanic(a, "process.run", r)
						code, rerr = -1, fmt.Errorf("internal error while running the background process")
					}
				}()
				_, code, rerr = runShellWithPolicyBackgroundInput(runCtx, dir, command, onOutput, nil, stdinReader)
			}()
			p.finish(code, rerr)
			if flush != nil {
				flush()
			}
			logAppErrorFor(a, "process", fmt.Sprintf("%s finished (%s)", id, p.status()))
		})
		return p, nil
	}
	safeGo(a, "process.run", func() {
		defer close(p.wait)
		var code int
		var rerr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					recordPanic(a, "process.run", r)
					code, rerr = -1, fmt.Errorf("internal error while running the background process")
				}
			}()
			_, code, rerr = runShellWithPolicyBackground(runCtx, dir, command, onOutput, pol)
		}()
		p.finish(code, rerr)
		if flush != nil {
			flush()
		}
		logAppErrorFor(a, "process", fmt.Sprintf("%s finished (%s)", id, p.status()))
	})
	return p, nil
}

// stopBackgroundProcess درخت فرایند را می‌کشد و تا خروج (با مهلت) صبر می‌کند.
func (a *App) stopBackgroundProcess(id string) (*bgProcess, error) {
	p := a.bgGet(id)
	if p == nil {
		return nil, fmt.Errorf("unknown process id %q (use process_list to see active processes)", id)
	}
	p.mu.Lock()
	done := p.done
	p.mu.Unlock()
	if !done {
		p.cancel()
		select {
		case <-p.wait:
		case <-time.After(bgStopGrace):
			return p, fmt.Errorf("process %s did not exit within %s after being killed", p.id, bgStopGrace)
		}
	}
	return p, nil
}

// stopAllBackgroundProcesses همه‌ی فرایندهای این نشست را می‌کشد (خروج برنامه).
func (a *App) stopAllBackgroundProcesses() {
	for _, p := range a.bgList() {
		_, _ = a.stopBackgroundProcess(p.id)
	}
}

// ---------------------------------------------------------------- ابزارها

// toolRunCommandBackground مسیر background=true در run_command.
func (a *App) toolRunCommandBackground(parent context.Context, dir, command string) (string, bool) {
	emit, flush := a.shellOutputEmitter(parent)
	p, err := a.startBackgroundProcess(dir, command, a.sandboxPolicyForCommand(dir, command), emit, flush)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	turnRecorderFrom(parent).recordCommand(command, 0, true)
	return fmt.Sprintf("OK: started background process %s (status: running).\n"+
		"Read new log output with process_output {\"id\":%q}; stop it with process_stop {\"id\":%q}. "+
		"It keeps running between turns and is killed when the app closes.", p.id, p.id, p.id), true
}

// toolProcessOutput خروجی تازه‌ی یک فرایند را برمی‌گرداند.
// toolProcessOutput لاگ یک فرایند پس‌زمینه را می‌خواند. hasOffset=false یعنی
// مدل فیلد offset را نفرستاده (فقط خروجی جدید را بده)؛ hasOffset=true با
// offset=0 یعنی «از ابتدای لاگ بخوان» — تا مدل بتواند لاگ کامل را بازبینی کند.
func (a *App) toolProcessOutput(id string, offset int64, hasOffset bool, maxChars int) (string, bool) {
	p := a.bgGet(id)
	if p == nil {
		return fmt.Sprintf("ERROR: unknown process id %q (use process_list to see active processes).", strings.TrimSpace(id)), false
	}
	if maxChars <= 0 {
		maxChars = bgDefaultChars
	}
	if maxChars > bgMaxChars {
		maxChars = bgMaxChars
	}
	p.mu.Lock()
	if !hasOffset {
		offset = p.read // خواندن افزایشی پیش‌فرض (فیلد offset فرستاده نشده)
	}
	p.mu.Unlock()
	if offset < 0 {
		offset = 0
	}
	text, from, next, total, status, gap := p.snapshot(offset, maxChars)
	p.mu.Lock()
	p.read = next
	p.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "process %s [%s]\ntotal=%d bytes, read from offset %d, next_offset=%d\n", p.id, status, total, from, next)
	if gap {
		b.WriteString("note: earlier output has been discarded (bounded buffer) or truncated.\n")
	}
	if strings.TrimSpace(text) == "" {
		b.WriteString("(no new output)")
	} else {
		b.WriteString(strings.TrimRight(text, "\n"))
	}
	return b.String(), true
}

// toolProcessStop فرایند را می‌کشد و وضعیت/لاگ پایانی را برمی‌گرداند.
func (a *App) toolProcessStop(id string) (string, bool) {
	p, err := a.stopBackgroundProcess(id)
	if p == nil {
		return "ERROR: " + err.Error(), false
	}
	text, _, _, total, status, _ := p.snapshot(0, bgTailOnStop)
	a.bgRemove(p.id)
	var b strings.Builder
	fmt.Fprintf(&b, "process %s stopped: %s (total output: %d bytes)\n", p.id, status, total)
	if err != nil {
		fmt.Fprintf(&b, "warning: %v\n", err)
	}
	if t := strings.TrimRight(text, "\n"); t != "" {
		b.WriteString("\n--- last output ---\n")
		b.WriteString(t)
	}
	return b.String(), err == nil
}

// toolProcessInput متن را به stdin یک فرایند پس‌زمینه می‌نویسد (CLI تعاملی،
// انتخاب پیش‌فرض پرامپت‌ها، دستورهای dev server مثل r/q).
func (a *App) toolProcessInput(id, text string, newline bool) (string, bool) {
	p := a.bgGet(id)
	if p == nil {
		return fmt.Sprintf("ERROR: unknown process id %q (use process_list to see active processes).", strings.TrimSpace(id)), false
	}
	p.mu.Lock()
	stdin, done := p.stdin, p.done
	p.mu.Unlock()
	if done {
		return fmt.Sprintf("ERROR: process %s has already exited; stdin cannot be written.", p.id), false
	}
	if stdin == nil {
		return "ERROR: this process has no stdin (it was started inside the sandbox). " +
			"Disable the sandbox in Settings → Folder and start it again to drive an interactive CLI.", false
	}
	payload := text
	if newline {
		payload += "\n"
	}
	if payload == "" {
		return "ERROR: nothing to send (pass text and/or newline=true).", false
	}
	if _, err := io.WriteString(stdin, payload); err != nil {
		return "ERROR: writing to the process stdin failed: " + err.Error(), false
	}
	return fmt.Sprintf("OK: sent %d byte(s) to stdin of %s. Read the reaction with process_output.", len(payload), p.id), true
}

// toolProcessWait تا خروج فرایند یا مهلت صبر می‌کند (بدون poll کردن لاگ).
func (a *App) toolProcessWait(id string, timeoutSec int) (string, bool) {
	p := a.bgGet(id)
	if p == nil {
		return fmt.Sprintf("ERROR: unknown process id %q (use process_list to see active processes).", strings.TrimSpace(id)), false
	}
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	if timeoutSec > 300 {
		timeoutSec = 300
	}
	timer := time.NewTimer(time.Duration(timeoutSec) * time.Second)
	defer timer.Stop()
	select {
	case <-p.wait:
		text, _, _, total, status, _ := p.snapshot(p.read, bgTailOnStop)
		var b strings.Builder
		fmt.Fprintf(&b, "process %s finished: %s (total output: %d bytes)\n", p.id, status, total)
		if t := strings.TrimRight(text, "\n"); t != "" {
			b.WriteString("\n--- output since the last read ---\n")
			b.WriteString(t)
		}
		return b.String(), true
	case <-timer.C:
		return fmt.Sprintf("process %s is still running after %ds. Read output with process_output or stop it with process_stop.", p.id, timeoutSec), true
	}
}

// toolWaitForPort تا باز شدن یک پورت محلی صبر می‌کند (dev server آماده شد).
// spec می‌تواند URL باشد (http://localhost:5173) یا host:port یا فقط port.
func (a *App) toolWaitForPort(spec string, timeoutSec int) (string, bool) {
	host, port, err := parsePortSpec(spec)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	if timeoutSec > 300 {
		timeoutSec = 300
	}
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	addr := net.JoinHostPort(host, port)
	for {
		conn, derr := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if derr == nil {
			_ = conn.Close()
			return fmt.Sprintf("OK: %s is accepting connections (waited %s).", addr, time.Since(deadline.Add(-time.Duration(timeoutSec)*time.Second)).Round(time.Millisecond)), true
		}
		if time.Now().After(deadline) {
			return fmt.Sprintf("ERROR: %s did not open within %ds (last error: %v). Check the process log with process_output.", addr, timeoutSec, derr), false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// parsePortSpec «http://127.0.0.1:5173/x»، «localhost:5173» یا «5173» را
// به host/port تبدیل می‌کند (پیش‌فرض host: 127.0.0.1).
func parsePortSpec(spec string) (string, string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", fmt.Errorf("port or URL is required (e.g. 5173, localhost:5173 or http://127.0.0.1:5173)")
	}
	host, port := "127.0.0.1", ""
	if strings.Contains(spec, "://") {
		u, err := url.Parse(spec)
		if err != nil || u.Host == "" {
			return "", "", fmt.Errorf("invalid URL %q", spec)
		}
		host, port = u.Hostname(), u.Port()
	} else if h, p, err := net.SplitHostPort(spec); err == nil {
		host, port = h, p
	} else {
		port = spec
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return "", "", fmt.Errorf("invalid port %q (expected 1-65535)", port)
	}
	return host, strconv.Itoa(n), nil
}

// toolProcessList فهرست فرایندهای نشست.
func (a *App) toolProcessList() (string, bool) {
	list := a.bgList()
	if len(list) == 0 {
		return "No background processes in this session.", true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d background process(es):\n", len(list))
	for _, p := range list {
		p.mu.Lock()
		total := p.total
		p.mu.Unlock()
		fmt.Fprintf(&b, "- %s [%s] %s — %d bytes, started %s\n", p.id, p.status(),
			cutUTF8(p.command, 120), total, p.started.Format("15:04:05"))
	}
	b.WriteString("Use process_output {\"id\":...} to read new output and process_stop {\"id\":...} to stop.")
	return b.String(), true
}
