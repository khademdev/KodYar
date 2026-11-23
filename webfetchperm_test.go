// تست‌های دسترسی per-host برای web_fetch (مأموریت ۳ — C.1).
package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWebHostNormalization(t *testing.T) {
	cases := map[string]string{
		"https://Example.COM/docs":            "example.com",
		"https://example.com:8443/x?q=1":      "example.com",
		"http://example.com.":                 "example.com",
		"https://Sub.Example.com/A":           "sub.example.com",
		"http://[::1]:8080/":                  "::1",
		"not a url":                           "",
		"https://xn--mgbh0fb.example/p?q=%20": "xn--mgbh0fb.example",
	}
	for in, want := range cases {
		if got := webHostFromURL(in); got != want {
			t.Errorf("webHostFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWebHostRuleMatching(t *testing.T) {
	type tc struct {
		pattern, host string
		want          bool
	}
	cases := []tc{
		{"example.com", "example.com", true},
		{"example.com", "sub.example.com", false}, // دقیق
		{"Example.COM", "example.com", true},
		{"*.example.com", "a.example.com", true},
		{"*.example.com", "example.com", true},           // خود دامنه هم مجاز
		{"*.example.com", "a.b.example.com", true},       // زیردامنه‌ی عمیق‌تر
		{"*.example.com", "notexample.com", false},       // پسوندِ فریب‌کار
		{"*.example.com", "example.com.evil.net", false}, // پسوندِ جعلی
		{"", "example.com", false},
		{"example.com", "", false},
	}
	for _, c := range cases {
		if got := webHostMatches(c.pattern, c.host); got != c.want {
			t.Errorf("webHostMatches(%q,%q) = %v, want %v", c.pattern, c.host, got, c.want)
		}
	}

	rules := []PermissionRule{
		{Pattern: "*.docs.example.com", Decision: "allow"},
		{Pattern: "denied.example.com", Decision: "deny"},
	}
	if !webHostRuleListAllowed(rules, "api.docs.example.com") {
		t.Error("wildcard allow rule must match a subdomain")
	}
	if webHostRuleListAllowed(rules, "denied.example.com") {
		t.Error("deny rule must not grant access")
	}
	if webHostRuleListAllowed(rules, "other.example.com") {
		t.Error("unrelated host must not match")
	}
}

func TestValidWebHostPattern(t *testing.T) {
	good := []string{"example.com", "*.example.com", "sub-domain.example.co", "::1"}
	bad := []string{"", " ", "*", "*.", ".", ".example.com", "exa mple.com", "ex*mple.com", "a..b", "http://x", "example.com/path", "*.a.*.b"}
	for _, p := range good {
		if !validWebHostPattern(p) {
			t.Errorf("pattern %q must be accepted", p)
		}
	}
	for _, p := range bad {
		if validWebHostPattern(p) {
			t.Errorf("pattern %q must be rejected", p)
		}
	}
}

// استارت اپ با فایل تنظیمات قدیمی که web_fetch را سراسری مجاز کرده بود:
// هرگز بی‌صدا حفظ نمی‌شود و به «بپرس» برمی‌گردد.
func TestWebFetchGlobalAllowMigration(t *testing.T) {
	a := newTestApp(t)
	path, err := a.settingsPath()
	if err != nil {
		t.Fatal(err)
	}
	legacy := map[string]interface{}{
		"provider": "ollama", "model": "llama3.2",
		"workspacePath": a.settings.WorkspacePath,
		"permissions": map[string]interface{}{
			"tools": map[string]string{"web_fetch": "allow", "read_file": "allow"},
		},
	}
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if v, ok := a.settings.Permissions.Tools["web_fetch"]; ok {
		t.Fatalf("tool-wide web_fetch permission %q must be migrated away", v)
	}
	a.mu.Lock()
	d := a.toolPermDecisionLocked("web_fetch")
	a.mu.Unlock()
	if d != permAsk {
		t.Fatalf("migrated web_fetch decision = %v, want ask", d)
	}
	// overrideهای بی‌ربط دست‌نخورده می‌مانند
	if a.settings.Permissions.Tools["read_file"] != "allow" {
		t.Fatal("unrelated tool override must survive migration")
	}
}

// حتی اگر کسی از راه برنامه allow سراسری بگذارد، پذیرفته نمی‌شود.
func TestSetToolPermissionRejectsWebFetchAllow(t *testing.T) {
	a := newTestApp(t)
	if err := a.SetToolPermission("web_fetch", "allow"); err == nil {
		t.Fatal("tool-wide allow for web_fetch must be rejected")
	}
	// دستکاری مستقیم فیلد هم اثر ندارد (دفاع دوم)
	a.mu.Lock()
	a.settings.Permissions.Tools = map[string]string{"web_fetch": "allow"}
	d := a.toolPermDecisionLocked("web_fetch")
	a.mu.Unlock()
	if d != permAsk {
		t.Fatalf("stored web_fetch allow must still evaluate to ask, got %v", d)
	}
	// ask و deny مجازند
	if err := a.SetToolPermission("web_fetch", "deny"); err != nil {
		t.Fatalf("deny must be accepted: %v", err)
	}
}

// createAskResponder یک «کاربر فیلم‌نامه‌ای» می‌سازد: هر ask را ثبت می‌کند و
// با تصمیم داده‌شده پاسخ می‌دهد.
func createAskResponder(t *testing.T, a *App, decisions []string) (*[]*permissionAsk, *sync.WaitGroup) {
	t.Helper()
	var mu sync.Mutex
	var asks []*permissionAsk
	ch := make(chan *permissionAsk, 8)
	a.ctx = context.Background()
	a.permAskEmit = func(ask *permissionAsk) { ch <- ask }
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for ask := range ch {
			mu.Lock()
			asks = append(asks, ask)
			mu.Unlock()
			dec := "allow"
			if i < len(decisions) {
				dec = decisions[i]
			}
			i++
			_ = a.RespondPermissionConfirm(ask.ID, dec)
		}
	}()
	t.Cleanup(func() {
		close(ch)
		wg.Wait()
	})
	return &asks, &wg
}

func TestGateWebFetchSessionAndAlways(t *testing.T) {
	a := newTestApp(t)
	asks, _ := createAskResponder(t, a, []string{"session", "always"})

	ctx := context.Background()
	// ۱) «در این نشست» → همان دامنه بی‌پرسش می‌ماند
	if ok, msg := a.gateWebFetch(ctx, "https://Docs.Example.com/a?token=x"); !ok {
		t.Fatalf("session allow must permit: %s", msg)
	}
	if ok, msg := a.gateWebFetch(ctx, "https://docs.example.com/other"); !ok {
		t.Fatalf("session rule must cover the same host again: %s", msg)
	}
	if n := len(*asks); n != 1 {
		t.Fatalf("expected exactly one modal for the same host, got %d", n)
	}
	if (*asks)[0].Kind != "web_host" || (*asks)[0].Pattern != "docs.example.com" {
		t.Fatalf("unexpected ask: %+v", (*asks)[0])
	}
	if !strings.Contains((*asks)[0].Message, "docs.example.com") {
		t.Fatalf("message must name the host: %q", (*asks)[0].Message)
	}

	// ۲) دامنه‌ی دیگر → «همیشه برای این دامنه» → قاعده‌ی ماندگار
	if ok, msg := a.gateWebFetch(ctx, "https://other.example.org/x"); !ok {
		t.Fatalf("always allow must permit: %s", msg)
	}
	persisted := false
	for _, r := range a.settings.Permissions.WebHosts {
		if r.Pattern == "other.example.org" && r.Decision == "allow" {
			persisted = true
		}
	}
	if !persisted {
		t.Fatalf("always-allow must persist a host rule, got %+v", a.settings.Permissions.WebHosts)
	}
	// و در ارزیابی هم اثر دارد (بدون مودال تازه)
	a.permAskEmit = nil
	if !a.webHostAllowed("other.example.org") {
		t.Fatal("persisted host rule must be honoured")
	}
	if a.webHostAllowed("third.example.net") {
		t.Fatal("unrelated host must not be allowed")
	}
}

func TestGateWebFetchReject(t *testing.T) {
	a := newTestApp(t)
	_, _ = createAskResponder(t, a, []string{"reject"})
	ok, msg := a.gateWebFetch(context.Background(), "https://secret.example.com/?key=abc")
	if ok {
		t.Fatal("reject must deny the fetch")
	}
	if !strings.HasPrefix(msg, "ERROR: ") || !strings.Contains(msg, "secret.example.com") {
		t.Fatalf("model-facing error must name the host: %q", msg)
	}
	if a.webHostAllowed("secret.example.com") {
		t.Fatal("rejected host must not become allowed")
	}
}

func TestRemoveWebHostRule(t *testing.T) {
	a := newTestApp(t)
	if err := a.appendWebHostRule("Docs.Example.com", "allow"); err != nil {
		t.Fatal(err)
	}
	if !a.webHostAllowed("docs.example.com") {
		t.Fatal("rule must apply")
	}
	if err := a.RemoveWebHostRule("docs.example.com"); err != nil {
		t.Fatal(err)
	}
	if a.webHostAllowed("docs.example.com") {
		t.Fatal("removed rule must stop applying")
	}
	if err := a.RemoveWebHostRule("docs.example.com"); err == nil {
		t.Fatal("removing a missing rule must report an error")
	}
}

// دامنه‌ی نامعتبر/URL ناقص نباید مودال بسازد و نباید بلاک کند — به خود ابزار
// واگذار می‌شود تا پیام دقیق بدهد.
func TestGateWebFetchInvalidURLSkipsModal(t *testing.T) {
	a := newTestApp(t)
	asks, _ := createAskResponder(t, a, []string{"reject"})
	if ok, msg := a.gateWebFetch(context.Background(), "not a url"); !ok {
		t.Fatalf("invalid URL must be delegated to the tool: %s", msg)
	}
	time.Sleep(20 * time.Millisecond)
	if len(*asks) != 0 {
		t.Fatalf("no modal may be shown for an unparsable URL (got %d)", len(*asks))
	}
}

// TestGateWebFetchTurnAllow — «تا پایان این درخواست» یک مودال برای اولین دامنه
// است و بقیه‌ی دامنه‌های همان نوبت (نتیجه‌های یک جست‌وجو) بی‌مودال عبور می‌کنند؛
// هیچ قاعده‌ای ذخیره نمی‌شود و نوبت بعد دوباره می‌پرسد.
func TestGateWebFetchTurnAllow(t *testing.T) {
	a := newTestApp(t)
	asks, _ := createAskResponder(t, a, []string{"turn", "allow"})

	ctx := context.Background()
	if ok, msg := a.gateWebFetch(ctx, "https://html.duckduckgo.com/html/?q=go"); !ok {
		t.Fatalf("turn allow must permit the first host: %s", msg)
	}
	if n := len(*asks); n != 1 {
		t.Fatalf("expected one modal, got %d", n)
	}
	if !(*asks)[0].AllowTurn {
		t.Fatal("web_host ask must offer the turn-scoped option")
	}
	// دامنه‌ی دیگر همان نوبت: بدون مودال
	if ok, msg := a.gateWebFetch(ctx, "https://example.com/result"); !ok {
		t.Fatalf("turn grant must cover other hosts in the same turn: %s", msg)
	}
	if n := len(*asks); n != 1 {
		t.Fatalf("turn grant must not ask again, got %d modals", n)
	}
	// هیچ قاعده‌ی ماندگار/نشستی ذخیره نشده است
	if a.webHostAllowed("example.com") || len(a.sessionWebHosts) != 0 {
		t.Fatalf("turn grant must not persist rules: session=%v persistent=%v",
			a.sessionWebHosts, a.settings.Permissions.WebHosts)
	}

	// نوبت بعد: streamChat/finishChat همین helper را صدا می‌زنند، پس دامنه‌ی
	// تازه دوباره می‌پرسد (تصمیم بعدی: allow).
	if !a.webTurnGranted() {
		t.Fatal("turn grant must be visible through webTurnGranted")
	}
	a.resetWebTurnGrant()
	if ok, msg := a.gateWebFetch(ctx, "https://third.example.net/x"); !ok {
		t.Fatalf("next turn must ask again and allow on demand: %s", msg)
	}
	if n := len(*asks); n != 2 {
		t.Fatalf("a new turn must prompt again, got %d modals", n)
	}
}
