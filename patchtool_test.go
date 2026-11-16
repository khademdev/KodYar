// تست‌های ابزار apply_patch (ویرایش ساختاری با unified diff — گام ۷).
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustPatch(t *testing.T, text string) []patchFile {
	t.Helper()
	files, err := parseUnifiedPatch(text)
	if err != nil {
		t.Fatalf("parseUnifiedPatch: %v\npatch:\n%s", err, text)
	}
	return files
}

func TestPatchParseAndApplyMultiHunk(t *testing.T) {
	src := "one\ntwo\nthree\nfour\nfive\nsix\nseven\n"
	patch := "--- a/f.txt\n+++ b/f.txt\n" +
		"@@ -1,4 +1,4 @@\n one\n-two\n+TWO\n three\n four\n" +
		"@@ -5,3 +5,4 @@\n five\n+five-b\n six\n seven\n"
	files := mustPatch(t, patch)
	if len(files) != 1 || len(files[0].hunks) != 2 {
		t.Fatalf("expected one file with two hunks, got %+v", files)
	}
	out, err := applyHunks("f.txt", src, files[0].hunks, false)
	if err != nil {
		t.Fatalf("applyHunks: %v", err)
	}
	want := "one\nTWO\nthree\nfour\nfive\nfive-b\nsix\nseven\n"
	if out != want {
		t.Fatalf("wrong result:\n%q\nwant:\n%q", out, want)
	}
}

func TestPatchCreateAndDeleteSections(t *testing.T) {
	files := mustPatch(t, "--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1,2 @@\n+hello\n+world\n")
	f := files[0]
	if !f.create || f.newName != "new.txt" {
		t.Fatalf("create section not detected: %+v", f)
	}
	out, err := applyHunks("new.txt", "", f.hunks, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out != "hello\nworld\n" {
		t.Fatalf("created content wrong: %q", out)
	}

	files = mustPatch(t, "--- a/old.txt\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-bye\n")
	if !files[0].delete || files[0].oldName != "old.txt" {
		t.Fatalf("delete section not detected: %+v", files[0])
	}
}

func TestPatchRejectsBrokenPatches(t *testing.T) {
	cases := map[string]string{
		"no patch":          "just some text\n",
		"count mismatch":    "--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n",
		"no hunks":          "--- a/x\n+++ b/x\n",
		"orphan hunk":       "@@ -1,1 +1,1 @@\n-a\n+b\n",
		"garbage inside":    "--- a/x\n+++ b/x\n@@ -1,1 +1,1 @@\n-a\n+b\nraw garbage\n",
		"plus before minus": "+++ b/x\n@@ -1,1 +1,1 @@\n-a\n+b\n",
	}
	for name, p := range cases {
		if _, err := parseUnifiedPatch(p); err == nil {
			t.Fatalf("%s: expected a parse error", name)
		}
	}
}

func TestPatchApplyRejectsMismatchAndAmbiguity(t *testing.T) {
	// بافت پیدا نمی‌شود
	files := mustPatch(t, "--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n-nope\n+NOPE\n keep\n")
	if _, err := applyHunks("x", "keep\n", files[0].hunks, false); err == nil {
		t.Fatal("mismatched hunk must fail")
	}
	// بافت تکراری و جابه‌جا (موقعیت اعلام‌شده نمی‌خواند) → مبهم
	dup := "x\ny\na\nb\na\nb\n"
	files = mustPatch(t, "--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n a\n-b\n+B\n")
	if _, err := applyHunks("x", dup, files[0].hunks, false); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous hunk must be reported, got %v", err)
	}
	// خط شماره‌ی جابه‌جا شده ولی بافت یکتا → با جستجو پیدا می‌شود
	files = mustPatch(t, "--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n three\n-four\n+FOUR\n")
	out, err := applyHunks("x", "zero\none\ntwo\nthree\nfour\n", files[0].hunks, false)
	if err != nil {
		t.Fatalf("shifted hunk should still apply: %v", err)
	}
	if !strings.Contains(out, "FOUR") {
		t.Fatalf("shifted hunk not applied: %q", out)
	}
}

func TestPatchToolDryRunWritesNothing(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	p := filepath.Join(ws, "dry.txt")
	if err := os.WriteFile(p, []byte("alpha\nbeta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch := "--- a/dry.txt\n+++ b/dry.txt\n@@ -1,2 +1,2 @@\n alpha\n-beta\n+BETA\n"
	msg, ok := a.toolApplyPatch(context.Background(), patch, true)
	if !ok || !strings.Contains(msg, "DRY RUN OK") {
		t.Fatalf("dry run must succeed: ok=%v msg=%q", ok, msg)
	}
	if !strings.Contains(msg, "dry.txt") {
		t.Fatalf("dry run must list the file: %q", msg)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "alpha\nbeta\n" {
		t.Fatalf("dry run must not write, file is %q", string(data))
	}
}

func TestPatchToolRejectsOutsideWorkspace(t *testing.T) {
	a := newTestApp(t)
	patch := "--- a/../escape.txt\n+++ b/../escape.txt\n@@ -1,1 +1,1 @@\n-a\n+b\n"
	if msg, ok := a.toolApplyPatch(context.Background(), patch, true); ok {
		t.Fatalf("path outside the workspace must be rejected, got %q", msg)
	}
}

func TestPatchToolValidatesBeforeWritingAnything(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	if err := os.WriteFile(filepath.Join(ws, "ok.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// فایل دوم وجود ندارد → هیچ فایلی نباید نوشته شود
	patch := "--- a/ok.txt\n+++ b/ok.txt\n@@ -1,1 +1,1 @@\n-x\n+X\n" +
		"--- a/missing.txt\n+++ b/missing.txt\n@@ -1,1 +1,1 @@\n-a\n+b\n"
	if msg, ok := a.toolApplyPatch(context.Background(), patch, false); ok {
		t.Fatalf("patch with a missing file must fail, got %q", msg)
	}
	data, _ := os.ReadFile(filepath.Join(ws, "ok.txt"))
	if string(data) != "x\n" {
		t.Fatalf("validation must happen before writing, file changed to %q", string(data))
	}
	if _, err := os.Stat(filepath.Join(ws, "missing.txt")); err == nil {
		t.Fatal("absent file must not be created by a failed patch")
	}
}

// TestPatchEndToEnd — مدل پچ می‌فرستد و فایل واقعاً با ثبت تغییر ساخته می‌شود.
func TestPatchEndToEnd(t *testing.T) {
	patchJSON, _ := jsonStringForTest(map[string]interface{}{
		"patch": "--- /dev/null\n+++ b/hello.txt\n@@ -0,0 +1,2 @@\n+hello\n+patch\n",
	})
	env := newE2E(t,
		stepTool("c1", "apply_patch", patchJSON),
		stepText("فایل ساخته شد."),
	)
	env.send("یک فایل با پچ بساز")
	if got := env.readFile("hello.txt"); got != "hello\npatch\n" {
		t.Fatalf("apply_patch must write the file, got %q", got)
	}
}

// jsonStringForTest یک آبجکت را به JSON رمزگذاری می‌کند (کمکی تست).
func jsonStringForTest(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}
