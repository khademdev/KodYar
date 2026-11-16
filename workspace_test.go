// نگهبانِ ساخت پوشه‌ی کاری پیش‌فرض.
//
// رگرسیون: loadSettings مسیر پیش‌فرض را فقط در حافظه ست می‌کرد و هیچ‌وقت
// روی دیسک ساخته نمی‌شد؛ اولین اجرا با مسیر ناموجود کار می‌کرد و
// فهرست‌کردن/اجرای دستور/git خطا می‌دادند.
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDefaultWorkspaceCreatesDefault(t *testing.T) {
	def := filepath.Join(t.TempDir(), "Projects", "MyWorkspace")

	if err := ensureDefaultWorkspaceAt(def, def); err != nil {
		t.Fatalf("ensureDefaultWorkspaceAt: %v", err)
	}
	st, err := os.Stat(def)
	if err != nil || !st.IsDir() {
		t.Fatalf("default workspace was not created: err=%v", err)
	}

	// اجرای دوباره باید بی‌خطر و بدون خطا باشد (پوشه از قبل هست).
	if err := ensureDefaultWorkspaceAt(def, def); err != nil {
		t.Fatalf("second run must be idempotent: %v", err)
	}
}

func TestEnsureDefaultWorkspaceIgnoresCustomPath(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "Projects", "MyWorkspace")
	custom := filepath.Join(base, "my-own-project")

	if err := ensureDefaultWorkspaceAt(def, custom); err != nil {
		t.Fatalf("ensureDefaultWorkspaceAt: %v", err)
	}
	if _, err := os.Stat(custom); !os.IsNotExist(err) {
		t.Fatal("a user-chosen workspace path must never be created automatically")
	}
}

func TestDefaultWorkspacePathShape(t *testing.T) {
	p := defaultWorkspacePath()
	if p == "" {
		t.Skip("user home directory is unavailable")
	}
	if filepath.Base(p) != "MyWorkspace" || filepath.Base(filepath.Dir(p)) != "Projects" {
		t.Fatalf("default workspace path must end in Projects/MyWorkspace, got %q", p)
	}
}
