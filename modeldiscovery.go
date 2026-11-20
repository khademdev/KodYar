// تشخیص خودکار مدل‌ها (مأموریت ۵ / نسخه ۱.۹.۰).
//
// کاربر فقط کلید API (و ترجیحاً Base URL) می‌دهد؛ KodYar خودش فهرست مدل‌های
// در دسترس سرویس‌دهنده را می‌پرسد. مکانیزم:
//
//	۱) مسیر استاندارد سازگار با OpenAI:   GET {base}/models با «Authorization: Bearer»
//	   (OpenAI، OpenRouter، Groq، DeepSeek، Mistral، LM Studio، Ollama،
//	   vLLM، LiteLLM و تقریباً هر gateway سازگار با OpenAI)
//	۲) مسیر بومی Google AI Studio:        GET {host}/v1beta/models با هدر «x-goog-api-key»
//	   (پاسخ شامل supportedGenerationMethods است؛ فقط مدل‌های
//	   generateContent دار نگه داشته می‌شوند)
//	۳) مسیر سازگار OpenAI گوگل (پایه‌ی …/v1beta/openai): مثل حالت ۱
//
// اگر Base URL خالی باشد، از پیشوند کلید حدس زده می‌شود:
// «AIza…» → Google AI Studio، «sk-or-…» → OpenRouter، «gsk_…» → Groq.
// برای Providerهای ذخیره‌شده، Base URL و کلید ذخیره‌شده هم به‌کار می‌رود.
//
// امنیت: کلید هرگز در خطاها/لاگ ظاهر نمی‌شود (redactKey)، بدنه‌ی پاسخ
// محدود است (۴MB)، طرح URL فقط http/https و timeout سخت ۲۰ ثانیه است.
package main

import (
	"bytes"
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

// ModelInfo یک مدل شناسایی‌شده از سرویس‌دهنده.
type ModelInfo struct {
	ID            string `json:"id"`
	DisplayName   string `json:"displayName,omitempty"`
	OwnedBy       string `json:"ownedBy,omitempty"`
	ContextWindow int    `json:"contextWindow,omitempty"`
	ChatLikely    bool   `json:"chatLikely"`
}

// discoveryMaxBody سقف بدنه‌ی پاسخ فهرست مدل‌ها — فهرست واقعی حتی با صدها
// مدل چند صد کیلوبایت است؛ ۴MB برای جلوگیری از exhausted-memory کافی است.
const discoveryMaxBody = 4 << 20

// discoveryHTTPClient کلاینت مخصوص تشخیص مدل‌ها — پروکسی‌آگاه
// (chatTransport) و با timeout سخت؛ در تست‌ها با سرور محلی جایگزین می‌شود.
var discoveryHTTPClient = &http.Client{
	Timeout:   20 * time.Second,
	Transport: chatTransport,
}

// DiscoverModels فهرست مدل‌های در دسترس سرویس‌دهنده را برمی‌گرداند.
//
// baseURL خالی و providerID معنی باشد → آدرس و کلید ذخیره‌شده‌ی همان Provider
// به‌کار می‌رود؛ اگر باز هم آدرس خالی بود، از پیشوند کلید حدس زده می‌شود.
// apiKey خالی و providerID معنی باشد → کلید ذخیره‌شده (رمزگشایی‌شده) استفاده
// می‌شود (مگر NoKey). نتیجه: ابتدا مدل‌های چت‌مانند، بعد بقیه؛ هر گروه الفبایی
// و بدون تکرار.
func (a *App) DiscoverModels(baseURL, apiKey, providerID string) ([]ModelInfo, error) {
	defer a.recoverBound("DiscoverModels")
	baseURL = strings.TrimSpace(baseURL)
	apiKey = strings.TrimSpace(apiKey)
	providerID = strings.TrimSpace(providerID)

	// آدرس/کلید ذخیره‌شده‌ی Provider (مسیر کوتاه برای دکمه‌ی «تشخیص خودکار»)
	if providerID != "" {
		a.mu.Lock()
		p := a.findProvider(providerID)
		if p != nil {
			if baseURL == "" {
				baseURL = strings.TrimSpace(p.BaseURL)
			}
			if apiKey == "" && !p.NoKey {
				apiKey = a.decryptKey(providerID)
			}
		}
		a.mu.Unlock()
	}

	// آدرس خالی → حدس از روی پیشوند کلید (مثل OpenCode؛ فقط برای
	// ارائه‌دهنده‌هایی که کلیدشان یکتاست و حدس اشتباه ندارد)
	if baseURL == "" {
		if guessed := guessEndpointFromKey(apiKey); guessed != "" {
			baseURL = guessed
		} else {
			return nil, errors.New("برای تشخیص خودکار، Base URL را وارد کنید یا از کلید Google AI Studio / OpenRouter / Groq استفاده کنید")
		}
	}

	// همان مرز ورودی SaveProvider: فقط http/https (فایل/سوکت محلی رد می‌شود)
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, errors.New("Base URL باید با http:// یا https:// شروع شود")
	}

	return discoverProviderModels(baseURL, apiKey)
}

// discoverProviderModels کشف مدل‌ها بدون قفل و بدون خواندن تنظیمات —
// نقطه‌ی مشترک DiscoverModels و SaveProvider (پس از تشخیص آدرس پایه).
func discoverProviderModels(baseURL, apiKey string) ([]ModelInfo, error) {
	var list []ModelInfo
	var err error
	switch classifyEndpoint(baseURL) {
	case endpointGoogleNative:
		if apiKey == "" {
			return nil, errors.New("برای Google AI Studio کلید API لازم است (کلید با «AIza» شروع می‌شود)")
		}
		list, err = discoverGoogleNative(baseURL, apiKey)
	default:
		list, err = discoverOpenAICompat(baseURL, apiKey)
	}
	if err != nil {
		return nil, err
	}
	return sortAndDedupModels(list), nil
}

// ---------------------------------------------------------------- kinds ----

type endpointKind int

const (
	endpointOpenAI       endpointKind = iota // سازگار با OpenAI — GET {base}/models با Bearer
	endpointGoogleNative                     // ListModels بومی گوگل — /v1beta/models با x-goog-api-key
)

// classifyEndpoint بر اساس میزبان/مسیر، نوع endpoint را مشخص می‌کند.
// خالص و بدون شبکه — تست‌پذیر.
func classifyEndpoint(rawURL string) endpointKind {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return endpointOpenAI
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, "generativelanguage.googleapis.com") {
		if strings.Contains(strings.ToLower(u.Path), "/openai") {
			// پایه‌ی سازگار OpenAI گوگل (…/v1beta/openai) — همان مسیر استاندارد
			return endpointOpenAI
		}
		return endpointGoogleNative
	}
	return endpointOpenAI
}

// guessEndpointFromKey از پیشوند کلید، Base URL پیش‌فرض را حدس می‌زند.
// عمداً برای «sk-» عمومی حدسی وجود ندارد (OpenAI/DeepSeek و ده‌ها gateway
// از این الگو استفاده می‌کنند) — حدس اشتباه بدتر از پرسیدن آدرس است.
func guessEndpointFromKey(key string) string {
	key = strings.TrimSpace(key)
	switch {
	case strings.HasPrefix(key, "AIza"):
		return "https://generativelanguage.googleapis.com/v1beta"
	case strings.HasPrefix(key, "sk-or-"):
		return "https://openrouter.ai/api/v1"
	case strings.HasPrefix(key, "gsk_"):
		return "https://api.groq.com/openai/v1"
	default:
		return ""
	}
}

// ---------------------------------------------------------- openai path ----

type openAIModelsResp struct {
	Data []openAIModelEntry `json:"data"`
}

type openAIModelEntry struct {
	ID            string `json:"id"`
	Object        string `json:"object"`
	OwnedBy       string `json:"owned_by"`
	Name          string `json:"name"`           // OpenRouter: نام نمایشی «OpenAI: GPT-4o»
	ContextWindow int64  `json:"context_window"` // OpenRouter
	ContextLength int64  `json:"context_length"` // بعضی gatewayها
	MaxModelLen   int64  `json:"max_model_len"`  // vLLM
}

// discoverOpenAICompat مسیر استاندارد GET {base}/models.
func discoverOpenAICompat(baseURL, key string) ([]ModelInfo, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, errors.New("Base URL نامعتبر است")
	}
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	raw, status, err := fetchDiscoveryBody(req, key)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, discoveryHTTPError(status, readErrorBody(bytes.NewReader(raw)), key)
	}
	return parseOpenAIModels(raw)
}

// parseOpenAIModels پاسخ «object با data» یا آرایه‌ی خالی‌محور را می‌خواند.
// خالص — fuzz-safe.
func parseOpenAIModels(raw []byte) ([]ModelInfo, error) {
	var resp openAIModelsResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		// بعضی سرورها آرایه‌ی خام برمی‌گردانند: [{"id":…},…]
		var arr []openAIModelEntry
		if err2 := json.Unmarshal(raw, &arr); err2 != nil {
			return nil, errors.New("پاسخ JSON معتبر فهرست مدل‌ها از سرویس دریافت نشد")
		}
		resp.Data = arr
	}
	list := make([]ModelInfo, 0, len(resp.Data))
	for _, m := range resp.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		cw := m.ContextWindow
		if cw == 0 {
			cw = m.ContextLength
		}
		if cw == 0 {
			cw = m.MaxModelLen
		}
		if cw < 0 || cw > 1<<40 {
			cw = 0 // داده‌ی نامعقول ناشناخته → «نامشخص»
		}
		display := strings.TrimSpace(m.Name)
		if display == id {
			display = ""
		}
		list = append(list, ModelInfo{
			ID:            id,
			DisplayName:   display,
			OwnedBy:       strings.TrimSpace(m.OwnedBy),
			ContextWindow: int(cw),
			ChatLikely:    chatLikelyID(id),
		})
	}
	return list, nil
}

// nonChatHints نشانه‌های مدل‌های غیرچت (embedding، صدا، تصویر و…) — فقط
// ترتیب نمایش را تعیین می‌کنند؛ هیچ مدلی حذف نمی‌شود.
var nonChatHints = []string{
	"embed", "whisper", "tts", "dall-e", "dalle", "moderation", "imagen",
	"veo", "babbage", "davinci", "transcribe", "realtime", "stable-diffusion",
	"sd3", "flux", "rerank", "guard", "music", "lyria", "aqa",
}

// chatLikelyID حدس سبک: آیا این id به‌احتمال قوی مدل چت است؟
func chatLikelyID(id string) bool {
	s := strings.ToLower(id)
	for _, h := range nonChatHints {
		if strings.Contains(s, h) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------- google path ----

type googleNativeResp struct {
	NextPageToken string             `json:"nextPageToken"`
	Models        []googleModelEntry `json:"models"`
}

type googleModelEntry struct {
	Name                       string   `json:"name"`
	DisplayName                string   `json:"displayName"`
	InputTokenLimit            int64    `json:"inputTokenLimit"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
}

// discoverGoogleNative مسیر بومی Google AI Studio — با پیگیری صفحه
// (nextPageToken) تا سقف ۳ صفحه.
func discoverGoogleNative(baseURL, key string) ([]ModelInfo, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, errors.New("Base URL نامعتبر است")
	}
	pageToken := ""
	var all []ModelInfo
	for page := 0; page < 3; page++ {
		endpoint := u.Scheme + "://" + u.Host + "/v1beta/models?pageSize=200"
		if pageToken != "" {
			endpoint += "&pageToken=" + url.QueryEscape(pageToken)
		}
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, errors.New("Base URL نامعتبر است")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("x-goog-api-key", key) // کلید در هدر — نه در URL که ممکن است جایی لاگ شود
		raw, status, err := fetchDiscoveryBody(req, key)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, discoveryHTTPError(status, readErrorBody(bytes.NewReader(raw)), key)
		}
		pageModels, next, err := parseGoogleNativeModels(raw)
		if err != nil {
			return nil, err
		}
		all = append(all, pageModels...)
		if next == "" {
			break
		}
		pageToken = next
	}
	return all, nil
}

// parseGoogleNativeModels فقط مدل‌های generateContent دار را برمی‌گرداند و
// پیشوند «models/» را از id حذف می‌کند. خالص — fuzz-safe.
func parseGoogleNativeModels(raw []byte) ([]ModelInfo, string, error) {
	var resp googleNativeResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, "", errors.New("پاسخ JSON معتبر فهرست مدل‌ها از Google دریافت نشد")
	}
	list := make([]ModelInfo, 0, len(resp.Models))
	for _, m := range resp.Models {
		id := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m.Name), "models/"))
		if id == "" {
			continue
		}
		chat := false
		for _, method := range m.SupportedGenerationMethods {
			if strings.TrimSpace(method) == "generateContent" {
				chat = true
				break
			}
		}
		if !chat {
			continue // embedding / aqa / countTokens-محض → در فهرست چت جایی ندارند
		}
		cw := m.InputTokenLimit
		if cw < 0 || cw > 1<<40 {
			cw = 0
		}
		list = append(list, ModelInfo{
			ID:            id,
			DisplayName:   strings.TrimSpace(m.DisplayName),
			OwnedBy:       "google",
			ContextWindow: int(cw),
			ChatLikely:    true,
		})
	}
	return list, strings.TrimSpace(resp.NextPageToken), nil
}

// ------------------------------------------------------------ helpers ----

// fetchDiscoveryBody بدنه‌ی پاسخ را (با سقف ۴MB) کامل می‌خواند و وضعیت
// HTTP برمی‌گرداند؛ بدنه همیشه بسته می‌شود و خطاهای شبکه با کلید
// پاک‌سازی‌شده گزارش می‌شوند.
func fetchDiscoveryBody(req *http.Request, key string) ([]byte, int, error) {
	resp, err := discoveryHTTPClient.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, 0, errors.New("اتصال به سرویس‌دهنده طول کشید (timeout) — دوباره تلاش کنید")
		}
		return nil, 0, fmt.Errorf("اتصال ناموفق بود: %s", redactKey(err.Error(), key))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, discoveryMaxBody))
	if err != nil {
		return nil, resp.StatusCode, errors.New("خواندن پاسخ سرویس‌دهنده ناموفق بود")
	}
	return raw, resp.StatusCode, nil
}

// discoveryHTTPError خطای وضعیت HTTP را با پیام فارسیِ روشن برمی‌گرداند.
func discoveryHTTPError(status int, body, key string) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("کلید API نامعتبر است یا به فهرست مدل‌ها دسترسی ندارد (HTTP %d)", status)
	case http.StatusNotFound:
		return errors.New("این سرویس فهرست مدل‌ها را ارائه نمی‌کند (HTTP 404) — نام مدل‌ها را دستی وارد کنید")
	case http.StatusTooManyRequests:
		return errors.New("محدودیت نرخ سرویس‌دهنده (HTTP 429) — چند لحظه بعد دوباره تلاش کنید")
	default:
		msg := strings.TrimSpace(body)
		if msg == "" || len(msg) > 200 {
			msg = ""
		}
		if msg != "" {
			return fmt.Errorf("خطای سرویس‌دهنده (HTTP %d): %s", status, redactKey(msg, key))
		}
		return fmt.Errorf("خطای سرویس‌دهنده (HTTP %d)", status)
	}
}

// redactKey هر رخداد کلید در متن را می‌پوشاند — کلید هرگز نباید در پیام
// خطا یا لاگ ظاهر شود (مثلاً url.Error آدرس کامل را چاپ می‌کند).
func redactKey(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "***")
}

// sortAndDedupModels مرتب‌سازی نهایی: مدل‌های چت‌مانند اول؛ داخل هر گروه
// الفبایی؛ تکراری‌ها حذف (اولین نگه داشته می‌شود).
func sortAndDedupModels(list []ModelInfo) []ModelInfo {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].ChatLikely != list[j].ChatLikely {
			return list[i].ChatLikely
		}
		return strings.ToLower(list[i].ID) < strings.ToLower(list[j].ID)
	})
	out := list[:0:0]
	seen := make(map[string]bool, len(list))
	for _, m := range list {
		k := strings.ToLower(m.ID)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, m)
	}
	return out
}
