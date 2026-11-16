// تست‌های فرایند پس‌زمینه: شروع، خواندن افزایشی لاگ، توقف تمیز، فهرست و
// بافر کران‌دار. همه‌ی فرایندها واقعی‌اند اما کوتاه/قابل‌کشت.
package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func bgCmd(win, posix string) string {
	if isWindows() {
		return win
	}
	return posix
}

// waitForProcessOutput تا ظاهر شدن want در خروجی صبر می‌کند (بدون تغییر
// offset خواندن ابزار — snapshot مستقیم).
func waitForProcessOutput(t *testing.T, p *bgProcess, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		text, _, _, _, _, _ := p.snapshot(0, bgMaxChars)
		if strings.Contains(text, want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	text, _, _, _, status, _ := p.snapshot(0, bgMaxChars)
	t.Fatalf("output never contained %q (status %s): %q", want, status, text)
}

func TestBackgroundProcessIncrementalOutput(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	cmd := bgCmd(
		`echo one & ping -n 2 127.0.0.1 >nul & echo two`,
		`echo one; sleep 2; echo two`)
	p, err := a.startBackgroundProcess(dir, cmd, nil, nil, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _, _ = a.stopBackgroundProcess(p.id) })

	waitForProcessOutput(t, p, "one")
	first, ok := a.toolProcessOutput(p.id, 0, 0)
	if !ok {
		t.Fatalf("first read failed: %s", first)
	}
	if !strings.Contains(first, "one") {
		t.Fatalf("first read must contain the early output: %q", first)
	}
	if !strings.Contains(first, "next_offset=") {
		t.Fatalf("read must report next_offset for incremental reads: %q", first)
	}

	waitForProcessOutput(t, p, "two")
	second, ok := a.toolProcessOutput(p.id, 0, 0)
	if !ok {
		t.Fatalf("second read failed: %s", second)
	}
	if !strings.Contains(second, "two") {
		t.Fatalf("second read must contain the new output: %q", second)
	}
	if strings.Contains(second, "one") && !strings.Contains(first, "two") {
		t.Fatalf("incremental read must not repeat older output: %q", second)
	}
}

func TestBackgroundProcessStopKillsAndReports(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	cmd := bgCmd(`ping -n 30 127.0.0.1 >nul`, `sleep 30`)
	p, err := a.startBackgroundProcess(dir, cmd, nil, nil, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// کمی صبر تا فرایند واقعاً بالا بیاید.
	time.Sleep(200 * time.Millisecond)
	if got := p.status(); got != "running" {
		t.Fatalf("process must be running before stop, got %s", got)
	}

	start := time.Now()
	out, ok := a.toolProcessStop(p.id)
	if !ok {
		t.Fatalf("stop failed: %s", out)
	}
	if d := time.Since(start); d > bgStopGrace+2*time.Second {
		t.Fatalf("stop took too long: %s", d)
	}
	if !strings.Contains(out, "stopped") || !strings.Contains(out, p.id) {
		t.Fatalf("stop result must report the process and status: %q", out)
	}
	if strings.TrimSpace(p.status()) == "running" {
		t.Fatalf("process must not be running after stop: %s", out)
	}
	// پس از stop رکورد از فهرست حذف می‌شود و دوباره stop خطای روشن می‌دهد.
	if _, ok := a.toolProcessStop(p.id); ok {
		t.Fatal("stopping an unknown/removed process must fail")
	}
	if list, _ := a.toolProcessList(); strings.Contains(list, p.id) {
		t.Fatalf("stopped process must be removed from the list: %q", list)
	}
}

func TestBackgroundProcessListAndUnknownID(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	cmd := bgCmd(`ping -n 30 127.0.0.1 >nul`, `sleep 30`)
	p, err := a.startBackgroundProcess(dir, cmd, nil, nil, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _, _ = a.stopBackgroundProcess(p.id) })

	list, ok := a.toolProcessList()
	if !ok || !strings.Contains(list, p.id) || !strings.Contains(list, "running") {
		t.Fatalf("list must include the running process: %q", list)
	}
	if !strings.Contains(list, "process_output") || !strings.Contains(list, "process_stop") {
		t.Fatalf("list must explain the follow-up tools: %q", list)
	}

	if out, ok := a.toolProcessOutput("proc-does-not-exist", 0, 0); ok || !strings.Contains(out, "unknown process id") {
		t.Fatalf("unknown id must return an ERROR: %q", out)
	}
	if out, ok := a.toolProcessStop("proc-does-not-exist"); ok || !strings.Contains(out, "unknown process id") {
		t.Fatalf("unknown stop must return an ERROR: %q", out)
	}
}

// TestBackgroundProcessRunnerEndToEnd مسیر ابزار run_command background=true.
func TestBackgroundProcessRunnerEndToEnd(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	cmd := bgCmd(`ping -n 30 127.0.0.1 >nul`, `sleep 30`)
	out, ok := a.toolRunCommandBackground(context.Background(), dir, cmd)
	if !ok {
		t.Fatalf("background run failed: %s", out)
	}
	if !strings.Contains(out, "started background process proc-") || !strings.Contains(out, "process_stop") {
		t.Fatalf("start result must return the id and next steps: %q", out)
	}
	list, _ := a.toolProcessList()
	if !strings.Contains(list, "running") {
		t.Fatalf("process must be listed as running: %q", list)
	}
	a.stopAllBackgroundProcesses()
	for _, p := range a.bgList() {
		if strings.TrimSpace(p.status()) == "running" {
			t.Fatalf("stopAll must kill every process, %s still running", p.id)
		}
	}
}

// TestBackgroundBufferIsBounded بافر هیچ‌گاه بی‌نهایت رشد نمی‌کند و
// offset قدیمی با پرچم gap گزارش می‌شود.
func TestBackgroundBufferIsBounded(t *testing.T) {
	p := &bgProcess{id: "proc-x"}
	chunk := []byte(strings.Repeat("x", 64*1024))
	for i := 0; i < 6; i++ { // ۳۸۴KB > ۲۵۶KB
		p.append(chunk)
	}
	p.mu.Lock()
	size := len(p.buf)
	base := p.base
	total := p.total
	p.mu.Unlock()
	if size > bgMaxBuffer {
		t.Fatalf("buffer grew beyond the cap: %d > %d", size, bgMaxBuffer)
	}
	if total != 6*int64(len(chunk)) {
		t.Fatalf("total must count every byte: %d", total)
	}
	if base == 0 {
		t.Fatal("old bytes must be dropped from the front")
	}
	text, from, next, _, _, gap := p.snapshot(0, 0)
	if !gap || from != base || next != total || len(text) == 0 {
		t.Fatalf("old offset must be clamped with gap: gap=%v from=%d base=%d next=%d total=%d", gap, from, base, next, total)
	}
	// خواندن از offset معتبر بدون gap.
	text, _, _, _, _, gap2 := p.snapshot(total-10, 0)
	if gap2 || len(text) != 10 {
		t.Fatalf("valid offset must not report a gap: gap=%v text=%d", gap2, len(text))
	}
}

// TestBackgroundProcessCap سقف فرایندهای هم‌زمان رعایت می‌شود.
func TestBackgroundProcessCap(t *testing.T) {
	a := newTestApp(t)
	cancels := make([]context.CancelFunc, 0, bgMaxProcesses)
	for i := 0; i < bgMaxProcesses; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		_ = ctx
		cancels = append(cancels, cancel)
		p := &bgProcess{id: "proc-cap-" + string(rune('a'+i)), cancel: cancel, wait: make(chan struct{})}
		if !a.bgAdd(p) {
			t.Fatalf("process %d must fit under the cap", i)
		}
	}
	p := &bgProcess{id: "proc-over", cancel: func() {}, wait: make(chan struct{})}
	if a.bgAdd(p) {
		t.Fatal("the (max+1)-th running process must be rejected")
	}
	for _, c := range cancels {
		c()
	}
}
