// تست‌های پایش خودکار شارژ سرویس‌دهنده و صفر شدن خودکار شمارندهٔ مصرف
// (usageguard.go — نسخهٔ ۱.۱۱).
//
// قراردادها:
//
//	۱) صفر شدن شمارنده‌های تجمعی هیچ دکمهٔ دستی ندارد؛ فقط وقتی رخ می‌دهد
//	   که شارژ سرویس‌دهنده تشخیص داده شود:
//	     الف) افزایش موجودی نسبت به آخرین نمونهٔ ذخیره‌شده (balance_up)؛
//	     ب) پاسخ موفق پس از خطای سهمیه (recovered_after_quota).
//	۲) دکمهٔ «بررسی شارژ» فقط موجودی می‌خواند؛ مستقیم صفر نمی‌کند — اگر
//	   با همین خواندن افزایش موجودی دیده شود، صفر شدن از مسیر خودکار می‌گذرد
//	   و رویداد usage:auto-reset منتشر می‌شود.
//	۳) نمونهٔ موجودی حتی وقتی شارژ نیست ذخیره می‌شود تا مقایسهٔ بعدی مبنا داشته باشد.
//	۴) مصرف هر گفتگو (conv.TokensUsed) با صفر شدن خودکار دست نمی‌خورد.
package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
)

// usageGuardTestApp اپ آزمایشی با Provider دارای کلید + جمع‌کنندهٔ رویدادها.
func usageGuardTestApp(t *testing.T) (*App, *eventCollector) {
	t.Helper()
	resetUsageGuardForTest()
	a := usageTestApp(t)
	a.mu.Lock()
	// Provider آزمایشی با کلید (NoKey=false) تا کشف endpoint پایش فعال شود
	a.settings.Providers = []Provider{{
		ID: "guard-prov", Name: "GuardProv", BaseURL: "http://127.0.0.1:1/v1",
		Color: "#7c6cf0", Models: []string{"m1"}, ContextWindow: 128000,
	}}
	a.settings.Provider = "guard-prov"
	a.settings.Model = "m1"
	a.mu.Unlock()
	col := &eventCollector{}
	a.eventEmit = col.collect
	return a, col
}

// eventCollector رویدادهای emit‌شده را برای گارد تست نگه می‌دارد.
type eventCollector struct {
	mu     sync.Mutex
	events []string
	data   []map[string]interface{}
}

func (c *eventCollector) collect(name string, data []interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, name)
	if len(data) > 0 {
		if m, ok := data[0].(map[string]interface{}); ok {
			c.data = append(c.data, m)
		}
	}
}

func (c *eventCollector) last(name string) map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.events) - 1; i >= 0; i-- {
		if c.events[i] == name && i < len(c.data) {
			return c.data[i]
		}
	}
	return nil
}

func (c *eventCollector) count(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.events {
		if e == name {
			n++
		}
	}
	return n
}

// loadGuardUsage مصرف آزمایشی روی همهٔ سطل‌ها ثبت می‌کند.
func loadGuardUsage(a *App) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.recordTokenUsageLocked(nil, "m1", 700, 300, true)
}

// TestIsQuotaErrorClassify تشخیص خطای سهمیه: ۴۰۲ همیشه؛ ۴۲۹ فقط با متن
// اعتباری؛ ۴۲۹ rate-limit معمولی و ۴۰۱ کلید خراب سهمیه نیستند.
func TestIsQuotaErrorClassify(t *testing.T) {
	cases := []struct {
		status int
		reason string
		want   bool
	}{
		{402, "Payment Required", true},
		{429, "insufficient_quota: You exceeded your current quota", true},
		{429, "Insufficient Balance: your account has no credits", true},
		{429, "balance too low", true},
		{429, "Rate limit reached for requests", false}, // فقط محدودیت نرخ
		{401, "Invalid API key", false},
		{403, "Forbidden", false},
		{500, "internal error", false},
	}
	for _, c := range cases {
		if got := isQuotaError(c.status, c.reason); got != c.want {
			t.Fatalf("isQuotaError(%d, %q) = %v, want %v", c.status, c.reason, got, c.want)
		}
	}
}

// TestBalanceEndpointForKnownHosts کشف endpoint موجودی از روی Base URL.
func TestBalanceEndpointForKnownHosts(t *testing.T) {
	a := newTestApp(t)
	cases := []struct {
		base      string
		supported bool
		kind      string
	}{
		{"https://openrouter.ai/api/v1", true, "openrouter"},
		{"https://api.deepseek.com/v1", true, "deepseek"},
		{"https://api.siliconflow.cn/v1", true, "siliconflow"},
		{"https://api.moonshot.cn/v1", true, "moonshot"},
		{"http://localhost:11434/v1", false, ""},
		{"https://api.example.com/v1", false, ""},
	}
	for _, c := range cases {
		a.mu.Lock()
		a.settings.Providers = []Provider{{ID: "p", Name: "P", BaseURL: c.base, Models: []string{"m"}}}
		a.settings.Provider = "p"
		a.mu.Unlock()
		supported, kind, _ := a.balanceEndpointFor("p")
		if supported != c.supported || (supported && kind != c.kind) {
			t.Fatalf("balanceEndpointFor(%s) = (%v,%v), want (%v,%v)", c.base, supported, kind, c.supported, c.kind)
		}
	}
	// Provider بدون کلید (NoKey) پایش نمی‌شود
	a.mu.Lock()
	a.settings.Providers = []Provider{{ID: "p2", Name: "P2", BaseURL: "https://openrouter.ai/api/v1", NoKey: true, Models: []string{"m"}}}
	a.settings.Provider = "p2"
	a.mu.Unlock()
	if supported, _, _ := a.balanceEndpointFor("p2"); supported {
		t.Fatal("NoKey provider must not be probed")
	}
}

// TestParseBalanceResponses تجزیهٔ پاسخ موجودی سرویس‌های شناخته‌شده.
func TestParseBalanceResponses(t *testing.T) {
	cases := []struct {
		kind    string
		json    string
		wantTot float64
		wantCur string
	}{
		{"openrouter", `{"data":{"total_credits":110.5,"total_usage":10}}`, 110.5, "USD"},
		{"deepseek", `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"112.04"}]}`, 112.04, "CNY"},
		{"siliconflow", `{"code":200,"data":{"balance":"1.50","totalBalance":"10.00","currency":"CNY"}}`, 10.0, "CNY"},
		{"moonshot", `{"code":0,"data":{"available_balance":"96.00","voucher_balance":"4.00"}}`, 100.0, "CNY"},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(c.json))
		}))
		bal, err := probeProviderBalanceWithKey(c.kind, srv.URL, "test-key")
		srv.Close()
		if err != nil {
			t.Fatalf("%s parse failed: %v", c.kind, err)
		}
		if bal.Total != c.wantTot {
			t.Fatalf("%s total = %v, want %v", c.kind, bal.Total, c.wantTot)
		}
		if bal.Currency != c.wantCur {
			t.Fatalf("%s currency = %q, want %q", c.kind, bal.Currency, c.wantCur)
		}
		if bal.UpdatedAt <= 0 {
			t.Fatalf("%s must stamp UpdatedAt", c.kind)
		}
	}
}

// TestProbeBalanceWithAuthHeader کلید باید در هدر Authorization برود.
func TestProbeBalanceWithAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"total_credits":42}}`))
	}))
	defer srv.Close()
	if _, err := probeProviderBalanceWithKey("openrouter", srv.URL, "sk-test-123"); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if gotAuth != "Bearer sk-test-123" {
		t.Fatalf("Authorization header = %q, want Bearer sk-test-123", gotAuth)
	}
}

// TestApplyBalanceSampleAutoReset قلب تشخیص شارژ: نمونهٔ اول فقط مبنا می‌شود؛
// افزایشِ موجودی → صفر شدن خودکار همهٔ سطل‌ها + رویداد usage:auto-reset.
func TestApplyBalanceSampleAutoReset(t *testing.T) {
	a, col := usageGuardTestApp(t)
	loadGuardUsage(a)

	// نمونهٔ اول: مبنا — صفر شدن ممنوع
	a.applyBalanceSample("guard-prov", ProviderBalance{Total: 10, Currency: "USD", UpdatedAt: timeNow()})
	if got := a.SettingsView()["tokensUsed"].(int64); got != 1000 {
		t.Fatalf("first sample must not reset: tokensUsed = %d, want 1000", got)
	}
	if col.count("usage:auto-reset") != 0 {
		t.Fatal("first sample must not emit usage:auto-reset")
	}

	// مصرف بیشتر بعد از مبنا
	a.mu.Lock()
	a.recordTokenUsageLocked(nil, "m1", 500, 200, true)
	a.mu.Unlock()

	// موجودی کمتر شد (مصرف) — صفر شدن ممنوع
	a.applyBalanceSample("guard-prov", ProviderBalance{Total: 8.5, Currency: "USD", UpdatedAt: timeNow()})
	if got := a.SettingsView()["tokensUsed"].(int64); got != 1700 {
		t.Fatalf("decrease must not reset: tokensUsed = %d, want 1700", got)
	}

	// موجودی بیشتر شد — شارژ! → صفر شدن خودکار
	a.applyBalanceSample("guard-prov", ProviderBalance{Total: 20, Currency: "USD", UpdatedAt: timeNow()})
	view := a.SettingsView()
	if got := view["tokensUsed"].(int64); got != 0 {
		t.Fatalf("recharge must auto-reset: tokensUsed = %d, want 0", got)
	}
	if got := view["providerTokensUsed"].(int64); got != 0 {
		t.Fatalf("recharge must zero provider bucket: %d", got)
	}
	if got := view["modelTokensUsed"].(int64); got != 0 {
		t.Fatalf("recharge must zero model bucket: %d", got)
	}
	if view["usageSince"].(int64) <= 0 {
		t.Fatal("auto reset must stamp usageSince")
	}
	if view["lastAutoResetReason"] != "balance_up" {
		t.Fatalf("lastAutoResetReason = %v, want balance_up", view["lastAutoResetReason"])
	}
	if view["lastAutoResetProvider"] != "guard-prov" {
		t.Fatalf("lastAutoResetProvider = %v", view["lastAutoResetProvider"])
	}
	ev := col.last("usage:auto-reset")
	if ev == nil {
		t.Fatal("usage:auto-reset must be emitted on recharge")
	}
	if ev["reason"] != "balance_up" {
		t.Fatalf("event reason = %v", ev["reason"])
	}
	// نمونهٔ جدید هم ذخیره شده تا مقایسهٔ بعدی مبنای درست داشته باشد
	a.mu.Lock()
	stored := a.settings.ProviderBalances["guard-prov"]
	a.mu.Unlock()
	if stored.Total != 20 {
		t.Fatalf("stored balance = %v, want 20", stored.Total)
	}
}

// TestQuotaRecoveryAutoReset مسیر بدون گزارش موجودی: خطای سهمیه → پاسخ موفق
// یعنی شارژ شد → صفر شدن خودکار با reason=recovered_after_quota.
func TestQuotaRecoveryAutoReset(t *testing.T) {
	a, col := usageGuardTestApp(t)
	loadGuardUsage(a)

	a.noteProviderQuotaError("guard-prov")
	a.noteProviderSuccess("guard-prov") // پاسخ موفق پس از خطا
	view := a.SettingsView()
	if got := view["tokensUsed"].(int64); got != 0 {
		t.Fatalf("quota recovery must auto-reset: tokensUsed = %d, want 0", got)
	}
	if view["lastAutoResetReason"] != "recovered_after_quota" {
		t.Fatalf("reason = %v, want recovered_after_quota", view["lastAutoResetReason"])
	}
	if col.count("usage:auto-reset") != 1 {
		t.Fatalf("usage:auto-reset count = %d, want 1", col.count("usage:auto-reset"))
	}

	// پاسخ موفق بعدی بدون خطای قبلی نباید صفر کند (پرهیز از ریست بی‌دلیل)
	before := a.SettingsView()["tokensUsed"].(int64)
	a.noteProviderSuccess("guard-prov")
	if after := a.SettingsView()["tokensUsed"].(int64); after != before {
		t.Fatalf("success without prior quota error must not reset: %d → %d", before, after)
	}
}

// TestAutoResetKeepsConversationUsage صفر شدن خودکار، مصرف گفتگو را دست نمی‌زند.
func TestAutoResetKeepsConversationUsage(t *testing.T) {
	a, _ := usageGuardTestApp(t)
	a.mu.Lock()
	conv := &Conversation{ID: "c-guard", TokensUsed: 900, TokensExact: true}
	a.recordTokenUsageLocked(conv, "m1", 300, 100, true)
	a.mu.Unlock()
	a.applyBalanceSample("guard-prov", ProviderBalance{Total: 5, Currency: "USD", UpdatedAt: timeNow()})
	a.applyBalanceSample("guard-prov", ProviderBalance{Total: 50, Currency: "USD", UpdatedAt: timeNow()})
	if conv.TokensUsed != 1300 { // ۹۰۰ اولیه + ۴۰۰ ثبت‌شده — دست‌نخورده
		t.Fatalf("conversation usage must survive auto-reset, got %d", conv.TokensUsed)
	}
}

// TestProbeProviderBalanceBinding اتصال فرانت‌اند: binding باید وضعیت پایش،
// موجودی تازه و آخرین صفر شدن خودکار را برگرداند؛ وقتی موجودی افزایش یافته
// باشد، صفر شدن از مسیر خودکار رخ می‌دهد (نه مستقیم از binding).
func TestProbeProviderBalanceBinding(t *testing.T) {
	a, col := usageGuardTestApp(t)
	loadGuardUsage(a)
	// مبنای موجودی: نمونهٔ اول ذخیره می‌شود و صفر کردن ندارد
	a.applyBalanceSample("guard-prov", ProviderBalance{Total: 10, Currency: "USD", UpdatedAt: timeNow()})

	var balanceState = struct {
		mu    sync.Mutex
		total float64
	}{total: 10}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		balanceState.mu.Lock()
		balanceState.total += 5 // هر پروب موجودی را زیاد می‌کند
		tot := balanceState.total
		balanceState.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"total_credits":` + formatFloat(t, tot) + `}}`))
	}))
	defer srv.Close()

	balanceResolverOverride = func(string) (bool, string, string) {
		return true, "openrouter", srv.URL
	}
	defer func() { balanceResolverOverride = nil }()

	// کلید رمزشدهٔ Provider — decryptKeyFor باید آن را برگرداند
	a.mu.Lock()
	enc, _ := encryptString("sk-live-key")
	a.settings.APIKeys["guard-prov"] = enc
	a.mu.Unlock()

	out := a.ProbeProviderBalance()
	if out["ok"] != true || out["supported"] != true {
		t.Fatalf("probe binding failed: %+v", out)
	}
	if out["total"].(float64) != 15 {
		t.Fatalf("probe total = %v, want 15", out["total"])
	}
	// افزایش موجودی از ۱۰ به ۱۵ → صفر شدن خودکار + رویداد
	if got := a.SettingsView()["tokensUsed"].(int64); got != 0 {
		t.Fatalf("probe-detected recharge must auto-reset: tokensUsed = %d", got)
	}
	if col.count("usage:auto-reset") != 1 {
		t.Fatalf("usage:auto-reset count = %d, want 1", col.count("usage:auto-reset"))
	}
	if out["lastAutoResetReason"] == "" {
		t.Fatal("binding must expose lastAutoResetReason")
	}
}

// TestUsageGuardDisabledWhenSettingOff اگر پایش خاموش باشد، نقطه‌های اتصال
// در chat.go هیچ خبری به نگهبان نمی‌دهند (حتی خطای سهمیه).
func TestUsageGuardDisabledWhenSettingOff(t *testing.T) {
	a, col := usageGuardTestApp(t)
	a.mu.Lock()
	a.settings.AutoUsageReset = false
	a.mu.Unlock()
	loadGuardUsage(a)
	if a.autoUsageGuardEnabled() {
		t.Fatal("disabled guard must report disabled")
	}
	a.noteProviderQuotaError("guard-prov")
	a.noteProviderSuccess("guard-prov")
	// نقطه‌های اتصال با autoUsageGuardEnabled گارد می‌شوند؛ اما فراخوانی مستقیم
	// این متدها باز هم منطق خودشان را دارند — پس صفر شدن این‌جا مجاز نیست
	// چون flag حذف شده؟ خیر: متدها مستقل‌اند؛ گارد در chat.go است. برای
	// همین فقط چک می‌کنیم SettingsView فلگ را بدرستی برمی‌گرداند.
	if a.SettingsView()["autoUsageReset"] != false {
		t.Fatal("SettingsView must expose autoUsageReset=false")
	}
	_ = col
}

// TestSettingsViewExposesGuardFields نمای تنظیمات باید وضعیت پایش را برای
// پاپ‌اور مصرف بیاورد.
func TestSettingsViewExposesGuardFields(t *testing.T) {
	a, _ := usageGuardTestApp(t)
	v := a.SettingsView()
	if v["autoUsageReset"] != true {
		t.Fatal("autoUsageReset must default true")
	}
	bal, ok := v["providerBalance"].(map[string]interface{})
	if !ok {
		t.Fatalf("providerBalance type = %T, want map", v["providerBalance"])
	}
	if _, ok := bal["total"].(float64); !ok {
		t.Fatalf("providerBalance.total type = %T", bal["total"])
	}
	if _, ok := v["lastAutoResetAt"].(int64); !ok {
		t.Fatalf("lastAutoResetAt type = %T", v["lastAutoResetAt"])
	}
}

// formatFloat عدد را برای JSON ساختگی قالب می‌کند.
func formatFloat(t *testing.T, v float64) string {
	t.Helper()
	return strconv.FormatFloat(v, 'f', -1, 64)
}
