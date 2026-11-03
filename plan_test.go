// تست‌های حالت برنامه‌ریزی (اولویت ۵).
package main

import (
	"strings"
	"testing"
	"time"
)

func TestPlanModePromptInjection(t *testing.T) {
	a := newTestApp(t)
	a.settings.Provider = "ollama"
	a.settings.Model = "llama3.2"
	a.settings.AgentMode = false

	// بدون plan mode → بدون بلوک
	conv := &Conversation{ID: newID(), Title: "t", Messages: []Message{
		{ID: newID(), Role: "user", Content: "سلام"},
	}}
	h1, err := a.buildHistoryForTest(conv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(contentStr(h1[0].Content), "PLANNING MODE") {
		t.Fatal("plan block must not appear when plan mode is off")
	}

	// فراخوانی مستقیم prepareStream با planMode=true
	a.mu.Lock()
	_, _, _, _, h2, err2 := a.prepareStream(conv, true, "")
	a.mu.Unlock()
	if err2 != nil {
		t.Fatal(err2)
	}
	if !strings.Contains(contentStr(h2[0].Content), "PLANNING MODE is ACTIVE") ||
		!strings.Contains(contentStr(h2[0].Content), "Do NOT call any tools") {
		t.Fatal("plan block missing in system prompt")
	}
}

func TestApprovePlanFlow(t *testing.T) {
	a := newTestApp(t)
	a.settings.Provider = "ollama"
	a.settings.Model = "llama3.2"
	conv := &Conversation{ID: newID(), Title: "t", Messages: []Message{
		{ID: newID(), Role: "user", Content: "یک صفحه بساز"},
		{ID: newID(), Role: "assistant", Content: "۱. فایل index.html می‌سازم\n۲. محتوای فارسی اضافه می‌کنم", At: time.Now().Unix()},
	}}
	a.mu.Lock()
	if err := a.saveConversation(conv); err != nil {
		t.Fatal(err)
	}
	a.mu.Unlock()

	// تأیید برنامه → پیام تأیید + شروع استریم (استریم واقعی نیست؛ فقط ساختار)
	updated, err := a.ApprovePlan(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Messages) != 3 {
		t.Fatalf("approval message must be appended: %d msgs", len(updated.Messages))
	}
	last := updated.Messages[len(updated.Messages)-1]
	if last.Role != "user" || !strings.Contains(contentStr(last.Content), "تأیید شد") {
		t.Fatalf("approval message wrong: %+v", last)
	}
	// استریم فعال شده باشد (gen!= nil) و بلافاصله متوقفش کنیم
	a.genMu.Lock()
	gen := a.gen
	a.genMu.Unlock()
	if gen == nil {
		t.Fatal("generation must be active after approval")
	}
	a.StopGeneration()
	// صبر تا گوروتین استریم کاملاً تمام شود — وگرنه نوشتنِ هم‌زمان آن
	// در پاک‌سازی TempDir تداخل می‌کند («directory not empty»).
	waitForGenDone(t, a)

	// بدون برنامه (آخرین پیام user) → خطا
	conv2 := &Conversation{ID: newID(), Title: "t2", Messages: []Message{
		{ID: newID(), Role: "user", Content: "سلام"},
	}}
	a.mu.Lock()
	_ = a.saveConversation(conv2)
	a.mu.Unlock()
	if _, err := a.ApprovePlan(conv2.ID); err == nil {
		t.Fatal("approve without a plan must fail")
	}
}

func TestSetPlanModePersists(t *testing.T) {
	a := newTestApp(t)
	if err := a.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	if !a.settings.PlanMode {
		t.Fatal("plan mode must be on")
	}
}
