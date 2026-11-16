// تست‌های فشرده‌سازی مدل‌محور + واقعیت‌های سنجاق‌شده (گام ۸).
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPinnedFactsExtraction(t *testing.T) {
	msgs := []chatMessage{
		{Role: "assistant", ToolCalls: []apiToolCall{
			testToolCall("t", "write_file", `{"path":"a.go","content":"x"}`),
			testToolCall("t", "run_command", `{"command":"go build ./..."}`),
			testToolCall("t", "apply_patch", `{"patch":"--- a/x\n+++ b/dir/y.ts\n@@ -1 +1 @@\n-a\n+b\n"}`),
		}},
		{Role: "tool", Content: "ERROR: old_string not found in 'a.go'"},
		{Role: "tool", Content: "ok"},
		{Role: "assistant", ToolCalls: []apiToolCall{
			testToolCall("t", "write_file", `{"path":"a.go","content":"y"}`),
		}},
	}
	facts := pinnedFactsFrom(msgs, 14)
	joined := strings.Join(facts, "\n")
	for _, want := range []string{"file touched: a.go", "file touched: dir/y.ts", "verification ran: go build ./...", "open error: old_string not found"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing fact %q in:\n%s", want, joined)
		}
	}
	if strings.Count(joined, "file touched: a.go") != 1 {
		t.Fatalf("duplicate facts must collapse:\n%s", joined)
	}
	if got := len(pinnedFactsFrom(msgs, 2)); got != 2 {
		t.Fatalf("limit not applied: %d facts", got)
	}
}

func TestContextDropMarkerCarriesSummaryAndFacts(t *testing.T) {
	base := []chatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "older"},
		{Role: "assistant", Content: "older answer"},
	}
	withMarker := ensureContextDropMarker(base)
	out := enrichContextDropMarkerDetails(withMarker, "decided to use SQLite", []string{"file touched: a.go"})
	markers := 0
	for _, m := range out {
		if !isContextDropMarker(m) {
			continue
		}
		markers++
		c := historyContentString(m.Content)
		if !strings.Contains(c, "decided to use SQLite") || !strings.Contains(c, "file touched: a.go") {
			t.Fatalf("marker must carry summary and facts: %q", c)
		}
		if !strings.HasPrefix(c, contextDropMarkerText) {
			t.Fatalf("marker prefix must stay: %q", c)
		}
	}
	if markers != 1 {
		t.Fatalf("exactly one marker expected, got %d", markers)
	}
	if got := enrichContextDropMarkerDetails(withMarker, "", nil); len(got) != len(withMarker) {
		t.Fatal("empty enrichment must be a no-op")
	}
}

func TestCompactionCallsSummarizerOnce(t *testing.T) {
	var history []chatMessage
	history = append(history, chatMessage{Role: "system", Content: "system"})
	for i := 0; i < 8; i++ {
		history = append(history,
			chatMessage{Role: "user", Content: strings.Repeat("old question ", 200)},
			chatMessage{Role: "assistant", Content: strings.Repeat("old answer ", 200)},
		)
	}
	history = append(history, chatMessage{Role: "user", Content: "current question"})

	calls := 0
	var seen int
	nh, changed := compactHistoryForBudget(history, 4000, nil, false, func(dropped []chatMessage) string {
		calls++
		seen = len(dropped)
		return "SUMMARY: decision X, file a.go"
	})
	if !changed {
		t.Fatal("history with a tiny budget must be compacted")
	}
	if calls != 1 {
		t.Fatalf("summarizer must run exactly once, ran %d", calls)
	}
	if seen == 0 {
		t.Fatal("summarizer must receive the dropped messages")
	}
	found := false
	for _, m := range nh {
		if isContextDropMarker(m) && strings.Contains(historyContentString(m.Content), "SUMMARY: decision X") {
			found = true
		}
	}
	if !found {
		t.Fatal("summary must end up in the drop marker")
	}
	small := []chatMessage{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}}
	if _, ch := compactHistoryForBudget(small, 100000, nil, false); ch {
		t.Fatal("small history under budget must not be compacted")
	}
}

// TestSummarizerUsesModel سرور مدل واقعی (غیراستریمی) پاسخ می‌دهد.
func TestSummarizerUsesModel(t *testing.T) {
	var gotSystem, gotUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream   bool          `json:"stream"`
			Messages []chatMessage `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Stream {
			t.Error("summarizer must not ask for a stream")
		}
		if len(req.Messages) == 2 {
			gotSystem = historyContentString(req.Messages[0].Content)
			gotUser = historyContentString(req.Messages[1].Content)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"- decision: SQLite\n- file: a.go"}}]}`))
	}))
	defer srv.Close()

	a := newTestApp(t)
	a.mu.Lock()
	a.settings.Providers = []Provider{{ID: "test", Name: "test", BaseURL: srv.URL, NoKey: true}}
	a.settings.Provider = "test"
	a.settings.Model = "test-model"
	a.mu.Unlock()

	msgs := []chatMessage{
		{Role: "user", Content: "please use SQLite"},
		{Role: "assistant", Content: "ok, decided SQLite"},
	}
	out := a.summarizeDroppedMessages(context.Background(), msgs, []string{"file touched: a.go"})
	if !strings.Contains(out, "SQLite") {
		t.Fatalf("summary must come from the model, got %q", out)
	}
	if !strings.Contains(gotSystem, "compress conversation history") {
		t.Fatalf("system prompt missing: %q", gotSystem)
	}
	if !strings.Contains(gotUser, "file touched: a.go") || !strings.Contains(gotUser, "please use SQLite") {
		t.Fatalf("summarizer input must carry facts and transcript: %q", gotUser)
	}
}

// TestSummarizerFailureIsSilent خطای مدل نباید نوبت را بشکند.
func TestSummarizerFailureIsSilent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
	}))
	defer srv.Close()
	a := newTestApp(t)
	a.mu.Lock()
	a.settings.Providers = []Provider{{ID: "test", Name: "test", BaseURL: srv.URL, NoKey: true}}
	a.settings.Provider = "test"
	a.settings.Model = "m"
	a.mu.Unlock()
	if out := a.summarizeDroppedMessages(context.Background(), []chatMessage{{Role: "user", Content: "x"}}, nil); out != "" {
		t.Fatalf("failed summarization must return empty, got %q", out)
	}
}
