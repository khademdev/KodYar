// پایش خودکار شارژ سرویس‌دهنده و صفر کردن شمارندهٔ مصرف (نسخهٔ ۱.۱۱).
//
// مشکل کاربر: تا پیش از این، صفر شدن شمارنده‌های تجمعی مصرف فقط با دکمهٔ
// دستی «صفر کردن پس از شارژ سرویس» ممکن بود؛ یعنی کاربر باید خودش حواسش
// باشد سرویس را شارژ کرده و بعد دکمه را بزند. این ماژول همان تصمیم را
// خودکار می‌گیرد:
//
//	۱) بررسی موجودی سرویس‌دهنده‌هایی که گزارش موجودی دارند (OpenRouter،
//	   DeepSeek، SiliconFlow، Moonshot) با کلید همان Provider؛ اگر موجودی
//	   جدید از آخرین موجودیِ دیده‌شده بیشتر شد → «شارژ» تشخیص داده می‌شود.
//	۲) برای هر سرویس‌دهندهٔ دیگری: اگر درخواست‌ها با خطای سهمیه/اعتبار
//	   (۴۰۲ یا ۴۲۹ «insufficient quota/balance/credit») رد شدند و بعداً
//	   درخواست موفق رفت → «شارژ» تشخیص داده می‌شود.
//
// در هر دو حالت شمارنده‌های تجمعی (کل/Provider/مدل/RecentUsage) یک‌بار
// صفر می‌شوند، رویداد usage:auto-reset به فرانت‌اند می‌رود و تاریخ و دلیل
// در تنظیمات ثبت می‌شود. هیچ مسیر دستی برای صفر کردن وجود ندارد؛ دکمهٔ
// «بررسی شارژ» فقط موجودی را دوباره می‌خواند (همان کاری که پایش خودکار
// در پس‌زمینه می‌کند) و صرفِ آن هم ممکن است شارژ را تشخیص دهد.
//
// قفل‌ها: پروب شبکه هرگز با a.mu گرفته‌شده انجام نمی‌شود (ترتیب قفل‌ها در
// ARCHITECTURE.md). usageGuardMu فقط وضعیت حافظه‌ای خودِ نگهبان را
// محافظت می‌کند و قفل برگ است.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// تنظیمات پایش — در تست‌ها بازنویسی می‌شوند.
var (
	usageGuardInterval   = 15 * time.Minute // دورهٔ پایش دوره‌ای
	usageGuardStartDelay = 45 * time.Second // اولین پایش پس از راه‌اندازی
	usageProbeTimeout    = 10 * time.Second // مهلت هر درخواست موجودی
	usageGuardMinGap     = 60 * time.Second // حداقل فاصلهٔ دو پروب یک Provider
)

// ProviderBalance آخرین موجودی شناخته‌شدهٔ یک Provider (روی دیسک می‌ماند تا
// «شارژ» با بستن برنامه گم نشود).
type ProviderBalance struct {
	Total     float64 `json:"total"`
	Currency  string  `json:"currency,omitempty"`
	UpdatedAt int64   `json:"updatedAt"`
}

// usageGuardState وضعیت حافظه‌ای نگهبان — بین پراب‌ها.
type usageGuardState struct {
	mu          sync.Mutex
	quotaSince  map[string]int64 // providerID → زمان آخرین خطای سهمیه (۰ = نبوده)
	lastProbeAt map[string]int64 // providerID → آخرین تلاش پروب (هر نتیجه)
	probing     bool             // یک‌پروبی در جریان است (single-flight)
	started     bool
}

var guardState = &usageGuardState{quotaSince: map[string]int64{}, lastProbeAt: map[string]int64{}}

// resetUsageGuardForTest وضعیت حافظه‌ای نگهبان را برای تست‌ها پاک می‌کند.
func resetUsageGuardForTest() {
	guardState.mu.Lock()
	defer guardState.mu.Unlock()
	guardState.quotaSince = map[string]int64{}
	guardState.lastProbeAt = map[string]int64{}
	guardState.probing = false
	guardState.started = false
}

// startUsageGuard حلقهٔ پایش پس‌زمینه را راه می‌اندازد (فقط یک‌بار).
func startUsageGuard(a *App) {
	guardState.mu.Lock()
	if guardState.started {
		guardState.mu.Unlock()
		return
	}
	guardState.started = true
	guardState.mu.Unlock()
	go usageGuardLoop(a)
}

// usageGuardLoop هر usageGuardInterval یک‌بار Provider فعال را پروب می‌کند؛
// با لغو ctx (بستن برنامه) تمام می‌شود.
func usageGuardLoop(a *App) {
	if a == nil || a.ctx == nil {
		return
	}
	timer := time.NewTimer(usageGuardStartDelay)
	defer timer.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-timer.C:
		}
		if pid := a.activeProviderID(); pid != "" {
			a.usageGuardProbe(pid)
		}
		timer.Reset(usageGuardInterval)
	}
}

// activeProviderID شناسهٔ Provider فعال (برای پایش) — با قفل کوتاه.
func (a *App) activeProviderID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.TrimSpace(a.settings.Provider)
}

// autoUsageGuardEnabled آیا پایش خودکار شارژ روشن است؟ (پیش‌فرض تنظیمات true.)
func (a *App) autoUsageGuardEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings.AutoUsageReset
}

// usageGuardProbe یک نمونهٔ موجودی می‌گیرد و در صورت تشخیص شارژ، شمارنده‌ها
// را خودکار صفر می‌کند. بی‌صدا: خطا فقط در لاگ می‌رود و UI را مزاحم نمی‌شود.
func (a *App) usageGuardProbe(pid string) {
	// حداقل فاصلهٔ پروب‌ها + single-flight
	guardState.mu.Lock()
	if guardState.probing {
		guardState.mu.Unlock()
		return
	}
	if now := timeNow(); now-guardState.lastProbeAt[pid] < int64(usageGuardMinGap/time.Second) {
		guardState.mu.Unlock()
		return
	}
	guardState.probing = true
	guardState.lastProbeAt[pid] = timeNow()
	guardState.mu.Unlock()
	defer func() {
		guardState.mu.Lock()
		guardState.probing = false
		guardState.mu.Unlock()
	}()

	supported, kind, endpoint := a.balanceEndpointFor(pid)
	if !supported {
		return // این سرویس‌دهنده گزارش موجودی ندارد؛ پایش فقط با خطای سهمیه
	}
	bal, perr := probeProviderBalanceWithKey(kind, endpoint, a.decryptKeyFor(pid))
	if perr != nil {
		logAppError("usage", "balance probe "+pid+": "+perr.Error())
		return
	}
	a.applyBalanceSample(pid, bal)
}

// ---------------------------------------------------------------- نقطه‌های اتصال از chat.go

// noteProviderQuotaError هنگام خطای سهمیه/اعتبار صدا زده می‌شود؛ بلافاصله
// یک پروب موجودی (اگر سرویس گزارش موجودی دارد) را زمان‌بندی می‌کند تا شارژ
// زودهنگام شناسایی شود.
func (a *App) noteProviderQuotaError(pid string) {
	pid = strings.TrimSpace(pid)
	if pid == "" {
		return
	}
	guardState.mu.Lock()
	guardState.quotaSince[pid] = timeNow()
	guardState.mu.Unlock()
	// پروب فوری در پس‌زمینه — اثر جانبی ندارد و UI را مزاحم نمی‌شود.
	go func() {
		// کمی صبر: کاربر معمولاً بلافاصله پس از خطا شارژ می‌کند؛ اما اگر
		// همین حالا شارژ شده باشد، پروب بعدی هم آن را می‌بیند.
		time.Sleep(20 * time.Second)
		if a == nil || a.ctx == nil {
			return
		}
		select {
		case <-a.ctx.Done():
			return
		default:
		}
		a.usageGuardProbe(pid)
	}()
}

// noteProviderSuccess هنگام نخستین پاسخ موفق پس از خطای سهمیه صدا زده می‌شود:
// یعنی سرویس دوباره کار می‌کند → شارژ شناسایی شد → شمارنده‌ها صفر می‌شوند.
func (a *App) noteProviderSuccess(pid string) {
	pid = strings.TrimSpace(pid)
	if pid == "" {
		return
	}
	guardState.mu.Lock()
	_, had := guardState.quotaSince[pid]
	if !had {
		guardState.mu.Unlock()
		return
	}
	delete(guardState.quotaSince, pid)
	guardState.mu.Unlock()
	a.autoResetUsage(pid, "recovered_after_quota", 0, 0, "")
}

// isQuotaError تشخیص خطای «اعتبار/سهمیه تمام شده» از وضعیت HTTP و متن خطا:
// ۴۰۲ همیشه سهمیه است؛ ۴۲۹ فقط وقتی متن نشاندهندهٔ اعتبار باشد (وگرنه
// rate-limit معمولی است). ۴۰۱/۴۰۳ کلید خراب‌اند، نه شارژ.
func isQuotaError(status int, reason string) bool {
	if status == http.StatusPaymentRequired {
		return true
	}
	if status != http.StatusTooManyRequests {
		return false
	}
	s := strings.ToLower(reason)
	for _, key := range []string{
		"insufficient_quota", "insufficient quota",
		"insufficient balance", "insufficient_balance",
		"insufficient credit", "insufficient_credit",
		"balance too low", "no more credit", "out of credit",
		"exceeded your current quota", "billing", "payment required",
		"اعتبار", "موجودی",
	} {
		if strings.Contains(s, key) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- کشف endpoint موجودی

// balanceResolverOverride فقط برای تست‌ها: کشف endpoint از روی Base URL را
// جابه‌جا می‌کند تا httptest (میزبان 127.0.0.1) همان مسیر واقعی را طی کند.
// در اجرای عرفی همیشه nil است.
var balanceResolverOverride func(baseURL string) (bool, string, string)

// balanceEndpointFor بر اساس Base URL یک Provider، endpoint گزارش موجودی آن
// را پیدا می‌کند. خروجی: (پشتیبانی‌شده، نوع تجزیه، آدرس کامل).
// فقط سرویس‌هایی که گزارش موجودی مستند و پایدار دارند اینجا هستند؛ بقیه با
// مسیر «خطای سهمیه → موفقیت» پایش می‌شوند.
func (a *App) balanceEndpointFor(pid string) (bool, string, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.findProvider(pid)
	if p == nil || p.NoKey {
		return false, "", ""
	}
	if balanceResolverOverride != nil {
		return balanceResolverOverride(p.BaseURL)
	}
	host := ""
	if u, err := url.Parse(strings.TrimRight(p.BaseURL, "/")); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	for _, svc := range balanceKnownServices() {
		if host == svc.host || strings.HasSuffix(host, "."+svc.host) {
			return true, svc.kind, svc.balanceURL(p.BaseURL)
		}
	}
	return false, "", ""
}

type balanceService struct {
	host       string
	kind       string
	balanceURL func(base string) string
}

// balanceKnownServices فهرست سرویس‌های دارای گزارش موجودی. آدرس موجودی در
// برخی سرویس‌ها بیرون از Base URL چت است (مثل OpenRouter و DeepSeek).
func balanceKnownServices() []balanceService {
	return []balanceService{
		{host: "openrouter.ai", kind: "openrouter",
			balanceURL: func(string) string { return "https://openrouter.ai/api/v1/credits" }},
		{host: "deepseek.com", kind: "deepseek",
			balanceURL: func(string) string { return "https://api.deepseek.com/user/balance" }},
		{host: "siliconflow.cn", kind: "siliconflow",
			balanceURL: func(base string) string { return trimToOrigin(base) + "/user/info" }},
		{host: "moonshot.cn", kind: "moonshot",
			balanceURL: func(base string) string { return strings.TrimRight(base, "/") + "/users/me/balance" }},
		{host: "moonshot.ai", kind: "moonshot",
			balanceURL: func(base string) string { return strings.TrimRight(base, "/") + "/users/me/balance" }},
	}
}

// trimToOrigin «https://host/…/v1» را به «https://host» برمی‌گرداند.
func trimToOrigin(base string) string {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(base, "/")
	}
	return u.Scheme + "://" + u.Host
}

// ---------------------------------------------------------------- پروب موجودی

// probeProviderBalanceWithKey درخواست موجودی را برای نوع مشخص می‌فرستد و
// تجزیه می‌کند. جدا از Provider بودنش باعث می‌شود تست‌ها با httptest مستقیم
// همان مسیر واقعی را پوشش دهند؛ Provider بدون کلید از ابتدا پایش نمی‌شود.
func probeProviderBalanceWithKey(kind, endpoint, key string) (ProviderBalance, error) {
	if endpoint == "" {
		return ProviderBalance{}, errors.New("endpoint موجودی خالی است")
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return ProviderBalance{}, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: usageProbeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return ProviderBalance{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ProviderBalance{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, readErrorBody(resp.Body))
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return ProviderBalance{}, err
	}
	switch kind {
	case "openrouter":
		return parseOpenRouterCredits(raw)
	case "deepseek":
		return parseDeepSeekBalance(raw)
	case "siliconflow":
		return parseSiliconFlowBalance(raw)
	case "moonshot":
		return parseMoonshotBalance(raw)
	}
	return ProviderBalance{}, errors.New("نوع گزارش موجودی ناشناخته است")
}

// parseOpenRouterCredits {"data":{"total_credits":110,"total_usage":10}}
//
// total_credits مبلغ تجمعیِ خریدِ اعتبار است، نه موجودی لحظه‌ای؛ حسابِ تازه
// یا حسابی که فقط مدل آزاد/BYOK استفاده می‌کند صفر برمی‌گرداند. صفر یک
// خواندنِ معتبر است و نباید «خطای خواندن موجودی» شمرده شود (وگرنه پروب هر
// ۱۵ دقیقه یک خطای بی‌معنا در لاگ می‌نویسد و تشخیص شارژ هرگز مبنا نمی‌گیرد).
func parseOpenRouterCredits(raw json.RawMessage) (ProviderBalance, error) {
	var v struct {
		Data *struct {
			TotalCredits *float64 `json:"total_credits"`
			TotalUsage   float64  `json:"total_usage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ProviderBalance{}, err
	}
	if v.Data == nil || v.Data.TotalCredits == nil {
		return ProviderBalance{}, errors.New("موجودی OpenRouter خوانده نشد")
	}
	return ProviderBalance{Total: *v.Data.TotalCredits, Currency: "USD", UpdatedAt: timeNow()}, nil
}

// parseDeepSeekBalance {"balance_infos":[{"currency":"CNY","total_balance":"112.00"}]}
func parseDeepSeekBalance(raw json.RawMessage) (ProviderBalance, error) {
	var v struct {
		BalanceInfos []struct {
			Currency     string `json:"currency"`
			TotalBalance string `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ProviderBalance{}, err
	}
	if len(v.BalanceInfos) == 0 {
		return ProviderBalance{}, errors.New("موجودی DeepSeek خوانده نشد")
	}
	total, perr := strconv.ParseFloat(strings.TrimSpace(v.BalanceInfos[0].TotalBalance), 64)
	if perr != nil {
		return ProviderBalance{}, fmt.Errorf("موجودی DeepSeek ناخواناست: %s", v.BalanceInfos[0].TotalBalance)
	}
	return ProviderBalance{Total: total, Currency: v.BalanceInfos[0].Currency, UpdatedAt: timeNow()}, nil
}

// parseSiliconFlowBalance {"code":200,"data":{"balance":"1.50","totalBalance":"10.00","currency":"CNY"}}
func parseSiliconFlowBalance(raw json.RawMessage) (ProviderBalance, error) {
	var v struct {
		Data struct {
			Balance      string `json:"balance"`
			TotalBalance string `json:"totalBalance"`
			Currency     string `json:"currency"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ProviderBalance{}, err
	}
	s := strings.TrimSpace(v.Data.TotalBalance)
	if s == "" {
		s = strings.TrimSpace(v.Data.Balance)
	}
	total, perr := strconv.ParseFloat(s, 64)
	if perr != nil {
		return ProviderBalance{}, errors.New("موجودی SiliconFlow خوانده نشد")
	}
	return ProviderBalance{Total: total, Currency: v.Data.Currency, UpdatedAt: timeNow()}, nil
}

// parseMoonshotBalance {"code":0,"data":{"available_balance":"96.00","voucher_balance":"10.00"}}
func parseMoonshotBalance(raw json.RawMessage) (ProviderBalance, error) {
	var v struct {
		Data struct {
			AvailableBalance string `json:"available_balance"`
			VoucherBalance   string `json:"voucher_balance"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ProviderBalance{}, err
	}
	total, perr := strconv.ParseFloat(strings.TrimSpace(v.Data.AvailableBalance), 64)
	if perr != nil {
		return ProviderBalance{}, errors.New("موجودی Moonshot خوانده نشد")
	}
	if vb, verr := strconv.ParseFloat(strings.TrimSpace(v.Data.VoucherBalance), 64); verr == nil && vb > 0 {
		total += vb
	}
	return ProviderBalance{Total: total, Currency: "CNY", UpdatedAt: timeNow()}, nil
}

// ---------------------------------------------------------------- تشخیص شارژ + صفر کردن

// applyBalanceSample نمونهٔ تازهٔ موجودی را با آخرین نمونهٔ روی دیسک مقایسه
// می‌کند؛ افزایشِ موجودی = شارژ → شمارنده‌ها خودکار صفر می‌شوند. نمونهٔ تازه
// همیشه ذخیره می‌شود تا مقایسهٔ بعدی مبنا داشته باشد.
func (a *App) applyBalanceSample(pid string, bal ProviderBalance) {
	a.mu.Lock()
	prev, hadPrev := a.settings.ProviderBalances[pid]
	// مقایسه با دقت پولی: اختلاف کمتر از یک‌صدم واحد (یک سنت) نویز است.
	recharged := hadPrev && prev.UpdatedAt > 0 && bal.Total-prev.Total >= 0.01
	if a.settings.ProviderBalances == nil {
		a.settings.ProviderBalances = map[string]ProviderBalance{}
	}
	a.settings.ProviderBalances[pid] = bal
	if err := a.saveSettings(); err != nil {
		logAppError("save", "usageguard balance: "+err.Error())
	}
	a.mu.Unlock()

	if recharged {
		a.autoResetUsage(pid, "balance_up", prev.Total, bal.Total, bal.Currency)
	}
}

// autoResetUsage شمارنده‌های تجمعی مصرف را خودکار صفر می‌کند (بدون دخالت
// کاربر) و رویداد usage:auto-reset می‌فرستد. مصرف هر گفتگو دست نمی‌خورد.
func (a *App) autoResetUsage(pid string, reason string, oldBal, newBal float64, currency string) {
	provName := ""
	a.mu.Lock()
	a.settings.TokensUsed = 0
	a.settings.TokensByProvider = map[string]int64{}
	a.settings.TokensByModel = map[string]int64{}
	a.settings.RecentUsage = []int64{}
	a.settings.UsageSince = timeNow()
	a.settings.LastAutoResetAt = timeNow()
	a.settings.LastAutoResetProvider = pid
	a.settings.LastAutoResetReason = reason
	p := a.findProvider(pid)
	if p != nil {
		provName = p.DisplayName
		if provName == "" {
			provName = p.Name
		}
	}
	serr := a.saveSettings()
	a.mu.Unlock()
	if serr != nil {
		logAppError("save", "usageguard auto-reset: "+serr.Error())
	}
	runtimeEmit(a, "usage:auto-reset", map[string]interface{}{
		"providerId":   pid,
		"providerName": provName,
		"reason":       reason, // balance_up | recovered_after_quota
		"oldBalance":   oldBal,
		"newBalance":   newBal,
		"currency":     currency,
		"at":           timeNow(),
	})
}

// ---------------------------------------------------------------- اتصال به فرانت‌اند

// ProbeProviderBalance موجودی Provider فعال را همین حالا می‌خواند (دکمهٔ
// «بررسی شارژ» در پاپ‌اور مصرف). این فراخوانی هرگز شمارنده را مستقیم صفر
// نمی‌کند؛ اگر با این بررسی شارژ تشخیص داده شود، صفر کردن از مسیر خودکار
// autoResetUsage می‌گذرد و رویداد usage:auto-reset می‌رود.
func (a *App) ProbeProviderBalance() map[string]interface{} {
	defer a.recoverBound("ProbeProviderBalance")
	pid := a.activeProviderID()
	out := map[string]interface{}{
		"supported":             false,
		"ok":                    false,
		"total":                 0.0,
		"currency":              "",
		"checkedAt":             int64(0),
		"error":                 "",
		"lastAutoResetAt":       int64(0),
		"lastAutoResetReason":   "",
		"lastAutoResetProvider": "",
	}
	if pid == "" {
		out["error"] = "سرویس‌دهنده‌ای فعال نیست"
		return out
	}
	a.mu.Lock()
	if b, ok := a.settings.ProviderBalances[pid]; ok {
		out["total"] = b.Total
		out["currency"] = b.Currency
		out["checkedAt"] = b.UpdatedAt
	}
	out["lastAutoResetAt"] = a.settings.LastAutoResetAt
	out["lastAutoResetReason"] = a.settings.LastAutoResetReason
	out["lastAutoResetProvider"] = a.settings.LastAutoResetProvider
	a.mu.Unlock()

	supported, kind, endpoint := a.balanceEndpointFor(pid)
	out["supported"] = supported
	if !supported {
		return out
	}
	key := a.decryptKeyFor(pid)
	bal, perr := probeProviderBalanceWithKey(kind, endpoint, key)
	if perr != nil {
		out["error"] = perr.Error()
		return out
	}
	out["ok"] = true
	out["total"] = bal.Total
	out["currency"] = bal.Currency
	out["checkedAt"] = bal.UpdatedAt
	a.applyBalanceSample(pid, bal)
	// اگر همین خواندن شارژ را تشخیص داد، وضعیت صفر شدن خودکار تازه شده است —
	// برگرداندنش به UI کمک می‌کند بدون سرک زدن به تنظیمات، خبر را نشان دهد.
	a.mu.Lock()
	if b, ok := a.settings.ProviderBalances[pid]; ok {
		out["total"] = b.Total
		out["currency"] = b.Currency
		out["checkedAt"] = b.UpdatedAt
	}
	out["lastAutoResetAt"] = a.settings.LastAutoResetAt
	out["lastAutoResetReason"] = a.settings.LastAutoResetReason
	out["lastAutoResetProvider"] = a.settings.LastAutoResetProvider
	a.mu.Unlock()
	return out
}

// decryptKeyFor کلید رمزگشایی‌شدهٔ Provider — با قفل کوتاه، بیرون از قفل
// درخواست شبکه استفاده می‌شود.
func (a *App) decryptKeyFor(pid string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.decryptKey(pid)
}
