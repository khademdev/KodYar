// تست‌های اجرای موازیِ فراخوانی‌های مستقلِ تغییردهنده (نوشتن/ویرایش/
// جابه‌جایی/حذف) و حفظ ترتیب رویدادهای UI.
//
// پوشش:
//  1. دو فایل مستقل هم‌زمان اجرا می‌شوند (اثبات با کانال، نه زمان‌سنجی) و
//     رویدادهای UI به ترتیب اصلی و «دسته‌ای» می‌آیند: اول همه‌ی chat:tool،
//     بعد همه‌ی chat:tool_result.
//  2. دو ویرایش روی «همان فایل» هرگز هم‌پوشان نمی‌شوند (سریال و به ترتیب).
//  3. تداخل در میانه‌ی دسته، سگمنت را می‌شکند: a,b موازی، سپس a دوباره
//     تنها و پس از پایان b، سپس c.
//  4. خواندن و نوشتنِ همان فایل سد می‌شوند (خواندن تمام، بعد نوشتن).
//  5. مسیر نامعلوم/آرگومان ناقص → اجرای ترتیبی (fail-safe).
//  6. toolCallPaths برای هر ابزار و مسیرهای عادی‌شده.
package main

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// maxTracker بیشینه‌ی هم‌زمانی را بدون race نگه می‌دارد.
type maxTracker struct {
	active int32
	max    int32
}

func (m *maxTracker) enter() int32 {
	cur := atomic.AddInt32(&m.active, 1)
	for {
		old := atomic.LoadInt32(&m.max)
		if cur <= old || atomic.CompareAndSwapInt32(&m.max, old, cur) {
			break
		}
	}
	return cur
}

func (m *maxTracker) leave() { atomic.AddInt32(&m.active, -1) }

// writeExec مسیر را از آرگومان JSON استخراج می‌کند (برای لاگ/تصمیم تست).
func writeExecPath(argsJSON string) string {
	for _, p := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		if strings.Contains(argsJSON, `"`+p+`"`) {
			return p
		}
	}
	return "?"
}

// TestExecuteToolCallsParallelWritesDistinctFiles دو نوشتن مستقل باید
// هم‌پوشان باشند و رویدادها به ترتیب اصلی بیایند.
func TestExecuteToolCallsParallelWritesDistinctFiles(t *testing.T) {
	a := newTestApp(t)
	var evMu sync.Mutex
	var events []string
	a.eventEmit = func(name string, data []interface{}) {
		if name != "chat:tool" && name != "chat:tool_result" {
			return
		}
		id := ""
		if len(data) > 0 {
			if m, ok := data[0].(map[string]interface{}); ok {
				id, _ = m["callId"].(string)
			}
		}
		evMu.Lock()
		events = append(events, name+":"+id)
		evMu.Unlock()
	}

	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	var tr maxTracker
	var once1, once2 sync.Once
	exec := func(ctx context.Context, name, argsJSON string) (string, bool) {
		tr.enter()
		defer tr.leave()
		switch writeExecPath(argsJSON) {
		case "a.txt":
			once1.Do(func() { close(firstStarted) })
			select {
			case <-secondStarted:
				return "a-done", true
			case <-time.After(5 * time.Second):
				return "TIMEOUT: independent write never started", false
			}
		case "b.txt":
			once2.Do(func() { close(secondStarted) })
			select {
			case <-firstStarted:
				return "b-done", true
			case <-time.After(5 * time.Second):
				return "TIMEOUT: independent write never started", false
			}
		}
		return "other", true
	}
	tasks := []toolBatchTask{
		{call: batchCall("c1", "write_file", `{"path":"a.txt","content":"A"}`), exec: true},
		{call: batchCall("c2", "write_file", `{"path":"b.txt","content":"B"}`), exec: true},
	}
	out := a.executeToolCalls(context.Background(), "conv", tasks, false, exec)
	if !out[0].ok || !out[1].ok {
		t.Fatalf("both writes must succeed: %+v", out)
	}
	if out[0].result != "a-done" || out[1].result != "b-done" {
		t.Fatalf("outcomes must keep original order: %+v", out)
	}
	if got := atomic.LoadInt32(&tr.max); got != 2 {
		t.Fatalf("independent writes must overlap, max concurrency = %d", got)
	}

	evMu.Lock()
	got := append([]string(nil), events...)
	evMu.Unlock()
	want := []string{"chat:tool:c1", "chat:tool:c2", "chat:tool_result:c1", "chat:tool_result:c2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("UI events must be ordered (all starts in order, then results in order):\n got %v\nwant %v", got, want)
	}
}

// TestExecuteToolCallsSerializesSameFileEdits دو ویرایش روی یک فایل باید
// ترتیبی بمانند (تداخل ندارد) و ترتیب حفظ شود.
func TestExecuteToolCallsSerializesSameFileEdits(t *testing.T) {
	a := newTestApp(t)
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	var tr maxTracker
	var once1, once2 sync.Once
	exec := func(ctx context.Context, name, argsJSON string) (string, bool) {
		tr.enter()
		defer tr.leave()
		if strings.Contains(argsJSON, `"old_string":"one"`) {
			once1.Do(func() { close(firstStarted) })
			select {
			case <-secondStarted:
				return "OVERLAP: second edit of the same file started while the first was running", false
			case <-time.After(150 * time.Millisecond):
				// پنجره‌ی منفی: اگر موازی بود، دومی همین‌جا شروع می‌شد.
				return "edit-1", true
			}
		}
		once2.Do(func() { close(secondStarted) })
		return "edit-2", true
	}
	tasks := []toolBatchTask{
		{call: batchCall("c1", "edit_file", `{"path":"a.txt","old_string":"one","new_string":"1"}`), exec: true},
		{call: batchCall("c2", "edit_file", `{"path":"a.txt","old_string":"two","new_string":"2"}`), exec: true},
	}
	out := a.executeToolCalls(context.Background(), "conv", tasks, false, exec)
	if !out[0].ok || out[0].result != "edit-1" {
		t.Fatalf("first edit must finish without overlap: %+v", out[0])
	}
	if !out[1].ok || out[1].result != "edit-2" {
		t.Fatalf("second edit must run after the first: %+v", out[1])
	}
	if got := atomic.LoadInt32(&tr.max); got != 1 {
		t.Fatalf("same-file edits must never overlap, max concurrency = %d", got)
	}
}

// TestExecuteToolCallsWriteConflictSplitsSegment دسته‌ی a,b,a,c: اولی و
// دومی موازی‌اند، سومی (همان a) بعد از پایان b و به‌تنهایی، سپس c.
func TestExecuteToolCallsWriteConflictSplitsSegment(t *testing.T) {
	a := newTestApp(t)
	var mu sync.Mutex
	var order []string
	var tr maxTracker
	exec := func(ctx context.Context, name, argsJSON string) (string, bool) {
		tr.enter()
		defer tr.leave()
		p := writeExecPath(argsJSON)
		mu.Lock()
		order = append(order, "start:"+p)
		mu.Unlock()
		time.Sleep(30 * time.Millisecond) // پنجره‌ی واقعی برای هم‌پوشانی
		mu.Lock()
		order = append(order, "end:"+p)
		mu.Unlock()
		return p, true
	}
	tasks := []toolBatchTask{
		{call: batchCall("c1", "write_file", `{"path":"a.txt","content":"1"}`), exec: true},
		{call: batchCall("c2", "write_file", `{"path":"b.txt","content":"2"}`), exec: true},
		{call: batchCall("c3", "write_file", `{"path":"a.txt","content":"3"}`), exec: true},
		{call: batchCall("c4", "write_file", `{"path":"c.txt","content":"4"}`), exec: true},
	}
	out := a.executeToolCalls(context.Background(), "conv", tasks, false, exec)
	for i, o := range out {
		if !o.ok {
			t.Fatalf("call %d failed: %+v", i, o)
		}
	}
	if got := atomic.LoadInt32(&tr.max); got != 2 {
		t.Fatalf("expected exactly the independent pair to overlap, max concurrency = %d", got)
	}
	mu.Lock()
	log := strings.Join(order, ",")
	mu.Unlock()
	// سومی باید بعد از پایان دومی شروع شده باشد (تداخل همان فایل سریال).
	iEndB := strings.Index(log, "end:b.txt")
	iStartA3 := strings.LastIndex(log, "start:a.txt")
	if iEndB < 0 || iStartA3 < 0 || iStartA3 < iEndB {
		t.Fatalf("conflicting write must start after the earlier write on the same path finished:\n%s", log)
	}
}

// TestExecuteToolCallsReadThenWriteSameFile خواندن و نوشتنِ همان فایل باید
// سد شوند: نوشتن پس از پایان خواندن.
func TestExecuteToolCallsReadThenWriteSameFile(t *testing.T) {
	a := newTestApp(t)
	var tr maxTracker
	var mu sync.Mutex
	var order []string
	exec := func(ctx context.Context, name, argsJSON string) (string, bool) {
		tr.enter()
		defer tr.leave()
		mu.Lock()
		order = append(order, "start:"+name)
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		order = append(order, "end:"+name)
		mu.Unlock()
		return name, true
	}
	tasks := []toolBatchTask{
		{call: batchCall("c1", "read_file", `{"path":"a.txt"}`), exec: true},
		{call: batchCall("c2", "write_file", `{"path":"a.txt","content":"x"}`), exec: true},
	}
	out := a.executeToolCalls(context.Background(), "conv", tasks, false, exec)
	if !out[0].ok || !out[1].ok {
		t.Fatalf("read+write must succeed: %+v", out)
	}
	if got := atomic.LoadInt32(&tr.max); got != 1 {
		t.Fatalf("read and write of the same file must not overlap, max = %d", got)
	}
	mu.Lock()
	log := strings.Join(order, ",")
	mu.Unlock()
	if log != "start:read_file,end:read_file,start:write_file,end:write_file" {
		t.Fatalf("read must finish before the write starts, got: %s", log)
	}
}

// TestExecuteToolCallsUnknownPathStaysSequential آرگومان ناقص (بدون path)
// نباید به سگمنت موازی راه پیدا کند.
func TestExecuteToolCallsUnknownPathStaysSequential(t *testing.T) {
	a := newTestApp(t)
	var tr maxTracker
	var mu sync.Mutex
	var order []string
	exec := func(ctx context.Context, name, argsJSON string) (string, bool) {
		tr.enter()
		defer tr.leave()
		mu.Lock()
		order = append(order, argsJSON)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return "ok", true
	}
	tasks := []toolBatchTask{
		{call: batchCall("c1", "write_file", `{"content":"no path"}`), exec: true},
		{call: batchCall("c2", "write_file", `{"path":"b.txt","content":"B"}`), exec: true},
	}
	out := a.executeToolCalls(context.Background(), "conv", tasks, false, exec)
	if !out[0].ok || !out[1].ok {
		t.Fatalf("calls must run (sequentially): %+v", out)
	}
	if got := atomic.LoadInt32(&tr.max); got != 1 {
		t.Fatalf("unknown path must force sequential execution, max = %d", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != `{"content":"no path"}` {
		t.Fatalf("original order must be preserved: %v", order)
	}
}

// TestToolCallPaths استخراج/نرمال‌سازی مسیرها.
func TestToolCallPaths(t *testing.T) {
	cases := []struct {
		call   apiToolCall
		want   []string
		wantOK bool
	}{
		{batchCall("1", "write_file", `{"path":"A.TXT"}`), []string{"a.txt"}, true},
		{batchCall("2", "edit_file", `{"path":"src\\main.go"}`), []string{"src/main.go"}, true},
		{batchCall("3", "delete_path", `{"path":"./tmp/x"}`), []string{"tmp/x"}, true},
		{batchCall("4", "move_path", `{"from":"a.txt","to":"b.txt"}`), []string{"a.txt", "b.txt"}, true},
		{batchCall("5", "write_file", `{"content":"x"}`), nil, false},
		{batchCall("6", "move_path", `{"from":"a.txt"}`), nil, false},
		{batchCall("7", "write_file", `not json`), nil, false},
		{batchCall("8", "list_dir", `{"path":"."}`), nil, false},
	}
	for _, c := range cases {
		got, ok := toolCallPaths(c.call)
		if ok != c.wantOK {
			t.Errorf("%s ok = %v, want %v", c.call.ID, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s paths = %v, want %v", c.call.ID, got, c.want)
		}
	}
	if !pathsOverlap(map[string]bool{"a.txt": true}, []string{"b.txt", "a.txt"}) {
		t.Error("pathsOverlap must detect the shared path")
	}
	if pathsOverlap(map[string]bool{"a.txt": true}, []string{"b.txt"}) {
		t.Error("pathsOverlap must not report unrelated paths")
	}
}
