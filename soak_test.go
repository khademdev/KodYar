// تست‌های Stage 5 — soak، تزریق خرابی و دیده‌بان بن‌بست.
//
// soak حلقه‌ی (نوشتن → برگرداندن → چک‌پوینت/بازگردانی → فشرده‌سازی → Stop)
// را چند بار روی هارنس سرتاسری تکرار می‌کند و پس از گرم‌شدن رشد گوروتین،
// heap، توصیف‌کننده‌های فایل و ورودی‌های پوشه‌ی config را می‌سنجد. حالت
// طولانی با KODYAR_SOAK_ITERATIONS فعال می‌شود؛ در گیت عادی ≤۲۰ ثانیه است.
//
// TestFaultInjectionEndStates سناریوهای خرابی (انفجار 429/5xx، SSE خراب،
// panic ابزار، دیسک پر، سکون استریم، کرش/هنگ MCP، git ناموجود/هنگ) را روی
// هارنس واقعی اجرا می‌کند؛ هر سناریو باید با وضعیت سازگار تمام شود.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// runWithDeadline تابع را با مهلت اجرا می‌کند و در صورت عبور، دامپ کامل
// گوروتین‌ها را چاپ می‌کند (کشف بن‌بست/هنگ به‌جای انتظار بی‌پایان تست).
func runWithDeadline(t *testing.T, d time.Duration, name string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("%s panicked: %v", name, r)
			}
		}()
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		t.Fatalf("%s exceeded %s — possible deadlock/hang.\n%s", name, d, buf[:n])
	}
}

// countOpenFDs تعداد توصیف‌کننده‌های باز پروسه‌ی تست را برمی‌گرداند (فقط
// لینوکس که /proc/self/fd دارد؛ سکوهای دیگر ۰ برمی‌گردانند و سنجش رد می‌شود).
func countOpenFDs() int {
	if runtime.GOOS != "linux" {
		return 0
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0
	}
	return len(entries)
}

// countConfigEntries تعداد ورودی‌های (بازگشتی، سطح ۲) پوشه‌ی config را
// می‌شمارد؛ معیار نشت فایل/پوشه در soak.
func countConfigEntries(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		n++
		if e.IsDir() {
			if sub, err := os.ReadDir(filepath.Join(dir, e.Name())); err == nil {
				n += len(sub)
			}
		}
	}
	return n
}

// TestSoakAgentLoop — چرخه‌ی کامل چند بار تکرار می‌شود؛ پس از دو دور
// گرم‌شدن، ماکروها نباید رشد پیوسته داشته باشند.
func TestSoakAgentLoop(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git is not installed")
	}
	iterations := 5
	if v := os.Getenv("KODYAR_SOAK_ITERATIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			iterations = n
		}
	}
	withInstantRetry(t)
	env := newE2E(t)                       // فیلم‌نامه‌ی خالی؛ هر دور مراحل خودش را اضافه می‌کند
	env.writeFile("keep.txt", "keep me\n") // مخزن باید یک کامیت اولیه داشته باشد
	env.initGitWorkspace(t)
	cfgDir, err := env.app.configDir()
	if err != nil {
		t.Fatal(err)
	}

	var baseG int
	var baseHeap uint64
	var baseEntries int
	var baseFDs int
	runWithDeadline(t, 120*time.Second, "soak loop", func() {
		for i := 0; i < iterations; i++ {
			name := fmt.Sprintf("gen_%d.txt", i)
			env.model.addSteps(
				stepTool(fmt.Sprintf("w%d", i), "write_file",
					fmt.Sprintf(`{"path":%q,"content":"value %d\n"}`, name, i)),
				// پای «تست» چرخه‌ی Stage 5.2: اجرای واقعی فرایند در هر دور
				// (spawn/teardown، پایپ‌ها، دروازه‌ی دستور) — «echo» الگوی
				// راستی‌آزمایی نیست، پس تذکر پایان نوبت سر جایش می‌ماند.
				stepTool(fmt.Sprintf("c%d", i), "run_command", `{"command":"echo soak-ok"}`),
				stepText("نوشته شد."),
				// تذکر راستی‌آزمایی پس از نوشتنِ تأییدنشده یک درخواست دیگر
				// مصرف می‌کند (همان‌طور که TestNudgeFiresOnceWhenUnverified
				// نشان می‌دهد) — بدون این مرحله، درخواستِ تذکر مرحله‌ی بعدی
				// (نگه‌داشته‌ی Stop) را می‌بلعد و حلقه هنگ می‌کند.
				stepText("نوشتم ولی راستی‌آزمایی نکردم."),
			)
			env.send("فایل را بساز و دستور را اجرا کن")
			if got := env.readFile(name); got != fmt.Sprintf("value %d\n", i) {
				t.Fatalf("iteration %d: file content %q", i, got)
			}
			if out := env.toolResult(t, fmt.Sprintf("c%d", i)); !strings.Contains(out, "soak-ok") {
				t.Fatalf("iteration %d: run_command output %q", i, out)
			}

			// برگرداندن تغییر فایل ابزار
			var changeID string
			for _, ch := range env.app.GetFileChanges() {
				if ch.Path == name {
					changeID = ch.ID
				}
			}
			if changeID == "" {
				t.Fatalf("iteration %d: change record missing", i)
			}
			if err := env.app.RevertFileChange(changeID); err != nil {
				t.Fatalf("iteration %d: revert: %v", i, err)
			}
			if _, err := os.Stat(filepath.Join(env.ws(), name)); !os.IsNotExist(err) {
				t.Fatalf("iteration %d: reverted file still on disk", i)
			}

			// چک‌پوینت → تغییر → بازگردانی
			turn := int64(i + 1)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, cerr := env.app.createCheckpoint(ctx, env.ws(), env.convID, turn)
			cancel()
			if cerr != nil {
				t.Fatalf("iteration %d: checkpoint: %v", i, cerr)
			}
			env.writeFile(name, "mutated\n")
			rep := env.app.RestoreCheckpoint(env.convID, turn)
			if len(rep.Errors) > 0 {
				t.Fatalf("iteration %d: restore errors: %v", i, rep.Errors)
			}
			if got := env.readFile(name); got == "mutated\n" {
				t.Fatalf("iteration %d: restore did not revert the file", i)
			}

			// فشرده‌سازی کانتکست
			hist := []chatMessage{{Role: "system", Content: "sys"}}
			for j := 0; j < 30; j++ {
				hist = append(hist,
					chatMessage{Role: "user", Content: strings.Repeat("متن ", 200)},
					chatMessage{Role: "assistant", Content: strings.Repeat("پاسخ ", 200)},
				)
			}
			var counter historyTokenCache
			if comp, _ := compactHistoryForBudget(hist, 2000, &counter, false); len(comp) > len(hist) {
				t.Fatalf("iteration %d: compaction grew the history", i)
			}

			// Stop وسط استریم
			hold := make(chan struct{})
			reached := make(chan struct{}, 1)
			env.model.addSteps(scenarioStep{Text: "پاسخ نیمه‌کاره", Hold: hold, Reached: reached})
			if _, err := env.app.SendMessage(env.convID, "سلام", nil); err != nil {
				t.Fatalf("iteration %d: send: %v", i, err)
			}
			waitReached(t, reached)
			env.app.StopGeneration()
			close(hold)
			waitStreamDone(t, env.app)

			if i == 1 { // گرم‌شدن: دو دور اول مجاز به رشدند
				runtime.GC()
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				baseG = runtime.NumGoroutine()
				baseHeap = ms.HeapAlloc
				baseEntries = countConfigEntries(t, cfgDir)
				baseFDs = countOpenFDs()
			}
		}
	})

	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if g := runtime.NumGoroutine(); g > baseG+8 {
		t.Fatalf("goroutine leak in soak: %d → %d", baseG, g)
	}
	if ms.HeapAlloc > baseHeap+32<<20 {
		t.Fatalf("heap growth in soak: %d → %d bytes", baseHeap, ms.HeapAlloc)
	}
	if n := countConfigEntries(t, cfgDir); n > baseEntries+120 {
		t.Fatalf("config dir growth in soak: %d → %d entries", baseEntries, n)
	}
	if fds := countOpenFDs(); fds > 0 && fds > baseFDs+16 {
		t.Fatalf("open fd growth in soak: %d → %d", baseFDs, fds)
	}
	// گفتگوی نهایی باید معتبر و بدون «در حال تولید» باشد.
	assertTurnEndsClean(t, env.app, env.convID)
}

// assertTurnEndsClean وضعیت پایانی سازگار را می‌سنجد: هیچ پاسخ‌دهی فعالی
// نمانده، فایل گفتگو JSON معتبر است و جفت‌بندی tool_calls↔tool سالم است.
func assertTurnEndsClean(t *testing.T, a *App, convID string) {
	t.Helper()
	a.genMu.Lock()
	g := a.gen
	a.genMu.Unlock()
	if g != nil {
		t.Fatal("a generation is still registered after the turn (UI would stay 'generating')")
	}
	conv, err := a.loadConversationForTest(convID)
	if err != nil {
		t.Fatalf("conversation unreadable: %v", err)
	}
	calls := map[string]bool{}
	for _, m := range conv.Messages {
		if m.Role == "assistant" {
			for _, c := range m.ToolCalls {
				calls[c.ID] = true
			}
		}
	}
	for _, m := range conv.Messages {
		if m.Role == "tool" && m.ToolCallID != "" && !calls[m.ToolCallID] {
			t.Fatalf("orphan tool result %q", m.ToolCallID)
		}
	}
	dir, err := a.conversationsDir()
	if err != nil {
		t.Fatal(err)
	}
	if raw, rerr := os.ReadFile(convPath(dir, convID)); rerr == nil && !json.Valid(raw) {
		t.Fatalf("conversation file is not valid JSON")
	}
}

// TestFaultInjectionEndStates — تزریق خرابی‌های واقعی؛ هر سناریو باید با
// حالت سازگار تمام شود (نه هنگ، نه «در حال تولید»، فایل معتبر).
func TestFaultInjectionEndStates(t *testing.T) {
	t.Run("rate_limit_burst_then_success", func(t *testing.T) {
		withInstantRetry(t)
		env := newE2E(t,
			scenarioStep{Status: 429},
			scenarioStep{Status: 503},
			stepText("پاسخ نهایی پس از تلاش مجدد"),
		)
		runWithDeadline(t, 30*time.Second, "rate_limit scenario", func() {
			conv := env.send("سلام")
			if got := lastAssistant(conv); !strings.Contains(got, "پاسخ نهایی") {
				t.Fatalf("retry burst must end with the real answer, got %q", got)
			}
		})
		assertTurnEndsClean(t, env.app, env.convID)
	})

	t.Run("malformed_sse_ends_cleanly", func(t *testing.T) {
		env := newE2E(t, scenarioStep{Raw: "data: {this is not json\n\n"})
		runWithDeadline(t, 30*time.Second, "malformed_sse scenario", func() {
			env.send("سلام")
		})
		assertTurnEndsClean(t, env.app, env.convID)
	})

	t.Run("tool_panic_keeps_pairing", func(t *testing.T) {
		a := newTestApp(t)
		conv := &Conversation{ID: newID(), Title: "panic", CreatedAt: time.Now().Unix()}
		a.mu.Lock()
		if err := a.saveConversation(conv); err != nil {
			a.mu.Unlock()
			t.Fatal(err)
		}
		a.mu.Unlock()
		ctx := withToolCallInfo(context.Background(), conv.ID, "c1")
		c1 := apiToolCall{ID: "c1"}
		c1.Function.Name, c1.Function.Arguments = "write_file", `{}`
		c2 := apiToolCall{ID: "c2"}
		c2.Function.Name, c2.Function.Arguments = "write_file", `{}`
		tasks := []toolBatchTask{{call: c1, exec: true}, {call: c2, exec: true}}
		calls := 0
		outcomes := a.executeToolCalls(ctx, conv.ID, tasks, false, func(_ context.Context, _, _ string) (string, bool) {
			calls++
			if calls == 1 {
				panic("injected tool panic")
			}
			return "ok", true
		})
		if len(outcomes) != 2 {
			t.Fatalf("outcomes = %d, want 2 (pairing must be preserved)", len(outcomes))
		}
		if !strings.Contains(outcomes[0].result, "internal error") || outcomes[0].ok {
			t.Fatalf("panicking tool must produce an English internal error, got %+v", outcomes[0])
		}
		if !outcomes[1].ok || outcomes[1].result != "ok" {
			t.Fatalf("the other tool must still run, got %+v", outcomes[1])
		}
		assertTurnEndsClean(t, a, conv.ID)
	})

	t.Run("disk_full_mid_turn_surfaces_notice_and_keeps_file", func(t *testing.T) {
		srv := newScriptedModel(t, stepText("پاسخ ذخیره‌نشدنی"))
		a := chatAppOnServer(t, srv.URL())
		// جمع‌آوری اعلان‌های chat:notice (runtimeEmit در تست به همین قلاب می‌رود).
		var mu sync.Mutex
		var notices []string
		a.eventEmit = func(name string, data []interface{}) {
			if name != "chat:notice" || len(data) == 0 {
				return
			}
			if m, ok := data[0].(map[string]interface{}); ok {
				if s, ok := m["text"].(string); ok {
					mu.Lock()
					notices = append(notices, s)
					mu.Unlock()
				}
			}
		}
		conv := &Conversation{ID: newID(), Title: "disk", CreatedAt: time.Now().Unix()}
		a.mu.Lock()
		if err := a.saveConversation(conv); err != nil {
			a.mu.Unlock()
			t.Fatal(err)
		}
		a.mu.Unlock()
		dir, _ := a.conversationsDir()
		target := convPath(dir, conv.ID)

		// دیسک فقط از «دومین ذخیره‌ی فایل گفتگو» به بعد پر می‌شود: ذخیره‌ی
		// پیام کاربر موفق می‌ماند، نوبت اجرا می‌شود و ذخیره‌ی پاسخ نهایی
		// شکست می‌خورد — همان لحظه‌ی واقعیِ پرشدن دیسک وسط نوبت.
		saves := 0
		old := atomicWriteHook
		atomicWriteHook = func(path string) error {
			if path == target {
				saves++
				if saves >= 2 {
					return syscall.ENOSPC
				}
			}
			return nil
		}
		t.Cleanup(func() { atomicWriteHook = old })

		runWithDeadline(t, 30*time.Second, "disk_full scenario", func() {
			if _, err := a.SendMessage(conv.ID, "سلام", nil); err != nil {
				t.Fatal(err)
			}
			waitStreamDone(t, a)
		})

		assertTurnEndsClean(t, a, conv.ID)
		mu.Lock()
		noticeFound := false
		for _, n := range notices {
			if strings.Contains(n, "ذخیره‌ی این گفتگو روی دیسک ناموفق بود") {
				noticeFound = true
			}
		}
		mu.Unlock()
		if !noticeFound {
			t.Fatalf("disk-full Persian notice missing, notices=%v", notices)
		}
		// فایل قبلی سالم مانده است: JSON معتبر، پیام کاربر سر جاش، بدون برش.
		raw, rerr := os.ReadFile(target)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if !json.Valid(raw) {
			t.Fatal("disk-full must never truncate the existing conversation file")
		}
		if !strings.Contains(string(raw), "سلام") {
			t.Fatal("existing user message lost on disk-full")
		}
	})

	t.Run("stall_mid_stream_saves_partial_no_retry", func(t *testing.T) {
		withFastStreamIdle(t, 1) // مهلت بیکاری تزریقی: ~۱ ثانیه
		withInstantRetry(t)
		env := newE2E(t, scenarioStep{Text: "پاسخ نیمه", Stall: true})
		logPath := redirectToTestLog(t)
		runWithDeadline(t, 30*time.Second, "stall scenario", func() {
			conv := env.send("سلام")
			// پیام خطای ماندگار بعد از پاسخ نیمه‌کاره می‌آید؛ خودِ پاسخ
			// نیمه‌کاره باید با Aborted در گفتگو باقی بماند.
			var partial *Message
			for i := range conv.Messages {
				if conv.Messages[i].Role == "assistant" && strings.Contains(conv.Messages[i].Content, "پاسخ نیمه") && conv.Messages[i].Aborted {
					partial = &conv.Messages[i]
				}
			}
			if partial == nil {
				t.Fatalf("partial answer must be saved on mid-stream stall; messages=%+v", conv.Messages)
			}
		})
		if n := env.model.count(); n != 1 {
			t.Fatalf("mid-stream stall must NOT auto-retry, requests=%d", n)
		}
		waitErrorLogContains(t, logPath, "دریافت پاسخ متوقف شد")
		assertTurnEndsClean(t, env.app, env.convID)
	})

	t.Run("mcp_server_crash_fails_tool_and_turn_ends", func(t *testing.T) {
		env := newE2E(t,
			stepTool("m1", "mcp_fake_echo", `{"text":"سلام"}`),
			stepText("پایان."),
		)
		cmd, args := fakeMCPCommand("fake-crash") // بلافاصله خارج می‌شود
		env.app.mu.Lock()
		env.app.settings.MCPServers = []MCPServerConfig{
			{Name: "fake", Command: cmd, Args: args, Enabled: true},
		}
		env.app.mu.Unlock()
		defer env.app.mcp.stopAll()

		var toolResult string
		runWithDeadline(t, 30*time.Second, "mcp crash scenario", func() {
			env.send("از ابزار mcp_fake_echo استفاده کن")
			toolResult = env.toolResult(t, "m1")
		})
		if !strings.HasPrefix(toolResult, "ERROR:") {
			t.Fatalf("tool call against a crashed MCP server must return an ERROR result, got %q", toolResult)
		}
		// نوبت با پاسخ نهایی مدل تمام می‌شود؛ گفتگو سازگار می‌ماند.
		conv := env.lastConv(t)
		if got := lastAssistant(conv); !strings.Contains(got, "پایان.") {
			t.Fatalf("turn must finish with the model's final text, got %q", got)
		}
		assertTurnEndsClean(t, env.app, env.convID)
	})

	t.Run("mcp_hanging_tool_times_out_and_turn_ends", func(t *testing.T) {
		restore := setMCPCallTimeoutForTest(1200 * time.Millisecond)
		defer restore()
		env := newE2E(t,
			stepTool("m1", "mcp_fake_slow", `{}`),
			stepText("پایان."),
		)
		cmd, args := fakeMCPCommand("fake-ok")
		env.app.mu.Lock()
		env.app.settings.MCPServers = []MCPServerConfig{
			{Name: "fake", Command: cmd, Args: args, Enabled: true},
		}
		env.app.mu.Unlock()
		defer env.app.mcp.stopAll()
		if _, err := env.app.mcp.ensureStarted(context.Background(), env.app, env.app.settings.MCPServers[0]); err != nil {
			t.Fatalf("fake server did not start: %v", err)
		}

		var toolResult string
		runWithDeadline(t, 30*time.Second, "mcp hang scenario", func() {
			env.send("ابزار آهسته را صدا بزن")
			toolResult = env.toolResult(t, "m1")
		})
		if !strings.Contains(toolResult, "ERROR") || !strings.Contains(toolResult, "timed out after") {
			t.Fatalf("hanging MCP tool must fail with the timeout message, got %q", toolResult)
		}
		assertTurnEndsClean(t, env.app, env.convID)
	})

	t.Run("git_missing_clear_error_then_recovers", func(t *testing.T) {
		if !gitAvailable() {
			t.Skip("git is not installed")
		}
		a := newTestApp(t)
		ws := a.settings.WorkspacePath
		initRepo(t, ws)
		conv := &Conversation{ID: newID(), Title: "gitless", CreatedAt: time.Now().Unix()}
		a.mu.Lock()
		if err := a.saveConversation(conv); err != nil {
			a.mu.Unlock()
			t.Fatal(err)
		}
		a.mu.Unlock()

		empty := t.TempDir()
		orig := os.Getenv("PATH")
		t.Setenv("PATH", empty) // هیچ gitای در دسترس نیست
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := a.createCheckpoint(ctx, ws, conv.ID, 1)
		cancel()
		if err == nil {
			t.Fatal("checkpoint without git must fail with a clear error")
		}
		if !strings.Contains(err.Error(), "git") {
			t.Fatalf("error must name git, got: %v", err)
		}
		t.Setenv("PATH", orig) // برگشت مسیر — برنامه باید سالم بماند
		ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
		_, err2 := a.createCheckpoint(ctx2, ws, conv.ID, 1)
		cancel2()
		if err2 != nil {
			t.Fatalf("checkpoint must succeed again once git is available: %v", err2)
		}
	})

	t.Run("git_hang_times_out_and_leaves_no_orphan", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("posix pidfile/orphan check")
		}
		if !gitAvailable() {
			t.Skip("git is not installed")
		}
		a := newTestApp(t)
		ws := a.settings.WorkspacePath
		initRepo(t, ws)

		dir := t.TempDir()
		pidFile := filepath.Join(dir, "hang.pid")
		// git جعلی: PID خودش را می‌نویسد، یک فرزند پس‌زمینه می‌سازد
		// (PID آن هم ثبت می‌شود) و بعد هنگ می‌کند — بررسی «فرزندِ فرزند»
		// با الگوی TestRunShellTimeoutKillsChildTree: بعد از مهلت، نه
		// فقط git جعلی بلکه فرزندِ ساخته‌ی آن هم باید مرده باشد (بدون
		// یتیم). («exec sleep» قبلی هیچ نوه‌ای نمی‌ساخت و کشتنِ درخت
		// را اثبات نمی‌داد.)
		childPidFile := filepath.Join(dir, "child.pid")
		script := "#!/bin/sh\necho $$ > " + pidFile + "\nsleep 60 &\necho $! > " + childPidFile + "\nwait\n"
		if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		orig := os.Getenv("PATH")
		t.Setenv("PATH", dir+string(os.PathListSeparator)+orig)
		oldTimeout := checkpointTimeout
		checkpointTimeout = 400 * time.Millisecond
		t.Cleanup(func() { checkpointTimeout = oldTimeout })

		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := a.createCheckpoint(ctx, ws, newID(), 1)
		cancel()
		elapsed := time.Since(start)
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("hanging git must produce a timeout error, got: %v", err)
		}
		if elapsed > 3*time.Second {
			t.Fatalf("checkpoint must respect the (shortened) git timeout, took %s", elapsed)
		}
		// بدون فرایند یتیم: git جعلی و فرزند پس‌زمینه‌اش هر دو باید تا
		// چند صد میلی‌ثانیه بمیرند (کشتن گروه فرایند، نه فقط فرزند مستقیم).
		waitPIDGone(t, pidFile, "fake git")
		waitPIDGone(t, childPidFile, "fake git's background child")
	})
}

// waitPIDGone منتظر می‌ماند تا پروسه‌ی PID ثبت‌شده در فایل از /proc برود.
func waitPIDGone(t *testing.T, pidFile, what string) {
	t.Helper()
	raw, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("%s never wrote its pid: %v", what, rerr)
	}
	pid := strings.TrimSpace(string(raw))
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, lerr := os.Stat(filepath.Join("/proc", pid)); lerr != nil {
			return // دیگر وجود ندارد — کشته شده
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphan %s process %s is still alive", what, pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// initRepo پوشه را با یک کامیت اولیه به مخزن git تبدیل می‌کند (کمکی مشترک
// سناریوهای خرابی git؛ محیط نویسنده صریح تا به تنظیمات میزبان وابسته نباشد).
func initRepo(t *testing.T, ws string) {
	t.Helper()
	// مخزن خالی قابل کامیت نیست؛ یک فایل نگه‌دارنده می‌سازیم (همان الگوی
	// keep.txt در TestSoakAgentLoop).
	if err := os.WriteFile(filepath.Join(ws, "keep.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", ws}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=tester", "GIT_AUTHOR_EMAIL=tester@localhost",
			"GIT_COMMITTER_NAME=tester", "GIT_COMMITTER_EMAIL=tester@localhost",
			"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("add", "-A")
	run("commit", "-q", "-m", "init")
}
