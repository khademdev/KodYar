// تست‌های فایل‌های دستورالعمل — زنجیره‌ی.myai/rules.md → AGENTS.md
// → CLAUDE.md →.cursorrules →.myairules و کش mtime.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRulesCandidatesChain(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	a.settings.RulesEnabled = true

	// هیچ فایلی نیست → بدون بلوک
	if block := rulesBlockOf(a); block != "" {
		t.Fatalf("empty workspace must give no rules, got: %s", block)
	}

	// هر سه فایل سازگار → AGENTS.md برنده است
	os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("agents rules"), 0o644)
	os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte("claude rules"), 0o644)
	os.WriteFile(filepath.Join(ws, ".cursorrules"), []byte("cursor rules"), 0o644)

	block := rulesBlockOf(a)
	if !strings.Contains(block, "agents rules") {
		t.Fatalf("AGENTS.md must win first: %s", block)
	}
	for _, banned := range []string{"claude rules", "cursor rules"} {
		if strings.Contains(block, banned) {
			t.Fatalf("lower-priority rules leaked: %s", block)
		}
	}
	if info := a.GetRulesInfo(); info.FilePath != "AGENTS.md" || !info.FileExists {
		t.Fatalf("GetRulesInfo must reflect AGENTS.md: %+v", info)
	}

	// حذف AGENTS.md → CLAUDE.md
	os.Remove(filepath.Join(ws, "AGENTS.md"))
	block = rulesBlockOf(a)
	if !strings.Contains(block, "claude rules") {
		t.Fatalf("CLAUDE.md must be next in the chain: %s", block)
	}
	if info := a.GetRulesInfo(); info.FilePath != "CLAUDE.md" {
		t.Fatalf("GetRulesInfo must reflect CLAUDE.md: %+v", info)
	}

	// حذف CLAUDE.md → سازگاری قدیمی (.cursorrules)
	os.Remove(filepath.Join(ws, "CLAUDE.md"))
	block = rulesBlockOf(a)
	if !strings.Contains(block, "cursor rules") {
		t.Fatalf(".cursorrules must remain as legacy fallback: %s", block)
	}

	// فایل اختصاصی MyAI از همه بالاتر است
	os.MkdirAll(filepath.Join(ws, ".myai"), 0o755)
	os.WriteFile(filepath.Join(ws, ".myai", "rules.md"), []byte("myai rules"), 0o644)
	block = rulesBlockOf(a)
	if !strings.Contains(block, "myai rules") || strings.Contains(block, "cursor rules") {
		t.Fatalf(".myai/rules.md must have the highest priority: %s", block)
	}
	if info := a.GetRulesInfo(); info.FilePath != ".myai/rules.md" {
		t.Fatalf("GetRulesInfo must reflect .myai/rules.md: %+v", info)
	}
}

func TestRulesMtimeCache(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	a.settings.RulesEnabled = true
	rp := filepath.Join(ws, ".myai", "rules.md")
	os.MkdirAll(filepath.Dir(rp), 0o755)
	os.WriteFile(rp, []byte("v1 rules"), 0o644)

	// پر کردن کش
	if block := rulesBlockOf(a); !strings.Contains(block, "v1 rules") {
		t.Fatalf("first load failed: %s", block)
	}
	fi, err := os.Stat(rp)
	if err != nil {
		t.Fatal(err)
	}

	// بازنویسی با همان حجم و همان mtime → کش باید مقدار قدیمی را بدهد
	// (اثباتِ کارکردِ کش؛ تغییرِ واقعی بدون تغییر mtime دیده نمی‌شود)
	os.WriteFile(rp, []byte("v2 rules"), 0o644)
	if err := os.Chtimes(rp, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if block := rulesBlockOf(a); !strings.Contains(block, "v1 rules") {
		t.Fatalf("unchanged (size,mtime) must be served from the cache: %s", block)
	}

	// تغییر mtime → کش نامعتبر و مقدار تازه خوانده می‌شود
	// (حداقل ۲ ثانیه جلو می‌بریم: NTFS زمان‌ها را با تفکیک ۱۰۰ نانوثانیه و
	// FAT با تفکیک ۲ ثانیه ذخیره می‌کند؛ افزودن ۱ نانوثانیه در عمل هیچ
	// تغییری در mtime ذخیره‌شده ایجاد نمی‌کرد و تست روی ویندوز شکست می‌خورد)
	fresh := fi.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(rp, fresh, fresh); err != nil {
		t.Fatal(err)
	}
	if block := rulesBlockOf(a); !strings.Contains(block, "v2 rules") {
		t.Fatalf("mtime change must invalidate the cache: %s", block)
	}

	// تغییر workspace → کش (که به ws قدیمی گره خورده) نباید مقدار کهنه بدهد
	ws2 := t.TempDir()
	os.MkdirAll(filepath.Join(ws2, ".myai"), 0o755)
	os.WriteFile(filepath.Join(ws2, ".myai", "rules.md"), []byte("other ws"), 0o644)
	a.mu.Lock()
	a.settings.WorkspacePath = ws2
	a.mu.Unlock()
	if block := rulesBlockOf(a); !strings.Contains(block, "other ws") {
		t.Fatalf("workspace change must bypass the stale cache: %s", block)
	}
}

func TestRulesCapAppliesToAgentsMd(t *testing.T) {
	a := newTestApp(t)
	ws := a.settings.WorkspacePath
	a.settings.RulesEnabled = true
	// متن فارسی چندبایتی — برش باید در مرز کاراکتر باشد (cutUTF8)
	os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte(strings.Repeat("قانونٍ ", 4000)), 0o644)

	block := rulesBlockOf(a)
	if !strings.Contains(block, "بریده شد") {
		t.Fatalf("oversized rules must be cut with a notice: %.200s", block)
	}
	// پوشش <project_rules> + هدر + محتوای بریده‌شده (سقف) + نشانه
	if len(block) > maxRulesBytes+512 {
		t.Fatalf("injected block must respect the cap: %d bytes", len(block))
	}
	if !utf8.ValidString(block) {
		t.Fatal("cut must stay valid UTF-8 (Persian is multi-byte)")
	}
}

func TestGetRulesInfoDefaults(t *testing.T) {
	a := newTestApp(t)
	a.settings.RulesEnabled = true
	info := a.GetRulesInfo()
	if info.FileExists || info.FilePath != ".myai/rules.md" {
		t.Fatalf("default candidate path must be .myai/rules.md: %+v", info)
	}
	if info.FileContent != "" {
		t.Fatalf("no file must give empty content: %+v", info)
	}
}
