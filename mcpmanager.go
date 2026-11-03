// مدیر سرورهای MCP — چرخه‌ی حیات: شروع تنبلِ موازی، backoff، کشتن درخت
// فرایند، وضعیت برای UI و اتصال ابزارها به حلقه‌ی ایجنت.
//
// ترتیب قفل‌ها (سند کامل: ARCHITECTURE.md):
//
// a.genMu → a.changes.mu → a.mu → mcp.mu (mcp.mu فقط وقتی a.mu آزاد است)
// mcp.mu قفل «برگ» است: هرگز در طول نگه‌داشتن آن با سرور حرف نمی‌زنیم،
// فرایندی نمی‌کشیم و a.mu نمی‌گیریم — کارِ طولانی (start/call/kill) همیشه
// بیرون از قفل با نسخه‌ی کپی‌شده انجام می‌شود.
//
// شروع تنبل: ساختن فهرست ابزارهای یک نوبت (mcpToolDefs) سرورهای فعال را
// موازی راه می‌اندازد؛ بودجه‌ی کل ۱۵ ثانیه برای initialize + tools/list.
// سرورِ ناموفق نوبت را نمی‌شکند — در لاگ خطاها ثبت و وضعیتش «خطا» می‌شود.
//
// خروج غیرمنتظره → marking dead + restart تنبل با backoff ۱s/۲s/۴s؛ پس از
// ۳ شکست پیاپی تا ذخیره‌ی دوباره‌ی تنظیمات تلاش نمی‌کند. فراخوانی در حال
// اجرا روی سرورِ مرده فوراً خطای انگلیسی می‌گیرد.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// MCPServerConfig یک سرور MCP در تنظیمات.
//
// Env «در حالت خاموش» رمزشده است (AES-GCM/keyring — همان crypto.go کلیدهای
// API)؛ به UI فقط ماسک برمی‌گردد و اگر UI همان ماسک را برگرداند، مقدار
// ذخیره‌شده دست نمی‌خورد.
type MCPServerConfig struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
}

// mcpEnvMask ماسک نمایش مقدار env در UI.
const mcpEnvMask = "••••••••"

// mcpServerState وضعیت یک سرور از دید UI/مدیر.
type mcpServerState struct {
	status    string // "connecting" | "connected" | "error" | "disabled" | "stopped"
	lastErr   string
	failures  int       // شکست‌های پیاپیِ start (ریست بعد از موفقیت/ذخیره‌ی تنظیمات)
	deadUntil time.Time // قبل از این زمان restart تنبل نمی‌کوشد (backoff)
	gaveUp    bool      // ≥۳ شکست پیاپی — تا ذخیره‌ی دوباره‌ی تنظیمات معطل

	// single-flight برای refresh ابزارها (scheduleRefresh) — همه زیر m.mu.
	refreshing    bool
	refreshQueued bool
}

// وضعیت‌های سرور برای نمایش فارسی UI.
const (
	mcpStatusConnecting = "connecting"
	mcpStatusConnected  = "connected"
	mcpStatusError      = "error"
	mcpStatusDisabled   = "disabled"
	mcpStatusStopped    = "stopped"
)

// backoffهای restart (۱s، ۲s، ۴s) — پس از شکست n-ام.
var mcpRestartBackoffs = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// mcpManager مالک همه‌ی کلاینت‌های زنده. قفل برگ — به ARCHITECTURE.md رجوع شود.
type mcpManager struct {
	mu             sync.Mutex
	clients        map[string]*mcpClient
	states         map[string]*mcpServerState
	cfgFingerprint map[string]string // name → امضای پیکربندی (تغییر = ریست backoff)
}

func newMCPManager() *mcpManager {
	return &mcpManager{
		clients:        map[string]*mcpClient{},
		states:         map[string]*mcpServerState{},
		cfgFingerprint: map[string]string{},
	}
}

// stateLocked باید با m.mu قفل‌شده صدا زده شود (می‌سازد اگر نبود).
func (m *mcpManager) stateLocked(name string) *mcpServerState {
	st, ok := m.states[name]
	if !ok {
		st = &mcpServerState{status: mcpStatusStopped}
		m.states[name] = st
	}
	return st
}

// statusOf وضعیت فعلی — کلاینتِ زنده خودش «متصل» است (حقیقتِ منبع: فرایند).
func (m *mcpManager) statusOf(name string) (string, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c := m.clients[name]; c != nil {
		c.pendMu.Lock()
		closed := c.closed
		c.pendMu.Unlock()
		if !closed {
			return mcpStatusConnected, ""
		}
	}
	if st := m.states[name]; st != nil {
		return st.status, st.lastErr
	}
	return mcpStatusStopped, ""
}

// client کلاینت زنده‌ی سرور را برمی‌گرداند (nil اگر متصل نیست).
func (m *mcpManager) client(name string) *mcpClient {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.clients[name]
	if c == nil {
		return nil
	}
	c.pendMu.Lock()
	closed := c.closed
	c.pendMu.Unlock()
	if closed {
		return nil
	}
	return c
}

// onClientExit از waitExit گوروتینِ هر کلاینت صدا می‌شود (خروج غیرمنتظره):
// علامت مرده + افزایش شکست‌ها + برنامه‌ریزی backoff برای restart تنبل.
// فقط وقتی می‌شمارد که کلاینتِ مرده همان نمونه‌ی نصب‌شده در نقشه باشد —
// مرگ در جریان راه‌اندازی (قبل از نصب) با مسیر خطای ensureStarted می‌شمارد.
func (m *mcpManager) onClientExit(name string, dead *mcpClient) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.clients[name]; !ok || cur != dead {
		return // هرگز نصب نشد یا جایگزین شده — دوباره‌شماری ممنوع
	}
	delete(m.clients, name)
	st := m.stateLocked(name)
	st.status = mcpStatusStopped
	st.failures++
	if st.failures >= len(mcpRestartBackoffs) {
		st.gaveUp = true
		st.lastErr = "server exited repeatedly; retries paused until settings are re-saved"
	} else {
		st.deadUntil = time.Now().Add(mcpRestartBackoffs[st.failures-1])
	}
}

// ensureStarted سرور را در صورت نیاز (تنبل) راه می‌اندازد — بیرون از قفل.
// خروجی: کلاینت زنده یا خطای انگلیسی (شامل حالت backoff/gaveUp).
func (m *mcpManager) ensureStarted(ctx context.Context, app *App, cfg MCPServerConfig) (*mcpClient, error) {
	if c := m.client(cfg.Name); c != nil {
		return c, nil
	}
	m.mu.Lock()
	st := m.stateLocked(cfg.Name)
	if st.gaveUp {
		err := fmt.Errorf("MCP server '%s' failed to start %d times in a row; re-save the MCP settings to retry", cfg.Name, st.failures)
		m.mu.Unlock()
		return nil, err
	}
	if time.Now().Before(st.deadUntil) {
		d := time.Until(st.deadUntil).Round(time.Millisecond)
		m.mu.Unlock()
		return nil, fmt.Errorf("MCP server '%s' is in restart backoff (retry in %s)", cfg.Name, d)
	}
	// رزرو وضعیت «در حال اتصال» تا دو فراخوانی هم‌زمان دوباره استارت نزنند
	if st.status == mcpStatusConnecting {
		m.mu.Unlock()
		return nil, fmt.Errorf("MCP server '%s' is already starting", cfg.Name)
	}
	st.status = mcpStatusConnecting
	st.lastErr = ""
	m.mu.Unlock()

	c, err := startMCPClient(ctx, app, cfg,
		func() { m.scheduleRefresh(cfg.Name) },
		func(dead *mcpClient) { m.onClientExit(cfg.Name, dead) })
	if err != nil {
		m.mu.Lock()
		st = m.stateLocked(cfg.Name)
		st.failures++
		st.status = mcpStatusError
		st.lastErr = err.Error()
		if st.failures >= len(mcpRestartBackoffs) {
			st.gaveUp = true
		} else {
			st.deadUntil = time.Now().Add(mcpRestartBackoffs[st.failures-1])
		}
		m.mu.Unlock()
		return nil, err
	}

	m.mu.Lock()
	// کلاینتِ موجودِ مرده (کرش؛ onExit هنوز نساخته/نرسیده) → جایگزین با تازه.
	if existing, ok := m.clients[cfg.Name]; ok && existing != nil {
		if existing.isClosed() {
			m.clients[cfg.Name] = c
			st = m.stateLocked(cfg.Name)
			st.failures = 0
			st.gaveUp = false
			st.status = mcpStatusConnected
			st.lastErr = ""
			m.cfgFingerprint[cfg.Name] = mcpConfigFingerprint(cfg)
			m.mu.Unlock()
			return c, nil
		}
		// کلاینت زنده‌ی موجود برنده می‌ماند (نباید رخ دهد — تابع client مانع است)
		m.mu.Unlock()
		c.stop()
		return existing, nil
	}
	// مرگ در همین چند میکروثانیه بین پایان handshake و نصب — کلاینت مرده
	// نصب نمی‌شود؛ onExit آن را نشمرده (نصب‌نشده بود) پس همین‌جا می‌شماریم.
	if c.isClosed() {
		st = m.stateLocked(cfg.Name)
		st.failures++
		st.status = mcpStatusError
		st.lastErr = "server exited immediately after startup"
		if st.failures >= len(mcpRestartBackoffs) {
			st.gaveUp = true
		} else {
			st.deadUntil = time.Now().Add(mcpRestartBackoffs[st.failures-1])
		}
		m.mu.Unlock()
		return nil, fmt.Errorf("MCP server '%s' exited immediately after startup", cfg.Name)
	}
	m.clients[cfg.Name] = c
	st = m.stateLocked(cfg.Name)
	st.failures = 0
	st.gaveUp = false
	st.status = mcpStatusConnected
	st.lastErr = ""
	m.cfgFingerprint[cfg.Name] = mcpConfigFingerprint(cfg)
	m.mu.Unlock()
	return c, nil
}

// scheduleRefresh اعلان list_changed را با تاخیر کوتاه جمع می‌کند و فهرست
// ابزارها را دوباره می‌خواند (best-effort — خطا فقط در لاگ). single-flight
// واقعی است: اگر refresh در حال اجراست، فقط یک «بازخوانی بعدی» علامت
// می‌خورد و اعلان‌های تکراری goroutine تازه نمی‌سازند.
func (m *mcpManager) scheduleRefresh(name string) {
	m.mu.Lock()
	st := m.stateLocked(name)
	if st.refreshing {
		st.refreshQueued = true
		m.mu.Unlock()
		return
	}
	st.refreshing = true
	m.mu.Unlock()
	go func() {
		defer func() {
			m.mu.Lock()
			st.refreshing = false
			again := st.refreshQueued
			st.refreshQueued = false
			m.mu.Unlock()
			if again {
				m.scheduleRefresh(name)
			}
		}()
		// چند اعلان پشت‌سرهم → یک refresh
		time.Sleep(300 * time.Millisecond)
		c := m.client(name)
		if c == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), mcpStartBudget)
		defer cancel()
		if _, err := c.listTools(ctx); err != nil {
			logAppError("mcp", "refresh tools for '"+name+"' failed: "+err.Error())
		}
	}()
}

// stopAll همه‌ی کلاینت‌ها را می‌بندد (shutdown/تغییر تنظیمات). کشتنِ درخت
// فرایند بیرون از قفل انجام می‌شود.
func (m *mcpManager) stopAll() {
	m.mu.Lock()
	current := m.clients
	m.clients = map[string]*mcpClient{}
	stopped := make([]*mcpClient, 0, len(current))
	for _, c := range current {
		stopped = append(stopped, c)
	}
	m.mu.Unlock()
	for _, c := range stopped {
		c.stop()
	}
}

// stopServer یک سرور مشخص را می‌بندد (disable/remove/تغییر پیکربندی).
func (m *mcpManager) stopServer(name string) {
	m.mu.Lock()
	c := m.clients[name]
	if c != nil {
		delete(m.clients, name)
	}
	if st := m.states[name]; st != nil {
		st.status = mcpStatusStopped
	}
	m.mu.Unlock()
	if c != nil {
		c.stop()
	}
}

// reload بعد از ذخیره‌ی تنظیمات: پیکربندی تغییرکرده/حذف‌شده/غیرفعال بسته
// می‌شود و وضعیت backoff همان‌ها ریست؛ سرورِ سالم با پیکربندی ثابت می‌ماند.
func (m *mcpManager) reload(app *App, configs []MCPServerConfig) {
	// کپی کانفیگ‌ها پیش از قفل (فراخواننده settings را قفل نگه نمی‌دارد)
	fp := map[string]MCPServerConfig{}
	for _, c := range configs {
		fp[c.Name] = c
	}
	m.mu.Lock()
	var toStop []string
	for name := range m.clients {
		cfg, ok := fp[name]
		if !ok || !cfg.Enabled {
			toStop = append(toStop, name)
			continue
		}
		// امضای ناشناخته (سروری که خارج از reload استارت شده) = بدون تغییر
		if prev, seen := m.cfgFingerprint[name]; !seen || prev != mcpConfigFingerprint(cfg) {
			toStop = append(toStop, name)
		}
	}
	// ریست backoff برای سرورهایی که پیکربندی‌شان عوض شده یا تازه اضافه شده
	for name, cfg := range fp {
		f := mcpConfigFingerprint(cfg)
		if m.cfgFingerprint[name] != f {
			m.cfgFingerprint[name] = f
			if st := m.states[name]; st != nil {
				st.failures = 0
				st.gaveUp = false
				st.deadUntil = time.Time{}
				if !cfg.Enabled {
					st.status = mcpStatusDisabled
				}
			} else if !cfg.Enabled {
				m.stateLocked(name).status = mcpStatusDisabled
			}
		}
	}
	// سرور غیرفعالی که کلاینت ندارد → وضعیت disabled
	for _, cfg := range fp {
		if !cfg.Enabled {
			if _, has := m.clients[cfg.Name]; !has {
				m.stateLocked(cfg.Name).status = mcpStatusDisabled
			}
		}
	}
	// حذف‌شده‌ها از دفترچه‌ی امضا/وضعیت
	for name := range m.cfgFingerprint {
		if _, ok := fp[name]; !ok {
			delete(m.cfgFingerprint, name)
			if st := m.states[name]; st != nil && st.status != mcpStatusError {
				st.status = mcpStatusStopped
			}
		}
	}
	m.mu.Unlock()
	for _, name := range toStop {
		m.stopServer(name)
	}
}

// mcpConfigFingerprint امضای پیکربندی (بدون Env — مقدار env ممکن است
// رمزشده باشد و هر ذخیره تغییر کند؛ تغییر env هم باید restart بدهد؟
// بله — پس env هم با «مقدار واقعی» در امضا می‌آید اما فقط در حافظه می‌ماند).
func mcpConfigFingerprint(cfg MCPServerConfig) string {
	env := make([]string, 0, len(cfg.Env))
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	return cfg.Command + "\x00" + strings.Join(cfg.Args, "\x00") + "\x00" + strings.Join(env, "\x00")
}

// ---------------------------------------------------------------- اتصال به App

// initMCPManager ساخت مدیر (startup تست هم صدا می‌زند).
func (a *App) initMCPManager() {
	if a.mcp == nil {
		a.mcp = newMCPManager()
	}
}

// mcpEnabledConfigs کپی پیکربندی‌های فعال — زیر a.mu.
func (a *App) mcpEnabledConfigs() []MCPServerConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]MCPServerConfig, 0, len(a.settings.MCPServers))
	for _, c := range a.settings.MCPServers {
		out = append(out, c)
	}
	return out
}

// mcpToolDefs ابزارهای همه‌ی سرورهای فعال برای نوبت جاری — شروع تنبلِ
// موازی با بودجه‌ی ۱۵ ثانیه؛ سرورِ ناموفق نوبت را نمی‌شکند (لاگ + skip).
// هرگز با a.mu گرفته‌شده صدا نشود (start با سرور حرف می‌زند).
func (a *App) mcpToolDefs(ctx context.Context) []toolDef {
	configs := a.mcpEnabledConfigs()
	if len(configs) == 0 {
		return nil
	}
	a.initMCPManager()

	type outcome struct {
		cfg   MCPServerConfig
		tools []mcpToolDef
	}
	outCh := make(chan outcome, len(configs))
	budget, cancel := context.WithTimeout(ctx, mcpStartBudget)
	defer cancel()

	started := 0
	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		started++
		go func(cfg MCPServerConfig) {
			ov := outcome{cfg: cfg}
			c, err := a.mcp.ensureStarted(budget, a, cfg)
			if err == nil {
				if cached := c.cachedTools(); cached != nil {
					ov.tools = cached
				} else {
					ov.tools, err = c.listTools(budget)
				}
			}
			if err != nil {
				logAppErrorFor(a, "mcp", "tools for '"+cfg.Name+"' unavailable: "+err.Error())
			}
			outCh <- ov
		}(cfg)
	}

	var defs []toolDef
	seen := map[string]string{} // نام کامل → «server/tool» (حل برخورد نام)
	for i := 0; i < started; i++ {
		ov := <-outCh
		if len(ov.tools) == 0 {
			continue
		}
		for _, t := range ov.tools {
			name := mcpToolFullName(ov.cfg.Name, t.Name)
			// برخورد نام دو جفت متفاوت → پسوند هش پایدار
			if prev, dup := seen[name]; dup && prev != ov.cfg.Name+"/"+t.Name {
				name = mcpTruncHash(ov.cfg.Name, t.Name)
			}
			seen[name] = ov.cfg.Name + "/" + t.Name
			defs = append(defs, mcpExposeTool(ov.cfg.Name, t, name))
		}
	}
	return defs
}

// mcpExposeTool تعریف API ابزار سرور برای مدل: توضیح با پیشوند [MCP:server]،
// شمای ورودی با type:object و بدون $schema و با سقف حجم.
func mcpExposeTool(server string, t mcpToolDef, fullName string) toolDef {
	desc := "[MCP:" + server + "] " + strings.TrimSpace(t.Description)
	if r := []rune(desc); len(r) > mcpMaxDescRunes {
		desc = string(r[:mcpMaxDescRunes]) + "…"
	}
	schema := mcpNormalizeSchema(t.InputSchema)
	return toolDef{
		Type: "function",
		Function: toolFunctionDef{
			Name:        fullName,
			Description: desc,
			Parameters:  schema,
		},
	}
}

// mcpNormalizeSchema شمای سرور را برای OpenAI آماده می‌کند: type=object،
// حذف $schema، سقف حجم (بزرگ‌تر از حد → شمای خنثی با توضیح).
func mcpNormalizeSchema(raw json.RawMessage) map[string]interface{} {
	if len(raw) == 0 {
		return schemaObj(map[string]interface{}{}, nil)
	}
	if len(raw) > mcpMaxSchemaBytes {
		return schemaObj(map[string]interface{}{}, nil)
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(raw, &schema); err != nil || schema == nil {
		return schemaObj(map[string]interface{}{}, nil)
	}
	delete(schema, "$schema")
	if t, _ := schema["type"].(string); t != "object" {
		schema["type"] = "object"
	}
	return schema
}

// isMCPToolName آیا نام ابزار متعلق به MCP است؟ (mcp_<server>_<tool> —
// حداقل دو زیرخط: یکی بعد از mcp و یکی جداکننده‌ی سرور/ابزار)
func isMCPToolName(name string) bool {
	return strings.HasPrefix(name, "mcp_") && strings.Count(name, "_") >= 2
}

// splitMCPToolName نام کامل را به (سرور، ابزار) می‌شکند — از رجیستری
// ابزارهای همین نوبت؛ نام سرور/ابزار ممکن است خودشان «_» داشته باشند،
// پس تطبیق با فهرست تعریف‌های زنده انجام می‌شود.
func (a *App) splitMCPToolName(full string) (server, tool string, ok bool) {
	if !isMCPToolName(full) {
		return "", "", false
	}
	a.initMCPManager()
	for _, cfg := range a.mcpEnabledConfigs() {
		c := a.mcp.client(cfg.Name)
		if c == nil {
			continue
		}
		for _, t := range c.cachedTools() {
			if mcpToolFullName(cfg.Name, t.Name) == full || mcpTruncHash(cfg.Name, t.Name) == full {
				return cfg.Name, t.Name, true
			}
		}
	}
	return "", "", false
}

// toolMCPCall اجرای یک ابزار MCP: مهلت ۶۰ ثانیه، لغو با notifications/cancelled،
// خروجی کران‌دار در پوشش untrusted با escape تگ بسته‌شونده.
func (a *App) toolMCPCall(ctx context.Context, name, argsJSON string) (string, bool) {
	a.initMCPManager()
	server, tool, ok := a.splitMCPToolName(name)
	if !ok {
		return "ERROR: unknown MCP tool '" + name + "' (its server is not connected).", false
	}
	var cfg *MCPServerConfig
	configs := a.mcpEnabledConfigs()
	for i := range configs {
		if configs[i].Name == server {
			cfg = &configs[i]
			break
		}
	}
	if cfg == nil || !cfg.Enabled {
		return "ERROR: MCP server '" + server + "' is not configured or disabled.", false
	}

	client, err := a.mcp.ensureStarted(ctx, a, *cfg)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}

	// آرگومان‌ها فقط باید «آبجکت JSON» باشند — اعتبارسنجی عمیق با سرور است
	args := json.RawMessage(nil)
	if s := strings.TrimSpace(argsJSON); s != "" && s != "{}" {
		var probe map[string]interface{}
		if jerr := json.Unmarshal([]byte(s), &probe); jerr != nil || probe == nil {
			return "ERROR: invalid arguments for " + name + ": arguments must be a single JSON object.", false
		}
		args = json.RawMessage(s)
	}

	res, cerr := client.callTool(ctx, tool, args)
	if cerr != nil {
		return "ERROR: MCP call '" + name + "' failed: " + cerr.Error(), false
	}

	content := mcpRenderContent(res)
	if res.IsError {
		return "ERROR: MCP tool '" + name + "' returned an error: " + truncateToolOutput(content), false
	}
	return wrapUntrustedContent("mcp_content", map[string]string{
		"server":    server,
		"tool":      tool,
		"untrusted": "true",
	}, truncateToolOutput(content)), true
}

// mcpRenderContent آیتم‌های content را به متن می‌رساند: textها می‌چسبند؛
// تصویر/blob با جای‌نگهدار کوتاه؛ resource متنی در می‌آید.
func mcpRenderContent(res mcpCallResult) string {
	var b strings.Builder
	for _, it := range res.Content {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		switch it.Type {
		case "text", "":
			b.WriteString(it.Text)
		case "image":
			b.WriteString(fmt.Sprintf("[image omitted: %s, %d bytes]", mcpMimeOrDefault(it.MimeType, "image/png"), mcpDecodedLen(it.Data)))
		case "audio":
			b.WriteString(fmt.Sprintf("[audio omitted: %s, %d bytes]", mcpMimeOrDefault(it.MimeType, "audio/wav"), mcpDecodedLen(it.Data)))
		case "resource":
			if it.Resource != nil {
				if it.Resource.Text != "" {
					b.WriteString(it.Resource.Text)
				} else if it.Resource.Blob != "" {
					b.WriteString(fmt.Sprintf("[resource blob omitted: %s, %d bytes]", mcpMimeOrDefault(it.Resource.MimeType, "application/octet-stream"), mcpDecodedLen(it.Resource.Blob)))
				} else {
					b.WriteString(fmt.Sprintf("[resource: %s]", it.Resource.URI))
				}
			}
		default:
			b.WriteString(fmt.Sprintf("[unsupported content type: %s]", it.Type))
		}
	}
	if b.Len() == 0 {
		return "(no content)"
	}
	return b.String()
}

func mcpMimeOrDefault(mime, def string) string {
	if strings.TrimSpace(mime) == "" {
		return def
	}
	return mime
}

// mcpDecodedLen طول باینری واقعی data پایه۶۴ (برای جای‌نگهدار تصویر/blob).
func mcpDecodedLen(b64 string) int {
	if b64 == "" {
		return 0
	}
	if n, err := base64.StdEncoding.DecodeString(b64); err == nil {
		return len(n)
	}
	if n, err := base64.RawStdEncoding.DecodeString(b64); err == nil {
		return len(n)
	}
	if n, err := base64.URLEncoding.DecodeString(b64); err == nil {
		return len(n)
	}
	return len(b64) // حداقلِ قابل گزارش
}

// ---------------------------------------------------------------- تنظیمات (bound)

// mcpValidateName قاعده‌ی نام سرور: [a-zA-Z0-9_-]{1,32}.
func mcpValidateName(name string) error {
	if len(name) < 1 || len(name) > 32 {
		return errors.New("نام سرور باید بین ۱ تا ۳۲ نویسه باشد")
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return errors.New("نام سرور فقط می‌تواند حرف انگلیسی، رقم، خط تیره و زیرخط داشته باشد")
		}
	}
	return nil
}

// SaveMCPServers ذخیره‌ی پیکربندی سرورهای MCP از UI (رشته‌ی JSON).
// مقادیر env: ماسکِ ارسالی = حفظ مقدار رمزشده‌ی قبلی؛ در غیر این صورت
// رمزنگاری مقدار جدید. اعتبارسنجی با خطاهای دقیق فارسی.
func (a *App) SaveMCPServers(configJSON string) error {
	var incoming []MCPServerConfig
	if strings.TrimSpace(configJSON) != "" {
		if err := json.Unmarshal([]byte(configJSON), &incoming); err != nil {
			return fmt.Errorf("JSON نامعتبر است: %v", err)
		}
	}
	names := map[string]bool{}
	for i := range incoming {
		c := &incoming[i]
		c.Name = strings.TrimSpace(c.Name)
		if err := mcpValidateName(c.Name); err != nil {
			return fmt.Errorf("سرور #%d: %v", i+1, err)
		}
		if names[c.Name] {
			return fmt.Errorf("نام سرور «%s» تکراری است", c.Name)
		}
		names[c.Name] = true
		if err := mcpValidateCommand(c.Command); err != nil {
			return fmt.Errorf("سرور «%s»: فرمان نباید خالی یا شامل نویسه‌ی NUL/خط جدید باشد", c.Name)
		}
	}

	a.mu.Lock()
	old := map[string]MCPServerConfig{}
	for _, c := range a.settings.MCPServers {
		old[c.Name] = c
	}
	// env: ماسک → حفظ رمزشده‌ی قبلی؛ مقدار جدید → رمزنگاری
	for i := range incoming {
		c := &incoming[i]
		if len(c.Env) == 0 {
			continue
		}
		env := make(map[string]string, len(c.Env))
		for k, v := range c.Env {
			k = strings.TrimSpace(k)
			if k == "" || strings.ContainsAny(k, "=\x00") || strings.Contains(k, "\n") {
				a.mu.Unlock()
				return fmt.Errorf("سرور «%s»: نام متغیر محیطی «%s» نامعتبر است", c.Name, k)
			}
			if v == mcpEnvMask {
				if prev, ok := old[c.Name]; ok {
					if pv, pok := prev.Env[k]; pok {
						env[k] = pv // همان ماسک — مقدار رمزشده دست‌نخورده می‌ماند
						continue
					}
				}
				// ماسک برای کلیدی که قبلاً نبوده — پاک کردن یعنی مقدار خالی
				env[k] = ""
				continue
			}
			enc, err := encryptString(v)
			if err != nil {
				a.mu.Unlock()
				return fmt.Errorf("سرور «%s»: رمزنگاری متغیر «%s» ناموفق بود: %v", c.Name, k, err)
			}
			env[k] = enc
		}
		c.Env = env
	}
	a.settings.MCPServers = incoming
	if a.settings.MCPServers == nil {
		a.settings.MCPServers = []MCPServerConfig{}
	}
	configs := make([]MCPServerConfig, len(a.settings.MCPServers))
	copy(configs, a.settings.MCPServers)
	if err := a.saveSettings(); err != nil {
		a.mu.Unlock()
		return err
	}
	a.mu.Unlock()

	// reload بیرون از a.mu (کشتن/راه‌اندازی فرایند زیر قفل ممنوع)
	a.initMCPManager()
	a.mcp.reload(a, configs)
	return nil
}

// MCPStatus وضعیت سرورها برای UI (نام، فعال، وضعیت فارسی، خطا، تعداد ابزار).
func (a *App) MCPStatus() []map[string]interface{} {
	a.initMCPManager()
	configs := a.mcpEnabledConfigs()
	out := make([]map[string]interface{}, 0, len(configs))
	for _, cfg := range configs {
		st, lastErr := a.mcp.statusOf(cfg.Name)
		label := "متصل"
		switch st {
		case mcpStatusConnecting:
			label = "در حال اتصال"
		case mcpStatusError:
			label = "خطا: " + cutUTF8(lastErr, 160)
		case mcpStatusDisabled:
			label = "غیرفعال"
		case mcpStatusStopped:
			if cfg.Enabled {
				label = "قطع (در نخستین نیاز دوباره وصل می‌شود)"
			} else {
				label = "غیرفعال"
			}
		case mcpStatusConnected:
			label = "متصل"
		}
		toolCount := 0
		if c := a.mcp.client(cfg.Name); c != nil {
			toolCount = len(c.cachedTools())
		}
		out = append(out, map[string]interface{}{
			"name":      cfg.Name,
			"enabled":   cfg.Enabled,
			"status":    label,
			"toolCount": toolCount,
		})
	}
	return out
}
