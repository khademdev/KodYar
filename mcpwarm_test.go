// تست warm start سرورهای MCP (گام ۳): پیش‌گرم‌کردن در پس‌زمینه، تا
// initialize/tools/list از مسیر بحرانی نوبت اول بیرون بماند.
package main

import (
	"context"
	"testing"
	"time"
)

func TestMCPWarmStartPreparesServers(t *testing.T) {
	a := newFakeMCPApp(t, "fake-ok", true)
	defer a.mcp.stopAll()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	ready := a.mcpWarmStart(ctx)
	warmElapsed := time.Since(start)
	if ready != 1 {
		t.Fatalf("warm start must prepare the single enabled server, got %d", ready)
	}

	// پس از warm start، ساخت تعریف ابزارهای همان نوبت باید فقط خواندن کش
	// باشد — نه اسپاون فرایند و نه initialize.
	defsStart := time.Now()
	defs := a.mcpToolDefs(ctx)
	defsElapsed := time.Since(defsStart)
	t.Logf("warm start: spawn+initialize+list=%dms, first-turn mcpToolDefs after warm=%dms (servers=%d, tools=%d)",
		warmElapsed.Milliseconds(), defsElapsed.Milliseconds(), ready, len(defs))

	if len(defs) == 0 {
		t.Fatal("warm-started server must expose its tools")
	}
	if defsElapsed > 2*time.Second {
		t.Fatalf("after warm start mcpToolDefs must not restart servers, took %v", defsElapsed)
	}
}

func TestMCPWarmStartNoServersIsNoop(t *testing.T) {
	a := newTestApp(t)
	a.settings.MCPServers = nil
	start := time.Now()
	if ready := a.mcpWarmStart(context.Background()); ready != 0 {
		t.Fatalf("no servers → nothing to warm, got %d", ready)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("warm start without servers must return immediately, took %v", d)
	}
}
