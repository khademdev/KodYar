// تست‌های Stage 3 — استحکام فایل‌سیستم و داده:
// نوشتن اتمیک با نام موقت یکتا، چرخش .bak، بازیابی از خرابی/برش، خطای
// دیسک پر بدون آسیب به فایل موجود، و اعتبارسنجی نام ویندوز.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func writeRaw(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestAtomicWriteConcurrentUniqueTemp — نام موقت یکتاست: نویسنده‌های هم‌زمان
// روی یک مسیر همدیگر را خراب نمی‌کنند و هیچ .tmp ای نمی‌ماند.
func TestAtomicWriteConcurrentUniqueTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.json")
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"n":%d,"pad":"%s"}`, i, strings.Repeat("x", 2048))
			if err := atomicWriteFile(p, []byte(body), 0o600); err != nil {
				t.Errorf("atomic write %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var v struct{ N int }
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("concurrent writers corrupted the file: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

// TestSweepRecognizesUniqueTempNames — جاروی راه‌اندازی هر دو سبک نام موقت
// (`.tmp` ثابت قدیمی و `name.<rand>.tmp` تازه) را می‌شناسد.
func TestSweepRecognizesUniqueTempNames(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, filepath.Join(dir, "settings.json.tmp"), "x")
	writeRaw(t, filepath.Join(dir, "settings.json.123456.tmp"), "x")
	if n := sweepStaleTempFiles(dir); n != 2 {
		t.Fatalf("swept %d temp files, want 2", n)
	}
}

// TestConversationBackupRotationAndRecovery — یک نسل .bak نگه داشته می‌شود و
// فایل اصلی خراب از آن بازیابی می‌شود.
func TestConversationBackupRotationAndRecovery(t *testing.T) {
	a := newTestApp(t)
	c := &Conversation{ID: newID(), Title: "نسخه اول", CreatedAt: time.Now().Unix(), Messages: []Message{
		{ID: newID(), Role: "user", Content: "متن نسخه‌ی اول", At: 1},
	}}
	a.mu.Lock()
	if err := a.saveConversation(c); err != nil {
		a.mu.Unlock()
		t.Fatal(err)
	}
	a.mu.Unlock()

	c.Title = "نسخه دوم"
	c.Messages = append(c.Messages, Message{ID: newID(), Role: "assistant", Content: "پاسخ دوم", At: 2})
	a.mu.Lock()
	if err := a.saveConversation(c); err != nil {
		a.mu.Unlock()
		t.Fatal(err)
	}
	a.mu.Unlock()

	dir, err := a.conversationsDir()
	if err != nil {
		t.Fatal(err)
	}
	p := convPath(dir, c.ID)
	bak, err := os.ReadFile(backupPath(p))
	if err != nil {
		t.Fatalf("one-generation .bak must exist: %v", err)
	}
	if !strings.Contains(string(bak), "متن نسخه‌ی اول") || strings.Contains(string(bak), "پاسخ دوم") {
		t.Fatalf(".bak must hold the previous generation: %s", string(bak))
	}

	// فایل اصلی خراب → از .bak بازیابی
	writeRaw(t, p, `{"id":"`)
	got, err := a.GetConversation(c.ID)
	if err != nil {
		t.Fatalf("corrupt main file must recover from .bak: %v", err)
	}
	if got.Title != "نسخه اول" || len(got.Messages) != 1 {
		t.Fatalf("recovered generation wrong: %+v", got)
	}
}

// TestConversationSalvageTruncated — JSON بریده‌شده: پیام‌های کامل می‌مانند،
// فایل خراب به .corrupt-* می‌رود و اعلان فارسی با ارقام فارسی می‌آید.
func TestConversationSalvageTruncated(t *testing.T) {
	a := newTestApp(t)
	var mu sync.Mutex
	var notices []string
	a.eventEmit = func(name string, data []interface{}) {
		if name != "chat:notice" || len(data) == 0 {
			return
		}
		m, ok := data[0].(map[string]interface{})
		if !ok {
			return
		}
		if s, ok := m["text"].(string); ok {
			mu.Lock()
			notices = append(notices, s)
			mu.Unlock()
		}
	}

	id := "20250101-101010-abcd"
	dir, err := a.conversationsDir()
	if err != nil {
		t.Fatal(err)
	}
	p := convPath(dir, id)
	body := `{"id":"` + id + `","title":"بریده","createdAt":1,"updatedAt":2,"messages":[` +
		`{"id":"m1","role":"user","content":"سلام\u200cدنیا","at":1},` +
		`{"id":"m2","role":"assistant","content":"پاسخ","at":2},` +
		`{"id":"m3","role":"assi`
	writeRaw(t, p, body)

	got, err := a.GetConversation(id)
	if err != nil {
		t.Fatalf("salvage must succeed: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("salvaged messages = %d, want 2", len(got.Messages))
	}
	if got.Messages[0].Content != "سلام\u200cدنیا" {
		t.Fatalf("Persian/ZWNJ must survive byte-exact: %q", got.Messages[0].Content)
	}
	matches, _ := filepath.Glob(p + ".corrupt-*")
	if len(matches) != 1 {
		t.Fatalf("damaged file must be kept as .corrupt-* exactly once, got %v", matches)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("salvaged conversation must be rewritten to the main file: %v", err)
	}
	var healthy Conversation
	raw, _ := os.ReadFile(p)
	if err := json.Unmarshal(raw, &healthy); err != nil {
		t.Fatalf("rewritten conversation must be valid JSON: %v", err)
	}

	want := "گفتگو آسیب دیده بود؛ ۲ پیام بازیابی شد."
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		found := false
		for _, n := range notices {
			if n == want {
				found = true
			}
		}
		mu.Unlock()
		if found {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("Persian recovery notice missing; notices=%v", notices)
}

// TestCorruptFilesAreBounded — حداکثر ۵ نسخه‌ی .corrupt-* نگه داشته می‌شود.
func TestCorruptFilesAreBounded(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	for i := 0; i < 8; i++ {
		writeRaw(t, fmt.Sprintf("%s.corrupt-2024010%d-000000", p, i), "x")
	}
	pruneCorruptFiles(p)
	matches, _ := filepath.Glob(p + ".corrupt-*")
	if len(matches) != maxCorruptFiles {
		t.Fatalf("corrupt files = %d, want %d", len(matches), maxCorruptFiles)
	}
}

// TestDiskFullKeepsExistingFileAndMemory — خطای نوشتن (ENOSPC) فایل قبلی را
// دست نمی‌زند، حالت حافظه را دور نمی‌ریزد و پیام فارسی می‌دهد.
func TestDiskFullKeepsExistingFileAndMemory(t *testing.T) {
	a := newTestApp(t)
	c := &Conversation{ID: newID(), Title: "t", CreatedAt: time.Now().Unix(), Messages: []Message{
		{ID: "m1", Role: "user", Content: "محتوای قبلی", At: 1},
	}}
	a.mu.Lock()
	if err := a.saveConversation(c); err != nil {
		a.mu.Unlock()
		t.Fatal(err)
	}
	a.mu.Unlock()
	dir, _ := a.conversationsDir()
	p := convPath(dir, c.ID)
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	old := atomicWriteHook
	atomicWriteHook = func(path string) error {
		if path == p {
			return syscall.ENOSPC
		}
		return nil
	}
	t.Cleanup(func() { atomicWriteHook = old })

	c.Messages = append(c.Messages, Message{ID: "m2", Role: "assistant", Content: "محتوای تازه", At: 2})
	a.mu.Lock()
	err = a.saveConversation(c)
	a.mu.Unlock()
	if err == nil {
		t.Fatal("disk-full save must fail")
	}
	if !strings.Contains(err.Error(), "ذخیره‌ی گفتگو") {
		t.Fatalf("error must be a clear Persian message: %v", err)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("existing file must stay byte-identical on a failed save")
	}
	if len(c.Messages) != 2 {
		t.Fatal("in-memory conversation must keep the new message")
	}
}

// TestChangesJSONRecoversFromBackup — changes.json خراب از .bak برمی‌گردد.
func TestChangesJSONRecoversFromBackup(t *testing.T) {
	a := newTestApp(t)
	a.initChangesStore()
	p, err := a.changesPath()
	if err != nil {
		t.Fatal(err)
	}
	writeRaw(t, p, `[{"id":`)
	writeRaw(t, backupPath(p), `[]`)

	got := a.loadChanges()
	if len(got) != 0 {
		t.Fatalf("expected an empty list from .bak, got %+v", got)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("main changes file must be restored from .bak: %v", err)
	}
	var list []FileChange
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("restored changes file must be valid JSON: %v", err)
	}
	matches, _ := filepath.Glob(p + ".corrupt-*")
	if len(matches) != 1 {
		t.Fatalf("damaged changes file must be kept as .corrupt-*: %v", matches)
	}
}

// TestValidatePathName — نام‌های ممنوع/مجاز ویندوز (روی همه‌ی سکوها).
func TestValidatePathName(t *testing.T) {
	bad := []string{
		"", "CON", "con.txt", "NUL", "nul.json", "COM1", "LPT9.log", "aux",
		"a.", "a ", "a<b", "a>b", "a:b", "a|b", "a?b", "a*b",
		"a\x01b",
	}
	for _, name := range bad {
		if err := validatePathName(name); err == nil {
			t.Errorf("name %q must be rejected", name)
		}
	}
	good := []string{
		"main.go", "پروژه‌ی من.txt", "a b.txt", "COM10", "console.txt",
		".gitignore", " گزارش نهایی.md",
	}
	for _, name := range good {
		if err := validatePathName(name); err != nil {
			t.Errorf("name %q must be accepted: %v", name, err)
		}
	}
}

// TestValidatePathForWriteLongPath — مسیر بلندتر از سقف ویندوز فقط در ویندوز
// رد می‌شود (روی POSIX مسیرهای بلند قانونی‌اند).
func TestValidatePathForWriteLongPath(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("a", 300))
	err := validatePathForWrite(long)
	if isWindows() {
		if err == nil || !strings.Contains(err.Error(), "too long") {
			t.Fatalf("long Windows path must be rejected clearly, got %v", err)
		}
	} else if err != nil {
		t.Fatalf("POSIX must not reject long paths: %v", err)
	}
	if err := validatePathForWrite(""); err == nil {
		t.Fatal("empty path must be rejected")
	}
}
