// تست رویداد «مصرف زنده» (chat:usage).
//
// دایره‌ی توکن باید در طول پاسخ هم عدد بدهد، نه فقط در chat:done؛ وگرنه
// کاربر هنگام پاسخ‌دهی هیچ بازخوردی از مصرف نمی‌بیند. این تست تضمین می‌کند
// که پس از هر دورِ استریم، رویداد با کانتکست واقعیِ همان درخواست، سقف
// پنجره‌ی کانتکست و جمع نوبت به UI فرستاده می‌شود.
package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestStreamEmitsLiveUsage(t *testing.T) {
	redirectToTestLog(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseFinalWithUsage(w, "پاسخ آزمایشی", 1234, 56)
	}))
	defer srv.Close()

	a := agentStreamApp(t, srv.URL)

	var mu sync.Mutex
	var live []map[string]interface{}
	a.eventEmit = func(name string, data []interface{}) {
		if name != "chat:usage" || len(data) == 0 {
			return
		}
		if m, ok := data[0].(map[string]interface{}); ok {
			mu.Lock()
			live = append(live, m)
			mu.Unlock()
		}
	}

	conv, err := a.SendMessage("", "سلام", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitStreamDone(t, a)

	mu.Lock()
	defer mu.Unlock()
	if len(live) == 0 {
		t.Fatal("chat:usage must be emitted while the answer streams, not only in chat:done")
	}
	first := live[0]
	if first["conversationId"] != conv.ID {
		t.Fatalf("usage event must carry the conversation id: %+v", first)
	}
	if ctx, _ := first["contextTokens"].(int64); ctx != 1234 {
		t.Fatalf("contextTokens must be the real prompt tokens of the round (1234), got %+v", first["contextTokens"])
	}
	if round, _ := first["roundPrompt"].(int64); round != 1234 {
		t.Fatalf("roundPrompt must be reported as-is, got %+v", first["roundPrompt"])
	}
	if turn, _ := first["turnTokens"].(int64); turn != 1234+56 {
		t.Fatalf("turnTokens must be prompt+completion so far (1290), got %+v", first["turnTokens"])
	}
	if exact, _ := first["exact"].(bool); !exact {
		t.Fatal("exact must be true when the provider reported usage")
	}
	if limit, _ := first["tokenLimit"].(int); limit != 128000 {
		t.Fatalf("tokenLimit must follow the active provider context window (128000), got %+v", first["tokenLimit"])
	}
}
