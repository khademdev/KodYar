// لایه‌ی تنظیمات KodYar.
//
// تنظیمات و کلیدهای API در پوشه‌ی استاندارد پیکربندی کاربر ذخیره می‌شوند:
// - ویندوز: %AppData%\KodYar
// - مک: ~/Library/Application Support/KodYar
// - لینوکس: ~/.config/KodYar
//
// عملکرد: مسیر configDir یک‌بار در طول عمر برنامه محاسبه و cache می‌شود
// (sync.Once) تا در هر فراخوانی از os.MkdirAll و syscall تکراری جلوگیری شود.
package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// appDirName نام پوشه‌ی داده‌ی برنامه است.
	appDirName = "KodYar"
	// legacyAppDirName نام پوشه در نسخه‌های پیش از تغییر برند است؛ فقط برای
	// خواندن/مهاجرت دادهٔ قدیمی و سازگاری فایل‌های پروژه به کار می‌رود.
	legacyAppDirName = "MyAI"
)

// Provider یک سرویس‌دهنده‌ی سازگار با OpenAI را توصیف می‌کند.
type Provider struct {
	ID string `json:"id"`
	// Name نام فنی سرویس است (آدرس پایه از روی آن حدس زده می‌شود).
	Name string `json:"name"`
	// DisplayName نام نمایشی اختیاری برای فهرست‌ها؛ خالی = Name نمایش داده
	// می‌شود. عمداً از Name جدا است تا شناسایی سرویس به‌هم نخورد.
	DisplayName   string   `json:"displayName,omitempty"`
	BaseURL       string   `json:"baseUrl"`
	Color         string   `json:"color"`
	Models        []string `json:"models"`
	ContextWindow int      `json:"contextWindow"`
	NoKey         bool     `json:"noKey"`
}

// CustomTool ابزار سفارشی تعریف‌شده توسط کاربر (اجرا با shell در workspace).
type CustomTool struct {
	ID          string `json:"id"`
	Name        string `json:"name"`        // نام ابزار برای مدل (حروف لاتین/زیرخط)
	Description string `json:"description"` // توضیح برای مدل
	Parameters  string `json:"parameters"`  // JSON Schema (اختیاری)
	Command     string `json:"command"`     // الگوی دستور؛ {{argName}} جایگزین می‌شود
	TimeoutSec  int    `json:"timeoutSec"`
	Confirm     bool   `json:"confirm"` // نیاز به تأیید کاربر قبل از اجرا
}

// Settings مدل ذخیره‌سازی تنظیمات روی دیسک است.
type Settings struct {
	Provider      string     `json:"provider"`
	Model         string     `json:"model"`
	Environment   string     `json:"environment"`
	Providers     []Provider `json:"providers"`
	WorkspacePath string     `json:"workspacePath"`
	AllowCommands bool       `json:"allowCommands"`
	AgentMode     bool       `json:"agentMode"`
	// PromptCache فعال‌سازی نشانه‌های کش پرامپت برای سرویس‌دهنده‌های
	// سازگار با cache_control. **پیش‌فرض روشن است** (۱.۹) چون کش، TTFT و
	// هزینهٔ هر دور را کم می‌کند؛ سرویس‌دهنده‌ای که فیلد را رد کند یک‌بار
	// ۴۰۰ می‌دهد و برای همان Provider خودکار خاموش می‌شود (promptcache.go).
	PromptCache bool `json:"promptCache,omitempty"`
	// PromptCacheTouched یعنی کاربر خودش انتخاب کرده؛ «خاموشِ ذخیره‌شده»
	// فقط وقتی معتبر است که این پرچم روشن باشد — وگرنه پیش‌فرض روشن اعمال
	// می‌شود (تنظیمات قدیمی که این فیلد را ندارند هم خودکار روشن می‌شوند).
	PromptCacheTouched bool `json:"promptCacheTouched,omitempty"`
	// AutoVerifyOff خاموش‌کردن «راستی‌آزمایی خودکار» پس از تغییر فایل
	// (autoverify.go). صفر بودن یعنی روشن — تنظیمات قدیمی خودکار روشن‌اند.
	AutoVerifyOff bool `json:"autoVerifyOff,omitempty"`
	// ShowActivity نمایش نوار «گزارش زنده‌ی فعالیت ایجنت» بالای جعبه‌ی پیام
	// (پیش‌فرض روشن). پنل «برنامهٔ کار» مستقل از این تنظیم است.
	ShowActivity bool              `json:"showActivity"`
	APIKeys      map[string]string `json:"apiKeys"`
	SystemPrompt string            `json:"systemPrompt"`
	// BrandPromptMigrated مهاجرت یک‌بارهٔ نام برند داخل «دستور سیستمی»
	// ذخیره‌شده را ثبت می‌کند (MyAI → KodYar). بدون این پرچم، پرامپت
	// ذخیره‌شدهٔ نسخه‌های قبل مدل را وادار می‌کرد خودش را MyAI معرفی کند.
	BrandPromptMigrated bool    `json:"brandPromptMigrated,omitempty"`
	Temperature         float64 `json:"temperature"`
	MaxHistory          int     `json:"maxHistory"`
	TokenLimit          int     `json:"tokenLimit"`
	TokensUsed          int64   `json:"tokensUsed"` // مجموع کل (سازگاری با نسخه‌های قبل)
	// TokensByProvider مصرف تجمعی هر Provider (کلید = شناسه Provider).
	// با تعویض API، عدد Provider دیگر قاطی نمی‌شود.
	TokensByProvider map[string]int64 `json:"tokensByProvider,omitempty"`
	// TokensByModel مصرف تجمعی به تفکیک مدل (کلید = نام مدل ارسالی به API).
	// توکن‌های مدل‌های مختلف هرگز با هم قاطی نمی‌شوند — حتی وقتی هر دو مدل
	// از یک Provider هستند و کاربر بین‌شان جابه‌جا می‌شود.
	TokensByModel map[string]int64 `json:"tokensByModel,omitempty"`
	RecentUsage   []int64          `json:"recentUsage"`
	// UsageSince زمان (Unix) آخرین صفر شدن شمارنده‌های مصرف؛ ۰ = هرگز.
	// شمارنده‌ها با بستن برنامه/قطع اتصال صفر نمی‌شوند — فقط وقتی شارژ
	// سرویس‌دهنده خودکار تشخیص داده شود (usageguard.go).
	UsageSince int64 `json:"usageSince,omitempty"`

	// ---- پایش خودکار شارژ سرویس‌دهنده (usageguard.go) ----

	// ProviderBalances آخرین موجودی شناخته‌شدهٔ هر Provider؛ افزایشِ موجودی
	// نسبت به این مبنا یعنی «شارژ شد» → شمارنده‌های مصرف خودکار صفر می‌شوند.
	ProviderBalances map[string]ProviderBalance `json:"providerBalances,omitempty"`
	// AutoUsageReset پایش خودکار شارژ (پیش‌فرض روشن). صفر شدن شمارنده
	// دیگر دست کاربر نیست؛ فقط با تشخیص خودکار شارژ انجام می‌شود.
	AutoUsageReset bool `json:"autoUsageReset"`
	// LastAutoReset* آخرین صفر شدن خودکار — برای نمایش در پاپ‌اور مصرف.
	LastAutoResetAt       int64  `json:"lastAutoResetAt,omitempty"`
	LastAutoResetProvider string `json:"lastAutoResetProvider,omitempty"`
	LastAutoResetReason   string `json:"lastAutoResetReason,omitempty"` // balance_up | recovered_after_quota

	// ---- امکانات جدید ----
	ConfirmFileWrites bool `json:"confirmFileWrites"` // همگام با EditMode (review_each = true)

	// EditMode حالت ویرایش (۴.۱): "review_each" = مودال Diff برای هر تغییر؛
	// "auto" = اعمال فوریِ تغییرات داخل پوشه + بازبینی در پایان نوبت.
	// پیش‌فرض نصب‌های تازه: "auto". فایل‌های قدیمی: از ConfirmFileWrites مشتق می‌شود.
	EditMode string `json:"editMode,omitempty"`

	// ProtectedGlobs مسیرهای محافظت‌شده (نسبی/glob) که حتی در auto هم تأیید
	// می‌خواهند. خالی = فهرست پیش‌فرض (turnreview.go).
	ProtectedGlobs []string `json:"protectedGlobs,omitempty"`

	// MaxTurnTokens سقف مصرف توکن هر نوبت (۵.۳؛ ۰ = خاموش).
	MaxTurnTokens int64 `json:"maxTurnTokens,omitempty"`

	// StreamIdleTimeoutSec مهلت بیکاری استریم مدل (ثانیه؛ ۰ = پیش‌فرض ۱۸۰،
	// بازه‌ی مجاز ۳۰..۹۰۰). پنهان است و UI ندارد — فقط برای پایداری/دیباگ.
	StreamIdleTimeoutSec int `json:"streamIdleTimeoutSec"`

	PlanMode          bool   `json:"planMode"`          // حالت برنامه‌ریزی قبل از اجرا
	ContextFiles      int    `json:"contextFiles"`      // سقف فایل‌های خودکار افزوده به کانتکست @
	ProjectRules      string `json:"projectRules"`      // قوانین پروژه تعریف‌شده از داخل اپ
	RulesEnabled      bool   `json:"rulesEnabled"`      // خواندن خودکار.kodyar/rules.md و مشابه
	MaxTokens         int    `json:"maxTokens"`         // سقف توکن خروجی (۰ = بدون محدودیت)
	MaxAgentRounds    int    `json:"maxAgentRounds"`    // سقف دورهای اجرای ابزار ایجنت در یک پاسخ (پیش‌فرض ۲۵، محدوده ۵ تا ۲۰۰)
	MaxSubagentRounds int    `json:"maxSubagentRounds"` // سقف دورهای ابزار زیرایجنتِ task پیش‌فرض ۸، محدوده ۳ تا ۵۰)

	// SubagentModel مدلِ اختیاری برای زیرایجنت task؛ خالی = همان مدل اصلی.
	// برای «کاوش با مدل ارزان، اجرا با مدل قوی» است و Provider همان Provider فعال است.
	SubagentModel string `json:"subagentModel,omitempty"`

	// VisionImages سیاست ارسال تصویر (اسکرین‌شات browser_screenshot) به مدل:
	// auto (پیش‌فرض؛ فقط مدل شناخته‌شدهٔ تصویری) | on (اجبار) | off (هرگز).
	// در حالت غیرفعال، ابزار صریح می‌گوید فقط کاربر تصویر را می‌بیند.
	VisionImages string `json:"visionImages,omitempty"`

	// SkillsDisabled خاموش‌کننده‌ی کشف فایل‌های SKILL.md است. فیلد منفی است
	// تا settings.jsonهای قدیمی (بدون این کلید) پیش‌فرض روشن بمانند.
	SkillsDisabled bool         `json:"skillsDisabled,omitempty"`
	ToolResultKeep int          `json:"toolResultKeep"` // تعداد نتایج ابزارِ اخیر که کامل در تاریخچه می‌مانند
	MaxConvKB      int          `json:"maxConvKb"`      // سقف حجم فایل گفتگو (کیلوبایت)
	Theme          string       `json:"theme"`          // dark | light
	FontSize       int          `json:"fontSize"`       // اندازه فونت UI (۱۲ تا ۱۸)
	Density        string       `json:"density"`        // compact | normal | relaxed
	CustomTools    []CustomTool `json:"customTools"`    // ابزارهای سفارشی کاربر

	// MCPServers سرورهای MCP (mcpmanager.go) — فیلد اختیاری: فایل‌های قدیمی
	// بدون این فیلد بی‌خطا بارگذاری می‌شوند. مقادیر Env در حالت خاموش
	// رمزشده‌اند (crypto.go) و به UI فقط ماسک برمی‌گردد.
	MCPServers []MCPServerConfig `json:"mcpServers,omitempty"`

	// ProxyMode: auto = هوشمند (پروکسی سیستم ویندوز → محیط → مستقیم با fail-open)،
	// off = بدون پروکسی، custom = ProxyURL
	// فایل‌های قدیمی بدون این فیلد → auto (رفتار هوشمند)
	ProxyMode string `json:"proxyMode,omitempty"`
	ProxyURL  string `json:"proxyURL,omitempty"` // مثلاً http://127.0.0.1:7890

	// ProxySmartMigrated: مهاجرت یک‌باره به حالت «هوشمند» — نسخه‌های قبلی
	// پیش‌فرض env داشتند و برای رفع proxyconnect انتخاب دستی «خاموش» لازم
	// بود؛ یک‌بار حالت خاموشِ بدون پیکربندی سفارشی به auto برمی‌گردد تا
	// تشخیص پروکسی دوباره خودکار شود. پس از آن انتخاب صریح کاربر محترم است.
	ProxySmartMigrated bool `json:"proxySmartMigrated,omitempty"`

	// Permissions سیستم دسترسی ابزارها/دستورات. مقادر صفر = پیش‌فرضهای امن؛
	// سازگار با نسخه‌های قبلی (فیلد جدید optional)
	Permissions PermissionSettings `json:"permissions"`

	// SandboxMode حالت ایزوله‌سازی اجرای دستور (sandbox_windows.go):
	// "" یا "on" = سندباکس AppContainer روشن (پیش‌فرض امن، حتی برای
	// settings.json قدیمی که این فیلد را ندارد)، "off" = اجرای معمولی.
	SandboxMode string `json:"sandboxMode,omitempty"`
	// SandboxNoNetwork اگر true باشد، دستور داخل سندباکس هیچ دسترسی
	// شبکه‌ای ندارد (قابلیت internetClient به توکن AppContainer داده نمی‌شود).
	SandboxNoNetwork bool `json:"sandboxNoNetwork,omitempty"`

	// DefaultsRemoved: مهاجرت یک‌باره — Providerهای پیش‌فرض نسخه‌های قدیمی
	// (OpenAI، Groq و…) فقط یک‌بار حذف می‌شوند؛ Providerهایی که کاربر بعداً
	// خودش با همین امضا اضافه کند دیگر دست نمی‌خورند.
	DefaultsRemoved bool `json:"defaultsRemoved"`
}

// ConnectionDTO داده‌ای که فرانت‌اند در تب «اتصال» و هدر می‌بیند.
type ConnectionDTO struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Environment string `json:"environment"`
	Workspace   string `json:"workspace"`
	AllowCmds   bool   `json:"allowCommands"`
	TokensUsed  int64  `json:"tokensUsed"`
	TokenLimit  int    `json:"tokenLimit"`
	HasKey      bool   `json:"hasKey"`
}

func defaultSettings() *Settings {
	return &Settings{
		// از ۱.3.0 هیچ Provider پیش‌فرضی وجود ندارد — کاربر خودش
		// Providerهایش را از تب «Providerها» تعریف می‌کند.
		Provider:          "",
		Model:             "",
		Environment:       "general",
		SystemPrompt:      promptForEnvironment("general"),
		Providers:         []Provider{},
		WorkspacePath:     "",
		AllowCommands:     false,
		APIKeys:           map[string]string{},
		Temperature:       0.7,
		MaxHistory:        30,
		TokenLimit:        200000,
		TokensByProvider:  map[string]int64{},
		TokensByModel:     map[string]int64{},
		RecentUsage:       []int64{},
		ProviderBalances:  map[string]ProviderBalance{},
		AutoUsageReset:    true,  // صفر شدن شمارنده فقط خودکار، با تشخیص شارژ
		ConfirmFileWrites: false, // همگام با EditMode=auto (پیش‌فرض ۱.۸.۰)
		EditMode:          "auto",
		ProtectedGlobs:    defaultProtectedGlobs,
		MaxTurnTokens:     defaultMaxTurnTokens,
		PlanMode:          false,
		ShowActivity:      true,
		ContextFiles:      4,
		RulesEnabled:      true,
		ToolResultKeep:    6,
		MaxConvKB:         2048,
		PromptCache:       true,
		MaxAgentRounds:    defaultMaxAgentRounds,
		MaxSubagentRounds: defaultMaxSubagentRounds,
		VisionImages:      visionModeAuto,
		Theme:             "dark",
		FontSize:          14,
		Density:           "normal",
		CustomTools:       []CustomTool{},
		MCPServers:        []MCPServerConfig{},
	}
}

// computeConfigDir پوشه‌ی تنظیمات را محاسبه و در صورت نیاز می‌سازد.
// جدا از a.configDir است تا بتوان آن را پشت sync.Once cache کرد.
func computeConfigDir() (string, error) {
	if dir := os.Getenv("KODYAR_CONFIG_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return resolveConfigDir(base)
}

// resolveConfigDir پوشه‌ی داده را زیر base مشخص و در صورت نیاز می‌سازد.
// اگر پوشه‌ی نام جدید نباشد و پوشه‌ی نام قدیمی وجود داشته باشد، یک‌بار
// به نام جدید منتقل می‌شود تا تنظیمات، گفتگوها و کلیدهای رمزشده‌ی
// نسخه‌های پیشین از دست نروند. اگر پوشه‌ی جدید از قبل باشد هیچ چیزی
// جابه‌جا نمی‌شود.
func resolveConfigDir(base string) (string, error) {
	dir := filepath.Join(base, appDirName)
	if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
		legacy := filepath.Join(base, legacyAppDirName)
		if _, lerr := os.Stat(legacy); lerr == nil {
			if rerr := os.Rename(legacy, dir); rerr != nil {
				// اگر تغییر نام ممکن نبود (فایل قفل‌شده)، همان پوشه‌ی قدیمی
				// ادامه می‌دهد — داده گم نمی‌شود.
				if mkErr := os.MkdirAll(legacy, 0o755); mkErr != nil {
					return "", mkErr
				}
				return legacy, nil
			}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// configDir مسیر پوشه‌ی تنظیمات (کش‌شده). اولین فراخوانی computeConfigDir
// را صدا می‌زند؛ فراخوانی‌های بعدی مقدار کش‌شده را برمی‌گردانند.
func (a *App) configDir() (string, error) {
	a.configDirOnce.Do(func() {
		a.cachedConfigDir, a.configDirErr = computeConfigDir()
	})
	return a.cachedConfigDir, a.configDirErr
}

func (a *App) settingsPath() (string, error) {
	dir, err := a.configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

// providerIDSafe همان مجموعه‌ای که slugify می‌سازد (حروف/ارقام یونیکد و
// خط‌تیره) به‌علاوه سقف طول. شناسه‌های فارسیِ موجود معتبر می‌مانند؛ فقط
// نویسه‌های خطرناک برای درج در HTML (نقل‌قول، <، >، فاصله، کنترل و…)
// رد می‌شوند — UI مقدار id را escape نمی‌کند چون همیشه از همین مسیر
// امن ساخته می‌شود؛ این تابع همان نامتغیر را روی مرز داده اعمال می‌کند.
func providerIDSafe(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-') {
			return false
		}
	}
	return true
}

// providerColorSafe رنگ Provider: فقط کد هگز (خودِ UI تنها #RRGGBB
// می‌فرستد؛ مقدار دیگر یعنی فایل settings.json دست‌کاری شده).
func providerColorSafe(c string) bool {
	if len(c) != 4 && len(c) != 7 && len(c) != 9 || c[0] != '#' {
		return false
	}
	for _, r := range c[1:] {
		ok := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !ok {
			return false
		}
	}
	return true
}

// sanitizeProviders پاک‌سازی مقادیر خوانده‌شده از دیسک (مرز داده).
// settings.json ممکن است دست‌کاری یا هم‌رسانی شده باشد؛ شناسه/رنگِ
// دلخواه نباید به innerHTML صفحات (modals/popovers) راه پیدا کند —
// صفحه‌ی برنامه به bridge کامل گو دسترسی دارد. شناسه‌ی نامعتبر → بازسازی
// از نام (الگوی slugify)؛ رنگ نامعتبر → رنگ پیش‌فرض. کلید API هم به
// شناسه‌ی تازه منتقل می‌شود تا اتصال کاربر نشکند.
func sanitizeProviders(s *Settings) {
	if s == nil {
		return
	}
	taken := map[string]bool{}
	for _, p := range s.Providers {
		if p.ID != "" {
			taken[p.ID] = true
		}
	}
	for i := range s.Providers {
		p := &s.Providers[i]
		if !providerIDSafe(p.ID) {
			old := p.ID
			slug := slugify(p.Name)
			id := slug
			for n := 2; taken[id]; n++ {
				id = slug + "-" + itoa(n)
			}
			taken[id] = true
			if old != "" && s.APIKeys != nil {
				if k, ok := s.APIKeys[old]; ok && s.APIKeys[id] == "" {
					s.APIKeys[id] = k
				}
			}
			p.ID = id
		}
		if p.Color == "" || !providerColorSafe(p.Color) {
			p.Color = "#7c6cf0"
		}
	}
}

// loadSettings تنظیمات را از دیسک می‌خواند (یا مقادیر پیش‌فرض می‌سازد).
func (a *App) loadSettings() error {
	path, err := a.settingsPath()
	if err != nil {
		return err
	}
	s := defaultSettings()
	hadFile := false
	// آیا فایل واقعاً کلید editMode را دارد؟ (unmarshal روی پیش‌فرض‌ها
	// انجام می‌شود؛ نبودِ کلید را فقط با probe می‌شود از مقدار پیش‌فرض
	// تشخیص داد — مهاجرتِ ۴۱ به همین وابسته است؛ فایل خراب =مانند فایل قدیمی.)
	hadEditMode := false
	hadAutoUsageReset := false
	raw, err := os.ReadFile(path)
	if err == nil {
		hadFile = true
		var probe map[string]json.RawMessage
		if json.Unmarshal(raw, &probe) == nil {
			_, hadEditMode = probe["editMode"]
			_, hadAutoUsageReset = probe["autoUsageReset"]
		}
		if jerr := json.Unmarshal(raw, s); jerr != nil {
			// فایل خراب را کنار می‌گذاریم تا کلیدهای API از بین نروند
			_ = os.Rename(path, path+".corrupt")
			s = defaultSettings()
			hadEditMode = false
		}
	}
	if s.Providers == nil {
		s.Providers = []Provider{}
	}
	if s.APIKeys == nil {
		s.APIKeys = map[string]string{}
	}
	// مرز داده: شناسه/رنگ Provider خوانده‌شده از دیسک پاک‌سازی می‌شود
	// (فایل دست‌کاری‌شده نباید به innerHTML صفحات راه پیدا کند).
	sanitizeProviders(s)
	a.settings = s
	// مهاجرت یک‌باره: Providerهای پیش‌فرض قدیمی حذف می‌شوند؛ کاربر
	// Providerهای خودش را تعریف می‌کند. پس از اولین اجرا فلگ ذخیره
	// می‌شود تا Provider سفارشی با امضای مشابه بعداً حذف نشود.
	if !s.DefaultsRemoved {
		s.DefaultsRemoved = true
		if hadFile && s.stripBuiltinProviders() {
			_ = a.saveSettings()
		}
	}
	// مهاجرت یک‌باره به پروکسی هوشمند: «خاموش»ی که فقط برای دور زدن
	// proxyconnect انتخاب شده بود، به auto برمی‌گردد تا پروکسی دوباره خودکار
	// تشخیص داده شود (fail-open خودش جایش را می‌گیرد). انتخاب‌های بعدیِ
	// خودِ کاربر (off/custom) پس از این مهاجرت دست‌نخورده می‌مانند.
	if !s.ProxySmartMigrated {
		s.ProxySmartMigrated = true
		if hadFile && normalizeProxyMode(s.ProxyMode) == "off" {
			s.ProxyMode = "auto"
			_ = a.saveSettings()
		}
	}
	if s.Environment == "" {
		s.Environment = "general"
	}
	if s.SystemPrompt == "" {
		s.SystemPrompt = promptForEnvironment(s.Environment)
	}
	// مهاجرت یک‌بارهٔ نام برند در «دستور سیستمی» ذخیره‌شده: پرامپتی که پیش
	// از تغییر نام ذخیره شده هنوز «You are MyAI» را به مدل می‌گوید، پس مدل
	// وقتی از اسمش پرسیده می‌شد «MyAI» جواب می‌داد. متن دست‌نویس کاربر
	// حفظ می‌شود و فقط خودِ نام عوض می‌شود.
	if !s.BrandPromptMigrated {
		s.BrandPromptMigrated = true
		if rewritten, changed := rewriteLegacyBrandPrompt(s.SystemPrompt); changed {
			s.SystemPrompt = rewritten
			_ = a.saveSettings()
		}
	}
	if s.WorkspacePath == "" {
		s.WorkspacePath = defaultWorkspacePath()
	}
	if s.TokenLimit <= 0 {
		s.TokenLimit = 200000
	}
	if s.TokensByProvider == nil {
		s.TokensByProvider = map[string]int64{}
	}
	if s.TokensByModel == nil {
		s.TokensByModel = map[string]int64{}
	}
	if s.ProviderBalances == nil {
		s.ProviderBalances = map[string]ProviderBalance{}
	}
	// مهاجرت پیش‌فرض روشن: فایل‌های قدیمی کلید autoUsageReset را ندارند
	// (unmarshal روی صفر false می‌ماند) — پایش خودکار شارژ باید فعال باشد.
	if !hadAutoUsageReset {
		s.AutoUsageReset = true
	}
	if s.MaxHistory <= 0 {
		s.MaxHistory = 30
	}
	if s.Temperature < 0 {
		s.Temperature = 0.7
	}
	// مقادیر پیش‌فرض امکانات جدید (برای فایل‌های تنظیمات قدیمی)
	if s.ContextFiles <= 0 {
		s.ContextFiles = 4
	}
	if s.ToolResultKeep <= 0 {
		s.ToolResultKeep = 6
	}
	if s.MaxConvKB <= 0 {
		s.MaxConvKB = 2048
	}
	// حالت ویرایش (۴.۱) — مهاجرت: فایل‌های قدیمی EditMode ندارند؛ از
	// ConfirmFileWrites مشتق می‌شود (true → review_each، false → auto) و هر
	// دو فیلد همیشه همگام می‌مانند. مقدار نامعتبر → auto.
	if !hadEditMode {
		if s.ConfirmFileWrites {
			s.EditMode = "review_each"
		} else {
			s.EditMode = "auto"
		}
	}
	s.EditMode = normalizeEditMode(s.EditMode)
	if s.EditMode == "" {
		s.EditMode = "auto"
	}
	s.ConfirmFileWrites = s.EditMode == "review_each"
	// سقف مصرف توکن هر نوبت (۵.۳)
	s.MaxTurnTokens = clampMaxTurnTokens(s.MaxTurnTokens)
	// سقف دورهای ایجنت — فایل‌های قدیمی فیلد را ندارند (صفر) و به پیش‌فرض
	// برمی‌گردند؛ مقادیر خارج از محدوده به نزدیک‌ترین حد مجاز می‌چسبند.
	s.MaxAgentRounds = clampMaxAgentRounds(s.MaxAgentRounds)
	// سقف دورهای زیرایجنت — همان سازگاری با فایل‌های قدیمی
	s.MaxSubagentRounds = clampMaxSubagentRounds(s.MaxSubagentRounds)
	// سیاست ارسال تصویر به مدل؛ فایل‌های قدیمی (خالی) = auto.
	s.VisionImages = normalizeVisionMode(s.VisionImages)
	if s.Theme != "light" && s.Theme != "dark" {
		s.Theme = "dark"
	}
	if s.FontSize < 12 {
		s.FontSize = 12
	}
	if s.FontSize > 18 {
		s.FontSize = 18
	}
	if s.Density != "compact" && s.Density != "relaxed" {
		s.Density = "normal"
	}
	if s.CustomTools == nil {
		s.CustomTools = []CustomTool{}
	}
	if s.MCPServers == nil {
		s.MCPServers = []MCPServerConfig{}
	}
	// ---- Permissions: پیش‌فرضهای امن برای فایلهای قدیمی ----
	// مهاجرت AllowCommands=true: کاربران قبلی «همهٔ دستورات (به‌جز لیست سیاه و
	// پرریسک‌ها)» داشتند؛ اکنون لیست‌سفید امن بدون پرسش اجرا می‌شود و
	// بقیه «ask» می‌شوند — هرگز قدرت بیشتری از قبل اعطا نمی‌شود؛ دستورات
	// رایج با یک «همیشه اجازه بده» پایدار می‌شوند.
	if s.Permissions.CommandRules == nil {
		s.Permissions.CommandRules = []PermissionRule{}
	}
	if s.Permissions.Tools == nil {
		s.Permissions.Tools = map[string]string{}
	}
	if len(s.Permissions.SensitiveGlobs) == 0 {
		s.Permissions.SensitiveGlobs = defaultSensitiveGlobs
	}
	// ---- WebHosts (C.1): اجازه‌ی web_fetch فقط per-host ----
	// مهاجرت: «همیشه اجازه»ی سراسری web_fetch در نسخه‌های قبل، ابزار را به
	// کانال نشت داده تبدیل می‌کرد. هرگز بی‌صدا حفظ نمی‌شود: حذف می‌شود تا
	// رفتار به پیش‌فرض (ask) برگردد و دسترسی به دامنه‌ها هنگام درخواست
	// داده شود.
	if normalizePermissionDecision(s.Permissions.Tools[webFetchToolName]) == "allow" {
		delete(s.Permissions.Tools, webFetchToolName)
		logAppErrorFor(a, "perm", "migration: removed tool-wide web_fetch allow (now per-host)")
	}
	if normalizePermissionDecision(s.Permissions.Tools[webSearchToolName]) == "allow" {
		delete(s.Permissions.Tools, webSearchToolName)
		logAppErrorFor(a, "perm", "migration: removed tool-wide web_search allow (now per-host)")
	}
	if s.Permissions.WebHosts == nil {
		s.Permissions.WebHosts = []PermissionRule{}
	}
	{ // نرمال‌سازی و حذف الگوهای نامعتبر (دستکاری‌شده/قدیمی)
		clean := make([]PermissionRule, 0, len(s.Permissions.WebHosts))
		for _, r := range s.Permissions.WebHosts {
			host := normalizeWebHost(r.Pattern)
			dec := normalizePermissionDecision(r.Decision)
			if host == "" || dec == "" || !validWebHostPattern(host) {
				continue
			}
			if dec != "allow" {
				continue // فقط قاعده‌ی allow معنا دارد
			}
			clean = append(clean, PermissionRule{Pattern: host, Decision: dec})
		}
		s.Permissions.WebHosts = clean
	}
	a.settings = s
	return nil
}

// stripBuiltinProviders Providerهای پیش‌فرض نسخه‌های قدیمی را از تنظیمات
// حذف می‌کند (مهاجرت یک‌باره). فقط امضای دقیق (ID + BaseURL یکسان) حذف
// می‌شود؛ Providerی که کاربر URL آن را عوض کرده باشد، سفارشی محسوب می‌شود
// و می‌ماند. اگر سرویس‌دهنده‌ی فعال حذف شود، فعال‌بودن به اولین Provider
// باقی‌مانده می‌رود (یا خالی می‌شود). مقدار برگشتی: آیا چیزی حذف شد.
func (s *Settings) stripBuiltinProviders() bool {
	sigs := builtinProviderSignatures()
	kept := make([]Provider, 0, len(s.Providers))
	removedActive := false
	removedAny := false
	for _, p := range s.Providers {
		if base, isBuiltin := sigs[p.ID]; isBuiltin && p.BaseURL == base {
			delete(s.APIKeys, p.ID)
			removedAny = true
			if s.Provider == p.ID {
				removedActive = true
			}
			continue
		}
		kept = append(kept, p)
	}
	if !removedAny {
		return false
	}
	s.Providers = kept
	if removedActive {
		s.Provider = ""
		s.Model = ""
		if len(kept) > 0 {
			s.Provider = kept[0].ID
			if len(kept[0].Models) > 0 {
				s.Model = kept[0].Models[0]
			}
		}
	}
	return true
}

// saveSettings تنظیمات را روی دیسک می‌نویسد.
//
// عملکرد: از json.Marshal (نه MarshalIndent) استفاده می‌کنیم — ۲-۳ برابر
// سریع‌تر و فایل کوچک‌تر. فایل تنظیمات برای خواندن انسانی طراحی نشده.
func (a *App) saveSettings() error {
	path, err := a.settingsPath()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(a.settings)
	if err != nil {
		return err
	}
	// نوشتن اتمیک با fsync — تضمین دادهٔ حساس (کلیدهای رمزشده) پس از کرش
	// سیستم نیمه‌نوشته روی دیسک نماند.
	return atomicWriteFile(path, raw, 0o600)
}

// GetSettings خلاصه‌ی تنظیمات را برای فرانت‌اند برمی‌گرداند (بدون کلیدها).
func (a *App) GetSettings() Settings {
	defer a.recoverBound("GetSettings")
	a.mu.Lock()
	defer a.mu.Unlock()
	s := *a.settings
	s.APIKeys = nil
	s.Providers = copyProviders(a.settings.Providers)
	s.RecentUsage = copyInt64s(a.settings.RecentUsage)
	s.ProviderBalances = copyProviderBalanceMap(a.settings.ProviderBalances)
	return s
}

// copyProviderBalanceMap کپی امن map موجودی‌ها (بدون اشتراک حافظه با state).
func copyProviderBalanceMap(m map[string]ProviderBalance) map[string]ProviderBalance {
	out := make(map[string]ProviderBalance, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// copyProviderBalance کپی امن یک نمونهٔ موجودی برای SettingsView.
func copyProviderBalance(b ProviderBalance) map[string]interface{} {
	return map[string]interface{}{
		"total":     b.Total,
		"currency":  b.Currency,
		"updatedAt": b.UpdatedAt,
	}
}

// SetAllowCommands سوییچ «اجازه اجرای دستور» را ذخیره می‌کند.
func (a *App) SetAllowCommands(allow bool) error {
	defer a.recoverBound("SetAllowCommands")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.AllowCommands = allow
	return a.saveSettings()
}

// SetAgentMode سوییچ «حالت ایجنت» را ذخیره می‌کند.
func (a *App) SetAgentMode(on bool) error {
	defer a.recoverBound("SetAgentMode")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.AgentMode = on
	return a.saveSettings()
}

// ResetUsage صفر کردن دستی شمارنده‌های تجمعی — فقط برای سازگاری با نسخه‌های
// قبل و تست‌ها به‌جا مانده است. مسیر عرفی از ۱.۱۱ دیگر هیچ دکمهٔ دستی ندارد:
// صفر شدن فقط وقتی رخ می‌دهد که شارژ سرویس‌دهنده خودکار تشخیص داده شود
// (autoResetUsage در usageguard.go). مصرف هر گفتگو (conv.TokensUsed) دست نمی‌خورد.
func (a *App) ResetUsage() error {
	defer a.recoverBound("ResetUsage")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.TokensUsed = 0
	a.settings.TokensByProvider = map[string]int64{}
	a.settings.TokensByModel = map[string]int64{}
	a.settings.RecentUsage = []int64{}
	a.settings.UsageSince = time.Now().Unix()
	return a.saveSettings()
}

// SetStreamIdleTimeout مهلت سکون استریم (ثانیه) را ذخیره می‌کند؛ مقدار به
// بازهٔ مجاز ۳۰..۹۰۰ clamp می‌شود (۰ = پیش‌فرض ۱۸۰).
func (a *App) SetStreamIdleTimeout(sec int) error {
	defer a.recoverBound("SetStreamIdleTimeout")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.StreamIdleTimeoutSec = clampStreamIdleSeconds(sec)
	return a.saveSettings()
}

// SetShowActivity نمایش/پنهان‌کردن نوار «گزارش زنده» را ذخیره می‌کند.
// پنل «برنامهٔ کار» تحت تأثیر آن نیست: آن فقط با وجود چک‌لیست ظاهر می‌شود.
func (a *App) SetShowActivity(on bool) error {
	defer a.recoverBound("SetShowActivity")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.ShowActivity = on
	return a.saveSettings()
}

// SetPromptCache کش پرامپت را روشن/خاموش می‌کند و انتخاب کاربر را ماندگار
// می‌کند (پیش‌فرض نصب تازه: روشن).
func (a *App) SetPromptCache(on bool) error {
	defer a.recoverBound("SetPromptCache")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.PromptCache = on
	a.settings.PromptCacheTouched = true
	return a.saveSettings()
}

// promptCacheEnabled آیا تنظیم کش پرامپت روشن است؟ (بدون I/O)
// پیش‌فرض (و تنظیمات قدیمی بدون فیلد) → روشن.
func (a *App) promptCacheEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.settings == nil {
		return true
	}
	if !a.settings.PromptCacheTouched {
		return true
	}
	return a.settings.PromptCache
}

// SetAutoVerify «راستی‌آزمایی خودکار پس از تغییر فایل» را روشن/خاموش می‌کند
// (پیش‌فرض روشن).
func (a *App) SetAutoVerify(on bool) error {
	defer a.recoverBound("SetAutoVerify")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.AutoVerifyOff = !on
	return a.saveSettings()
}

// autoVerifyEnabled آیا راستی‌آزمایی خودکار روشن است؟ (بدون I/O)
func (a *App) autoVerifyEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings != nil && !a.settings.AutoVerifyOff
}

// SetPlanMode سوییچ «حالت برنامه‌ریزی» را ذخیره می‌کند. وقتی روشن باشد،
// ایجنت ابتدا فقط یک برنامه‌ی چندمرحله‌ای می‌نویسد و پس از تأیید کاربر
// (ApprovePlan) اجرا را شروع می‌کند.
func (a *App) SetPlanMode(on bool) error {
	defer a.recoverBound("SetPlanMode")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.PlanMode = on
	return a.saveSettings()
}

// SetConfirmFileWrites سوییچ «تأیید تغییرات فایل (Diff Preview)» را ذخیره
// می‌کند. وقتی روشن باشد، ایجنت قبل از هر نوشتن/ویرایش/حذف/جابه‌جایی،
// پیش‌نمایش diff را به کاربر نشان می‌دهد و منتظر تأیید می‌ماند.
func (a *App) SetConfirmFileWrites(on bool) error {
	defer a.recoverBound("SetConfirmFileWrites")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.ConfirmFileWrites = on
	if on {
		a.settings.EditMode = "review_each"
	} else {
		a.settings.EditMode = "auto"
	}
	return a.saveSettings()
}

// SetEditMode حالت ویرایش را ذخیره می‌کند (۴.۱): "review_each" یا "auto".
// ConfirmFileWrites همیشه همگام نگه داشته می‌شود.
func (a *App) SetEditMode(mode string) error {
	defer a.recoverBound("SetEditMode")
	mode = normalizeEditMode(strings.TrimSpace(mode))
	if mode == "" {
		return errors.New("حالت ویرایش نامعتبر است (review_each | auto)")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.EditMode = mode
	a.settings.ConfirmFileWrites = mode == "review_each"
	return a.saveSettings()
}

// normalizeEditMode مقدار معتبر حالت ویرایش (خالی = نامعتبر).
func normalizeEditMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "review_each", "review-each", "revieweach":
		return "review_each"
	case "auto":
		return "auto"
	}
	return ""
}

// editModeLocked حالت ویرایش نرمال‌شده. باید با a.mu قفل‌شده صدا زده شود.
func (a *App) editModeLocked() string {
	if m := normalizeEditMode(a.settings.EditMode); m != "" {
		return m
	}
	if a.settings.ConfirmFileWrites {
		return "review_each"
	}
	return "auto"
}

// defaultMaxTurnTokens پیش‌فرض سقف توکن هر نوبت (۵.۳).
const defaultMaxTurnTokens = 2000000

// clampMaxTurnTokens سقف توکن هر نوبت در محدوده‌ی مجاز
// (۵۰ هزار تا ۱۰ میلیون؛ صفر = خاموش).
func clampMaxTurnTokens(v int64) int64 {
	if v == 0 {
		return 0
	}
	if v < 50000 {
		return 50000
	}
	if v > 10000000 {
		return 10000000
	}
	return v
}

// SetMaxTurnTokens سقف مصرف توکن هر نوبت را ذخیره می‌کند (۵.۳).
// ۰ = خاموش؛ محدوده‌ی مجاز ۵۰ هزار تا ۱۰ میلیون (clamp).
func (a *App) SetMaxTurnTokens(v int64) error {
	defer a.recoverBound("SetMaxTurnTokens")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.MaxTurnTokens = clampMaxTurnTokens(v)
	return a.saveSettings()
}

// defaultWorkspacePath مسیر پیش‌فرض پوشه‌ی کاری است:
// %USERPROFILE%\Projects\MyWorkspace (رشته‌ی خالی یعنی مسیر خانه خوانده نشد).
func defaultWorkspacePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Projects", "MyWorkspace")
}

// ensureDefaultWorkspaceAt فقط پوشه‌ی کاریِ «پیش‌فرضِ خود برنامه» را می‌سازد.
// پیش از این، loadSettings این مسیر را فقط در حافظه ست می‌کرد و هیچ‌وقت روی
// دیسک ساخته نمی‌شد؛ نتیجه: اولین اجرا با مسیر ناموجود کار می‌کرد و
// فهرست‌کردن/اجرای دستور/git خطا می‌دادند. مسیر دستیِ کاربر دست‌نخورده
// می‌ماند تا پوشه‌ای را که خودش پاک کرده دوباره نسازیم.
// جدا شده تا تست بدون دست‌زدن به پوشه‌ی خانه‌ی واقعی اجرا شود.
func ensureDefaultWorkspaceAt(def, ws string) error {
	if def == "" || strings.TrimSpace(ws) != def {
		return nil
	}
	if st, err := os.Stat(def); err == nil && st.IsDir() {
		return nil
	}
	return os.MkdirAll(def, 0o755)
}

// ensureDefaultWorkspace پوشه‌ی کاری پیش‌فرض را در راه‌اندازی می‌سازد.
func (a *App) ensureDefaultWorkspace() error {
	a.mu.Lock()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()
	return ensureDefaultWorkspaceAt(defaultWorkspacePath(), ws)
}

// SaveWorkspace مسیر پوشه‌ی کاری (پروژه) را ذخیره می‌کند.
func (a *App) SaveWorkspace(path string) error {
	defer a.recoverBound("SaveWorkspace")
	a.mu.Lock()
	defer a.mu.Unlock()
	path = strings.TrimSpace(path)
	if path != "" {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				if mkErr := os.MkdirAll(path, 0o755); mkErr != nil {
					return errors.New("مسیر پروژه ساخته نشد: " + path + " (" + mkErr.Error() + ")")
				}
			} else {
				return errors.New("دسترسی به مسیر ممکن نیست: " + path + " (" + err.Error() + ")")
			}
		} else if !info.IsDir() {
			return errors.New("مسیر واردشده یک پوشه نیست: " + path)
		}
	}
	a.settings.WorkspacePath = path
	return a.saveSettings()
}

// SettingsView نمای عمومی تنظیمات (بدون کلید رمز) برای فرانت‌اند.
func (a *App) SettingsView() map[string]interface{} {
	defer a.recoverBound("SettingsView")
	// شمارش تغییرات بازگشت‌پذیر باید «قبل» از گرفتن a.mu انجام شود.
	// ترتیب قفل‌ها اینجا حیاتی است: RevertFileChange با changes.mu در دست
	// resolveWorkspacePath (یعنی a.mu) را صدا می‌زند؛ اگر اینجا هم a.mu را
	// بگیریم و بعد changes.mu بخواهیم، بن‌بست دوطرفه رخ می‌دهد.
	pending := 0
	if a.changes != nil {
		pending = a.pendingChangeCount()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.settings
	limit := s.TokenLimit
	if p := a.findProvider(s.Provider); p != nil && p.ContextWindow > 0 {
		limit = p.ContextWindow
	}
	return map[string]interface{}{
		"provider":           s.Provider,
		"model":              s.Model,
		"environment":        s.Environment,
		"providers":          copyProviders(s.Providers),
		"workspace":          s.WorkspacePath,
		"allowCommands":      s.AllowCommands,
		"agentMode":          s.AgentMode,
		"tokensUsed":         s.TokensUsed,
		"tokenLimit":         limit,
		"providerTokensUsed": s.TokensByProvider[s.Provider],
		"tokensByProvider":   copyInt64Map(s.TokensByProvider),
		"tokensByModel":      copyInt64Map(s.TokensByModel),
		"modelTokensUsed":    s.TokensByModel[s.Model],
		"recentUsage":        copyInt64s(s.RecentUsage),
		"usageSince":         s.UsageSince,
		// پایش خودکار شارژ — پاپ‌اور مصرف وضعیت را از این‌جا می‌خواند.
		"autoUsageReset":        s.AutoUsageReset,
		"providerBalance":       copyProviderBalance(s.ProviderBalances[s.Provider]),
		"lastAutoResetAt":       s.LastAutoResetAt,
		"lastAutoResetProvider": s.LastAutoResetProvider,
		"lastAutoResetReason":   s.LastAutoResetReason,
		"temperature":           s.Temperature,
		"systemPrompt":          s.SystemPrompt,
		"hasKeys":               a.hasKeysMap(),
		"configDir":             a.mustConfigDir(),
		"confirmFileWrites":     s.ConfirmFileWrites,
		"editMode":              a.editModeLocked(),
		"protectedGlobs":        append([]string(nil), a.protectedGlobsLocked()...),
		"defaultProtectedGlobs": defaultProtectedGlobs,
		"maxTurnTokens":         clampMaxTurnTokens(s.MaxTurnTokens),
		"planMode":              s.PlanMode,
		"promptCache":           s.PromptCache || !s.PromptCacheTouched,
		"autoVerify":            !s.AutoVerifyOff,
		"showActivity":          s.ShowActivity,
		// مقدار مؤثر (پیش‌فرض ۱۸۰) — UI عدد قابل‌نمایش می‌خواهد، نه صفر.
		"streamIdleTimeoutSec": clampStreamIdleSeconds(s.StreamIdleTimeoutSec),
		"contextFiles":         s.ContextFiles,
		"projectRules":         s.ProjectRules,
		"rulesEnabled":         s.RulesEnabled,
		"maxTokens":            s.MaxTokens,
		"toolResultKeep":       s.ToolResultKeep,
		"maxConvKb":            s.MaxConvKB,
		"maxAgentRounds":       clampMaxAgentRounds(s.MaxAgentRounds),
		"maxSubagentRounds":    clampMaxSubagentRounds(s.MaxSubagentRounds),
		"maxHistory":           s.MaxHistory,
		"theme":                s.Theme,
		"fontSize":             s.FontSize,
		"density":              s.Density,
		"customTools":          copyCustomTools(s.CustomTools),
		"mcpServers":           maskedMCPServers(s.MCPServers),
		"pendingChanges":       pending,
		"proxyMode":            normalizeProxyMode(s.ProxyMode),
		"proxyURL":             s.ProxyURL,
		"sandbox":              sandboxEnabledLocked(s),
		"sandboxNetwork":       !s.SandboxNoNetwork,
		"sandboxAvailable":     sandboxSupported(),
	}
}

// maskedMCPServers کپی سرورهای MCP با env ماسک‌شده برای UI — مقدار واقعی
// (رمزشده) هرگز از settings خارج نمی‌شود؛ UI که همان ماسک را برگرداند،
// مقدار ذخیره‌شده را نگه می‌دارد (SaveMCPServers).
func maskedMCPServers(ms []MCPServerConfig) []MCPServerConfig {
	out := make([]MCPServerConfig, len(ms))
	for i, m := range ms {
		out[i] = MCPServerConfig{
			Name: m.Name, Command: m.Command, Enabled: m.Enabled,
			Args: append([]string(nil), m.Args...),
		}
		if len(m.Env) > 0 {
			env := make(map[string]string, len(m.Env))
			for k, v := range m.Env {
				if v == "" {
					env[k] = ""
					continue
				}
				env[k] = mcpEnvMask
			}
			out[i].Env = env
		}
	}
	return out
}

func copyCustomTools(ts []CustomTool) []CustomTool {
	out := make([]CustomTool, len(ts))
	copy(out, ts)
	return out
}

func copyProviders(ps []Provider) []Provider {
	out := make([]Provider, len(ps))
	copy(out, ps)
	return out
}

func copyInt64s(xs []int64) []int64 {
	if xs == nil {
		return []int64{}
	}
	out := make([]int64, len(xs))
	copy(out, xs)
	return out
}

func (a *App) mustConfigDir() string {
	dir, err := a.configDir()
	if err != nil {
		return ""
	}
	return dir
}

// hasKeysMap نشان می‌دهد برای کدام Providerها کلید ذخیره شده است.
func (a *App) hasKeysMap() map[string]bool {
	out := map[string]bool{}
	for _, p := range a.settings.Providers {
		if p.NoKey {
			out[p.ID] = true
			continue
		}
		v := a.settings.APIKeys[p.ID]
		out[p.ID] = strings.TrimSpace(v) != ""
	}
	return out
}

// SaveConnection تب «اتصال» را ذخیره می‌کند.
func (a *App) SaveConnection(provider, model, environment, apiKey string) error {
	defer a.recoverBound("SaveConnection")
	a.mu.Lock()
	defer a.mu.Unlock()

	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return errors.New("سرویس‌دهنده و مدل الزامی هستند")
	}
	if a.findProvider(provider) == nil {
		return errors.New("سرویس‌دهنده پیدا نشد: " + provider)
	}
	a.settings.Provider = provider
	a.settings.Model = model
	// پرامپت پیش‌فرض محیط فقط با «تغییر واقعی محیط» نصب می‌شود. انتخاب
	// مدل از چت‌باکس همان environment ذخیره‌شده را می‌فرستد و نباید متنِ
	// دست‌نویس «دستور سیستمی» کاربر را بی‌صدا بازنویسی کند.
	if environment != "" && environment != a.settings.Environment {
		a.settings.Environment = environment
		a.settings.SystemPrompt = promptForEnvironment(environment)
	}
	if apiKey == "-" {
		a.settings.APIKeys[provider] = ""
	} else if apiKey != "" {
		enc, err := encryptString(apiKey)
		if err != nil {
			return err
		}
		a.settings.APIKeys[provider] = enc
	}
	return a.saveSettings()
}

// GetEnvironmentPresets فهرست محیط‌های آماده را برمی‌گرداند.
func (a *App) GetEnvironmentPresets() []map[string]string {
	defer a.recoverBound("GetEnvironmentPresets")
	return []map[string]string{
		{"id": "general", "title": "عمومی", "desc": "پاسخ‌های متعادل و دوستانه"},
		{"id": "developer", "title": "توسعه‌دهنده", "desc": "تمرکز روی کد و دیباگ"},
	}
}

// legacyBrandPromptName نام برند در پرامپت‌های ذخیره‌شده پیش از تغییر نام
// است؛ فقط برای مهاجرت خوانده می‌شود.
const legacyBrandPromptName = "MyAI"

// rewriteLegacyBrandPrompt نام قدیمی برند را در پرامپت سیستمی به نام فعلی
// برمی‌گرداند و می‌گوید آیا چیزی عوض شد.
func rewriteLegacyBrandPrompt(p string) (string, bool) {
	if p == "" {
		return p, false
	}
	out := rewriteBrandToken(p, legacyBrandPromptName, appDirName)
	out = rewriteBrandToken(out, "MyAi", appDirName)
	out = rewriteBrandToken(out, "MYAI", appDirName)
	return out, out != p
}

// rewriteBrandToken تنها تطبیق‌های «واژه‌ی کامل» را عوض می‌کند. کاراکترهای
// چسبیده (حرف، رقم، _ - / \\) یعنی بخشی از یک مسیر یا شناسه است (مثل
// MyAI-1.0.6 یا پوشه‌ی MyAI\settings.json) و دست‌نخورده می‌ماند.
func rewriteBrandToken(s, old, new string) string {
	if s == "" || !strings.Contains(s, old) {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, old)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := i + len(old)
		standalone := true
		if i > 0 {
			if r, _ := utf8.DecodeLastRuneInString(s[:i]); brandAttachedRune(r) {
				standalone = false
			}
		}
		if end < len(s) {
			if r, _ := utf8.DecodeRuneInString(s[end:]); brandAttachedRune(r) {
				standalone = false
			}
		}
		b.WriteString(s[:i])
		if standalone {
			b.WriteString(new)
		} else {
			b.WriteString(old)
		}
		s = s[end:]
	}
}

// brandAttachedRune یعنی این کاراکتر بخشی از یک مسیر/شناسه است.
func brandAttachedRune(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	switch r {
	case '_', '-', '/', '\\':
		return true
	}
	return false
}

// promptForEnvironment پرامپت سیستم برای هر محیط.
func promptForEnvironment(env string) string {
	switch env {
	case "developer":
		return "You are KodYar, an expert senior software engineer. Answer in Persian (Farsi). Be concise and practical. Always format code in fenced code blocks with the language tag."
	default:
		return "You are KodYar, a concise and helpful AI assistant for coding and questions. Answer in Persian (Farsi). Use Markdown for formatting."
	}
}

// SaveGenerationSettings تب «مدل و رفتار» را ذخیره می‌کند:
// Temperature (۰ تا ۲)، سقف توکن خروجی (۰ = بدون محدودیت) و System Prompt دستی.
func (a *App) SaveGenerationSettings(temperature float64, maxTokens int, systemPrompt string) error {
	defer a.recoverBound("SaveGenerationSettings")
	a.mu.Lock()
	defer a.mu.Unlock()
	if temperature < 0 {
		temperature = 0
	}
	if temperature > 2 {
		temperature = 2
	}
	if maxTokens < 0 {
		maxTokens = 0
	}
	if maxTokens > 200000 {
		maxTokens = 200000
	}
	if len(systemPrompt) > 16*1024 {
		// برش در مرز کاراکتر UTF-8 — برش بایتی خام می‌توانست
		// آخرین حرف فارسی را نصف کند و رشته‌ی نامعتبر بسازد.
		systemPrompt = cutUTF8(systemPrompt, 16*1024)
	}
	a.settings.Temperature = temperature
	a.settings.MaxTokens = maxTokens
	a.settings.SystemPrompt = strings.TrimSpace(systemPrompt)
	return a.saveSettings()
}

// محدوده‌ی مجاز «حداکثر مراحل ایجنت» — کمتر از ۵ عملاً بی‌فایده و
// بیشتر از ۲۰۰ در عمل فقط توکن می‌سوزاند؛ هر دو حد در UI هم اعمال می‌شوند.
const (
	minMaxAgentRounds = 5
	maxMaxAgentRounds = 200
)

// clampMaxAgentRounds مقدار را به محدوده‌ی مجاز می‌آورد؛ صفر/منفی یعنی
// «تنظیم نشده» و به پیش‌فرض (۲۵) برمی‌گردد — سازگار با settings.json قدیمی.
func clampMaxAgentRounds(n int) int {
	if n <= 0 {
		return defaultMaxAgentRounds
	}
	if n < minMaxAgentRounds {
		return minMaxAgentRounds
	}
	if n > maxMaxAgentRounds {
		return maxMaxAgentRounds
	}
	return n
}

// SetMaxAgentRounds تنظیم «حداکثر مراحل ایجنت» را ذخیره می‌کند.
// مقادیر خارج از محدوده مثل بقیه‌ی تنظیمات عددی به‌جای خطا clamped می‌شوند
// (الگوی SetAppearance) تا فرانت‌اند ساده بماند.
func (a *App) SetMaxAgentRounds(rounds int) error {
	defer a.recoverBound("SetMaxAgentRounds")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.MaxAgentRounds = clampMaxAgentRounds(rounds)
	return a.saveSettings()
}

// SetMaxSubagentRounds تنظیم «حداکثر مراحل زیرایجنت» را ذخیره می‌کند.
// مثل بقیه‌ی تنظیمات عددی به‌جای خطا clamp می‌شود.
func (a *App) SetMaxSubagentRounds(rounds int) error {
	defer a.recoverBound("SetMaxSubagentRounds")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.MaxSubagentRounds = clampMaxSubagentRounds(rounds)
	return a.saveSettings()
}

// SetSubagentModel مدل اختیاری زیرایجنت را ذخیره می‌کند (خالی = مدل اصلی).
func (a *App) SetSubagentModel(model string) error {
	defer a.recoverBound("SetSubagentModel")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.SubagentModel = strings.TrimSpace(model)
	return a.saveSettings()
}

// SetTheme تم رابط کاربری را ذخیره می‌کند (dark | light).
func (a *App) SetTheme(theme string) error {
	defer a.recoverBound("SetTheme")
	a.mu.Lock()
	defer a.mu.Unlock()
	if theme != "light" && theme != "dark" {
		theme = "dark"
	}
	a.settings.Theme = theme
	return a.saveSettings()
}

// SetAppearance تنظیمات نمایش UI — اندازه فونت (۱۲ تا ۱۸) و تراکم
// (compact | normal | relaxed). مقادیر نامعتبر به محدوده/پیش‌فرض برمی‌گردند.
func (a *App) SetAppearance(fontSize int, density string) error {
	defer a.recoverBound("SetAppearance")
	a.mu.Lock()
	defer a.mu.Unlock()
	if fontSize < 12 {
		fontSize = 12
	}
	if fontSize > 18 {
		fontSize = 18
	}
	if density != "compact" && density != "relaxed" {
		density = "normal"
	}
	a.settings.FontSize = fontSize
	a.settings.Density = density
	return a.saveSettings()
}

// normalizeProxyMode مقدار خالی/ناشناخته و env قدیمی را به auto برمی‌گرداند
// (حالت هوشمند). off و custom انتخاب صریح کاربرند و دست‌نخورده می‌مانند.
func normalizeProxyMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "off", "none", "direct":
		return "off"
	case "custom":
		return "custom"
	default:
		return "auto"
	}
}

// SetProxySettings حالت و آدرس پروکسی را برای درخواست‌های چت/تست ذخیره می‌کند.
// mode: auto | off | custom — برای custom مقدار url لازم است (مثلاً http://127.0.0.1:7890).
func (a *App) SetProxySettings(mode, url string) error {
	defer a.recoverBound("SetProxySettings")
	mode = normalizeProxyMode(mode)
	url = strings.TrimSpace(url)
	if mode == "custom" {
		if url == "" {
			return errors.New("برای حالت پروکسی سفارشی، آدرس پروکسی لازم است")
		}
		u, err := parseProxyURL(url)
		if err != nil {
			return err
		}
		_ = u
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.ProxyMode = mode
	if mode == "custom" {
		a.settings.ProxyURL = url
	}
	return a.saveSettings()
}

func copyInt64Map(m map[string]int64) map[string]int64 {
	if m == nil {
		return map[string]int64{}
	}
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
