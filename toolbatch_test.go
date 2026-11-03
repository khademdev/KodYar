// تست‌های — اجرای موازی ابزارهای فقط‌خواندنی.
//
// پوشش:
// 1. planToolCalls: تصمیم‌های محافظ تکرار عیناً مثل اجرای ترتیبی —
// سومین تکرار یکسان مسدود، پنجمین توقف نوبت، پس از توقف بقیه‌ی دسته
// فقط خطای توقف (و دیگر به محافظ گزارش نمی‌شوند).
// 2. هم‌زمانی قطعی: در یک سگمنت فقط‌خواندنی، فراخوانی دوم «پیش از پایان»
// فراخوانی اول شروع می‌شود — اثبات با کانال، نه با زمان‌سنجی.
// 3. سقف worker دقیقاً ۴: با ۸ task، بیشینه‌ی هم‌زمانی دقیقاً ۴ است
// (اثبات قطعی با کانالِ «چهارمین task وارد شد»).
// 4. سدهای سگمنت: ابزار تغییردهنده فقط پس از پایان خواندن‌های قبلش و
// پیش از شروع خواندن‌های بعدیش اجرا می‌شود (اثبات با ترتیب لاگ).
// 5. لغو میان‌کار: همه‌ی outcomeها حاضرند (جفت‌بندی نمی‌شکند)، taskهای
// صف‌شده «قبل از اجرا لغو شد» می‌گیرند و executor برایشان صدا نمی‌شود،
// و تابع برمی‌گردد (join همه‌ی workerها).
// 6. سریال‌سازی مودال مجوز: دو ask هم‌زمان هرگز هم‌پوشان نمی‌شوند.
// 7. استریم واقعی: خواندن حساسِ (کند، منتظر مودال) اولِ دسته — نتایج
// به ترتیب اصلی روی دیسک؛ دو خواندن حساس موازی → askها پشت‌سرهم؛
// لغو کاربر میان دسته → ذخیره‌ی partial سالم؛ محافظ تکرار روی دسته‌ی
// موازی مثل قبل.
//
// همه‌ی تست‌ها قطعی‌اند: هیچ شبکه‌ی واقعی، هیچ خواب طولانی (پنجره‌های
// منفیِ ۱۲۰–۱۵۰ms فقط برای اثبات عدمِ هم‌پوشانی) و سرورهای httptest محلی.
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------- کمک‌های تست

// batchCall فراخوانی ابزار آزمایشی می‌سازد (برای دسته‌های plan/execute).
func batchCall(id, name, args string) apiToolCall {
	var c apiToolCall
	c.ID = id
	c.Type = "function"
	c.Function.Name = name
	c.Function.Arguments = args
	return c
}

// countPendingAsks تعداد askهای فعال (زیر changes.mu).
func countPendingAsks(a *App) int {
	a.changes.mu.Lock()
	defer a.changes.mu.Unlock()
	return len(a.changes.permAsks)
}

// waitAskPresent تا ثبت ask با شناسه‌ی داده‌شده صبر می‌کند.
func waitAskPresent(t *testing.T, a *App, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.changes.mu.Lock()
		_, ok := a.changes.permAsks[id]
		a.changes.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("ask %q never appeared", id)
}

// waitPendingAskID تا ثبت یک ask با شناسه‌ای «غیر از» notID صبر می‌کند و
// شناسه‌اش را برمی‌گرداند (پنجره‌ی حذفِ ask قبلی را بی‌خطر رد می‌کند).
func waitPendingAskID(t *testing.T, a *App, notID string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.changes.mu.Lock()
		var id string
		for k := range a.changes.permAsks {
			if k != notID {
				id = k
			}
		}
		a.changes.mu.Unlock()
		if id != "" {
			return id
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no new pending permission ask appeared")
	return ""
}

// sseToolCallsBatch یک پاسخ استریم با چند فراخوانی ابزار «در یک پیام
// دستیار» می‌فرستد — هر فراخوانی یک chunk با index خودش (مثل رفتار
// providerهای واقعی).
func sseToolCallsBatch(w http.ResponseWriter, calls []apiToolCall, finish string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for i, c := range calls {
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":%d,\"id\":%q,\"function\":{\"name\":%q,\"arguments\":%q}}]},\"finish_reason\":null}]}\n\n",
			i, c.ID, c.Function.Name, c.Function.Arguments)
	}
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":%q}]}\n\n", finish)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// ---------------------------------------------------------------- ۱. تصمیم‌های محافظ

func TestPlanToolCallsGuardDecisions(t *testing.T) {
	// شش فراخوانی: پنج تکرار یکسان + یکی متفاوت — مثل اجرای ترتیبی:
	// دو اجرا، مسدود (۳)، مسدود (۴)، توقف (۵)، بعدی‌ها فقط خطای توقف.
	calls := []apiToolCall{
		batchCall("c1", "read_file", `{"path":"a.txt"}`),
		batchCall("c2", "read_file", `{"path":"a.txt"}`),
		batchCall("c3", "read_file", `{"path":"a.txt"}`),
		batchCall("c4", "read_file", `{"path":"a.txt"}`),
		batchCall("c5", "read_file", `{"path":"a.txt"}`),
		batchCall("c6", "list_dir", `{}`),
	}
	guard := &toolLoopGuard{}
	tasks, stopped, stoppedTool := planToolCalls(calls, guard)
	if !stopped || stoppedTool != "read_file" {
		t.Fatalf("want turn stopped by read_file, got stopped=%v tool=%q", stopped, stoppedTool)
	}
	wantExec := []bool{true, true, false, false, false, false}
	for i, we := range wantExec {
		if tasks[i].exec != we {
			t.Fatalf("task %d exec=%v, want %v", i, tasks[i].exec, we)
		}
	}
	if !strings.Contains(tasks[2].result, "identical repeated tool call blocked") || tasks[2].ok {
		t.Fatalf("task 2 (3rd identical) must be blocked, got %q", tasks[2].result)
	}
	if tasks[4].result != toolTurnStoppedError || tasks[5].result != toolTurnStoppedError {
		t.Fatalf("tasks 4 and 5 must carry the turn-stopped error, got %q / %q", tasks[4].result, tasks[5].result)
	}
	// فراخوانیِ بعد از توقف به محافظ گزارش نمی‌شود (رفتار قبلی): ادامه‌ی
	// شمارشِ همان امضا باید ۶ بدهد، نه اینکه با list_dir از نو شروع شود.
	if got := guard.observe("read_file", `{"path":"a.txt"}`); got != 6 {
		t.Fatalf("post-stop call must not have been observed; continuing must give run=6, got %d", got)
	}
}

// ---------------------------------------------------------------- ۲. هم‌زمانی قطعی

func TestExecuteToolCallsReadOnlyOverlap(t *testing.T) {
	a := newTestApp(t)
	secondStarted := make(chan struct{})
	exec := func(ctx context.Context, name, argsJSON string) (string, bool) {
		switch name {
		case "read_file": // اول — «کند»: تا شروع شدنِ دومی منتظر می‌ماند
			select {
			case <-secondStarted:
				return "first-done", true
			case <-time.After(5 * time.Second):
				// اگر ترتیبی اجرا می‌شد، دومی هرگز شروع نمی‌شد و اینجا می‌ایستاد
				return "TIMEOUT: second call never started — tools ran sequentially", false
			}
		default: // list_dir — دوم
			close(secondStarted)
			return "second-done", true
		}
	}
	tasks := []toolBatchTask{
		{call: batchCall("c1", "read_file", `{}`), exec: true},
		{call: batchCall("c2", "list_dir", `{}`), exec: true},
	}
	outcomes := a.executeToolCalls(context.Background(), "conv", tasks, false, exec)
	if !outcomes[0].ok || outcomes[0].result != "first-done" {
		t.Fatalf("first call must finish after the second started: %+v", outcomes[0])
	}
	if !outcomes[1].ok || outcomes[1].result != "second-done" {
		t.Fatalf("second call must succeed: %+v", outcomes[1])
	}
	// خروجی همیشه به ترتیب اصلی (ایندکس ۰ = فراخوانی اول)
	if outcomes[0].result != "first-done" || outcomes[1].result != "second-done" {
		t.Fatalf("outcomes must be indexed in original call order: %+v", outcomes)
	}
}

// ---------------------------------------------------------------- ۳. سقف worker

func TestExecuteToolCallsWorkerLimit(t *testing.T) {
	a := newTestApp(t)
	const n = 8 // دو برابر سقف — موج دوم باید صف شود
	var active, maxActive int32
	var once sync.Once
	reached4 := make(chan struct{})
	release := make(chan struct{})
	exec := func(ctx context.Context, name, argsJSON string) (string, bool) {
		cur := atomic.AddInt32(&active, 1)
		for { // maxActive = max(maxActive, cur) بدون race
			m := atomic.LoadInt32(&maxActive)
			if cur <= m || atomic.CompareAndSwapInt32(&maxActive, m, cur) {
				break
			}
		}
		if cur == parallelToolWorkers {
			once.Do(func() { close(reached4) }) // چهار task هم‌زمان داخل اجرا
		}
		<-release
		atomic.AddInt32(&active, -1)
		return "done-" + argsJSON, true
	}
	tasks := make([]toolBatchTask, n)
	for i := range tasks {
		tasks[i] = toolBatchTask{call: batchCall(fmt.Sprintf("c%d", i), "read_file", fmt.Sprintf(`{"i":%d}`, i)), exec: true}
	}
	done := make(chan []toolCallOutcome, 1)
	go func() { done <- a.executeToolCalls(context.Background(), "conv", tasks, false, exec) }()
	select {
	case <-reached4: // هم‌زمانی اثبات شد (دقیقاً ۴ داخل اجرا)
	case <-time.After(5 * time.Second):
		t.Fatal("4 concurrent workers never started — tools ran with a lower limit or sequentially")
	}
	close(release)
	outcomes := <-done
	// سقف اثبات شد: هیچ‌گاه بیش از ۴ task هم‌زمان داخل executor نبوده
	if got := atomic.LoadInt32(&maxActive); got != parallelToolWorkers {
		t.Fatalf("max concurrent workers = %d, want exactly %d", got, parallelToolWorkers)
	}
	if len(outcomes) != n {
		t.Fatalf("want %d outcomes, got %d", n, len(outcomes))
	}
	for i, o := range outcomes {
		if !o.ok || o.result != fmt.Sprintf("done-{\"i\":%d}", i) {
			t.Fatalf("outcome %d must map to its own call: %+v", i, o)
		}
	}
}

// ---------------------------------------------------------------- ۴. سدهای سگمنت

func TestExecuteToolCallsMixedSegmentsBarrier(t *testing.T) {
	a := newTestApp(t)
	var mu sync.Mutex
	var log []string
	rec := func(s string) { mu.Lock(); log = append(log, s); mu.Unlock() }
	idxOf := func(ev string) int {
		mu.Lock()
		defer mu.Unlock()
		for i, s := range log {
			if s == ev {
				return i
			}
		}
		return -1
	}
	secondStarted := make(chan struct{})
	exec := func(ctx context.Context, name, argsJSON string) (string, bool) {
		rec("start:" + argsJSON)
		switch argsJSON {
		case `"S1"`: // خواندن اول — تا شروع دومی می‌ایستد (اثبات هم‌پوشانی سگمنت)
			select {
			case <-secondStarted:
			case <-time.After(5 * time.Second):
				rec("TIMEOUT")
				return "TIMEOUT", false
			}
		case `"S2"`:
			close(secondStarted)
		}
		rec("end:" + argsJSON)
		return argsJSON, true
	}
	tasks := []toolBatchTask{
		{call: batchCall("c1", "read_file", `"S1"`), exec: true},
		{call: batchCall("c2", "list_dir", `"S2"`), exec: true},
		{call: batchCall("c3", "write_file", `"W"`), exec: true}, // تغییردهنده — سد
		{call: batchCall("c4", "read_file", `"S3"`), exec: true},
	}
	outcomes := a.executeToolCalls(context.Background(), "conv", tasks, false, exec)
	if len(outcomes) != 4 {
		t.Fatalf("want 4 outcomes, got %d", len(outcomes))
	}
	for i, o := range outcomes {
		if !o.ok {
			t.Fatalf("outcome %d failed: %+v (log=%v)", i, o, log)
		}
	}
	// هم‌پوشانی داخل سگمنت فقط‌خواندنی: S2 قبل از پایان S1 شروع شده
	if !(idxOf("start:\"S2\"") < idxOf("end:\"S1\"")) {
		t.Fatalf("read-only segment must overlap: %v", log)
	}
	// سد تغییردهنده: هر دو خواندنِ قبلش «تمام شده‌اند» قبل از شروعش
	if !(idxOf("end:\"S1\"") < idxOf("start:\"W\"")) || !(idxOf("end:\"S2\"") < idxOf("start:\"W\"")) {
		t.Fatalf("mutating call must start only after prior reads finished: %v", log)
	}
	// و خواندنِ بعدی فقط پس از پایان تغییردهنده
	if !(idxOf("end:\"W\"") < idxOf("start:\"S3\"")) {
		t.Fatalf("read after a mutating call must wait for it: %v", log)
	}

	// سناریوی دوم: تغییردهنده در ابتدای دسته — تنها و اول.
	log = nil
	tasks2 := []toolBatchTask{
		{call: batchCall("c1", "edit_file", `"W2"`), exec: true},
		{call: batchCall("c2", "read_file", `"S4"`), exec: true},
	}
	a.executeToolCalls(context.Background(), "conv", tasks2, false, exec)
	if !(idxOf("end:\"W2\"") < idxOf("start:\"S4\"")) {
		t.Fatalf("mutating-first batch must run the mutation alone first: %v", log)
	}
}

// ---------------------------------------------------------------- ۵. لغو میان‌کار

func TestExecuteToolCallsCancelMidFlight(t *testing.T) {
	a := newTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const n = 6 // سقف ۴ → دو task در صف می‌مانند
	var mu sync.Mutex
	started := map[string]bool{}
	exec := func(ctx context.Context, name, argsJSON string) (string, bool) {
		mu.Lock()
		started[argsJSON] = true
		mu.Unlock()
		select {
		case <-ctx.Done(): // در پرواز بود — با لغو برمی‌گردد
			return "in-flight cancelled", false
		case <-time.After(5 * time.Second):
			return "TIMEOUT", false
		}
	}
	tasks := make([]toolBatchTask, n)
	for i := range tasks {
		tasks[i] = toolBatchTask{call: batchCall(fmt.Sprintf("c%d", i), "read_file", fmt.Sprintf(`"t%d"`, i)), exec: true}
	}
	done := make(chan []toolCallOutcome, 1)
	go func() { done <- a.executeToolCalls(ctx, "conv", tasks, false, exec) }()
	// صبر تا دقیقاً ۴ task اول داخل اجرا باشند، بعد لغو کاربر
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		s := len(started)
		mu.Unlock()
		if s == parallelToolWorkers {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	outcomes := <-done // join همه‌ی workerها — تابع قطعاً برمی‌گردد
	if len(outcomes) != n {
		t.Fatalf("every call must produce an outcome (pairing!), want %d got %d", n, len(outcomes))
	}
	cancelledBefore := 0
	for i, o := range outcomes {
		if o.result == "" {
			t.Fatalf("outcome %d empty — pairing would break", i)
		}
		if strings.Contains(o.result, "cancelled by the user before this tool ran") {
			cancelledBefore++
		}
	}
	if cancelledBefore != n-parallelToolWorkers {
		t.Fatalf("queued tasks must be cancelled before running, want %d got %d", n-parallelToolWorkers, cancelledBefore)
	}
	// برای taskهای صف‌شده executor هرگز صدا نشده است
	mu.Lock()
	s := len(started)
	mu.Unlock()
	if s != parallelToolWorkers {
		t.Fatalf("executor must not run for queued tasks, got %d exec calls", s)
	}
}

// ---------------------------------------------------------------- ۶. سریال‌سازی ask

func TestPermissionAsksSerialized(t *testing.T) {
	a := newTestApp(t)
	a.ctx = context.Background() // فعال‌سازی مسیر ask (emit واقعی نیست — uiReady=false)
	ask1 := &permissionAsk{ID: "perm-t1", Kind: "sensitive_read", Message: "م", At: timeNow()}
	ask2 := &permissionAsk{ID: "perm-t2", Kind: "sensitive_read", Message: "م", At: timeNow()}
	res1 := make(chan string, 1)
	res2 := make(chan string, 1)
	go func() { res1 <- a.requestPermissionConfirm(context.Background(), ask1) }()
	go func() { res2 <- a.requestPermissionConfirm(context.Background(), ask2) }()

	// اولین ask ثبت شده (کدام گوروتین اول برسد قطعی نیست) — دومی پشت
	// permAskMu صف است. اگر سریال‌سازی نبود، هر دو فوراً ثبت می‌شدند.
	first := waitPendingAskID(t, a, "")
	var firstRes, secondRes chan string
	var secondID string
	if first == "perm-t1" {
		firstRes, secondRes, secondID = res1, res2, "perm-t2"
	} else {
		firstRes, secondRes, secondID = res2, res1, "perm-t1"
	}
	if got := countPendingAsks(a); got != 1 {
		t.Fatalf("exactly one ask must be pending, got %d", got)
	}
	// پنجره‌ی منفی: تا ask اول باز است، دومی هرگز ثبت نمی‌شود
	time.Sleep(150 * time.Millisecond)
	if got := countPendingAsks(a); got != 1 {
		t.Fatalf("second ask leaked past the serializer (pending=%d)", got)
	}
	if err := a.RespondPermissionConfirm(first, "allow"); err != nil {
		t.Fatal(err)
	}
	if got := <-firstRes; got != permRespondAllow {
		t.Fatalf("first ask result = %q, want allow", got)
	}
	// حالا نوبت دومی است — نه هم‌زمان با اولی
	waitAskPresent(t, a, secondID)
	if err := a.RespondPermissionConfirm(secondID, "reject"); err != nil {
		t.Fatal(err)
	}
	if got := <-secondRes; got != permRespondReject {
		t.Fatalf("second ask result = %q, want reject", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && countPendingAsks(a) != 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := countPendingAsks(a); got != 0 {
		t.Fatalf("asks must be cleaned up, %d left", got)
	}
}

// ---------------------------------------------------------------- ۷. استریم واقعی

// TestStreamChatParallelBatchOriginalOrder: خواندنِ حساسِ اول (روی مودال
// می‌ایستاد = «کند») و خواندن عادی دوم — نتایج باید به ترتیب اصلیِ
// فراخوانی‌ها روی دیسک بنشینند و جفت‌بندی سالم بماند.
func TestStreamChatParallelBatchOriginalOrder(t *testing.T) {
	redirectToTestLog(t)
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&reqs, 1) == 1 {
			sseToolCallsBatch(w, []apiToolCall{
				batchCall("call-env", "read_file", `{"path":".env"}`),        // اول — منتظر مودال
				batchCall("call-notes", "read_file", `{"path":"notes.txt"}`), // دوم — فوری
			}, "tool_calls")
			return
		}
		sseFinalWithUsage(w, "انجام شد", 10, 5)
	}))
	defer srv.Close()

	a := agentStreamApp(t, srv.URL)
	ws := a.settings.WorkspacePath
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("hello-notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.ctx = context.Background() // مسیر ask فعال
	conv, err := a.SendMessage("", "این دو فایل را بخوان", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)

	// ask فایل حساس ظاهر شده و باز است؛ پاسخ می‌دهیم (تا این لحظه خواندن
	// عادی آزادانه تمام شده — هم‌زمانی در TestExecuteToolCallsReadOnlyOverlap
	// به‌صورت قطعی اثبات شده).
	id := waitPendingAskID(t, a, "")
	if err := a.RespondPermissionConfirm(id, "allow"); err != nil {
		t.Fatal(err)
	}

	loaded := waitConversationUntil(t, a, conv.ID, hasAssistantWith("انجام شد"))
	if got := atomic.LoadInt32(&reqs); got != 2 {
		t.Fatalf("want 2 rounds, got %d", got)
	}
	checkToolPairing(t, loaded)
	envIdx, notesIdx := -1, -1
	for i, m := range loaded.Messages {
		if m.Role != "tool" {
			continue
		}
		switch m.ToolCallID {
		case "call-env":
			envIdx = i
		case "call-notes":
			notesIdx = i
		}
	}
	if envIdx < 0 || notesIdx < 0 || envIdx > notesIdx {
		t.Fatalf("tool results must persist in ORIGINAL call order (slow first): env=%d notes=%d", envIdx, notesIdx)
	}
	if !strings.Contains(loaded.Messages[envIdx].Content, "SECRET=abc") {
		t.Fatalf("approved sensitive read missing content: %q", loaded.Messages[envIdx].Content)
	}
	if !strings.Contains(loaded.Messages[notesIdx].Content, "hello-notes") {
		t.Fatalf("normal read missing content: %q", loaded.Messages[notesIdx].Content)
	}
}

// TestStreamChatParallelSensitiveAsksSerialized: دو خواندن حساس در یک دسته‌ی
// موازی — askها پشت‌سرهم ثبت می‌شوند (هرگز هم‌پوشان نه) و هر دو خواندن
// پس از اجازه، به ترتیب اصلی ذخیره می‌شوند.
func TestStreamChatParallelSensitiveAsksSerialized(t *testing.T) {
	redirectToTestLog(t)
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&reqs, 1) == 1 {
			sseToolCallsBatch(w, []apiToolCall{
				batchCall("call-a", "read_file", `{"path":".env"}`),
				batchCall("call-b", "read_file", `{"path":".env.local"}`),
			}, "tool_calls")
			return
		}
		sseFinalWithUsage(w, "هر دو خوانده شد", 10, 5)
	}))
	defer srv.Close()

	a := agentStreamApp(t, srv.URL)
	ws := a.settings.WorkspacePath
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("ROOT=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".env.local"), []byte("LOCAL=2"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.ctx = context.Background()
	conv, err := a.SendMessage("", "دو فایل محیطی را بخوان", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)

	id1 := waitPendingAskID(t, a, "")
	// پنجره‌ی منفی: تا وقتی ask اول باز است، دومی ثبت نمی‌شود (سریال‌سازی)
	time.Sleep(120 * time.Millisecond)
	if got := countPendingAsks(a); got != 1 {
		t.Fatalf("sensitive asks overlapped in a parallel batch (pending=%d)", got)
	}
	if err := a.RespondPermissionConfirm(id1, "allow"); err != nil {
		t.Fatal(err)
	}
	id2 := waitPendingAskID(t, a, id1)
	if err := a.RespondPermissionConfirm(id2, "allow"); err != nil {
		t.Fatal(err)
	}

	loaded := waitConversationUntil(t, a, conv.ID, hasAssistantWith("هر دو خوانده شد"))
	checkToolPairing(t, loaded)
	aIdx, bIdx := -1, -1
	for i, m := range loaded.Messages {
		if m.Role != "tool" {
			continue
		}
		switch m.ToolCallID {
		case "call-a":
			aIdx = i
		case "call-b":
			bIdx = i
		}
	}
	if aIdx < 0 || bIdx < 0 || aIdx > bIdx {
		t.Fatalf("results must be in original order: a=%d b=%d", aIdx, bIdx)
	}
	if !strings.Contains(loaded.Messages[aIdx].Content, "ROOT=1") || !strings.Contains(loaded.Messages[bIdx].Content, "LOCAL=2") {
		t.Fatalf("both approved reads must carry their content: %q / %q",
			loaded.Messages[aIdx].Content, loaded.Messages[bIdx].Content)
	}
}

// TestStreamChatParallelCancelDuringBatch: لغو کاربر وقتی دسته‌ی موازی روی
// مودالِ خواندن حساس ایستاده — ask رد می‌شود، همه‌ی فراخوانی‌ها نتیجه
// می‌گیرند، جفت‌بندی سالم می‌ماند و پیام نهایی partial با Aborted ذخیره
// می‌شود؛ گوروتین استریم کامل خارج می‌شود.
func TestStreamChatParallelCancelDuringBatch(t *testing.T) {
	redirectToTestLog(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseToolCallsBatch(w, []apiToolCall{
			batchCall("call-env", "read_file", `{"path":".env"}`),
			batchCall("call-notes", "read_file", `{"path":"notes.txt"}`),
		}, "tool_calls")
	}))
	defer srv.Close()

	a := agentStreamApp(t, srv.URL)
	ws := a.settings.WorkspacePath
	if err := os.WriteFile(filepath.Join(ws, ".env"), []byte("SECRET=zzz"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.ctx = context.Background()
	conv, err := a.SendMessage("", "بخوان و بعداً ادامه بده", nil)
	if err != nil {
		t.Fatal(err)
	}

	// ask باز است → دسته هنوز در جریان است؛ لغو کاربر
	waitPendingAskID(t, a, "")
	a.StopGeneration()
	waitStreamDone(t, a)

	loaded := waitConversationUntil(t, a, conv.ID, func(c *Conversation) bool {
		for _, m := range c.Messages {
			if m.Role == "assistant" && len(m.ToolCalls) == 0 && m.Aborted {
				return true
			}
		}
		return false
	})
	checkToolPairing(t, loaded)
	envDenied := false
	notesOK := false
	for _, m := range loaded.Messages {
		if m.Role != "tool" {
			continue
		}
		switch m.ToolCallID {
		case "call-env":
			envDenied = !m.ToolOK && strings.Contains(m.Content, "denied")
		case "call-notes":
			notesOK = m.ToolOK && strings.Contains(m.Content, "plain")
		}
	}
	if !envDenied || !notesOK {
		t.Fatalf("after cancel: sensitive read denied=%v, normal read ok=%v", envDenied, notesOK)
	}
}

// TestStreamChatDoomLoopParallelBatch: محافظ تکرار روی «دسته‌ی موازی» —
// هر دور سه فراخوانی یکسان: دور ۱ [اجرا، اجرا، مسدود]؛ دور ۲ [مسدود،
// توقف، متوقف] → نوبت با اعلان فارسی پایان می‌یابد و جفت‌بندی سالم می‌ماند.
func TestStreamChatDoomLoopParallelBatch(t *testing.T) {
	redirectToTestLog(t)
	var reqs int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqs, 1)
		sseToolCallsBatch(w, []apiToolCall{
			batchCall("call-1", "list_dir", `{"path":"."}`),
			batchCall("call-2", "list_dir", `{"path":"."}`),
			batchCall("call-3", "list_dir", `{"path":"."}`),
		}, "tool_calls")
	}))
	defer srv.Close()

	a := agentStreamApp(t, srv.URL)
	conv, err := a.SendMessage("", "سه بار لیست بگیر و گیر کن", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer waitStreamDone(t, a)

	waitConversationUntil(t, a, conv.ID, hasAssistantWith("حلقه‌ی بی\u200cپایان"))
	time.Sleep(150 * time.Millisecond) // فرصتِ دورِ اضافی اگر باگ باشد
	if got := atomic.LoadInt32(&reqs); got != 2 {
		t.Fatalf("doom loop must stop after 2 rounds, got %d requests", got)
	}

	a.mu.Lock()
	loaded, lerr := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if lerr != nil {
		t.Fatal(lerr)
	}
	if n := countRoundsWithToolCalls(loaded); n != 2 {
		t.Fatalf("want 2 assistant tool rounds, got %d", n)
	}
	checkToolPairing(t, loaded)

	executed, blocked, stopped := 0, 0, 0
	for _, m := range loaded.Messages {
		if m.Role != "tool" || m.ToolName != "list_dir" {
			continue
		}
		switch {
		case strings.Contains(m.Content, "loop guard"):
			stopped++
		case strings.Contains(m.Content, "identical repeated tool call blocked"):
			blocked++
		default:
			executed++
		}
	}
	if executed != 2 || blocked != 2 || stopped != 2 {
		t.Fatalf("want executed=2 blocked=2 stopped=2, got %d/%d/%d", executed, blocked, stopped)
	}
	var final *Message
	for i := range loaded.Messages {
		if m := &loaded.Messages[i]; m.Role == "assistant" && len(m.ToolCalls) == 0 && strings.Contains(m.Content, "حلقه‌ی") {
			final = m
		}
	}
	if final == nil || final.Aborted {
		t.Fatalf("final Persian notice missing or marked aborted: %+v", final)
	}
	if !strings.Contains(final.Content, "list_dir") || !strings.Contains(final.Content, "۵ بار") {
		t.Fatalf("notice must name the tool and Persian repeat count: %s", final.Content)
	}
}
