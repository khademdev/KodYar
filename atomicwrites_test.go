// تست‌های رگرسیون — اصلاح آخرین مسیر نوشتنِ غیراتمیک:
// فایل‌های تصویر جانبی conversations/img/*.b64.
//
// پیش از اصلاح، externalizeImagesLocked با os.WriteFile «درجا» می‌نوشت
// (truncate بدون fsync). کرش سیستم در حین نوشتن فایل نیم‌کاره به جا
// می‌گذاشت و hydrate بعدی data URL بریده را بی‌هیچ خطایی تزریق می‌کرد —
// خرابی خاموش تصویر. اکنون همان مسیر atomicWriteFile (tmp → fsync
// → rename در همان پوشه) استفاده می‌شود.
//
// اثبات‌ها (به سبک تست‌های F2:
// - روی دیسک فقط مرجع img:// می‌ماند، نه data URL
// - فایل.b64 دقیقاً همان محتوای کامل را دارد و round-trip می‌شود
// - بازنویسیِ تصویرِ هم‌نام، جایگزینیِ کامل است (نه الحاق/بریده)
// - هیچ فایل موقتی.tmp در «کل درخت» گفتگوها باقی نمی‌ماند (walk
// بازگشتی — تست فقط سطح بالا را می‌دید و img/ را نه)
// - نام‌گذاری fallback وقتی پیام ID ندارد (نام گفتگو به‌جای آن)
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// تصویر PNG واقعی ۱×۱ پیکسل (همان testPNGData در vision_test.go).
const testPNGAlt = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

// assertNoTmpRecursive هیچ فایل.tmp نباید در کل درخت گفتگوها باشد —
// از جمله زیرشاخه‌ی img/ که تست (ReadDir سطح‌بالا) آن را نمی‌دید.
func assertNoTmpRecursive(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk conversations dir: %v", err)
	}
}

// TestB64ImagesExternalizeAtomic مسیر کامل را می‌سنجد: ذخیره با data URL →
// روی دیسک فایل.b64 جانبی با محتوای کامل + JSON فقط با مرجع img:// →
// بارگذاری مجدد data URL اصلی را برمی‌گرداند. چند تصویر در یک پیام هم
// (اندیس‌های j=۰و۱) بررسی می‌شود.
func TestB64ImagesExternalizeAtomic(t *testing.T) {
	a := newTestApp(t)

	imgA := testPNGData
	imgB := testPNGAlt
	conv := &Conversation{ID: newID(), Title: "t", CreatedAt: time.Now().Unix(), Messages: []Message{
		{ID: newID(), Role: "user", Content: "این‌ها را ببین", Images: []string{imgA, imgB}, At: time.Now().Unix()},
	}}

	a.mu.Lock()
	err := a.saveConversation(conv)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	cdir, err := a.conversationsDir()
	if err != nil {
		t.Fatal(err)
	}
	imgDir := filepath.Join(cdir, "img")

	// JSON روی دیسک: فقط مرجع img:// — نه data URL
	raw, err := os.ReadFile(convPath(cdir, conv.ID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "data:image/") {
		t.Fatal("conversation JSON must not embed the data URL (externalization failed)")
	}
	if !strings.Contains(string(raw), imgRefPrefix) {
		t.Fatal("conversation JSON must contain img:// reference")
	}

	// فایل‌های جانبی با محتوای دقیق و کامل
	for j, want := range []string{imgA, imgB} {
		name := filepath.Base(conv.Messages[0].ID) + "-" + strconv.Itoa(j) + ".b64"
		got, err := os.ReadFile(filepath.Join(imgDir, name))
		if err != nil {
			t.Fatalf("image file %s missing: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("image file %s content mismatch: %d bytes, want %d", name, len(got), len(want))
		}
	}

	// round-trip: بارگذاری، data URL کامل را برمی‌گرداند
	a.mu.Lock()
	c2, err := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.Messages) != 1 || len(c2.Messages[0].Images) != 2 {
		t.Fatalf("round-trip shape changed: %d messages, %d images", len(c2.Messages), len(c2.Messages[0].Images))
	}
	if c2.Messages[0].Images[0] != imgA || c2.Messages[0].Images[1] != imgB {
		t.Fatal("round-trip must restore full data URLs verbatim")
	}

	assertNoTmpRecursive(t, cdir)
}

// TestB64ImageOverwriteFullReplacement ذخیره‌ی مجدد همان پیام (نام فایل
// یکسان) با محتوای جدید: فایل باید «کاملاً» با محتوای جدید جایگزین شود —
// نه الحاق شود و نه بریده‌ای از ترکیب قدیمی/جدید بماند.
func TestB64ImageOverwriteFullReplacement(t *testing.T) {
	a := newTestApp(t)

	conv := &Conversation{ID: newID(), Title: "t", CreatedAt: time.Now().Unix(), Messages: []Message{
		{ID: newID(), Role: "user", Content: "v1", Images: []string{testPNGData}, At: time.Now().Unix()},
	}}

	a.mu.Lock()
	if err := a.saveConversation(conv); err != nil {
		t.Fatal(err)
	}
	// همان پیام، همان اندیس — محتوای جدید
	conv.Messages[0].Images[0] = testPNGAlt
	if err := a.saveConversation(conv); err != nil {
		t.Fatal(err)
	}
	a.mu.Unlock()

	cdir, err := a.conversationsDir()
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(conv.Messages[0].ID) + "-0.b64"
	got, err := os.ReadFile(filepath.Join(cdir, "img", name))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != testPNGAlt {
		t.Fatalf("overwrite must fully replace content: got %d bytes starting %q, want alt PNG (%d bytes)",
			len(got), got[:min(40, len(got))], len(testPNGAlt))
	}

	a.mu.Lock()
	c2, err := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if c2.Messages[0].Images[0] != testPNGAlt {
		t.Fatal("round-trip after overwrite must return the NEW image")
	}

	assertNoTmpRecursive(t, cdir)
}

// TestB64ImageFallbackNameForEmptyMessageID پیام بدون ID → نام فایل از
// ID گفتگو ساخته می‌شود (مسیر fallback در externalizeImagesLocked).
func TestB64ImageFallbackNameForEmptyMessageID(t *testing.T) {
	a := newTestApp(t)

	conv := &Conversation{ID: newID(), Title: "t", CreatedAt: time.Now().Unix(), Messages: []Message{
		{ID: "", Role: "user", Content: "بدون آی‌دی", Images: []string{testPNGData}, At: time.Now().Unix()},
	}}

	a.mu.Lock()
	err := a.saveConversation(conv)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	cdir, err := a.conversationsDir()
	if err != nil {
		t.Fatal(err)
	}
	fallback := filepath.Join(cdir, "img", conv.ID+"-0.b64")
	if _, err := os.Stat(fallback); err != nil {
		t.Fatalf("expected fallback-named file %s: %v", fallback, err)
	}

	a.mu.Lock()
	c2, err := a.loadConversation(conv.ID)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if c2.Messages[0].Images[0] != testPNGData {
		t.Fatal("round-trip must restore image saved under fallback name")
	}

	assertNoTmpRecursive(t, cdir)
}

// ---------------------------------------------------------------- بخش B

// TestQuickPingHTTPErrorPaths مسیرهای خطای HTTP «تست اتصال» Provider را با
// سرور آزمایشی می‌سنجد: ۲۰۰ → موفق؛ بدنه‌ی خطای سبک OpenAI → پیام
// استخراج‌شده؛ متن خام → همان متن؛ بدنه‌ی خالی → «پاسخ نامشخص».
// (اتصال قطع در تست جداگانه.)
func TestQuickPingHTTPErrorPaths(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantErrSub []string // بخش‌هایی که باید در متن خطا باشند
	}{
		{
			name:       "ok_200",
			status:     http.StatusOK,
			wantErrSub: nil,
		},
		{
			name:       "openai_style_error_body",
			status:     http.StatusUnauthorized,
			body:       `{"error":{"message":"Invalid API key","type":"invalid_request_error"}}`,
			wantErrSub: []string{"HTTP 401", "Invalid API key"},
		},
		{
			name:       "plain_text_body",
			status:     http.StatusInternalServerError,
			body:       "boom",
			wantErrSub: []string{"HTTP 500", "boom"},
		},
		{
			name:       "empty_body",
			status:     http.StatusBadGateway,
			body:       "",
			wantErrSub: []string{"HTTP 502", "پاسخ نامشخص"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if tc.body != "" {
					io.WriteString(w, tc.body)
				}
			}))
			defer srv.Close()

			err := quickPing(srv.URL, "", "test-model")
			if tc.wantErrSub == nil {
				if err != nil {
					t.Fatalf("200 must succeed, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("status %d must fail", tc.status)
			}
			for _, sub := range tc.wantErrSub {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("error %q must contain %q", err.Error(), sub)
				}
			}
		})
	}
}

// TestQuickPingConnectionRefused سرور بسته‌شده → خطای «اتصال ناموفق بود»
// (و نه پنیک یا خطای خام شبکه بدون پیام فارسی).
func TestQuickPingConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // پورت فوراً بسته می‌شود

	err := quickPing(url, "", "m")
	if err == nil {
		t.Fatal("connection refused must fail")
	}
	if !strings.Contains(err.Error(), "اتصال ناموفق بود") {
		t.Fatalf("error must be the friendly connection failure, got: %v", err)
	}
}

// TestQuickPingMaxTokensRetry مسیر تلاش مجدد: سرویس‌دهنده‌ای که پارامتر
// max_tokens را رد می‌کند (خطای ۴۰۰ با متن max_tokens) → یک‌بار بدون آن
// دوباره تلاش می‌شود و همان‌جا موفق می‌شود؛ درخواست دوم نباید max_tokens داشته باشد.
func TestQuickPingMaxTokensRetry(t *testing.T) {
	var mu sync.Mutex
	var bodies []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"max_tokens is not supported by this model"}}`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := quickPing(srv.URL, "k", "m"); err != nil {
		t.Fatalf("retry after max_tokens rejection must succeed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("expected exactly 2 requests, got %d", len(bodies))
	}
	if !strings.Contains(bodies[0], `"max_tokens"`) {
		t.Fatal("first request must carry max_tokens")
	}
	if strings.Contains(bodies[1], "max_tokens") {
		t.Fatal("retry request must omit max_tokens")
	}
}

// TestTestProviderGates دروازه‌های متد Provider: Provider ناموجود و مدل خالی.
func TestTestProviderGates(t *testing.T) {
	a := newTestApp(t) // Provider آزمایشی «ollama» با NoKey

	if err := a.TestProvider("nope", "llama3.2"); err == nil || !strings.Contains(err.Error(), "Provider پیدا نشد") {
		t.Fatalf("unknown provider must fail with not-found: %v", err)
	}

	a.mu.Lock()
	a.settings.Model = ""
	a.mu.Unlock()
	if err := a.TestProvider("ollama", ""); err == nil || !strings.Contains(err.Error(), "نام مدل را وارد کنید") {
		t.Fatalf("empty model must fail with enter-model: %v", err)
	}
}

// TestTestProviderHappyPath مسیر کاملِ موفق: Provider با NoKey و BaseURL سرور
// آزمایشی → درخواست POST به /chat/completions بدون هدر Authorization و با
// نام مدل در بدنه.
func TestTestProviderHappyPath(t *testing.T) {
	a := newTestApp(t)

	var mu sync.Mutex
	var gotAuth, gotPath, gotMethod string
	var gotModel interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		json.Unmarshal(raw, &body)
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotModel = body["model"]
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a.mu.Lock()
	p := a.findProvider("ollama")
	p.BaseURL = srv.URL
	a.mu.Unlock()

	if err := a.TestProvider("ollama", "llama3.2"); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotMethod != http.MethodPost || gotPath != "/chat/completions" {
		t.Fatalf("request shape wrong: %s %s", gotMethod, gotPath)
	}
	if gotAuth != "" {
		t.Fatal("NoKey provider must not send an Authorization header")
	}
	if gotModel != "llama3.2" {
		t.Fatalf("model not in body: %v", gotModel)
	}
}

// TestGetConversationExportFormats توزیع فرمت خروجی: md (و نام‌های
// مستعار markdown/خالی)، json (round-trip کامل)، فرمت ناشناخته → خطا.
func TestGetConversationExportFormats(t *testing.T) {
	a := newTestApp(t)
	conv := &Conversation{ID: newID(), Title: "صادرات", CreatedAt: time.Now().Unix(), Messages: []Message{
		{ID: newID(), Role: "user", Content: "سلام", At: time.Now().Unix()},
		{ID: newID(), Role: "assistant", Content: "درود", At: time.Now().Unix()},
	}}
	a.mu.Lock()
	err := a.saveConversation(conv)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	// md + نام‌های مستعار — همان خروجی
	for _, fmtName := range []string{"md", "markdown", "  ", "MD"} {
		md, err := a.GetConversationExport(conv.ID, fmtName)
		if err != nil {
			t.Fatalf("format %q: %v", fmtName, err)
		}
		if !strings.Contains(md, "# صادرات") || !strings.Contains(md, "سلام") || !strings.Contains(md, "درود") {
			t.Fatalf("markdown export (%q) missing pieces:\n%s", fmtName, md)
		}
		if !strings.Contains(md, faDateTimeStatic(conv.CreatedAt)) {
			t.Fatalf("markdown export (%q) missing jalali date stamp", fmtName)
		}
	}

	// json — round-trip کامل ساختار
	js, err := a.GetConversationExport(conv.ID, "  JSON ")
	if err != nil {
		t.Fatal(err)
	}
	var back Conversation
	if err := json.Unmarshal([]byte(js), &back); err != nil {
		t.Fatalf("exported json must unmarshal: %v", err)
	}
	if back.ID != conv.ID || back.Title != conv.Title || len(back.Messages) != 2 || back.Messages[1].Content != "درود" {
		t.Fatalf("json round-trip mismatch: %+v", back)
	}

	// فرمت ناشناخته → خطای راهنما
	if _, err := a.GetConversationExport(conv.ID, "xml"); err == nil || !strings.Contains(err.Error(), "فرمت ناشناخته") {
		t.Fatalf("unknown format must fail with guidance: %v", err)
	}
	// گفتگوی ناموجود → خطا (بدون پنیک)
	if _, err := a.GetConversationExport("does-not-exist", "md"); err == nil {
		t.Fatal("missing conversation must fail")
	}
}

// TestExportConversationJSONKeepsImages خروجی JSON باید data URL تصویر را
// کامل حفظ کند (برخلاف MD که فقط شمارش می‌کند) — تصویر در گفتگوی صادرشده
// قابل بازیابی است.
func TestExportConversationJSONKeepsImages(t *testing.T) {
	c := &Conversation{ID: newID(), Title: "با تصویر", CreatedAt: time.Now().Unix(), Messages: []Message{
		{ID: newID(), Role: "user", Content: "این را ببین", Images: []string{testPNGData}, At: time.Now().Unix()},
	}}
	js, err := exportConversationJSON(c)
	if err != nil {
		t.Fatal(err)
	}
	var back Conversation
	if err := json.Unmarshal([]byte(js), &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Messages) != 1 || len(back.Messages[0].Images) != 1 || back.Messages[0].Images[0] != testPNGData {
		t.Fatalf("json export must keep the full data URL, got: %+v", back.Messages[0].Images)
	}
}

// TestAtomicWritesLeaveNoTmp نوشتن اتمیک همه‌ی فایل‌های داده (settings،
// گفتگوها، changes) — پس از نوشتنِ موفق نباید فایل موقتی باقی بماند و
// داده باید round-trip شود.
func TestAtomicWritesLeaveNoTmp(t *testing.T) {
	a := newTestApp(t)

	conv := &Conversation{ID: newID(), Title: "t", CreatedAt: time.Now().Unix(), Messages: []Message{
		{ID: newID(), Role: "user", Content: "سلام", At: time.Now().Unix()},
	}}
	a.mu.Lock()
	if err := a.saveConversation(conv); err != nil {
		t.Fatal(err)
	}
	a.mu.Unlock()

	cdir, err := a.conversationsDir()
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(cdir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind in conversations dir: %s", e.Name())
		}
	}
	if _, err := a.loadConversation(conv.ID); err != nil {
		t.Fatalf("conversation must round-trip: %v", err)
	}
	if loadConversationIndex(cdir) == nil {
		t.Fatal("conversation index missing after save")
	}

	// changes.json — از مسیر قفل‌دارِ خودش
	a.changes.mu.Lock()
	a.appendChangeLocked(FileChange{
		ID: newID(), ConvID: conv.ID, Path: "x.txt", Kind: "modify",
		Added: 1, Removed: 0, At: time.Now().Unix(),
	})
	a.changes.mu.Unlock()

	p, err := a.changesPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("changes.json missing after append")
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Fatal("changes.json.tmp left behind")
	}
	a.changes.mu.Lock()
	list := a.loadChangesLocked()
	a.changes.mu.Unlock()
	if len(list) != 1 || list[0].Path != "x.txt" {
		t.Fatalf("changes must round-trip, got: %+v", list)
	}
}

// ---------------------------------------------------------------- F3

// TestCappedGitBuffer رفتار نویسنده‌ی کراندار: نگه‌داشتن «limit» بایت اول،
// شمارش بقیه و بازگشتِ موفقِ Write (تا فرایند git کشته نشود).
