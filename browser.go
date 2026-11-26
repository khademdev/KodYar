// برآورد بصری برای ایجنت (P0-4): سه ابزار فقط‌خواندنی روی یک کروم/اج
// نصب‌شدهٔ سیستم در حالت headless، از طریق Chrome DevTools Protocol.
//
//   - browser_open:       باز کردن آدرس و گزارش عنوان/وضعیت بارگذاری
//   - browser_console:    پیام‌های کنسول، استثناها و شکست‌های شبکه
//   - browser_screenshot: تصویر PNG در workspace (پیش‌فرض screenshots/)
//
// تضمین‌ها:
//   - هیچ دانلود/باندل مرورگری نیست؛ اگر کروم/اج/کرومیوم نباشد پیام روشن و
//     راهنمای نصب برمی‌گردد (و ابزار هرگز ادعای اسکرین‌شات نمی‌کند).
//   - فقط http(s) پذیرفته می‌شود (file:/javascript: رد می‌شوند).
//   - یک مرورگر به‌ازای هر نشست (start-once) روی یک تب؛ هنگام خروج برنامه و
//     با browser_shutdown بسته می‌شود. user-data-dir موقت است.
//   - اسکرین‌شات فقط داخل workspace نوشته می‌شود (مسیر بیرون/غیر-png رد).
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	browserStartTimeout = 30 * time.Second
	browserCallTimeout  = 25 * time.Second
	browserLoadTimeout  = 20 * time.Second
	browserMaxEvents    = 400
	browserMaxTextLen   = 4000
	browserDefaultW     = 1280
	browserDefaultH     = 800
)

// ---------------------------------------------------------------- CDP

type cdpEvent struct {
	Method string
	Params json.RawMessage
}

type cdpReply struct {
	Result json.RawMessage
	Err    string
}

// cdpSession یک اتصال CDP به یک target (تب) مرورگر.
type cdpSession struct {
	ws *wsConn

	mu      sync.Mutex
	nextID  int
	waiters map[int]chan cdpReply

	evMu   sync.Mutex
	events []cdpEvent
	loadCh chan struct{}

	done      chan struct{}
	closeOnce sync.Once
	readErr   error
}

func newCDPSession(ws *wsConn) *cdpSession {
	s := &cdpSession{
		ws:      ws,
		waiters: map[int]chan cdpReply{},
		loadCh:  make(chan struct{}, 8),
		done:    make(chan struct{}),
	}
	go s.readLoop()
	return s
}

func (s *cdpSession) readLoop() {
	defer s.shutdown(nil)
	for {
		raw, err := s.ws.ReadMessage()
		if err != nil {
			s.shutdown(err)
			return
		}
		var msg struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		if msg.ID != nil {
			s.mu.Lock()
			ch := s.waiters[*msg.ID]
			delete(s.waiters, *msg.ID)
			s.mu.Unlock()
			if ch != nil {
				rep := cdpReply{Result: msg.Result}
				if msg.Error != nil {
					rep.Err = fmt.Sprintf("%s (code %d)", msg.Error.Message, msg.Error.Code)
				}
				ch <- rep
			}
			continue
		}
		if msg.Method == "" {
			continue
		}
		s.evMu.Lock()
		s.events = append(s.events, cdpEvent{Method: msg.Method, Params: msg.Params})
		if over := len(s.events) - browserMaxEvents; over > 0 {
			s.events = append(s.events[:0], s.events[over:]...)
		}
		s.evMu.Unlock()
		if msg.Method == "Page.loadEventFired" {
			select {
			case s.loadCh <- struct{}{}:
			default:
			}
		}
	}
}

func (s *cdpSession) shutdown(err error) {
	s.closeOnce.Do(func() {
		s.readErr = err
		close(s.done)
		_ = s.ws.CloseAbrupt()
		s.mu.Lock()
		for id, ch := range s.waiters {
			delete(s.waiters, id)
			close(ch)
		}
		s.mu.Unlock()
	})
}

// call یک متد CDP صدا می‌زند و نتیجه را برمی‌گرداند.
func (s *cdpSession) call(method string, params interface{}, out interface{}) error {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	ch := make(chan cdpReply, 1)
	s.waiters[id] = ch
	s.mu.Unlock()

	payload, err := json.Marshal(map[string]interface{}{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if err := s.ws.WriteText(payload); err != nil {
		s.shutdown(err)
		return fmt.Errorf("browser connection lost: %w", err)
	}
	select {
	case rep, ok := <-ch:
		if !ok {
			return fmt.Errorf("browser connection closed%s", s.errSuffix())
		}
		if rep.Err != "" {
			return fmt.Errorf("%s: %s", method, rep.Err)
		}
		if out != nil && len(rep.Result) > 0 {
			return json.Unmarshal(rep.Result, out)
		}
		return nil
	case <-time.After(browserCallTimeout):
		return fmt.Errorf("%s timed out after %s", method, browserCallTimeout)
	}
}

func (s *cdpSession) errSuffix() string {
	if s.readErr != nil {
		return ": " + s.readErr.Error()
	}
	return ""
}

// waitLoad تا Page.loadEventFired یا مهلت صبر می‌کند.
func (s *cdpSession) waitLoad(timeout time.Duration) bool {
	// سیگنال‌های قدیمی را دور بریز
	for {
		select {
		case <-s.loadCh:
			continue
		default:
		}
		break
	}
	select {
	case <-s.loadCh:
		return true
	case <-s.done:
		return false
	case <-time.After(timeout):
		return false
	}
}

// takeEvents رویدادهای ذخیره‌شده را برمی‌گرداند (clear=true خالی می‌کند).
func (s *cdpSession) takeEvents(clear bool) []cdpEvent {
	s.evMu.Lock()
	defer s.evMu.Unlock()
	out := append([]cdpEvent(nil), s.events...)
	if clear {
		s.events = nil
	}
	return out
}

// ---------------------------------------------------------------- مرورگر

type browserSession struct {
	proc     *exec.Cmd
	tree     *procTree // درخت فرایند مرورگر (کودکان هم می‌میرند تا پوشهٔ موقت آزاد شود)
	conn     *cdpSession
	dir      string
	port     string
	wsURL    string
	waitDone chan struct{} // پس از خروج واقعی proc بسته می‌شود
	closeOne sync.Once
}

// browserExecutableCandidates مسیرهای متعارف کروم/اج/کرومیوم.
func browserExecutableCandidates() []string {
	var out []string
	if p := strings.TrimSpace(os.Getenv("KODYAR_BROWSER_PATH")); p != "" {
		out = append(out, p)
	}
	if p := strings.TrimSpace(os.Getenv("CHROME_PATH")); p != "" {
		out = append(out, p)
	}
	switch runtime.GOOS {
	case "windows":
		var roots []string
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			if v := os.Getenv(env); v != "" {
				roots = append(roots, v)
			}
		}
		for _, r := range roots {
			out = append(out,
				filepath.Join(r, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(r, "Microsoft", "Edge", "Application", "msedge.exe"),
				filepath.Join(r, "Chromium", "Application", "chrome.exe"),
			)
		}
	case "darwin":
		out = append(out,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		)
	default:
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge"} {
			if p, err := exec.LookPath(name); err == nil {
				out = append(out, p)
			}
		}
	}
	return out
}

// findBrowserExecutable اولین مرورگر موجود را برمی‌گرداند.
func findBrowserExecutable() (string, error) {
	for _, p := range browserExecutableCandidates() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("no Chrome, Edge or Chromium installation found; install one, or set KODYAR_BROWSER_PATH to its executable")
}

// parseDevToolsURL خط «DevTools listening on ws://…» را از stderr می‌خواند.
func parseDevToolsURL(line string) (string, bool) {
	const tag = "DevTools listening on "
	i := strings.Index(line, tag)
	if i < 0 {
		return "", false
	}
	u := strings.TrimSpace(line[i+len(tag):])
	if !strings.HasPrefix(u, "ws://") && !strings.HasPrefix(u, "wss://") {
		return "", false
	}
	return u, true
}

// httpJSON یک GET/PUT با مهلت انجام می‌دهد و JSON را decode می‌کند.
func httpJSON(method, addr string, out interface{}) error {
	req, err := http.NewRequest(method, addr, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

type devtoolsTarget struct {
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerUrl string `json:"webSocketDebuggerUrl"`
}

// devToolsURLFromPortFile آدرس WebSocket مرورگر را از فایل DevToolsActivePort
// می‌سازد (خط اول پورت، خط دوم مسیر /devtools/browser/…). این قرارداد پایدار
// CDP است و به نسخهٔ کروم وابسته نیست.
func devToolsURLFromPortFile(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		return ""
	}
	port := strings.TrimSpace(lines[0])
	path := strings.TrimSpace(lines[1])
	if port == "" || !strings.HasPrefix(path, "/devtools/") {
		return ""
	}
	return "ws://127.0.0.1:" + port + path
}

// startBrowser مرورگر headless را اجرا و به تب about:blank وصل می‌شود.
func startBrowser() (*browserSession, error) {
	exe, err := findBrowserExecutable()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "kodyar-browser-*")
	if err != nil {
		return nil, err
	}
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-background-networking",
		"--hide-scrollbars",
		"--mute-audio",
		"--remote-debugging-port=0",
		"--user-data-dir=" + dir,
		fmt.Sprintf("--window-size=%d,%d", browserDefaultW, browserDefaultH),
		"about:blank",
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, exe, args...)
	hideConsoleWindow(cmd) // بدون این، ویندوز پنجرهٔ سیاه مرورگر را چشمک می‌زند
	// درخت فرایند: کروم چند کودک (renderer/gpu) می‌سازد که فایل‌های
	// user-data-dir را باز نگه می‌دارند؛ بدون کشتن آن‌ها RemoveAll ویندوز شکست می‌خورد.
	tree, _ := prepareProcessTree(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		os.RemoveAll(dir)
		return nil, err
	}
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		cancel()
		os.RemoveAll(dir)
		return nil, fmt.Errorf("start browser: %w", err)
	}
	sess := &browserSession{proc: cmd, tree: tree, dir: dir, waitDone: make(chan struct{})}
	if tree != nil {
		_ = tree.attach(cmd) // خطا مانع ادامه نیست؛ فقط بازگشت به Kill ساده
	}
	// مرگ مرورگر نباید فرزندی جامانده بگذارد؛ waitDone به Close اجازه می‌دهد
	// تا خروج واقعی صبر کند و بعد پوشهٔ موقت را پاک کند.
	go func() {
		defer close(sess.waitDone)
		_ = cmd.Wait()
		cancel()
	}()

	devURL := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			if u, ok := parseDevToolsURL(sc.Text()); ok {
				select {
				case devURL <- u:
				default:
				}
			}
		}
	}()

	// دو مسیر موازی: نسخه‌های قدیمی‌تر کروم «DevTools listening on ws://…»
	// را روی stderr چاپ می‌کنند؛ نسخه‌های تازه فقط فایل DevToolsActivePort را
	// می‌نویسند. هر کدام زودتر رسید، همان است (وگرنه ۳۰ ثانیه تایم‌اوت).
	var wsURL string
	timeout := time.NewTimer(browserStartTimeout)
	defer timeout.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
waitStart:
	for {
		select {
		case wsURL = <-devURL:
			break waitStart
		case <-ticker.C:
			if u := devToolsURLFromPortFile(dir); u != "" {
				wsURL = u
				break waitStart
			}
		case <-timeout.C:
			sess.Close()
			return nil, fmt.Errorf("browser did not report a DevTools endpoint within %s (is it running with --remote-debugging enabled?)", browserStartTimeout)
		}
	}
	sess.wsURL = wsURL

	// پورت را از ws://127.0.0.1:PORT/... بگیر و تب را انتخاب کن.
	hostPort := strings.TrimPrefix(wsURL, "ws://")
	if i := strings.Index(hostPort, "/"); i >= 0 {
		hostPort = hostPort[:i]
	}
	if i := strings.LastIndex(hostPort, ":"); i >= 0 {
		sess.port = hostPort[i+1:]
	}
	var targets []devtoolsTarget
	deadline := time.Now().Add(browserStartTimeout)
	var pageWS string
	for time.Now().Before(deadline) {
		targets = nil
		if httpJSON(http.MethodGet, "http://127.0.0.1:"+sess.port+"/json/list", &targets) == nil {
			for _, tg := range targets {
				if tg.Type == "page" && tg.WebSocketDebuggerUrl != "" {
					pageWS = tg.WebSocketDebuggerUrl
					break
				}
			}
		}
		if pageWS != "" {
			break
		}
		// اگر تبی نبود، خودمان یکی بساز.
		var created devtoolsTarget
		if httpJSON(http.MethodPut, "http://127.0.0.1:"+sess.port+"/json/new?url=about:blank", &created) == nil && created.WebSocketDebuggerUrl != "" {
			pageWS = created.WebSocketDebuggerUrl
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if pageWS == "" {
		sess.Close()
		return nil, fmt.Errorf("browser started but exposed no page target to attach to")
	}
	ws, err := wsDial(pageWS, 10*time.Second)
	if err != nil {
		sess.Close()
		return nil, fmt.Errorf("attach to browser: %w", err)
	}
	sess.conn = newCDPSession(ws)
	for _, d := range []string{"Page.enable", "Runtime.enable", "Log.enable", "Network.enable"} {
		_ = sess.conn.call(d, map[string]interface{}{}, nil)
	}
	return sess, nil
}

// Close مرورگر را می‌بندد و پوشهٔ موقت را پاک می‌کند. هر بار به‌طور کران‌دار
// برمی‌گردد: قتل پروسه، انتظار حداکثر ۳ ثانیه برای خروج، و پاک‌سازی با تلاش
// مجدد (ویندوز تا لحظه‌ای فایل‌های user-data-dir را قفل نگه می‌دارد).
// Close idempotent است و هرگز بیش از چند ثانیه معطل نمی‌ماند.
func (b *browserSession) Close() {
	if b == nil {
		return
	}
	b.closeOne.Do(func() {
		if b.conn != nil {
			b.conn.shutdown(nil)
			b.conn = nil
		}
		if b.tree != nil {
			b.tree.kill() // کل درخت، نه فقط پروسهٔ اصلی
		} else if b.proc != nil && b.proc.Process != nil {
			_ = b.proc.Process.Kill()
		}
		if b.waitDone != nil {
			select {
			case <-b.waitDone:
			case <-time.After(3 * time.Second):
				// پروسه پاسخ نداد؛ پاک‌سازی با تلاش مجدد ادامه می‌یابد
			}
		}
		if b.tree != nil {
			b.tree.close()
			b.tree = nil
		}
		b.proc = nil
		if b.dir != "" {
			removeBrowserTempDir(b.dir)
			b.dir = ""
		}
	})
}

// removeBrowserTempDir پوشهٔ موقت را با تلاش مجدد پاک می‌کند (قفل کوتاه
// ویندوز روی فایل‌های Chrome). حداکثر ~۳ ثانیه، هرگز بی‌پایان.
func removeBrowserTempDir(dir string) {
	for i := 0; i < 15; i++ {
		if err := os.RemoveAll(dir); err == nil {
			if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	// آخرین تلاش در پس‌زمینه تا خروج برنامه بیش از این معطل نشود.
	go func() {
		for i := 0; i < 10; i++ {
			time.Sleep(500 * time.Millisecond)
			if err := os.RemoveAll(dir); err == nil {
				if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
					return
				}
			}
		}
	}()
}

// browserEnsure یک مرورگر را start-once نگه می‌دارد؛ شکست = پیام روشن.
func (a *App) browserEnsure() (*cdpSession, error) {
	a.browserMu.Lock()
	defer a.browserMu.Unlock()
	if a.browser != nil {
		select {
		case <-a.browser.conn.done:
			a.browser.Close()
			a.browser = nil
		default:
			return a.browser.conn, nil
		}
	}
	sess, err := startBrowser()
	if err != nil {
		return nil, err
	}
	a.browser = sess
	return sess.conn, nil
}

// browserShutdown مرورگر را می‌بندد (خروج برنامه/دکمهٔ توقف).
func (a *App) browserShutdown() {
	a.browserMu.Lock()
	defer a.browserMu.Unlock()
	if a.browser != nil {
		a.browser.Close()
		a.browser = nil
	}
}

// ---------------------------------------------------------------- ابزارها

// browserNavigate به آدرس می‌رود و منتظر load می‌ماند.
func browserNavigate(s *cdpSession, rawURL string) (bool, error) {
	var nav struct {
		ErrorText string `json:"errorText"`
	}
	if err := s.call("Page.navigate", map[string]interface{}{"url": rawURL}, &nav); err != nil {
		return false, err
	}
	if nav.ErrorText != "" {
		return false, fmt.Errorf("navigation failed: %s", nav.ErrorText)
	}
	return s.waitLoad(browserLoadTimeout), nil
}

// browserEval رشته را در صفحه ارزیابی می‌کند.
func browserEval(s *cdpSession, expr string) (string, error) {
	var res struct {
		Result struct {
			Value interface{} `json:"value"`
			Type  string      `json:"type"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := s.call("Runtime.evaluate", map[string]interface{}{
		"expression": expr, "returnByValue": true, "awaitPromise": true,
	}, &res); err != nil {
		return "", err
	}
	if res.ExceptionDetails != nil {
		return "", fmt.Errorf("page script error: %s", res.ExceptionDetails.Text)
	}
	if res.Result.Value == nil {
		return "", nil
	}
	if v, ok := res.Result.Value.(string); ok {
		return v, nil
	}
	return fmt.Sprintf("%v", res.Result.Value), nil
}

// toolBrowserOpen آدرس را باز می‌کند و عنوان/readyState/طول متن را می‌دهد.
func (a *App) toolBrowserOpen(rawURL string, width, height int) (string, bool) {
	rawURL = strings.TrimSpace(rawURL)
	if !wsIsLocalURL(rawURL) {
		return "ERROR: browser_open accepts only http(s) URLs (got " + cutUTF8(rawURL, 120) + ").", false
	}
	s, err := a.browserEnsure()
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	if width <= 0 {
		width = browserDefaultW
	}
	if height <= 0 {
		height = browserDefaultH
	}
	_ = s.call("Emulation.setDeviceMetricsOverride", map[string]interface{}{
		"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false,
	}, nil)
	loaded, err := browserNavigate(s, rawURL)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	title, _ := browserEval(s, "document.title")
	href, _ := browserEval(s, "location.href")
	state, _ := browserEval(s, "document.readyState")
	textLen, _ := browserEval(s, "document.body ? document.body.innerText.trim().length : 0")
	var b strings.Builder
	fmt.Fprintf(&b, "OK: opened %s (%dx%d)\nurl: %s\ntitle: %s\nreadyState: %s\nvisible text: %s chars\n",
		rawURL, width, height, href, cutUTF8(title, 200), state, textLen)
	if !loaded {
		fmt.Fprintf(&b, "note: no load event within %s; the page may still be loading.\n", browserLoadTimeout)
	}
	b.WriteString("Next: browser_console for errors, browser_screenshot for the rendered image.")
	return b.String(), true
}

// toolBrowserConsole پیام‌های کنسول/استثناها/خطاهای شبکه را برمی‌گرداند.
func (a *App) toolBrowserConsole(rawURL string, clear bool) (string, bool) {
	// اعتبارسنجی آدرس قبل از هر کار با مرورگر: ورودی غیر-http(s) باید همیشه
	// همین پیام را بدهد، حتی اگر مرورگری نصب نباشد.
	rawURL = strings.TrimSpace(rawURL)
	if rawURL != "" && !wsIsLocalURL(rawURL) {
		return "ERROR: browser_console accepts only http(s) URLs.", false
	}
	s, err := a.browserEnsure()
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	if rawURL != "" {
		if _, err := browserNavigate(s, rawURL); err != nil {
			return "ERROR: " + err.Error(), false
		}
	}
	// چند لحظه فرصت برای پیام‌های async (fetch/render).
	time.Sleep(300 * time.Millisecond)
	events := s.takeEvents(clear)
	var lines []string
	for _, ev := range events {
		switch ev.Method {
		case "Runtime.consoleAPICalled":
			var e struct {
				Type string `json:"type"`
				Args []struct {
					Value       interface{} `json:"value"`
					Description string      `json:"description"`
					Type        string      `json:"type"`
				} `json:"args"`
			}
			if json.Unmarshal(ev.Params, &e) != nil {
				continue
			}
			var parts []string
			for _, a := range e.Args {
				if a.Value != nil {
					parts = append(parts, fmt.Sprintf("%v", a.Value))
				} else if a.Description != "" {
					parts = append(parts, a.Description)
				} else {
					parts = append(parts, a.Type)
				}
			}
			lines = append(lines, fmt.Sprintf("[console.%s] %s", e.Type, cutUTF8(strings.Join(parts, " "), browserMaxTextLen)))
		case "Runtime.exceptionThrown":
			var e struct {
				ExceptionDetails struct {
					Text      string `json:"text"`
					Exception struct {
						Description string `json:"description"`
					} `json:"exception"`
				} `json:"exceptionDetails"`
			}
			if json.Unmarshal(ev.Params, &e) != nil {
				continue
			}
			msg := e.ExceptionDetails.Text
			if e.ExceptionDetails.Exception.Description != "" {
				msg = e.ExceptionDetails.Exception.Description
			}
			lines = append(lines, "[pageerror] "+cutUTF8(msg, browserMaxTextLen))
		case "Log.entryAdded":
			var e struct {
				Entry struct {
					Level  string `json:"level"`
					Text   string `json:"text"`
					URL    string `json:"url"`
					Source string `json:"source"`
				} `json:"entry"`
			}
			if json.Unmarshal(ev.Params, &e) != nil {
				continue
			}
			loc := ""
			if e.Entry.URL != "" {
				loc = " (" + e.Entry.URL + ")"
			}
			lines = append(lines, fmt.Sprintf("[log.%s] %s%s", e.Entry.Level, cutUTF8(e.Entry.Text, browserMaxTextLen), loc))
		case "Network.loadingFailed":
			var e struct {
				ErrorText string `json:"errorText"`
				Canceled  bool   `json:"canceled"`
				Type      string `json:"type"`
			}
			if json.Unmarshal(ev.Params, &e) != nil || e.Canceled {
				continue
			}
			lines = append(lines, fmt.Sprintf("[network] %s request failed: %s", e.Type, e.ErrorText))
		case "Network.responseReceived":
			var e struct {
				Response struct {
					URL      string `json:"url"`
					Status   int    `json:"status"`
					MimeType string `json:"mimeType"`
				} `json:"response"`
			}
			if json.Unmarshal(ev.Params, &e) != nil || e.Response.Status < 400 {
				continue
			}
			lines = append(lines, fmt.Sprintf("[http %d] %s", e.Response.Status, cutUTF8(e.Response.URL, 300)))
		}
	}
	if len(lines) == 0 {
		return "No console messages, page errors or failed requests were captured. Run browser_open (or pass url here) and try again.", true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d browser message(s):\n", len(lines))
	for _, l := range lines {
		b.WriteString("- ")
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String(), true
}

// toolBrowserScreenshot از صفحهٔ جاری (یا آدرس داده‌شده) PNG می‌گیرد و داخل
// workspace ذخیره می‌کند.
func (a *App) toolBrowserScreenshot(rawURL string, fullPage bool, relPath string) (string, bool) {
	// همان ترتیب browser_console: رد آدرس غیر-http(s) پیش از راه‌اندازی مرورگر.
	rawURL = strings.TrimSpace(rawURL)
	if rawURL != "" && !wsIsLocalURL(rawURL) {
		return "ERROR: browser_screenshot accepts only http(s) URLs.", false
	}
	s, err := a.browserEnsure()
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	if rawURL != "" {
		if _, err := browserNavigate(s, rawURL); err != nil {
			return "ERROR: " + err.Error(), false
		}
	}
	var shot struct {
		Data string `json:"data"`
	}
	params := map[string]interface{}{"format": "png", "fromSurface": true}
	if fullPage {
		var metrics struct {
			CSSContentSize struct {
				Width  float64 `json:"width"`
				Height float64 `json:"height"`
			} `json:"cssContentSize"`
		}
		if err := s.call("Page.getLayoutMetrics", map[string]interface{}{}, &metrics); err != nil {
			return "ERROR: " + err.Error(), false
		}
		w, h := metrics.CSSContentSize.Width, metrics.CSSContentSize.Height
		if w <= 0 || h <= 0 {
			w, h = browserDefaultW, browserDefaultH
		}
		// سقف ۱۶هزار پیکسل ارتفاع تا تصویر غول‌آسا نشود.
		if h > 16000 {
			h = 16000
		}
		params["clip"] = map[string]interface{}{"x": 0, "y": 0, "width": w, "height": h, "scale": 1}
		params["captureBeyondViewport"] = true
	}
	if err := s.call("Page.captureScreenshot", params, &shot); err != nil {
		return "ERROR: " + err.Error(), false
	}
	data, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil || len(data) == 0 {
		return "ERROR: the browser returned no image data.", false
	}
	path, err := a.browserShotPath(relPath)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "ERROR: " + err.Error(), false
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "ERROR: " + err.Error(), false
	}
	a.mu.Lock()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()
	rel, rerr := filepath.Rel(ws, path)
	if rerr != nil {
		rel = path
	}
	pageURL, _ := browserEval(s, "location.href")
	a.mu.Lock()
	model := strings.TrimSpace(a.settings.Model)
	mode := a.settings.VisionImages
	a.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "OK: screenshot saved to %s (%d bytes, %s)\nurl: %s\n",
		filepath.ToSlash(rel), len(data), map[bool]string{true: "full page", false: "viewport"}[fullPage], pageURL)
	if visionImagesEnabled(mode, model) {
		// حلقه این خط را جدا می‌کند و PNG را واقعاً به پیام مدل ضمیمه می‌کند.
		b.WriteString("The image is attached to this turn: look at the picture itself for layout, colors and overflow.\n")
		b.WriteString(shotMarkerPrefix + path + "\n")
	} else {
		label := model
		if label == "" {
			label = "the selected model"
		}
		fmt.Fprintf(&b, "This model (%s) has no image input, so you CANNOT see this screenshot — only the user can open the file. Do not describe the image from imagination; use browser_console output or ask the user.\n", label)
	}
	return b.String(), true
}

// browserShotPath مسیر نهایی PNG را داخل workspace اعتبارسنجی می‌کند.
func (a *App) browserShotPath(relPath string) (string, error) {
	a.mu.Lock()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()
	if ws == "" {
		return "", fmt.Errorf("no workspace folder is open")
	}
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		relPath = filepath.Join("screenshots", time.Now().Format("shot-20060102-150405")+".png")
	}
	// مستقل از سیستم‌عامل: مسیر مطلق POSIX، مسیر مطلق ویندوزی (C:\… یا
	// \\server\…) و هر جداکننده‌ی ویندوزی رد می‌شود — وگرنه همان مسیر روی
	// لینوکس به‌عنوان نام فایل نسبی پذیرفته می‌شد (باگ کشف‌شده در CI).
	if filepath.IsAbs(relPath) || strings.HasPrefix(relPath, "/") || strings.HasPrefix(relPath, "\\") ||
		strings.Contains(relPath, "\\") || strings.Contains(relPath, ":") {
		return "", fmt.Errorf("screenshot path must be relative to the workspace (use forward slashes)")
	}
	full := filepath.Join(ws, relPath)
	rel, err := filepath.Rel(ws, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("screenshot path must stay inside the workspace")
	}
	if !strings.EqualFold(filepath.Ext(full), ".png") {
		return "", fmt.Errorf("screenshot path must end in .png")
	}
	return full, nil
}
