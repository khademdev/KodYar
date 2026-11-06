// اجرای موازی ابزارهای فقط‌خواندنی.
//
// وقتی یک پیام دستیار چند فراخوانی ابزار دارد، دنباله‌های پیوسته‌ی
// فراخوانی‌های «فقط‌خواندنی و مستقل» (list_dir، read_file، search_files،
// glob، git_status/diff/log/show) با سقف parallelToolWorkers به‌صورت هم‌زمان اجرا
// می‌شوند؛ هر فراخوانی دیگر (تغییردهنده، مسدودشده توسط محافظ تکرار،...)
// گام ترتیبیِ تنها می‌ماند و مثل سد عمل می‌کند: خواندن‌های قبل از یک
// تغییردهنده باید تمام شوند تا شروع شود و خواندن‌های بعد از آن تا پایان
// تغییر صبر می‌کنند — ترتیب معناییِ فراخوانی‌ها حفظ می‌شود.
//
// تضمین‌ها:
// - نتایج، پیام‌های تاریخچه و ذخیره‌ی میان‌دوری روی دیسک همیشه به
// «ترتیب اصلی فراخوانی‌ها» تولید می‌شوند؛ فقط زمانِ اجرا هم‌پوشان
// می‌شود. جفت‌بندی tool_calls/tool مانند قبل سالم می‌ماند.
// - رویدادهای هر فراخوانی (chat:tool و سپس chat:tool_result همان
// callId) درست مانند حلقه‌ی ترتیبیِ قبلی، درست پیش از اجرا و درست پس
// از پایان همان فراخوانی ارسال می‌شوند؛ فرانت‌اند کارت‌ها را با
// callId کلید می‌زند، پس درهم‌آمیزیِ فراخوانی‌های موازی بی‌ضرر است.
// - هیچ قفلی در طول اجرای ابزار گرفته نمی‌شود (ابزارهای فقط‌خواندنی
// a.mu را فقط لحظه‌ای و برای کپی می‌گیرند)؛ همه‌ی workerها پیش از
// بازگشت executeToolCalls join می‌شوند — هیچ گوروتینی از نوبت طولانی‌تر
// نمی‌کند — و لغوِ ctx به همه سرایت می‌کند.
// - تصمیم‌های محافظ حلقه‌ی تکرار (doom-loop) «پیش از اجرا»، به ترتیب
// اصلی و تک‌نخی محاسبه می‌شوند تا رفتار محافظ دقیقاً مثل اجرای
// ترتیبی بماند (سومین تکرار یکسان مسدود، پنجمین نوبت را متوقف می‌کند).
// - مودال‌های مجوز (مثل خواندن فایل حساس) با permAskMu در
// permissions.go سریال می‌شوند — کاربر هرگز دو دیالوگ هم‌زمان نمی‌بیند.
package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// parallelToolWorkers سقف workerهای اجرای هم‌زمان ابزارهای فقط‌خواندنی.
const parallelToolWorkers = 4

// parallelReadOnlyTools ابزارهای فقط‌خواندنیِ مستقل که اجرای هم‌زمانشان
// بی‌خطر است: هیچ وضعیت مشترکی را تغییر نمی‌دهند و a.mu را فقط لحظه‌ای
// (کپی مقدار) می‌گیرند. ابزار glob همین‌جا هم هست:
var parallelReadOnlyTools = map[string]bool{
	"list_dir":     true,
	"read_file":    true,
	"search_files": true,
	"glob":         true,
	"git_status":   true,
	"git_diff":     true,
	"git_log":      true,
	"git_show":     true,
}

// isParallelReadOnlyTool این ابزار قابل اجرای هم‌زمان در دسته‌ی موازی است؟
func isParallelReadOnlyTool(name string) bool {
	return parallelReadOnlyTools[name]
}

// toolCallOutcome نتیجه‌ی اجرای یک فراخوانی ابزار؛ executeToolCalls
// outcomeها را همیشه به ترتیب اصلی فراخوانی‌ها برمی‌گرداند.
type toolCallOutcome struct {
	result string
	ok     bool
	ms     int64
}

// toolBatchTask یک فراخوانی از دسته‌ی این دور به‌همراه تصمیمِ
// ازپیش‌محاسبه‌شده. exec=false یعنی نتیجه از قبل معلوم است (مسدودشده توسط
// محافظ تکرار، متوقف‌شده یا لغوشده) و result/ok همان نتیجه‌ی نهایی‌اند.
type toolBatchTask struct {
	call   apiToolCall
	exec   bool
	result string
	ok     bool
}

// toolExecFn اجرای واقعی یک ابزار — قابل تزریق برای تست‌های قطعی.
type toolExecFn func(ctx context.Context, name, argsJSON string) (string, bool)

// planToolCalls تصمیم‌های یک دسته فراخوانی را پیش از اجرا و به ترتیب اصلی
// محاسبه می‌کند — همان منطق قبلیِ داخل حلقه، فقط جدا شده تا اجرای موازی
// رفتار محافظ تکرار (doom-loop) را دقیقاً مثل اجرای ترتیبی نگه دارد:
// فراخوانی سومِ یکسانِ متوالی مسدود می‌شود، پنجمی نوبت را متوقف می‌کند و
// پس از توقف، بقیه‌ی فراخوانی‌های همان دسته فقط خطای توقف می‌گیرند تا
// جفت‌بندی tool_calls/tool روی دیسک سالم بماند (فراخوانیِ بعد از توقف
// دیگر به محافظ گزارش نمی‌شود — مانند رفتار قبلی).
// خروجی: taskها به ترتیب اصلی، پرچم توقف نوبت و نام ابزار متوقف‌کننده.
func planToolCalls(calls []apiToolCall, guard *toolLoopGuard) (tasks []toolBatchTask, turnStopped bool, stoppedTool string) {
	tasks = make([]toolBatchTask, len(calls))
	for i := range calls {
		t := &tasks[i]
		t.call = calls[i]
		switch {
		case turnStopped:
			t.result, t.ok = toolTurnStoppedError, false
		default:
			// شمارش تکرارهای یکسانِ متوالی — پیش از اجرا؛ حتی وقتی آرگومان‌ها
			// نامعتبرند یا دسترسی رد شده (تکرارِ بی‌اثر هم حلقه است).
			run := guard.observe(calls[i].Function.Name, calls[i].Function.Arguments)
			switch {
			case run >= toolRepeatStopRun:
				turnStopped = true
				stoppedTool = calls[i].Function.Name
				t.result, t.ok = toolTurnStoppedError, false
			case run >= toolRepeatBlockRun:
				t.result, t.ok = repeatBlockedToolError(calls[i].Function.Name, run), false
			default:
				t.exec = true
			}
		}
	}
	return tasks, turnStopped, stoppedTool
}

// executeToolCalls دسته‌ی فراخوانی‌های یک دور را اجرا می‌کند: دنباله‌های
// پیوسته‌ی «قابل‌اجرا + فقط‌خواندنی» سگمنت موازی می‌شوند (سقف
// parallelToolWorkers) و هر فراخوانی دیگر گام ترتیبیِ تنها می‌ماند؛
// سگمنت‌ها خودشان به ترتیب اصلی و پشت‌سرهم اجرا می‌شوند. خروجی همیشه به
// ترتیب اصلی است. همه‌ی گوروتین‌ها پیش از بازگشت join می‌شوند.
func (a *App) executeToolCalls(ctx context.Context, convID string, tasks []toolBatchTask, truncated bool, exec toolExecFn) []toolCallOutcome {
	outcomes := make([]toolCallOutcome, len(tasks))
	for i := 0; i < len(tasks); {
		if !tasks[i].exec || !isParallelReadOnlyTool(tasks[i].call.Function.Name) {
			outcomes[i] = a.runToolCall(ctx, convID, tasks[i], truncated, exec)
			i++
			continue
		}
		j := i + 1
		for j < len(tasks) && tasks[j].exec && isParallelReadOnlyTool(tasks[j].call.Function.Name) {
			j++
		}
		a.runParallelSegment(ctx, convID, tasks[i:j], outcomes[i:j], truncated, exec)
		i = j
	}
	return outcomes
}

// runParallelSegment یک سگمنت فقط‌خواندنی را با حداکثر parallelToolWorkers
// worker هم‌زمان اجرا و پیش از بازگشت از خروج همه‌شان مطمئن می‌شود. هر
// task صرف‌نظر از هر چیز outcome خودش را می‌نویسد تا جفت‌بندی
// tool_calls/tool هرگز نشکند (لغو → خطای «قبل از اجرا لغو شد» از داخل
// runToolCall). گرفتن semaphore با ctx قطع نمی‌شود: workerها کارِ کوتاه یا
// قابل‌لغو انجام می‌دهند و صف خیلی زود پیش می‌رود (بدون آن، taskی که
// نوبتش نیست بدون outcome می‌ماند).
func (a *App) runParallelSegment(ctx context.Context, convID string, tasks []toolBatchTask, outcomes []toolCallOutcome, truncated bool, exec toolExecFn) {
	if len(tasks) <= 1 {
		for k := range tasks {
			outcomes[k] = a.runToolCall(ctx, convID, tasks[k], truncated, exec)
		}
		return
	}
	limit := parallelToolWorkers
	if len(tasks) < limit {
		limit = len(tasks)
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for k := range tasks {
		wg.Add(1)
		// هر worker با مهار panic اجرا می‌شود: فراخوانی خراب باید نتیجه‌ی
		// خطای خودش را بنویسد (جفت‌بندی tool_calls/tool سالم می‌ماند) و
		// بقیه‌ی workerها به کارشان ادامه دهند.
		safeGo(a, "tool.worker", func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					recordPanic(a, "tool."+tasks[k].call.Function.Name, r)
					outcomes[k] = toolCallOutcome{
						result: fmt.Sprintf(errInternalTool, tasks[k].call.Function.Name),
						ok:     false,
					}
				}
			}()
			outcomes[k] = a.runToolCall(ctx, convID, tasks[k], truncated, exec)
		})
	}
	wg.Wait()
}

// callToolGuarded اجرای ابزار را با مهار panic انجام می‌دهد. مسیر ترتیبی هم
// مثل workerهای موازی باید زنده بماند: panic در ابزار فقط نتیجه‌ی خطای همان
// ابزار می‌شود، جفت‌بندی tool_calls/tool سالم می‌ماند و برنامه نمی‌میرد.
func callToolGuarded(a *App, ctx context.Context, name string, exec toolExecFn, args string) (result string, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			recordPanic(a, "tool."+name, r)
			result, ok = fmt.Sprintf(errInternalTool, name), false
		}
	}()
	return exec(ctx, name, args)
}

// runToolCall یک فراخوانی را اجرا می‌کند: رویداد chat:tool درست پیش از
// اجرا، بررسی لغو، اجرا (یا نتیجه‌ی ازپیش‌محاسبه‌شده)، لاگ خطا و رویداد
// chat:tool_result — دقیقاً همان توالیِ حلقه‌ی ترتیبیِ قبلی. از چند worker
// هم‌زمان هم امن است: runtimeEmit و logAppErrorFor قفل‌برگ‌اند و outcomes
// به ازای هر task اندیس اختصاصی خودش را دارد.
func (a *App) runToolCall(ctx context.Context, convID string, t toolBatchTask, truncated bool, exec toolExecFn) toolCallOutcome {
	c := t.call
	runtimeEmit(a, "chat:tool", map[string]interface{}{
		"conversationId": convID,
		"callId":         c.ID,
		"name":           c.Function.Name,
		"args":           c.Function.Arguments,
	})
	t0 := time.Now()
	var result string
	var ok bool
	switch {
	case !t.exec:
		result, ok = t.result, t.ok
	case ctx.Err() != nil:
		result, ok = "ERROR: cancelled by the user before this tool ran.", false
	default:
		// با info فراخوانی → استریم خروجی run_command (chat:tool_output)
		toolCtx := withToolCallInfo(ctx, convID, c.ID)
		if truncated {
			toolCtx = withTruncationHint(toolCtx)
		}
		result, ok = callToolGuarded(a, toolCtx, c.Function.Name, exec, c.Function.Arguments)
	}
	if result == "" {
		result = "(empty)"
	}
	// خطاهای ابزار در لاگ شفاف ثبت می‌شوند (بدون فشار به UI)
	if !ok {
		logAppErrorFor(a, "tool", c.Function.Name+": "+cutUTF8(result, 300))
	}
	ms := time.Since(t0).Milliseconds()
	runtimeEmit(a, "chat:tool_result", map[string]interface{}{
		"conversationId": convID,
		"callId":         c.ID,
		"name":           c.Function.Name,
		"ok":             ok,
		"result":         result,
		"ms":             ms,
	})
	return toolCallOutcome{result: result, ok: ok, ms: ms}
}
