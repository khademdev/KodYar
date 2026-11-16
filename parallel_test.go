// تست‌های سقف پویای ابزارهای موازی + اندازه‌گیری بهره‌ی سرعت (گام ۴).
package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// withParallelLimit سقف را موقتاً برای تست عوض می‌کند.
func withParallelLimit(t *testing.T, n int) {
	t.Helper()
	old := parallelToolLimitFn
	parallelToolLimitFn = func() int { return n }
	t.Cleanup(func() { parallelToolLimitFn = old })
}

// runReadBatch دسته‌ای از read_fileهای کندِ مصنوعی را اجرا و زمان می‌گیرد.
func runReadBatch(t *testing.T, a *App, n int, perTool time.Duration) time.Duration {
	t.Helper()
	exec := func(ctx context.Context, name, argsJSON string) (string, bool) {
		time.Sleep(perTool)
		return "ok" + argsJSON, true
	}
	tasks := make([]toolBatchTask, n)
	for i := range tasks {
		tasks[i] = toolBatchTask{
			call: batchCall(fmt.Sprintf("c%d", i), "read_file", fmt.Sprintf(`"f%d"`, i)),
			exec: true,
		}
	}
	start := time.Now()
	out := a.executeToolCalls(context.Background(), "conv", tasks, false, exec)
	elapsed := time.Since(start)
	if len(out) != n {
		t.Fatalf("want %d outcomes, got %d", n, len(out))
	}
	return elapsed
}

// TestParallelReadsSpeedup عدد «قبل/بعد» را می‌سازد: همان ۸ خواندن با سقف ۱
// (معادل اجرای ترتیبی) در مقابل سقف پویا.
func TestParallelReadsSpeedup(t *testing.T) {
	a := newTestApp(t)
	const n = 8
	const perTool = 40 * time.Millisecond

	withParallelLimit(t, 1)
	serial := runReadBatch(t, a, n, perTool)

	parallelToolLimitFn = parallelToolLimit // سقف واقعی ماشین
	par := runReadBatch(t, a, n, perTool)

	t.Logf("8 reads × %dms: serial(limit=1)=%dms parallel(limit=%d)=%dms speedup=%.1fx",
		perTool.Milliseconds(), serial.Milliseconds(), parallelToolLimit(), par.Milliseconds(),
		float64(serial)/float64(par))
	if par >= serial/2 {
		t.Fatalf("parallel batch must be at least 2x faster: serial=%v parallel=%v", serial, par)
	}
}

// TestParallelLimitWithinBounds سقف پویا هرگز زیر ۴ یا بالای ۸ نیست.
func TestParallelLimitWithinBounds(t *testing.T) {
	if got := parallelToolLimit(); got < 4 || got > 8 {
		t.Fatalf("parallelToolLimit must stay in [4,8], got %d", got)
	}
}

// TestParallelOrderPreserved رویدادها/خروجی‌ها با سقف بزرگ‌تر هم به ترتیب
// اصلی می‌مانند (جفت‌شدن tool_calls/tool نباید به هم بریزد).
func TestParallelOrderPreserved(t *testing.T) {
	a := newTestApp(t)
	var mu sync.Mutex
	var seen []string
	exec := func(ctx context.Context, name, argsJSON string) (string, bool) {
		mu.Lock()
		seen = append(seen, argsJSON)
		mu.Unlock()
		return argsJSON, true
	}
	const n = 6
	tasks := make([]toolBatchTask, n)
	for i := range tasks {
		tasks[i] = toolBatchTask{call: batchCall(fmt.Sprintf("c%d", i), "read_file", fmt.Sprintf(`"p%d"`, i)), exec: true}
	}
	out := a.executeToolCalls(context.Background(), "conv", tasks, false, exec)
	for i, o := range out {
		if o.result != fmt.Sprintf(`"p%d"`, i) {
			t.Fatalf("outcome %d mapped to wrong call: %+v", i, o)
		}
	}
	if len(seen) != n {
		t.Fatalf("exec must run every task once, got %d", len(seen))
	}
}
