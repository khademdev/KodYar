// تست‌های رگرسیون باگ‌های بازبینی حلقهٔ ایجنت:
//
//  1. search_files با context>0 نتایج را بی‌صدا کم برمی‌گرداند (سقف با خطوط
//     context پر می‌شد و نشانگر «limit reached» چاپ نمی‌شد).
//  2. search_files لغوپذیر نبود (Stop در درخت بزرگ اثری نداشت).
//  3. شمارش «more items not shown» در list_dir آیتم‌های ردشده را هم می‌شمرد.
//  4. process_output با offset=0 نمی‌توانست لاگ را از ابتدا بخواند.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workspaceWithFiles یک پوشهٔ کاری آزمایشی با n فایل و در هر فایل یک تطبیق.
func workspaceWithFiles(t *testing.T, n int) *App {
	t.Helper()
	a := newTestApp(t)
	ws := t.TempDir()
	a.settings.WorkspacePath = ws
	for i := 0; i < n; i++ {
		content := fmt.Sprintf("a\nb\nc\nNEEDLE %d\nd\ne\nf\n", i)
		if err := os.WriteFile(filepath.Join(ws, fmt.Sprintf("f%03d.txt", i)), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

// خطوط context نباید سقف max_results را پر کنند: با 60 تطبیق و سقف ۲۰ باید
// دقیقاً ۲۰ تطبیق برگردد و نشانگر «limit reached» هم بیاید.
func TestSearchFilesContextDoesNotCapResults(t *testing.T) {
	a := workspaceWithFiles(t, 60)
	out, ok := a.toolSearchFilesOpts(context.Background(), searchOptions{Query: "NEEDLE", Context: 5, MaxResults: 20})
	if !ok {
		t.Fatalf("search failed: %q", out)
	}
	if hits := strings.Count(out, "NEEDLE "); hits != 20 {
		t.Fatalf("context lines must not consume the result cap: got %d hit(s), want 20 (%q)", hits, out)
	}
	if !strings.Contains(out, "result limit reached") {
		t.Fatalf("a capped search must say so, otherwise the agent believes it saw every match: %q", out)
	}
}

// بدون context هم سقف باید دقیق رعایت شود (رفتار قبلی حفظ شود).
func TestSearchFilesCapWithoutContext(t *testing.T) {
	a := workspaceWithFiles(t, 30)
	out, ok := a.toolSearchFilesOpts(context.Background(), searchOptions{Query: "NEEDLE", MaxResults: 5})
	if !ok {
		t.Fatalf("search failed: %q", out)
	}
	if hits := strings.Count(out, "NEEDLE "); hits != 5 {
		t.Fatalf("got %d hit(s), want 5", hits)
	}
	if !strings.Contains(out, "result limit reached") {
		t.Fatalf("capped search must report the limit: %q", out)
	}
}

// جستجو باید مثل glob با لغو کاربر فوراً برگردد و نتیجهٔ نصفه را «موفق» اعلام نکند.
func TestSearchFilesCancelled(t *testing.T) {
	a := workspaceWithFiles(t, 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, ok := a.toolSearchFilesOpts(ctx, searchOptions{Query: "NEEDLE"})
	if ok || !strings.Contains(out, "cancelled by the user") {
		t.Fatalf("a cancelled search must fail with a clear message, got ok=%v out=%q", ok, out)
	}
}

// list_dir باید فقط آیتم‌های نمایش‌پذیر را بشمارد (نه پوشه‌های cache و نه
// فایل‌های نادیده‌گرفته‌شده) و وقتی چیزی پنهان نمانده هشدار «more items» ندهد.
func TestListDirCountsOnlyDisplayableItems(t *testing.T) {
	a := newTestApp(t)
	ws := t.TempDir()
	a.settings.WorkspacePath = ws
	for _, name := range []string{"f0.txt", "f1.txt", "f2.txt", "f3.txt"} {
		if err := os.WriteFile(filepath.Join(ws, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ws, ".gitignore"), []byte("f3.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, ok := a.toolListDir(".")
	if !ok {
		t.Fatalf("list_dir failed: %q", out)
	}
	// نمایش‌پذیرها: f0, f1, f2, .gitignore (f3 نادیده، node_modules پرش)
	if !strings.Contains(out, "4 item(s)") {
		t.Fatalf("header must count only displayable items, got %q", out)
	}
	if strings.Contains(out, "more items") {
		t.Fatalf("nothing was hidden by the cap, so no remainder notice is expected: %q", out)
	}
	if strings.Contains(out, "f3.txt") || strings.Contains(out, "node_modules") {
		t.Fatalf("ignored/cache entries must stay hidden: %q", out)
	}
}

// offset=0 که مدل صریح می‌فرستد یعنی «از ابتدای لاگ بخوان»؛ نبودِ offset یعنی
// «فقط خروجی جدید» — دو رفتار نباید قاطی شوند.
func TestProcessOutputOffsetZeroReadsFromStart(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	p, err := a.startBackgroundProcess(dir, bgCmd(`echo first`, `echo first`), nil, nil, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _, _ = a.stopBackgroundProcess(p.id) })
	waitForProcessOutput(t, p, "first")

	first, ok := a.toolProcessOutput(p.id, 0, false, 0)
	if !ok || !strings.Contains(first, "first") {
		t.Fatalf("incremental read must return the output: ok=%v out=%q", ok, first)
	}
	// خواندن افزایشی دوم خروجی جدیدی ندارد…
	second, ok := a.toolProcessOutput(p.id, 0, false, 0)
	if !ok || !strings.Contains(second, "(no new output)") {
		t.Fatalf("a second incremental read must report no new output: ok=%v out=%q", ok, second)
	}
	// …ولی offset=0 صریح باید همان لاگ را از سر بدهد.
	again, ok := a.toolProcessOutput(p.id, 0, true, 0)
	if !ok || !strings.Contains(again, "first") {
		t.Fatalf("an explicit offset=0 must re-read the log from the beginning: ok=%v out=%q", ok, again)
	}
	if !strings.Contains(again, "read from offset 0") {
		t.Fatalf("the header must show that the read started at offset 0: %q", again)
	}
}
