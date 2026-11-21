// تست: فرمان‌های git که نقطه‌ی بازگشت (checkpoint) اجرا می‌کند نباید از
// .git/config دستکاری‌شده فرمان بیرونی اجرا کنند.
//
// بردارها: filter.<driver>.clean (hash-object)، core.pager، و
// core.hooksPath + هوک reference-transaction (update-ref).
//
// تست خوداعتبارسنج است: اول با git خام نشان می‌دهد که همان تنظیمات واقعاً
// فرمان را اجرا می‌کنند (وگرنه skip)، بعد همان عملیات از مسیر برنامه اجرا
// می‌شود و باید اثری از فرمان‌ها نباشد.
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// poisonScript یک اسکریپت شِل می‌سازد که فایل نشانگر را می‌سازد و stdin را
// بی‌تغییر عبور می‌دهد (برای filter.clean لازم است).
func poisonScript(t *testing.T, dir, name string) (script, marker string) {
	t.Helper()
	marker = filepath.Join(dir, name+".MARKER")
	script = filepath.Join(dir, name+".sh")
	body := "#!/bin/sh\ntouch \"" + marker + "\"\ncat\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", script, err)
	}
	return filepath.ToSlash(script), marker
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// TestCheckpointGitIgnoresRepoDrivers: هیچ نشانگری نباید ساخته شود.
func TestCheckpointGitIgnoresRepoDrivers(t *testing.T) {
	gitAvailableForTest(t)
	ws := t.TempDir()
	gitInForTest(t, ws, "init", "-q")
	gitInForTest(t, ws, "config", "user.email", "test@example.com")
	gitInForTest(t, ws, "config", "user.name", "Test")

	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitInForTest(t, ws, "add", "-A")
	gitInForTest(t, ws, "commit", "-qm", "init")

	filterScript, filterMarker := poisonScript(t, ws, "filter")
	pagerScript, pagerMarker := poisonScript(t, ws, "pager")
	hookMarker := filepath.Join(ws, "hook.MARKER")
	hooksDir := filepath.Join(ws, "evil-hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hookBody := "#!/bin/sh\ntouch \"" + filepath.ToSlash(hookMarker) + "\"\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "reference-transaction"), []byte(hookBody), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(ws, ".gitattributes"), []byte("*.txt filter=evil\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitInForTest(t, ws, "config", "filter.evil.clean", filterScript)
	gitInForTest(t, ws, "config", "filter.evil.required", "true")
	gitInForTest(t, ws, "config", "core.pager", pagerScript)
	gitInForTest(t, ws, "config", "core.hooksPath", hooksDir)

	clearMarkers := func() {
		_ = os.Remove(filterMarker)
		_ = os.Remove(pagerMarker)
		_ = os.Remove(hookMarker)
	}

	// کنترل ۱: hash-object بدون --no-filters باید فیلتر را اجرا کند.
	clearMarkers()
	rawHash := exec.Command("git", "-C", ws, "hash-object", "-w", "a.txt")
	rawHash.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	_ = rawHash.Run()
	filterFires := exists(filterMarker)

	// کنترل ۲: update-ref واقعاً هوک reference-transaction را اجرا می‌کند؟
	clearMarkers()
	rawRef := exec.Command("git", "-C", ws, "update-ref", "refs/heads/probe", "HEAD")
	rawRef.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	_ = rawRef.Run()
	hookFires := exists(hookMarker)
	_ = exec.Command("git", "-C", ws, "-c", "core.hooksPath=", "update-ref", "-d", "refs/heads/probe").Run()

	t.Logf("controls — raw git runs the poisoned filter: %v, the reference-transaction hook: %v", filterFires, hookFires)
	if !filterFires && !hookFires {
		t.Skip("this git cannot run the poisoned filter/hook at all, so the test proves nothing")
	}

	// مسیر واقعی برنامه.
	clearMarkers()
	a := newTestApp(t)
	a.settings.WorkspacePath = ws
	if _, err := a.createCheckpoint(context.Background(), ws, "conv-driver", 1); err != nil {
		t.Fatalf("createCheckpoint: %v", err)
	}
	if filterFires && exists(filterMarker) {
		t.Fatalf("checkpoint ran filter.evil.clean from .gitattributes/.git/config (marker %s)", filterMarker)
	}
	if hookFires && exists(hookMarker) {
		t.Fatalf("checkpoint ran the reference-transaction hook via core.hooksPath (marker %s)", hookMarker)
	}
	if exists(pagerMarker) {
		t.Fatalf("checkpoint ran core.pager from .git/config (marker %s)", pagerMarker)
	}

	// همان بررسی برای مسیر بازگردانی: بعد از یک تغییر، RestoreCheckpoint باید
	// بایت‌ها را از cat-file خام بنویسد و هیچ درایوری اجرا نکند.
	clearMarkers()
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rrep := a.RestoreCheckpoint("conv-driver", 1)
	if len(rrep.Restored) == 0 {
		t.Fatalf("restore did not restore anything: %+v", rrep)
	}
	if got, _ := os.ReadFile(filepath.Join(ws, "a.txt")); string(got) != "one\n" {
		t.Fatalf("restored content = %q, want the snapshot bytes", got)
	}
	if filterFires && exists(filterMarker) {
		t.Fatalf("restore ran filter.evil.clean (marker %s)", filterMarker)
	}
	if hookFires && exists(hookMarker) {
		t.Fatalf("restore ran the reference-transaction hook via core.hooksPath (marker %s)", hookMarker)
	}
	if exists(pagerMarker) {
		t.Fatalf("restore ran core.pager from .git/config (marker %s)", pagerMarker)
	}
}
