package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// truncatedSSEEnv یک Provider مصنوعی می‌سازد که بدنهٔ استریم را می‌فرستد و
// اتصال را پیش از پایانِ اعلام‌شده (Content-Length بزرگ‌تر) می‌بندد؛ همان
// چیزی که Go با «unexpected EOF» گزارش می‌کند.
func truncatedSSEEnv(t *testing.T, raw string) *e2eEnv {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", "99999")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, raw)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	env := newE2E(t, stepText("unused"))
	env.app.mu.Lock()
	env.app.settings.Providers[0].BaseURL = srv.URL
	env.app.mu.Unlock()
	return env
}

// TestStreamCutAfterFinishIsNeutral — مدل پاسخ را تمام کرده (finish_reason
// رسیده) و فقط اتصال بدون [DONE] بسته شده: پاسخ کامل ذخیره می‌شود، پیام
// خنثی می‌آید و «ادامه» اجباری نیست (هیچ chat:errorای صادر نمی‌شود).
func TestStreamCutAfterFinishIsNeutral(t *testing.T) {
	raw := sseSeq(
		`{"choices":[{"delta":{"content":"پاسخ کامل است."}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	// sseSeq خودش [DONE] می‌گذارد؛ برای این سناریو بدون آن لازم است.
	raw = strings.Replace(raw, "data: [DONE]\n\n", "", 1)

	env := truncatedSSEEnv(t, raw)
	env.send("سلام")
	conv := env.lastConv(t)

	if n := len(env.eventsByName("chat:error")); n != 0 {
		t.Fatalf("completed answer must not raise chat:error, got %d", n)
	}
	notices := strings.Join(env.noticesText(), "\n")
	if !strings.Contains(notices, "نیازی به «ادامه» نیست") {
		t.Fatalf("expected the neutral notice, got %q", notices)
	}
	last := lastAssistantOf(t, conv)
	if last.Aborted || last.Unfinished {
		t.Fatalf("completed answer must not be marked aborted/unfinished (aborted=%v unfinished=%v)", last.Aborted, last.Unfinished)
	}
	if !strings.Contains(last.Content, "پاسخ کامل است.") {
		t.Fatalf("answer content lost: %q", last.Content)
	}
	logText, _ := env.app.GetErrorLog()
	if !strings.Contains(logText, "stream cut:") || !strings.Contains(logText, `finish="stop"`) {
		t.Fatalf("diagnostic log must record bytes and finish_reason, got %q", logText)
	}
}

// TestStreamCutWithoutFinishStaysTruncated — بدون finish_reason واقعاً
// نیمه‌کاره است: پیام قبلی + «ادامه» و ذخیرهٔ بخش دریافت‌شده.
func TestStreamCutWithoutFinishStaysTruncated(t *testing.T) {
	raw := strings.Replace(sseSeq(
		`{"choices":[{"delta":{"content":"بخش نیمه‌کاره"}}]}`,
	), "data: [DONE]\n\n", "", 1)

	env := truncatedSSEEnv(t, raw)
	env.send("سلام")
	conv := env.lastConv(t)

	errs := env.eventsByName("chat:error")
	if len(errs) == 0 {
		t.Fatal("a cut without finish_reason must surface chat:error")
	}
	msg := ""
	if len(errs[0].data) > 0 {
		if m, ok := errs[0].data[0].(map[string]interface{}); ok {
			msg, _ = m["error"].(string)
		}
	}
	if !strings.Contains(msg, "نیمه‌کاره") || !strings.Contains(msg, "ادامه") {
		t.Fatalf("truncated notice must ask to continue, got %q", msg)
	}
	var partial *Message
	for i := range conv.Messages {
		if strings.Contains(conv.Messages[i].Content, "بخش نیمه‌کاره") {
			partial = &conv.Messages[i]
		}
	}
	if partial == nil {
		t.Fatal("received part must be saved")
	}
	if !partial.Aborted {
		t.Fatal("partial answer must be saved as aborted so «ادامه» works")
	}
	logText, _ := env.app.GetErrorLog()
	if !strings.Contains(logText, "stream cut:") || !strings.Contains(logText, `finish=""`) {
		t.Fatalf("diagnostic log must show an empty finish_reason, got %q", logText)
	}
}

// TestStreamCutAfterLengthStaysTruncated — finish_reason=length یعنی پاسخ به
// سقف توکن خروجی خورده؛ هرچند finish_reason رسیده، «کامل» نیست و باید هشدار
// نیمه‌کاره + «ادامه» بدهد، نه پیام خنثی.
func TestStreamCutAfterLengthStaysTruncated(t *testing.T) {
	raw := strings.Replace(sseSeq(
		`{"choices":[{"delta":{"content":"متن بریده"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"length"}]}`,
	), "data: [DONE]\n\n", "", 1)

	env := truncatedSSEEnv(t, raw)
	env.send("سلام")

	errs := env.eventsByName("chat:error")
	if len(errs) == 0 {
		t.Fatal("finish_reason=length must raise the truncated-stream warning")
	}
	msg := ""
	if len(errs[0].data) > 0 {
		if m, ok := errs[0].data[0].(map[string]interface{}); ok {
			msg, _ = m["error"].(string)
		}
	}
	if !strings.Contains(msg, "نیمه‌کاره") || !strings.Contains(msg, "ادامه") {
		t.Fatalf("length cut must ask to continue, got %q", msg)
	}
	if strings.Contains(strings.Join(env.noticesText(), "\n"), "نیازی به «ادامه» نیست") {
		t.Fatal("length cut must not get the neutral 'no need to continue' notice")
	}
	conv := env.lastConv(t)
	var partial *Message
	for i := range conv.Messages {
		if strings.Contains(conv.Messages[i].Content, "متن بریده") {
			partial = &conv.Messages[i]
		}
	}
	if partial == nil {
		t.Fatal("received part must be saved")
	}
	if !partial.Aborted {
		t.Fatal("length cut must be saved as aborted so «ادامه» works")
	}
	logText, _ := env.app.GetErrorLog()
	if !strings.Contains(logText, `finish="length"`) {
		t.Fatalf("diagnostic log must record finish=length, got %q", logText)
	}
}

// TestStreamCutText — یک پیام یکتا برای هر دو حالت نیمه‌کاره: سقف توکن خروجی
// راه‌حل مشخص می‌دهد و بقیه به پیام فارسیِ EOF می‌رسند (خطاهای دیگر خام می‌مانند).
func TestStreamCutText(t *testing.T) {
	if got := streamCutText(io.ErrUnexpectedEOF, "length"); !strings.Contains(got, "length") || !strings.Contains(got, "ادامه") {
		t.Fatalf("length cut message = %q", got)
	}
	if got := streamCutText(io.ErrUnexpectedEOF, ""); !strings.Contains(got, "نیمه‌کاره") {
		t.Fatalf("eof cut message = %q", got)
	}
	if got := streamCutText(errors.New("boom"), ""); got != "boom" {
		t.Fatalf("other errors must stay raw, got %q", got)
	}
}

// TestStreamCutAfterToolRoundContinues — قطع استریم بعد از یک دورِ ابزارِ کامل
// (finish_reason=tool_calls با فراخوانی پارس‌شده) خطا نیست: ابزار اجرا می‌شود و
// نوبت با درخواست بعدی عادی ادامه می‌یابد.
func TestStreamCutAfterToolRoundContinues(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		n := requests
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			// دورِ اول: فراخوانی ابزار + finish_reason، سپس بستن پیش از پایان.
			w.Header().Set("Content-Length", "99999")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "data: "+`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"write_file","arguments":"{\"path\":\"cut.txt\",\"content\":\"ok\"}"}}]}}]}`+"\n\n"+
				"data: "+`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return
		}
		// دورِ دوم: پاسخ کامل و تمیز.
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: "+`{"choices":[{"delta":{"content":"تمام شد."}}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	env := newE2E(t, stepText("unused"))
	env.app.mu.Lock()
	env.app.settings.Providers[0].BaseURL = srv.URL
	env.app.mu.Unlock()

	env.send("یک فایل بساز")

	if n := len(env.eventsByName("chat:error")); n != 0 {
		t.Fatalf("a complete tool round must not raise chat:error, got %d", n)
	}
	if got := env.readFile("cut.txt"); got != "ok" {
		t.Fatalf("tool from the cut round must run, file=%q", got)
	}
	conv := env.lastConv(t)
	if !strings.Contains(lastAssistantOf(t, conv).Content, "تمام شد.") {
		t.Fatalf("turn must continue with a normal next round, got %q", lastAssistantOf(t, conv).Content)
	}
}
