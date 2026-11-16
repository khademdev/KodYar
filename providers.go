// مدیریت Provider‌های کاربر — هر سرویس‌دهنده‌ی سازگار با API نوع OpenAI را
// کاربر خودش تعریف می‌کند. از این نسخه، کاربر فقط «نام» و «کلید API» می‌دهد؛
// آدرس پایه از روی نام/کلید تشخیص داده می‌شود و فهرست مدل‌ها و اندازه‌ی
// پنجره‌ی کانتکست مستقیماً از خود سرویس‌دهنده خوانده می‌شود (modeldiscovery.go).
//
// از نسخه‌ی ۱.3.0 هیچ Provider از پیش تعریف‌شده‌ای وجود ندارد؛ نسخه‌های
// قدیمی که Providerهای پیش‌فرض (OpenAI، Groq و…) در تنظیماتشان مانده،
// یک‌بار هنگام بارگذاری مهاجرت می‌شوند (stripBuiltinProviders در settings.go).
package main

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"unicode"
)

// builtinProviderSignatures امضای Providerهای پیش‌فرض نسخه‌های قدیمی —
// فقط برای مهاجرت یک‌باره‌ی تنظیمات قدیمی به کار می‌رود.
// کلید: ID — مقدار: BaseURL (مقایسه‌ی دقیق؛ ورودی سفارشی‌شده با URL متفاوت دست نمی‌خورد).
func builtinProviderSignatures() map[string]string {
	return map[string]string{
		"openai":  "https://api.openai.com/v1",
		"groq":    "https://api.groq.com/openai/v1",
		"hormoz":  "https://api.hormoz.ai/v1",
		"ollama":  "http://localhost:11434/v1",
		"mistral": "https://api.mistral.ai/v1",
	}
}

// providerBaseURLAliases نام‌های آشنای سرویس‌دهنده‌ها → آدرس پایه‌ی API.
// کلید با normalizeProviderNameKey ساخته می‌شود (حروف کوچک، بدون فاصله/نقطه/
// خط تیره/زیرخط). فقط سرویس‌های سازگار با API نوع OpenAI اینجا هستند.
func providerBaseURLAliases() map[string]string {
	return map[string]string{
		"openai":       "https://api.openai.com/v1",
		"chatgpt":      "https://api.openai.com/v1",
		"openrouter":   "https://openrouter.ai/api/v1",
		"groq":         "https://api.groq.com/openai/v1",
		"deepseek":     "https://api.deepseek.com/v1",
		"mistral":      "https://api.mistral.ai/v1",
		"mistralai":    "https://api.mistral.ai/v1",
		"ollama":       "http://localhost:11434/v1",
		"lmstudio":     "http://localhost:1234/v1",
		"together":     "https://api.together.xyz/v1",
		"togetherai":   "https://api.together.xyz/v1",
		"xai":          "https://api.x.ai/v1",
		"grok":         "https://api.x.ai/v1",
		"google":       "https://generativelanguage.googleapis.com/v1beta/openai",
		"gemini":       "https://generativelanguage.googleapis.com/v1beta/openai",
		"googlegemini": "https://generativelanguage.googleapis.com/v1beta/openai",
		"cerebras":     "https://api.cerebras.ai/v1",
		"fireworks":    "https://api.fireworks.ai/inference/v1",
		"moonshot":     "https://api.moonshot.cn/v1",
		"kimi":         "https://api.moonshot.cn/v1",
		"qwen":         "https://dashscope.aliyuncs.com/compatible-mode/v1",
		"dashscope":    "https://dashscope.aliyuncs.com/compatible-mode/v1",
		"zhipu":        "https://open.bigmodel.cn/api/paas/v4",
		"glm":          "https://open.bigmodel.cn/api/paas/v4",
		"nvidia":       "https://integrate.api.nvidia.com/v1",
		"perplexity":   "https://api.perplexity.ai",
		"pplx":         "https://api.perplexity.ai",
		"sambanova":    "https://api.sambanova.ai/v1",
		"hyperbolic":   "https://api.hyperbolic.xyz/v1",
		"github":       "https://models.inference.ai.azure.com",
		"hormoz":       "https://api.hormoz.ai/v1",
	}
}

// normalizeProviderNameKey نام را برای تطبیق با فهرست آشناها نرمال می‌کند.
func normalizeProviderNameKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch r {
		case ' ', '-', '_', '.', '\u200c', '\t':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// inferProviderBaseURL آدرس پایه‌ی سرویس را فقط از «نام» و «کلید» حدس می‌زند:
//  1. پیشوند یکتای کلید (AIza… گوگل، sk-or-… اوپن‌روتر، gsk_… گروک)؛
//  2. نام‌های آشنای سرویس‌ها (openai، openrouter، …)؛
//  3. اگر نام خودش دامنه باشد (مثل «api.example.com/v1» یا «localhost:1234»)
//     همان به‌عنوان آدرس استفاده می‌شود — راه فرار برای سرویس‌های ناشناس.
//
// برای کلید عمومی «sk-…» عمداً حدسی زده نمی‌شود (OpenAI/DeepSeek و ده‌ها
// gateway همین الگو را دارند و حدس اشتباه، کلید را به سرویس اشتباه می‌فرستد).
func inferProviderBaseURL(name, apiKey string) string {
	if u := guessEndpointFromKey(apiKey); u != "" {
		return u
	}
	if u := aliasBaseURLFromName(name); u != "" {
		return u
	}
	// نام آزاد که یک دامنه در خود دارد: «سرور من api.example.com/v1»
	if u := domainTokenBaseURL(name); u != "" {
		return u
	}

	raw := strings.TrimSpace(name)
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	raw = strings.TrimRight(raw, "/")
	return normalizeBaseURLHost(raw)
}

// normalizeBaseURLHost «host» یا «host/path» را به یک Base URL کامل تبدیل
// می‌کند؛ نامی که دامنه نیست (مثل «MyCorp») رد می‌شود.
func normalizeBaseURLHost(raw string) string {
	if raw == "" || strings.ContainsAny(raw, " 	#?") {
		return ""
	}
	host, path := raw, ""
	if k := strings.IndexByte(raw, '/'); k >= 0 {
		host, path = raw[:k], raw[k:]
	}
	hostOnly := host
	if k := strings.IndexByte(host, ':'); k >= 0 {
		hostOnly = host[:k]
	}
	ip := net.ParseIP(strings.Trim(hostOnly, "[]"))
	loopback := hostOnly == "localhost" || hostOnly == "127.0.0.1" || hostOnly == "[::1]" || (ip != nil && ip.IsLoopback())
	if !loopback && ip == nil {
		if !strings.Contains(hostOnly, ".") {
			return "" // نام آزاد (مثل «MyCorp») آدرس نیست
		}
		// آخرین برچسب باید یک TLD حرفی باشد؛ وگرنه «v1.2» هم دامنه شمرده می‌شد.
		labels := strings.Split(hostOnly, ".")
		tld := labels[len(labels)-1]
		if len(tld) < 2 {
			return ""
		}
		for _, r := range tld {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
				return ""
			}
		}
	}
	scheme := "https"
	if loopback {
		scheme = "http"
	}
	if path != "" {
		return scheme + "://" + raw
	}
	return scheme + "://" + raw + "/v1"
}

// domainTokenBaseURL نخستین «توکن دامنه‌مانند» داخل یک نام آزاد را پیدا
// می‌کند (مثل «سرور من — api.example.com/v1») تا کاربر فقط با یک نام هم
// بتواند سرویس سفارشی‌اش را ثبت کند.
func domainTokenBaseURL(name string) string {
	splitters := func(r rune) bool {
		if unicode.IsSpace(r) {
			return true
		}
		switch r {
		case '(', ')', '[', ']', '{', '}', '\u00ab', '\u00bb', '\u060c', ',', '\u061b', ';', '|', '\u2014', '\u2013', '=':
			return true
		}
		return false
	}
	for _, tok := range strings.FieldsFunc(name, splitters) {
		tok = strings.Trim(tok, "`'\"")
		tok = strings.TrimPrefix(tok, "https://")
		tok = strings.TrimPrefix(tok, "http://")
		tok = strings.TrimRight(tok, "/")
		if tok == "" || strings.ContainsAny(tok, "#?@") {
			continue
		}
		if u := normalizeBaseURLHost(tok); u != "" {
			return u
		}
	}
	return ""
}

// aliasBaseURLFromName آدرس پایه را از یک «نام آزاد» تشخیص می‌دهد؛ چون در
// فرم افزودن Provider کاربر فقط یک نام نمایشی می‌دهد (مثل «OpenRouter (حساب
// شخصی)» یا «سرور LM Studio من»)، تطبیق فقط «برابری کامل» نیست:
//  1. برابری کامل نرمال‌شده (openrouter، groq، …)؛
//  2. توکن‌های لاتین نام (Word boundary) — برای نام‌های کوتاه مثل Groq/XAI؛
//  3. نام سرویس به‌عنوان زیررشته (طول ≥ ۵) — «myopenai…»، «lmstudio-personal».
func aliasBaseURLFromName(name string) string {
	aliases := providerBaseURLAliases()
	norm := normalizeProviderNameKey(name)
	if norm == "" {
		return ""
	}
	if u, ok := aliases[norm]; ok {
		return u
	}
	lower := strings.ToLower(name)
	isLatin := func(r rune) bool {
		return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
	}
	for _, tok := range strings.FieldsFunc(lower, func(r rune) bool { return !isLatin(r) }) {
		if u, ok := aliases[tok]; ok {
			return u
		}
	}
	best, bestLen := "", 0
	for k, u := range aliases {
		if len(k) < 5 {
			continue
		}
		if strings.Contains(norm, k) && len(k) > bestLen {
			best, bestLen = u, len(k)
		}
	}
	return best
}

// findProvider با شناسه، Provider را برمی‌گرداند.
func (a *App) findProvider(id string) *Provider {
	for i := range a.settings.Providers {
		if a.settings.Providers[i].ID == id {
			return &a.settings.Providers[i]
		}
	}
	return nil
}

// GetProviders فهرست Providerها را برمی‌گرداند (کپی امن بدون alias به state داخلی).
func (a *App) GetProviders() []Provider {
	defer a.recoverBound("GetProviders")
	a.mu.Lock()
	defer a.mu.Unlock()
	return copyProviders(a.settings.Providers)
}

// SaveProvider یک Provider جدید اضافه یا موجود را ویرایش می‌کند.
// کاربر فقط Name و apiKey را می‌دهد؛ آدرس پایه از نام/کلید تشخیص داده
// می‌شود و فهرست مدل‌ها و پنجره‌ی کانتکست از خود سرویس‌دهنده خوانده می‌شود.
//
// apiKey: "" → کلید قبلی حفظ می‌شود (فقط در ویرایش؛ Provider تازه کلید لازم دارد)
//
// "-" → کلید قبلی پاک می‌شود
// بقیه → رمزنگاری و ذخیره می‌شود (همه‌چیز همان‌جا که Provider تعریف می‌شود)
//
// Provider ذخیره‌شده برگردانده می‌شود تا فرانت‌اند بتواند آن را فعال کند.
func (a *App) SaveProvider(p Provider, apiKey string) (Provider, error) {
	defer a.recoverBound("SaveProvider")

	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	// در فرم جدید کاربر فقط «نام» و «کلید API» می‌دهد؛ همان نام نمایشی است.
	// آدرس پایه، مدل‌ها و پنجره‌ی کانتکست از روی همین نام/کلید و سپس از خود
	// سرویس‌دهنده تشخیص داده می‌شوند. Name فنی برای سازگاری با داده‌های قبلی
	// حفظ می‌شود و اگر خالی باشد از نام نمایشی ساخته می‌شود.
	p.DisplayName = strings.TrimSpace(p.DisplayName)
	if len([]rune(p.DisplayName)) > 80 {
		p.DisplayName = string([]rune(p.DisplayName)[:80])
	}
	if len([]rune(p.Name)) > 80 {
		p.Name = string([]rune(p.Name)[:80])
	}
	// مرز داده: شناسه‌ی وارده (مسیر ویرایش) و رنگ باید همان الگوی امنِ
	// ساخته‌شده توسط UI باشند — درخواست ساختگی/فایل دست‌کاری‌شده رد می‌شود.
	// (پیش از هر تماس شبکه‌ای، تا ورودی نامعتبر هرگز کلیدی ارسال نکند.)
	if p.ID != "" && !providerIDSafe(p.ID) {
		return Provider{}, errors.New("شناسه‌ی Provider نامعتبر است")
	}
	if p.Color != "" && !providerColorSafe(p.Color) {
		return Provider{}, errors.New("رنگ Provider باید کد هگز معتبر باشد (مثل #7c6cf0)")
	}
	if p.Color == "" {
		p.Color = "#7c6cf0"
	}

	key := strings.TrimSpace(apiKey)
	if key == "-" {
		key = ""
	}

	// عکس‌برداری از وضعیت فعلی — کشف مدل‌ها شبکه‌ای است و نباید با a.mu
	// گرفته‌شده انجام شود (کل برنامه قفل می‌ماند).
	a.mu.Lock()
	var prev *Provider
	if p.ID != "" {
		cur := a.findProvider(p.ID)
		if cur == nil {
			a.mu.Unlock()
			return Provider{}, errors.New("Provider پیدا نشد")
		}
		snapshot := *cur
		snapshot.Models = append([]string(nil), cur.Models...)
		prev = &snapshot
		if key == "" && !cur.NoKey {
			key = a.decryptKey(p.ID)
		}
	}
	a.mu.Unlock()

	// نام فنی: در ویرایش دست‌نخورده می‌ماند (هویت سرویس)، در افزودن تازه از
	// نام نمایشی ساخته می‌شود.
	if p.Name == "" && prev != nil {
		p.Name = strings.TrimSpace(prev.Name)
	}
	if p.Name == "" {
		p.Name = p.DisplayName
	}
	if p.Name == "" {
		return Provider{}, errors.New("نام سرویس‌دهنده را وارد کنید")
	}

	// آدرس پایه: ورودی صریح (فایل تنظیمات قدیمی/آزمون) → حدس از نام و کلید →
	// آدرس ذخیره‌شده‌ی همین Provider.
	baseURL := strings.TrimSpace(p.BaseURL)
	if baseURL == "" {
		baseURL = inferProviderBaseURL(p.Name, key)
	}
	if baseURL == "" && prev != nil {
		baseURL = strings.TrimSpace(prev.BaseURL)
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		return Provider{}, errors.New("سرویس‌دهنده با این نام شناسایی نشد؛ نام سرویس را واضح بنویسید (مثلاً OpenRouter) یا آدرس API آن را وارد کنید (مثلاً api.example.com/v1)")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return Provider{}, errors.New("آدرس سرویس باید با http:// یا https:// شروع شود")
	}

	// بقیه‌ی تنظیمات از خود سرویس‌دهنده: فهرست مدل‌ها + بزرگ‌ترین کانتکست.
	models, err := discoverProviderModels(baseURL, key)
	if err != nil {
		return Provider{}, fmt.Errorf("دریافت اطلاعات از سرویس‌دهنده ناموفق بود: %s", err.Error())
	}
	if len(models) == 0 {
		return Provider{}, errors.New("سرویس‌دهنده هیچ مدلی برنگرداند — نام یا کلید API را بررسی کنید")
	}
	ids := make([]string, 0, len(models))
	contextWindow := 0
	for _, m := range models {
		ids = append(ids, m.ID)
		if m.ContextWindow > contextWindow {
			contextWindow = m.ContextWindow
		}
	}
	if contextWindow <= 0 {
		contextWindow = 200000
	}

	p.BaseURL = baseURL
	p.Models = ids
	p.ContextWindow = contextWindow
	p.NoKey = key == "" // سرویس محلی بدون کلید (Ollama/LM Studio) خودکار شناسایی می‌شود

	a.mu.Lock()
	defer a.mu.Unlock()

	// شناسه‌ی خودکار برای Provider تازه
	if p.ID == "" {
		slug := slugify(p.Name)
		id := slug
		for n := 2; a.findProvider(id) != nil; n++ {
			id = slug + "-" + itoa(n)
		}
		p.ID = id
		a.settings.Providers = append(a.settings.Providers, p)
	} else {
		idx := -1
		for i := range a.settings.Providers {
			if a.settings.Providers[i].ID == p.ID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return Provider{}, errors.New("Provider پیدا نشد")
		}
		a.settings.Providers[idx] = p
	}

	// کلید API — همان‌جا که Provider تعریف می‌شود ذخیره می‌شود
	if apiKey == "-" {
		a.settings.APIKeys[p.ID] = ""
	} else if strings.TrimSpace(apiKey) != "" {
		enc, err := encryptString(strings.TrimSpace(apiKey))
		if err != nil {
			return Provider{}, err
		}
		a.settings.APIKeys[p.ID] = enc
	}

	if err := a.saveSettings(); err != nil {
		return Provider{}, err
	}
	return p, nil
}

// DeleteProvider یک Provider را حذف می‌کند. حذف آخرین Provider هم مجاز است
// (کاربر خودش Providerهایش را تعریف می‌کند)؛ در این صورت سرویس‌دهنده‌ی فعال
// پاک می‌شود و تا تعریف Provider بعدی، ارسال پیام با پیام راهنما رد می‌شود.
func (a *App) DeleteProvider(id string) error {
	defer a.recoverBound("DeleteProvider")
	a.mu.Lock()
	defer a.mu.Unlock()

	idx := -1
	for i := range a.settings.Providers {
		if a.settings.Providers[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errors.New("Provider پیدا نشد")
	}
	a.settings.Providers = append(a.settings.Providers[:idx], a.settings.Providers[idx+1:]...)
	delete(a.settings.APIKeys, id)
	if a.settings.Provider == id {
		a.settings.Provider = ""
		a.settings.Model = ""
		// اگر Provider دیگری مانده، اولین مورد فعال می‌شود
		if len(a.settings.Providers) > 0 {
			first := a.settings.Providers[0]
			a.settings.Provider = first.ID
			if len(first.Models) > 0 {
				a.settings.Model = first.Models[0]
			}
		}
	}
	return a.saveSettings()
}

// TestProvider یک درخواست کوچک برای اطمینان از درستی آدرس و کلید می‌فرستد.
func (a *App) TestProvider(providerID, model string) error {
	defer a.recoverBound("TestProvider")
	a.mu.Lock()
	p := a.findProvider(providerID)
	if p == nil {
		a.mu.Unlock()
		return errors.New("Provider پیدا نشد")
	}
	base := p.BaseURL
	key := ""
	if !p.NoKey {
		key = a.decryptKey(providerID)
	}
	a.mu.Unlock()

	if model == "" {
		a.mu.Lock()
		model = a.settings.Model
		a.mu.Unlock()
	}
	if strings.TrimSpace(model) == "" {
		return errors.New("نام مدل را وارد کنید")
	}
	return quickPing(base, key, model)
}
