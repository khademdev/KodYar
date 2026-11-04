// تست‌های محافظ «خواندن پیش از نوشتن» و «فایل کهنه» (مأموریت ۳ — A.1).
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readTool یک read_file موفق روی فایل می‌زند (helper تست).
func readTool(t *testing.T, a *App, rel string) {
	t.Helper()
	if r, ok := a.execTool("read_file", `{"path":"`+rel+`"}`); !ok {
		t.Fatalf("read_file %s failed: %s", rel, r)
	}
}

func TestWriteRequiresPriorRead(t *testing.T) {
	a := newTestApp(t)
	writeAppWsFile(t, a, "a.txt", "hello\n")

	// هرگز خوانده نشده → رد با پیام انگلیسی قابل‌اقدام
	res, ok := a.execTool("write_file", `{"path":"a.txt","content":"bye\n"}`)
	if ok {
		t.Fatalf("unread overwrite must be rejected, got: %s", res)
	}
	if !strings.Contains(res, "has not been read in this conversation") || !strings.Contains(res, "read_file") {
		t.Fatalf("unexpected rejection message: %s", res)
	}
	if got := readAppWsFile(t, a, "a.txt"); got != "hello\n" {
		t.Fatalf("file must be untouched, got %q", got)
	}

	// پس از خواندن → مجاز
	readTool(t, a, "a.txt")
	if res, ok = a.execTool("write_file", `{"path":"a.txt","content":"bye\n"}`); !ok {
		t.Fatalf("write after read must succeed: %s", res)
	}
	if got := readAppWsFile(t, a, "a.txt"); got != "bye\n" {
		t.Fatalf("content not written: %q", got)
	}
}

func TestWriteStaleFileRejected(t *testing.T) {
	a := newTestApp(t)
	writeAppWsFile(t, a, "stale.txt", "v1\n")
	readTool(t, a, "stale.txt")

	// کاربر/ابزار دیگری فایل را عوض می‌کند
	writeAppWsFile(t, a, "stale.txt", "v2 external\n")

	res, ok := a.execTool("write_file", `{"path":"stale.txt","content":"v3\n"}`)
	if ok {
		t.Fatalf("stale overwrite must be rejected, got: %s", res)
	}
	if !strings.Contains(res, "changed on disk since you last read it") {
		t.Fatalf("unexpected stale message: %s", res)
	}
	if got := readAppWsFile(t, a, "stale.txt"); got != "v2 external\n" {
		t.Fatalf("external content must survive, got %q", got)
	}

	// خواندن دوباره → مجاز
	readTool(t, a, "stale.txt")
	if res, ok = a.execTool("write_file", `{"path":"stale.txt","content":"v3\n"}`); !ok {
		t.Fatalf("write after re-read must succeed: %s", res)
	}
	if got := readAppWsFile(t, a, "stale.txt"); got != "v3\n" {
		t.Fatalf("content not written: %q", got)
	}
}

func TestCreateThenOverwriteAllowed(t *testing.T) {
	a := newTestApp(t)
	// ساخت فایل تازه همیشه آزاد است
	if res, ok := a.execTool("write_file", `{"path":"new/file.txt","content":"one\n"}`); !ok {
		t.Fatalf("create must be allowed: %s", res)
	}
	// فایلی که خودِ ایجنت ساخته «دیده‌شده» است — بازنویسی بدون read
	if res, ok := a.execTool("write_file", `{"path":"new/file.txt","content":"two\n"}`); !ok {
		t.Fatalf("overwrite of agent-created file must be allowed: %s", res)
	}
	if got := readAppWsFile(t, a, "new/file.txt"); got != "two\n" {
		t.Fatalf("content: %q", got)
	}
}

func TestEditUnreadAllowedWithNote(t *testing.T) {
	a := newTestApp(t)
	writeAppWsFile(t, a, "e.txt", "alpha beta\n")

	res, ok := a.execTool("edit_file", `{"path":"e.txt","old_string":"alpha","new_string":"gamma"}`)
	if !ok {
		t.Fatalf("edit of unread file must be allowed (exact old_string protects): %s", res)
	}
	if !strings.Contains(res, "had not been read in this conversation") {
		t.Fatalf("result must mention the unread file: %s", res)
	}
	if got := readAppWsFile(t, a, "e.txt"); got != "gamma beta\n" {
		t.Fatalf("edit not applied: %q", got)
	}
	// پس از نوشتن، رکورد به‌روز است → دیگر یادآوری ندارد
	res, ok = a.execTool("edit_file", `{"path":"e.txt","old_string":"beta","new_string":"delta"}`)
	if !ok {
		t.Fatalf("second edit failed: %s", res)
	}
	if strings.Contains(res, "had not been read") {
		t.Fatalf("no note expected after the agent wrote the file: %s", res)
	}
}

func TestEditStaleFileRejected(t *testing.T) {
	a := newTestApp(t)
	writeAppWsFile(t, a, "es.txt", "alpha\n")
	readTool(t, a, "es.txt")
	writeAppWsFile(t, a, "es.txt", "alpha changed\n")

	res, ok := a.execTool("edit_file", `{"path":"es.txt","old_string":"alpha","new_string":"beta"}`)
	if ok {
		t.Fatalf("stale edit must be rejected: %s", res)
	}
	if !strings.Contains(res, "changed on disk") {
		t.Fatalf("unexpected message: %s", res)
	}
}

// TestGuardByteExactContent: BOM + CRLF + فارسی با نیم‌فاصله — هش بایت‌به‌بایت
// است و نوشتنِ عیناً همان محتوا کهنه‌بودن نمی‌سازد.
func TestGuardByteExactContent(t *testing.T) {
	a := newTestApp(t)
	content := "\xef\xbb\xbfخط یک\r\nخط دو با نیم‌فاصله\u200cاینجا\r\n"
	writeAppWsFile(t, a, "fa.txt", content)
	readTool(t, a, "fa.txt")
	// همان محتوا → «تغییر لازم نیست» (نه خطای کهنه)
	res, ok := a.execTool("write_file", `{"path":"fa.txt","content":"`+strings.ReplaceAll(content, "\r\n", "\\r\\n")+`"}`)
	if !ok {
		t.Fatalf("rewriting identical content must not be treated as stale: %s", res)
	}
	if !strings.Contains(res, "no changes needed") {
		t.Fatalf("expected no-changes notice, got: %s", res)
	}
	if got := readAppWsFile(t, a, "fa.txt"); got != content {
		t.Fatalf("bytes changed: %q", got)
	}
}

// TestSubagentReadDoesNotCount: خواندن زیرایجنت نباید محافظ گفتگوی اصلی را
// باز کند.
func TestSubagentReadDoesNotCount(t *testing.T) {
	a := newTestApp(t)
	writeAppWsFile(t, a, "s.txt", "main\n")

	subCtx := withSubagentNoAsk(context.Background())
	if r, ok := a.toolReadFile(subCtx, "s.txt", 0, 0); !ok {
		t.Fatalf("sub-agent read failed: %s", r)
	}
	res, ok := a.execTool("write_file", `{"path":"s.txt","content":"x\n"}`)
	if ok {
		t.Fatalf("sub-agent read must not satisfy the main agent's read guard: %s", res)
	}
}

// TestReadStoreLRUBound: سقف ۵۰۰ رکورد در هر گفتگو و سقف گفتگوها.
func TestReadStoreLRUBound(t *testing.T) {
	var s fileReadStore
	for i := 0; i < maxReadRecordsPerConv+120; i++ {
		s.note("conv-a", "p"+itoa(i), fileReadRecord{Size: 1, SHA256: "h"})
	}
	if got := s.convSize("conv-a"); got != maxReadRecordsPerConv {
		t.Fatalf("per-conversation LRU bound = %d, want %d", got, maxReadRecordsPerConv)
	}
	// قدیمی‌ترین‌ها باید رفته باشند و جدیدترین‌ها مانده باشند
	if _, ok := s.lookup("conv-a", "p0"); ok {
		t.Fatal("oldest record must be evicted")
	}
	if _, ok := s.lookup("conv-a", "p"+itoa(maxReadRecordsPerConv+119)); !ok {
		t.Fatal("newest record must be kept")
	}
	// سقف گفتگوها
	for i := 0; i < maxReadRecordConvs+5; i++ {
		s.note("conv-"+itoa(i), "x", fileReadRecord{Size: 1})
	}
	if n := len(s.convIDs()); n > maxReadRecordConvs {
		t.Fatalf("conversation cap = %d, want <= %d", n, maxReadRecordConvs)
	}
	s.dropConversation("conv-a")
	if got := s.convSize("conv-a"); got != 0 {
		t.Fatalf("dropConversation left %d records", got)
	}
	// LRU bump: خواندن یک مسیر قدیمی آن را نگه می‌دارد
	var s2 fileReadStore
	s2.note("c", "keep", fileReadRecord{})
	for i := 0; i < maxReadRecordsPerConv-1; i++ {
		s2.note("c", "n"+itoa(i), fileReadRecord{})
	}
	if _, ok := s2.lookup("c", "keep"); !ok {
		t.Fatal("keep must survive")
	}
	s2.note("c", "extra", fileReadRecord{}) // evicts the oldest non-bumped entry
	if _, ok := s2.lookup("c", "keep"); !ok {
		t.Fatal("recently used record must not be evicted before older ones")
	}
}

// TestReadStoreIsolatedPerConversation: رکورد یک گفتگو برای گفتگوی دیگر معتبر
// نیست.
func TestReadStoreIsolatedPerConversation(t *testing.T) {
	a := newTestApp(t)
	writeAppWsFile(t, a, "c.txt", "x\n")

	ctxA := withToolCallInfo(context.Background(), "conv-a", "call-1")
	if r, ok := a.toolReadFile(ctxA, "c.txt", 0, 0); !ok {
		t.Fatalf("read failed: %s", r)
	}
	ctxB := withToolCallInfo(context.Background(), "conv-b", "call-2")
	res, ok := a.writeFileWithConfirm(ctxB, "c.txt", "y\n", intentOverwrite)
	if ok {
		t.Fatalf("other conversation must not inherit the read record: %s", res)
	}
	// همان گفتگو مجاز است
	if res, ok := a.writeFileWithConfirm(ctxA, "c.txt", "y\n", intentOverwrite); !ok {
		t.Fatalf("same conversation write must pass: %s", res)
	}
}

// TestPersianPathsGuard: مسیر فارسی با فاصله و نیم‌فاصله.
func TestPersianPathsGuard(t *testing.T) {
	a := newTestApp(t)
	rel := "پوشه‌ی من/یادداشت ۱.txt"
	writeAppWsFile(t, a, rel, "محتوا\n")
	if res, ok := a.execTool("write_file", `{"path":"`+rel+`","content":"جدید\n"}`); ok {
		t.Fatalf("unread Persian path overwrite must be rejected: %s", res)
	}
	if r, ok := a.execTool("read_file", `{"path":"`+rel+`"}`); !ok {
		t.Fatalf("read Persian path failed: %s", r)
	}
	// نوشتن هم‌مسیر (نرمال‌شده) باید مجاز باشد
	if res, ok := a.execTool("write_file", `{"path":"`+rel+`","content":"جدید\n"}`); !ok {
		t.Fatalf("write after read of Persian path must pass: %s", res)
	}
	if _, err := os.Stat(filepath.Join(a.settings.WorkspacePath, "پوشه‌ی من", "یادداشت ۱.txt")); err != nil {
		t.Fatalf("file missing: %v", err)
	}
}

// TestMoveAndDeleteUpdateRecords: مقصدِ move «دیده‌شده» است و فایل حذف‌شده
// رکورد ندارد.
func TestMoveAndDeleteUpdateRecords(t *testing.T) {
	a := newTestApp(t)
	writeAppWsFile(t, a, "m1.txt", "m\n")
	readTool(t, a, "m1.txt")
	if res, ok := a.execTool("move_path", `{"from":"m1.txt","to":"m2.txt"}`); !ok {
		t.Fatalf("move failed: %s", res)
	}
	// مقصد بدون خواندن دوباره قابل بازنویسی است (ایجنت خودش جابه‌جا کرد)
	if res, ok := a.execTool("write_file", `{"path":"m2.txt","content":"m2\n"}`); !ok {
		t.Fatalf("destination must be recorded as seen: %s", res)
	}
	if res, ok := a.execTool("delete_path", `{"path":"m2.txt"}`); !ok {
		t.Fatalf("delete failed: %s", res)
	}
	// پس از حذف، ساخت دوباره آزاد است
	if res, ok := a.execTool("write_file", `{"path":"m2.txt","content":"again\n"}`); !ok {
		t.Fatalf("recreate after delete must be allowed: %s", res)
	}
}
