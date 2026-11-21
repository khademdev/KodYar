// تست: نوشتن فایل‌هایی که نیمه‌ی «تنظیم» حمله‌ی درایور بیرونی هستند
// (.gitattributes و .git/config) در هر دو حالت ویرایش باید تأیید بگیرد.
//
// در تست بدون UI، «تأیید گرفتن» یعنی تغییر اعمال نمی‌شود (مودال بی‌پاسخ =
// رد). برای اینکه تست بی‌اثر نباشد، همان حالت خودکار روی یک فایل معمولی
// نوشتن را انجام می‌دهد — پس شکستِ موارد بالا واقعاً از محافظت مسیر است.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// protectedMode یک حالت ویرایش را روی App اعمال می‌کند.
type protectedMode struct {
	name     string
	editMode string
	confirm  bool
}

var protectedModes = []protectedMode{
	{"auto", "auto", false},
	{"review_each", "review_each", true},
}

// sanitizeProtectedPaths مسیرهای محافظت‌شده‌ای که این تست می‌نویسد.
var protectedTargets = []string{
	".gitattributes",
	".git/config",
	"sub/.gitattributes",
	"sub/.git/config",
}

func TestProtectedRepoFilesAlwaysRequireConfirm(t *testing.T) {
	for _, mode := range protectedModes {
		t.Run(mode.name, func(t *testing.T) {
			a := newTestApp(t)
			a.settings.EditMode = mode.editMode
			a.settings.ConfirmFileWrites = mode.confirm
			ws := a.settings.WorkspacePath

			// کنترل: در حالت خودکار، فایل معمولی بی‌تأیید نوشته می‌شود؛ پس اگر
			// موارد زیر رد شوند، دلیلش محافظت مسیر است نه نبود UI.
			if _, ok := a.execTool("write_file", `{"path":"notes.txt","content":"ok\n"}`); mode.name == "auto" && !ok {
				t.Fatal("auto mode must write an ordinary file without a modal (control)")
			}

			for _, rel := range protectedTargets {
				out, ok := a.execTool("write_file", fmt.Sprintf(`{"path":%q,"content":"*.txt diff=evil\n"}`, rel))
				if ok {
					t.Fatalf("%s mode: write_file(%q) was applied without confirmation: %s", mode.name, rel, out)
				}
				if _, err := os.Stat(filepath.Join(ws, filepath.FromSlash(rel))); err == nil {
					t.Fatalf("%s mode: %q exists on disk after a refused write", mode.name, rel)
				}
			}
		})
	}
}

// TestProtectedRepoFilesBlockEditAndPatch همان بررسی برای edit_file،
// apply_patch و move_path — همه‌ی مسیرهای نوشتن باید از همان محافظت بگذرند.
func TestProtectedRepoFilesBlockEditAndPatch(t *testing.T) {
	a := newTestApp(t)
	a.settings.EditMode = "auto"
	a.settings.ConfirmFileWrites = false
	ws := a.settings.WorkspacePath

	// فایل روی دیسک ساخته می‌شود (نه با ابزار) تا edit/patch مسیر واقعی داشته باشند.
	if err := os.WriteFile(filepath.Join(ws, ".gitattributes"), []byte("*.txt diff=evil\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, ok := a.execTool("read_file", `{"path":".gitattributes"}`); !ok {
		t.Fatalf("read_file: %s", r)
	}

	if out, ok := a.execTool("edit_file", `{"path":".gitattributes","old_string":"evil","new_string":"evil2"}`); ok {
		t.Fatalf("edit_file changed .gitattributes without confirmation: %s", out)
	}
	if data, _ := os.ReadFile(filepath.Join(ws, ".gitattributes")); !strings.Contains(string(data), "evil\n") {
		t.Fatalf("edit_file changed the protected file anyway: %q", data)
	}

	patch := "--- a/.gitattributes\n+++ b/.gitattributes\n@@ -1,1 +1,1 @@\n-*.txt diff=evil\n+*.txt diff=evil2\n"
	if out, ok := a.execTool("apply_patch", fmt.Sprintf(`{"patch":%q}`, patch)); ok {
		t.Fatalf("apply_patch changed .gitattributes without confirmation: %s", out)
	}

	// مقصد move به مسیر محافظت‌شده هم باید تأیید بخواهد.
	if err := os.WriteFile(filepath.Join(ws, "plain.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, ok := a.execTool("move_path", `{"from":"plain.txt","to":".git/config"}`); ok {
		t.Fatalf("move_path into .git/config succeeded without confirmation: %s", out)
	}
}
