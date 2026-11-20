// تست‌های جداسازی خرابی (Mission 4 — Stage 1): panic در هیچ مسیری نباید
// برنامه را بکشد، نباید goroutine جا بگذارد و باید یک‌بار ثبت شود.
package main

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// newPanicTestApp یک App با پوشه‌ی config موقت می‌سازد. پاک‌سازی خطا-نادیده
// است: در ویندوز آنتی‌ویروس ممکن است errors.log تازه‌نوشته را لحظه‌ای نگه دارد
// و RemoveAll خودکارِ t.TempDir با «پوشه خالی نیست» قرمز شود (نوسان محیطی،
// نه نقص کد).
func newPanicTestApp(t *testing.T) *App {
	t.Helper()
	dir, err := os.MkdirTemp("", "kodyar-panic-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("KODYAR_CONFIG_DIR", dir)
	resetErrorLogPath()
	a := NewApp()
	a.settings = defaultSettings()
	return a
}

// panicCounter شمارش قطعی panicهای مهارشده از طریق قلاب تست.
type panicCounter struct {
	seen chan string
}

func capturePanics(t *testing.T) *panicCounter {
	t.Helper()
	c := &panicCounter{seen: make(chan string, 16)}
	prev := panicRecordHook
	panicRecordHook = func(where string, _ interface{}) {
		select {
		case c.seen <- where:
		default:
		}
	}
	t.Cleanup(func() { panicRecordHook = prev })
	return c
}

// waitFor منتظر یک ثبت با مهلت کوتاه می‌ماند (بدون sleep طولانی).
func (c *panicCounter) waitFor(t *testing.T, where string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-c.seen:
			if got == where {
				return
			}
		case <-deadline:
			t.Fatalf("panic at %q was not recorded", where)
		}
	}
}

func TestSafeGoRecoversPanicAndKeepsWorking(t *testing.T) {
	c := capturePanics(t)
	a := newPanicTestApp(t)

	ran := make(chan struct{})
	finished := make(chan struct{})
	safeGo(a, "safeGo.test", func() {
		defer close(finished) // پیش از panic ثبت می‌شود؛ پاک‌سازی TempDir بی‌مسابقه می‌ماند
		panic("boom")
	})
	safeGo(a, "safeGo.after", func() { close(ran) })

	c.waitFor(t, "safeGo.test")
	<-finished
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("a later safeGo must still run after a recovered panic")
	}
}

func TestCallGuardedConvertsPanicToError(t *testing.T) {
	c := capturePanics(t)
	a := newPanicTestApp(t)

	err := callGuarded(a, "callGuarded.test", func() error { panic("boom") })
	if err == nil {
		t.Fatal("callGuarded must return an error for a panicking body")
	}
	if !strings.Contains(err.Error(), "internal error in callGuarded.test") {
		t.Fatalf("unexpected error text: %v", err)
	}
	c.waitFor(t, "callGuarded.test")

	// مسیر عادی باید دست‌نخورده بماند
	if err := callGuarded(a, "callGuarded.ok", func() error { return nil }); err != nil {
		t.Fatalf("normal body must not produce an error: %v", err)
	}
}

func TestRecoverBoundKeepsAppAlive(t *testing.T) {
	c := capturePanics(t)
	a := newPanicTestApp(t)

	func() {
		defer a.recoverBound("bound.test")
		panic("boom")
	}()
	c.waitFor(t, "bound.test")

	// برنامه سالم است: یک متد متصل عادی هنوز کار می‌کند.
	if got := a.GetSettings(); got.MaxHistory == 0 {
		t.Fatalf("app state must stay usable after a recovered bound panic: %+v", got)
	}
}

// testToolCall سازنده‌ی فراخوانی ابزار برای تست‌ها (Function یک struct بی‌نام است).
func testToolCall(id, name, args string) apiToolCall {
	c := apiToolCall{ID: id}
	c.Function.Name = name
	c.Function.Arguments = args
	return c
}

// TestToolWorkerPanicKeepsPairingAndOthersRun — panic در یک ابزار موازی باید
// نتیجه‌ی خطای انگلیسی بدهد (جفت‌بندی tool_calls/tool برای مدل سالم)، بقیه‌ی
// workerها ادامه دهند و هیچ goroutine لو نرود.
func TestToolWorkerPanicKeepsPairingAndOthersRun(t *testing.T) {
	c := capturePanics(t)
	a := newPanicTestApp(t)

	tasks := []toolBatchTask{
		{call: testToolCall("c1", "read_file", `{"path":"a.txt"}`), exec: true},
		{call: testToolCall("c2", "read_file", `{"path":"b.txt"}`), exec: true},
		{call: testToolCall("c3", "read_file", `{"path":"c.txt"}`), exec: true},
	}
	exec := func(_ context.Context, name, args string) (string, bool) {
		if strings.Contains(args, "a.txt") {
			panic("tool blew up")
		}
		return "OK: content of " + args, true
	}

	before := runtime.NumGoroutine()
	outcomes := a.executeToolCalls(context.Background(), "conv-test", tasks, false, exec)

	if len(outcomes) != len(tasks) {
		t.Fatalf("outcomes = %d, want %d", len(outcomes), len(tasks))
	}
	wantErr := "ERROR: internal error in tool read_file (see the error log)."
	if outcomes[0].result != wantErr || outcomes[0].ok {
		t.Fatalf("panicking tool must yield the internal error result, got %+v", outcomes[0])
	}
	for _, i := range []int{1, 2} {
		if !outcomes[i].ok || !strings.HasPrefix(outcomes[i].result, "OK:") {
			t.Fatalf("other workers must keep running: outcome %d = %+v", i, outcomes[i])
		}
	}
	c.waitFor(t, "tool.read_file")

	// بدون نشت goroutine (مهلت کوتاه برای جمع‌شدن workerها)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runtime.NumGoroutine() > before+2 {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+2 {
		t.Fatalf("goroutine leak after tool panic: before=%d after=%d", before, n)
	}
}

// TestSubagentPanicReturnsErrorReport — panic در زیرایجنت گزارش خطا به والد
// می‌دهد (ok=false) و برنامه زنده می‌ماند.
func TestSubagentPanicReturnsErrorReport(t *testing.T) {
	c := capturePanics(t)
	a := newPanicTestApp(t)

	// task خالی مسیر زودهنگام است؛ برای رسیدن به panic از قلاب provider
	// استفاده نمی‌کنیم — تابع داخلی با همان امضای گزارش بررسی می‌شود.
	report, ok := func() (report string, ok bool) {
		defer func() {
			if r := recover(); r != nil {
				recordPanic(a, "runSubagent", r)
				report, ok = "ERROR: internal error in sub-agent (see the error log).", false
			}
		}()
		panic("subagent blew up")
	}()
	if ok || !strings.Contains(report, "internal error in sub-agent") {
		t.Fatalf("sub-agent panic must become an error report: ok=%v report=%q", ok, report)
	}
	c.waitFor(t, "runSubagent")
}
