// MyAI — زیرایجنت: ابزار task برای سپردن زیرکارهای خودکفا به
// یک ایجنتِ فقط‌خواندنی با کانتکست تازه.
//
// طراحی:
// - ابزار `task` فقط از حلقه‌ی اصلی ایجنت قابل فراخوانی است. زیرایجنتِ
// ساخته‌شده در runSubagent ابزارهایش را از execSubagentTool می‌گیرد که
// ابزارهای فقط‌خواندنی و run_commandِ محدود به راستی‌آزمایی را می‌پذیرد —
// یعنی تودرتویی (task داخل task) و هرگونه تغییر فایل از ابتدا غیرممکن است
// و دستورِ غیر تست/بیلد هم توسط subagentRunCommand رد می‌شود.
// - زیرایجنت کاملاً headless است: هیچ رویدادی به UI نمی‌فرستد، چیزی روی
// دیسک ذخیره نمی‌کند و بودجه‌ی توکن/دور خودش را دارد. خروجی نهایی‌اش
// (کران‌دار) به‌عنوان نتیجه‌ی ابزار به مدلِ والد برمی‌گردد.
// - امنیت: داخل زیرایجنت هیچ مودال تأییدی باز نمی‌شود — کلید کانتکست
// subagentNoAsk باعث می‌شود requestPermissionConfirm فوراً «رد» برگرداند؛
// در نتیجه فایل‌های حساس (.env و…) خوانده نمی‌شوند و دستوری اجرا نمی‌شود.
// - استفاده‌ی مجدد: dialStream (تلاش مجدد هوشمند، بودجه/فشرده‌سازی
// کانتکست، محافظ حلقه‌ی تکرار و اعتبارسنجی آرگومان‌ها
// عیناً همان پیاده‌سازی‌های اصلی‌اند — کپی‌کاری ممنوع.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------- محدودیت‌ها

const (
	// پیش‌فرض و محدوده‌ی «حداکثر دورهای زیرایجنت» — جدا از سقف دورهای
	// ایجنت اصلی (کاربر می‌تواند هر دو را در تنظیمات عوض کند).
	defaultMaxSubagentRounds = 8
	minMaxSubagentRounds     = 3
	maxMaxSubagentRounds     = 50

	// سقف مطلق زمان اجرای یک زیرایجنت — حتی اگر دورها تمام نشده باشند.
	subagentWallTimeout = 5 * time.Minute

	// سقف متن گزارشی که به مدل والد برمی‌گردد (کاراکتر) — head+tail تا
	// ابتدا و انتهای گزارش (خلاصه و نتیجه‌گیری) هر دو حفظ شوند.
	maxSubagentResult = 8 * 1024

	// سقف متنِ «مأموریت» که از مدل والد پذیرفته می‌شود (کاراکتر).
	maxSubagentTaskLen = 16 * 1024
)

// clampMaxSubagentRounds مثل clampMaxAgentRounds — صفر/منفی یعنی تنظیم‌نشده
// و به پیش‌فرض برمی‌گردد (سازگار با settings.json قدیمی).
func clampMaxSubagentRounds(n int) int {
	if n <= 0 {
		return defaultMaxSubagentRounds
	}
	if n < minMaxSubagentRounds {
		return minMaxSubagentRounds
	}
	if n > maxMaxSubagentRounds {
		return maxMaxSubagentRounds
	}
	return n
}

// ---------------------------------------------------------------- کلید کانتکست

// subagentNoAskKey کلید نشانگر «داخل زیرایجنت» روی context. فقط
// withSubagentNoAsk مقدارش را می‌گذارد و subagentNoAsk می‌خواندش.
type subagentNoAskKey struct{}

// withSubagentNoAsk کانتکست ورودی را با نشانگر زیرایجنت برمی‌گرداند.
func withSubagentNoAsk(ctx context.Context) context.Context {
	return context.WithValue(ctx, subagentNoAskKey{}, true)
}

// subagentNoAsk گزارش می‌دهد که این کانتکست متعلق به اجرای زیرایجنت است
// (یعنی هیچ مودال تأییدی نباید باز شود).
func subagentNoAsk(ctx context.Context) bool {
	v, _ := ctx.Value(subagentNoAskKey{}).(bool)
	return v
}

// effectiveSubagentModel مدل مؤثر زیرایجنت را برمی‌گرداند: مدل اختصاصی
// اگر تنظیم شده باشد، وگرنه مدل اصلی ایجنت.
func effectiveSubagentModel(mainModel, subModel string) string {
	if m := strings.TrimSpace(subModel); m != "" {
		return m
	}
	return mainModel
}

// skillsSuffix بلوک مهارت‌ها را با جداکننده‌ی مناسب به پرامپت زیرایجنت می‌چسباند.
func skillsSuffix(block string) string {
	if strings.TrimSpace(block) == "" {
		return ""
	}
	return "\n\n" + block
}

// ---------------------------------------------------------------- آمار مصرف (Bug 8)

// usageTally جمع آمار توکنِ مصرفیِ زیرکارها (ابزار task) در طول یک نوبتِ گفتگوی
// اصلی است: streamChat یک نمونه می‌سازد و آن را در ctx می‌گذارد، runSubagent
// آمار خودش را در آن می‌ریزد و streamChat پیش از finishChat دو بخش را جمع
// می‌کند. اتمیک است (بدون قفل) چون ابزارهای موازی ممکن است هم‌زمان زیرایجنت
// اجرا کنند؛ هیچ قالبِ ذخیره‌شده‌ای تغییر نمی‌کند.
type usageTally struct {
	prompt     atomic.Int64
	completion atomic.Int64
}

// usageTallyKey کلید ctx برای accumulator آمار.
type usageTallyKey struct{}

// withUsageTally accumulator را به ctx می‌چسباند (فقط streamChat).
func withUsageTally(ctx context.Context, t *usageTally) context.Context {
	return context.WithValue(ctx, usageTallyKey{}, t)
}

// usageTallyFrom accumulator همین نوبت را برمی‌گرداند (nil اگر اجرای مستقل
// باشد — مثلاً تست یا فراخوانی بیرون از نوبت).
func usageTallyFrom(ctx context.Context) *usageTally {
	t, _ := ctx.Value(usageTallyKey{}).(*usageTally)
	return t
}

// add آمار یک زیرایجنت را اضافه می‌کند (nil-safe).
func (t *usageTally) add(prompt, completion int64) {
	if t == nil {
		return
	}
	t.prompt.Add(prompt)
	t.completion.Add(completion)
}

// snapshot مقادیر فعلی را برمی‌گرداند (nil-safe).
func (t *usageTally) snapshot() (int64, int64) {
	if t == nil {
		return 0, 0
	}
	return t.prompt.Load(), t.completion.Load()
}

// truncateHeadTail متن را با نگه‌داشتن ابتدا و انتها کوتاه می‌کند — برای
// گزارش زیرایجنت که خلاصه در ابتدا و نتیجه در انتهاست.
func truncateHeadTail(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	head := limit * 3 / 4
	tail := limit - head
	return string(r[:head]) + "\n...[output truncated]...\n" + string(r[len(r)-tail:])
}

// ---------------------------------------------------------------- ابزارها

// subagentAllowedTools ابزارهای زیرایجنت: فقط‌خواندنی‌ها + run_commandِ
// **محدود به دستورهای راستی‌آزمایی** (build/test/vet/lint/typecheck). تغییردهنده‌ها
// و خودِ task عمداً غایب‌اند (تودرتویی ممنوع، یک سطح عمق).
//
// run_command در subagentRunCommand از همان دروازه‌ی دسترسی و همان سندباکس
// run_command می‌گذرد؛ چون داخل زیرایجنت هیچ مودالی باز نمی‌شود، دستوری که
// «نیاز به تأیید» داشته باشد اجرا نمی‌شود (به والد گزارش می‌شود).
var subagentAllowedTools = map[string]bool{
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
	"run_command":     true,
}

// subagentToolDefs زیرمجموعه‌ی فقط‌خواندنی agentToolDefs برای معرفی به مدل
// زیرایجنت — همان توصیف‌های رسمی؛ منبع واحد، بدون کپی رشته.
func subagentToolDefs() []toolDef {
	all := agentToolDefs()
	out := make([]toolDef, 0, len(subagentAllowedTools))
	for _, d := range all {
		if subagentAllowedTools[d.Function.Name] {
			out = append(out, d)
		}
	}
	return out
}

// subagentSystemPrompt هویت و قواعد زیرایجنت — کوتاه و انگلیسی (متن مدل).
// گزارش نهایی باید ساختارمند و مختصر باشد تا والد بتواند مستقیم استفاده کند.
func subagentSystemPrompt(workspace string) string {
	var b strings.Builder
	b.WriteString("You are MyAI's read-only sub-agent. You were spawned by the main agent to complete ONE self-contained exploration or analysis task, with a fresh context.\n\n")
	b.WriteString("Workspace directory (read-only tools are relative to it and CANNOT access anything outside it):\n")
	b.WriteString(workspace + "\n\n")
	b.WriteString("Available tools: list_dir, read_file, search_files, glob, find_symbol, find_references, git_status, git_diff, git_log, git_show, run_command (verification only).\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("1. Explore efficiently: use glob / search_files to locate candidates, then read_file only the relevant parts. You have a limited number of rounds — plan before you read.\n")
	b.WriteString("2. You CANNOT create, edit or delete files, ask the user questions, or spawn further sub-agents. Sensitive files (e.g. .env, private keys) are not readable.\n")
	b.WriteString("2b. run_command is limited to verification commands (go build/vet/test, npm/cargo/pytest test, tsc, lint). There is no approval modal inside a sub-agent, so a command that needs approval is refused: report it instead of insisting. Never start long-running or background processes, never install packages, never run mutating commands.\n")
	b.WriteString("3. Base every statement on real tool output; never invent file contents. If something cannot be found or read, say so plainly.\n")
	b.WriteString("4. When done (or when you cannot proceed), STOP calling tools and write your final report.\n")
	b.WriteString("Final report format (concise, structured Markdown, English is fine):\n")
	b.WriteString("- «Findings»: the concrete answer to the task (paths, names, line references, small snippets where essential).\n")
	b.WriteString("- «Notes»: caveats, dead ends or follow-ups the main agent should know about.")
	return b.String()
}

// execSubagentTool اجرای یک فراخوانی ابزار «داخل زیرایجنت»:
//
// - allowlist اول از همه (پیام انگلیسی دقیق برای مدل)،
// - همان parse/validate آرگومان‌های ایجنت اصلی ,
// - اجرای مستقیم پیاده‌سازی فقط‌خواندنی — بدون دروازه‌ی مودال؛
// کانتکست نشانگر subagentNoAsk دارد، پس فایل حساس از داخل
// requestSensitiveRead رد می‌شود (نه مودال، نه سکوت).
func (a *App) execSubagentTool(ctx context.Context, name, argsJSON string) (string, bool) {
	if !subagentAllowedTools[name] {
		return "ERROR: tool '" + name + "' is not available to sub-agents (read-only tools only; no nested task, no mutations, no commands).", false
	}
	args, err := parseToolArgs(argsJSON, truncationHinted(ctx))
	if err != nil {
		return err.Error(), false
	}
	if err := validateToolArgs(name, args); err != nil {
		return err.Error(), false
	}
	gs := func(key string) string {
		s, _ := coerceString(args[key])
		return s
	}
	gi := func(key string) int {
		n, _ := coerceInt(args[key])
		return n
	}
	gb := func(key string) bool {
		b, _ := coerceBool(args[key])
		return b
	}
	switch name {
	case "list_dir":
		return a.toolListDir(gs("path"))
	case "read_file":
		return a.toolReadFile(ctx, gs("path"), gi("start_line"), gi("end_line"))
	case "search_files":
		regex := gb("regex") || gb("is_regex")
		return a.toolSearchFilesOpts(ctx, searchOptions{
			Query:         gs("query"),
			Path:          gs("path"),
			Regex:         regex,
			MaxResults:    gi("max_results"),
			Include:       gs("include"),
			CaseSensitive: gb("case_sensitive"),
			Context:       gi("context"),
		})
	case "glob":
		return a.toolGlob(ctx, gs("pattern"), gs("path"))
	case "git_status":
		return a.toolGitStatus(ctx)
	case "git_diff":
		return a.toolGitDiff(ctx, gs("path"), gb("staged"))
	case "git_log":
		return a.toolGitLog(ctx, gs("path"), gi("limit"))
	case "git_show":
		return a.toolGitShow(ctx, gs("ref"))
	case "run_command":
		if gb("background") {
			return "ERROR: background processes are not allowed in sub-agents.", false
		}
		return a.subagentRunCommand(ctx, gs("command"))
	}
	return "ERROR: tool '" + name + "' is not available to sub-agents.", false
}

// subagentCommandTimeout سقف زمان یک دستور راستی‌آزمایی در زیرایجنت
// (تست‌ها می‌توانند طول بکشند، ولی نه بی‌نهایت).
const subagentCommandTimeout = 180 * time.Second

// subagentRunCommand اجرای دستور در زیرایجنت — فقط دستورهای راستی‌آزمایی
// (همان پیشوندهای turnReviewer)، با همان دروازهٔ دسترسی و سندباکس.
func (a *App) subagentRunCommand(ctx context.Context, command string) (string, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "ERROR: empty command.", false
	}
	if !isVerificationCommand(command) {
		return "ERROR: sub-agents may only run verification commands (build/test/vet/lint/type-check). '" +
			command + "' is not one; report it so the main agent can run it with the user's approval.", false
	}
	a.mu.Lock()
	allowed := a.settings != nil && a.settings.AllowCommands
	dir := ""
	if a.settings != nil {
		dir = strings.TrimSpace(a.settings.WorkspacePath)
	}
	a.mu.Unlock()
	if !allowed {
		return "ERROR: command execution is disabled by the user; report that instead of retrying.", false
	}
	if dir == "" {
		return "ERROR: workspace directory is not configured.", false
	}
	if autoVerifyRepoBinary(dir, command) {
		return "ERROR: running executables from inside the repository is not allowed in sub-agents ('" + command + "').", false
	}
	switch e := a.evalCommandPermDetailed(command); e.decision {
	case permDeny:
		return "ERROR: " + e.reason, false
	case permAllow:
	default:
		return "ERROR: '" + command + "' needs the user's approval and no approval modal exists inside a sub-agent. Report the command so the main agent can run it.", false
	}
	ctx, cancel := context.WithTimeout(ctx, subagentCommandTimeout)
	defer cancel()
	out, code, err := runShellWithPolicy(ctx, dir, command, subagentCommandTimeout, nil,
		a.sandboxPolicyForCommand(dir, command))
	body := truncateToolOutputKeepTail(strings.TrimRight(out, "\n"))
	header := fmt.Sprintf("exit=%d", code)
	if err != nil && body == "" {
		body = err.Error()
	}
	result := header + "\n" + body
	if err != nil {
		return result, false
	}
	return result, true
}

// ---------------------------------------------------------------- حلقه‌ی زیرایجنت

// runSubagent یک زیرایجنت کامل را اجرا می‌کند و گزارش نهایی را برمی‌گرداند.
//
// خروجی: (گزارشِ کران‌دار با سرایند آمار, ok). خطاها هم به‌صورت متن
// انگلیسی به مدل والد برمی‌گردند تا خودش مسیر دیگر امتحان کند.
func (a *App) runSubagent(ctx context.Context, task string) (report string, ok bool) {
	// panic زیرایجنت به گزارش خطا به والد تبدیل می‌شود — هرگز برنامه را
	// نمی‌کشد و جفت‌بندی tool_calls/tool والد را نمی‌شکند.
	defer func() {
		if r := recover(); r != nil {
			recordPanic(a, "runSubagent", r)
			report, ok = "ERROR: internal error in sub-agent (see the error log).", false
		}
	}()
	task = strings.TrimSpace(task)
	if task == "" {
		return "ERROR: empty task description.", false
	}
	if r := []rune(task); len(r) > maxSubagentTaskLen {
		task = string(r[:maxSubagentTaskLen]) + "\n...[task truncated]"
	}

	// اتصال مدل و تنظیمات — کپی زیر قفل، بدون قفل‌ماندن روی I/O (قاعده‌ی
	// معماری). زیرایجنت از همان Provider فعال استفاده می‌کند و مدلش یا مدل
	// اصلی است یا SubagentModel (اگر کاربر تنظیم کرده باشد) — مثلاً کاوش با
	// مدل ارزان و اجرا با مدل قوی. tools همیشه فعال است (ابزارهای فقط‌خواندنی).
	a.mu.Lock()
	provider := a.findProvider(a.settings.Provider)
	baseURL, model := "", a.settings.Model
	if provider != nil {
		baseURL = provider.BaseURL
		if model == "" && len(provider.Models) > 0 {
			model = provider.Models[0]
		}
	}
	model = effectiveSubagentModel(model, a.settings.SubagentModel)
	key := ""
	if provider != nil && !provider.NoKey {
		key = a.decryptKey(provider.ID)
	}
	temperature := a.settings.Temperature
	maxTokens := a.settings.MaxTokens
	maxRounds := clampMaxSubagentRounds(a.settings.MaxSubagentRounds)
	workspace := strings.TrimSpace(a.settings.WorkspacePath)
	skillsBlock := a.skillsPromptBlockLocked()
	cw := 0
	if provider != nil {
		cw = provider.ContextWindow
	}
	a.mu.Unlock()

	if baseURL == "" || model == "" {
		return "ERROR: sub-agent needs a configured provider and model.", false
	}
	if workspace == "" {
		return "ERROR: sub-agent needs a workspace directory.", false
	}
	if provider != nil && !provider.NoKey && key == "" {
		return "ERROR: sub-agent needs the provider API key.", false
	}

	// نشانگر «بدون مودال» + سقف زمانی مطلق. لغوِ کاربر (Stop) از طریق
	// ctx والد به کل زیرایجنت سرایت می‌کند.
	ctx = withSubagentNoAsk(ctx)
	ctx, cancel := context.WithTimeout(ctx, subagentWallTimeout)
	defer cancel()

	budget := contextBudget(cw, maxTokens)
	tools := subagentToolDefs()
	history := []chatMessage{
		{Role: "system", Content: subagentSystemPrompt(workspace) + a.repoMapSubagentBlock(workspace) + skillsSuffix(skillsBlock)},
		{Role: "user", Content: task},
	}
	counter := &historyTokenCache{}
	guard := &toolLoopGuard{}
	// تنظیمات ثابت این اجرا — subagentRequest هم برای دورهای عادی و هم برای
	// درخواست نهاییِ بدون ابزار از همین استفاده می‌کند (بدون کپی‌کاری).
	sess := &subagentSession{
		baseURL:             baseURL,
		key:                 key,
		model:               model,
		temperature:         temperature,
		maxTokens:           maxTokens,
		supportsTemperature: supportsTemperature(model),
	}

	var promptTokens, completionTokens int64
	if tally := usageTallyFrom(ctx); tally != nil {
		// آمار مصرف این زیرایجنت به آمار نوبت والد اضافه می‌شود — در همه‌ی
		// مسیرهای خروج (موفق/خطا/لغو)، چون توکن در هر حال مصرف شده است.
		defer func() { tally.add(promptTokens, completionTokens) }()
	}
	toolCalls, toolRounds := 0, 0 // فراخوانی‌ها جدا از دورها شمرده می‌شوند
	overflowRetried := false
	started := time.Now()

	for round := 0; ; round++ {
		// فشرده‌سازی خودکار در ≥۸۰٪ بودجه — همان آستانه‌ی گفتگوی اصلی؛
		// تاریخچه‌ی زیرایجنت هیچ نسخه‌ی دیسکی ندارد، پس «کپی API» همان
		// تاریخچه‌ی درون‌حافظه‌ای است.
		estTotal := counter.totalOf(history)
		if budget > 0 && estTotal >= budget*compactTriggerPercent/100 {
			if nh, ch := compactHistoryForBudget(history, budget, counter, false); ch {
				history = nh
				if fixed, problems := validateHistory(history); len(problems) > 0 {
					history = fixed
					counter.invalidate()
				}
			}
		}

		// یک دور کامل مدل: ساخت درخواست، dial، بررسی HTTP و تجزیه‌ی SSE همه در
		// subagentRequest است (همان تجزیه‌گر مشترک با streamChat).
		res, roundErr := a.subagentRequest(ctx, sess, history, tools)
		promptTokens += res.prompt
		completionTokens += res.completion
		if roundErr != nil {
			var httpErr *subagentHTTPError
			var modelErr *subagentModelError
			var dialErr *subagentDialError
			var stallErr *subagentStallError
			switch {
			case errors.As(roundErr, &httpErr):
				reason := httpErr.reason
				// همان عقب‌نشینی‌های بدون هزینه‌ی دور: یک‌بار در نوبت.
				if res.streamOptions && isStreamOptionsUnsupported(httpErr.status, reason) {
					markStreamUsageBroken(baseURL)
					round--
					continue
				}
				if !overflowRetried && isContextOverflowError(httpErr.status, reason) {
					overflowRetried = true
					if nh, shrank := recoverHistoryFromOverflow(history, budget, counter); shrank {
						history = nh
						if fixed, problems := validateHistory(history); len(problems) > 0 {
							history = fixed
							counter.invalidate()
						}
						round--
						continue
					}
				}
				logAppErrorFor(a, "subagent", fmt.Sprintf("HTTP %d: %s", httpErr.status, reason))
				return fmt.Sprintf("ERROR: sub-agent stopped: provider returned HTTP %d: %s", httpErr.status, reason), false
			case errors.As(roundErr, &dialErr):
				if ctx.Err() != nil {
					return "ERROR: sub-agent was cancelled before finishing (user stop or time limit).", false
				}
				logAppErrorFor(a, "subagent", "dial: "+dialErr.Error())
				return "ERROR: sub-agent could not reach the provider: " + dialErr.Error(), false
			case errors.As(roundErr, &stallErr):
				// سکون استریم: گزارش خطای قابل‌استفاده به والد (بدون hang).
				logAppErrorFor(a, "subagent", stallErr.Error())
				return fmt.Sprintf("ERROR: sub-agent's stream stalled (no data for %d seconds); the task could not be completed. Retry or split it into smaller steps.", stallErr.seconds), false
			case ctx.Err() != nil:
				return "ERROR: sub-agent was cancelled mid-run (user stop or time limit).", false
			case errors.As(roundErr, &modelErr):
				logAppErrorFor(a, "subagent", "model: "+modelErr.message)
				return "ERROR: sub-agent stopped: model error: " + modelErr.message, false
			default:
				logAppErrorFor(a, "subagent", "stream: "+roundErr.Error())
				return "ERROR: sub-agent's stream broke: " + roundErr.Error(), false
			}
		}

		// ---------- نتیجه‌ی این دور (بدون رویداد UI) ----------
		visFinal, _ := cleanAssistantContent(res.content)
		calls := res.calls
		if len(calls) == 0 {
			return subagentReport(visFinal, toolCalls, toolRounds, promptTokens, completionTokens, started, false), true
		}

		if round >= maxRounds {
			// شمارنده‌ها با اشارهگر می‌روند تا مصرفِ درخواست نهایی هم در همین
			// متغیرها بنشیند و defer بالای تابع (tally) آن را ببیند.
			return a.subagentRoundLimitReport(ctx, sess, history, visFinal, calls, toolCalls, toolRounds, &promptTokens, &completionTokens, started, maxRounds)
		}

		history = append(history, chatMessage{
			Role:      "assistant",
			Content:   visFinal,
			ToolCalls: calls,
		})
		// اجرای ترتیبی (بدون دسته‌ی موازی — زیرایجنت ابزار سبک فقط‌خواندنی
		// دارد و سادگی/قابل‌پیش‌بینی مهم‌تر است) با همان محافظ تکرار.
		// آرگومان‌های برش‌خورده (finish_reason=length) همان hint والد را می‌گیرند
		// تا پیام خطای JSON نیمه‌کاره دقیق باشد (truncationHinted در execSubagentTool).
		toolCtx := ctx
		if res.finish == "length" {
			toolCtx = withTruncationHint(toolCtx)
		}

		tasks, turnStopped, stoppedTool := planToolCalls(calls, guard)
		for _, tk := range tasks {
			// تصمیم محافظ حلقه از پیش محاسبه شده است (مثل executeToolCalls در
			// toolbatch.go): فراخوانیِ مسدودشده/پس-از-توقف اجرا نمی‌شود و نتیجه‌ی
			// آماده‌اش همان پیام tool است. هر فراخوانی دقیقاً یک پیام tool می‌گیرد
			// تا جفت‌بندی tool_calls/tool سالم بماند.
			var result string
			switch {
			case !tk.exec:
				result = tk.result
			case toolCtx.Err() != nil:
				// لغو کاربر (Stop) در میانه‌ی دسته — بقیه اجرا نمی‌شوند اما پیام
				// نتیجه‌ی خودشان را دارند (همان متن حلقه‌ی اصلی).
				result = "ERROR: cancelled by the user before this tool ran."
			default:
				result, _ = a.execSubagentTool(toolCtx, tk.call.Function.Name, tk.call.Function.Arguments)
				toolCalls++ // فقط فراخوانی‌های واقعاً اجراشده
			}
			history = append(history, chatMessage{
				Role:       "tool",
				Content:    truncateToolOutput(result),
				ToolCallID: tk.call.ID,
			})
		}
		toolRounds++ // این دورِ ابزار کامل شد
		if turnStopped {
			return subagentReport(
				"Stopped early: the same tool call repeated too many times ('"+stoppedTool+"'). Partial findings:\n"+visFinal,
				toolCalls, toolRounds, promptTokens, completionTokens, started, false), true
		}
	}
}

// ---------------------------------------------------------------- یک دورِ درخواست

const (
	// پیام result هر فراخوانیِ اجرانشده پس از پر شدن سقف دورها (Bug 2) — جفت‌بندی
	// tool_calls/tool با یک پیام برای هر فراخوانی سالم می‌ماند.
	subagentRoundLimitToolError = "ERROR: round limit reached; no more tool calls are allowed. Write your final report now."

	// پیام کاربر پیش از درخواست نهاییِ گزارش (بدون ابزار).
	subagentFinalReportPrompt = "Round limit reached. Do NOT call any tools. Write your final report now, based only on what you have already read."
)

// subagentSession تنظیمات ثابت یک اجرای زیرایجنت (Provider/مدل/دما/سقف توکن).
type subagentSession struct {
	baseURL             string
	key                 string
	model               string
	temperature         float64
	maxTokens           int
	supportsTemperature bool
}

// subagentRound نتیجه‌ی یک دور کامل مدل (استریم + فراخوانی‌های جمع‌آوری‌شده).
type subagentRound struct {
	content    string
	calls      []apiToolCall
	finish     string
	prompt     int64
	completion int64
	// streamOptions یعنی این درخواست stream_options داشت — برای عقب‌نشینی
	// یک‌باره‌ی حلقه در پاسخ ۴۰۰ مربوط به آن.
	streamOptions bool
}

// subagentHTTPError پاسخ غیر-۲۰۰ سرویس‌دهنده در یک دور.
type subagentHTTPError struct {
	status int
	reason string
}

// خطا رو به مدل است (انگلیسی)، ولی بدنهٔ خام/HTML هم نباید به آن برسد.
func (e *subagentHTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.status, sanitizeErrorSnippet(e.reason, maxSnippetRunes))
}

// subagentModelError خطای داخل بدنه‌ی استریم (chunk.Error).
type subagentModelError struct{ message string }

func (e *subagentModelError) Error() string { return "model error: " + e.message }

// subagentStallError سکون استریم زیرایجنت (داده‌ای نرسید و پاسخ کامل نشد).
type subagentStallError struct{ seconds int }

func (e *subagentStallError) Error() string {
	return fmt.Sprintf("stream stalled: no data for %d seconds", e.seconds)
}

// subagentDialError شکست فاز اتصال (پس از تلاش‌های مجدد dialStream).
type subagentDialError struct{ err error }

func (e *subagentDialError) Error() string { return e.err.Error() }
func (e *subagentDialError) Unwrap() error { return e.err }

// subagentRequest یک دور کامل را اجرا می‌کند: ساخت بدنه، dialStream با تلاش
// مجدد، بررسی وضعیت HTTP و تجزیه‌ی SSE با readSSEChunks (همان تجزیه‌گر
// streamChat — بدون کپی دوم). tools=nil یعنی هیچ ابزاری معرفی نمی‌شود
// (درخواست نهاییِ گزارش پس از پر شدن سقف دورها).
//
// خطاها نوع‌دارند تا حلقه همان رفتار قبلی را حفظ کند: subagentHTTPError
// (عقب‌نشینی‌های stream_options/سرریز)، subagentDialError، subagentModelError
// و در غیر این صورت خطای استریم/لغو.
func (a *App) subagentRequest(ctx context.Context, s *subagentSession, history []chatMessage, tools []toolDef) (subagentRound, error) {
	var out subagentRound
	reqBody := chatRequest{Model: s.model, Messages: history, Stream: true}
	if s.maxTokens > 0 {
		reqBody.MaxTokens = s.maxTokens
	}
	if s.supportsTemperature {
		t := s.temperature
		reqBody.Temperature = &t
	}
	if len(tools) > 0 {
		reqBody.Tools = tools
	}
	if providerSupportsStreamUsage(s.baseURL) {
		reqBody.StreamOptions = &chatStreamOptions{IncludeUsage: true}
	}
	out.streamOptions = reqBody.StreamOptions != nil
	body, _ := json.Marshal(reqBody)
	// نگهبان سکون — همان سیاست گفتگوی اصلی؛ زیرایجنت هم نباید بی‌پایان
	// «فکر کند». مهلت در شروع درخواست خوانده می‌شود.
	a.mu.Lock()
	idleSec := clampStreamIdleSeconds(a.settings.StreamIdleTimeoutSec)
	a.mu.Unlock()
	guard, reqCtx := newStreamIdleGuard(ctx, time.Duration(idleSec)*time.Second)
	// فاز اتصال با تلاش مجدد — بدون اعلان UI؛ تلاش‌ها فقط لاگ
	// می‌شوند تا در «لاگ خطاها» قابل ردیابی باشند.
	resp, err := a.dialStream(reqCtx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, strings.TrimRight(s.baseURL, "/")+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		if s.key != "" {
			req.Header.Set("Authorization", "Bearer "+s.key)
		}
		return req, nil
	}, func(rec retryRecord) {
		kind := "network error"
		if !rec.network {
			kind = fmt.Sprintf("HTTP %d", rec.status)
		}
		logAppErrorFor(a, "subagent", "transient "+kind+"; retrying (attempt "+fmt.Sprint(rec.nextAttempt)+")")
	})
	if err != nil {
		guard.Stop()
		return out, &subagentDialError{err: err}
	}
	// فقط از این لحظه (رسیدن پاسخ) سکون شمرده می‌شود (باگ «قطع بی‌دلیل»).
	guard.Arm()
	if resp.StatusCode != http.StatusOK {
		reason := readErrorBody(resp.Body)
		resp.Body.Close()
		guard.Stop()
		return out, &subagentHTTPError{status: resp.StatusCode, reason: reason}
	}

	// ---------- استریم یک دور (بدون رویداد UI) ----------
	var content strings.Builder
	pend := map[int]*pendingToolCall{}
	modelErr := ""
	scanErr := readSSEChunks(reqCtx, guard.Body(resp.Body), func(chunk sseChunk) bool {
		if chunk.Error != nil && chunk.Error.Message != "" {
			modelErr = chunk.Error.Message
			return true
		}
		if chunk.Usage != nil {
			out.prompt = chunk.Usage.PromptTokens
			out.completion = chunk.Usage.CompletionTokens
		}
		for _, ch := range chunk.Choices {
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				out.finish = *ch.FinishReason
			}
			if ch.Delta.Content != "" {
				content.WriteString(ch.Delta.Content)
			}
			for _, tc := range ch.Delta.ToolCalls {
				p := pend[tc.Index]
				if p == nil {
					p = &pendingToolCall{}
					pend[tc.Index] = p
				}
				if tc.ID != "" {
					p.id = tc.ID
				}
				if tc.Function.Name != "" {
					p.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					p.args.WriteString(tc.Function.Arguments)
				}
			}
		}
		return false
	})
	resp.Body.Close()
	guard.Stop()
	if guard.Stalled() && ctx.Err() == nil {
		return out, &subagentStallError{seconds: idleSec}
	}
	if modelErr != "" {
		return out, &subagentModelError{message: modelErr}
	}
	if scanErr != nil {
		return out, scanErr
	}
	out.content = content.String()
	out.calls = collectToolCalls(pend)
	return out, nil
}

// subagentRoundLimitReport گزارش پایانِ «سقف دورها» را می‌سازد (Bug 2): هیچ
// فراخوانی‌ای اجرا نمی‌شود، اما یک درخواست نهاییِ بدون ابزار متن گزارش را
// می‌گیرد؛ اگر آن هم شکست بخورد یا خالی برگردد، گزارش جایگزینِ قطعی که
// فهرست کارهای بررسی‌شده را دارد برگردانده می‌شود (هیچ‌وقت متن خالی).
func (a *App) subagentRoundLimitReport(ctx context.Context, sess *subagentSession, history []chatMessage, visFinal string, calls []apiToolCall, toolCalls, toolRounds int, promptTokens, completionTokens *int64, started time.Time, maxRounds int) (string, bool) {
	// پیام دستیار و یک پیام tool برای هر فراخوانی: جفت‌بندی پروتکل معتبر
	// می‌ماند، ولی ابزاری اجرا نمی‌شود.
	history = append(history, chatMessage{Role: "assistant", Content: visFinal, ToolCalls: calls})
	for _, c := range calls {
		history = append(history, chatMessage{Role: "tool", Content: subagentRoundLimitToolError, ToolCallID: c.ID})
	}
	history = append(history, chatMessage{Role: "user", Content: subagentFinalReportPrompt})

	// فقط یک درخواست نهایی — بدون حلقه و بدون ابزار.
	// مصرف درخواست نهایی (که کل تاریخچه را دارد و معمولاً بزرگ‌ترین درخواست
	// است) روی همان شمارنده‌های runSubagent جمع می‌شود تا آمار والد کم نشود
	// و سرایند گزارش هم دقیقاً همان اعداد را نشان دهد.
	final, ferr := a.subagentRequest(ctx, sess, history, nil)
	*promptTokens += final.prompt
	*completionTokens += final.completion
	if ferr != nil && ctx.Err() != nil {
		return "ERROR: sub-agent was cancelled mid-run (user stop or time limit).", false
	}
	finalText := ""
	if ferr != nil {
		logAppErrorFor(a, "subagent", "final report request: "+ferr.Error())
	} else {
		// اگر مدل باز هم tool_calls فرستاده باشد نادیده گرفته می‌شوند.
		finalText, _ = cleanAssistantContent(final.content)
	}
	if strings.TrimSpace(finalText) == "" {
		finalText = subagentFallbackReport(history, maxRounds)
	}
	return subagentReport(finalText, toolCalls, toolRounds, *promptTokens, *completionTokens, started, true), true
}

// subagentFallbackReport گزارش جایگزینِ قطعی وقتی درخواست نهایی متن نداد —
// سرایند + فهرست فشرده‌ی آنچه تا اینجا بررسی شده (نام ابزار + اولین آرگومان)
// تا مدل والد بداند کدام بخش‌ها پوشش داده شده‌اند. برش‌ها روی مرز کاراکترند
// (cutUTF8) و تعداد خط‌ها کران‌دار است.
func subagentFallbackReport(history []chatMessage, maxRounds int) string {
	const maxLines = 40
	var b strings.Builder
	fmt.Fprintf(&b, "ROUND LIMIT (%d) REACHED and the final report request produced no text. What the sub-agent had already inspected:\n", maxRounds)
	n := 0
	for _, m := range history {
		if m.Role != "assistant" {
			continue
		}
		for _, c := range m.ToolCalls {
			if n >= maxLines {
				b.WriteString("... (remaining calls omitted)\n")
				return b.String()
			}
			b.WriteString("- " + c.Function.Name + "(" + subagentFirstArg(c.Function.Arguments) + ")\n")
			n++
		}
	}
	if n == 0 {
		b.WriteString("(no tool calls were made)\n")
	}
	return b.String()
}

// subagentFirstArg اولین مقدار آرگومان‌های JSON را (کران‌دار و روی مرز کاراکتر)
// برمی‌گرداند — فقط برای گزارش جایگزین؛ ورودی نامعتبر خودش بریده می‌شود.
func subagentFirstArg(argsJSON string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &m); err == nil && len(m) > 0 {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if v, ok := m[keys[0]].(string); ok {
			return cutUTF8(v, 80)
		}
		if v, err := json.Marshal(m[keys[0]]); err == nil {
			return cutUTF8(string(v), 80)
		}
	}
	return cutUTF8(strings.TrimSpace(argsJSON), 80)
}

// subagentReport گزارش نهایی زیرایجنت را با سرایند آمار می‌سازد — آمار برای
// مدل والد مفید است (هزینه/عمق کار را می‌بیند) و متن کران‌دار می‌شود.
func subagentReport(content string, toolCalls, toolRounds int, promptTokens, completionTokens int64, started time.Time, hitLimit bool) string {
	content = strings.TrimSpace(content)
	if content == "" {
		content = "(the sub-agent produced no final text)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Sub-agent report — %d tool call(s) in %d round(s), ~%d tokens, %s", toolCalls, toolRounds, promptTokens+completionTokens, time.Since(started).Round(time.Millisecond))
	if hitLimit {
		fmt.Fprintf(&b, " — ROUND LIMIT (%d) REACHED, the task may be incomplete", toolRounds)
	}
	b.WriteString(":\n\n")
	b.WriteString(truncateHeadTail(content, maxSubagentResult))
	return b.String()
}

// toolTask نقطه‌ی ورود ابزار task از سمت ایجنت اصلی (والد).
func (a *App) toolTask(ctx context.Context, description string) (string, bool) {
	started := time.Now()
	result, ok := a.runSubagent(ctx, description)
	ms := time.Since(started).Milliseconds()
	if !ok {
		return fmt.Sprintf("%s (after %d ms)", result, ms), false
	}
	return fmt.Sprintf("%s (elapsed %d ms)", result, ms), true
}
