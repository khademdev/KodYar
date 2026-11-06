// تست‌های مرحله‌ی ۲ مأموریت ۲ (مدل نوبت + حالت ویرایش + مسیرهای محافظت‌شده).
//
// پوشش: مهاجرت EditMode از فایل‌های قدیمی، اعتبارسنجی/clamp تنظیم‌ها،
// تطبیق مسیرهای محافظت‌شده، پرسشِ مسیر محافظت‌شده/حذف سنگین حتی در auto،
// پیام صادقِ نتیجه، ضبط‌کننده‌ی نوبت (راستی‌آزمایی)، خلاصه‌ی پایان نوبت
// (payload + سقف ۵۰)، برگرداندن بایت‌دقیقِ نوبت، تشخیص تعارض و سازگاری
// با رکوردهای قدیمی بدون TurnID.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------- حالت ویرایش

// TestEditModeMigration: فایل قدیمی بدون editMode → از confirmFileWrites
// مشتق می‌شود؛ مقدار نامعتبر → auto؛ هر دو فیلد همیشه همگام.
func TestEditModeMigration(t *testing.T) {
	cases := []struct {
		name            string
		json            string
		wantMode        string
		wantConfirmSync bool
	}{
		{"old-on", `{"confirmFileWrites":true}`, "review_each", true},
		{"old-off", `{"confirmFileWrites":false}`, "auto", false},
		{"new-review", `{"confirmFileWrites":false,"editMode":"review_each"}`, "review_each", true},
		{"new-auto", `{"confirmFileWrites":true,"editMode":"auto"}`, "auto", false},
		{"invalid", `{"editMode":"yolo"}`, "auto", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(tc.json), 0o600); err != nil {
				t.Fatal(err)
			}
			os.Setenv("MYAI_CONFIG_DIR", dir)
			a := NewApp()
			if err := a.loadSettings(); err != nil {
				t.Fatal(err)
			}
			if a.settings.EditMode != tc.wantMode {
				t.Fatalf("EditMode = %q, want %q", a.settings.EditMode, tc.wantMode)
			}
			if a.settings.ConfirmFileWrites != tc.wantConfirmSync {
				t.Fatalf("ConfirmFileWrites = %v, want %v (must stay in sync)", a.settings.ConfirmFileWrites, tc.wantConfirmSync)
			}
		})
	}
}

// TestDefaultEditModeAuto: پیش‌فرض نصب تازه = auto (تغییر عمدی ۱.۸.۰).
func TestDefaultEditModeAuto(t *testing.T) {
	s := defaultSettings()
	if s.EditMode != "auto" {
		t.Fatalf("default EditMode = %q, want auto", s.EditMode)
	}
	if s.ConfirmFileWrites {
		t.Fatal("default ConfirmFileWrites must be false in 1.8.1")
	}
	if s.MaxTurnTokens != defaultMaxTurnTokens {
		t.Fatalf("default MaxTurnTokens = %d, want %d", s.MaxTurnTokens, defaultMaxTurnTokens)
	}
}

// TestSetEditMode: مقادیر معتبر پذیرفته و نرمال می‌شوند؛ نامعتبر خطا؛
// ConfirmFileWrites همگام می‌ماند و روی دیسک می‌رود.
func TestSetEditMode(t *testing.T) {
	a := newTestApp(t)
	if err := a.SetEditMode("review-each"); err != nil {
		t.Fatal(err)
	}
	if a.settings.EditMode != "review_each" || !a.settings.ConfirmFileWrites {
		t.Fatalf("normalize/sync failed: %q %v", a.settings.EditMode, a.settings.ConfirmFileWrites)
	}
	if err := a.SetEditMode("AUTO"); err != nil {
		t.Fatal(err)
	}
	if a.settings.EditMode != "auto" || a.settings.ConfirmFileWrites {
		t.Fatalf("upper-case normalize/sync failed: %q %v", a.settings.EditMode, a.settings.ConfirmFileWrites)
	}
	if err := a.SetEditMode("sometimes"); err == nil {
		t.Fatal("invalid mode must be rejected")
	}
	// ماندگاری روی دیسک
	a2 := NewApp()
	os.Setenv("MYAI_CONFIG_DIR", a.mustConfigDir())
	if err := a2.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if a2.settings.EditMode != "auto" {
		t.Fatalf("EditMode not persisted: %q", a2.settings.EditMode)
	}
}

// TestSetMaxTurnTokensClamp: صفر = خاموش؛ کران‌های ۵۰ هزار و ۱۰ میلیون.
func TestSetMaxTurnTokensClamp(t *testing.T) {
	a := newTestApp(t)
	if err := a.SetMaxTurnTokens(0); err != nil {
		t.Fatal(err)
	}
	if a.settings.MaxTurnTokens != 0 {
		t.Fatalf("0 must stay 0 (disabled), got %d", a.settings.MaxTurnTokens)
	}
	if err := a.SetMaxTurnTokens(1000); err != nil {
		t.Fatal(err)
	}
	if a.settings.MaxTurnTokens != 50000 {
		t.Fatalf("1000 must clamp to 50000, got %d", a.settings.MaxTurnTokens)
	}
	if err := a.SetMaxTurnTokens(20000000); err != nil {
		t.Fatal(err)
	}
	if a.settings.MaxTurnTokens != 10000000 {
		t.Fatalf("20M must clamp to 10M, got %d", a.settings.MaxTurnTokens)
	}
	if err := a.SetMaxTurnTokens(123456); err != nil {
		t.Fatal(err)
	}
	if a.settings.MaxTurnTokens != 123456 {
		t.Fatalf("in-range value must be kept, got %d", a.settings.MaxTurnTokens)
	}
}

// ---------------------------------------------------------------- مسیرهای محافظت‌شده

func TestPathMatchesProtectedGlobs(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{".git/config", true},
		{".git/hooks/pre-commit", true},
		{".github/workflows/ci.yml", true},
		{".myai/rules.md", true},
		{".vscode/settings.json", true},
		{".env", true},
		{".env.local", true},
		{"src/.env", true},         // الگوی بدون «/» با نام فایل تطبیق می‌کند
		{"package.json", true},     // فایل تعریف ساخت
		{"web/package.json", true}, // در هر عمقی
		{"Makefile", true},
		{"makefile", true}, // حساس به بزرگی/کوچکی نیست
		{"tasks.mk", true},
		{"justfile", true},
		{"Taskfile.yaml", true},
		{"Taskfile.yml", true},
		{"app.csproj", true},
		{"pom.xml", true},
		{"build.gradle", true},
		{"build.gradle.kts", true},
		{"server.pem", true},
		{"keys/api.key", true},
		{"cert.pfx", true},
		// نامطابق‌ها
		{"src/main.go", false},
		{"README.md", false},
		{"packaged.json", false},
		{"mypackage.jsonx", false},
		{"docs/package.json.md", false},
		{"my.git/config", false}, // «.git» باید جزء مسیر باشد، نه پسوند
		// C.5 (تغییر عمدی رفتار): مخزن تودرتو هم محافظت می‌شود (**/.git/**)
		{"src/.git/config", true},
		{"environment.txt", false},
		{"makerfile", false},
		// جداکننده‌ی ویندوزی
		{`src\.git\config`, true}, // مثل بالا (C.5)
		{`src\app\package.json`, true},
	}
	for _, tc := range cases {
		if got := pathMatchesProtectedGlobs(tc.rel, defaultProtectedGlobs); got != tc.want {
			t.Errorf("pathMatchesProtectedGlobs(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
}

// autoAcceptConfirms تأییدگر خودکار: اولین درخواست مودال را «اجازه» جواب
// می‌دهد. برای تست‌هایی که در auto هم پرسش انتظار دارند.
func autoAcceptConfirms(a *App) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			a.changes.mu.Lock()
			var id string
			for k := range a.changes.confirms {
				id = k
				break
			}
			a.changes.mu.Unlock()
			if id != "" {
				_ = a.RespondFileConfirm(id, true)
				return
			}
			SleepShort()
		}
	}()
	return done
}

// TestProtectedPathAsksInAutoMode: نوشتن روی مسیر محافظت‌شده حتی در auto
// مودال می‌خواهد؛ مسیر عادی بی‌پرسش اعمال و با پیام صادق اعلام می‌شود.
func TestProtectedPathAsksInAutoMode(t *testing.T) {
	a := newTestApp(t) // EditMode=auto
	a.ctx = context.Background()
	ws := a.settings.WorkspacePath

	// مسیر عادی → بدون مودال، پیام «auto-applied»
	r, ok := a.execTool("write_file", `{"path":"notes.txt","content":"hello"}`)
	if !ok {
		t.Fatal(r)
	}
	if !strings.Contains(r, "auto-applied") {
		t.Fatalf("auto-mode message must be honest, got: %s", r)
	}
	if strings.Contains(r, "approved by the user") {
		t.Fatalf("auto-mode message must not claim user approval: %s", r)
	}
	if _, err := os.Stat(filepath.Join(ws, "notes.txt")); err != nil {
		t.Fatal("file must be written in auto mode")
	}

	// مسیر محافظت‌شده → مودال + یادداشت فارسی + پرچم Protected
	a.changes.mu.Lock()
	nConfirms := len(a.changes.confirms)
	a.changes.mu.Unlock()
	if nConfirms != 0 {
		t.Fatalf("no confirm expected for normal write, got %d", nConfirms)
	}

	done := autoAcceptConfirms(a)
	r, ok = a.execTool("write_file", `{"path":"package.json","content":"{}"}`)
	if !ok {
		t.Fatal(r)
	}
	<-done
	list := a.loadChanges()
	var rec *FileChange
	for i := range list {
		if list[i].Path == "package.json" {
			rec = &list[i]
		}
	}
	if rec == nil {
		t.Fatal("protected write must be recorded")
	}
	if !rec.Protected {
		t.Fatal("protected write must set Protected flag")
	}
	if !strings.Contains(rec.ConfirmNote, "محافظت‌شده") {
		t.Fatalf("ConfirmNote must explain in Persian, got: %q", rec.ConfirmNote)
	}
	if !strings.Contains(r, "approved by the user") {
		t.Fatalf("asked write must say approved by the user, got: %s", r)
	}
}

// TestHeavyDeleteAsksInAutoMode: حذف پوشه یا فایل بزرگ‌تر از ۵ مگابایت حتی
// در auto تأیید می‌خواهد؛ حذف فایل کوچک عادی بی‌پرسش است.
func TestHeavyDeleteAsksInAutoMode(t *testing.T) {
	a := newTestApp(t) // auto
	a.ctx = context.Background()
	ws := a.settings.WorkspacePath

	// فایل کوچک → بدون پرسش
	if _, ok := a.execTool("write_file", `{"path":"small.txt","content":"x"}`); !ok {
		t.Fatal("setup write failed")
	}
	if r, ok := a.toolDeletePath(context.Background(), "small.txt"); !ok {
		t.Fatalf("small delete must not ask in auto: %s", r)
	}

	// پوشه → پرسش
	if err := os.MkdirAll(filepath.Join(ws, "somedir"), 0o755); err != nil {
		t.Fatal(err)
	}
	done := autoAcceptConfirms(a)
	if r, ok := a.toolDeletePath(context.Background(), "somedir"); !ok {
		t.Fatalf("dir delete must be allowed after confirm: %s", r)
	}
	<-done
	for _, c := range a.loadChanges() {
		if c.Path == "somedir" {
			if !c.HeavyDelete {
				t.Fatal("dir delete must be flagged HeavyDelete")
			}
			if !strings.Contains(c.ConfirmNote, "پوشه") {
				t.Fatalf("dir delete note must mention folder: %q", c.ConfirmNote)
			}
		}
	}

	// فایل بزرگ (>۵MB) → پرسش
	big := make([]byte, 5*1024*1024+1)
	if err := os.WriteFile(filepath.Join(ws, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	done = autoAcceptConfirms(a)
	if r, ok := a.toolDeletePath(context.Background(), "big.bin"); !ok {
		t.Fatalf("big delete must be allowed after confirm: %s", r)
	}
	<-done
	for _, c := range a.loadChanges() {
		if c.Path == "big.bin" && !c.HeavyDelete {
			t.Fatal("big delete must be flagged HeavyDelete")
		}
	}
}

// ---------------------------------------------------------------- ضبط‌کننده‌ی نوبت

func TestIsVerificationCommand(t *testing.T) {
	yes := []string{
		"go test ./...", "go build", "go vet", "GO TEST ./...", "npm test",
		"npm run build", "cargo test", "pytest -q", "python -m pytest",
		"mvn test", "make test", "tsc --noEmit", "dotnet build",
	}
	no := []string{"", "goat test", "echo go test", "npm install", "make clean", "cat go test"}
	for _, c := range yes {
		if !isVerificationCommand(c) {
			t.Errorf("isVerificationCommand(%q) = false, want true", c)
		}
	}
	for _, c := range no {
		if isVerificationCommand(c) {
			t.Errorf("isVerificationCommand(%q) = true, want false", c)
		}
	}
}

func TestTurnRecorderVerificationWindow(t *testing.T) {
	// راستی‌آزماییِ موفقِ «بعد از» آخرین تغییر → OK
	rec := &turnRecorder{}
	rec.recordFileChange()
	rec.recordCommand("go test ./...", 0, true)
	_, v := rec.snapshot()
	if !v.Ran || !v.OK || v.Command != "go test ./..." {
		t.Fatalf("verified-after-change: %+v", v)
	}

	// راستی‌آزمایی «قبل از» تغییر → Ran اما نه OK
	rec = &turnRecorder{}
	rec.recordCommand("go build", 0, true)
	rec.recordFileChange()
	_, v = rec.snapshot()
	if !v.Ran {
		t.Fatal("verification ran")
	}
	if v.OK {
		t.Fatal("verification before the last change must not count as OK")
	}

	// تستِ خراب → راستی‌آزمایی حساب نمی‌شود
	rec = &turnRecorder{}
	rec.recordFileChange()
	rec.recordCommand("go test ./...", 1, false)
	_, v = rec.snapshot()
	if v.Ran {
		t.Fatal("failing test must not count as a verification run")
	}
	if v.OK {
		t.Fatal("failing test must not be OK")
	}

	// ضبط‌کننده‌ی nil بی‌خطر است (خارج از نوبت)
	var nilRec *turnRecorder
	nilRec.recordFileChange()
	nilRec.recordCommand("go test", 0, true)
	cmds, v := nilRec.snapshot()
	if cmds != nil || v.Ran || v.OK {
		t.Fatal("nil recorder must be a safe no-op")
	}
}

func TestTurnRecorderCommandCap(t *testing.T) {
	rec := &turnRecorder{}
	for i := 0; i < maxTurnCommands+50; i++ {
		rec.recordCommand("echo hi", 0, true)
	}
	cmds, _ := rec.snapshot()
	if len(cmds) != maxTurnCommands {
		t.Fatalf("commands must be capped at %d, got %d", maxTurnCommands, len(cmds))
	}
}

// ---------------------------------------------------------------- خلاصه‌ی نوبت

// newTurnTestApp اپِ تست با گفتگوی روی دیسک و شناسه‌ی گفتگوی فعال.
func newTurnTestApp(t *testing.T) (*App, string) {
	t.Helper()
	a := newTestApp(t)
	conv := &Conversation{ID: newID(), Title: "t", CreatedAt: 1, Messages: []Message{{ID: "m1", Role: "user", Content: "hi"}}}
	if err := a.saveConversation(conv); err != nil {
		t.Fatal(err)
	}
	a.genMu.Lock()
	a.gen = &genState{convID: conv.ID}
	a.genMu.Unlock()
	return a, conv.ID
}

func TestEmitTurnSummary(t *testing.T) {
	a, convID := newTurnTestApp(t)
	turnID := a.nextTurnID()
	rec := &turnRecorder{}
	ctx := withTurnContext(context.Background(), turnID, rec)

	// یک تغییر فایل + یک دستورِ راستی‌آزماییِ موفق
	if r, ok := a.toolWriteFile(ctx, "sum.go", "package main\n"); !ok {
		t.Fatal(r)
	}
	rec.recordCommand("go build ./...", 0, true)
	a.emitTurnSummary(convID, turnID, rec, 120, 30)

	got, err := a.loadConversation(convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.TurnSummaries) != 1 {
		t.Fatalf("want 1 turn summary, got %d", len(got.TurnSummaries))
	}
	s := got.TurnSummaries[0]
	if s.ConversationID != convID || s.TurnID != turnID {
		t.Fatalf("ids: %+v", s)
	}
	if len(s.Files) != 1 || s.Files[0].Path != "sum.go" || s.Files[0].Op != ChangeCreate {
		t.Fatalf("files: %+v", s.Files)
	}
	if len(s.Commands) != 1 || s.Commands[0].Command != "go build ./..." || !s.Commands[0].Verified {
		t.Fatalf("commands: %+v", s.Commands)
	}
	if !s.Verification.Ran || !s.Verification.OK || s.Verification.Command != "go build ./..." {
		t.Fatalf("verification: %+v", s.Verification)
	}
	if s.Tokens.Prompt != 120 || s.Tokens.Completion != 30 {
		t.Fatalf("tokens: %+v", s.Tokens)
	}

	// نوبتِ بدون تغییر و بدون دستور → هیچ خلاصه‌ای نمی‌سازد
	before := len(a.loadChanges())
	_ = before
	a.emitTurnSummary(convID, 999, &turnRecorder{}, 0, 0)
	got, _ = a.loadConversation(convID)
	if len(got.TurnSummaries) != 1 {
		t.Fatalf("empty turn must not emit a summary, got %d", len(got.TurnSummaries))
	}
}

func TestTurnSummariesBounded(t *testing.T) {
	a, convID := newTurnTestApp(t)
	// ۵۰ خلاصه‌ی از پیش موجود + یکی تازه → همچنان ۵۰
	conv, err := a.loadConversation(convID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxTurnSummariesPerConv; i++ {
		conv.TurnSummaries = append(conv.TurnSummaries, TurnSummary{ConversationID: convID, TurnID: int64(i + 1), At: int64(i)})
	}
	if err := a.saveConversation(conv); err != nil {
		t.Fatal(err)
	}
	turnID := a.nextTurnID()
	rec := &turnRecorder{}
	ctx := withTurnContext(context.Background(), turnID, rec)
	if r, ok := a.toolWriteFile(ctx, "bounded.txt", "x"); !ok {
		t.Fatal(r)
	}
	a.emitTurnSummary(convID, turnID, rec, 1, 1)

	got, _ := a.loadConversation(convID)
	if len(got.TurnSummaries) != maxTurnSummariesPerConv {
		t.Fatalf("must be bounded at %d, got %d", maxTurnSummariesPerConv, len(got.TurnSummaries))
	}
	last := got.TurnSummaries[len(got.TurnSummaries)-1]
	if last.TurnID != turnID {
		t.Fatalf("newest summary must be the fresh turn %d, got %d", turnID, last.TurnID)
	}
}

// ---------------------------------------------------------------- برگرداندن نوبت

func TestRevertTurnChangesByteExact(t *testing.T) {
	a, convID := newTurnTestApp(t)
	ws := a.settings.WorkspacePath
	turnID := a.nextTurnID()
	ctx := withTurnContext(context.Background(), turnID, &turnRecorder{})

	// فایل موجود با BOM + CRLF + متن فارسی با نیم‌فاصله
	orig := []byte("\xef\xbb\xbfسطر یک\r\nسطر دو با نیم‌فاصله‌ی ZWNJ\u200cاینجا\r\n")
	if err := os.WriteFile(filepath.Join(ws, "legacy.txt"), orig, 0o644); err != nil {
		t.Fatal(err)
	}
	modified := []byte("\xef\xbb\xbfجایگزین\r\n")
	// محافظ A.1: بازنویسی فایلِ موجود نیاز به خواندن قبلی دارد.
	if r, ok := a.toolReadFile(ctx, "legacy.txt", 0, 0); !ok {
		t.Fatal(r)
	}
	if r, ok := a.toolWriteFile(ctx, "legacy.txt", string(modified)); !ok {
		t.Fatal(r)
	}
	// فایل تازه هم در همان نوبت ساخته می‌شود
	if r, ok := a.toolWriteFile(ctx, "created.txt", "new"); !ok {
		t.Fatal(r)
	}

	rep := a.RevertTurnChanges(convID, turnID)
	if len(rep.Errors) != 0 || len(rep.Conflicts) != 0 {
		t.Fatalf("report: %+v", rep)
	}
	if len(rep.Reverted) != 2 {
		t.Fatalf("want 2 reverted, got %v", rep.Reverted)
	}
	data, err := os.ReadFile(filepath.Join(ws, "legacy.txt"))
	if err != nil || string(data) != string(orig) {
		t.Fatalf("byte-exact restore failed (err=%v, got %q)", err, data)
	}
	if _, err := os.Stat(filepath.Join(ws, "created.txt")); !os.IsNotExist(err) {
		t.Fatal("created file must be removed on revert")
	}
	// رکوردها برگشته علامت خورده‌اند
	for _, c := range a.loadChanges() {
		if c.TurnID == turnID && !c.Reverted {
			t.Fatalf("change %s must be marked reverted", c.Path)
		}
	}
	// برگرداندن دوباره → هیچ چیز جدید
	rep = a.RevertTurnChanges(convID, turnID)
	if len(rep.Reverted) != 0 {
		t.Fatalf("second revert must be a no-op, got %v", rep.Reverted)
	}
}

func TestRevertTurnChangesConflict(t *testing.T) {
	a, convID := newTurnTestApp(t)
	ws := a.settings.WorkspacePath
	turnID := a.nextTurnID()
	ctx := withTurnContext(context.Background(), turnID, &turnRecorder{})

	if r, ok := a.toolWriteFile(ctx, "conflict.txt", "agent version"); !ok {
		t.Fatal(r)
	}
	// کاربر بعد از ایجنت فایل را ویرایش می‌کند
	if err := os.WriteFile(filepath.Join(ws, "conflict.txt"), []byte("user version"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := a.RevertTurnChanges(convID, turnID)
	if len(rep.Reverted) != 0 {
		t.Fatalf("conflicting file must not be reverted, got %v", rep.Reverted)
	}
	if len(rep.Conflicts) != 1 || rep.Conflicts[0] != "conflict.txt" {
		t.Fatalf("conflicts: %v", rep.Conflicts)
	}
	data, _ := os.ReadFile(filepath.Join(ws, "conflict.txt"))
	if string(data) != "user version" {
		t.Fatal("user's edit must win over revert")
	}
}

func TestRevertTurnChangesDeleteConflict(t *testing.T) {
	a, convID := newTurnTestApp(t)
	ws := a.settings.WorkspacePath
	turnID := a.nextTurnID()
	ctx := withTurnContext(context.Background(), turnID, &turnRecorder{})

	if err := os.WriteFile(filepath.Join(ws, "gone.txt"), []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.ctx = context.Background()
	done := autoAcceptConfirms(a) // حذف فایل کوچک در auto پرسش ندارد؛ تأییدگر برای اطمینان
	if r, ok := a.toolDeletePath(ctx, "gone.txt"); !ok {
		t.Fatal(r)
	}
	select {
	case <-done:
	default:
	}
	// کاربر فایل را دوباره می‌سازد
	if err := os.WriteFile(filepath.Join(ws, "gone.txt"), []byte("recreated"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := a.RevertTurnChanges(convID, turnID)
	found := false
	for _, p := range rep.Conflicts {
		if p == "gone.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("recreated file must be a conflict, report: %+v", rep)
	}
	data, _ := os.ReadFile(filepath.Join(ws, "gone.txt"))
	if string(data) != "recreated" {
		t.Fatal("user's recreated file must not be overwritten")
	}
}

func TestGetTurnChanges(t *testing.T) {
	a, convID := newTurnTestApp(t)
	t1 := a.nextTurnID()
	t2 := a.nextTurnID()
	ctx1 := withTurnContext(context.Background(), t1, &turnRecorder{})
	ctx2 := withTurnContext(context.Background(), t2, &turnRecorder{})
	if r, ok := a.toolWriteFile(ctx1, "a.txt", "a"); !ok {
		t.Fatal(r)
	}
	if r, ok := a.toolWriteFile(ctx2, "b.txt", "b"); !ok {
		t.Fatal(r)
	}
	got := a.GetTurnChanges(convID, t1)
	if len(got) != 1 || got[0].Path != "a.txt" {
		t.Fatalf("turn changes: %+v", got)
	}
	if got[0].Diff != "" {
		t.Fatal("summary records must not carry the diff payload")
	}
	if got[0].TurnID != t1 {
		t.Fatalf("TurnID = %d, want %d", got[0].TurnID, t1)
	}
	// نوبت نامعتبر → خالی
	if got := a.GetTurnChanges(convID, 0); got != nil {
		t.Fatalf("turn 0 must return nil, got %+v", got)
	}
	// شناسه‌ی نامعتبر → خطا در گزارش
	rep := a.RevertTurnChanges("", 5)
	if len(rep.Errors) == 0 {
		t.Fatal("invalid ids must produce an error in the report")
	}
}

// TestOldChangesWithoutTurnID: رکوردهای قدیمی (بدون turnId) بارگذاری
// می‌شوند و در خلاصه/برگرداندنِ نوبت‌ها شرکت نمی‌کنند — رفتار پیشین
// (RevertFileChange تک‌فایلی) دست‌نخورده می‌ماند.
func TestOldChangesWithoutTurnID(t *testing.T) {
	a := newTestApp(t)
	dir := a.mustConfigDir()
	old := []map[string]interface{}{
		{"id": "old1", "convId": "c1", "path": "x.txt", "kind": "modify", "added": 1, "removed": 0, "at": 1},
	}
	raw, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(dir, "changes.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	a2 := NewApp()
	os.Setenv("MYAI_CONFIG_DIR", dir)
	a2.initChangesStore()
	list := a2.loadChanges()
	if len(list) != 1 || list[0].TurnID != 0 {
		t.Fatalf("old changes must load with TurnID 0: %+v", list)
	}
	if got := a2.turnFileChanges("c1", 1); got != nil {
		t.Fatalf("old records must not appear in turn queries: %+v", got)
	}
}

// TestTurnIDsDistinct: شناسه‌ی نوبت‌ها یکتا و صعودی است — حتی با فراخوانی
// هم‌زمان (نوبت‌های هم‌زمان در گفتگوهای مختلف).
func TestTurnIDsDistinct(t *testing.T) {
	a := newTestApp(t)
	const n = 64
	ids := make([]int64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i] = a.nextTurnID()
		}(i)
	}
	wg.Wait()
	seen := map[int64]bool{}
	for _, id := range ids {
		if id == 0 || seen[id] {
			t.Fatalf("turn ids must be unique and non-zero: %v", ids)
		}
		seen[id] = true
	}
}

// ------------------------------------ C.5: محافظت از .git تودرتو (submodule)

// TestProtectedNestedGitPaths: «**/.git/**» مسیرهای مخزن‌های تودرتو را هم
// محافظت می‌کند، ولی مسیرهای عادی را نه.
func TestProtectedNestedGitPaths(t *testing.T) {
	protected := []string{
		".git/config",
		"src/.git/config", // submodule
		"a/b/c/.git/HEAD", // عمق دلخواه
		"vendor/dep/.git/hooks/pre-commit",
		"package.json",
		".env",
		".env.local",
	}
	for _, p := range protected {
		if !pathMatchesProtectedGlobs(p, defaultProtectedGlobs) {
			t.Errorf("%q must be protected", p)
		}
	}
	notProtected := []string{
		"src/git/config", // بدون نقطه — فایل معمولی
		"src/.gitignore", // .gitignore عادی است، خودِ پوشه‌ی .git نه
		"src/main.go",
		"README.md",
		"docs/.github-notes.md",
	}
	for _, p := range notProtected {
		if pathMatchesProtectedGlobs(p, defaultProtectedGlobs) {
			t.Errorf("%q must not be protected", p)
		}
	}
}
