// تست‌های دروازهٔ پایان کار (Finish Gate).
//
// سه چیز تضمین می‌شود:
//
//	۱. چک‌لیست باز در شروع نوبت ایجنت به مدل داده می‌شود.
//	۲. پایان نوبت با چک‌لیست باز پذیرفته نمی‌شود — حداکثر دو تذکر.
//	۳. سقف‌ها رعایت می‌شوند و چت معمولی/بدون ابزار هرگز گیت نمی‌گیرد.
package main

import (
	"strings"
	"testing"
)

func TestOpenTodoItemsFiltersCompleted(t *testing.T) {
	items := []TodoItem{
		{ID: "1", Content: "design", Status: todoStatusCompleted},
		{ID: "2", Content: "implement", Status: todoStatusInProgress},
		{ID: "3", Content: "test", Status: todoStatusPending},
		{ID: "4", Content: "  ", Status: todoStatusPending},
		{ID: "5", Content: "docs", Status: ""},
	}
	open := openTodoItems(items)
	if len(open) != 3 {
		t.Fatalf("expected 3 open items, got %d: %+v", len(open), open)
	}
	if open[0].ID != "2" || open[1].ID != "3" || open[2].ID != "5" {
		t.Fatalf("open items must keep order and skip completed/empty: %+v", open)
	}
	if len(openTodoItems(nil)) != 0 {
		t.Fatal("nil list has no open items")
	}
}

func TestTodoResumeBlockOnlyWhenOpen(t *testing.T) {
	if got := todoResumeBlock(nil); got != "" {
		t.Fatalf("empty checklist must not inject a block, got %q", got)
	}
	done := []TodoItem{{ID: "1", Content: "done", Status: todoStatusCompleted}}
	if got := todoResumeBlock(done); got != "" {
		t.Fatalf("fully completed checklist must not inject a block, got %q", got)
	}
	open := []TodoItem{
		{ID: "1", Content: "refactor chat loop", Status: todoStatusInProgress},
		{ID: "2", Content: "run tests", Status: todoStatusPending},
	}
	block := todoResumeBlock(open)
	if !strings.Contains(block, "carried over") || !strings.Contains(block, "refactor chat loop") {
		t.Fatalf("resume block must name the carried-over items:\n%s", block)
	}
	if strings.Contains(block, "1. [completed]") {
		t.Fatalf("completed items must not be listed:\n%s", block)
	}
	if len([]rune(block)) > finishGateResumeMaxRunes {
		t.Fatalf("resume block must stay bounded, got %d runes", len([]rune(block)))
	}
}

func TestFinishGateNudgeText(t *testing.T) {
	if got := finishGateNudge(nil, true, []string{"a.go"}); got != "" {
		t.Fatalf("no open items → the existing verification nudge handles it, got %q", got)
	}
	open := []TodoItem{{ID: "1", Content: "wire the gate", Status: todoStatusInProgress}}
	if got := finishGateNudge(open, false, nil); !strings.Contains(got, "checklist is not closed") {
		t.Fatalf("gate text must explain the open checklist:\n%s", got)
	}
	withVerify := finishGateNudge(open, true, []string{"chat.go", "finishgate.go"})
	if !strings.Contains(withVerify, "no successful verification") {
		t.Fatalf("gate must also demand verification when the last change is unverified:\n%s", withVerify)
	}
	if !strings.Contains(withVerify, "chat.go") {
		t.Fatalf("gate should point at the changed files:\n%s", withVerify)
	}
	if len([]rune(withVerify)) > finishGateNudgeMaxRunes {
		t.Fatalf("nudge must stay bounded, got %d runes", len([]rune(withVerify)))
	}
	if !strings.Contains(finishGateNotice(3, 2), "۳") {
		t.Fatalf("notice must use Persian digits: %q", finishGateNotice(3, 2))
	}
}

// TestFinishGateBlocksPrematureFinish: مدل بعد از todo_write ناتمام «انجام شد»
// می‌گوید؛ گیت باید یک دور دیگر بدهد و بعد از بسته‌شدن چک‌لیست اجازهٔ پایان.
func TestFinishGateBlocksPrematureFinish(t *testing.T) {
	env := newE2E(t,
		stepTool("c1", "todo_write", `{"todos":[{"id":"1","content":"create note.txt","status":"in_progress"},{"id":"2","content":"finish","status":"pending"}]}`),
		stepText("انجام شد."),
		stepTool("c2", "todo_write", `{"todos":[{"id":"1","content":"create note.txt","status":"completed"},{"id":"2","content":"finish","status":"completed"}]}`),
		stepText("همه‌ی کارها تمام شد."),
	)
	conv := env.send("کار را انجام بده")

	if n := env.model.count(); n != 4 {
		t.Fatalf("expected 4 model requests (todo → gate → todo → final), got %d", n)
	}
	reqs := env.model.requests()
	if strings.Contains(requestMessages(reqs[1]), "checklist is not closed") {
		t.Fatal("gate must not fire before the model's first final answer")
	}
	if !strings.Contains(requestMessages(reqs[2]), "checklist is not closed") {
		t.Fatalf("gate text must be in the request after the premature final answer:\n%s", requestMessages(reqs[2]))
	}
	last := lastAssistantOf(t, conv)
	if !strings.Contains(last.Content, "تمام شد") {
		t.Fatalf("final answer must survive: %q", last.Content)
	}
	found := false
	for _, n := range env.noticesText() {
		if strings.Contains(n, "چک‌لیست") {
			found = true
		}
	}
	if !found {
		t.Fatalf("user must see a notice about the open checklist, notices=%v", env.noticesText())
	}
}

// TestFinishGateCapAndMaxRounds: مدل هیچ‌وقت چک‌لیست را نمی‌بندد؛ گیت باید
// حداکثر دو تذکر بدهد و بعد نوبت (با احترام به سقف دورها) تمام شود.
func TestFinishGateCapAndMaxRounds(t *testing.T) {
	env := newE2E(t,
		stepTool("c1", "todo_write", `{"todos":[{"id":"1","content":"never done","status":"in_progress"}]}`),
		stepText("تمام شد."),
		stepText("باز هم تمام شد."),
		stepText("دیگر واقعاً تمام شد."),
	)
	env.app.mu.Lock()
	env.app.settings.MaxAgentRounds = 6
	env.app.mu.Unlock()
	env.send("کار")

	if n := env.model.count(); n != 4 {
		t.Fatalf("expected 4 requests (todo + 3 finals where only 2 are nudged), got %d", n)
	}
	nudges := 0
	for _, txt := range env.noticesText() {
		if strings.Contains(txt, "چک‌لیست") {
			nudges++
		}
	}
	if nudges != maxFinishGateNudges {
		t.Fatalf("expected exactly %d checklist nudges, got %d (%v)", maxFinishGateNudges, nudges, env.noticesText())
	}
}

// TestTodoChecklistInjectedAtTurnStart: چک‌لیستِ ذخیره‌شده روی گفتگو در اولین
// درخواست نوبت جدید به مدل دیده می‌شود (نه فقط در فشرده‌سازی).
func TestTodoChecklistInjectedAtTurnStart(t *testing.T) {
	env := newE2E(t, stepText("ادامه دادم."))
	conv, err := env.app.loadConversationForTest(env.convID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	conv.Todos = []TodoItem{
		{ID: "1", Content: "finish the finish gate", Status: todoStatusInProgress},
		{ID: "2", Content: "run the suite", Status: todoStatusPending},
	}
	env.app.mu.Lock()
	if err := env.app.saveConversation(conv); err != nil {
		env.app.mu.Unlock()
		t.Fatalf("save: %v", err)
	}
	env.app.mu.Unlock()

	env.send("ادامه بده")

	reqs := env.model.requests()
	if len(reqs) == 0 {
		t.Fatal("no model request")
	}
	first := requestMessages(reqs[0])
	if !strings.Contains(first, "carried over") || !strings.Contains(first, "finish the finish gate") {
		t.Fatalf("open checklist must be injected at turn start:\n%s", first)
	}
}

// TestCarriedOverChecklistBlocksFinish: چک‌لیستی که از نوبت قبل باز مانده
// (بدون هیچ todo_write در نوبت جدید) هم باید پایان نوبت را ببندد؛ پیش از
// این اصلاح، رکورد نوبت خالی بود و گیت این حالت را نمی‌دید.
func TestCarriedOverChecklistBlocksFinish(t *testing.T) {
	env := newE2E(t,
		stepTool("t1", "todo_write", `{"todos":[{"id":"1","content":"write the report","status":"in_progress"}]}`),
		stepText("شروع کردم."),
		stepText("ادامه می‌دهم."),
		stepText("باز هم ادامه می‌دهم."),
	)
	conv := env.send("گزارش را بنویس")
	if len(conv.Todos) != 1 {
		t.Fatalf("turn 1 must leave the checklist on the conversation: %+v", conv.Todos)
	}

	// نوبت دوم: مدل هیچ todo_write‌ای صدا نمی‌زند و فقط متن می‌دهد.
	before := env.model.count()
	env.model.addSteps(
		stepText("گزارش را نوشتم."),
		stepText("تأیید شد، تمام است."),
		stepText("واقعاً تمام است."),
	)
	env.send("ادامه بده")
	reqs := env.model.requests()
	if got := env.model.count() - before; got != 3 {
		t.Fatalf("carried-over checklist must keep the turn open for 2 nudges, got %d requests", got)
	}
	turn2 := reqs[before:]
	if !strings.Contains(requestMessages(turn2[0]), "carried over") {
		t.Fatal("turn 2 must receive the carried-over checklist")
	}
	if strings.Contains(requestMessages(turn2[0]), "checklist is not closed") {
		t.Fatal("gate must not fire before the model's first final answer")
	}
	if !strings.Contains(requestMessages(turn2[1]), "checklist is not closed") {
		t.Fatalf("a carried-over open item must block the finish:\n%s", requestMessages(turn2[1]))
	}
}

// TestMaxRoundsEndsUnfinishedAndResumes: پر شدن سقف دورهای ایجنت نباید بن‌بست
// باشد. پیام پایانی باید «نیمه‌کاره» علامت بخورد (نه «متوقف شد») و همان دکمهٔ
// «ادامه بده» (ResumeAgent) واقعاً از همان‌جا ادامه دهد.
func TestMaxRoundsEndsUnfinishedAndResumes(t *testing.T) {
	env := newE2E(t,
		stepTool("r1", "write_file", `{"path":"a.txt","content":"1\n"}`),
		stepTool("r2", "write_file", `{"path":"b.txt","content":"2\n"}`),
		stepTool("r3", "write_file", `{"path":"c.txt","content":"3\n"}`),
		stepTool("r4", "write_file", `{"path":"d.txt","content":"4\n"}`),
		stepTool("r5", "write_file", `{"path":"e.txt","content":"5\n"}`),
		// درخواست ششم فقط برای رسیدن به بررسی سقف است؛ ابزارش اجرا نمی‌شود.
		stepTool("r6", "write_file", `{"path":"f.txt","content":"6\n"}`),
	)
	env.app.mu.Lock()
	env.app.settings.MaxAgentRounds = 5
	env.app.mu.Unlock()

	conv := env.send("پنج فایل بساز")
	// سقف در ابتدای دور ششم بررسی می‌شود، پس یک درخواست بیشتر از سقف
	// ساخته می‌شود و ابزارهایش اجرا نمی‌شوند.
	if n := env.model.count(); n != 6 {
		t.Fatalf("expected 6 requests (5 rounds + cap check), got %d", n)
	}
	last := lastAssistantOf(t, conv)
	if !last.Unfinished {
		t.Fatalf("a capped turn must be marked unfinished: %+v", last)
	}
	if last.Aborted {
		t.Fatal("hitting the round cap is not a user stop; Aborted must stay false")
	}
	if !strings.Contains(last.Content, "ادامه بده") {
		t.Fatalf("the capped turn must point at the resume button: %q", last.Content)
	}

	// یک کلیک واقعی روی «ادامه بده».
	before := env.model.count()
	env.model.addSteps(stepText("ادامه دادم و کار تمام شد."))
	if _, err := env.app.ResumeAgent(env.convID); err != nil {
		t.Fatalf("ResumeAgent after the cap: %v", err)
	}
	waitStreamDone(t, env.app)

	if got := env.model.count() - before; got != 1 {
		t.Fatalf("one click must add exactly one model round, got %d", got)
	}
	reqs := env.model.requests()
	if !strings.Contains(requestMessages(reqs[before]), "حداکثر تعداد مراحل") {
		t.Fatalf("the resumed request must carry the capped-turn context:\n%s", requestMessages(reqs[before]))
	}
	final := lastAssistantOf(t, env.lastConv(t))
	if final.Unfinished || final.Aborted {
		t.Fatalf("the resumed turn must finish cleanly: %+v", final)
	}
	if !strings.Contains(final.Content, "تمام شد") {
		t.Fatalf("resumed answer must be kept: %q", final.Content)
	}
}

// TestUserStopStaysAbortedNotUnfinished: توقف واقعی کاربر همان «متوقف شد»
// می‌ماند و با حالت نیمه‌کاره قاطی نمی‌شود.
func TestUserStopStaysAbortedNotUnfinished(t *testing.T) {
	hold := make(chan struct{})
	reached := make(chan struct{}, 1)
	env := newE2E(t, scenarioStep{Text: "پاسخ نیمه‌کاره", Hold: hold, Reached: reached})
	env.app.mu.Lock()
	env.app.settings.MaxAgentRounds = 5
	env.app.mu.Unlock()

	if _, err := env.app.SendMessage(env.convID, "سلام", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitReached(t, reached)
	env.app.StopGeneration()
	close(hold)
	waitStreamDone(t, env.app)

	last := lastAssistantOf(t, env.lastConv(t))
	if !last.Aborted {
		t.Fatalf("a user stop must be marked aborted: %+v", last)
	}
	if last.Unfinished {
		t.Fatal("a user stop must not be reported as an unfinished (resumable) turn")
	}
}
