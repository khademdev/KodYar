// کلاینت MCP (Model Context Protocol) — انتقال stdio با JSON-RPC 2.0.
//
// هر سرور MCP یک فرایند فرزند است: پیام‌های JSON-RPC «خط‌به‌خط» روی
// stdin/stdout رد و بدل می‌شوند (پیام هرگز newline خام ندارد). این فایل فقط
// «پروتکل» را پیاده می‌کند (اتصال، handshake، tools/list صفحه‌بندی‌شده،
// tools/call، پاسخ به درخواست‌های سرور، notifications)؛ چرخه‌ی حیات
// (شروع تنبل، backoff، کشتن درخت فرایند) در mcpmanager.go است.
//
// امنیت:
// - محیط فرزند «حداقلی» است + env تنظیم‌شده‌ی کاربر (رمزشده در
// settings.json با همان AES-GCM/keyring کلیدهای API) — هرگز کل تنظیمات
// یا کلیدهای برنامه به فرزند نمی‌رسد (mcpChildEnv).
// - stderr فرزند در بافر حلقه‌ای کران‌دار (۶۴KB) جمع و نرخ‌محدود به لاگ
// خطاها می‌رود.
// - کشتن درخت فرایند (فرزندِ فرزند هم) با همان helperهای shell.go.
// - نوشتن روی stdin فقط با writeMu — هرگز از دو goroutine هم‌زمان.
//
// زبان: خطاهای این فایل رو به مدل/لاگ و انگلیسی‌اند؛ متن‌های UI فارسی در
// فرانت‌اند (mcpsettings.js) هستند.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// محدودیت‌های کلاینت MCP.
const (
	mcpProtocolVersion = "2025-06-18" // نسخه‌ای که پیشنهاد می‌دهیم (negotiation پایین‌تر)

	mcpStartBudget = 15 * time.Second // بودجه‌ی کل «initialize + tools/list» برای شروع تنبل

	mcpMaxToolsPerServer = 200             // سقف کل ابزارهای هر سرور
	mcpMaxMessageBytes   = 8 << 20         // سقف یک پیام JSON-RPC (۸MB)
	mcpMaxStderrRing     = 64 * 1024       // بافر حلقه‌ای stderr
	mcpStderrLogEvery    = 2 * time.Second // نرخ لاگ stderr (حداکثر یک‌بار در این بازه)

	mcpMaxSchemaBytes = 16 * 1024 // سقف شمای هر ابزار — بزرگ‌تر کنار گذاشته می‌شود
	mcpMaxDescRunes   = 1024      // سقف توضیح هر ابزار
)

// mcpCallTimeout مهلت هر فراخوانی tools/call (پیش‌فرض ۶۰ ثانیه — «ثابت
// قابل تنظیم»؛ در تست با setMCPCallTimeoutForTest کوتاه می‌شود). فقط
// پیش از فراخوانی‌های هم‌زمان عوض شود.
var mcpCallTimeout = 60 * time.Second

// mcpWriteTimeout سقف هر نوشتن روی stdin سرور. سروری که hang کرده باشد و
// stdin را نخواند، Write را برای همیشه بلاک می‌کند؛ بدون این سقف، goroutine
// ابزار و کل نوبت ایجنت قفل می‌شد و حتی StopGeneration هم بازش نمی‌کرد.
// پس از این مهلت درخت فرایند کشته می‌شود — مرگ فرایند لوله را می‌بندد و
// Write بلاک‌شده با خطا برمی‌گردد.
var mcpWriteTimeout = 10 * time.Second

// setMCPWriteTimeoutForTest تغییر مهلت نوشتن برای تست؛ مقدار قبلی را
// به‌عنوان cleanup برمی‌گرداند.
func setMCPWriteTimeoutForTest(d time.Duration) (restore func()) {
	old := mcpWriteTimeout
	mcpWriteTimeout = d
	return func() { mcpWriteTimeout = old }
}

// mcpReadJoinTimeout حداکثر انتظار waitExit برای تخلیه‌ی کامل stdout پس از
// مرگ فرایند — آخرین پاسخ‌های سرورِ در حال مرگ برسند؛ حالت نادرِ فرزندی
// که stdout را باز نگه دارد بعد از این مدت رها می‌شود.
const mcpReadJoinTimeout = 2 * time.Second

// setMCPCallTimeoutForTest تغییر مهلت فراخوانی برای تست؛ مقدار قبلی را
// به‌عنوان cleanup برمی‌گرداند.
func setMCPCallTimeoutForTest(d time.Duration) (restore func()) {
	old := mcpCallTimeout
	mcpCallTimeout = d
	return func() { mcpCallTimeout = old }
}

// knownMCPProtocolVersions نسخه‌هایی که می‌فهمیم؛ پاسخ سرور باید یکی از
// این‌ها باشد وگرنه اتصال با خطای روشن رد می‌شود.
var knownMCPProtocolVersions = map[string]bool{
	"2024-10-07": true,
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

// ---------------------------------------------------------------- انواع JSON-RPC

// jsonRPCMessage نمای کلی یک پیام — هم درخواست/اعلان ما، هم پاسخ/درخواست سرور.
type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // number/string؛ nil = notification
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *jsonRPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// mcpInitializeResult پاسخ سرور به initialize.
type mcpInitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

// mcpToolDef ابزار یک سرور MCP (بخشِ لازم از پاسخ tools/list).
type mcpToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type mcpToolsListResult struct {
	Tools      []mcpToolDef `json:"tools"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

// mcpContentItem یک آیتم content نتیجه‌ی tools/call.
type mcpContentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
	Resource *struct {
		URI      string `json:"uri"`
		MimeType string `json:"mimeType,omitempty"`
		Text     string `json:"text,omitempty"`
		Blob     string `json:"blob,omitempty"`
	} `json:"resource,omitempty"`
}

type mcpCallResult struct {
	Content []mcpContentItem `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

// ---------------------------------------------------------------- کلاینت

// rpcResult پاسخ در انتظارِ یک درخواست.
type rpcResult struct {
	msg *jsonRPCMessage
	err error
}

// mcpClient اتصال به یک سرور MCP از طریق stdio.
type mcpClient struct {
	name   string
	cmd    *exec.Cmd
	tree   *procTree
	stdin  io.WriteCloser
	stdout io.ReadCloser
	app    *App // فقط برای لاگ خطاها

	writeMu  sync.Mutex // نگهبان نوشتن روی stdin (هرگز هم‌زمان دو نویسنده)
	pendMu   sync.Mutex
	pending  map[string]chan rpcResult
	nextID   int64
	closed   bool
	closeErr error

	listChanged func() // callback اعلان notifications/tools/list_changed (manager)

	// onExit پس از مرگ «غیرمنتظره» فرایند صدا زده می‌شود (خارج از قفل‌های
	// کلاینت) تا manager کلاینت مرده را از نقشه در کند و backoff را بشمارد.
	// توقف عامدانه (stop/kill از سمت ما) با deliberate علامت می‌خورد و
	// callback نمی‌خورد.
	onExit     func()
	deliberate atomic.Bool

	toolsMu     sync.Mutex
	tools       []mcpToolDef // کش فهرست ابزارها
	toolsLoaded bool

	// readDone با پایان readLoop بسته می‌شود — waitExit پیش از Wait آن را
	// می‌خواند تا بافر stdout کامل تخلیه شود (مسابقه از‌دست‌رفتن آخرین
	// پاسخ‌ها هنگام مرگ فرایند).
	readDone chan struct{}

	exitOnce sync.Once
}

var (
	// mcpBaseEnvKeys متغیرهای پایه‌ای که هر سرور MCP (معمولاً یک فرایند
	// Node/Python) برای اجرا لازم دارد. هیچ کلید API یا تنظیمات MyAI اینجا نیست.
	mcpBaseEnvKeys = []string{
		"PATH", "SystemRoot", "windir", "TEMP", "TMP", "HOME", "USERPROFILE",
		"USER", "USERNAME", "LANG", "LC_ALL", "LC_CTYPE", "COMSPEC", "PATHEXT",
		"APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
		"PROGRAMFILES", "ProgramFiles(x86)", "ProgramW6432", "ALLUSERSPROFILE",
		"SystemDrive", "PROCESSOR_ARCHITECTURE", "NUMBER_OF_PROCESSORS",
	}

	// mcpProxyEnvKeys متغیرهای شبکه/TLS برای محیط‌های شرکت/پروکسی (C.4): بدون
	// این‌ها سرور MCP در شبکه‌ی پروکسی‌دار اصلاً بالا نمی‌آید. فقط همین‌ها منتقل
	// می‌شوند — نه OPENAI_API_KEY یا هر رازِ دیگری از محیط برنامه.
	mcpProxyEnvKeys = []string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY",
		"http_proxy", "https_proxy", "no_proxy", "all_proxy",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
	}
)

// mcpChildEnv محیط «حداقلیِ» فرزند + env رمزگشایی‌شده‌ی کاربر.
//
// عمداً فقط PATH/SystemRoot/TEMP/TMP/HOME/USERPROFILE و locale منتقل
// می‌شود — نه settings.json، نه کلیدهای API، نه متغیرهای برنامه؛ مقادیر
// Env تنظیمات همان‌جا رمزگشایی می‌شوند و هرگز لاگ نمی‌شوند.
func mcpChildEnv(cfgEnv map[string]string) ([]string, error) {
	keep := append(append([]string{}, mcpBaseEnvKeys...), mcpProxyEnvKeys...)
	// ترتیب/تکرار مهم نیست (Windows خودش آخرین مقدار را برمی‌دارد)، ولی
	// کلیدهای تکراری ساخته نمی‌شوند چون دو لیست جدا هستند.
	env := make([]string, 0, len(keep)+len(cfgEnv))
	for _, k := range keep {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	for k, v := range cfgEnv {
		k = strings.TrimSpace(k)
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.Contains(k, "\n") {
			return nil, fmt.Errorf("invalid environment variable name %q", k)
		}
		if v == "" {
			env = append(env, k+"=")
			continue
		}
		plain := v
		// مقادیر v2: همیشه رمزشده‌اند (SaveMCPServers)؛ مقدار legacy هم
		// تلاش رمزگشایی می‌شود و اگر باز نشد (مثلاً فایل دستی‌نویس) خام
		// منتقل می‌شود — دست کاربر است.
		if strings.HasPrefix(v, cryptoV2Prefix) {
			dec, err := decryptString(v)
			if err != nil {
				return nil, fmt.Errorf("cannot decrypt environment variable %q (settings may be from another machine)", k)
			}
			plain = dec
		} else if dec, err := legacyDecryptString(v); err == nil {
			plain = dec
		}
		if strings.ContainsRune(plain, 0) {
			return nil, fmt.Errorf("environment variable %q contains a NUL character", k)
		}
		env = append(env, k+"="+plain)
	}
	return env, nil
}

// isMCPEnvCiphertext آیا مقدار env رمزشده است (فرمت v2: یا legacy)؟
// مقدار «خام» کاربر (مثلاً "1" یا "true") همان‌طور که هست منتقل می‌شود.
func isMCPEnvCiphertext(v string) bool {
	return strings.HasPrefix(v, cryptoV2Prefix) || isLegacyCiphertext(v)
}

// mcpValidateCommand قواعد امن فرمان فرزند: بدون NUL/newline، اجرای
// مستقیم argv (بدون ساخت رشته‌ی shell) و حل مسیر با exec.LookPath —
// روی ویندوز فرمان‌هایی مثل npx به فایل.cmd می‌رسند و stdlib خودش
// PATHEXT را حل می‌کند.
func mcpValidateCommand(command string) error {
	if strings.TrimSpace(command) == "" {
		return errors.New("command is empty")
	}
	if strings.ContainsRune(command, 0) || strings.ContainsAny(command, "\r\n") {
		return errors.New("command must not contain NUL or newline characters")
	}
	return nil
}

// startMCPClient فرایند فرزند را اجرا و handshake کامل می‌کند.
// ctx سقف کل راه‌اندازی را اعمال می‌کند (بودجه‌ی ۱۵ ثانیه‌ی manager).
// onExit (اختیاری) پس از مرگ «غیرمنتظره» فرایند با خودِ کلاینت صدا زده
// می‌شود — manager برای شمارش backoff آن را می‌بندد.
func startMCPClient(ctx context.Context, app *App, cfg MCPServerConfig, onListChanged func(), onExit func(*mcpClient)) (*mcpClient, error) {
	if err := mcpValidateCommand(cfg.Command); err != nil {
		return nil, fmt.Errorf("MCP server '%s': %v", cfg.Name, err)
	}
	env, err := mcpChildEnv(cfg.Env)
	if err != nil {
		return nil, fmt.Errorf("MCP server '%s': %v", cfg.Name, err)
	}
	lp, err := exec.LookPath(cfg.Command)
	if err != nil {
		return nil, fmt.Errorf("MCP server '%s': command %q not found in PATH", cfg.Name, cfg.Command)
	}

	cmd := exec.Command(lp, cfg.Args...)
	cmd.Env = env
	tree, terr := prepareProcessTree(cmd)
	if terr != nil {
		return nil, fmt.Errorf("MCP server '%s': process tree setup: %w", cfg.Name, terr)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("MCP server '%s': stdin pipe: %w", cfg.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("MCP server '%s': stdout pipe: %w", cfg.Name, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("MCP server '%s': stderr pipe: %w", cfg.Name, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("MCP server '%s': cannot start %q: %w", cfg.Name, cfg.Command, err)
	}
	if err := tree.attach(cmd); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		tree.close()
		// بستن لوله‌ها — Process.Wait از مسیر os می‌گذرد و پاکسازی
		// خودکار exec.Cmd.Wait را دور می‌زند؛ بدون این، FDها می‌مانند.
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderrPipe.Close()
		return nil, fmt.Errorf("MCP server '%s': attach process tree: %w", cfg.Name, err)
	}

	c := &mcpClient{
		name:        cfg.Name,
		cmd:         cmd,
		tree:        tree,
		stdin:       stdin,
		stdout:      stdout,
		app:         app,
		pending:     map[string]chan rpcResult{},
		listChanged: onListChanged,
		readDone:    make(chan struct{}),
	}
	if onExit != nil {
		oc := c
		c.onExit = func() { onExit(oc) }
	}

	// خواننده‌ی stdout — تا پایان عمر فرایند زنده است و با EOF تمام می‌شود.
	safeGo(c.app, "mcp.readLoop("+c.name+")", c.readLoop)
	// دیده‌بان خروج فرایند — درخت را می‌بندد و pendingها را آزاد می‌کند.
	safeGo(c.app, "mcp.waitExit("+c.name+")", c.waitExit)
	// stderr کران‌دار + نرخ‌محدود به لاگ خطاها.
	safeGo(c.app, "mcp.stderr("+c.name+")", func() { c.drainStderr(stderrPipe) })

	if err := c.initialize(ctx); err != nil {
		// شکست راه‌اندازی توقف عامدانه است — شمارش شکست با مسیر خطای
		// ensureStarted است، نه onExit.
		c.deliberate.Store(true)
		c.kill()
		return nil, fmt.Errorf("MCP server '%s': %v", cfg.Name, err)
	}
	return c, nil
}

// initialize دست‌دهی: initialize → پذیرش نسخه‌ی سرور → initialized.
func (c *mcpClient) initialize(ctx context.Context) error {
	params := map[string]interface{}{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]string{"name": "MyAI", "version": version},
	}
	var res mcpInitializeResult
	if err := c.call(ctx, "initialize", params, &res); err != nil {
		return fmt.Errorf("initialize failed: %w", err)
	}
	if !knownMCPProtocolVersions[res.ProtocolVersion] {
		return fmt.Errorf("server uses unsupported protocol version %q (known: 2024-10-07, 2024-11-05, 2025-03-26, 2025-06-18)", res.ProtocolVersion)
	}
	// اعلان initialized — پاسخی ندارد
	if err := c.notify("notifications/initialized", nil); err != nil {
		return fmt.Errorf("sending notifications/initialized: %w", err)
	}
	return nil
}

// listTools فهرست ابزارها را (با صفحه‌بندی) می‌خواند و کش می‌کند.
func (c *mcpClient) listTools(ctx context.Context) ([]mcpToolDef, error) {
	var all []mcpToolDef
	cursor := ""
	for {
		params := map[string]interface{}{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res mcpToolsListResult
		if err := c.call(ctx, "tools/list", params, &res); err != nil {
			return nil, fmt.Errorf("tools/list failed: %w", err)
		}
		all = append(all, res.Tools...)
		if res.NextCursor == "" || len(all) >= mcpMaxToolsPerServer {
			break
		}
		cursor = res.NextCursor
	}
	if len(all) > mcpMaxToolsPerServer {
		all = all[:mcpMaxToolsPerServer]
	}
	c.toolsMu.Lock()
	c.tools = all
	c.toolsLoaded = true
	c.toolsMu.Unlock()
	return all, nil
}

// cachedTools آخرین فهرست کش‌شده (nil اگر هنوز خوانده نشده).
func (c *mcpClient) cachedTools() []mcpToolDef {
	c.toolsMu.Lock()
	defer c.toolsMu.Unlock()
	if !c.toolsLoaded {
		return nil
	}
	out := make([]mcpToolDef, len(c.tools))
	copy(out, c.tools)
	return out
}

// callTool یک tools/call با مهلت mcpCallTimeout؛ لغو کاربر باعث ارسال
// notifications/cancelled برای همین درخواست و بازگشت فوری می‌شود.
func (c *mcpClient) callTool(ctx context.Context, name string, arguments json.RawMessage) (mcpCallResult, error) {
	ctx, cancel := context.WithTimeout(ctx, mcpCallTimeout)
	defer cancel()

	params := map[string]interface{}{"name": name}
	if len(arguments) > 0 {
		params["arguments"] = arguments
	}
	var res mcpCallResult
	if err := c.call(ctx, "tools/call", params, &res); err != nil {
		return res, err
	}
	return res, nil
}

// call یک درخواست JSON-RPC می‌فرستد، روی پاسخ می‌ماند و آن را در out می‌ریزد.
// خروجی nil یعنی Result؛ خطای پروتکل به‌صورت error برگردانده می‌شود.
func (c *mcpClient) call(ctx context.Context, method string, params interface{}, out interface{}) error {
	started := time.Now()
	idCh, id, err := c.sendRequest(method, params)
	if err != nil {
		return err
	}
	select {
	case r := <-idCh:
		if r.err != nil {
			return r.err
		}
		if r.msg.Error != nil {
			return r.msg.Error
		}
		if out != nil && len(r.msg.Result) > 0 {
			if jerr := json.Unmarshal(r.msg.Result, out); jerr != nil {
				return fmt.Errorf("invalid result for %s: %w", method, jerr)
			}
		}
		return nil
	case <-ctx.Done():
		// حذف بهترین‌تلاشِ ورودی از نقشه‌ی pending — پاسخ دیرهنگام به
		// کانال یتیم می‌رسد (بافردار — بدون نشتی) و نقشه تمیز می‌ماند.
		c.pendMu.Lock()
		delete(c.pending, fmt.Sprintf("%d", id))
		c.pendMu.Unlock()
		// اطلاع لغو به سرور (best-effort، بدون انتظار) — بعد از آن فوری برمی‌گردیم.
		_ = c.notify("notifications/cancelled", map[string]interface{}{"requestId": id})
		if errors.Is(ctx.Err(), context.Canceled) {
			return errors.New("cancelled by the user")
		}
		// مدت واقعیِ انتظار — ctx والد (بودجه‌ی شروع و…) ممکن است زودتر از
		// mcpCallTimeout تمام شود؛ پیام همان باشد که واقعاً رخ داده است.
		return fmt.Errorf("timed out after %s", time.Since(started).Round(time.Millisecond))
	}
}

// sendRequest درخواست را با شناسه‌ی یکتا می‌نویسد و کانال پاسخ را برمی‌گرداند.
func (c *mcpClient) sendRequest(method string, params interface{}) (chan rpcResult, int64, error) {
	c.pendMu.Lock()
	if c.closed {
		c.pendMu.Unlock()
		return nil, 0, errors.New("server connection is closed")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResult, 1)
	c.pending[fmt.Sprintf("%d", id)] = ch
	c.pendMu.Unlock()

	payload := map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	if err := c.writeJSON(payload); err != nil {
		c.pendMu.Lock()
		delete(c.pending, fmt.Sprintf("%d", id))
		c.pendMu.Unlock()
		return nil, 0, err
	}
	return ch, id, nil
}

// notify یک اعلان JSON-RPC (بدون id/بدون پاسخ) می‌فرستد.
func (c *mcpClient) notify(method string, params interface{}) error {
	payload := map[string]interface{}{"jsonrpc": "2.0", "method": method}
	if params != nil {
		payload["params"] = params
	}
	return c.writeJSON(payload)
}

// reply کوتاه به یک «درخواست سرور» — id عیناً بازگردانده می‌شود.
func (c *mcpClient) reply(id json.RawMessage, result interface{}, rpcErr *jsonRPCError) error {
	payload := map[string]interface{}{"jsonrpc": "2.0", "id": json.RawMessage(id)}
	if rpcErr != nil {
		payload["error"] = rpcErr
	} else {
		payload["result"] = result
	}
	return c.writeJSON(payload)
}

// isClosed/markClosed وضعیت بسته‌شدن اتصال — «همیشه» زیر pendMu (نگهبان
// واحد؛ قبلاً writeMu هم نوشت و مسابقه داده‌ای بود).
func (c *mcpClient) isClosed() bool {
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	return c.closed
}

func (c *mcpClient) markClosed() {
	c.pendMu.Lock()
	c.closed = true
	c.pendMu.Unlock()
}

// writeJSON مارشال + newline + نوشتن روی stdin زیر writeMu. نوشتن با سقف
// mcpWriteTimeout همراه است: سرور hang‌شده‌ای که stdin را نمی‌خواند Write را
// برای همیشه بلاک می‌کرد و کل نوبت ایجنت را قفل می‌کرد؛ پس از سقف، درخت
// فرایند کشته می‌شود — مرگ فرایند لوله را می‌بندد و Write آزاد می‌شود
// (goroutine نویسنده با خطا برمی‌گردد و کانال بافردارش را خالی می‌گذارد).
func (c *mcpClient) writeJSON(payload interface{}) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if bytes.IndexByte(raw, '\n') >= 0 {
		return errors.New("internal error: JSON message contains a raw newline")
	}
	if c.isClosed() {
		return errors.New("server connection is closed")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.isClosed() { // دوباره زیر writeMu — بین دو چک بسته شده باشد
		return errors.New("server connection is closed")
	}
	done := make(chan error, 1)
	// نتیجه همیشه فرستاده می‌شود (حتی در صورت panic) تا Write برای همیشه
	// منتظر نماند و watchdog همیشه آزاد شود.
	safeGo(c.app, "mcp.write("+c.name+")", func() {
		defer func() {
			if r := recover(); r != nil {
				recordPanic(c.app, "mcp.write("+c.name+")", r)
				done <- errors.New("internal error while writing to server stdin")
			}
		}()
		_, werr := c.stdin.Write(append(raw, '\n'))
		done <- werr
	})
	timer := time.NewTimer(mcpWriteTimeout)
	defer timer.Stop()
	select {
	case werr := <-done:
		if werr != nil {
			return fmt.Errorf("write to server stdin: %w", werr)
		}
		return nil
	case <-timer.C:
		// سرور stdin را نمی‌خواند — بکش تا لوله بسته شود و Write برگردد.
		// «غیرعامدانه» است: شمارش شکست/backoff با onExit انجام می‌شود.
		c.kill()
		return fmt.Errorf("write to server stdin timed out after %s; server appears hung and was killed", mcpWriteTimeout)
	}
}

// readLoop خواندن خط‌به‌خط stdout و مسیریابی پیام‌ها. با پایان (EOF/خطا)
// readDone بسته می‌شود تا waitExit بداند بافر تخلیه شده است.
func (c *mcpClient) readLoop() {
	defer close(c.readDone)
	// panic در پردازش یک پیام نباید برنامه را بکشد یا فراخوانی‌های منتظر را
	// معلق بگذارد؛ سرور مرده علامت می‌خورد و backoff معمول اجرا می‌شود.
	defer func() {
		if r := recover(); r != nil {
			recordPanic(c.app, "mcp.readLoop("+c.name+")", r)
			c.failPending(errors.New("internal error in MCP read loop"))
			c.markClosed()
		}
	}()
	sc := bufio.NewScanner(c.stdout)
	sc.Buffer(make([]byte, 0, 64*1024), mcpMaxMessageBytes)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg jsonRPCMessage
		if jerr := json.Unmarshal(line, &msg); jerr != nil {
			// خط نامعتبر بی‌صدا رد می‌شود (تلورانس پروتکل) — فقط لاگ سبک
			if c.app != nil {
				logAppErrorFor(c.app, "mcp", c.name+": ignoring invalid JSON-RPC line ("+cutUTF8(string(line), 120)+")")
			}
			continue
		}
		switch {
		case len(msg.ID) > 0 && msg.Method != "":
			c.handleServerRequest(msg)
		case len(msg.ID) > 0:
			c.routeResponse(msg)
		case msg.Method != "":
			c.handleNotification(msg)
		}
	}
	c.failPending(errors.New("server closed the connection"))
}

// routeResponse پاسخ را به درخواست منتظر می‌رساند.
func (c *mcpClient) routeResponse(msg jsonRPCMessage) {
	// id ممکن است number یا string باشد — کلید یکسان‌سازی: رشته‌ی خام بدون space
	key := compactJSONRaw(msg.ID)
	c.pendMu.Lock()
	ch, ok := c.pending[key]
	if ok {
		delete(c.pending, key)
	}
	c.pendMu.Unlock()
	if ok {
		ch <- rpcResult{msg: &msg}
	}
}

// compactJSONRaw فاصله‌های بیرون/داخل خام id را برای کلید حذف می‌کند.
func compactJSONRaw(raw json.RawMessage) string {
	return string(bytes.TrimSpace(raw))
}

// handleServerRequest درخواست سرور→کلاینت: فقط ping پشتیبانی می‌شود؛
// بقیه (sampling/roots و…) با خطای -32601 جواب می‌گیرند.
func (c *mcpClient) handleServerRequest(msg jsonRPCMessage) {
	switch msg.Method {
	case "ping":
		_ = c.reply(msg.ID, map[string]interface{}{}, nil)
	default:
		_ = c.reply(msg.ID, nil, &jsonRPCError{
			Code:    -32601, // Method not found
			Message: "MyAI MCP client does not support this server request: " + msg.Method,
		})
	}
}

// handleNotification اعلان‌های سرور — فقط tools/list_changed معنا دارد؛
// ناشناخته‌ها تلورانت نادیده گرفته می‌شوند.
func (c *mcpClient) handleNotification(msg jsonRPCMessage) {
	if msg.Method == "notifications/tools/list_changed" && c.listChanged != nil {
		safeGo(c.app, "mcp.notification("+c.name+")", c.listChanged) // خارج از readLoop — callback ممکن است با سرور حرف بزند
	}
}

// failPending همه‌ی درخواست‌های در انتظار را با خطا آزاد می‌کند.
func (c *mcpClient) failPending(err error) {
	c.pendMu.Lock()
	if c.closed {
		c.pendMu.Unlock()
		return
	}
	pending := c.pending
	c.pending = map[string]chan rpcResult{}
	c.pendMu.Unlock()
	for _, ch := range pending {
		ch <- rpcResult{err: err}
	}
}

// waitExit مرگ فرایند را می‌بیند، pendingها را آزاد و درخت را می‌بندد.
// ترتیب: نخست تخلیه‌ی کامل stdout (readDone — آخرین پاسخ‌های سرورِ در حال
// مرگ برسند؛ exec.Cmd.Wait لوله را می‌بندد و داده‌ی نخوانده را دور می‌ریخت)،
// سپس Wait. سقف mcpReadJoinTimeout برای حالت نادرِ فرزندی که stdout را
// باز نگه می‌دارد.
func (c *mcpClient) waitExit() {
	// panic نباید pendingها را معلق بگذارد: همان پاک‌سازی خروج یک‌باره اجرا
	// می‌شود (exitOnce تضمین می‌کند دوباره انجام نشود).
	defer func() {
		if r := recover(); r != nil {
			recordPanic(c.app, "mcp.waitExit("+c.name+")", r)
			c.exitOnce.Do(func() {
				c.failPending(errors.New("internal error while waiting for server process"))
				c.markClosed()
				c.tree.close()
				if cb := c.onExit; cb != nil && !c.deliberate.Load() {
					cb()
				}
			})
		}
	}()
	select {
	case <-c.readDone:
	case <-time.After(mcpReadJoinTimeout):
	}
	werr := c.cmd.Wait()
	c.exitOnce.Do(func() {
		c.failPending(fmt.Errorf("server process exited: %v", werr))
		c.markClosed()
		c.writeMu.Lock()
		c.closeErr = werr
		c.writeMu.Unlock()
		// بستن stdin باعث EOF خواننده‌ها می‌شود؛ درخت هم دفاعاً بسته می‌شود
		// (KILL_ON_JOB_CLOSE ویندوز / گروه فرایند یونیکس قبلاً سیگنال خورده‌اند).
		_ = c.stdin.Close()
		c.tree.close()
		// خروج غیرمنتظره → خبر به manager (حذف از نقشه + شمارش backoff).
		// توقف عامدانه (stop/kill از سمت ما) علامت خورده و callback نمی‌خورد.
		if cb := c.onExit; cb != nil && !c.deliberate.Load() {
			cb()
		}
	})
}

// drainStderr خواندن کران‌دار stderr با بافر حلقه‌ای ۶۴KB و لاگ نرخ‌محدود.
func (c *mcpClient) drainStderr(r io.ReadCloser) {
	defer r.Close()
	ring := make([]byte, 0, mcpMaxStderrRing)
	buf := make([]byte, 4*1024)
	var lastLog time.Time
	for {
		n, err := r.Read(buf)
		if n > 0 {
			add := buf[:n]
			if len(ring)+len(add) > mcpMaxStderrRing {
				over := len(ring) + len(add) - mcpMaxStderrRing
				if over >= len(ring) {
					ring = ring[:0]
				} else {
					ring = ring[over:]
				}
				ring = append(ring, []byte("\n…[older stderr dropped]\n")...)
			}
			ring = append(ring, add...)
			// نرخ‌محدود: حداکثر یکبار در هر ۲ ثانیه
			if time.Since(lastLog) >= mcpStderrLogEvery {
				lastLog = time.Now()
				if c.app != nil {
					logAppErrorFor(c.app, "mcp", c.name+" stderr: "+cutUTF8(string(ring), 400))
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// kill درخت فرایند را SIGTERM و بعد از مهلت کوتاه SIGKILL می‌کند؛ دیدنِ
// خروج و آزادسازی pendingها کار goroutine waitExit است (از start).
func (c *mcpClient) kill() {
	c.tree.terminate()
	time.AfterFunc(shellGracePeriod, func() { safeGo(c.app, "mcp.kill", c.tree.kill) })
}

// shutdownLike kill + بستن stdin (برای توقف عامدانه).
func (c *mcpClient) stop() {
	c.deliberate.Store(true) // توقف از سمت ما — onExit/backoff بی‌معناست
	c.markClosed()
	_ = c.stdin.Close()
	c.tree.terminate()
	time.AfterFunc(shellGracePeriod, func() { safeGo(c.app, "mcp.kill", c.tree.kill) })
}

// ---------------------------------------------------------------- نام‌گذاری ابزارها

// mcpSanitizeName نویسه‌های خارج از [A-Za-z0-9_-] را به «_» تبدیل می‌کند.
func mcpSanitizeName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// mcpToolFullName نام کامل ابزار برای مدل: mcp_<server>_<tool> — حداکثر ۶۴
// کاراکتر؛ سرریز/برخورد با هش کوتاهِ پایدار از «server/tool» حل می‌شود.
func mcpToolFullName(server, tool string) string {
	base := "mcp_" + mcpSanitizeName(server) + "_" + mcpSanitizeName(tool)
	if len(base) <= 64 {
		return base
	}
	return mcpTruncHash(server, tool)
}

// mcpTruncHash برش نام به جایی که هش جا شود + هش FNV-1a مبنای ۳۶.
func mcpTruncHash(server, tool string) string {
	h := fnvHash36("mcp\x00" + server + "\x00" + tool)
	suffix := "_" + h
	budget := 64 - len(suffix)
	base := "mcp_" + mcpSanitizeName(server) + "_" + mcpSanitizeName(tool)
	if len(base) > budget {
		base = base[:budget]
	}
	return base + suffix
}

// fnvHash36 هش کوتاهِ پایدار FNV-1a در مبنای ۳۶ — برای پسوند نام ابزارهای
// MCP با نام بلند/برخوردی (قطعیت: ورودی یکسان → خروجی یکسان).
func fnvHash36(s string) string {
	h := fnv.New64a()
	h.Write([]byte(s))
	return strconv.FormatUint(h.Sum64(), 36)
}
