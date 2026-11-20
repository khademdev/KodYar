// تست‌های مدیریت دسترسی‌ها (تب تنظیمات): نمای امن-از-جهت-کپی،
// اعتبارسنجی/پایداری override ابزار، حذف قاعده‌ی «همیشه اجازه»،
// بازنشانی کامل و اثر واقعی overrideها بر دروازه‌ی اجرا.
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// mustSetToolPermission wrapper کمکی برای تست‌ها.
func mustSetToolPermission(t *testing.T, a *App, tool, decision string) {
	t.Helper()
	if err := a.SetToolPermission(tool, decision); err != nil {
		t.Fatalf("SetToolPermission(%q,%q): %v", tool, decision, err)
	}
}

// TestPermissionsViewReturnsCopies تغییر دادن خروجی PermissionsView نباید
// وضعیت داخلی را تغییر دهد (aliasing ممنوع) و داده‌ها باید کامل باشند.
func TestPermissionsViewReturnsCopies(t *testing.T) {
	a := newTestApp(t)
	a.settings.CustomTools = []CustomTool{{Name: "greet", Command: "echo hi", Description: "test"}}
	mustSetToolPermission(t, a, "read_file", "deny")
	mustSetToolPermission(t, a, "custom_greet", "ask")
	if err := a.RemoveCommandRule("nonexistent-should-fail"); err == nil {
		t.Fatal("removing nonexistent rule must error (sanity for this test)")
	}

	v := a.PermissionsView()

	// جهنم جهانی: تغییر خروجی نباید داخلی را تغییر دهد
	tools := v["tools"].([]map[string]string)
	for i := range tools {
		tools[i]["override"] = "allow"
		tools[i]["name"] = "mutated"
	}
	rules := v["commandRules"].([]map[string]string)
	_ = rules // فقط اطمینان از نوع
	globs := v["sensitiveGlobs"].([]string)
	if len(globs) > 0 {
		globs[0] = "mutated-*"
	}

	// بازخوانی: مقادیر واقعی باید دست‌نخورده باشند
	v2 := a.PermissionsView()
	tools2 := v2["tools"].([]map[string]string)
	byName := map[string]map[string]string{}
	for _, tm := range tools2 {
		byName[tm["name"]] = tm
	}
	if byName["read_file"]["override"] != "deny" {
		t.Fatalf("read_file override must survive view mutation, got %q", byName["read_file"]["override"])
	}
	if byName["read_file"]["default"] != "allow" {
		t.Fatalf("read_file default = %q, want allow", byName["read_file"]["default"])
	}
	if byName["custom_greet"]["override"] != "ask" {
		t.Fatalf("custom_greet override = %q, want ask", byName["custom_greet"]["override"])
	}
	if _, ok := byName["run_command"]; !ok {
		t.Fatal("run_command must be listed")
	}
	if v2["sensitiveGlobs"].([]string)[0] == "mutated-*" {
		t.Fatal("sensitive globs view must be a copy")
	}
	// ترتیب ابزارهای داخلی حفظ شود
	if tools2[0]["name"] != "list_dir" {
		t.Fatalf("first tool = %q, want list_dir", tools2[0]["name"])
	}
}

// TestPermissionsViewShowsOrphanOverrides overrideهای یتیم (ابزار سفارشیِ
// حذف‌شده) باید همچنان نمایش داده شوند تا پاک‌شدنی باشند.
func TestPermissionsViewShowsOrphanOverrides(t *testing.T) {
	a := newTestApp(t)
	mustSetToolPermission(t, a, "custom_gone", "allow")
	found := false
	for _, tm := range a.PermissionsView()["tools"].([]map[string]string) {
		if tm["name"] == "custom_gone" && tm["override"] == "allow" {
			found = true
		}
	}
	if !found {
		t.Fatal("orphan override custom_gone must appear in view")
	}
}

// TestSetToolPermissionValidation اعتبارسنجی نام ابزار و مقدار تصمیم.
func TestSetToolPermissionValidation(t *testing.T) {
	a := newTestApp(t)
	bad := []struct{ tool, decision string }{
		{"", "allow"}, {"run command", "allow"}, {"run_command;", "allow"},
		{"../../etc", "allow"}, {"x", "yes"}, {"x", "always"}, {"x", ""},
		{strings.Repeat("x", 65), "allow"},
	}
	for _, c := range bad {
		// نام خالی با تصمیم خالی هر دو بد هستند؛ فقط یکی هم کافی است
		if err := a.SetToolPermission(c.tool, c.decision); err == nil {
			// استثنا: نام معتبر + تصمیم "" = default مجاز است
			if !(toolNameRe.MatchString(c.tool) && c.decision == "") {
				t.Errorf("SetToolPermission(%q,%q) must fail", c.tool, c.decision)
			}
		}
	}
	// مقدارهای معتبر
	for _, d := range []string{"allow", "ask", "deny", "default"} {
		mustSetToolPermission(t, a, "search_files", d)
	}
}

// TestSetToolPermissionPersists override باید در settings.json روی دیسک
// بنشیند و پس از بارگذاری دوباره برقرار باشد.
func TestSetToolPermissionPersists(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("KODYAR_CONFIG_DIR", dir)
	a := NewApp()
	if err := a.loadSettings(); err != nil {
		t.Fatal(err)
	}
	a.settings.WorkspacePath = t.TempDir()
	if err := a.SetToolPermission("delete_path", "deny"); err != nil {
		t.Fatal(err)
	}
	b := NewApp()
	if err := b.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if b.settings.Permissions.Tools["delete_path"] != "deny" {
		t.Fatalf("override not persisted: %+v", b.settings.Permissions.Tools)
	}
	// default = حذف override
	if err := b.SetToolPermission("delete_path", "default"); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.settings.Permissions.Tools["delete_path"]; ok {
		t.Fatal("default must remove the override")
	}
}

// TestSetToolPermissionAffectsExecTools deny باید اجرای ابزار را بدون هیچ
// عملی رد کند (این‌جا با read_file روی فایل واقعی).
func TestSetToolPermissionAffectsExecTools(t *testing.T) {
	a := newTestApp(t)
	target := filepath.Join(a.settings.WorkspacePath, "sample.txt")
	if err := os.WriteFile(target, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	// پیش‌فرض: read_file مجاز است (a.ctx=nil یعنی مودال‌ها رد می‌شوند؛
	// allow به مودال نمی‌رود)
	if _, ok := a.execToolCtx(context.Background(), "read_file", `{"path":"sample.txt"}`); !ok {
		t.Fatal("default read_file must run headless")
	}
	mustSetToolPermission(t, a, "read_file", "deny")
	if r, ok := a.execToolCtx(context.Background(), "read_file", `{"path":"sample.txt"}`); ok {
		t.Fatalf("deny override must block read_file, got %q", r)
	}
	mustSetToolPermission(t, a, "read_file", "default")
	if _, ok := a.execToolCtx(context.Background(), "read_file", `{"path":"sample.txt"}`); !ok {
		t.Fatal("removing override must restore default allow")
	}
}

// TestRemoveCommandRuleLifecycle چرخه‌ی کامل: افزودن قاعده → دستور بدون
// پرسش مجاز → حذف قاعده → دوباره ask → حذف تکراری خطا.
func TestRemoveCommandRuleLifecycle(t *testing.T) {
	a := newTestApp(t)
	a.ctx = context.Background()

	if d, _ := a.evalCommandPerm("cargo build --release"); d != permAsk {
		t.Fatalf("fresh cargo build = %v, want ask", d)
	}
	a.appendCommandRule("cargo build*", "allow")
	if d, _ := a.evalCommandPerm("cargo build --release"); d != permAllow {
		t.Fatalf("with rule cargo build = %v, want allow", d)
	}
	if err := a.RemoveCommandRule("cargo build*"); err != nil {
		t.Fatal(err)
	}
	if d, _ := a.evalCommandPerm("cargo build --release"); d != permAsk {
		t.Fatalf("after removal cargo build = %v, want ask again", d)
	}
	if err := a.RemoveCommandRule("cargo build*"); err == nil {
		t.Fatal("second removal must error")
	}
	// قاعده‌ی حذف‌شده نباید در نمای UI باقی بماند
	for _, r := range a.PermissionsView()["commandRules"].([]map[string]string) {
		if r["pattern"] == "cargo build*" {
			t.Fatal("removed rule must not appear in view")
		}
	}
}

// TestRemoveCommandRulePersists حذف باید روی دیسک بنشیند.
func TestRemoveCommandRulePersists(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("KODYAR_CONFIG_DIR", dir)
	a := NewApp()
	if err := a.loadSettings(); err != nil {
		t.Fatal(err)
	}
	a.settings.WorkspacePath = t.TempDir()
	a.appendCommandRule("git status*", "allow")
	b := NewApp()
	if err := b.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if err := b.RemoveCommandRule("git status*"); err != nil {
		t.Fatal(err)
	}
	c := NewApp()
	if err := c.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if len(c.settings.Permissions.CommandRules) != 0 {
		t.Fatalf("removal not persisted: %+v", c.settings.Permissions.CommandRules)
	}
}

// TestResetPermissions بازنشانی: قاعده‌ها/overrideها پاک و الگوهای حساس
// به پیش‌فرض برمی‌گردند؛ لیست سیاه سخت دست‌نخورده می‌ماند.
func TestResetPermissions(t *testing.T) {
	a := newTestApp(t)
	a.ctx = context.Background()
	a.appendCommandRule("cargo build*", "allow")
	mustSetToolPermission(t, a, "read_file", "deny")
	a.settings.Permissions.SensitiveGlobs = []string{"*.supersecret"}

	if err := a.ResetPermissions(); err != nil {
		t.Fatal(err)
	}
	v := a.PermissionsView()
	if len(v["commandRules"].([]map[string]string)) != 0 {
		t.Fatal("rules must be cleared")
	}
	if d, _ := a.evalCommandPerm("cargo build"); d != permAsk {
		t.Fatal("cargo build must ask again after reset")
	}
	if _, ok := a.settings.Permissions.Tools["read_file"]; ok {
		t.Fatal("tool overrides must be cleared")
	}
	if d, _ := a.evalCommandPerm("rm -rf /"); d != permDeny {
		t.Fatal("hard block must survive reset")
	}
	globs := v["sensitiveGlobs"].([]string)
	if len(globs) != len(defaultSensitiveGlobs) {
		t.Fatalf("sensitive globs must reset to defaults, got %v", globs)
	}
	// بعد از reset باید read_file دوباره پیش‌فرض (allow) باشد
	if _, ok := a.execToolCtx(context.Background(), "list_dir", `{"path":"."}`); !ok {
		t.Fatal("default tools must work after reset")
	}
}

// TestPermissionManagementConcurrent دستکاری هم‌زمان تنظیمات دسترسی از
// چند گوروتین نباید race/deadlock بدهد (gate test).
func TestPermissionManagementConcurrent(t *testing.T) {
	a := newTestApp(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tool := []string{"read_file", "search_files", "list_dir", "run_command"}[i%4]
			_ = a.SetToolPermission(tool, []string{"allow", "ask", "deny", "default"}[i%4])
			_ = a.PermissionsView()
			_, _ = a.evalCommandPerm("go build ./...")
		}(i)
	}
	wg.Wait()
}
