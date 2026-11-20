//go:build windows

// تست‌های سرور MCP داخل AppContainer: مسیر موفق (stdio دوطرفه + ابزارها) و
// مسیر شکست (آماده‌سازی سندباکس شکست می‌خورد و سرور اجرا نمی‌شود).
package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func copyTestBinary(t *testing.T, dst string) {
	t.Helper()
	src, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func sandboxMCPTestApp(t *testing.T, ws string) *App {
	t.Helper()
	app := newTestApp(t)
	app.mu.Lock()
	app.settings.SandboxMode = "on"
	app.settings.SandboxNoNetwork = true
	app.settings.WorkspacePath = ws
	app.mu.Unlock()
	return app
}

func TestMCPRunsInsideAppContainer(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}
	ws := t.TempDir()
	server := filepath.Join(ws, "fake-mcp-server.exe")
	copyTestBinary(t, server)
	app := sandboxMCPTestApp(t, ws)

	if pol, gotWs, ok := app.mcpSandboxPolicy(); !ok || pol == nil || gotWs != ws {
		t.Fatalf("MCP must use the sandbox when it is on: pol=%v ws=%q ok=%v", pol, gotWs, ok)
	}

	cfg := MCPServerConfig{
		Name:    "fake",
		Command: server,
		Args:    []string{"-test.run=TestMCPHelperProcess", "--", "fake-ok"},
		Env:     map[string]string{"KODYAR_MCP_FAKE": "1"},
		Enabled: true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, err := startMCPClient(ctx, app, cfg, nil, nil)
	if err != nil {
		t.Fatalf("sandboxed MCP server failed to start: %v", err)
	}
	defer c.stop()
	tools, terr := c.listTools(ctx)
	if terr != nil {
		t.Fatalf("tools/list over the sandbox pipes failed: %v", terr)
	}
	if len(tools) == 0 {
		t.Fatal("sandboxed MCP server must expose its tools")
	}
	found := false
	for _, tl := range tools {
		if strings.Contains(strings.ToLower(tl.Name), "echo") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the fake server's echo tool, got %+v", tools)
	}
}

func TestMCPFailsClosedWhenSandboxSetupFails(t *testing.T) {
	if !sandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}
	ws := t.TempDir()
	server := filepath.Join(ws, "fake-mcp-server.exe")
	copyTestBinary(t, server)
	// پوشه‌ی کاری ناموجود: grant سندباکس شکست می‌خورد و سرور نباید اجرا شود.
	app := sandboxMCPTestApp(t, filepath.Join(ws, "missing-dir"))

	cfg := MCPServerConfig{
		Name:    "fake",
		Command: server,
		Args:    []string{"-test.run=TestMCPHelperProcess", "--", "fake-ok"},
		Env:     map[string]string{"KODYAR_MCP_FAKE": "1"},
		Enabled: true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := startMCPClient(ctx, app, cfg, nil, nil)
	if err == nil {
		c.stop()
		t.Fatal("sandbox setup failure must not start the MCP server (fail-closed)")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "sandbox") {
		t.Fatalf("failure should identify the sandbox path, got: %v", err)
	}
}
