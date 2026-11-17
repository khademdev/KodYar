// تست‌های بستهٔ عیب‌یابی (P2 #۱۵): محتوا کامل باشد، هیچ رازی نشت نکند.
package main

import (
	"archive/zip"
	"io"
	"os"
	"strings"
	"testing"
)

// readZipEntries همهٔ ورودی‌های zip را به map نام→محتوا تبدیل می‌کند.
func readZipEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open bundle: %v", err)
	}
	defer r.Close()
	out := map[string]string{}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = string(data)
	}
	return out
}

func TestDiagnosticBundleContents(t *testing.T) {
	a := newTestApp(t)
	a.mu.Lock()
	a.settings.APIKeys = map[string]string{"prov": "sk-SUPERSECRET-123456"}
	a.settings.MCPServers = []MCPServerConfig{{
		Name: "demo", Command: "npx", Args: []string{"--token", "gsk_ARGTOKEN123456"},
		Env: map[string]string{"API_KEY": "sk-ENVSECRET-999999"}, Enabled: true,
	}}
	a.mu.Unlock()

	path, err := a.ExportDiagnosticBundle()
	if err != nil {
		t.Fatalf("ExportDiagnosticBundle: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("bundle file missing: %v", statErr)
	}
	entries := readZipEntries(t, path)
	for _, want := range []string{"README.txt", "info.txt", "settings.redacted.json"} {
		if _, ok := entries[want]; !ok {
			t.Fatalf("bundle is missing %s (has: %v)", want, keysOf(entries))
		}
	}

	info := entries["info.txt"]
	if !strings.Contains(info, "version: "+version) {
		t.Fatalf("info.txt must carry the version, got:\n%s", info)
	}
	if !strings.Contains(info, "os: ") || !strings.Contains(info, "go_version: ") {
		t.Fatalf("info.txt must describe the runtime, got:\n%s", info)
	}

	// هیچ رازی نباید در بسته باشد — نه کلید provider، نه env، نه آرگومان.
	all := ""
	for _, v := range entries {
		all += v
	}
	for _, secret := range []string{"sk-SUPERSECRET-123456", "sk-ENVSECRET-999999", "gsk_ARGTOKEN123456"} {
		if strings.Contains(all, secret) {
			t.Fatalf("secret %q leaked into the bundle", secret)
		}
	}
	if !strings.Contains(entries["settings.redacted.json"], `"API_KEY": "***"`) {
		t.Fatalf("MCP env values must be masked, got:\n%s", entries["settings.redacted.json"])
	}
}

func TestDiagnosticBundleIncludesScrubbedErrorLog(t *testing.T) {
	a := newTestApp(t)
	logAppErrorFor(a, "chat", "boom with sk-LEAKEDKEY-123456 in the text")
	path, err := a.ExportDiagnosticBundle()
	if err != nil {
		t.Fatalf("ExportDiagnosticBundle: %v", err)
	}
	entries := readZipEntries(t, path)
	log, ok := entries["errors.log"]
	if !ok {
		t.Fatalf("errors.log missing (has: %v)", keysOf(entries))
	}
	if !strings.Contains(log, "boom with") {
		t.Fatalf("error log should carry the message, got: %q", log)
	}
	if strings.Contains(log, "sk-LEAKEDKEY-123456") {
		t.Fatal("error log must be scrubbed before packaging")
	}
}

func TestScrubSecrets(t *testing.T) {
	in := "a sk-abcdef123456 b AIzaSyA1b2c3d4e5f6g7h8i gsk_abcdef123456 Bearer abcdefghijklmnop"
	out := scrubSecrets(in)
	if strings.Contains(out, "sk-abcdef123456") || strings.Contains(out, "gsk_abcdef123456") ||
		strings.Contains(out, "AIzaSyA1b2c3d4e5f6g7h8i") || strings.Contains(out, "abcdefghijklmnop") {
		t.Fatalf("secrets survived scrubbing: %q", out)
	}
	if strings.Count(out, "[redacted]") < 3 {
		t.Fatalf("expected several redactions, got %q", out)
	}
	if !strings.Contains(scrubSecrets("plain text"), "plain text") {
		t.Fatal("scrubbing must not touch ordinary text")
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
