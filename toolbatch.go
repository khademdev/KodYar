// اجرای موازی ابزارهای مستقل (خواندنی و تغییردهنده).
//
// دو نوع سگمنت موازی وجود دارد: دنباله‌های پیوسته‌ی ابزارهای فقط‌خواندنی
// (مثل قبل) و دنباله‌های پیوسته‌ی تغییردهنده‌هایی که مسیرهایشان با هیچ
// فراخوانی دیگری در همان سگمنت مشترک نیست (write_file/edit_file/delete/
// move روی فایل‌های متفاوت). تداخل (همان فایل) سگمنت را می‌شکند و آن
// فراخوانی ترتیبی اجرا می‌شود تا ترتیب ویرایش‌ها حفظ شود.
//
// وقتی یک پیام دستیار چند فراخوانی ابزار دارد، دنباله‌های پیوسته‌ی
// فراخوانی‌های «فقط‌خواندنی و مستقل» (list_dir، read_file، search_files،
// glob، git_status/diff/log/show) با سقف parallelToolLimit() به‌صورت هم‌زمان اجرا
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
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// parallelToolLimit سقف workerهای اجرای هم‌زمان ابزارهای فقط‌خواندنی.
// پیش از این ثابت ۴ بود؛ خواندن‌ها I/O-bound هستند (دیسک/git/فایل‌های بزرگ)
// و روی ماشین‌های چند‌هسته‌ای ۴ worker ظرفیت را هدر می‌داد. سقف در بازهٔ
// ۴ تا ۸ و بر اساس تعداد CPU است.
func parallelToolLimit() int {
	n := runtime.GOMAXPROCS(0)
	if n < 4 {
		return 4
	}
	if n > 8 {
		return 8
	}
	return n
}

// parallelToolLimitFn قلاب تست (فقط پیش از هم‌زمانی‌ها تنظیم شود).
var parallelToolLimitFn = parallelToolLimit

// parallelReadOnlyTools ابزارهای فقط‌خواندنیِ مستقل که اجرای هم‌زمانشان
// بی‌خطر است: هیچ وضعیت مشترکی را تغییر نمی‌دهند و a.mu را فقط لحظه‌ای
// (کپی مقدار) می‌گیرند. ابزار glob همین‌جا هم هست:
var parallelReadOnlyTools = map[string]bool{
	"list_dir":        true,
	"read_file":       true,
	"search_files":    true,
	"glob":            true,
	"find_symbol":     true,
	"find_references": true,
	"git_status":      true,
	"git_diff":        true,
	"git_log":         true,
	"git_show":        true,
}

// isParallelReadOnlyTool این ابزار قابل اجرای هم‌زمان در دسته‌ی موازی است؟
func isParallelReadOnlyTool(name string) bool {
	return parallelReadOnlyTools[name]
}

// parallelTaskTools ابزار «task» (زیرایجنت). هر زیرایجنت کانتکست و حلقه‌ی
// مدل خودش را دارد و هیپ وضعیت مشترکی با دیگری ندارد (فقط می‌خواند و
// راستی‌آزمایی می‌کند) — پس چند زیرکار مستقل می‌توانند هم‌زمان اجرا شوند.
// سقف جداگانهٔ parallelTaskLimit() دارد تا انفجار درخواست به مدل رخ ندهد.
var parallelTaskTools = map[string]bool{"task": true}

func isParallelTaskTool(name string) bool { return parallelTaskTools[name] }

// parallelTaskLimit سقف هم‌زمانی زیرکارها (۲ تا ۳؛ کمتر از سقف ابزارهای
// فقط‌خواندنی چون هر زیرکار خودش یک جریان مدل/شبکه‌ی جداگانه است).
func parallelTaskLimit() int {
	n := parallelToolLimit() / 2
	if n < 2 {
		return 2
	}
	if n > 3 {
		return 3
	}
	return n
}

// parallelTaskLimitFn قلاب تست.
var parallelTaskLimitFn = parallelTaskLimit

// parallelWriteTools ابزارهای تغییردهنده‌ای که اگر مسیرهایشان با هیچ
// فراخوانی دیگری در همان سگمنت اشتراک نداشته باشد، اجرای هم‌زمانشان
// بی‌خطر است (مثل نوشتن چند فایل مستقل). create_dir عمداً نیست: ساخت
// پوشه با نوشتن فایل داخل همان پوشه می‌تواند تداخل کند و ارزان است.
var parallelWriteTools = map[string]bool{
	"write_file":  true,
	"edit_file":   true,
	"delete_path": true,
	"move_path":   true,
}

// isParallelWriteTool این ابزار در دسته‌ی موازیِ تغییردهنده‌هاست؟
func isParallelWriteTool(name string) bool {
	return parallelWriteTools[name]
}

// toolCallPaths مسیرهای تحت اثر یک فراخوانی تغییردهنده را برمی‌گرداند.
// ok=false یعنی مسیر قابل‌استخراج نیست (آرگومان نامعتبر/ناقص) و فراخوانی
// باید ترتیبی بماند. مسیرها کوچک و یکدست می‌شوند تا مقایسه‌ی «همان فایل»
// روی ویندوز/مک (حساس‌نبودن به بزرگی حروف) محافظه‌کارانه باشد.
func toolCallPaths(call apiToolCall, ws string) ([]string, bool) {
	args := map[string]interface{}{}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return nil, false
	}
	str := func(key string) string {
		s, _ := args[key].(string)
		return strings.TrimSpace(s)
	}
	var raw []string
	switch call.Function.Name {
	case "write_file", "edit_file", "delete_path":
		raw = []string{str("path")}
	case "move_path":
		raw = []string{str("from"), str("to")}
	default:
		return nil, false
	}
	out := make([]string, 0, len(raw))
	ws = strings.TrimSpace(ws)
	for _, p := range raw {
		if p == "" {
			return nil, false
		}
		// مسیر نسبی به پوشهٔ کاری گره می‌خورد تا «a.txt» و «F:\ws\a.txt»
		// یک فایل شناخته شوند؛ وگرنه دو نوشتنِ هم‌زمان روی یک فایل واقعی
		// از سد تداخل رد می‌شوند و یکی از ویرایش‌ها گم می‌شود.
		if ws != "" && !filepath.IsAbs(p) {
			p = filepath.Join(ws, p)
		}
		out = append(out, normalizeToolPath(p))
	}
	return out, true
}

// normalizeToolPath یکدست‌سازی مسیر برای تشخیص تداخل در همان دسته.
func normalizeToolPath(p string) string {
	clean := filepath.Clean(strings.ReplaceAll(strings.TrimSpace(p), `\`, "/"))
	// filepath.Clean روی ویندوز جداکننده را به «\» برمی‌گرداند؛ برای مقایسه
	// یکدست دوباره به «/» تبدیل می‌شود.
	return strings.ToLower(strings.ReplaceAll(clean, `\`, "/"))
}

// pathsOverlap آیا یکی از مسیرها قبلاً در همین سگمنت استفاده شده است؟
func pathsOverlap(seen map[string]bool, paths []string) bool {
	for _, p := range paths {
		if seen[p] {
			return true
		}
	}
	return false
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
// parallelToolLimit()) و هر فراخوانی دیگر گام ترتیبیِ تنها می‌ماند؛
// سگمنت‌ها خودشان به ترتیب اصلی و پشت‌سرهم اجرا می‌شوند. خروجی همیشه به
// ترتیب اصلی است. همه‌ی گوروتین‌ها پیش از بازگشت join می‌شوند.
func (a *App) executeToolCalls(ctx context.Context, convID string, tasks []toolBatchTask, truncated bool, exec toolExecFn) []toolCallOutcome {
	outcomes := make([]toolCallOutcome, len(tasks))
	for i := 0; i < len(tasks); {
		if !tasks[i].exec {
			outcomes[i] = a.runToolCall(ctx, convID, tasks[i], truncated, exec)
			i++
			continue
		}
		name := tasks[i].call.Function.Name
		switch {
		case isParallelReadOnlyTool(name):
			j := i + 1
			for j < len(tasks) && tasks[j].exec && isParallelReadOnlyTool(tasks[j].call.Function.Name) {
				j++
			}
			a.runParallelSegment(ctx, convID, tasks[i:j], outcomes[i:j], truncated, exec, false)
			i = j
		case isParallelTaskTool(name):
			// دنباله‌ی پیوستهٔ زیرکارهای مستقل → هم‌زمان با سقف کم.
			j := i + 1
			for j < len(tasks) && tasks[j].exec && isParallelTaskTool(tasks[j].call.Function.Name) {
				j++
			}
			a.runParallelSegmentLimit(ctx, convID, tasks[i:j], outcomes[i:j], truncated, exec, false, parallelTaskLimitFn())
			i = j
		case isParallelWriteTool(name):
			ws := ""
			if a.settings != nil {
				ws = strings.TrimSpace(a.settings.WorkspacePath)
			}
			// سگمنت موازیِ تغییردهنده‌ها فقط تا وقتی ادامه دارد که هر
			// فراخوانی مسیرهای قابل‌استخراج و «جدا» داشته باشد؛ اولین
			// تداخل (همان فایل، مثل دو edit_file روی یک مسیر) یا ابزار
			// نامرتبط، مرز سگمنت است و آن فراخوانی در گام بعدی/ترتیبی
			// اجرا می‌شود — یعنی ترتیبِ معنایی فایل حفظ می‌شود.
			seen := map[string]bool{}
			j := i
			for j < len(tasks) && tasks[j].exec && isParallelWriteTool(tasks[j].call.Function.Name) {
				paths, ok := toolCallPaths(tasks[j].call, ws)
				if !ok || pathsOverlap(seen, paths) {
					break
				}
				for _, p := range paths {
					seen[p] = true
				}
				j++
			}
			if j == i {
				// مسیر نامعلوم — همان گام ترتیبی
				outcomes[i] = a.runToolCall(ctx, convID, tasks[i], truncated, exec)
				i++
				continue
			}
			a.runParallelSegment(ctx, convID, tasks[i:j], outcomes[i:j], truncated, exec, true)
			i = j
		default:
			outcomes[i] = a.runToolCall(ctx, convID, tasks[i], truncated, exec)
			i++
		}
	}
	return outcomes
}

// runParallelSegment یک سگمنت فقط‌خواندنی را با حداکثر parallelToolLimit()
// worker هم‌زمان اجرا و پیش از بازگشت از خروج همه‌شان مطمئن می‌شود. هر
// task صرف‌نظر از هر چیز outcome خودش را می‌نویسد تا جفت‌بندی
// tool_calls/tool هرگز نشکند (لغو → خطای «قبل از اجرا لغو شد» از داخل
// runToolCall). گرفتن semaphore با ctx قطع نمی‌شود: workerها کارِ کوتاه یا
// قابل‌لغو انجام می‌دهند و صف خیلی زود پیش می‌رود (بدون آن، taskی که
// نوبتش نیست بدون outcome می‌ماند).
// orderedEvents=true یعنی رویدادهای UI به ترتیب اصلی و «دسته‌ای» ارسال
// می‌شوند: اول همه‌ی chat:toolها به ترتیب، بعد از پایان همه‌ی workerها همه‌ی
// chat:tool_resultها به ترتیب — همان چیزی که برای مودال‌های تأیید و کارت‌های
// تغییر فایل لازم است. برای خواندن‌ها (false) رویداد هر فراخوانی مثل قبل
// درست پیش/پس از خودش می‌رود.
func (a *App) runParallelSegment(ctx context.Context, convID string, tasks []toolBatchTask, outcomes []toolCallOutcome, truncated bool, exec toolExecFn, orderedEvents bool) {
	a.runParallelSegmentLimit(ctx, convID, tasks, outcomes, truncated, exec, orderedEvents, parallelToolLimitFn())
}

// runParallelSegmentLimit مثل runParallelSegment با سقف هم‌زمانیِ صریح.
func (a *App) runParallelSegmentLimit(ctx context.Context, convID string, tasks []toolBatchTask, outcomes []toolCallOutcome, truncated bool, exec toolExecFn, orderedEvents bool, maxWorkers int) {
	if len(tasks) <= 1 {
		for k := range tasks {
			outcomes[k] = a.runToolCall(ctx, convID, tasks[k], truncated, exec)
		}
		return
	}
	if orderedEvents {
		for k := range tasks {
			emitToolStart(a, convID, tasks[k].call)
		}
	}
	limit := maxWorkers
	if limit < 1 {
		limit = 1
	}
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
			if orderedEvents {
				outcomes[k] = a.executeToolBody(ctx, convID, tasks[k], truncated, exec)
			} else {
				outcomes[k] = a.runToolCall(ctx, convID, tasks[k], truncated, exec)
			}
		})
	}
	wg.Wait()
	if orderedEvents {
		for k := range tasks {
			emitToolResult(a, convID, tasks[k].call, outcomes[k])
		}
	}
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
// emitToolStart رویداد «شروع اجرای این فراخوانی» را به UI می‌فرستد.
func emitToolStart(a *App, convID string, c apiToolCall) {
	runtimeEmit(a, "chat:tool", map[string]interface{}{
		"conversationId": convID,
		"callId":         c.ID,
		"name":           c.Function.Name,
		"args":           c.Function.Arguments,
	})
}

// emitToolResult رویداد پایان همان فراخوانی را می‌فرستد (کلید: callId).
func emitToolResult(a *App, convID string, c apiToolCall, out toolCallOutcome) {
	runtimeEmit(a, "chat:tool_result", map[string]interface{}{
		"conversationId": convID,
		"callId":         c.ID,
		"name":           c.Function.Name,
		"ok":             out.ok,
		"result":         out.result,
		"ms":             out.ms,
	})
}

func (a *App) runToolCall(ctx context.Context, convID string, t toolBatchTask, truncated bool, exec toolExecFn) toolCallOutcome {
	emitToolStart(a, convID, t.call)
	out := a.executeToolBody(ctx, convID, t, truncated, exec)
	emitToolResult(a, convID, t.call, out)
	return out
}

// executeToolBody اجرای بدنه‌ی یک فراخوانی بدون رویداد start/result —
// حالت موازیِ مرتب، رویدادها را خودش و به ترتیب اصلی می‌فرستد.
func (a *App) executeToolBody(ctx context.Context, convID string, t toolBatchTask, truncated bool, exec toolExecFn) toolCallOutcome {
	c := t.call
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
	// زمان ابزار برای اندازه‌گیری تأخیر نوبت (latency.go) — بدون timing بی‌اثر.
	noteToolTiming(ctx, ms)
	return toolCallOutcome{result: result, ok: ok, ms: ms}
}
