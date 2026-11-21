// تست‌های P0-3: هدایت فرایند تعاملی (stdin)، انتظار برای خروج، و انتظار
// برای باز شدن پورت — بدون poll کردن لاگ.
package main

import (
	"net"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestProcessInputReachesStdin: متن ابزار باید واقعاً به stdin فرایند برسد.
func TestProcessInputReachesStdin(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	a := newTestApp(t)
	cmd := `node -e "process.stdin.once('data',d=>{console.log('GOT:'+d.toString().trim());process.exit(0)})"`
	p, err := a.startBackgroundProcess(t.TempDir(), cmd, nil, nil, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _, _ = a.stopBackgroundProcess(p.id) })

	out, ok := a.toolProcessInput(p.id, "hello", true)
	if !ok {
		t.Fatalf("process_input failed: %s", out)
	}
	waitForProcessOutput(t, p, "GOT:hello")
}

// TestProcessInputRejectsClosedProcess: نوشتن در فرایند تمام‌شده باید خطای
// روشن بدهد، نه panic یا نوشتن بی‌اثر.
func TestProcessInputRejectsClosedProcess(t *testing.T) {
	a := newTestApp(t)
	p, err := a.startBackgroundProcess(t.TempDir(), bgCmd("exit 0", "true"), nil, nil, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	<-p.wait
	if out, ok := a.toolProcessInput(p.id, "x", true); ok || !strings.Contains(out, "already exited") {
		t.Fatalf("expected a clear 'already exited' error, got ok=%v: %s", ok, out)
	}
	if out, ok := a.toolProcessInput("proc-missing", "x", true); ok || !strings.Contains(out, "unknown process id") {
		t.Fatalf("expected unknown-id error, got ok=%v: %s", ok, out)
	}
}

// TestProcessWaitReturnsExitCode: process_wait باید تا خروج صبر کند و کد
// خروج را گزارش دهد (بدون poll).
func TestProcessWaitReturnsExitCode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	a := newTestApp(t)
	p, err := a.startBackgroundProcess(t.TempDir(), `node -e "setTimeout(()=>process.exit(3),200)"`, nil, nil, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	out, ok := a.toolProcessWait(p.id, 15)
	if !ok {
		t.Fatalf("process_wait failed: %s", out)
	}
	if !strings.Contains(out, "finished: exited(3)") {
		t.Fatalf("wait must report the exit code: %s", out)
	}
}

// TestProcessWaitTimesOut: فرایند در حال اجرا باید «هنوز در حال اجراست» بدهد.
func TestProcessWaitTimesOut(t *testing.T) {
	a := newTestApp(t)
	cmd := bgCmd(`ping -n 6 127.0.0.1 >nul`, `sleep 5`)
	p, err := a.startBackgroundProcess(t.TempDir(), cmd, nil, nil, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _, _ = a.stopBackgroundProcess(p.id) })
	out, ok := a.toolProcessWait(p.id, 1)
	if !ok || !strings.Contains(out, "still running") {
		t.Fatalf("expected a still-running answer, got ok=%v: %s", ok, out)
	}
}

// TestWaitForPortSeesListener: پورت باز باید سریع تأیید شود؛ پورت بسته با
// مهلت کوتاه باید شکست صریح بدهد.
func TestWaitForPortSeesListener(t *testing.T) {
	a := newTestApp(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	if out, ok := a.toolWaitForPort("http://localhost:"+strconv.Itoa(port)+"/app", 5); !ok {
		t.Fatalf("open port not detected: %s", out)
	}
	// همان پورت با یک پورت بسته‌ی دیگر: باید شکست بدهد.
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closed := ln2.Addr().(*net.TCPAddr).Port
	ln2.Close()
	if out, ok := a.toolWaitForPort(strconv.Itoa(closed), 1); ok || !strings.Contains(out, "did not open") {
		t.Fatalf("closed port must fail clearly, got ok=%v: %s", ok, out)
	}
	if out, ok := a.toolWaitForPort("not-a-port", 1); ok || !strings.Contains(out, "invalid port") {
		t.Fatalf("bad input must be rejected, got ok=%v: %s", ok, out)
	}
}

// TestParsePortSpec: URL/host:port/port ساده.
func TestParsePortSpec(t *testing.T) {
	cases := map[string][2]string{
		"5173":                      {"127.0.0.1", "5173"},
		"localhost:3000":            {"127.0.0.1", "3000"},
		"http://127.0.0.1:8080/x":   {"127.0.0.1", "8080"},
		"https://example.com:4443/": {"example.com", "4443"},
	}
	for in, want := range cases {
		h, p, err := parsePortSpec(in)
		if err != nil || h != want[0] || p != want[1] {
			t.Fatalf("parsePortSpec(%q) = %q,%q,%v; want %v", in, h, p, err, want)
		}
	}
}
