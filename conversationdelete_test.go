// تست‌های حذف کامل گفتگو (فایل قبلی نام نسخه‌دار داشت؛ بدنه‌ها بدون تغییر منتقل شدند).
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeleteConversationRemovesEverything(t *testing.T) {
	a := newTestApp(t)
	c1 := &Conversation{ID: newID(), Title: "یکی", Messages: []Message{{ID: newID(), Role: "user", Content: "سلام"}}}
	c2 := &Conversation{ID: newID(), Title: "دو", Messages: []Message{{ID: newID(), Role: "user", Content: "دوباره"}}}
	a.mu.Lock()
	_ = a.saveConversation(c1)
	_ = a.saveConversation(c2)
	a.mu.Unlock()

	if err := a.DeleteConversation(c1.ID); err != nil {
		t.Fatal(err)
	}

	dir, _ := a.conversationsDir()
	if _, err := os.Stat(convPath(dir, c1.ID)); !os.IsNotExist(err) {
		t.Fatal("conversation file must be deleted from disk")
	}
	// ایندکس هم باید تمیز باشد
	idx := loadConversationIndex(dir)
	if idx != nil {
		for _, m := range idx.Entries {
			if m.ID == c1.ID {
				t.Fatal("index entry must be removed")
			}
		}
	}
	if got := a.GetConversations(); len(got) != 1 || got[0].ID != c2.ID {
		t.Fatalf("GetConversations must return only the remaining conversation, got %+v", got)
	}

	// حذف همه
	if err := a.DeleteAllConversations(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != ".index.json" {
			t.Fatalf("unexpected file left after delete-all: %s", e.Name())
		}
	}
	if got := a.GetConversations(); len(got) != 0 {
		t.Fatalf("all conversations must be gone, got %d", len(got))
	}
}

// TestDeleteConversationDuringStream سناریوی اصلی «حذف فقط از UI»:
// گفتگوی فعال در حال استریم حذف می‌شود؛ ذخیره‌ی نیمه‌کاره‌ی گوروتین
// نباید فایل را دوباره روی دیسک بسازد.
func TestDeleteConversationDuringStream(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for i := 0; i < 500; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"پاسخ \"}}]}\n\n")
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer srv.Close()

	a := newTestApp(t)
	a.settings.Providers[0].BaseURL = srv.URL // هدایت به سرور آزمایشی

	conv, err := a.SendMessage("", "تست حذف در حین استریم", nil)
	if err != nil {
		t.Fatal(err)
	}

	// صبر تا اولین درخواست استریم واقعاً برسد
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&hits) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("stream request never arrived")
	}
	time.Sleep(120 * time.Millisecond) // چند دلتا جاری شود

	// حذف در حین استریم — باید استریم را متوقف و منتظر خروج کامل بماند
	if err := a.DeleteConversation(conv.ID); err != nil {
		t.Fatal(err)
	}

	dir, _ := a.conversationsDir()
	if _, err := os.Stat(convPath(dir, conv.ID)); !os.IsNotExist(err) {
		t.Fatal("conversation file must be deleted despite active stream")
	}
	// گوروتین باید کاملاً خارج شده باشد
	a.genMu.Lock()
	genNil := a.gen == nil
	a.genMu.Unlock()
	if !genNil {
		t.Fatal("stream goroutine must have exited after delete")
	}

	// هیچ ذخیره‌ی دیرهنگامی فایل را بازنگرداند
	time.Sleep(250 * time.Millisecond)
	if _, err := os.Stat(convPath(dir, conv.ID)); !os.IsNotExist(err) {
		t.Fatal("deleted conversation resurrected by late save (memory-leak style race)")
	}
	if got := a.GetConversations(); len(got) != 0 {
		t.Fatalf("conversation list must be empty after delete-during-stream, got %d", len(got))
	}
}
