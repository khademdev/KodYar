// تست‌های صدای پایان کار ایجنت: فایل بارگذاری می‌شود و فقط در جای درست
// صدا می‌دهد (نه در چت معمولی، نه بعد از توقف دستی).
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletionSoundWired(t *testing.T) {
	html, err := os.ReadFile(filepath.Join("frontend", "dist", "index.html"))
	if err != nil {
		t.Fatalf("index.html: %v", err)
	}
	h := string(html)
	if !strings.Contains(h, `src="assets/js/sound.js"`) {
		t.Fatal("index.html must load assets/js/sound.js")
	}
	if strings.Index(h, "assets/js/app.js") > strings.Index(h, "assets/js/sound.js") {
		t.Fatal("sound.js must load after app.js (it reads window.MyAIState)")
	}

	raw, err := os.ReadFile(filepath.Join("frontend", "dist", "assets", "js", "sound.js"))
	if err != nil {
		t.Fatalf("sound.js: %v", err)
	}
	js := string(raw)
	for _, want := range []string{
		`EventsOn("chat:done", onDone)`,
		`if (!ev || ev.aborted) return;`,
		`if (!view || !view.agentMode) return;`,
		`AudioContext`,
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("sound.js must contain %q", want)
		}
	}
	if strings.Contains(js, "new Audio(") {
		t.Fatal("completion sound should be synthesized (no bundled audio asset)")
	}
}
