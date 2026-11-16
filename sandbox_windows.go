//go:build windows

// سندباکس اجرای دستور در ویندوز — AppContainer واقعی.
//
// چرا AppContainer: تنها مکانیزم جداسازی واقعی در ویندوز است که بدون
// دسترسی Administrator کار می‌کند. فرایند با یک SID اختصاصی AppContainer
// اجرا می‌شود؛ دسترسی فایل‌سیستم با ACL تعیین می‌شود، نه با «قصد» برنامه:
//
//   - پوشه‌ی کاری: خواندن/نوشتن (ACE اختصاصی AppContainer روی درخت پروژه)
//   - پوشه‌ی خانه‌ی سندباکس (TEMP/HOME/GOCACHE): خواندن/نوشتن
//   - کش ماژول Go: فقط خواندن تا buildهای آفلاین کار کنند
//   - مسیر ابزارهای PATH که ویندوز برای AppContainer باز نکرده: فقط خواندن
//   - بقیه‌ی پروفایل کاربر (اسناد، .ssh، AppData، …): ممنوع
//
// فرایند در یک Job Object با KILL_ON_JOB_CLOSE و سقف ۱۲۸ فرایند اجرا می‌شود؛
// پایان مهلت یا لغو، کل درخت را با TerminateJobObject می‌کشد. خروجی از یک
// pipe مشترک stdout/stderr خوانده و با boundedOutput کران‌دار می‌شود.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	sandboxProfileName = "MyAI.Sandbox.v1"

	// مقادیر مستند Win32 (در x/sys ثابت نیستند).
	attrHandleList           = 0x00020002 // PROC_THREAD_ATTRIBUTE_HANDLE_LIST
	attrSecurityCapabilities = 0x00020009 // PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES
	flagExtendedStartupInfo  = 0x00080000 // EXTENDED_STARTUPINFO_PRESENT
	flagUnicodeEnvironment   = 0x00000400 // CREATE_UNICODE_ENVIRONMENT
	flagNoWindow             = 0x08000000 // CREATE_NO_WINDOW
	flagSuspended            = 0x00000004 // CREATE_SUSPENDED
	flagDetached             = 0x00000008 // DETACHED_PROCESS

	jobLimitActiveProcess = 0x00000008 // JOB_OBJECT_LIMIT_ACTIVE_PROCESS
	jobLimitKillOnClose   = 0x00002000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE

	sandboxMaxProcesses = 128
	seGroupEnabled      = 0x00000004
	internetClientSID   = "S-1-15-3-1" // قابلیت شبکه‌ی AppContainer
)

var (
	sandboxKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	sandboxUserenv  = windows.NewLazySystemDLL("userenv.dll")
	sandboxAdvapi32 = windows.NewLazySystemDLL("advapi32.dll")

	procInitAttrList  = sandboxKernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateAttr    = sandboxKernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteAttr    = sandboxKernel32.NewProc("DeleteProcThreadAttributeList")
	procCreateProfile = sandboxUserenv.NewProc("CreateAppContainerProfile")
	procDeriveProfile = sandboxUserenv.NewProc("DeriveAppContainerSidFromAppContainerName")
	procSIDFromString = sandboxAdvapi32.NewProc("ConvertStringSidToSidW")
)

// securityCapabilities معادل SECURITY_CAPABILITIES ویندوز. چیدمان باید
// دقیقاً با struct سیستمی یکی باشد (Reserved یک DWORD است، نه uint64):
// UpdateProcThreadAttribute اندازه‌ی داده را اعتبارسنجی می‌کند و اندازه‌ی
// اشتباه → ERROR_INVALID_PARAMETER («The parameter is incorrect»).
type securityCapabilities struct {
	AppContainerSid *windows.SID
	Capabilities    *windows.SIDAndAttributes
	CapabilityCount uint32
	Reserved        uint32
}

// startupInfoEx معادل STARTUPINFOEXW.
type startupInfoEx struct {
	StartupInfo   windows.StartupInfo
	AttributeList uintptr
}

func sandboxSupported() bool { return true }

// ---------------------------------------------------------------- هویت AppContainer

var (
	sandboxIDOnce sync.Once
	sandboxIDSID  *windows.SID
	sandboxIDStr  string
	sandboxIDErr  error
)

// sandboxIdentity یک‌بار در عمر برنامه پروفایل AppContainer را می‌سازد (یا
// SID موجودش را استخراج می‌کند). ساخت پروفایل نیازی به Administrator ندارد.
func sandboxIdentity() (string, *windows.SID, error) {
	sandboxIDOnce.Do(func() {
		sandboxIDStr, sandboxIDSID, sandboxIDErr = createSandboxIdentity()
	})
	return sandboxIDStr, sandboxIDSID, sandboxIDErr
}

func createSandboxIdentity() (string, *windows.SID, error) {
	name, err := windows.UTF16PtrFromString(sandboxProfileName)
	if err != nil {
		return "", nil, err
	}
	desc, _ := windows.UTF16PtrFromString("MyAI command sandbox (AppContainer)")
	// هر دو تابع userenv یک PSID (اشاره‌گر به ساختار SID) برمی‌گردانند، نه رشته.
	var sid *windows.SID
	hr, _, _ := procCreateProfile.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(desc)),
		0, 0,
		uintptr(unsafe.Pointer(&sid)),
	)
	if int32(hr) < 0 || sid == nil {
		// پروفایل از قبل موجود است (یا ساخت ناموفق) — SID را مشتق می‌کنیم.
		var derived *windows.SID
		hr2, _, callErr := procDeriveProfile.Call(
			uintptr(unsafe.Pointer(name)),
			uintptr(unsafe.Pointer(&derived)),
		)
		if int32(hr2) < 0 || derived == nil {
			return "", nil, fmt.Errorf("AppContainer profile unavailable (0x%08X / 0x%08X: %v)", uint32(hr), uint32(hr2), callErr)
		}
		sid = derived
	}
	return sid.String(), sid, nil
}

func sidFromString(s string) (*windows.SID, error) {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return nil, err
	}
	var out *windows.SID
	r, _, callErr := procSIDFromString.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&out)))
	if r == 0 || out == nil {
		return nil, fmt.Errorf("invalid SID %q: %v", s, callErr)
	}
	return out, nil
}

// ---------------------------------------------------------------- ACL (icacls)

var (
	sandboxGrantMu     sync.Mutex
	sandboxGranted     = map[string]bool{}
	sandboxGrantFailed = map[string]bool{}
	// ابزارها یک‌بار مجوز می‌گیرند، اما با خاموش‌کردن سندباکس مجوزها پس
	// گرفته می‌شوند؛ پس این حالت باید قابل بازنشانی باشد (sync.Once نه).
	sandboxToolMu      sync.Mutex
	sandboxToolGranted bool
)

func sandboxCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "MyAI", "sandbox")
}

func grantKey(path, perm string) string {
	return strings.ToLower(filepath.Clean(path)) + "|" + perm
}

// grantMarkerOK: نشانه‌ی ماندگار «این مسیر قبلاً برای این SID و این سطح
// دسترسی تنظیم شده» — تا icacls /T روی درخت‌های بزرگ هر بار اجرا نشود.
func grantMarkerOK(path, sid, perm string) bool {
	b, err := os.ReadFile(filepath.Join(sandboxCacheDir(), markerName(path, perm)))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(b)) == sid+"\n"+filepath.Clean(path)
}

func markerName(path, perm string) string {
	h := fnv1a(strings.ToLower(filepath.Clean(path)) + "|" + perm)
	return fmt.Sprintf("grant-%08x-%s", h, perm)
}

func fnv1a(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

func writeGrantMarker(path, sid, perm string) {
	_ = os.MkdirAll(sandboxCacheDir(), 0o700)
	_ = os.WriteFile(filepath.Join(sandboxCacheDir(), markerName(path, perm)), []byte(sid+"\n"+filepath.Clean(path)), 0o600)
}

// sandboxGrantACE به SID سندباکس روی path دسترسی perm می‌دهد. recursive
// یعنی ACE با وراثت (OI)(CI) و اعمال روی همه‌ی فرزندان موجود (/T) — لازم
// است چون ویندوز ACE جدید را به فرزندانِ قدیمی خودکار اعمال نمی‌کند.
func sandboxGrantACE(sid, path, perm string, recursive bool) error {
	path = filepath.Clean(path)
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		return fmt.Errorf("sandbox grant target is not a directory: %s", path)
	}
	key := grantKey(path, perm)
	sandboxGrantMu.Lock()
	done := sandboxGranted[key]
	failed := sandboxGrantFailed[key]
	sandboxGrantMu.Unlock()
	// شکست قبلی (مثل C:\Users که ACLش با دسترسی کاربر قابل تغییر نیست)
	// در همین اجرا دوباره تلاش نمی‌شود — وگرنه هر دستور یک icacls اضافه
	// صرف می‌کرد.
	if failed {
		return nil
	}
	if done || grantMarkerOK(path, sid, perm) {
		sandboxGrantMu.Lock()
		sandboxGranted[key] = true
		sandboxGrantMu.Unlock()
		return nil
	}

	// ACE با وراثت (OI)(CI) فقط روی همین پوشه نوشته می‌شود؛ ویندوز ACEهای
	// ارثی را هنگام access-check از والد محاسبه می‌کند، پس فرزندان موجود هم
	// دسترسی می‌گیرند بدون پیمایش /T (که روی node_modules دقیقه‌ها طول می‌کشید).
	// عمق وراثت با پرچم recursive فقط کنترل می‌شود که ACE ارثی نوشته شود یا نه.
	acl := perm
	if recursive {
		acl = "(OI)(CI)" + perm
	}
	// بدون /C /Q: شکست واقعی باید کد خروج غیرصفر بدهد (fail-closed)، نه
	// این‌که بی‌صدا رد شود و دستور بعداً با «Access is denied» مرموز بماند.
	args := []string{path, "/grant", "*" + sid + ":" + acl}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "icacls", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flagNoWindow}
	out, err := cmd.CombinedOutput()
	if err != nil {
		sandboxGrantMu.Lock()
		sandboxGrantFailed[key] = true
		sandboxGrantMu.Unlock()
		return fmt.Errorf("icacls %s: %w (%s)", path, err, strings.TrimSpace(string(out)))
	}
	sandboxGrantMu.Lock()
	sandboxGranted[key] = true
	sandboxGrantMu.Unlock()
	writeGrantMarker(path, sid, perm)
	return nil
}

// dirAppContainerReadable: آیا ACL این پوشه از قبل به «ALL APPLICATION
// PACKAGES» دسترسی خواندن/اجرا می‌دهد؟ (System32 و بخش زیادی از Program
// Files این‌طورند و نیازی به تغییر ACL ندارند.) متن خروجی icacls در
// ویندوزهای غیرانگلیسی متفاوت است؛ در ابهام «نه» برمی‌گردد و فقط یک
// مجوز خواندن اضافه می‌شود (بی‌خطر).
func dirAppContainerReadable(dir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "icacls", dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flagNoWindow}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "ALL APPLICATION PACKAGES") || strings.Contains(line, "RESTRICTED") {
			continue
		}
		if strings.Contains(line, "(RX)") || strings.Contains(line, "(GR") || strings.Contains(line, "(F)") {
			return true
		}
	}
	return false
}

// sandboxHome پوشه‌ی خانه‌ی ایزوله (TEMP/HOME/کش ابزارها) را می‌سازد و
// برمی‌گرداند؛ کاملاً بیرون از پوشه‌ی پروژه و پوشه‌ی تنظیمات (کلیدها).
func sandboxHome() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		return "", errors.New("sandbox home: user cache directory unavailable")
	}
	home := filepath.Join(base, "MyAI", "Sandbox", "home")
	subs := []string{
		"", "tmp", filepath.Join("AppData", "Local"), filepath.Join("AppData", "Roaming"),
		"go", filepath.Join("go", "cache"), filepath.Join("go", "pkg", "mod"),
		"npm-cache", "pip-cache",
		// ویندوز LOCALAPPDATA را برای AppContainer به
		// …\AppData\Local\Packages\<profile>\AC هدایت می‌کند؛ اگر این
		// پوشه‌ها نباشند go test با «path not found» می‌میرد.
		filepath.Join("AppData", "Local", "Packages", sandboxProfileName, "AC", "Temp"),
		filepath.Join("AppData", "Local", "Packages", sandboxProfileName, "AC", "INetCache"),
		filepath.Join("AppData", "Roaming", "Packages", sandboxProfileName, "AC", "Temp"),
	}
	for _, sub := range subs {
		if err := os.MkdirAll(filepath.Join(home, sub), 0o700); err != nil {
			return "", fmt.Errorf("sandbox home: %w", err)
		}
	}
	return home, nil
}

// sandboxGrantTraverse به SID سندباکس حق «عبور» (X، بدون خواندن/لیست)
// روی همه‌ی والدهای path می‌دهد. بدون این ACE، توکن AppContainer (که عضو
// Users نیست و SeChangeNotifyPrivilege هم ندارد) نمی‌تواند از
// C:\Users\...\Temp به پوشه‌ی کاری برسد: هر دستور با «Access is denied»
// شکست می‌خورد، حتی dir ساده. خودِ پوشه‌ی کاری با M در sandboxGrantACE
// پوشش داده می‌شود.
func sandboxGrantTraverse(sid, path string) {
	dir := filepath.Dir(filepath.Clean(path))
	for {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			_ = sandboxGrantACE(sid, dir, "X", false)
		}
		parent := filepath.Dir(dir)
		if parent == dir || filepath.Dir(parent) == parent {
			// به ریشه‌ی درایو نرسیده‌ایم که ACLش معمولاً با دسترسی کاربر
			// قابل تغییر نیست (TrustedInstaller/مالک دیگر)؛ تلاش برای
			// اعطای ACE روی ریشه فقط یک icacls شکست‌خورنده‌ی اضافه است.
			return
		}
		dir = parent
	}
}

// isSystemDir آیا مسیر زیر پوشه‌ی Windows است؟ فقط همین‌جا از بررسی/اعطای
// ACL صرف‌نظر می‌کنیم (System32 از قبل ALL APPLICATION PACKAGES دارد).
// Program Files عمداً مستثنی نیست: نصب‌های معمول Go/Node در آنجا هستند و
// برخی (مثل C:\Program Files\Go) ACE مخصوص AppContainer ندارند.
func isSystemDir(dir string) bool {
	root := strings.TrimSpace(os.Getenv("SystemRoot"))
	return root != "" && withinDir(dir, root)
}

// sandboxEnsureToolAccess یک‌بار در عمر برنامه: مسیر ابزارهای PATH که
// AppContainer نمی‌تواند بخواند، مجوز فقط-خواندن می‌گیرند؛ کش ماژول Go هم
// فقط-خواندنی می‌شود تا build آفلاین کار کند (نوشتن در cache/download مجاز).
func sandboxEnsureToolAccess(sid string) {
	sandboxToolMu.Lock()
	defer sandboxToolMu.Unlock()
	if sandboxToolGranted {
		return
	}
	defer func() { sandboxToolGranted = true }()
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		// System32/Program Files از قبل به ALL APPLICATION PACKAGES
		// دسترسی خواندن می‌دهند؛ بررسی/اعطای دوباره فقط کندی است.
		if isSystemDir(dir) {
			continue
		}
		if dirAppContainerReadable(dir) {
			continue
		}
		// خطای مجوز ابزار نباید اجرای کل برنامه را متوقف کند؛ دستور
		// مربوطه در صورت نیاز خطای «Access is denied» روشن می‌دهد.
		_ = sandboxGrantACE(sid, dir, "RX", true)
		// go.exe در <root>\bin است اما GOROOT (src/pkg) یک سطح بالاتر؛
		// بدون خواندن GOROOT، go داخل سندباکس با کد ۲ و خروجی خالی می‌میرد.
		if _, serr := os.Stat(filepath.Join(dir, "go.exe")); serr == nil {
			_ = sandboxGrantACE(sid, filepath.Dir(filepath.Clean(dir)), "RX", true)
		}
	}
	if gm := strings.TrimSpace(os.Getenv("GOMODCACHE")); gm != "" {
		_ = sandboxGrantACE(sid, gm, "RX", true)
		_ = sandboxGrantACE(sid, filepath.Join(gm, "cache"), "M", true)
	}
}

// ---------------------------------------------------------------- محیط ایزوله

func sandboxEnvironment(home string, allowNetwork bool) []uint16 {
	netFlag := "1"
	if !allowNetwork {
		netFlag = "0"
	}
	overrides := map[string]string{
		"TEMP":             filepath.Join(home, "tmp"),
		"TMP":              filepath.Join(home, "tmp"),
		"TMPDIR":           filepath.Join(home, "tmp"),
		"HOME":             home,
		"USERPROFILE":      home,
		"LOCALAPPDATA":     filepath.Join(home, "AppData", "Local"),
		"APPDATA":          filepath.Join(home, "AppData", "Roaming"),
		"GOPATH":           filepath.Join(home, "go"),
		"GOCACHE":          filepath.Join(home, "go", "cache"),
		"npm_config_cache": filepath.Join(home, "npm-cache"),
		"PIP_CACHE_DIR":    filepath.Join(home, "pip-cache"), // جلوگیری از دانلود خودکار toolchain (شبکه ممکن است بسته باشد)؛
		// نصب محلی Go همان است که خارج از سندباکس کار می‌کند.
		"GOTOOLCHAIN":          "local",
		"MYAI_SANDBOX":         "1",
		"MYAI_SANDBOX_NETWORK": netFlag,
	}
	merged := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			merged[strings.ToUpper(kv[:i])] = kv[i+1:]
		}
	}
	for k, v := range overrides {
		merged[strings.ToUpper(k)] = v
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := make([]string, 0, len(keys))
	for _, k := range keys {
		s = append(s, k+"="+merged[k])
	}
	// encode واقعی UTF-16 (نه بایت‌به‌uint16): مسیرهای فارسی/غیرASCII در
	// USERPROFILE/TEMP باید سالم به فرایند برسند.
	block := strings.Join(s, "\x00") + "\x00"
	u := utf16.Encode([]rune(block))
	return append(u, 0)
}

// ---------------------------------------------------------------- ساخت فرایند

func runSandboxedShell(ctx context.Context, dir, command string, timeout time.Duration, onOutput func([]byte), pol *sandboxPolicy) (string, int, error) {
	if pol == nil {
		return runShell(ctx, dir, command, timeout, onOutput)
	}
	// timeout<=0 یعنی «بدون مهلت» (اجرای پس‌زمینه/بلندمدت)؛ لغو همچنان از
	// طریق ctx کار می‌کند و watchdog همان درخت فرایند را می‌کشد.
	cctx, cancel := ctx, context.CancelFunc(func() {})
	if timeout > 0 {
		if timeout > shellMaxTimeout {
			timeout = shellMaxTimeout
		}
		cctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	// «dir» را خودمان انجام می‌دهیم: cmd برای سرصفحه‌ی dir به اطلاعات volume
	// ریشه‌ی درایو نیاز دارد که توکن AppContainer بدون Administrator به آن
	// نمی‌رسد (نتیجه: «Access is denied.» حتی برای dir /b). فقط مسیرهای
	// داخل پوشه‌ی کاری مجازند تا ایزوله‌سازی دور زده نشود.
	if text, code, handled, derr := sandboxRunChain(cctx, pol.Workspace, command, timeout, pol); handled {
		if onOutput != nil && text != "" {
			onOutput([]byte(text))
		}
		if derr == nil && code != 0 {
			derr = fmt.Errorf("exit status %d", code)
		}
		return text, code, derr
	}

	if text, code, handled, derr := sandboxBuiltinDir(command, pol.Workspace); handled {
		if onOutput != nil && text != "" {
			onOutput([]byte(text))
		}
		if derr == nil && code != 0 {
			derr = fmt.Errorf("exit status %d", code)
		}
		return text, code, derr
	}

	sidStr, sid, err := sandboxIdentity()
	if err != nil {
		return "", -1, fmt.Errorf("sandbox unavailable: %w", err)
	}
	home, err := sandboxHome()
	if err != nil {
		return "", -1, err
	}
	if err := sandboxGrantACE(sidStr, home, "F", true); err != nil {
		return "", -1, err
	}
	if err := sandboxGrantACE(sidStr, pol.Workspace, "F", true); err != nil {
		return "", -1, err
	}
	sandboxGrantTraverse(sidStr, home)
	sandboxGrantTraverse(sidStr, pol.Workspace)
	sandboxEnsureToolAccess(sidStr)

	// دستور ساده مستقیماً (بدون cmd) اجرا می‌شود تا AppContainer هندل‌های
	// std را درست به فرایند بدهد؛ دستور مرکب از cmd عبور می‌کند.
	appExe, cmdLine, direct := sandboxLaunchTarget(command, pol.Workspace)
	if !direct {
		comspec := strings.TrimSpace(os.Getenv("COMSPEC"))
		if comspec == "" {
			comspec = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
		}
		appExe = comspec
		cmdLine = `cmd.exe /S /C "` + escapeCmdExeQuotes(command) + `"`
	}
	appName, err := windows.UTF16PtrFromString(appExe)
	if err != nil {
		return "", -1, err
	}
	lineBuf, err := windows.UTF16FromString(cmdLine)
	if err != nil {
		return "", -1, err
	}
	dirBuf, err := windows.UTF16PtrFromString(pol.Workspace)
	if err != nil {
		return "", -1, err
	}
	env := sandboxEnvironment(home, pol.AllowNetwork)

	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	var readH, writeH windows.Handle
	if err := windows.CreatePipe(&readH, &writeH, sa, 0); err != nil {
		return "", -1, fmt.Errorf("sandbox pipe: %w", err)
	}
	defer windows.CloseHandle(readH)
	_ = windows.SetHandleInformation(readH, windows.HANDLE_FLAG_INHERIT, 0)

	nulName, _ := windows.UTF16PtrFromString("NUL")
	nul, err := windows.CreateFile(nulName, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, sa, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		_ = windows.CloseHandle(writeH)
		return "", -1, fmt.Errorf("sandbox stdin: %w", err)
	}
	defer windows.CloseHandle(nul)

	list, listBuf, err := newAttributeList(2)
	if err != nil {
		_ = windows.CloseHandle(writeH)
		return "", -1, err
	}
	defer procDeleteAttr.Call(list)

	caps := make([]windows.SIDAndAttributes, 0, 1)
	if pol.AllowNetwork {
		if capSID, cerr := sidFromString(internetClientSID); cerr == nil {
			caps = append(caps, windows.SIDAndAttributes{Sid: capSID, Attributes: seGroupEnabled})
		}
	}
	var capPtr *windows.SIDAndAttributes
	if len(caps) > 0 {
		capPtr = &caps[0]
	}
	secCaps := securityCapabilities{
		AppContainerSid: sid,
		Capabilities:    capPtr,
		CapabilityCount: uint32(len(caps)),
	}
	if r, _, e := procUpdateAttr.Call(list, 0, attrSecurityCapabilities,
		uintptr(unsafe.Pointer(&secCaps)), unsafe.Sizeof(secCaps), 0, 0); r == 0 {
		_ = windows.CloseHandle(writeH)
		return "", -1, fmt.Errorf("sandbox: security capabilities attribute: %v", e)
	}
	handles := []windows.Handle{nul, writeH}
	if r, _, e := procUpdateAttr.Call(list, 0, attrHandleList,
		uintptr(unsafe.Pointer(&handles[0])), uintptr(len(handles))*unsafe.Sizeof(handles[0]), 0, 0); r == 0 {
		_ = windows.CloseHandle(writeH)
		return "", -1, fmt.Errorf("sandbox: handle list attribute: %v", e)
	}

	si := startupInfoEx{}
	si.StartupInfo.Cb = uint32(unsafe.Sizeof(si))
	si.StartupInfo.Flags = windows.STARTF_USESTDHANDLES
	si.StartupInfo.StdInput = nul
	si.StartupInfo.StdOutput = writeH
	si.StartupInfo.StdErr = writeH
	si.AttributeList = list

	var pi windows.ProcessInformation
	flags := uint32(flagUnicodeEnvironment | flagExtendedStartupInfo | flagSuspended | flagNoWindow)
	if err := windows.CreateProcess(
		appName, &lineBuf[0], nil, nil, true, flags,
		&env[0], dirBuf, (*windows.StartupInfo)(unsafe.Pointer(&si)), &pi,
	); err != nil {
		_ = windows.CloseHandle(writeH)
		return "", -1, fmt.Errorf("sandbox: create process: %w", err)
	}
	runtimeKeepAlive(listBuf, secCaps, caps, handles, env, lineBuf)
	_ = windows.CloseHandle(writeH)
	defer windows.CloseHandle(pi.Process)
	defer windows.CloseHandle(pi.Thread)

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return "", -1, fmt.Errorf("sandbox: job object: %w", err)
	}
	defer windows.CloseHandle(job)
	var jobInfo windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	jobInfo.BasicLimitInformation.LimitFlags = jobLimitKillOnClose | jobLimitActiveProcess
	jobInfo.BasicLimitInformation.ActiveProcessLimit = sandboxMaxProcesses
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&jobInfo)), uint32(unsafe.Sizeof(jobInfo))); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return "", -1, fmt.Errorf("sandbox: job limits: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, pi.Process); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return "", -1, fmt.Errorf("sandbox: assign job: %w", err)
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return "", -1, fmt.Errorf("sandbox: resume: %w", err)
	}

	out := &boundedOutput{onOutput: onOutput}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 32*1024)
		for {
			var n uint32
			rerr := windows.ReadFile(readH, buf, &n, nil)
			if n > 0 {
				_, _ = out.Write(buf[:n])
			}
			if rerr != nil {
				return
			}
		}
	}()

	waitDone := make(chan struct{})
	go func() {
		_, _ = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		close(waitDone)
	}()

	timedOut := false
	select {
	case <-waitDone:
	case <-cctx.Done():
		timedOut = cctx.Err() == context.DeadlineExceeded
		_ = windows.TerminateJobObject(job, 1)
		<-waitDone
	}
	var code uint32 = 1
	_ = windows.GetExitCodeProcess(pi.Process, &code)

	_ = windows.CloseHandle(job)
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
	}

	text := out.String()
	if timedOut {
		return text, int(code), context.DeadlineExceeded
	}
	if code != 0 {
		return text, int(code), fmt.Errorf("exit status %d", code)
	}
	return text, 0, nil
}

// newAttributeList حافظه‌ی لیست ویژگی‌ها را تخصیص و مقداردهی می‌کند؛ بافر
// برگشتی باید تا پایان CreateProcess زنده بماند.
func newAttributeList(count int) (uintptr, []byte, error) {
	var size uintptr
	_, _, _ = procInitAttrList.Call(0, uintptr(count), 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 {
		return 0, nil, errors.New("sandbox: attribute list size is zero")
	}
	buf := make([]byte, size)
	r, _, e := procInitAttrList.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(count), 0, uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return 0, nil, fmt.Errorf("sandbox: initialize attribute list: %v", e)
	}
	return uintptr(unsafe.Pointer(&buf[0])), buf, nil
}

// runtimeKeepAlive جلوگیری از جمع‌آوری/جابه‌جایی بافرهایی که اشاره‌گرشان
// تا پایان CreateProcess به ویندوز داده شده است.
func runtimeKeepAlive(vs ...interface{}) {
	for _, v := range vs {
		_ = v
	}
}
