// موتور چت استریمی KodYar.
//
// SendMessage پیام کاربر را به گفتگو اضافه می‌کند، به نقطه‌ی پایانی
// سازگار با OpenAI وصل می‌شود و هر تکه‌ی پاسخ را با رویداد «chat:delta»
// به فرانت‌اند می‌فرستد. پایان کار با «chat:done» یا خطا با «chat:error».
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// chatHTTPClient بدون Timeout کلی ساخته می‌شود تا پاسخ‌های استریمیِ بلند
// وسط کار قطع نشوند؛ کنترل مهلت با ResponseHeaderTimeout (اتصال و اولین
// بایت) و Context لغو کاربر (StopGeneration) انجام می‌شود.
// parseProxyURL آدرس پروکسی را نرمال می‌کند. اگر scheme نباشد، http فرض می‌شود.
func parseProxyURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("آدرس پروکسی خالی است")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("آدرس پروکسی نامعتبر است: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" {
		return nil, fmt.Errorf("scheme پروکسی پشتیبانی نمی‌شود (%s)؛ از http یا socks5 استفاده کنید", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("آدرس پروکسی باید شامل host:port باشد")
	}
	return u, nil
}

// resolveAppProxy پروکسی را از تنظیمات فعلی (appForLogs) می‌خواند.
// off → بدون پروکسی؛ custom → ProxyURL؛ auto/خالی → هوشمند (پروکسی سیستم
// ویندوز → متغییر محیطی → مستقیم، با fail-open).
func resolveAppProxy(req *http.Request) (*url.URL, error) {
	a := appForLogs
	if a == nil {
		return smartProxy(req)
	}
	a.mu.Lock()
	mode := "auto"
	proxyURL := ""
	if a.settings != nil {
		mode = normalizeProxyMode(a.settings.ProxyMode)
		proxyURL = a.settings.ProxyURL
	}
	a.mu.Unlock()

	switch mode {
	case "off":
		return nil, nil
	case "custom":
		if strings.TrimSpace(proxyURL) == "" {
			return nil, nil
		}
		return parseProxyURL(proxyURL)
	default:
		return smartProxy(req)
	}
}

// proxyModeIsAuto فقط حالت هوشمند؟ برای تصمیم fail-open.
func proxyModeIsAuto() bool {
	a := appForLogs
	if a == nil {
		return true
	}
	a.mu.Lock()
	mode := "auto"
	if a.settings != nil {
		mode = normalizeProxyMode(a.settings.ProxyMode)
	}
	a.mu.Unlock()
	return mode == "auto"
}

// directTransport هم‌پوشه‌ی chatTransport بدون پروکسی برای مسیر نجات fail-open.
var directTransport = func() *http.Transport {
	t := chatTransport.Clone()
	t.Proxy = nil
	return t
}()

// failOpenTransport حالت هوشمند را نجات می‌دهد: اگر اتصال به پروکسی خودش شکست
// بخورد (proxyconnect/socks_connect)، همان درخواست مستقیم ادامه می‌یابد و
// پروکسی تا ۵ دقیقه کنار گذاشته می‌شود — کاربر دیگر هرگز proxyconnect نمی‌بیند
// و نیازی به انتخاب دستی «خاموش» نیست.
type failOpenTransport struct {
	base http.RoundTripper
}

func (f failOpenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := f.base.RoundTrip(req)
	if err == nil || !isProxyDialError(err) || !proxyModeIsAuto() {
		return resp, err
	}
	alreadyCooling := proxyCoolingDown()
	markProxyDead()
	retry := req.Clone(req.Context())
	if req.GetBody != nil && req.Body != nil {
		if b, berr := req.GetBody(); berr == nil {
			retry.Body = b
		}
	}
	r2, err2 := directTransport.RoundTrip(retry)
	if err2 != nil {
		// هر دو مسیر شکست خوردند — خطای اصلیِ پروکسی گزارش می‌شود
		return resp, err
	}
	if !alreadyCooling {
		logAppError("proxy", "پروکسی هوشمند پاسخ نداد؛ درخواست مستقیم ادامه یافت و تا ۵ دقیقه از پروکسی استفاده نمی‌شود")
	}
	return r2, nil
}

// isProxyDialError خطای «اتصال به خود پروکسی» را از خطای مقصد جدا می‌کند؛
// فقط در این حالت fail-open مجاز است (خطای خود مقصد نباید ماسک شود).
func isProxyDialError(err error) bool {
	s := err.Error()
	return strings.Contains(s, "proxyconnect") || strings.Contains(s, "socks_connect")
}

// chatTransport با resolveAppProxy — تغییر تنظیمات پروکسی فوری اعمال می‌شود.
var chatTransport = &http.Transport{
	Proxy:                 resolveAppProxy,
	DialContext:           (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 90 * time.Second,
	IdleConnTimeout:       90 * time.Second,
}

// chatHTTPClient با پوشش fail-open — حالت هوشمند هرگز proxyconnect به کاربر
// نشان نمی‌دهد.
var chatHTTPClient = &http.Client{
	Transport: failOpenTransport{base: chatTransport},
}

// apiToolCall فرمت tool_calls در API سازگار با OpenAI.
type apiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatMessage struct {
	Role string `json:"role"`
	// Content برای پیام‌های متنی string و برای پیام‌های تصویری‌دار
	// []contentPart است (فرمت multimodal سازگار با OpenAI).
	Content    interface{}   `json:"content,omitempty"`
	ToolCalls  []apiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	// CacheControl فقط وقتی Settings.PromptCache روشن است و روی "کپی ارسالی"
	// گذاشته می‌شود (promptcache.go) — تاریخچه‌ی محلی همیشه پاک می‌ماند.
	CacheControl *cacheControlHint `json:"cache_control,omitempty"`
}

// contentPart یک بخش از پیام multimodal — متن یا تصویر.
type contentPart struct {
	Type     string    `json:"type"` // "text" | "image_url"
	Text     string    `json:"text,omitempty"`
	ImageURL *imageRef `json:"image_url,omitempty"`
}

// imageRef آدرس تصویر (data URL پایه۶۴).
type imageRef struct {
	URL string `json:"url"`
}

// streamDeltaFlushEvery فاصله‌ی دسته‌ای‌کردن دلتاهای استریم است؛ هر چانک SSE
// تمیزکاری کامل متن را تکرار می‌کرد و برای پاسخ‌های بلند O(n²) می‌شد.
const streamDeltaFlushEvery = 40 * time.Millisecond

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Stream      bool          `json:"stream"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	// ReasoningEffort فقط برای Providerهایی که آن را می‌شناسند فرستاده می‌شود؛
	// ۴۰۰ِ مربوط به آن یک‌بار بدون پارامتر تکرار می‌شود (toolguard).
	ReasoningEffort string             `json:"reasoning_effort,omitempty"`
	Tools           []toolDef          `json:"tools,omitempty"`
	StreamOptions   *chatStreamOptions `json:"stream_options,omitempty"`
}

// chatStreamOptions — با include_usage توکن‌های واقعیِ مصرف‌شده در آخرین
// chunk استریم برمی‌گردد (OpenAI-compatible). سرویس‌دهنده‌هایی که این
// پارامتر را نمی‌شناسند با خطای ۴۰۰ رد می‌کنند؛ در آن صورت یک‌بار بدون آن
// تلاش می‌شود و در حافظه per-provider به‌خاطر سپرده می‌شود (toolguard.go).
type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type sseChoice struct {
	Delta struct {
		Content          string `json:"content"`
		ReasoningContent string `json:"reasoning_content,omitempty"`
		Reasoning        string `json:"reasoning,omitempty"`
		ToolCalls        []struct {
			Index    int    `json:"index"`
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

// sseUsage توکن گزارش‌شده از سرویس‌دهنده — فیلدهای رایج OpenAI-compatible
// و چند نام جایگزین (Gemini/Anthropic-proxy) پشتیبانی می‌شوند.
type sseUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	// نام‌های جایگزین برخی پروکسی‌ها / SDKها
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

func (u *sseUsage) prompt() int64 {
	if u == nil {
		return 0
	}
	if u.PromptTokens > 0 {
		return u.PromptTokens
	}
	return u.InputTokens
}

func (u *sseUsage) completion() int64 {
	if u == nil {
		return 0
	}
	if u.CompletionTokens > 0 {
		return u.CompletionTokens
	}
	return u.OutputTokens
}

func (u *sseUsage) total() int64 {
	if u == nil {
		return 0
	}
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	p, c := u.prompt(), u.completion()
	if p > 0 || c > 0 {
		return p + c
	}
	return 0
}

type sseChunk struct {
	Choices []sseChoice `json:"choices"`
	Usage   *sseUsage   `json:"usage"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type genState struct {
	cancel context.CancelFunc
	convID string
	// thinking متن استدلال مدل (reasoning / <think>) — فقط برای نمایش UI
	thinking string
	// thinkMs مجموع مدت «فکر کردن» دورهای این نوبت (میلی‌ثانیه) — با پیام ذخیره می‌شود
	thinkMs int64
	// unfinished یعنی این نوبت به‌خاطر سقف دورهای ابزار نیمه‌کاره رها شد
	// (نه با توقف کاربر) — در پیام نهایی ذخیره می‌شود تا UI «ادامه بده» بدهد.
	unfinished bool
	// done با بسته‌شدنشان اعلام می‌کند گوروتین استریم کاملاً خارج شده
	// (هیچ ذخیره‌ی دیگری روی دیسک نمی‌نویسد) — برای حذف کامل گفتگو
	// در حین پاسخ‌دهی ضروری است تا فایل حذف‌شده دوباره ساخته نشود.
	done chan struct{}
}

// readSSEChunks خطوط یک استریم SSE از نوع chat-completions را می‌خواند و هر
// chunk را به onChunk می‌دهد؛ اگر onChunk مقدار true برگرداند خواندن تمام
// می‌شود (مثلاً برای خطای مدل). مالکیت همه‌ی جزئیات قالب همین‌جاست تا حلقه‌ی
// والد (streamChat) و زیرایجنت دقیقاً یک تجزیه‌گر داشته باشند:
//
// - بافر اسکنر: 64KB اولیه با سقف 1MB (خط بلندتر → bufio.ErrTooLong)،
// - فقط خطوط با پیشوند data: (پس از TrimSpace) پردازش می‌شوند،
// - خط خالی، خط غیر-JSON و JSON ناقص بی‌صدا رد می‌شوند،
// - [DONE] پایان استریم است،
// - لغو ctx پیش از هر خط، خواندن را متوقف و ctx.Err را برمی‌گرداند
// (فراخواننده‌ها همین را «لغو توسط کاربر/مهلت» می‌بینند).
//
// خطای مدل (chunk.Error) در onChunk مدیریت می‌شود چون رفتار دو فراخواننده
// متفاوت است (ذخیره‌ی جزئی + رویداد UI در برابر فقط پیام خطا).
func readSSEChunks(ctx context.Context, r io.Reader, onChunk func(sseChunk) (stop bool)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			return nil
		}
		var chunk sseChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if onChunk != nil && onChunk(chunk) {
			return nil
		}
	}
	return scanner.Err()
}

// streamReadErrorText متن خطای خواندن بدنهٔ استریم را فارسی و قابل‌اقدام
// می‌کند. قطعِ نیمه‌کاره (unexpected EOF) یعنی سرویس‌دهنده یا پروکسی اتصال را
// وسط پاسخ بست؛ بخش دریافت‌شده از قبل با savePartial ذخیره شده و «ادامه» کار
// را از همان‌جا جلو می‌برد. خطاهای دیگر متن خودشان را نگه می‌دارند تا علت فنی
// پنهان نشود.
func streamReadErrorText(err error) string {
	if err == nil {
		return ""
	}
	if isEOFText(err.Error()) {
		return "پاسخ سرویس‌دهنده نیمه‌کاره ماند (اتصال بسته شد). بخش دریافت‌شده ذخیره شد؛ «ادامه» را بزنید. اگر تکرار شد، Provider یا پروکسی/شبکه را بررسی کنید"
	}
	return err.Error()
}

// streamClosedAfterFinishNotice پیام خنثی برای استریمی که پس از رسیدن
// finish_reason بدون [DONE] بسته شد — پاسخ کامل است و «ادامه» لازم نیست.
func streamClosedAfterFinishNotice(finishReason string) string {
	reason := strings.TrimSpace(finishReason)
	if reason == "" {
		reason = "stop"
	}
	return "اتصال استریم بدون سیگنال پایان ([DONE]) بسته شد، اما مدل پاسخ را کامل کرده بود (finish_reason: " +
		reason + "). پاسخ کامل ذخیره شد؛ نیازی به «ادامه» نیست."
}

// pingHTTPClient برای «تست اتصال» — مشترک بین فراخوانی‌ها تا هر تست
// یک http.Client جدید نسازد (هر Client مجموعه اتصال خودش را نگه می‌دارد).
var pingTransport = &http.Transport{
	Proxy:               resolveAppProxy,
	DialContext:         (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout: 12 * time.Second,
}
var pingHTTPClient = &http.Client{Timeout: 30 * time.Second, Transport: pingTransport}

// estimateContentTokens تخمین توکن برای محتوای پیام — رشته یا آرایه‌ی
// multimodal (متن + تصویر). تصویر data URL پایه۶۴ است و توکن قابل شمارش
// ندارد؛ هزینه‌ی تقریبی ثابت در نظر گرفته می‌شود.
func estimateContentTokens(c interface{}) int64 {
	switch v := c.(type) {
	case string:
		return estimateTokens(v)
	case []contentPart:
		var n int64
		for _, p := range v {
			n += estimateTokens(p.Text)
			if p.ImageURL != nil {
				n += 850 // هزینه‌ی تقریبی هر تصویر برای مدل‌های Vision
			}
		}
		return n
	default:
		return 0
	}
}

// SendMessage پیام کاربر را ارسال می‌کند و پاسخ را استریم می‌کند.
func (a *App) SendMessage(convID, content string, images []string) (*Conversation, error) {
	defer a.recoverBound("SendMessage")
	content = strings.TrimSpace(content)
	images = sanitizeImages(images)
	if content == "" && len(images) == 0 {
		return nil, errors.New("متن پیام خالی است")
	}

	// ارجاع @mention — قبل از گرفتن قفل محاسبه می‌شود (خواندن فایل‌ها
	// ممکن است طول بکشد و a.mu نباید در این مدت بسته باشد)
	attached := a.expandMentions(content)

	// خط کانتکست git — مثل @mention قبل از گرفتن قفل‌ها محاسبه می‌شود:
	// exec فرمان git تا ~۵ ثانیه طول می‌کشد و نباید زیر a.mu رخ دهد (F1).
	gitLine := a.gitContextLineForSend()

	a.genMu.Lock()
	if a.gen != nil {
		a.genMu.Unlock()
		return nil, errors.New("پاسخ‌دهی قبلی هنوز در جریان است؛ ابتدا آن را متوقف کنید")
	}

	a.mu.Lock()
	var conv *Conversation
	if convID == "" {
		conv = &Conversation{
			ID:        newID(),
			Title:     titleFromContent(content),
			CreatedAt: time.Now().Unix(),
			Messages:  []Message{},
		}
	} else {
		c, err := a.loadConversation(convID)
		if err != nil {
			a.mu.Unlock()
			a.genMu.Unlock()
			return nil, err
		}
		conv = c
	}

	if len(conv.Messages) == 0 {
		conv.Title = titleFromContent(content)
	}

	userMsg := Message{
		ID:      newID(),
		Role:    "user",
		Content: content,
		At:      time.Now().Unix(),
		Tokens:  estimateTokens(content),
	}
	// ارجاع @mention — محتوای فایل‌ها به‌صورت بلوک جداگانه الصاق می‌شود؛
	// متن اصلی پیام برای نمایش در UI دست‌نخورده می‌ماند.
	userMsg.Attached = attached
	userMsg.Images = images
	// توجه: Tokens فقط متن نمایشی پیام است؛ Attached فقط وقتی فرستاده می‌شود
	// (آخرین پیام کاربر / پیام سنجاق‌شده) در conversationContextTokens حساب می‌شود.
	conv.Messages = append(conv.Messages, userMsg)

	planActive := a.settings.PlanMode && a.settings.AgentMode
	baseURL, key, model, temperature, history, perr := a.prepareStream(conv, planActive, gitLine)
	if perr != nil {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, perr
	}
	if err := a.saveConversation(conv); err != nil {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, err
	}
	a.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	myGen := &genState{cancel: cancel, convID: conv.ID, done: make(chan struct{})}
	a.gen = myGen
	a.genMu.Unlock()

	runtimeEmit(a, "chat:start", map[string]interface{}{
		"conversation": conv,
		"userMessage":  userMsg,
	})

	// در Plan Mode ابزارها نه ارسال می‌شوند و نه اجرا — فقط prompt کافی نیست.
	safeGo(a, "streamChat", func() { a.streamChat(ctx, myGen, baseURL, key, model, temperature, history, !planActive) })
	return conv, nil
}

// Regenerate پاسخ آخر دستیار را حذف و از روی آخرین پیام کاربر دوباره تولید می‌کند.
func (a *App) Regenerate(convID string) (*Conversation, error) {
	defer a.recoverBound("Regenerate")
	// خط کانتکست git — قبل از گرفتن قفل‌ها (exec زیر a.mu ممنوع است؛ F1)
	gitLine := a.gitContextLineForSend()

	a.genMu.Lock()
	if a.gen != nil {
		a.genMu.Unlock()
		return nil, errors.New("پاسخ‌دهی قبلی هنوز در جریان است؛ ابتدا آن را متوقف کنید")
	}

	a.mu.Lock()
	conv, err := a.loadConversation(convID)
	if err != nil {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, err
	}

	n := len(conv.Messages)
	for n > 0 && (conv.Messages[n-1].Role == "assistant" || conv.Messages[n-1].Role == "tool") {
		n--
	}
	if n == 0 || conv.Messages[n-1].Role != "user" {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, errors.New("پیامی برای تولید دوباره وجود ندارد")
	}
	conv.Messages = conv.Messages[:n]
	lastUser := conv.Messages[n-1]

	planActive := a.settings.PlanMode && a.settings.AgentMode
	baseURL, key, model, temperature, history, perr := a.prepareStream(conv, planActive, gitLine)
	if perr != nil {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, perr
	}
	if err := a.saveConversation(conv); err != nil {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, err
	}
	a.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	myGen := &genState{cancel: cancel, convID: conv.ID, done: make(chan struct{})}
	a.gen = myGen
	a.genMu.Unlock()

	runtimeEmit(a, "chat:start", map[string]interface{}{
		"conversation": conv,
		"userMessage":  lastUser,
	})

	safeGo(a, "streamChat", func() { a.streamChat(ctx, myGen, baseURL, key, model, temperature, history, !planActive) })
	return conv, nil
}

// DeleteMessage پیام مشخص‌شده و همه‌ی پیام‌های بعد از آن را از گفتگو حذف می‌کند.
// برای پیام کاربر: خود پیام و پاسخ‌های بعدی حذف می‌شوند.
// برای پیام دستیار/ابزار: از همان نقطه تا انتها بریده می‌شود.
func (a *App) DeleteMessage(convID, messageID string) (*Conversation, error) {
	defer a.recoverBound("DeleteMessage")
	a.genMu.Lock()
	if a.gen != nil {
		a.genMu.Unlock()
		return nil, errors.New("پاسخ‌دهی در جریان است؛ ابتدا آن را متوقف کنید")
	}
	a.genMu.Unlock()

	a.mu.Lock()
	defer a.mu.Unlock()

	conv, err := a.loadConversation(convID)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i := range conv.Messages {
		if conv.Messages[i].ID == messageID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, errors.New("پیام موردنظر پیدا نشد")
	}
	conv.Messages = conv.Messages[:idx]
	if err := a.saveConversation(conv); err != nil {
		return nil, err
	}
	return conv, nil
}

// EditMessage متن یک پیام کاربر را عوض می‌کند، پیام‌های بعد از آن را حذف
// می‌کند و دوباره از همان نقطه پاسخ می‌سازد (مشابه Regenerate).
func (a *App) EditMessage(convID, messageID, content string) (*Conversation, error) {
	defer a.recoverBound("EditMessage")
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, errors.New("متن پیام نمی‌تواند خالی باشد")
	}

	gitLine := a.gitContextLineForSend()

	a.genMu.Lock()
	if a.gen != nil {
		a.genMu.Unlock()
		return nil, errors.New("پاسخ‌دهی قبلی هنوز در جریان است؛ ابتدا آن را متوقف کنید")
	}

	a.mu.Lock()
	conv, err := a.loadConversation(convID)
	if err != nil {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, err
	}

	idx := -1
	for i := range conv.Messages {
		if conv.Messages[i].ID == messageID {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, errors.New("پیام موردنظر پیدا نشد")
	}
	if conv.Messages[idx].Role != "user" {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, errors.New("فقط پیام‌های کاربر قابل ویرایش هستند")
	}

	conv.Messages[idx].Content = content
	// تصاویر و attached قبلی حفظ می‌شوند مگر کاربر بخواهد عوض کند — فعلاً فقط متن
	conv.Messages = conv.Messages[:idx+1]
	lastUser := conv.Messages[idx]

	planActive := a.settings.PlanMode && a.settings.AgentMode
	baseURL, key, model, temperature, history, perr := a.prepareStream(conv, planActive, gitLine)
	if perr != nil {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, perr
	}
	if err := a.saveConversation(conv); err != nil {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, err
	}
	a.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	myGen := &genState{cancel: cancel, convID: conv.ID, done: make(chan struct{})}
	a.gen = myGen
	a.genMu.Unlock()

	runtimeEmit(a, "chat:start", map[string]interface{}{
		"conversation": conv,
		"userMessage":  lastUser,
	})

	safeGo(a, "streamChat", func() { a.streamChat(ctx, myGen, baseURL, key, model, temperature, history, !planActive) })
	return conv, nil
}

// ApprovePlan برنامه‌ی پیشنهادیِ آخر را تأیید و اجرای آن را شروع می‌کند.
// پیام تأیید فارسی به تاریخچه اضافه می‌شود و استریم بدون بلوک برنامه‌ریزی
// (یعنی با اجازه‌ی ابزار) آغاز می‌گردد.
func (a *App) ApprovePlan(convID string) (*Conversation, error) {
	defer a.recoverBound("ApprovePlan")
	// خط کانتکست git — قبل از گرفتن قفل‌ها (exec زیر a.mu ممنوع است؛ F1)
	gitLine := a.gitContextLineForSend()

	a.genMu.Lock()
	if a.gen != nil {
		a.genMu.Unlock()
		return nil, errors.New("پاسخ‌دهی قبلی هنوز در جریان است؛ ابتدا آن را متوقف کنید")
	}
	a.mu.Lock()
	conv, err := a.loadConversation(convID)
	if err != nil {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, err
	}
	n := len(conv.Messages)
	if n == 0 || conv.Messages[n-1].Role != "assistant" {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, errors.New("برنامه‌ای برای تأیید وجود ندارد")
	}

	approval := Message{
		ID:      newID(),
		Role:    "user",
		Content: "برنامه تأیید شد — همین حالا آن را گام‌به‌گام اجرا کن و در پایان خلاصه‌ی کارها را بنویس.",
		At:      time.Now().Unix(),
		Tokens:  24,
	}
	conv.Messages = append(conv.Messages, approval)

	// اجرا بدون بلوک برنامه‌ریزی — ابزارها آزادند
	baseURL, key, model, temperature, history, perr := a.prepareStream(conv, false, gitLine)
	if perr != nil {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, perr
	}
	if err := a.saveConversation(conv); err != nil {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, err
	}
	a.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	myGen := &genState{cancel: cancel, convID: conv.ID, done: make(chan struct{})}
	a.gen = myGen
	a.genMu.Unlock()

	runtimeEmit(a, "chat:start", map[string]interface{}{
		"conversation": conv,
		"userMessage":  approval,
	})

	// ApprovePlan همیشه با ابزار اجرا می‌شود (planMode در prepareStream خاموش بود).
	safeGo(a, "streamChat", func() { a.streamChat(ctx, myGen, baseURL, key, model, temperature, history, true) })
	return conv, nil
}

// ResumeAgent ادامه‌ی یک اجرای ایجنت نیمه‌کاره (مثلاً بعد از خطا، توقف یا
// بستن برنامه). پیام «ادامه بده» ثبت و پاسخ‌دهی دوباره شروع می‌شود.
// فقط وقتی معنا دارد که آخرین پیام، دستیار یا نتیجه‌ی ابزار باشد.
func (a *App) ResumeAgent(convID string) (*Conversation, error) {
	defer a.recoverBound("ResumeAgent")
	// خط کانتکست git — قبل از گرفتن قفل‌ها (exec زیر a.mu ممنوع است؛ F1)
	gitLine := a.gitContextLineForSend()

	a.genMu.Lock()
	if a.gen != nil {
		a.genMu.Unlock()
		return nil, errors.New("پاسخ‌دهی دیگری در جریان است؛ ابتدا آن را متوقف کنید")
	}
	a.mu.Lock()
	conv, err := a.loadConversation(convID)
	if err != nil {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, err
	}
	n := len(conv.Messages)
	if n == 0 || (conv.Messages[n-1].Role != "assistant" && conv.Messages[n-1].Role != "tool") {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, errors.New("اجرای نیمه‌کاره‌ای برای ادامه‌دادن پیدا نشد")
	}

	resume := Message{
		ID:      newID(),
		Role:    "user",
		Content: "از همان‌جا که متوقف شدی ادامه بده — کارهای باقی‌مانده را تمام کن و در پایان خلاصه‌ی وضعیت را بنویس.",
		At:      time.Now().Unix(),
		Tokens:  26,
	}
	conv.Messages = append(conv.Messages, resume)

	baseURL, key, model, temperature, history, perr := a.prepareStream(conv, false, gitLine)
	if perr != nil {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, perr
	}
	if err := a.saveConversation(conv); err != nil {
		a.mu.Unlock()
		a.genMu.Unlock()
		return nil, err
	}
	a.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	myGen := &genState{cancel: cancel, convID: conv.ID, done: make(chan struct{})}
	a.gen = myGen
	a.genMu.Unlock()

	runtimeEmit(a, "chat:start", map[string]interface{}{
		"conversation": conv,
		"userMessage":  resume,
	})

	// ادامهٔ ایجنت همیشه با ابزار
	safeGo(a, "streamChat", func() { a.streamChat(ctx, myGen, baseURL, key, model, temperature, history, true) })
	return conv, nil
}

// prepareStream تنظیمات و تاریخچه‌ی لازم برای استریم را آماده می‌کند.
// باید با a.mu قفل‌شده فراخوانی شود.
// planMode=true بلوک «فقط برنامه بریز، ابزار اجرا نکن» را به system prompt اضافه می‌کند.
// gitLine خط کانتکست git است که فراخواننده باید «قبل از گرفتن a.mu» با
// gitContextLineForSend محاسبه و پاس بدهد — داخل این تابع هیچ exec ای
// انجام نمی‌شود (رفع F1).
func (a *App) prepareStream(conv *Conversation, planMode bool, gitLine string) (baseURL, key, model string, temperature float64, history []chatMessage, err error) {
	provider := a.findProvider(a.settings.Provider)
	if provider == nil {
		return "", "", "", 0, nil, errors.New("سرویس‌دهنده تنظیم نشده است؛ از تنظیمات یک Provider انتخاب کنید")
	}
	baseURL = provider.BaseURL
	model = a.settings.Model
	if model == "" && len(provider.Models) > 0 {
		model = provider.Models[0]
	}
	if model == "" {
		return "", "", "", 0, nil, errors.New("هیچ مدلی انتخاب نشده؛ از تنظیمات مدل را وارد کنید")
	}
	temperature = a.settings.Temperature
	if !provider.NoKey {
		key = a.decryptKey(provider.ID)
		if key == "" {
			return "", "", "", 0, nil, errors.New("برای «" + provider.Name + "» کلید API تنظیم نشده؛ در تنظیمات، فرم Provider را باز کنید و کلید را همان‌جا وارد کنید")
		}
	}

	systemPrompt := a.settings.SystemPrompt
	if a.settings.AgentMode {
		ws := strings.TrimSpace(a.settings.WorkspacePath)
		if ws == "" {
			return "", "", "", 0, nil, errors.New("برای حالت ایجنت ابتدا مسیر پروژه (پوشه‌ی کاری) را در تنظیمات مشخص کنید")
		}
		systemPrompt = agentSystemPrompt(ws)
		// دروازهٔ پایان کار: چک‌لیست بازِ گفتگو در شروع نوبت به مدل داده
		// می‌شود تا پس از توقف/نوبت جدید، طرح کار فراموش نشود.
		if conv != nil {
			if block := todoResumeBlock(conv.Todos); block != "" {
				systemPrompt += "\n\n" + block
			}
		}
		// آگاهی مدل از branch و وضعیت dirty — از قبلِ قفل محاسبه شده
		// (gitContextLineForSend) و فقط اینجا تزریق می‌شود؛ خودِ
		// prepareStream هیچ فرمانی اجرا نمی‌کند (F1).
		if gitLine != "" {
			systemPrompt += "\n" + gitLine + "\n"
		}
		// مهارت‌های پروژه (SKILL.md): فقط فهرست فشرده؛ متن کامل با read_file.
		if block := a.skillsPromptBlockLocked(); block != "" {
			systemPrompt += "\n\n" + block
		}
	}
	if !a.settings.AgentMode {
		systemPrompt += "\n\n" + chatToolsPromptBlock(strings.TrimSpace(a.settings.WorkspacePath))
	}
	// قوانین پروژه (Project Rules) — هم در حالت عادی و هم ایجنت تزریق می‌شود
	if block := a.rulesPromptBlockLocked(); block != "" {
		systemPrompt += "\n\n" + block
	}
	// حالت برنامه‌ریزی: فقط برنامه، بدون اجرای ابزار
	if planMode {
		systemPrompt += "\n\n" +
			"PLANNING MODE is ACTIVE for this turn:\n" +
			"- Do NOT call any tools yet; the plan must be approved by the user first.\n" +
			"- Output ONLY a concise numbered step-by-step plan in Persian.\n" +
			"- For each step mention which files/directories will be read or changed (if predictable).\n" +
			"- Do not write full code in this turn; tiny illustrative snippets are fine.\n" +
			"- End with one short line telling the user they can approve the plan or ask for changes."
	}

	history = []chatMessage{{Role: "system", Content: systemPrompt}}
	start := 0
	if n := len(conv.Messages); n > a.settings.MaxHistory {
		start = n - a.settings.MaxHistory
	}
	for start < len(conv.Messages) && conv.Messages[start].Role == "tool" {
		start++
	}
	// پیام‌های Pinشده‌ای که از پنجره بیرون مانده‌اند — بعد از system prompt
	// و با نشانه‌گذاری واضح برمی‌گردند تا همیشه در کانتکست بمانند.
	for _, pm := range pinnedOutsideWindow(conv.Messages, start) {
		pc := pinnedHistoryMarker + "\n\n" + pm.Content
		if pm.Attached != "" {
			pc += "\n\n" + compactToolResult(pm.Attached)
		}
		history = append(history, chatMessage{Role: "user", Content: pc})
	}

	// هرس API (تک‌حلقه برای حفظ ترتیب دقیق پیام‌ها):
	// - نتایج ابزارهای قدیمی (به‌جز ToolResultKeep مورد آخر) فشرده می‌شوند
	// - Attached فقط برای آخرین پیام کاربر حفظ می‌شود
	// فایل ذخیره‌شده‌ی گفتگو دست‌نخورده می‌ماند.
	window := conv.Messages[start:]
	keep := a.settings.ToolResultKeep
	if keep <= 0 {
		keep = 6
	}
	toolTotal := 0
	for _, m := range window {
		if m.Role == "tool" {
			toolTotal++
		}
	}
	fullToolFrom := toolTotal - keep
	lastUserIdx := -1
	for i := len(window) - 1; i >= 0; i-- {
		if window[i].Role == "user" {
			lastUserIdx = i
			break
		}
	}

	// تصویر اسکرین‌شات فقط از آخرین دستهٔ نتایج ابزار و فقط برای مدل
	// تصویری فرستاده می‌شود: تصویر قدیمی هم توکن می‌خورد و هم کهنه است.
	visionOn := visionImagesEnabled(a.settings.VisionImages, model)
	trailingToolStart := len(window)
	for trailingToolStart > 0 && window[trailingToolStart-1].Role == "tool" {
		trailingToolStart--
	}
	var toolShots []string

	seenTools := 0
	for i, m := range window {
		if m.Role == "assistant" && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
			continue
		}
		cm := chatMessage{Role: m.Role, Content: m.Content}
		switch m.Role {
		case "user":
			if m.Attached != "" && i == lastUserIdx {
				cm.Content = m.Content + "\n\n" + m.Attached
			}
			// تصاویر فقط برای آخرین پیام کاربر ارسال می‌شوند (صرفه‌جویی توکن)
			if len(m.Images) > 0 && i == lastUserIdx {
				text, _ := cm.Content.(string)
				if text == "" {
					text = m.Content
				}
				parts := []contentPart{{Type: "text", Text: text}}
				for _, img := range m.Images {
					if img != "" {
						parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageRef{URL: img}})
					}
				}
				cm.Content = parts
			}
		case "tool":
			if seenTools < fullToolFrom {
				cm.Content = compactToolResult(m.Content)
			}
			seenTools++
			if m.Content == "" {
				cm.Content = "(empty)"
			}
			cm.ToolCallID = m.ToolCallID
			if visionOn && i >= trailingToolStart {
				for _, img := range m.Images {
					if len(toolShots) >= visionMaxShots {
						break
					}
					if img != "" {
						toolShots = append(toolShots, img)
					}
				}
			}
		case "assistant":
			if len(m.ToolCalls) > 0 {
				cm.ToolCalls = make([]apiToolCall, len(m.ToolCalls))
				for j, tc := range m.ToolCalls {
					cm.ToolCalls[j].ID = tc.ID
					cm.ToolCalls[j].Type = "function"
					cm.ToolCalls[j].Function.Name = tc.Name
					cm.ToolCalls[j].Function.Arguments = tc.Args
				}
			}
		}
		history = append(history, cm)
	}
	// اسکرین‌شات‌های همین دسته به‌صورت یک پیام کاربریِ چندبخشی ضمیمه می‌شوند
	// (پروتکل tool فقط متن می‌پذیرد؛ تصویر در پیام کاربر امن است).
	if len(toolShots) > 0 {
		parts := make([]contentPart, 0, len(toolShots)+1)
		parts = append(parts, contentPart{Type: "text",
			Text: "Attached screenshot(s) captured by browser_screenshot in this turn. Inspect the image(s) directly and describe only what you can actually see."})
		for _, u := range toolShots {
			parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageRef{URL: u}})
		}
		history = append(history, chatMessage{Role: "user", Content: parts})
	}
	return baseURL, key, model, temperature, history, nil
}

// pendingToolCall انباشت دلتاهای یک tool_call در جریان SSE.
type pendingToolCall struct {
	id   string
	name string
	args strings.Builder
}

// collectToolCalls فراخوانی‌های انباشته را به ترتیب ایندکس برمی‌گرداند.
func collectToolCalls(pend map[int]*pendingToolCall) []apiToolCall {
	if len(pend) == 0 {
		return nil
	}
	idxs := make([]int, 0, len(pend))
	for i := range pend {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	out := make([]apiToolCall, 0, len(idxs))
	for _, i := range idxs {
		p := pend[i]
		if p.name == "" {
			continue
		}
		var c apiToolCall
		c.ID = p.id
		if c.ID == "" {
			c.ID = "call_" + randSuffix(6)
		}
		c.Type = "function"
		c.Function.Name = p.name
		c.Function.Arguments = p.args.String()
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// isToolsUnsupportedError تشخیص خطای «این سرویس/مدل از ابزار پشتیبانی نمی‌کند».
func isToolsUnsupportedError(status int, body string) bool {
	if status != http.StatusBadRequest && status != http.StatusNotFound && status != http.StatusUnprocessableEntity {
		return false
	}
	b := strings.ToLower(body)
	for _, k := range []string{"tools", "tool_calls", "tool_choice", "tool use", "tool calling", "function calling", "function_call", "functions"} {
		if strings.Contains(b, k) {
			return true
		}
	}
	return false
}

// supportsTemperature مدل‌های استدلالی (سری o و gpt-5) فقط مقدار پیش‌فرض را می‌پذیرند.
func supportsTemperature(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	for _, p := range []string{"o1", "o3", "o4", "gpt-5"} {
		if strings.HasPrefix(m, p) {
			return false
		}
	}
	return true
}

// savePartial متن نیمه‌کاره‌ی دریافت‌شده را ذخیره می‌کند و توکن‌های مصرف‌شده
// تا این لحظه را در TokensUsed ثبت می‌کند.
// recordTokenUsageLocked مصرف توکن را به گفتگو، مدل فعلی، Provider فعلی و
// مجموع کل می‌افزاید. exact=true یعنی اعداد از usage رسمی API آمده‌اند.
// باید با a.mu نگه داشته‌شده صدا زده شود.
func (a *App) recordTokenUsageLocked(conv *Conversation, model string, promptTokens, completionTokens int64, exact bool) {
	total := promptTokens + completionTokens
	if total <= 0 {
		return
	}
	if conv != nil {
		// اگر قبلاً exact بوده و این نوبت تخمینی است → exact را false کن
		if conv.TokensUsed == 0 {
			conv.TokensExact = exact
		} else if !exact {
			conv.TokensExact = false
		}
		conv.TokensUsed += total
	}
	a.settings.TokensUsed += total
	if a.settings.TokensByProvider == nil {
		a.settings.TokensByProvider = map[string]int64{}
	}
	pid := strings.TrimSpace(a.settings.Provider)
	if pid != "" {
		a.settings.TokensByProvider[pid] += total
	}
	// حسابداری به تفکیک مدل — سطلِ هر مدل جدا از هر مدل دیگر؛ حتی وقتی چند
	// مدل از یک Provider استفاده می‌کنند، توکن‌شان قاطی نمی‌شود.
	model = strings.TrimSpace(model)
	if model != "" {
		if a.settings.TokensByModel == nil {
			a.settings.TokensByModel = map[string]int64{}
		}
		a.settings.TokensByModel[model] += total
	}
	if completionTokens > 0 {
		a.settings.RecentUsage = append(a.settings.RecentUsage, completionTokens)
		if len(a.settings.RecentUsage) > 30 {
			a.settings.RecentUsage = a.settings.RecentUsage[len(a.settings.RecentUsage)-30:]
		}
	}
}

// contextLimitLocked سقف پنجره‌ی کانتکست فعال را برمی‌گرداند: مقدار خود
// Provider اگر مشخص باشد، وگرنه تنظیمات کلی. باید با a.mu نگه داشته شود.
func (a *App) contextLimitLocked() int {
	if p := a.findProvider(a.settings.Provider); p != nil && p.ContextWindow > 0 {
		return p.ContextWindow
	}
	return a.settings.TokenLimit
}

// usageSnapshotForUI مقادیر نمایش نوار مصرف را می‌سازد.
// tokensUsed = تخمین کانتکست همین گفتگو (نه مجموع عمر برنامه).
// providerTokens = مصرف تجمعی Provider فعال.
func (a *App) usageSnapshotForUI(conv *Conversation) (used int64, limit int, providerUsed int64, chatAPI int64, exact bool, recent []int64) {
	limit = a.contextLimitLocked()
	if conv != nil {
		used = conversationContextTokens(conv)
		chatAPI = conv.TokensUsed
		exact = conv.TokensExact && chatAPI > 0
	}
	if a.settings.TokensByProvider != nil {
		providerUsed = a.settings.TokensByProvider[a.settings.Provider]
	}
	recent = make([]int64, len(a.settings.RecentUsage))
	copy(recent, a.settings.RecentUsage)
	return used, limit, providerUsed, chatAPI, exact, recent
}

func (a *App) savePartial(convID, content string, promptTokens, completionTokens int64, model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	vis, th := cleanAssistantContent(content)
	conv, err := a.loadConversation(convID)
	if err != nil {
		// حتی بدون گفتگو، مصرف را ثبت کن تا از دست نرود
		a.recordTokenUsageLocked(nil, model, promptTokens, completionTokens, promptTokens > 0 || completionTokens > 0)
		_ = a.saveSettings()
		return
	}
	a.recordTokenUsageLocked(conv, model, promptTokens, completionTokens, promptTokens > 0 || completionTokens > 0)
	if strings.TrimSpace(vis) != "" || strings.TrimSpace(th) != "" {
		partialMode := "chat"
		if a.settings.AgentMode {
			partialMode = "agent"
		}
		conv.Messages = append(conv.Messages, Message{
			ID:       newID(),
			Role:     "assistant",
			Content:  vis,
			Thinking: th,
			At:       time.Now().Unix(),
			Tokens:   estimateTokens(vis),
			Aborted:  true,
			Mode:     partialMode,
		})
	}
	if err := a.saveConversation(conv); err != nil {
		logAppError("save", "savePartial: "+err.Error())
	}
	_ = a.saveSettings()
}

// persistAgentRound پیام دستیار (با فراخوانی‌های ابزار) و نتایج ابزار را
// در میانه‌ی کار ایجنت روی دیسک ذخیره می‌کند.
func (a *App) persistAgentRound(convID, assistantContent string, calls []apiToolCall, toolMsgs []Message, thinkMs int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	conv, err := a.loadConversation(convID)
	if err != nil {
		return false
	}
	if assistantContent != "" || len(calls) > 0 {
		recs := make([]toolCallRecord, len(calls))
		for i, c := range calls {
			recs[i] = toolCallRecord{ID: c.ID, Name: c.Function.Name, Args: c.Function.Arguments}
		}
		vis, th := cleanAssistantContent(assistantContent)
		tk := int64(0)
		if th != "" {
			tk = estimateTokens(th)
		}
		conv.Messages = append(conv.Messages, Message{
			ID:             newID(),
			Role:           "assistant",
			Content:        vis,
			Thinking:       th,
			ThinkingMs:     thinkMs,
			ThinkingTokens: tk,
			At:             time.Now().Unix(),
			ToolCalls:      recs,
			Mode:           "agent",
		})
	}
	conv.Messages = append(conv.Messages, toolMsgs...)
	if err := a.saveConversation(conv); err != nil {
		logAppError("save", "persistAgentRound: "+err.Error())
		return false // خطای ذخیره → جریان ایجنت قطع شود تا وضعیت دیسک ناسالم نماند
	}
	return true
}

// streamChat اجرای استریم در یک گوروتین؛ در حالت ایجنت، حلقه‌ی ابزار
// تا رسیدن پاسخ نهایی تکرار می‌شود.
//
// allowTools: اگر false باشد (Plan Mode)، ابزارها نه به API فرستاده می‌شوند
// و نه در صورت بازگشت مدل اجرا می‌شوند — اجبار در لایهٔ اجرا، نه فقط prompt.
func (a *App) streamChat(ctx context.Context, myGen *genState, baseURL, key, model string, temperature float64, history []chatMessage, allowTools bool) {
	convID := myGen.convID
	// آمار مصرف زیرایجنت‌ها (ابزار task) در همین نوبت (Bug 8): اتمیک و بدون
	// قفل، از طریق ctx به ابزارها/زیرایجنت می‌رسد و پیش از finishChat با آمار
	// خودِ نوبت جمع می‌شود. قالب‌های ذخیره‌شده تغییری نمی‌کنند.
	usageTally := &usageTally{}
	ctx = withUsageTally(ctx, usageTally)
	// مدل نوبت (۴.۲): شناسه‌ی صعودی + ضبط‌کننده‌ی دستورات/تغییرات — از
	// طریق ctx به همه‌ی ابزارها (و زیرایجنت) می‌رسد؛ خلاصه‌ی پایان نوبت
	// فقط از همین رکوردها ساخته می‌شود (ground truth، نه متن مدل).
	turnID := a.nextTurnID()
	turnRec := &turnRecorder{}
	// دروازهٔ پایان کار: چک‌لیستِ منتقل‌شده از نوبت‌های قبل باید همین‌جا
	// هم دیده شود، وگرنه مدل می‌تواند با آیتم باز (بدون todo_write در این
	// نوبت) «تمام شد» بگوید و گیت بی‌خبر بماند. todo_write همین نوبت
	// بعداً آن را بازنویسی می‌کند.
	a.mu.Lock()
	if conv, cerr := a.loadConversation(convID); cerr == nil {
		turnRec.setTodos(conv.Todos)
	}
	a.mu.Unlock()
	ctx = withTurnContext(ctx, turnID, turnRec)
	// اندازه‌گیری تأخیر این نوبت (latency.go): TTFT، هر دور، هر ابزار.
	timing := newTurnTiming()
	ctx = withTurnTiming(ctx, timing)
	var allContent strings.Builder
	var promptTokens, completionTokens, estTotal int64
	aborted := false
	started := time.Now()
	persistedLen := 0

	a.mu.Lock()
	// ابزارها در حالت ایجنت کامل‌اند و در حالت چت فقط «خواندنی» (جستجوی وب،
	// جستجو/خواندن فایل) — تا «چیزی را جست‌وجو کن» در چت هم واقعاً کار کند.
	agentOn := a.settings.AgentMode
	useTools := allowTools && agentOn && strings.TrimSpace(a.settings.WorkspacePath) != ""
	if !agentOn {
		useTools = allowTools
	}
	customTools := copyCustomTools(a.settings.CustomTools)
	maxTokens := a.settings.MaxTokens
	thinkingEffort := normalizeThinkingEffort(a.settings.ThinkingEffort)
	// سقف دورهای ابزار — تنظیم قابل‌تغییر کاربر (پیش‌فرض ۲۵، محدوده‌ی ۵ تا ۲۰۰)؛
	// زیر همان قفلِ بقیه‌ی تنظیمات خوانده و برای کل نوبت ثابت می‌شود.
	maxRounds := clampMaxAgentRounds(a.settings.MaxAgentRounds)
	// سقف مصرف توکن هر نوبت (A.4) — ۰ = خاموش؛ برای کل نوبت ثابت.
	turnTokenCap := a.settings.MaxTurnTokens
	// سیاست تصویر برای کل نوبت ثابت است (مدل در param آمده است).
	visionOn := visionImagesEnabled(a.settings.VisionImages, model)
	// مهلت بیکاری استریم (Stage 2) — تنظیم پنهان؛ برای کل نوبت ثابت.
	streamIdleSec := clampStreamIdleSeconds(a.settings.StreamIdleTimeoutSec)
	streamIdleTimeout := time.Duration(streamIdleSec) * time.Second
	// بودجه‌ی توکن کانتکست — از پنجره‌ی Provider فعال؛ در طول نوبت ثابت.
	cw := 0
	if p := a.findProvider(a.settings.Provider); p != nil {
		cw = p.ContextWindow
	}
	a.mu.Unlock()
	budget := contextBudget(cw, maxTokens)
	// ابزارهای داخلی + ابزارهای سفارشی کاربر — هر دو به مدل معرفی می‌شوند.
	tools := agentToolDefs()
	if !agentOn {
		tools = filterChatTools(tools)
	}
	if len(customTools) > 0 && agentOn {
		tools = append(tools, customToolDefs(customTools)...)
	}
	// ابزارهای MCP — شروع تنبلِ سرورها با بودجه‌ی ۱۵ ثانیه؛ بیرون از
	// a.mu (start با فرایند فرزند حرف می‌زند) و شکستِ سرور نوبت را
	// نمی‌شکند (فقط لاگ + وضعیت «خطا»).
	if mcpDefs := a.mcpToolDefs(ctx); len(mcpDefs) > 0 && agentOn {
		tools = append(tools, mcpDefs...)
	}
	droppedTools := false

	// آزادسازی منابع: حتی در پایان طبیعی، cancel باید صدا زده شود.
	// (go vet: lostcancel — بدون این فراخوانی، درخت context هر پاسخ تا خروج
	// برنامه در حافظه می‌ماند و با هر پیام جدید انباشته می‌شد — نشت حافظه.)
	defer myGen.cancel()
	// done آخر از همه بسته می‌شود (بعد از a.gen=nil و پس از آخرین ذخیره) تا
	// منتظرماندگان (مثل DeleteConversation) مطمئن باشند نوشتن روی دیسک تمام شده.
	defer close(myGen.done)
	defer func() {
		a.genMu.Lock()
		if a.gen == myGen {
			a.gen = nil
		}
		a.genMu.Unlock()
	}()
	// مهار panic: برنامه زنده می‌ماند، وضعیت «در حال تولید» با emitChatError
	// آزاد می‌شود و کاربر خطای فارسی می‌بیند. این defer پیش از پاک‌سازی‌های
	// بالا اجرا می‌شود (ترتیب LIFO) تا قفل gen و done درست بسته شوند.
	defer func() {
		if r := recover(); r != nil {
			recordPanic(a, "streamChat", r)
			a.emitChatError(myGen, errInternalUI)
		}
	}()

	// finish پیام نهایی را با آمار نوبت + آمار زیرایجنت‌های همین نوبت ذخیره و
	// منتشر می‌کند (تنها تفاوت با finishChat قبلی: جمعِ دو بخش مصرف).
	finalize := func(content string, isAborted bool) {
		sp, sc := usageTally.snapshot()
		a.finishChat(myGen, model, content, isAborted, promptTokens+sp, completionTokens+sc, estTotal)
		a.emitTurnSummary(convID, turnID, turnRec, promptTokens+sp, completionTokens+sc)
		// اعداد تأخیر پس از ذخیره‌ی نهایی منتشر می‌شوند؛ نوبت کند در
		// errors.log با برچسب [latency] ثبت می‌شود.
		a.recordTurnTiming(convID, turnID, timing.snapshot())
	}
	// شمارش توکن تاریخچه با کش — هر پیام فقط یک‌بار تخمین زده می‌شود؛
	// tool_calls هم حساب می‌شود (قرابت نزدیک‌تر با prompt_tokens واقعی).
	counter := &historyTokenCache{}
	// واقعیت‌های پروژه (A.3) — نوع پروژه و دستورهای پیشنهادی راستی‌آزمایی،
	// یک‌بار در ابتدای نوبت و «بیرون از a.mu» (I/O دارد) به system prompt
	// اضافه می‌شود. هیچ فرمانی اجرا نمی‌شود.
	if useTools {
		a.mu.Lock()
		wsPath := strings.TrimSpace(a.settings.WorkspacePath)
		a.mu.Unlock()
		if wsPath != "" && len(history) > 0 && history[0].Role == "system" {
			if facts := a.projectFactsPromptLine(wsPath); facts != "" {
				history[0].Content = historyContentString(history[0].Content) + "\n" + facts
				counter.invalidate()
			}
			// نقشهٔ مخزن (repomap.go) — تصویری فشرده از نمادهای هر فایل تا
			// ایجنت پیش از هر جست‌وجو بداند چه چیزی کجاست. یک‌بار در هر نوبت،
			// بیرون از a.mu و با بودجهٔ کران‌دار.
			if rmap := a.repoMapPromptLine(wsPath); rmap != "" {
				history[0].Content = historyContentString(history[0].Content) + "\n" + rmap
				counter.invalidate()
			}
		}
	}
	// محافظ حلقه‌ی تکرار (doom-loop) — مالک این گوروتین؛ فقط همین‌جا صدا
	// زده می‌شود (تک‌نخی، بدون قفل). «یک نوبت» = کل این streamChat.
	guard := &toolLoopGuard{}
	// بازبینی/ترمیم تاریخچه‌ی ورودی — مشکلات فقط لاگ می‌شوند؛
	// فایل گفتگو روی دیسک دست نمی‌خورد.
	if fixed, problems := validateHistory(history); len(problems) > 0 {
		for _, p := range problems {
			logAppErrorFor(a, "context", p)
		}
		history = fixed
		counter.invalidate()
	}
	compactedNotified := false // اعلان فارسی فشرده‌سازی خودکار: فقط یک‌بار در نوبت
	overflowRetried := false   // بازیابی سرریز کانتکست: فقط یک تلاش مجدد در نوبت
	stallRetries := 0          // سکون پیش از خروجی: سقف تلاش مجدد مثل سیاست شبکه
	// دروازهٔ پایان کار: تذکرهای «چک‌لیست باز» (تا دو بار) و تذکر
	// راستی‌آزمایی (یک‌بار، همان سیاست A.2).
	finishNudges := 0
	verifyNudged := false
	// راستی‌آزمایی خودکار (گام ۵): حداکثر یک‌بار در هر نوبت.
	autoVerified := false
	// بازیابی «پاسخ خاموش»: حداکثر یک‌بار در هر نوبت؛ thinking دورِ
	// بی‌پاسخ برای پیام ماندگار نگه داشته می‌شود (دور بازیابی آن را
	// بازنویسی می‌کند).
	silentRecovered := false
	silentThinking := ""
	// سقف توکن هر نوبت (A.4): اعلان ۸۰٪ یک‌بار؛ عبور از سقف → پایان مودبانه.
	tokenCapWarned := false
	tokenCapHit := false
	// فهرست کارها فقط وقتی فشرده‌سازی واقعاً رخ می‌دهد از گفتگو خوانده
	// می‌شود (یک‌بار) تا نشانگر «حذف کانتکست» طرح کار را زنده نگه دارد (A.5).
	todosSeeded := false
	markerWithTodos := func(h []chatMessage) []chatMessage {
		if !todosSeeded {
			todosSeeded = true
			if !turnRec.hasTodos() {
				a.mu.Lock()
				if conv, cerr := a.loadConversation(convID); cerr == nil {
					turnRec.setTodos(conv.Todos)
				}
				a.mu.Unlock()
			}
		}
		return enrichContextDropMarker(h, turnRec.todosBlock())
	}
	// turnSpentNow مصرف تجمعی این نوبت (همه‌ی دورها + زیرایجنت‌ها) برای سقف
	// توکن (A.4). تخمین فقط وقتی اضافه می‌شود که usage واقعی نیامده باشد.
	// بسته به متغیرهای حلقه بسته می‌شود، پس هر بار مقدار تازه می‌دهد.
	turnSpentNow := func(roundText string, estimated bool) int64 {
		subP, subC := usageTally.snapshot()
		n := promptTokens + completionTokens + subP + subC
		if estimated {
			n += estTotal + estimateTokens(roundText)
		}
		return n
	}
	for round := 0; ; round++ {
		estTotal = counter.totalOf(history)

		// نسبت کالیبراسیون تخمین↔واقعی این گفتگو — فقط در منطق
		// بودجه اعمال می‌شود (آستانه‌ی فشرده‌سازی/بازیابی سرریز)؛ تخمین‌های
		// نمایشی (estTotal برای finishChat) دست‌نخورده می‌مانند. با تقسیم
		// بودجه هم‌ارز «تخمین×نسبت» است — جزئیات در tokencalib.go.
		calibRatio := globalTokenCalib.ratioFor(convID, history)

		// فشرده‌سازی خودکار در ≥۸۰٪ بودجه — فقط کپیِ ارسالی به مدل
		// کوچک می‌شود؛ گفتگوی ذخیره‌شده کامل می‌ماند.
		if budget > 0 && int64(float64(estTotal)*calibRatio) >= budget*compactTriggerPercent/100 {
			// گام ۸: پیش از حذف، خلاصهٔ مدل + واقعیت‌های سنجاق‌شده ساخته
			// می‌شود (best-effort و کران‌دار — خطا فقط لاگ می‌شود).
			if nh, ch := compactHistoryForBudget(history, calibratedBudget(budget, calibRatio), counter, false,
				func(dropped []chatMessage) string {
					return a.summarizeDroppedMessages(ctx, dropped, pinnedFactsFrom(dropped, compactFactsLimit))
				}); ch {
				history = nh
				if fixed, problems := validateHistory(history); len(problems) > 0 {
					for _, p := range problems {
						logAppErrorFor(a, "context", p)
					}
					counter.invalidate()
					history = fixed
				}
				history = markerWithTodos(history) // طرح کار در نشانگر فشرده‌سازی (A.5)
				estTotal = counter.totalOf(history)
				if !compactedNotified {
					compactedNotified = true
					runtimeEmit(a, "chat:notice", map[string]interface{}{
						"conversationId": convID,
						"text":           "حجم گفتگو به آستانه‌ی پنجره‌ی کانتکست رسید؛ بخش‌های قدیمی به‌صورت خودکار فشرده شدند (فایل گفتگو دست‌نخورده است).",
					})
				}
			}
		}

		reqBody := chatRequest{Model: model, Messages: history, Stream: true}
		if maxTokens > 0 {
			reqBody.MaxTokens = maxTokens
		}
		// «میزان فکر کردن» — پیش‌فرض low تا استدلال اضافی توکن نسوزاند؛
		// Provider ناشناس یک‌بار ۴۰۰ می‌دهد و بعد از آن پارامتر حذف می‌شود.
		if e := thinkingEffortForProvider(thinkingEffort, baseURL); e != "" {
			reqBody.ReasoningEffort = e
		}
		if supportsTemperature(model) {
			t := temperature
			reqBody.Temperature = &t
		}
		if useTools && !droppedTools {
			reqBody.Tools = tools
		}
		// کش پرامپت (فاز ۲) — پیش‌فرض خاموش؛ فقط بلوک پایدار (system + آخرین
		// tool) نشانه می‌خورد و نشانه روی کپی می‌نشیند. اگر سرویس‌دهنده آن را
		// رد کند، پایین‌تر یک‌بار بدون آن تلاش می‌شود و در حافظه علامت می‌خورد.
		cacheHints := false
		if a.promptCacheEnabled() && providerSupportsPromptCache(baseURL) {
			reqBody, cacheHints = withPromptCacheHints(reqBody)
		}
		timing.noteCacheHints(cacheHints)
		// توکن‌های واقعی از سرویس‌دهنده (آخرین chunk استریم). اگر Provider این
		// پارامتر را نشناسد، dialStream خطای ۴۰۰ را برمی‌گرداند و پایین‌تر یک‌بار
		// بدون آن تلاش می‌شود؛ حافظه‌ی per-provider در toolguard.go است.
		if providerSupportsStreamUsage(baseURL) {
			reqBody.StreamOptions = &chatStreamOptions{IncludeUsage: true}
		}
		body, _ := json.Marshal(reqBody)
		// فاز اتصال با تلاش مجدد: هر تلاش یک Request تازه (بدنه‌ی مصرف‌شدنی است).
		// فقط قبل از خواندن نخستین بایت SSE تلاش مجدد می‌شود؛ پس از آن هر
		// شکستی مسیر savePartial/abort فعلی خودش را دارد (بدون دوباره‌کاری).
		// نگهبان سکون این دور: هر بایت خوانده‌شده (حتی کامنت keep-alive)
		// فعالیت است؛ قطع بی‌صدای سرویس‌دهنده پس از مهلت لغو می‌شود.
		// آغاز زمان‌گیری دور (شامل dial، هدرها و خواندن کامل استریم).
		roundStart := time.Now()
		idleGuard, reqCtx := newStreamIdleGuard(ctx, streamIdleTimeout)
		resp, err := a.dialStream(reqCtx, func() (*http.Request, error) {
			req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "text/event-stream")
			if key != "" {
				req.Header.Set("Authorization", "Bearer "+key)
			}
			return req, nil
		}, func(rec retryRecord) {
			runtimeEmit(a, "chat:notice", map[string]interface{}{
				"conversationId": convID,
				"text":           rec.noticeText(),
			})
		})
		if err != nil {
			// پرچم نگهبان قبل از Stop خوانده می‌شود؛ پس از Stop دیگر معنا ندارد.
			stalledByGuard := idleGuard.Stalled()
			idleGuard.Stop()
			if ctx.Err() != nil {
				aborted = true
				finalize(allContent.String()[persistedLen:], aborted)
				return
			}
			// لغوِ نگهبان سکون، "context canceled" خام برمی‌گرداند و ctx والد هم
			// پاک است؛ پیش‌تر همین حالت خطای فنی انگلیسی می‌شد و مسیر تلاش مجدد
			// (که فقط بعد از خواندن بدنه بررسی می‌شد) دور می‌خورد.
			if stalledByGuard {
				stallRetries++
				if stallRetries < retryMaxAttempts {
					runtimeEmit(a, "chat:notice", map[string]interface{}{
						"conversationId": convID,
						"text": fmt.Sprintf("پاسخ از سرویس‌دهنده قطع شد؛ تلاش مجدد %s از %s …",
							faDigits(stallRetries+1), faDigits(retryMaxAttempts)),
					})
					if werr := retrySleepFn(ctx, retryDelay(stallRetries-1, "", time.Now())); werr != nil {
						aborted = true
						finalize(allContent.String()[persistedLen:], aborted)
						return
					}
					round-- // تلاش مجدد، دور را نمی‌سوزاند
					continue
				}
				a.emitChatError(myGen, fmt.Sprintf("دریافت پاسخ متوقف شد (بدون داده برای %s ثانیه). می‌توانید «ادامه» را بزنید.", faDigits(streamIdleSec)))
				return
			}
			a.emitChatError(myGen, friendlyStreamError(err))
			return
		}
		// فقط از این لحظه (رسیدن پاسخ) سکون شمرده می‌شود — صف/تأخیر هدرها
		// «سکون استریم» نیست و نباید درخواست را بکشد.
		idleGuard.Arm()

		if resp.StatusCode != http.StatusOK {
			reason := readErrorBody(resp.Body)
			resp.Body.Close()
			idleGuard.Stop()
			// ۴۰۰ِ ناشی از stream_options → یک‌بار بدون آن (حافظه‌ی per-provider
			// نشانه‌گذاری شد؛ دورِ بعد اصلاً فرستاده نمی‌شود). قبل از بررسیِ
			// «ابزارها پشتیبانی نمی‌شوند» چک می‌شود تا خطایی که به هر دو اشاره
			// دارد، اول stream_options را کنار بگذارد.
			// عقب‌نشینی دور نمی‌سوزاند و چون حافظه‌ی per-provider
			// یک‌بار در نوبت فعال می‌شود، حلقه‌ی بی‌پایان ممکن نیست.
			if reqBody.StreamOptions != nil && isStreamOptionsUnsupported(resp.StatusCode, reason) {
				markStreamUsageBroken(baseURL)
				round--
				continue
			}
			// ۴۰۰ِ مربوط به cache_control → یک‌بار بدون نشانه‌های کش (حافظه‌ی
			// per-provider؛ دور بعد اصلاً فرستاده نمی‌شود). مثل stream_options،
			// این عقب‌نشینی دور را مصرف نمی‌کند و حلقه‌ی بی‌پایان ممکن نیست.
			if cacheHints && isPromptCacheUnsupported(resp.StatusCode, reason) {
				markPromptCacheBroken(baseURL)
				round--
				continue
			}
			// ۴۰۰ِ مربوط به reasoning_effort → یک‌بار بدون آن؛ مثل بقیه، دور مصرف نمی‌شود.
			if dropThinkingEffortOnRejection(reqBody.ReasoningEffort, resp.StatusCode, reason, baseURL) {
				round--
				continue
			}
			if useTools && !droppedTools && isToolsUnsupportedError(resp.StatusCode, reason) {
				droppedTools = true
				runtimeEmit(a, "chat:notice", map[string]interface{}{
					"conversationId": convID,
					"text":           "این مدل از فراخوانی ابزار پشتیبانی نمی‌کند؛ ادامه به‌صورت چت معمولی (بدون دسترسی به فایل‌ها) است.",
				})
				round-- // عقب‌نشینی دور نمی‌سوزاند — پرچم droppedTools یک‌بار در نوبت
				continue
			}
			// سرریز کانتکست — بعد از چک stream_options و tools تا
			// خطای مربوط به آن دو، اول مسیر مخصوص خودش را بردارد.
			// فشرده‌سازی تهاجمی + فقط یک تلاش مجدد در هر نوبت؛
			// تلاش دومِ ناموفق یا حالتِ «چیزی برای حذف نبود» → خطا.
			if !overflowRetried && isContextOverflowError(resp.StatusCode, reason) {
				overflowRetried = true
				// بودجه‌ی کالیبره‌شده: تخمین‌ها برای این گفتگو ممکن است
				// کم‌برآورد باشند؛ بازیابی با بودجه‌ی مؤثر انجام می‌شود.
				if nh, shrank := recoverHistoryFromOverflow(history, calibratedBudget(budget, calibRatio), counter); shrank {
					history = nh
					if fixed, problems := validateHistory(history); len(problems) > 0 {
						for _, p := range problems {
							logAppErrorFor(a, "context", p)
						}
						counter.invalidate()
						history = fixed
					}
					history = markerWithTodos(history) // فهرست کارها پس از فشرده‌سازی (A.5)
					runtimeEmit(a, "chat:notice", map[string]interface{}{
						"conversationId": convID,
						"text":           "حجم کانتکست از حد مجاز سرویس‌دهنده بزرگ‌تر بود؛ قدیمی‌ترین بخش‌ها حذف/فشرده شد و یک‌بار دیگر تلاش شد (فایل گفتگو دست‌نخورده است).",
					})
					round-- // تلاش مجددِ سرریز دور نمی‌سوزاند — overflowRetried یک‌بار در نوبت
					continue
				}
			}
			// خطای سهمیه/اعتبار (۴۰۲ یا ۴۲۹ «insufficient quota/balance»):
			// نگهبان مصرف را خبر می‌کند تا شارژ بعدیِ کاربر خودکار شناسایی
			// شود و شمارنده‌ها بی‌دخالت کاربر صفر شوند (usageguard.go).
			if a.autoUsageGuardEnabled() && isQuotaError(resp.StatusCode, reason) {
				a.noteProviderQuotaError(a.activeProviderID())
			}
			// بدنهٔ خام (حتی HTML چندکیلوبایتی) هرگز در گفتگو/UI نوشته نمی‌شود؛
			// پیام کوتاه فارسی + حداکثر ۱۸۰ نویسه جزئیات (httperror.go).
			a.emitChatError(myGen, humanizeProviderHTTPError(resp.StatusCode, reason))
			return
		}
		// پاسخ موفق: اگر پیش‌تر خطای سهمیه دیده بودیم، همین یعنی سرویس
		// دوباره شارژ شده — نگهبان شمارنده‌ها را خودکار صفر می‌کند.
		if a.autoUsageGuardEnabled() {
			a.noteProviderSuccess(a.activeProviderID())
		}

		// ---------- استریم یک مرحله ----------
		var roundContent strings.Builder
		var roundThinking strings.Builder
		// زمان‌سنج «فکر کردن» همین دور: از اولین دلتای استدلال تا اولین متن/ابزار
		// (یا پایان دور). سمت سرور اندازه‌گیری می‌شود تا در فایل گفتگو بماند.
		var roundThinkStart, roundThinkEnd time.Time
		visEmitted, thinkEmitted := 0, 0
		// «فکر کردن» فقط در حالت ایجنت معنا دارد؛ در چت معمولی نه ارسال و نه
		// ذخیره می‌شود. دلتاها هم دسته‌ای (هر ۴۰ms) فرستاده می‌شوند: تمیزکاری
		// کامل متن در هر چانک SSE برای پاسخ‌های بلند O(n²) بود و اندازه‌گیری
		// ~۱.۴ ثانیه CPU برای پاسخ ۴۰ کیلوبایتی می‌داد.
		showThinking := agentOn
		lastFlush := time.Now()
		thinkSent := 0
		flushStream := func(force bool) {
			if !force && time.Since(lastFlush) < streamDeltaFlushEvery {
				return
			}
			lastFlush = time.Now()
			vis, th := cleanAssistantContent(roundContent.String())
			if showThinking && len(th) > thinkEmitted {
				roundThinking.WriteString(th[thinkEmitted:])
				thinkEmitted = len(th)
			}
			if showThinking && roundThinking.Len() > thinkSent {
				if roundThinkStart.IsZero() {
					roundThinkStart = time.Now()
				}
				runtimeEmit(a, "chat:thinking", map[string]interface{}{
					"conversationId": convID,
					"delta":          roundThinking.String()[thinkSent:],
					"elapsed":        int64(time.Since(started).Seconds()),
				})
				thinkSent = roundThinking.Len()
			}
			if len(vis) > visEmitted {
				if !roundThinkStart.IsZero() && roundThinkEnd.IsZero() {
					roundThinkEnd = time.Now()
				}
				runtimeEmit(a, "chat:delta", map[string]interface{}{
					"conversationId": convID,
					"delta":          vis[visEmitted:],
					"elapsed":        int64(time.Since(started).Seconds()),
				})
				visEmitted = len(vis)
			}
		}
		pend := map[int]*pendingToolCall{}
		var roundPrompt, roundCompletion int64
		// آخرین finish_reason غیرتهی این دور — «length» یعنی خروجی مدل به‌خاطر
		// سقف توکن برش خورده (آرگومان‌های ابزار ممکن است JSON نیمه‌کاره باشند).
		roundFinish := ""

		// تجزیه‌ی SSE مشترک با زیرایجنت (توابع readSSEChunks در همین فایل):
		// اسکنر، پیشوند data:، [DONE]، خطوط نامعتبر و بررسی لغو آن‌جا هستند؛
		// رفتار این حلقه (رویدادها، ذخیره‌ی جزئی، reasoning، usage) تغییری نکرده.
		var roundModelErr string
		scanErr := readSSEChunks(reqCtx, idleGuard.Body(resp.Body), func(chunk sseChunk) bool {
			// TTFT: نخستین دلتای محتوا/استدلال/فراخوانی ابزار = نخستین توکن.
			for _, ch := range chunk.Choices {
				if ch.Delta.Content != "" || ch.Delta.ReasoningContent != "" || ch.Delta.Reasoning != "" || len(ch.Delta.ToolCalls) > 0 {
					timing.noteFirstToken()
					break
				}
			}
			if chunk.Error != nil && chunk.Error.Message != "" {
				// خطای مدل: توقف خواندن؛ رسیدگی (ذخیره‌ی جزئی + رویداد) پایین‌تر
				roundModelErr = chunk.Error.Message
				return true
			}
			if chunk.Usage != nil {
				// فقط مقادیر مثبت را بگیر؛ چانک میانی با صفر نباید رقم واقعی را پاک کند
				if p := chunk.Usage.prompt(); p > roundPrompt {
					roundPrompt = p
				}
				if c := chunk.Usage.completion(); c > roundCompletion {
					roundCompletion = c
				}
				// اگر فقط total آمده، اختلاف را به completion نسبت بده
				if roundPrompt == 0 && roundCompletion == 0 {
					if t := chunk.Usage.total(); t > 0 {
						roundCompletion = t
					}
				} else if tot := chunk.Usage.total(); tot > roundPrompt+roundCompletion {
					// total بزرگ‌تر از جمع — باقی را به completion اضافه کن
					roundCompletion = tot - roundPrompt
				}
			}
			for _, ch := range chunk.Choices {
				// ثبت finish_reason (چانک آخر: «stop»/«tool_calls»/«length») —
				// «length» یعنی خروجی مدل برش خورده و آرگومان ابزار ممکن است
				// JSON نیمه‌کاره باشد؛ بعداً به execToolCtx سرایت می‌کند.
				if ch.FinishReason != nil && *ch.FinishReason != "" {
					roundFinish = *ch.FinishReason
				}
				// reasoning_content / reasoning — کانال جدا برای «فکر کردن» مدل
				rc := ch.Delta.ReasoningContent
				if rc == "" {
					rc = ch.Delta.Reasoning
				}
				if rc != "" && showThinking {
					roundThinking.WriteString(rc)
					// دلتای استدلال هم مثل متن باید زنده به UI برسد. پیش از این
					// flushStream فقط با دلتای content صدا زده می‌شد؛ مدلی که
					// دقیقه‌ها فقط reasoning_content می‌فرستد تا پایان همان دور
					// هیچ chat:thinkingای تولید نمی‌کرد: UI روی «در حال
					// آماده‌سازی…» می‌ماند و کاربر فکر می‌کرد ایجنت هنگ کرده؛
					// کل فکر تازه پس از «توقف»/پایان دور یک‌جا ظاهر می‌شد.
					// نخستین دلتا فوری (پس UI بلافاصله «فکر کردن» را می‌بیند)،
					// بقیه با همان گلوگاه ۴۰ms تا استریم ارزان بماند.
					flushStream(thinkSent == 0)
				}
				if ch.Delta.Content != "" {
					roundContent.WriteString(ch.Delta.Content)
					// جداسازی <think> و حذف [TOOL_CALL] خام از خروجی قابل‌نمایش
					flushStream(false)
				}
				for _, tc := range ch.Delta.ToolCalls {
					p := pend[tc.Index]
					if p == nil {
						p = &pendingToolCall{}
						pend[tc.Index] = p
					}
					if tc.ID != "" {
						p.id = tc.ID
					}
					if tc.Function.Name != "" {
						p.name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						p.args.WriteString(tc.Function.Arguments)
					}
				}
			}
			return false
		})
		resp.Body.Close()
		idleGuard.Stop()
		// پایان زمان دور — پیش از بررسی خطاها، تا دورهای ناموفق هم شمرده شوند.
		timing.noteRound(time.Since(roundStart))
		flushStream(true) // دلتاهای باقی‌مانده‌ی این دور پیش از هر خطا/پایان
		// ---------- سکون استریم (Stage 2) ----------
		// لغوِ عمدی کاربر (ctx والد) هرگز «سکون» گزارش نمی‌شود؛ پرچم نگهبان
		// منبع حقیقت است.
		if idleGuard.Stalled() && ctx.Err() == nil {
			stallNotice := fmt.Sprintf("دریافت پاسخ متوقف شد (بدون داده برای %s ثانیه). می‌توانید «ادامه» را بزنید.", faDigits(streamIdleSec))
			if roundContent.Len() == 0 && roundThinking.Len() == 0 && len(pend) == 0 &&
				roundPrompt == 0 && roundCompletion == 0 {
				// پیش از هر خروجی → خطای موقت و قابل تلاش مجدد (مثل خطای شبکه)
				stallRetries++
				if stallRetries < retryMaxAttempts {
					runtimeEmit(a, "chat:notice", map[string]interface{}{
						"conversationId": convID,
						"text": fmt.Sprintf("پاسخ از سرویس‌دهنده قطع شد؛ تلاش مجدد %s از %s …",
							faDigits(stallRetries+1), faDigits(retryMaxAttempts)),
					})
					if werr := retrySleepFn(ctx, retryDelay(stallRetries-1, "", time.Now())); werr != nil {
						aborted = true
						finalize(allContent.String()[persistedLen:], aborted)
						return
					}
					round-- // تلاش مجدد، دور را نمی‌سوزاند
					continue
				}
				a.emitChatError(myGen, stallNotice)
				return
			}
			// پس از خروجی/دلتای ابزار → بدون تلاش مجدد (تکرار اثر جانبی ممنوع)؛
			// پاسخ نیمه‌کاره دقیقاً مثل مسیر abort ذخیره می‌شود و «ادامه» می‌ماند.
			promptTokens += roundPrompt
			completionTokens += roundCompletion
			a.savePartial(convID, allContent.String()[persistedLen:]+roundContent.String(), promptTokens, completionTokens, model)
			a.emitTurnSummary(convID, turnID, turnRec, promptTokens, completionTokens)
			a.emitChatError(myGen, stallNotice)
			return
		}
		if roundModelErr != "" {
			promptTokens += roundPrompt
			completionTokens += roundCompletion
			a.savePartial(convID, allContent.String()[persistedLen:]+roundContent.String(), promptTokens, completionTokens, model)
			a.emitTurnSummary(convID, turnID, turnRec, promptTokens, completionTokens)
			// پیام مدل هم کران‌دار و پاک می‌شود (هرگز HTML/بدنهٔ بلند در گفتگو)
			a.emitChatError(myGen, "خطای مدل: "+sanitizeErrorSnippet(roundModelErr, maxSnippetRunes))
			return
		}
		if scanErr != nil && ctx.Err() == nil {
			promptTokens += roundPrompt
			completionTokens += roundCompletion
			// لاگ تشخیصیِ قطع استریم — همان داده‌ای که برای تفکیک «باگ اپ» از
			// «Provider/پروکسی» لازم است: Provider، مدل، بایت‌های محتوا و
			// استدلالِ دریافت‌شده، finish_reason و متن خطای شبکه.
			a.mu.Lock()
			providerID := a.settings.Provider
			a.mu.Unlock()
			logAppErrorFor(a, "stream", fmt.Sprintf(
				"stream cut: provider=%s model=%s bytes=%d finish=%q reason=%s",
				providerID, model, roundContent.Len()+roundThinking.Len(), roundFinish,
				sanitizeErrorSnippet(scanErr.Error(), 160)))
			switch {
			case roundFinish == "":
				// مدل هیچ سیگنال پایانی نفرستاد → واقعاً نیمه‌کاره؛ «ادامه» لازم است.
				a.savePartial(convID, allContent.String()[persistedLen:]+roundContent.String(), promptTokens, completionTokens, model)
				a.emitTurnSummary(convID, turnID, turnRec, promptTokens, completionTokens)
				a.emitChatError(myGen, "جریان پاسخ قطع شد: "+streamReadErrorText(scanErr))
				return
			case roundFinish == "tool_calls" && len(pend) > 0:
				// دورِ ابزار کامل شده و فقط بستن استریم غیراستاندارد بود؛ حلقه
				// باید بدون خطا فراخوانی‌ها را اجرا کند (فروافتادن به مسیر عادی).
			default:
				// مدل پاسخ را تمام کرده بود (finish_reason رسید) و اتصال فقط
				// بدون [DONE] بسته شد → پیام خنثی، ذخیرهٔ کامل و بدون «ادامه».
				runtimeEmit(a, "chat:notice", map[string]interface{}{
					"conversationId": convID,
					"text":           streamClosedAfterFinishNotice(roundFinish),
				})
				finalize(allContent.String()[persistedLen:]+roundContent.String(), false)
				return
			}
		}
		if ctx.Err() != nil {
			aborted = true
		}
		// مدت «فکر کردن» این دور؛ اگر متنی نیامده باشد تا پایان دور حساب می‌شود.
		var roundThinkMs int64
		if !roundThinkStart.IsZero() {
			endThink := roundThinkEnd
			if endThink.IsZero() {
				endThink = time.Now()
			}
			if d := endThink.Sub(roundThinkStart).Milliseconds(); d > 0 {
				roundThinkMs = d
				myGen.thinkMs += d
			}
		}

		promptTokens += roundPrompt
		completionTokens += roundCompletion
		// ---------- مصرف زنده (دایره‌ی توکن) ----------
		// بعد از هر دور، مصرف واقعیِ همان درخواست (اگر API usage داده باشد) یا
		// تخمین همان دور به UI می‌رسد تا دایره در طول پاسخ ایجنت - که چند دور
		// طول می‌کشد - زنده بماند؛ chat:done فقط عدد نهایی را قطعی می‌کند.
		// «دقیق» فقط وقتی که prompt_tokens واقعی از API آمده باشد؛ اگر فقط
		// completion یا total آمده بود، عدد نمایشی تخمین است.
		liveCtx, liveExact := roundPrompt, roundPrompt > 0
		if liveCtx <= 0 {
			liveCtx = estTotal
		}
		a.mu.Lock()
		liveLimit := a.contextLimitLocked()
		a.mu.Unlock()
		runtimeEmit(a, "chat:usage", map[string]interface{}{
			"conversationId":  convID,
			"contextTokens":   liveCtx,
			"tokenLimit":      liveLimit,
			"roundPrompt":     roundPrompt,
			"roundCompletion": roundCompletion,
			"turnTokens":      promptTokens + completionTokens,
			"exact":           liveExact,
		})
		// کالیبراسیون تخمین توکن: نسبتِ prompt_tokens واقعیِ همین
		// درخواست به تخمین estTotal (که برای همین درخواست محاسبه شده بود)
		// ثبت می‌شود؛ دورهای بدون usage یا با تخمین کوچک داخل observe رد می‌شوند.
		globalTokenCalib.observe(convID, roundPrompt, estTotal)
		// ---------- سقف توکن هر نوبت (A.4) ----------
		// مصرف تجمعی همه‌ی دورهای این نوبت + آمار زیرایجنت‌ها؛ وقتی usage
		// واقعی نبود، تخمین کالیبره‌نشده‌ی همان دور به آن اضافه می‌شود.
		spent := turnSpentNow(roundContent.String(), roundPrompt == 0 && roundCompletion == 0)
		if turnTokenCap > 0 {
			if !tokenCapWarned && spent*100 >= turnTokenCap*tokenCapWarnPercent {
				tokenCapWarned = true
				runtimeEmit(a, "chat:notice", map[string]interface{}{
					"conversationId": convID,
					"text": "مصرف توکن این نوبت به ۸۰٪ «سقف توکن هر نوبت» رسید (" + faGroup(spent) +
						" از " + faGroup(turnTokenCap) + ")؛ پس از پر شدن سقف، نوبت با خیال راحت متوقف می‌شود.",
				})
			}
			if spent >= turnTokenCap {
				tokenCapHit = true
				turnRec.noteTokenCap(turnTokenCap)
			}
		}
		// محتوای خام را تمیز کن؛ thinking جدا نگه داشته می‌شود
		visFinal, thFinal := cleanAssistantContent(roundContent.String())
		if thFinal != "" && roundThinking.Len() == 0 {
			roundThinking.WriteString(thFinal)
		}
		// اگر از reasoning_content آمده و thFinal هم هست، تکراری ننویس
		allContent.WriteString(visFinal)
		// thinking را روی gen برای finishChat نگه می‌داریم
		if showThinking {
			myGen.thinking = roundThinking.String()
			if myGen.thinking == "" {
				myGen.thinking = thFinal
			}
		}

		if aborted {
			finalize(allContent.String()[persistedLen:], true)
			return
		}

		calls := collectToolCalls(pend)
		if !useTools || droppedTools || len(calls) == 0 {
			// ---------- تذکر راستی‌آزمایی (A.2) ----------
			// فایلی تغییر کرده، بعد از آخرین تغییر هیچ راستی‌آزماییِ موفقی
			// اجرا نشده، حداقل یک دور دیگر جا هست و این تذکر قبلاً داده نشده
			// → یک پیام انگلیسی به‌عنوان «user» و ادامه‌ی حلقه. دور و محافظ
			// تکرار مصرف نمی‌شوند و پیام فقط یک‌بار در نوبت می‌آید.
			// ---------- دروازهٔ پایان کار ----------
			// مدل می‌خواهد تمام کند ولی چک‌لیست باز دارد یا آخرین تغییر
			// راستی‌آزمایی نشده؟ تا سقف دو تذکر ادامه می‌دهیم؛ توقف کاربر،
			// سقف توکن و سقف دورها هرگز نقض نمی‌شوند.
			canFinishNudge := finishNudges < maxFinishGateNudges
			canVerifyNudge := !verifyNudged && (turnRec.needsVerification() || turnRec.needsTestVerification())
			if agentOn && useTools && !droppedTools && !tokenCapHit && ctx.Err() == nil &&
				(canFinishNudge || canVerifyNudge) && round+1 < maxRounds {
				openTodos := turnRec.openTodos()
				needsVerify := turnRec.needsVerification()
				// «آخرین راستی‌آزمایی فقط build/vet بود»: یعنی تستی که رفتار را
				// بسنجد اجرا نشده؛ vet تحلیل ایستاست، نه تست.
				needsTest := !needsVerify && turnRec.needsTestVerification()
				nudge, notice := "", ""
				finishGate := false
				if len(openTodos) > 0 && finishNudges < maxFinishGateNudges {
					nudge = finishGateNudge(openTodos, needsVerify || needsTest, turnRec.changedFiles())
					notice = finishGateNotice(len(openTodos), finishNudges+1)
					finishGate = true
				} else if (needsVerify || needsTest) && !verifyNudged {
					// پوشش راستی‌آزمایی (فاز ۲): فقط «یک build/vet اجرا شد» کافی
					// نیست؛ دایرکتوری‌های تغییرکرده و یک دستور تست هدفمند
					// پیشنهاد می‌شود (verifycover.go). اگر پروژه هیچ دستور تستی
					// ندارد، برای «فقط build» تذکر نمی‌دهیم (فقط سروصدا است).
					a.mu.Lock()
					wsCover := strings.TrimSpace(a.settings.WorkspacePath)
					a.mu.Unlock()
					cmds := a.projectFactsFor(wsCover).Commands
					if needsVerify || hasTestCommand(cmds) {
						verifyNudged = true
						nudge, notice, _ = verificationNudgePayload(turnRec.changedFiles(), turnRec.lastVerificationCommand(), cmds)
					}
				}
				if nudge != "" {
					if finishGate {
						finishNudges++
					}
					if c := strings.TrimSpace(visFinal); c != "" {
						history = append(history, chatMessage{Role: "assistant", Content: c})
					}
					history = append(history, chatMessage{Role: "user", Content: nudge})
					counter.invalidate()
					runtimeEmit(a, "chat:notice", map[string]interface{}{
						"conversationId": convID,
						"text":           notice,
					})
					continue
				}
			}
			// ---------- بازیابی «پاسخ خاموش» (باگ فکر-بدون-جواب) ----------
			// دوری که هیچ متن قابل‌نمایشی نداشت، قبلاً نوبت را کاملاً بی‌صدا
			// تمام می‌کرد (کاربر فقط بلوک فکر یا هیچ‌چیز می‌دید). حالا یک‌بار
			// در هر نوبت با دستور صریح ادامه می‌دهیم؛ دلیل اصلی این حالت در
			// مدل‌های reasoning، نوشتن کد داخل استدلال و خوردن به سقف توکن
			// خروجی است.
			// شرط بر اساس visFinal است، نه len(pend): tool_callهای بی‌نام و
			// فراخوانی‌های ابزارِ خاموش/حذف‌شده هم باید همین مسیر را بگیرند،
			// وگرنه دقیقاً همان سکوت قبلی برمی‌گردد.
			if strings.TrimSpace(visFinal) == "" {
				if !silentRecovered && round+1 < maxRounds {
					silentRecovered = true
					silentThinking = roundThinking.String()
					if silentThinking == "" {
						silentThinking = thFinal
					}
					history = append(history, chatMessage{
						Role:    "user",
						Content: silentTurnRecoveryText(roundFinish == "length", !useTools || droppedTools),
					})
					counter.invalidate()
					runtimeEmit(a, "chat:notice", map[string]interface{}{
						"conversationId": convID,
						"text":           silentTurnRecoveryNotice(roundFinish == "length"),
					})
					round-- // بازیابی دورِ بودجه را مصرف نمی‌کند (مثل تذکر راستی‌آزمایی)
					continue
				}
				// بازیابی هم پاسخ نداد (یا جای دور نمانده): به‌جای پایان
				// خاموش، پیام ماندگار در گفتگو ذخیره می‌شود تا کاربر دلیل
				// نبود پاسخ و راه‌حل را ببیند. thinking دورِ اول حفظ می‌شود.
				if silentThinking != "" && !strings.Contains(myGen.thinking, silentThinking) {
					if strings.TrimSpace(myGen.thinking) == "" {
						myGen.thinking = silentThinking
					} else {
						myGen.thinking = strings.TrimSpace(silentThinking + "\n" + myGen.thinking)
					}
				}
				hadThinking := silentThinking != "" || roundThinking.Len() > 0 || strings.TrimSpace(myGen.thinking) != ""
				finalize(silentTurnNotice(roundFinish == "length", hadThinking), false)
				return
			}
			finalize(allContent.String()[persistedLen:], false)
			return
		}

		if round >= maxRounds {
			pc, _ := cleanAssistantContent(roundContent.String())
			a.persistAgentRound(convID, pc, nil, nil, roundThinkMs)
			// سقف دورها: نوبت با توقف کاربر یکی نیست؛ پرچم جدا می‌شود تا UI
			// به‌جای «متوقف شد» دکمهٔ «ادامه بده» نشان دهد.
			myGen.unfinished = true
			finalize("\n\n⚠️ حداکثر تعداد مراحل اجرای ابزار ("+faDigits(maxRounds)+" دور) پر شد؛ کار نیمه‌تمام ماند. برای ادامه، دکمهٔ «ادامه بده» را بزنید.", false)
			return
		}

		histContent, _ := cleanAssistantContent(roundContent.String())
		history = append(history, chatMessage{
			Role:      "assistant",
			Content:   histContent,
			ToolCalls: calls,
		})

		// ---------- اجرای ابزارها ----------
		// truncated: خروجی مدل در همین دور برش خورده (finish_reason=length) —
		// به execToolCtx سرایت می‌کند تا پیام خطای JSON نیمه‌کاره دقیق باشد.
		truncated := roundFinish == "length"
		// تصمیم‌های محافظ تکرار پیش از اجرا و به ترتیب اصلی —
		// محافظ تک‌نخی می‌ماند و رفتارش عیناً مثل اجرای ترتیبی است.
		tasks, turnStopped, stoppedTool := planToolCalls(calls, guard)
		// دنباله‌های فقط‌خواندنی با سقف ۴ worker موازی اجرا می‌شوند؛
		// تغییردهنده‌ها ترتیبی و مثل سد می‌مانند. outcomeها همیشه به
		// ترتیب اصلی‌اند (جزئیات و تضمین‌ها: toolbatch.go).
		outcomes := a.executeToolCalls(ctx, convID, tasks, truncated, a.execToolCtx)
		toolMsgs := make([]Message, 0, len(calls))
		var shotURLs []string
		for i, c := range calls {
			o := outcomes[i]
			result := o.result
			var shotPath string
			if c.Function.Name == "browser_screenshot" {
				// نشانهٔ اسکرین‌شات از متن ابزار حذف می‌شود؛ تصویر جدا ضمیمه می‌شود.
				result, shotPath = splitShotMarker(result)
			}
			var images []string
			if shotPath != "" && o.ok && visionOn && len(shotURLs) < visionMaxShots {
				if url, ierr := visionImageDataURL(shotPath); ierr == nil {
					images = []string{url}
					shotURLs = append(shotURLs, url)
				} else {
					result += "\n(note: the screenshot could not be attached to the conversation: " + ierr.Error() + ")"
				}
			}
			toolMsgs = append(toolMsgs, Message{
				ID:         newID(),
				Role:       "tool",
				Content:    result,
				Images:     images,
				At:         time.Now().Unix(),
				ToolName:   c.Function.Name,
				ToolArgs:   c.Function.Arguments,
				ToolCallID: c.ID,
				ToolOK:     o.ok,
				ToolMs:     o.ms,
			})
			history = append(history, chatMessage{
				Role:       "tool",
				Content:    result,
				ToolCallID: c.ID,
			})
		}
		if len(shotURLs) > 0 {
			// پروتکل tool فقط متن می‌پذیرد؛ PNG در یک پیام کاربریِ چندبخشی
			// ضمیمه می‌شود تا مدل خودِ تصویر را ببیند.
			parts := make([]contentPart, 0, len(shotURLs)+1)
			parts = append(parts, contentPart{Type: "text",
				Text: "Screenshot(s) captured by browser_screenshot this turn are attached. Inspect the image(s) directly and describe only what you can actually see."})
			for _, u := range shotURLs {
				parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageRef{URL: u}})
			}
			history = append(history, chatMessage{Role: "user", Content: parts})
		}

		// ---------- راستی‌آزمایی خودکار (گام ۵) ----------
		// این دور فایلی تغییر داده و از آخرین تغییر راستی‌آزمایی موفقی نبوده؟
		// یک دستور ساخت/تایپ‌چکِ ثابت اجرا می‌شود؛ فقط شکست به مدل داده می‌شود
		// تا همان‌جا علت را رفع کند (autoverify.go).
		if shouldAutoVerify(useTools, droppedTools, autoVerified, round, maxRounds, turnRec, a.autoVerifyEnabled()) {
			autoVerified = true
			a.mu.Lock()
			wsVerify := strings.TrimSpace(a.settings.WorkspacePath)
			a.mu.Unlock()
			runs, skipped := a.autoVerifyTurn(ctx, wsVerify, turnRec.changedFiles())
			for _, run := range runs {
				turnRec.recordCommand(run.Command, run.ExitCode, run.OK)
				timing.noteTool(run.Ms)
			}
			if len(runs) > 0 {
				noticeText, _ := autoVerifyNotice(runs, skipped)
				runtimeEmit(a, "chat:notice", map[string]interface{}{
					"conversationId": convID,
					"text":           noticeText,
				})
				if last := runs[len(runs)-1]; !last.OK {
					history = append(history, chatMessage{Role: "user", Content: autoVerifyFailedMessage(last)})
					counter.invalidate()
				}
			} else if skipped != "" && ctx.Err() == nil {
				// فقط یک‌بار برای هر (پوشه، دلیل) لاگ می‌شود — نه هر نوبت.
				a.noteAutoVerifySkip(wsVerify, skipped)
			}
		}

		pc, _ := cleanAssistantContent(roundContent.String())
		if !a.persistAgentRound(convID, pc, calls, toolMsgs, roundThinkMs) {
			a.emitTurnSummary(convID, turnID, turnRec, promptTokens, completionTokens)
			a.emitChatError(myGen, "ذخیره‌ی نتیجه‌ی ابزار ناموفق بود (گفتگو حذف شد یا دیسک در دسترس نیست).")
			return
		}
		persistedLen = allContent.Len()
		// ---------- توقف مودبانه با سقف توکن (A.4) ----------
		// دسته‌ی ابزار این دور اجرا و روی دیسک ذخیره شد؛ حالا متوقف می‌شویم
		// تا گفتگو در وضعیت پروتکل‌سالم بماند و «ادامه» هم ممکن باشد.
		// مصرف را بعد از اجرای ابزار دوباره می‌سنجیم: آمار زیرایجنت (ابزار
		// task) همین حالا به tally اضافه شده و باید در سقف حساب شود.
		spent = turnSpentNow(roundContent.String(), roundPrompt == 0 && roundCompletion == 0)
		if turnTokenCap > 0 && spent >= turnTokenCap {
			tokenCapHit = true
			turnRec.noteTokenCap(turnTokenCap)
		}
		if tokenCapHit {
			finalize(tokenCapStopNotice(spent, turnTokenCap), false)
			return
		}
		// توقف نوبت توسط محافظ حلقه — همه‌ی فراخوانی‌های دسته نتیجه گرفت و
		// روی دیسک ذخیره شدند؛ حالا پیام نهایی فارسی به کاربر.
		if turnStopped {
			finalize(doomLoopStopNotice(stoppedTool, faDigits(toolRepeatStopRun)), false)
			return
		}
	}
}

// finishChat پیام نهایی دستیار را ذخیره و رویداد پایان را می‌فرستد.
// model مدلِ همین نوبت است — مصرف به تفکیک مدل ثبت و به UI گزارش می‌شود.
func (a *App) finishChat(myGen *genState, model, content string, aborted bool, promptTokens, completionTokens, estPrompt int64) {
	convID := myGen.convID

	// آزادسازی gen قبل از هر emit (فقط اگر همان gen خودمان باشد)
	a.genMu.Lock()
	if a.gen == myGen {
		a.gen = nil
	}
	a.genMu.Unlock()
	a.clearGenAllowWrites()

	// اگر API usage داده باشد فقط همان ثبت می‌شود (بدون مخلوط با تخمین).
	// اگر هیچ usage نباشد → تخمین محلی و علامت غیر دقیق.
	// فقط وقتی دقیق است که prompt_tokens واقعی از API آمده باشد؛ در غیر این صورت
	// بخشی از عدد (prompt) تخمین محلی است و برچسب «تخمینی» باید بماند.
	exact := promptTokens > 0
	if !exact {
		if completionTokens == 0 && content != "" {
			completionTokens = estimateTokens(content)
		}
		if promptTokens == 0 && estPrompt > 0 {
			promptTokens = estPrompt
		}
	}
	total := promptTokens + completionTokens

	a.mu.Lock()
	conv, err := a.loadConversation(convID)
	if err != nil {
		a.mu.Unlock()
		if !aborted {
			runtimeEmit(a, "chat:error", map[string]interface{}{
				"conversationId": convID,
				"error":          "ذخیره‌ی پاسخ ناموفق بود: گفتگو در دسترس نیست",
			})
		}
		return
	}
	// تمیزکاری نهایی (دفاعی) — اگر از مسیر دیگری آمده باشد
	vis, th := cleanAssistantContent(content)
	if th == "" {
		th = myGen.thinking
	} else if myGen.thinking != "" && !strings.Contains(th, myGen.thinking) {
		th = strings.TrimSpace(myGen.thinking + "\n" + th)
	}
	mode := "chat"
	if a.settings.AgentMode {
		mode = "agent"
	}
	msg := Message{
		ID:         newID(),
		Role:       "assistant",
		Content:    vis,
		Thinking:   th,
		ThinkingMs: myGen.thinkMs,
		ThinkingTokens: func() int64 {
			if th == "" {
				return 0
			}
			return estimateTokens(th)
		}(),
		At:         time.Now().Unix(),
		Tokens:     completionTokens,
		Aborted:    aborted,
		Unfinished: myGen.unfinished && !aborted,
		Mode:       mode,
	}
	if vis != "" || th != "" || aborted || msg.Unfinished {
		conv.Messages = append(conv.Messages, msg)
	}
	a.recordTokenUsageLocked(conv, model, promptTokens, completionTokens, exact)
	saveFailed := false
	if err := a.saveConversation(conv); err != nil {
		logAppError("save", "finishChat: "+err.Error())
		saveFailed = true
	}
	if serr := a.saveSettings(); serr != nil {
		logAppError("save", "finishChat settings: "+serr.Error())
	}
	used, limit, providerUsed, chatAPI, tokensExact, recent := a.usageSnapshotForUI(conv)
	// مصرف تجمعی همین مدل نوبت — از سطلِ مدل (نه Provider) تا مصرف مدل‌های
	// مختلف یک Provider با هم قاطی نشود. بعد از recordTokenUsageLocked خوانده
	// می‌شود، پس مصرف همین نوبت هم داخلش هست (هم‌راستا با providerUsed).
	modelUsed := int64(0)
	if m := strings.TrimSpace(model); m != "" && a.settings.TokensByModel != nil {
		modelUsed = a.settings.TokensByModel[m]
	}
	// عدد کانتکست همراه خود گفتگو هم فرستاده می‌شود تا UI بدون بازمحاسبه
	// سمت کلاینت (که پیام‌های tool را نادیده می‌گرفت) همان مقدار را نشان دهد.
	conv.ContextTokens = used
	a.mu.Unlock()

	// اعلان ذخیره‌ی ناموفق — بیرون از قفل: a.mu هرگز روی runtimeEmit نگه
	// داشته نمی‌شود (ترتیب قفل در ARCHITECTURE.md؛ ممیزی Stage 5.4).
	if saveFailed {
		runtimeEmit(a, "chat:notice", map[string]interface{}{
			"conversationId": convID,
			"text":           "هشدار: ذخیره‌ی این گفتگو روی دیسک ناموفق بود (جزئیات در لاگ خطاها).",
		})
	}

	runtimeEmit(a, "chat:done", map[string]interface{}{
		"conversationId":     convID,
		"message":            msg,
		"conversation":       conv,
		"tokens":             total,
		"tokensExact":        exact && tokensExact,
		"aborted":            aborted,
		"unfinished":         msg.Unfinished,
		"tokensUsed":         used,
		"tokenLimit":         limit,
		"providerTokensUsed": providerUsed,
		"modelTokensUsed":    modelUsed,
		"chatTokensUsed":     chatAPI,
		"recentUsage":        recent,
	})
}

// friendlyStreamError متن خطاهای زمینه (context) را برای کاربر قابل‌فهم
// می‌کند؛ پیش از این «Post ...: context canceled» خام و انگلیسی در گفتگو
// می‌ماند و کاربر هیچ راهنمایی نمی‌گرفت.
func friendlyStreamError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "context canceled"):
		return "درخواست پیش از دریافت پاسخ لغو شد (خطای شبکه نبود). اگر خودتان متوقف نکردید، دوباره بفرستید؛ در صورت تکرار، مهلت سکون را در تب «مدل و رفتار» بیشتر کنید."
	case strings.Contains(msg, "context deadline exceeded"):
		return "زمان انتظار برای پاسخ سرویس‌دهنده تمام شد. دوباره بفرستید یا مهلت سکون را در تب «مدل و رفتار» بیشتر کنید."
	}
	return msg
}

// emitChatError خطا را به فرانت‌اند می‌فرستد و gen را (اگر همان gen ما بود)
// قبل از emit پاک می‌کند.
func (a *App) emitChatError(myGen *genState, msg string) {
	a.genMu.Lock()
	if a.gen == myGen {
		a.gen = nil
	}
	a.genMu.Unlock()
	a.clearGenAllowWrites()
	// ثبت شفاف در لاگ خطاها — قابل مشاهده از تنظیمات
	logAppErrorFor(a, "chat", msg)
	// خطا را در گفتگو هم ماندگار کن؛ توست chat:error گذراست و کاربر
	// پس از چند ثانیه دلیل نبود پاسخ را از دست می‌داد.
	a.persistChatError(myGen.convID, msg)
	runtimeEmit(a, "chat:error", map[string]interface{}{
		"conversationId": myGen.convID,
		"error":          msg,
	})
}

// persistChatError پیام خطای پایان نوبت را به‌صورت یک پیام دستیار در گفتگو
// ذخیره می‌کند تا با بسته‌شدن توست گذرا، خطا از دید کاربر پاک نشود. اگر
// گفتگو در دسترس نباشد (حذف‌شده/دیسک خراب) فقط لاگ می‌شود و چیزی تغییر نمی‌کند.
func (a *App) persistChatError(convID, msg string) {
	text := strings.TrimSpace(msg)
	if text == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	conv, err := a.loadConversation(convID)
	if err != nil {
		return
	}
	conv.Messages = append(conv.Messages, Message{
		ID:      newID(),
		Role:    "assistant",
		Content: "⚠️ " + text,
		At:      time.Now().Unix(),
	})
	if err := a.saveConversation(conv); err != nil {
		logAppError("save", "persistChatError: "+err.Error())
	}
}

// StopGeneration توقف پاسخ در جریان.
func (a *App) StopGeneration() {
	defer a.recoverBound("StopGeneration")
	a.genMu.Lock()
	g := a.gen
	a.genMu.Unlock()
	if g != nil {
		g.cancel()
	}
}

// stopGenerationAndWait پاسخ فعال را متوقف می‌کند و تا خروج کامل گوروتین
// استریم (بسته‌شدن done) یا انقضای مهلت انتظار می‌کند. تضمین می‌کند پس از
// بازگشتش، هیچ ذخیره‌ی دیرهنگامی نتواند گفتگوی تازه‌حذف‌شده را دوباره
// روی دیسک بسازد.
func (a *App) stopGenerationAndWait(timeout time.Duration) {
	a.genMu.Lock()
	g := a.gen
	a.genMu.Unlock()
	if g == nil {
		return
	}
	g.cancel()
	if g.done == nil {
		return
	}
	select {
	case <-g.done:
	case <-time.After(timeout):
	}
}

func (a *App) activeConvID() string {
	a.genMu.Lock()
	defer a.genMu.Unlock()
	if a.gen != nil {
		return a.gen.convID
	}
	return ""
}

// decryptKey خواندن و رمزگشایی کلید ذخیره‌شده‌ی یک Provider.
// باید با a.mu قفل‌شده صدا زده شود (از prepareStream).
// مهاجرت تنبل: مقدار با فرمت قدیمی که با کلید legacy باز شد، همان‌جا با
// کلید Keyring (v2) رمز و ذخیره می‌شود — کاربر بی‌خبر از تغییر فرمت.
func (a *App) decryptKey(providerID string) string {
	enc := a.settings.APIKeys[providerID]
	if enc == "" {
		return ""
	}
	plain, err := decryptString(enc)
	if err != nil {
		return ""
	}
	if isLegacyCiphertext(enc) {
		if re, rerr := encryptString(plain); rerr == nil {
			a.settings.APIKeys[providerID] = re
			if serr := a.saveSettings(); serr != nil {
				logAppError("save", "key migration to keyring: "+serr.Error())
			}
		}
	}
	return plain
}

// estimateTokens تخمین سرانگشتی توکن برای متن‌های فارسی/انگلیسی.
//
// بهینه‌سازی: یک پاس روی رشته بدون تخصیص []rune. نسبت‌ها به‌صورت صحیح
// مقایسه می‌شوند (latin*10 > total*7 معادل ratio > 0.7 است) تا از
// تقسیم اعشاری و تبدیل float جلوگیری شود.
func estimateTokens(s string) int64 {
	var total, latin int64
	for _, r := range s {
		total++
		if r < 0x0600 {
			latin++
		}
	}
	if total == 0 {
		return 0
	}
	// نسبت لاتین > 0.7 ⟺ latin*10 > total*7
	if latin*10 > total*7 {
		return total * 3 / 8 // ≈ /2.7
	}
	return total * 2 / 5 // ≈ /2.5
}

// readErrorBody متن خطای HTTP را تا حد معقولی می‌خواند.
func readErrorBody(r io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(r, 4*1024))
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &payload) == nil && payload.Error.Message != "" {
		return payload.Error.Message
	}
	txt := strings.TrimSpace(string(raw))
	if txt == "" {
		return "پاسخ نامشخص"
	}
	return txt
}

// quickPing یک درخواست بسیار کوچک برای «تست اتصال».
func quickPing(baseURL, key, model string) error {
	body, _ := json.Marshal(map[string]interface{}{
		"model":      model,
		"messages":   []chatMessage{{Role: "user", Content: "ping"}},
		"max_tokens": 1,
		"stream":     false,
	})
	err := postPing(baseURL, key, body)
	if err != nil && isMaxTokensError(err) {
		body, _ = json.Marshal(map[string]interface{}{
			"model":    model,
			"messages": []chatMessage{{Role: "user", Content: "ping"}},
			"stream":   false,
		})
		err = postPing(baseURL, key, body)
	}
	return err
}

// isMaxTokensError تشخیص خطای «پارامتر max_tokens پشتیبانی نمی‌شود».
func isMaxTokensError(err error) bool {
	if err == nil {
		return false
	}
	m := err.Error()
	return strings.Contains(m, "max_tokens") || strings.Contains(m, "max_completion_tokens")
}

func postPing(baseURL, key string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := pingHTTPClient
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("اتصال ناموفق بود: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, readErrorBody(resp.Body))
}

// sanitizeImages تصاویر ورودی را اعتبارسنجی و محدود می‌کند:
// فقط data URL تصویری، حداکثر ۳ عدد، هر کدام حداکثر ~۲.۸MB (نوشتن ~۲MB).
func sanitizeImages(images []string) []string {
	const maxImages = 3
	const maxDataURL = 2800 * 1024
	out := make([]string, 0, maxImages)
	for _, img := range images {
		if len(out) >= maxImages {
			break
		}
		img = strings.TrimSpace(img)
		if !strings.HasPrefix(img, "data:image/") || !strings.Contains(img, ";base64,") {
			continue // فقط data URL تصویری معتبر
		}
		if len(img) > maxDataURL {
			continue // بزرگ‌تر از حد — بی‌صدا رد می‌شود (UI قبلش محدود کرده)
		}
		out = append(out, img)
	}
	return out
}
