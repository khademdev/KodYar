// KodYar — کلاینت LSP بسیار سبک (JSON-RPC روی stdio) برای ابزار diagnostics.
//
// طراحی (کمترین دامنه، عمداً):
//   - یک فرایند سرور برای هر زبان، «یک‌بار» در طول عمر برنامه بالا می‌آید
//     (diagManager) و در shutdown با stopAll کشته می‌شود.
//   - فقط چیزی که برای گرفتن خطا لازم است: initialize/initialized،
//     textDocument/didOpen، و گوش‌دادن به publishDiagnostics.
//   - درخواست‌های سرور→کلاینت (مثل window/workDoneProgress/create) با
//     نتیجهٔ null پاسخ داده می‌شوند تا سرور معطل نماند.
//   - هیچ قفلِ برنامه (a.mu و…) در طول I/O نگه داشته نمی‌شود؛ همهٔ انتظارها
//     کران‌دار و قابل‌لغو با خروج فرایند هستند.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// lspInitTimeout مهلت initialize هر سرور.
	lspInitTimeout = 20 * time.Second
	// lspCallTimeout مهلت هر درخواست غیر-تشخیصی.
	lspCallTimeout = 10 * time.Second
	// lspDiagSettle مکث کوتاه پس از اولین publishDiagnostics تا انتشارهای
	// بعدی (gopls چند مرحله‌ای publish می‌کند) هم برسند.
	lspDiagSettle = 250 * time.Millisecond
	// lspStopTimeout مهلت خاموشی نرم پیش از kill.
	lspStopTimeout = 500 * time.Millisecond
)

// ---------------------------------------------------------------- پیام‌ها

type lspError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type lspMessage struct {
	ID     *int            `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *lspError       `json:"error"`
}

type lspPos struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPos `json:"start"`
	End   lspPos `json:"end"`
}

type lspDiagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"` // 1=Error 2=Warning 3=Info 4=Hint
	Source   string   `json:"source"`
	Message  string   `json:"message"`
}

type lspPublishParams struct {
	URI         string          `json:"uri"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
}

// ---------------------------------------------------------------- کلاینت

type lspClient struct {
	name string

	cmd   *exec.Cmd
	stdin io.WriteCloser

	writeMu sync.Mutex // سریال‌سازی نوشتن روی stdin

	mu      sync.Mutex
	nextID  int
	pending map[int]chan lspMessage
	diags   map[string][]lspDiagnostic
	waiters map[string][]chan struct{}
	closed  bool

	done chan struct{} // با خروج/خطای سرور بسته می‌شود
}

// newLSPClient ساختار کلاینت را بدون فرایند می‌سازد (تست‌پذیر).
func newLSPClient(name string) *lspClient {
	return &lspClient{
		name:    name,
		pending: map[int]chan lspMessage{},
		diags:   map[string][]lspDiagnostic{},
		waiters: map[string][]chan struct{}{},
		done:    make(chan struct{}),
	}
}

// startLSPClient سرور را اجرا می‌کند، خواننده را بالا می‌آورد و initialize
// را انجام می‌دهد. در خطا فرایند کشته می‌شود (بدون سرور زامبی).
func startLSPClient(name, bin string, args []string, rootDir string) (*lspClient, error) {
	cmd := exec.Command(bin, args...)
	// بدون پنجرهٔ کنسول سیاه روی ویندوز (قرارداد consolehide_test).
	hideConsoleWindow(cmd)
	cmd.Dir = rootDir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	// stderr دور ریخته می‌شود؛ سرورهای LSP آن را برای لاگ استفاده می‌کنند.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}

	c := newLSPClient(name)
	c.cmd = cmd
	c.stdin = stdin
	go c.readLoop(stdout)

	rootURI := fileURI(rootDir)
	if _, err := c.call("initialize", map[string]interface{}{
		"processId":    nil,
		"rootUri":      rootURI,
		"capabilities": map[string]interface{}{},
	}, lspInitTimeout); err != nil {
		c.stop()
		return nil, fmt.Errorf("%s initialize failed: %w", name, err)
	}
	c.notify("initialized", map[string]interface{}{})
	return c, nil
}

// fileURI مسیر مطلق را به URI فایل تبدیل می‌کند (ویندوز: file:///D:/x).
func fileURI(abs string) string {
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func (c *lspClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// ---------------------------------------------------------------- I/O

// send یک پیام JSON-RPC را با فریم Content-Length می‌نویسد.
func (c *lspClient) send(payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

func (c *lspClient) notify(method string, params interface{}) {
	_ = c.send(map[string]interface{}{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *lspClient) replyNull(id int) {
	_ = c.send(map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": nil})
}

// call یک درخواست می‌فرستد و تا پاسخ/پایان سرور/مهلت منتظر می‌ماند.
func (c *lspClient) call(method string, params interface{}, timeout time.Duration) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("language server is not running")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan lspMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.send(map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case msg := <-ch:
		if msg.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, msg.Error.Message)
		}
		return msg.Result, nil
	case <-c.done:
		return nil, fmt.Errorf("%s: language server exited", method)
	case <-timer.C:
		return nil, fmt.Errorf("%s: timeout", method)
	}
}

// readHeader هدرهای یک پیام را می‌خواند و Content-Length را برمی‌گرداند.
func readLSPHeader(br *bufio.Reader) (int, error) {
	length := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return 0, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if length < 0 {
				return 0, errors.New("missing Content-Length")
			}
			return length, nil
		}
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
			n, perr := strconv.Atoi(strings.TrimSpace(v))
			if perr != nil || n <= 0 {
				return 0, fmt.Errorf("invalid Content-Length %q", v)
			}
			length = n
		}
	}
}

// readLoop پیام‌های سرور را می‌خواند تا EOF/خطا.
func (c *lspClient) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		n, err := readLSPHeader(br)
		if err != nil {
			c.fail()
			return
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(br, body); err != nil {
			c.fail()
			return
		}
		var msg lspMessage
		if json.Unmarshal(body, &msg) != nil {
			continue
		}
		switch {
		case msg.Method == "textDocument/publishDiagnostics":
			var p lspPublishParams
			if json.Unmarshal(msg.Params, &p) == nil {
				c.setDiags(p.URI, p.Diagnostics)
			}
		case msg.ID != nil && msg.Method != "":
			// درخواست سرور→کلاینت: پاسخ خالی تا سرور معطل نماند.
			c.replyNull(*msg.ID)
		case msg.ID != nil:
			c.deliver(*msg.ID, msg)
		}
	}
}

func (c *lspClient) deliver(id int, msg lspMessage) {
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ch != nil {
		ch <- msg
	}
}

func (c *lspClient) setDiags(uri string, d []lspDiagnostic) {
	c.mu.Lock()
	c.diags[uri] = d
	ws := c.waiters[uri]
	delete(c.waiters, uri)
	c.mu.Unlock()
	for _, w := range ws {
		select {
		case w <- struct{}{}:
		default:
		}
	}
}

// fail کلاینت را مرده علامت می‌زند و همهٔ انتظارها را آزاد می‌کند.
func (c *lspClient) fail() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.done)
	c.mu.Unlock()
}

// ---------------------------------------------------------------- تشخیص‌ها

// openAndWait فایل را didOpen می‌کند و اولین انتشار تشخیص‌هایش را می‌گیرد.
func (c *lspClient) openAndWait(uri, languageID, text string, timeout time.Duration) ([]lspDiagnostic, error) {
	ch := make(chan struct{}, 1)
	c.mu.Lock()
	c.waiters[uri] = append(c.waiters[uri], ch)
	c.mu.Unlock()

	if err := c.send(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"uri":        uri,
				"languageId": languageID,
				"version":    1,
				"text":       text,
			},
		},
	}); err != nil {
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
	case <-c.done:
		return nil, errors.New("language server exited")
	case <-timer.C:
		return nil, fmt.Errorf("no diagnostics within %s", timeout)
	}
	// مکث کوتاه تا انتشارهای تکمیلی (gopls مرحله‌ای publish می‌کند).
	time.Sleep(lspDiagSettle)

	c.mu.Lock()
	out := append([]lspDiagnostic(nil), c.diags[uri]...)
	c.mu.Unlock()
	return out, nil
}

// stop خاموشی نرم و سپس kill — همیشه برگردد.
func (c *lspClient) stop() {
	if c.cmd == nil || c.cmd.Process == nil {
		c.fail()
		return
	}
	// shutdown/exit طبق پروتکل؛ بی‌صدا و کوتاه.
	if !c.isClosed() {
		_, _ = c.call("shutdown", nil, lspStopTimeout)
		c.notify("exit", nil)
	}
	_ = c.stdin.Close()
	wait := make(chan struct{})
	go func() { _ = c.cmd.Wait(); close(wait) }()
	select {
	case <-wait:
	case <-time.After(lspStopTimeout):
		_ = c.cmd.Process.Kill()
		<-wait
	}
	c.fail()
}

// ---------------------------------------------------------------- مدیر

// diagManager یک کلاینت به‌ازای هر زبان نگه می‌دارد (start-once).
type diagManager struct {
	mu      sync.Mutex
	clients map[string]*lspClient
}

// diagMgr مدیر سراسری تشخیص‌ها — در shutdown با stopAll بسته می‌شود.
var diagMgr = &diagManager{clients: map[string]*lspClient{}}

// lspLookup قابل تزریق در تست‌ها (پیش‌فرض exec.LookPath).
var lspLookup = exec.LookPath

// lspCommandFor دستور اجرای سرور زبان را برمی‌گرداند.
func lspCommandFor(lang string) (bin string, args []string, ok bool) {
	switch lang {
	case "go":
		if p, err := lspLookup("gopls"); err == nil {
			return p, nil, true
		}
	case "ts":
		if p, err := lspLookup("typescript-language-server"); err == nil {
			return p, []string{"--stdio"}, true
		}
	}
	return "", nil, false
}

// lspInstallHint راهنمای نصب سرور زبان (برای پیام ابزار).
func lspInstallHint(lang string) string {
	switch lang {
	case "go":
		return "gopls was not found: install it with `go install golang.org/x/tools/gopls@latest` (it must be on PATH)"
	case "ts":
		return "typescript-language-server was not found: install it with `npm i -g typescript-language-server typescript`"
	}
	return "no language server is configured for this file type"
}

// get کلاینت همان زبان را برمی‌گرداند و اگر نبود یک‌بار راه‌اندازی می‌کند.
func (m *diagManager) get(lang, rootDir string) (*lspClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.clients[lang]; ok && !c.isClosed() {
		return c, nil
	}
	bin, args, ok := lspCommandFor(lang)
	if !ok {
		return nil, errors.New(lspInstallHint(lang))
	}
	c, err := startLSPClient(lang, bin, args, rootDir)
	if err != nil {
		return nil, err
	}
	m.clients[lang] = c
	return c, nil
}

// stopAll همهٔ سرورها را می‌بندد (shutdown برنامه).
func (m *diagManager) stopAll() {
	m.mu.Lock()
	clients := make([]*lspClient, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.clients = map[string]*lspClient{}
	m.mu.Unlock()
	for _, c := range clients {
		c.stop()
	}
}
