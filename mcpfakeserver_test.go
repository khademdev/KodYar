// سرور MCP ساختگی برای تست‌ها — الگوی استاندارد TestHelperProcess/re-exec:
// همان باینری تست با متغیر محیطی MYAI_MCP_FAKE=1 به‌جای تست‌ها، سرور MCP
// stdio می‌شود. رفتارها با آرگومان اول انتخاب می‌شوند:
//
// fake-ok — handshake سالم؛ ابزارها در دو صفحه (pagination)؛
// echo (متن را برمی‌گرداند)، img (تصویر)، boom (isError)،
// slow (هنگ می‌کند)، big (خروجی بزرگ)، fa (متن فارسی)
// fake-badver — handshake با نسخه‌ی ناشناخته
// fake-crash — بلافاصله خارج می‌شود
// fake-spawn — یک فرزندِ خفته می‌سازد (تست tree-kill) و بعد fake-ok
// fake-ping — بعد از handshake یک درخواست ping و یک درخواست ناشناخته می‌فرستد
// fake-die — بعد از handshake موفق خودش خارج می‌شود (کرش پس از نصب)
// fake-silent — بعد از handshake موفق stdin را نمی‌خواند (سرور هنگ‌شده)
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// fakeMCPCommand فرمان اجرای سرور ساختگی از داخل تست‌ها.
func fakeMCPCommand(mode string) (string, []string) {
	return os.Args[0], []string{"-test.run=TestMCPHelperProcess", "--", mode}
}

// mcpFakeServer بدنه‌ی سرور ساختگی — روی stdin/stdout JSON-RPC خط‌به‌خط.
func mcpFakeServer(mode string) {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	write := func(v interface{}) {
		raw, _ := json.Marshal(v)
		out.Write(raw)
		out.WriteByte('\n')
		out.Flush()
	}
	reply := func(id json.RawMessage, result interface{}) {
		write(map[string]interface{}{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result})
	}
	replyErr := func(id json.RawMessage, code int, msg string) {
		write(map[string]interface{}{"jsonrpc": "2.0", "id": json.RawMessage(id),
			"error": map[string]interface{}{"code": code, "message": msg}})
	}

	if mode == "fake-crash" {
		fmt.Fprintln(os.Stderr, "fake server crashing on purpose")
		os.Exit(3)
	}
	if mode == "fake-die" {
		// کرش «بعد از» handshake موفق — کلاینت نصب شده و بعد ناگهان می‌میرد
		// (تست سیم‌کشی onExit: حذف از نقشه + شمارش backoff).
		go func() {
			time.Sleep(400 * time.Millisecond)
			os.Exit(1)
		}()
	}
	if mode == "fake-silent" {
		// پس از handshake موفق، stdin خوانده نمی‌شود و فرایند زنده می‌ماند —
		// نوشتن پیام بزرگ باید بلاک شود تا نگهبان مهلت نوشتن کشتن درخت را
		// اثبات کند (تست writeJSON watchdog). توقف خواندن در case
		// notifications/initialized انجام می‌شود (بعد از handshake کامل).
		go func() {
			time.Sleep(15 * time.Second)
			os.Exit(0)
		}()
	}
	if mode == "fake-spawn" {
		// فرزندِ خفته — برای اثبات کشتن «درخت» فرایند
		sleep := exec.Command("sleep", "120")
		sleep.Stdout = nil
		sleep.Stderr = nil
		_ = sleep.Start()
		go func() { _ = sleep.Wait() }()
	}

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg jsonRPCMessage
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			if mode == "fake-badver" {
				reply(msg.ID, map[string]interface{}{"protocolVersion": "1999-01-01"})
				continue
			}
			reply(msg.ID, map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{"tools": map[string]string{}},
				"serverInfo":      map[string]string{"name": "fake-mcp", "version": "1.0"},
			})
		case "notifications/initialized":
			// بدون پاسخ
			if mode == "fake-silent" {
				// handshake تمام شد — از این‌جا دیگر stdin خوانده نمی‌شود
				// (سرورِ هنگ‌شده؛ پردازش ادامه نمی‌یابد).
				time.Sleep(15 * time.Second)
				os.Exit(0)
			}
		case "notifications/cancelled":
			// بدون پاسخ
		case "tools/list":
			page := 1
			if mode == "fake-ok" || mode == "fake-spawn" || mode == "fake-ping" {
				var p struct {
					Params struct {
						Cursor string `json:"cursor"`
					} `json:"params"`
				}
				_ = json.Unmarshal([]byte(line), &p)
				if p.Params.Cursor == "page2" {
					page = 2
				}
			}
			if page == 1 {
				reply(msg.ID, map[string]interface{}{
					"tools": []map[string]interface{}{
						{
							"name":        "echo",
							"description": "Echo the given text back.",
							"inputSchema": map[string]interface{}{"type": "object", "$schema": "https://json-schema.org/draft-07/schema#", "properties": map[string]interface{}{"text": map[string]string{"type": "string"}}},
						},
						{
							"name":        "img",
							"description": "Returns an image.",
							"inputSchema": map[string]interface{}{"type": "object"},
						},
					},
					"nextCursor": "page2",
				})
			} else {
				reply(msg.ID, map[string]interface{}{
					"tools": []map[string]interface{}{
						{"name": "boom", "description": "Always fails.", "inputSchema": map[string]interface{}{"type": "object"}},
						{"name": "slow", "description": "Hangs forever.", "inputSchema": map[string]interface{}{"type": "object"}},
						{"name": "big", "description": "Huge output.", "inputSchema": map[string]interface{}{"type": "object"}},
						{"name": "fa", "description": "Persian output.", "inputSchema": map[string]interface{}{"type": "object"}},
					},
				})
			}
		case "tools/call":
			var p struct {
				Params struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"params"`
			}
			_ = json.Unmarshal([]byte(line), &p)
			switch p.Params.Name {
			case "echo":
				var args struct {
					Text string `json:"text"`
				}
				_ = json.Unmarshal(p.Params.Arguments, &args)
				reply(msg.ID, map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": "echo: " + args.Text}}})
			case "img":
				data := base64.StdEncoding.EncodeToString([]byte("PNGDATA"))
				reply(msg.ID, map[string]interface{}{"content": []map[string]interface{}{
					{"type": "image", "mimeType": "image/png", "data": data},
				}})
			case "boom":
				reply(msg.ID, map[string]interface{}{"isError": true,
					"content": []map[string]interface{}{{"type": "text", "text": "tool exploded"}}})
			case "slow":
				select {} // هنگ — تستِ timeout/cancel این را می‌شکند
			case "big":
				reply(msg.ID, map[string]interface{}{"content": []map[string]interface{}{
					{"type": "text", "text": strings.Repeat("A", 40*1024)},
				}})
			case "fa":
				reply(msg.ID, map[string]interface{}{"content": []map[string]interface{}{
					{"type": "text", "text": "سلام — این متنِ فارسی با نیم‌فاصله است"},
				}})
			default:
				replyErr(msg.ID, -32602, "unknown tool "+p.Params.Name)
			}
		case "ping":
			reply(msg.ID, map[string]interface{}{})
		default:
			if len(msg.ID) > 0 {
				replyErr(msg.ID, -32601, "method not supported: "+msg.Method)
			}
		}

		// حالت ping: بعد از اولین پاسخ، درخواست سرور→کلاینت بفرست
		if mode == "fake-ping" && msg.Method == "initialize" {
			write(map[string]interface{}{"jsonrpc": "2.0", "id": 777, "method": "ping"})
			write(map[string]interface{}{"jsonrpc": "2.0", "id": 778, "method": "sampling/createMessage",
				"params": map[string]string{"hint": "should be rejected with -32601"}})
		}
	}
}

// TestMCPHelperProcess نقطه‌ی ورود re-exec. فرزند با آرگومان‌های
// «-test.run=TestMCPHelperProcess -- <mode>» اجرا می‌شود؛ تشخیص فرزند با
// نشانگر «--» است (محیط فرزند عمداً حداقلی است — متغیر محیطی منتقل نمی‌شود).
// در اجرای عادیِ تست‌ها این تابع بی‌درنگ برمی‌گردد.
func TestMCPHelperProcess(t *testing.T) {
	for i, a := range os.Args {
		if a == "--" && i+1 < len(os.Args) {
			mcpFakeServer(os.Args[i+1])
			os.Exit(0)
		}
	}
}

// ---------------------------------------------------------------- کمکی تست

// newFakeMCPApp اپ آزمایشی با یک سرور MCP ساختگی در تنظیمات.
func newFakeMCPApp(t *testing.T, mode string, enabled bool) *App {
	t.Helper()
	a := newTestApp(t)
	cmd, args := fakeMCPCommand(mode)
	a.settings.MCPServers = []MCPServerConfig{{
		Name: "fake", Command: cmd, Args: args, Enabled: enabled,
		Env: map[string]string{"FAKE_TOKEN": "secret-token-123"},
	}}
	// رمزنگاری env مثل مسیر واقعی SaveMCPServers
	enc, err := encryptString("secret-token-123")
	if err != nil {
		t.Fatal(err)
	}
	a.settings.MCPServers[0].Env["FAKE_TOKEN"] = enc
	return a
}

// ensureMCPStarted مطمئن می‌شود سرور «fake» بالا آمده و ابزارهایش را داده.
func ensureMCPStarted(t *testing.T, a *App) *mcpClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := a.mcp.ensureStarted(ctx, a, a.settings.MCPServers[0])
	if err != nil {
		t.Fatalf("fake server did not start: %v", err)
	}
	if _, err := c.listTools(ctx); err != nil {
		t.Fatalf("tools/list failed: %v", err)
	}
	return c
}
