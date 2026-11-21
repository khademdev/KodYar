// تست پوشش resolver امن روی همه‌ی ابزارهای فایل: هیچ ابزاری نباید بتواند از
// پوشه‌ی کاری بیرون برود. این تست برای همه‌ی سکوها اجرا می‌شود چون «..» در
// هر دو سبک مسیر معنا دارد؛ حالت‌های ویژه‌ی ویندوز در workspace_guard_windows_test.go
// هستند.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardJSONString رشته را به یک literal معتبر JSON تبدیل می‌کند (برای مسیرهای
// ویندوزی که بک‌اسلش دارند).
func guardJSONString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// TestFileToolsRejectParentTraversal هر ابزار فایل را با مسیر «بیرون از
// workspace» صدا می‌زند و انتظار خطای «outside» دارد؛ همچنین مطمئن می‌شود
// هیچ فایل/پوشه‌ای بیرون ساخته نشده باشد.
func TestFileToolsRejectParentTraversal(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	outsideFile := filepath.Join(filepath.Dir(ws), "escape.txt")
	outsideDir := filepath.Join(filepath.Dir(ws), "escapedir")

	// فایل بیرون از قبل وجود ندارد؛ بعد از تست هم نباید ساخته شده باشد.
	_ = os.Remove(outsideFile)
	_ = os.RemoveAll(outsideDir)

	patch := "--- ../escape.txt\n+++ ../escape.txt\n@@ -1,1 +1,1 @@\n-old\n+new\n"
	cases := []struct {
		tool string
		args string
	}{
		{"list_dir", `{"path":".."}`},
		{"read_file", `{"path":"../escape.txt"}`},
		{"write_file", `{"path":"../escape.txt","content":"owned"}`},
		{"edit_file", `{"path":"../escape.txt","old_string":"a","new_string":"b"}`},
		{"apply_patch", `{"patch":` + guardJSONString(patch) + `}`},
		{"move_path", `{"from":"../escape.txt","to":"moved.txt"}`},
		{"delete_path", `{"path":"../escape.txt"}`},
		{"create_dir", `{"path":"../escapedir"}`},
		{"search_files", `{"query":"root","path":".."}`},
		{"glob", `{"pattern":"*","path":".."}`},
		{diagToolName, `{"path":"../escape.txt"}`},
	}

	for _, c := range cases {
		out, ok := a.execTool(c.tool, c.args)
		if ok {
			t.Errorf("%s accepted a path outside the workspace: %s", c.tool, out)
			continue
		}
		if !strings.Contains(strings.ToLower(out), "outside") &&
			!strings.Contains(strings.ToLower(out), "escape") {
			t.Errorf("%s failed for the wrong reason (want an 'outside the workspace' error): %s", c.tool, out)
		}
	}

	if _, err := os.Stat(outsideFile); err == nil {
		t.Fatalf("a tool wrote outside the workspace: %s exists", outsideFile)
	}
	if _, err := os.Stat(outsideDir); err == nil {
		t.Fatalf("a tool created a directory outside the workspace: %s", outsideDir)
	}
}

// TestFileToolsRejectAbsoluteOutsidePath همان پوشش را برای مسیر مطلق بیرونی
// بررسی می‌کند (مسیری که هیچ ابزاری نباید بپذیرد).
func TestFileToolsRejectAbsoluteOutsidePath(t *testing.T) {
	a := newTestApp(t)
	abs := filepath.Join(t.TempDir(), "outside.txt")

	cases := []struct{ tool, args string }{
		{"read_file", `{"path":` + guardJSONString(abs) + `}`},
		{"write_file", `{"path":` + guardJSONString(abs) + `,"content":"x"}`},
		{"list_dir", `{"path":` + guardJSONString(filepath.Dir(abs)) + `}`},
		{"glob", `{"pattern":"*","path":` + guardJSONString(filepath.Dir(abs)) + `}`},
	}
	for _, c := range cases {
		out, ok := a.execTool(c.tool, c.args)
		if ok {
			t.Errorf("%s accepted an absolute path outside the workspace: %s", c.tool, out)
			continue
		}
		if !strings.Contains(strings.ToLower(out), "outside") {
			t.Errorf("%s failed for the wrong reason: %s", c.tool, out)
		}
	}
	if _, err := os.Stat(abs); err == nil {
		t.Fatalf("write_file created a file outside the workspace: %s", abs)
	}
}
