// هسته‌ی اپلیکیشن MyAI — ساختار App و متدهای متصل به فرانت‌اند (Wails Bind).
package main

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App وضعیت سراسری برنامه.
type App struct {
	ctx context.Context

	mu       sync.Mutex // محافظ تنظیمات و گفتگوها
	genMu    sync.Mutex // محافظ وضعیت پاسخ‌دهی جاری
	gen      *genState  // پاسخ‌دهی فعال (nil = هیچ)
	settings *Settings

	startedAt int64

	// آخرین زمان‌بندی نوبت (latency.go) — قفل برگ، برای GetLastTurnTiming.
	timingMu   sync.Mutex
	lastTiming TurnTiming

	// مدیریت تغییرات فایل ایجنت (diff/تأیید/revert)
	changes *changesStore

	// permAskMu سریال‌سازی مودال‌های مجوز: اجرای موازی ابزارهای
	// فقط‌خواندنی ممکن است چند تأیید هم‌زمان بخواهد (مثلاً دو read_file روی
	// فایل حساس)؛ فقط requestPermissionConfirm این قفل را می‌گیرد و عمداً در
	// طول انتظار پاسخ کاربر نگهش می‌دارد (کارش همین است). در طول آن هیچ
	// قفلی جز changes.mu لحظه‌ای گرفته نمی‌شود — ترتیب: permAskMu → changes.mu
	// (سند ترتیب قفل‌ها در ARCHITECTURE.md).
	permAskMu sync.Mutex

	// هشدار حجم گفتگو — فقط یک‌بار برای هر گفتگو در هر اجرا
	sizeWarned map[string]bool

	// uiReady فقط پس از startup واقعی Wails true می‌شود؛ رویدادها در
	// حالت تست/هدلس هرگز emit نمی‌شوند (EventsEmit با ctx نامعتبر → log.Fatal).
	uiReady atomic.Bool

	// Cache مسیر configDir — یک‌بار محاسبه و برای عمر برنامه نگه‌داری می‌شود.
	// sync.Once تضمین می‌کند حتی اگر چند goroutine هم‌زمان configDir را صدا
	// بزنند، computeConfigDir فقط یک‌بار اجرا شود.
	configDirOnce   sync.Once
	cachedConfigDir string
	configDirErr    error

	// rulesCache کش محتوای فایل قوانین پروژه بر اساس (workspace، مسیر،
	// حجم، mtime) — با a.mu محافظت می‌شود (context.go).
	rulesCache rulesFileCache

	// todosEmit جایگزین تست برای انتشار رویداد chat:todos (todos.go)؛
	// در حالت عرفی nil است و مسیر استاندارد runtimeEmit اجرا می‌شود.
	// باید فقط پیش از شروع هم‌زمانی‌ها (در تست) تنظیم شود — بدون قفل
	// خوانده می‌شود. هیچ قفل دیگری هنگام نگه‌داشتن آن گرفته نمی‌شود.
	todosEmit func(convID string, items []TodoItem)

	// permAskEmit مشاهده‌گر تستی askهای دسترسی (permissions.go): payload
	// مودال (kind/message/url/…) بدون UI واقعی قابل بررسی می‌شود. همان
	// قرارداد todosEmit — فقط پیش از هم‌زمانی‌ها تنظیم شود.
	permAskEmit func(ask *permissionAsk)

	// eventEmit جایگزین تستیِ runtimeEmit: اگر تنظیم شود، هر رویداد (نام +
	// payload) فقط به آن داده می‌شود — برای سناریوهای ساخت‌یافته‌ی
	// مرحله D (شمارش مودال/نوتیس/خلاصه‌ی نوبت) بدون UI و بدون Wails.
	// همان قرارداد todosEmit: فقط پیش از هم‌زمانی‌ها تنظیم شود.
	eventEmit func(name string, data []interface{})

	// mcp مدیر سرورهای MCP (mcpmanager.go). قفل برگ (mcp.mu) — هرگز
	// گرفته‌شده‌ی a.mu حرف با سرور نمی‌زند؛ ترتیب کامل در ARCHITECTURE.md.
	mcp *mcpManager

	// bgMu/bgProcs فرایندهای پس‌زمینه‌ی نشست (processes.go). قفل برگ است؛
	// هرگز زیر a.mu گرفته نمی‌شود و کار طولانی زیر آن انجام نمی‌شود.
	bgMu    sync.Mutex
	bgProcs map[string]*bgProcess

	// sessionAllowWrites اگر true باشد، تأیید نوشتن فایل برای بقیه‌ی عمر
	// برنامه (یا تا خاموش شدن صریح) رد می‌شود — از مودال Diff با
	// «اجازه تا پایان نشست». جدا از ConfirmFileWrites در settings است.
	sessionAllowWrites atomic.Bool

	// sessionRules قاعده‌های دستورِ «اجازه تا پایان نشست» (permissions.go):
	// حافظه‌ای، با بستن برنامه محو می‌شوند؛ با a.mu محافظت می‌شوند و
	// پس از قاعده‌های ماندگارِ settings ارزیابی می‌شوند.
	sessionRules []PermissionRule

	// sessionWebHosts قاعده‌های دامنه‌ی «اجازه تا پایان نشست» برای web_fetch
	// (webfetchperm.go): حافظه‌ای، با a.mu محافظت می‌شوند و پیش از
	// قاعده‌های ماندگارِ settings.WebHosts بررسی می‌شوند.
	sessionWebHosts []PermissionRule

	// genAllowWrites اگر true باشد، تأیید نوشتن فقط تا پایان پاسخ‌دهی
	// جاری (finishChat / خطا / Stop) لازم نیست.
	genAllowWrites atomic.Bool

	// turnCounter شمارنده‌ی صعودیِ شناسه‌ی نوبت‌ها (turnreview.go) — atomic،
	// بدون قفل؛ هر streamChat یک مقدار می‌گیرد.
	turnCounter atomic.Int64

	// readStore محافظ «خواندن پیش از نوشتن» (fileread.go): اثر انگشت
	// فایل‌هایی که ایجنت خوانده/نوشته، به‌ازای هر گفتگو، با LRU کران‌دار.
	// قفل داخلی‌اش برگ است و هیچ قفل دیگری زیرش گرفته نمی‌شود.
	readStore fileReadStore

	// factsCache کش «واقعیت‌های پروژه» (projectfacts.go) بر اساس مسیر
	// پوشه‌ی کاری و اثر انگشت فایل‌های تعریف ساخت. قفل داخلی — برگ.
	factsCache projectFactsCache

	// repoMapCache کش کوتاه‌عمرِ «نقشهٔ مخزن» (repomap.go) — یک‌بار در هر نوبت
	// برای پرامپت اصلی و زیرایجنت ساخته می‌شود. قفل داخلی — برگ.
	repoMapCache repoMapCache

	// checkpointMu قفل برگِ وضعیت نقطه‌ی بازگشت (checkpoint.go): کدام
	// (گفتگو، نوبت) قبلاً تلاش شده و کدام اعلان‌ها قبلاً دیده شده‌اند.
	// هیچ I/O و هیچ git ای زیر این قفل انجام نمی‌شود.
	checkpointMu       sync.Mutex
	checkpointTurns    map[string]bool
	checkpointNotified map[string]bool
}

// ensureUTF8ChildEnv کدگذاری خروجیِ برنامه‌های فرزند را UTF-8 می‌کند.
//
// مشکل واقعی (روی یک ویندوز با کدپیج ANSI = cp1252 دیده شد): پایتون وقتی
// stdout یک pipe است از کدپیج ANSI استفاده می‌کند، پس چاپ متن فارسی با
// UnicodeEncodeError می‌میرد و خروجی کاملاً از دست می‌رود. `chcp 65001`
// فقط کنسول را عوض می‌کند، نه کدگذاری pipe. MyAI خروجی را UTF-8 می‌خواند،
// پس همان را به فرزندها هم می‌دهیم؛ مقدار دلخواه کاربر دست‌نخورده می‌ماند.
func ensureUTF8ChildEnv() {
	if os.Getenv("PYTHONIOENCODING") == "" {
		_ = os.Setenv("PYTHONIOENCODING", "utf-8")
	}
}

// NewApp ساخت نمونه‌ی اپ.
func NewApp() *App {
	ensureUTF8ChildEnv()
	a := &App{}
	a.initMCPManager()
	return a
}

// startup هنگام راه‌اندازی پنجره فراخوانی می‌شود.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.startedAt = timeNow()
	a.uiReady.Store(true)
	a.initChangesStore()

	// نمونه‌ی فعال برای لاگ خطاها (logAppError بدون گیرنده App)
	appForLogs = a

	// راه‌اندازی رمزنگاری: I/O مربوط به Keyring اینجا و «بیرون از قفل‌ها»
	// انجام می‌شود تا decryptKey بعداً فقط از کلید کش‌شده استفاده کند.
	initCrypto()

	// مسیر config را cache می‌کنیم تا فراخوانی‌های بعدی از os.MkdirAll معاف باشند.
	// (خود sync.Once هم lazy است؛ این pre-warm صرفاً خطاهای اولیه را زودتر آشکار می‌کند.)
	_, _ = a.configDir()

	if err := a.loadSettings(); err != nil {
		runtime.LogWarningf(ctx, "load settings: %v", err)
		a.settings = defaultSettings()
	}

	// پایش خودکار شارژ سرویس‌دهنده (usageguard.go): هر ۱۵ دقیقه موجودی Provider
	// فعال بررسی می‌شود؛ افزایش موجودی یا بازیابی پس از خطای سهمیه → شمارنده‌های
	// مصرف خودکار صفر می‌شوند. کاربر دیگر دکمهٔ «صفر کردن» ندارد.
	if a.settings != nil && a.settings.AutoUsageReset {
		startUsageGuard(a)
	}

	// گرم‌کردن سرورهای MCP در پس‌زمینه — تا اسپاون/initialize/tools/list
	// در نخستین نوبت و وسط TTFT انجام نشود (mcpWarmStart، بی‌صدا و best-effort).
	safeGo(a, "mcp.warmStart", func() { a.mcpWarmStart(context.Background()) })

	// سندباکس ویندوز: ACE دستگاه NUL با هر بوت بازنشانی می‌شود؛ اسکریپت را
	// در Startup کاربر ثبت و همین حالا (بی‌صدا، best-effort) اجرا می‌کنیم.
	if sandboxSupported() {
		go func() {
			if err := sandboxPlatformStartup(); err != nil {
				runtime.LogWarningf(ctx, "sandbox nul grant: %v", err)
			}
		}()
	}

	// پوشه‌ی کاری پیش‌فرض (…\Projects\MyWorkspace) در اولین اجرا باید روی
	// دیسک ساخته شود؛ loadSettings فقط رشته‌ی مسیر را ست می‌کند.
	if err := a.ensureDefaultWorkspace(); err != nil {
		runtime.LogWarningf(ctx, "default workspace: %v", err)
	}

	// جاروی فایل‌های.tmp جامانده از نوشتن‌های اتمیک نیم‌کاره (کرش بین
	// نوشتن و rename). کل درخت config، بازگشتی، best-effort.
	if cdir, cerr := a.configDir(); cerr == nil {
		if n := sweepStaleTempFiles(cdir); n > 0 {
			runtime.LogWarningf(ctx, "startup: swept %d stale .tmp file(s)", n)
		}
		// سبد زباله‌ی نقطه‌ی بازگشت: قدیمی‌تر از ۱۴ روز یا بیش از ۵۰۰MB
		// (best-effort؛ بازگردانی هرگز داده را بی‌اطلاع حذف نمی‌کند).
		if n := sweepCheckpointTrash(cdir); n > 0 {
			runtime.LogWarningf(ctx, "startup: swept %d old checkpoint trash folder(s)", n)
		}
	}
}

// shutdown هنگام بستن پنجره.
func (a *App) shutdown(ctx context.Context) {
	a.StopGeneration()
	// انتظار کوتاه برای خروج کامل goroutine استریم — بدون این، ذخیره‌ی
	// آخرین نوبت گفتگو وسط کار رها می‌شد (نوشتن اتمیک، فایل را سازگار
	// نگه می‌دارد ولی آخرین partial-save از دست می‌رفت).
	a.stopGenerationAndWait(2 * time.Second)
	// کشتن کل درخت فرایند سرورهای MCP — بدون زامبی، پیش از ذخیره‌ی تنظیمات
	// (mcp.mu قفل برگ است؛ کشتن بیرون از هر دو قفل انجام می‌شود).
	if a.mcp != nil {
		a.mcp.stopAll()
	}
	// کشتن فرایندهای پس‌زمینه‌ی همین نشست (dev server/watch) — خروج تمیز.
	a.stopAllBackgroundProcesses()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.settings != nil {
		_ = a.saveSettings()
	}
}

// ToggleFullscreen وضعیت تمام‌صفحه‌ی واقعی پنجره را برمی‌گرداند (F11).
// جدا از «بیشینه» (maximize) است: نوار عنوان و حاشیه‌ی پنجره مخفی می‌شوند.
func (a *App) ToggleFullscreen() {
	defer a.recoverBound("ToggleFullscreen")
	if a.ctx == nil {
		return
	}
	if runtime.WindowIsFullscreen(a.ctx) {
		runtime.WindowUnfullscreen(a.ctx)
	} else {
		runtime.WindowFullscreen(a.ctx)
	}
}

// supportURL صفحه‌ی حمایت از پروژه (دکمه‌ی قلب در صفحه‌ی اصلی).
const supportURL = "https://www.coffeebede.com/khademdev"

// OpenSupportPage صفحه‌ی حمایت از پروژه را در مرورگر پیش‌فرض سیستم باز
// می‌کند. در محیط تست (بدون Wails) بی‌خطر است و کاری نمی‌کند.
func (a *App) OpenSupportPage() {
	defer a.recoverBound("OpenSupportPage")
	if a.ctx == nil {
		return
	}
	runtime.BrowserOpenURL(a.ctx, supportURL)
}

// SelectFolder دیالوگ انتخاب پوشه‌ی سیستم‌عامل را باز می‌کند.
// در صورت لغو توسط کاربر، رشته خالی و خطای nil برمی‌گردد.
func (a *App) SelectFolder() (string, error) {
	defer a.recoverBound("SelectFolder")
	a.mu.Lock()
	current := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()

	if current != "" {
		if st, err := os.Stat(current); err != nil || !st.IsDir() {
			current = ""
		}
	}
	if current == "" {
		if home, herr := os.UserHomeDir(); herr == nil {
			current = home
		}
	}

	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "انتخاب پوشه‌ی کاری (مسیر پروژه)",
		DefaultDirectory: current,
	})
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(path), nil
}

// runtimeEmit رویداد Wails را فقط در محیط واقعی UI می‌فرستد.
// در تست‌ها (بدون Wails) EventsEmit با log.Fatal از بین می‌برد؛ فلگ uiReady
// این مسیر را قطع می‌کند.
func runtimeEmit(a *App, name string, data ...interface{}) {
	if a == nil {
		return
	}
	if a.eventEmit != nil {
		a.eventEmit(name, data)
		return
	}
	if a.ctx == nil || !a.uiReady.Load() {
		return
	}
	runtime.EventsEmit(a.ctx, name, data...)
}
