// تزریق کانتکست پروژه به گفتگو — قوانین پروژه (Project Rules) و ارجاع @mention.
//
// دو مسئولیت مستقل اما هم‌خانواده:
// 1. Project Rules: قوانین ثابت پروژه از فایل (.kodyar/rules.md و سازگارها)
// + قوانین تعریف‌شده از تنظیمات؛ هر دو به system prompt الصاق می‌شوند.
// 2. @mention: توکن‌های @path و @folder/ در پیام کاربر به محتوای واقعی فایل
// تبدیل می‌شوند و به‌صورت بلوک کانتکست به همان پیام الصاق می‌گردند
// (متن اصلی پیام برای نمایش تمیز می‌ماند).
//
// امنیت: همه‌ی مسیرها از resolveWorkspacePath عبور می‌کنند (حفاظت symlink و
// خروج از workspace)؛ فایل‌های باینری شناسایی و رد می‌شوند.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// محدودیت‌های کانتکست
const (
	maxRulesBytes      = 12 * 1024 // سقف کل قوانین تزریق‌شده به system prompt
	maxMentionFileKB   = 40        // سقف هر فایل @mention (کیلوبایت)
	maxMentionTotalKB  = 120       // سقف مجموع فایل‌های الصاق‌شده در یک پیام
	mentionSearchLimit = 12        // تعداد نتایج تکمیل خودکار @
	dirScanFileCap     = 4000      // سقف فایل بررسی‌شده هنگام انتخاب هوشمند پوشه
)

// ruleFileCandidates فایل‌های قوانین پروژه به ترتیب اولویت.
// اولویت: فایل اختصاصی KodYar، سپس سازگاری با ابزارهای رایج.
var ruleFileCandidates = []string{
	filepath.Join(".kodyar", "rules.md"),
	"AGENTS.md",
	"CLAUDE.md",
	".cursorrules",
	".kodyarrules",
	// سازگاری با پروژه‌هایی که فایل قوانین را با نام قدیمی ساخته‌اند.
	filepath.Join(legacyAppDirName, "rules.md"),
	"." + legacyAppDirName + "rules",
}

// rulesFileCache کش خواندن فایل قوانین: کلید = (workspace، مسیر،
// حجم، mtime). اگر فایل عوض نشده باشد، محتوای کش‌شده بدون خواندن دیسک
// برمی‌گردد — prepareStream در هر نوبت و GetRulesInfo در هر باز شدن تب
// تنظیمات به آن تکیه می‌کنند. با a.mu محافظت می‌شود؛ دقیق‌بودنش به
// گرانی مذکری mtime محدود است (در NTFS/ext4 نانوثانیه است).
type rulesFileCache struct {
	ws      string
	rel     string
	size    int64
	mtimeNs int64
	content string
	valid   bool
}

// binaryExt پسوندهایی که هرگز به کانتکست الصاق نمی‌شوند.
// توجه: «.min.js» پسوندِ واقعی فایل نیست (filepath.Ext فقط «.js» را
// برمی‌گرداند)؛ تشخیص آن در isBinaryAsset با پسوند انجام می‌شود.
var binaryExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".ico": true, ".pdf": true, ".zip": true, ".gz": true, ".tar": true,
	".exe": true, ".dll": true, ".so": true, ".bin": true, ".woff": true,
	".woff2": true, ".ttf": true, ".eot": true, ".mp3": true, ".mp4": true,
	".avi": true, ".mov": true, ".jar": true, ".class": true, ".pyc": true,
	".wasm": true, ".lock": true,
}

// isBinaryAsset بررسی باینری بودن نام فایل برای الصاق به کانتکست:
// پسوند در binaryExt یا پایان با «.min.js» (فایل‌های minify شده).
func isBinaryAsset(name string) bool {
	lower := strings.ToLower(name)
	if binaryExt[strings.ToLower(filepath.Ext(lower))] {
		return true
	}
	return strings.HasSuffix(lower, ".min.js")
}

// ---------------------------------------------------------------- قوانین پروژه

// loadRulesFromFileLocked اولین فایل قوانین موجود را برمی‌گرداند.
// خروجی: (محتوا، مسیر نسبی، وجود داشت یا نه)
//
// توجه: باید با a.mu قفل‌شده صدا زده شود (از prepareStream). حل مسیر عمداً
// از resolveWorkspacePath استفاده نمی‌کند چون آن هم a.mu را می‌گیرد؛ مسیرها
// ثابت‌اند و حفاظت symlink با evalExisting انجام می‌شود.
func (a *App) loadRulesFromFileLocked() (string, string, bool) {
	if !a.settings.RulesEnabled {
		return "", "", false
	}
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	if ws == "" {
		return "", "", false
	}
	for _, rel := range ruleFileCandidates {
		full := filepath.Clean(filepath.Join(ws, rel))
		if !withinDir(full, filepath.Clean(ws)) {
			continue
		}
		realWs := evalExisting(filepath.Clean(ws))
		realFull := evalExisting(full)
		if realWs == "" || realFull == "" || !withinDir(realFull, realWs) {
			continue
		}
		// کش mtime: همان فایلِ قبلی بدون تغییر؟ بدون خواندن برگردد.
		fi, serr := os.Stat(full)
		if serr != nil {
			continue
		}
		if c := a.rulesCache; c.valid && c.ws == ws && c.rel == rel &&
			c.size == fi.Size() && c.mtimeNs == fi.ModTime().UnixNano() {
			return c.content, rel, true
		}
		// سقف خواندن — این مسیر زیر a.mu اجرا می‌شود؛ فایل قواعد عظیم
		// (حتی چند گیگابایت) نباید کامل در حافظه بار شود و همه‌ی قفل‌گیرها
		// را معطل کند. فقط به‌اندازه‌ی سقف (+۱ بایت برای تشخیص سرریز)
		// می‌خوانیم؛ رفتار نهایی (برش UTF-8 + نشانگر) مثل قبل است.
		var data []byte
		if fi.Size() > int64(maxRulesBytes)+1 {
			f, ferr := os.Open(full)
			if ferr != nil {
				continue
			}
			data, _ = io.ReadAll(io.LimitReader(f, int64(maxRulesBytes)+1))
			_ = f.Close()
		} else {
			var rerr error
			data, rerr = os.ReadFile(full)
			if rerr != nil {
				continue
			}
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			continue
		}
		if len(data) > maxRulesBytes {
			// برش در مرز کاراکتر UTF-8 — برش بایتی خام می‌توانست
			// آخرین حرف فارسی را نصف کند. (len(data) نه len(text):
			// اگر خواندن به سقف رسیده، حتی پس از حذف فاصله‌ها هم
			// دنباله‌ی فایل نیامده و نشانگر صادقانه می‌ماند.)
			text = cutUTF8(text, maxRulesBytes) + "\n…[قوانین بیشتر از حد مجاز بریده شد]"
		}
		a.rulesCache = rulesFileCache{
			ws: ws, rel: rel,
			size:    fi.Size(),
			mtimeNs: fi.ModTime().UnixNano(),
			content: text,
			valid:   true,
		}
		return text, rel, true
	}
	return "", "", false
}

// rulesPromptBlock بلوک قوانین پروژه برای الصاق به system prompt می‌سازد.
// خروجی خالی یعنی «چیزی برای تزریق نیست».
// باید با a.mu قفل‌شده صدا زده شود (از prepareStream).
func (a *App) rulesPromptBlockLocked() string {
	userRules := strings.TrimSpace(a.settings.ProjectRules)
	fileRules, fileRel, _ := a.loadRulesFromFileLocked()

	if userRules == "" && fileRules == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<project_rules priority=\"high\">\n")
	b.WriteString("Follow these project rules in every response and every tool call:\n\n")
	if userRules != "" {
		b.WriteString(userRules + "\n")
	}
	if fileRules != "" {
		if userRules != "" {
			b.WriteString("\n")
		}
		// مسیر نمایشی همیشه با اسلش استاندارد (ویندوز: ToSlash)
		fmt.Fprintf(&b, "(from %s)\n\n%s\n", filepath.ToSlash(fileRel), fileRules)
	}
	b.WriteString("</project_rules>")
	return b.String()
}

// RulesInfo وضعیت قوانین برای نمایش در تنظیمات.
type RulesInfo struct {
	Enabled     bool   `json:"enabled"`     // خواندن خودکار فایل قوانین فعال است؟
	UserRules   string `json:"userRules"`   // قوانین تعریف‌شده از تنظیمات
	FilePath    string `json:"filePath"`    // مسیر فایل قوانین پروژه (نسبی)
	FileExists  bool   `json:"fileExists"`  // فایل قوانین پیدا شد؟
	FileContent string `json:"fileContent"` // پیش‌نمایش محتوای فایل قوانین
}

// GetRulesInfo وضعیت قوانین پروژه برای تب تنظیمات.
func (a *App) GetRulesInfo() RulesInfo {
	defer a.recoverBound("GetRulesInfo")
	a.mu.Lock()
	fileRules, fileRel, found := a.loadRulesFromFileLocked()
	enabled := a.settings.RulesEnabled
	userRules := a.settings.ProjectRules
	a.mu.Unlock()
	if !found {
		fileRel = filepath.ToSlash(filepath.Join(".kodyar", "rules.md"))
	}
	return RulesInfo{
		Enabled:     enabled,
		UserRules:   userRules,
		FilePath:    filepath.ToSlash(fileRel),
		FileExists:  found,
		FileContent: fileRules,
	}
}

// SetProjectRules قوانین تعریف‌شده از تنظیمات را ذخیره می‌کند.
func (a *App) SetProjectRules(text string) error {
	defer a.recoverBound("SetProjectRules")
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(text) > maxRulesBytes {
		// برش در مرز کاراکتر UTF-8 (فارسی چندبایتی است)
		text = cutUTF8(text, maxRulesBytes)
	}
	a.settings.ProjectRules = text
	return a.saveSettings()
}

// SetRulesEnabled سوییچ خواندن خودکار فایل قوانین پروژه را ذخیره می‌کند.
func (a *App) SetRulesEnabled(on bool) error {
	defer a.recoverBound("SetRulesEnabled")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings.RulesEnabled = on
	return a.saveSettings()
}

// ---------------------------------------------------------------- @mention

// MentionItem یک نتیجه‌ی تکمیل خودکار @.
type MentionItem struct {
	Path  string `json:"path"`  // مسیر نسبی با اسلش
	Label string `json:"label"` // نام فایل یا پوشه
	IsDir bool   `json:"isDir"`
}

// MentionSearch جستجوی مسیر برای تکمیل خودکار @ در composer.
// تطبیق: زیررشته‌ی غیرحساس به case روی کل مسیر. پوشه‌های نادیده‌گرفته
// (node_modules،.git و…) هرگز برنمی‌گردند.
func (a *App) MentionSearch(query string) []MentionItem {
	defer a.recoverBound("MentionSearch")
	query = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), "@")))
	out := []MentionItem{}
	a.mu.Lock()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()
	if ws == "" {
		return out
	}

	type scored struct {
		item MentionItem
		rank int
	}
	var hits []scored
	limit := mentionSearchLimit

	_ = filepath.WalkDir(ws, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(hits) >= 60 {
			return filepath.SkipAll
		}
		rel := relDisplay(ws, path)
		if rel == "." {
			if d.IsDir() {
				return nil
			}
		}
		name := d.Name()
		if d.IsDir() {
			if path != ws && (searchSkipDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
		} else if strings.HasPrefix(name, ".") {
			return nil
		}
		if rel == "" {
			return nil
		}
		lower := strings.ToLower(rel)
		if query != "" && !strings.Contains(lower, query) {
			return nil
		}
		// رتبه: تطبیق در نام فایل بهتر از تطبیق در وسط مسیر است
		rank := 0
		if query != "" && strings.HasPrefix(strings.ToLower(name), query) {
			rank = 2
		} else if query != "" && strings.Contains(strings.ToLower(name), query) {
			rank = 1
		}
		hits = append(hits, scored{MentionItem{
			Path:  filepath.ToSlash(rel),
			Label: name,
			IsDir: d.IsDir(),
		}, rank})
		return nil
	})

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].rank != hits[j].rank {
			return hits[i].rank > hits[j].rank
		}
		if hits[i].item.IsDir != hits[j].item.IsDir {
			return !hits[i].item.IsDir // فایل‌ها اول
		}
		return len(hits[i].item.Path) < len(hits[j].item.Path)
	})
	for _, h := range hits {
		if len(out) >= limit {
			break
		}
		out = append(out, h.item)
	}
	return out
}

// mentionRegex توکن @path — حروف، رقم، نقطه، اسلش، خط تیره و زیرخط.
var mentionRegex = regexp.MustCompile(`@([A-Za-z0-9_./\\-]+)`)

// looksBinary بررسی سریع باینری بودن (بایت NUL در ابتدای فایل).
func looksBinary(data []byte) bool {
	n := len(data)
	if n > 512 {
		n = 512
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

// expandMentions توکن‌های @ در پیام کاربر را به بلوک کانتکست فایل تبدیل می‌کند.
// متن اصلی پیام دست‌نخورده برمی‌گردد؛ بلوک الصاقی جداگانه است تا UI تمیز بماند.
//
// @file → کل فایل (تا سقف maxMentionFileKB)
// @dir/ → انتخاب هوشمند فایل‌های مرتبط پوشه (سقف: ContextFiles از تنظیمات)
//
// توجه: نباید با a.mu قفل‌شده صدا زده شود (resolveWorkspacePath خودش a.mu
// می‌گیرد)؛ در SendMessage قبل از گرفتن قفل صدا زده می‌شود.
func (a *App) expandMentions(content string) string {
	a.mu.Lock()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	smartCap := a.settings.ContextFiles
	a.mu.Unlock()
	if ws == "" {
		return ""
	}
	if smartCap <= 0 {
		smartCap = 4
	}

	matches := mentionRegex.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return ""
	}

	// کلمات کلیدی پیام برای انتخاب هوشمند فایل‌های پوشه
	keywords := extractKeywords(content)

	var files []string    // مسیرهای نسبی نهایی برای الصاق
	var warnings []string // هشدارهای کاربر-محور (فارسی)
	seen := map[string]bool{}
	total := 0 // بایت الصاق‌شده تاکنون

	addFile := func(rel string) bool {
		if seen[rel] {
			return true
		}
		p, err := a.resolveWorkspacePath(rel)
		if err != nil {
			warnings = append(warnings, "«"+rel+"» خارج از پوشه‌ی کاری است و نادیده گرفته شد")
			return false
		}
		info, err := os.Stat(p)
		if err != nil {
			warnings = append(warnings, "فایل «"+rel+"» پیدا نشد")
			return false
		}
		if info.IsDir() {
			warnings = append(warnings, "«"+rel+"» یک پوشه است؛ برای ارجاع به پوشه اسلش آخر را اضافه کنید: @"+rel+"/")
			return false
		}
		if isBinaryAsset(rel) {
			warnings = append(warnings, "«"+rel+"» فایل باینری است و به کانتکست الصاق نشد")
			return false
		}
		if info.Size() > maxMentionFileKB*1024 {
			warnings = append(warnings, "«"+rel+"» بزرگ‌تر از حد مجاز ("+fmt.Sprint(maxMentionFileKB)+"KB) است و نادیده گرفته شد")
			return false
		}
		// فایل حساس (کلید/توکن/...)؟ خواندن محتوا با تأیید کاربر
		if reason := sensitiveFileReason(rel, a.sensitiveGlobs()); reason != "" {
			// context عمر برنامه (نه Background بی‌لغو): این انتظار تا ۳ دقیقه
			// طول می‌کشد و permAskMu را نگه می‌دارد؛ با بسته‌شدن برنامه باید
			// بلافاصله رها شود (همان الگوی RunCommand).
			if !a.requestSensitiveRead(a.appContext(), rel, reason) {
				warnings = append(warnings, "خواندن «"+rel+"» (فایل حساس) نیازمند تأیید بود و رد شد")
				return false
			}
		}
		seen[rel] = true
		files = append(files, rel)
		total += int(info.Size())
		return true
	}

	addDir := func(rel string) {
		p, err := a.resolveWorkspacePath(rel)
		if err != nil {
			warnings = append(warnings, "«"+rel+"» خارج از پوشه‌ی کاری است")
			return
		}
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			return // پوشه نیست → مسیر عادی امتحان می‌شود
		}
		picked := a.pickSmartFiles(p, keywords, smartCap)
		if len(picked) == 0 {
			warnings = append(warnings, "در پوشه‌ی «"+rel+"» فایل متنی مناسبی پیدا نشد")
			return
		}
		for _, f := range picked {
			if total >= maxMentionTotalKB*1024 {
				break
			}
			frel := relDisplay(ws, f)
			if !seen[frel] {
				seen[frel] = true
				files = append(files, frel)
				if fi, err := os.Stat(f); err == nil {
					total += int(fi.Size())
				}
			}
		}
	}

	for _, m := range matches {
		if total >= maxMentionTotalKB*1024 {
			warnings = append(warnings, "مجموع فایل‌های الصاق‌شده به سقف مجاز رسید؛ بقیه نادیده گرفته شد")
			break
		}
		tok := strings.TrimSuffix(filepath.ToSlash(m[1]), "/")
		if tok == "" || tok == "." {
			continue
		}
		// پایان با / → ارجاع به پوشه
		if strings.HasSuffix(m[1], "/") || strings.HasSuffix(m[1], "\\") {
			addDir(tok)
			continue
		}
		// اول به‌عنوان مسیر مستقیم؛ اگر پوشه بود، هشدار هوشمند
		p, err := a.resolveWorkspacePath(tok)
		if err == nil {
			if info, serr := os.Stat(p); serr == nil && info.IsDir() {
				addDir(tok)
				continue
			}
		}
		addFile(tok)
	}

	if len(files) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<attached_files>\n")
	b.WriteString("The user referenced these project files with @mentions. Their current contents:\n\n")
	for i, rel := range files {
		if i > 0 {
			b.WriteString("\n\n")
		}
		p, err := a.resolveWorkspacePath(rel)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if looksBinary(data) {
			warnings = append(warnings, "«"+rel+"» محتوای باینری دارد و رد شد")
			continue
		}
		text := string(data)
		if len(text) > maxMentionFileKB*1024 {
			// برش در مرز کاراکتر UTF-8
			text = cutUTF8(text, maxMentionFileKB*1024) + "\n…[فایل بریده شد]"
		}
		fmt.Fprintf(&b, "## %s\n```\n%s\n```", rel, strings.TrimRight(text, "\n"))
	}
	b.WriteString("\n</attached_files>")

	if len(warnings) > 0 {
		b.WriteString("\n\n(mention warnings: " + strings.Join(warnings, "؛ ") + ")")
	}
	return b.String()
}

// pickSmartFiles انتخاب هوشمند فایل‌های مرتبط یک پوشه.
//
// امتیازدهی: تطبیق نام فایل با کلمات کلیدی پیام (وزن بالا) + جدید بودن
// (mtime) + حجم معقول. فایل‌های باینری/بزرگ/مخفی و پوشه‌های build رد می‌شوند.
func (a *App) pickSmartFiles(dir string, keywords []string, cap int) []string {
	type cand struct {
		path  string
		score float64
	}
	var cands []cand
	now := time.Now().Unix()
	count := 0

	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if count >= dirScanFileCap {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if path != dir && (searchSkipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		count++
		name := strings.ToLower(d.Name())
		if strings.HasPrefix(name, ".") || isBinaryAsset(name) {
			return nil
		}
		fi, ferr := d.Info()
		if ferr != nil || fi.Size() == 0 || fi.Size() > maxMentionFileKB*1024 {
			return nil
		}
		var score float64
		// تطبیق با کلمات کلیدی پیام کاربر
		for _, kw := range keywords {
			if strings.Contains(name, kw) {
				score += 5
			}
		}
		// جدید بودن: فایل‌های ویرایش‌شده اخیر امتیاز بیشتری دارند (لگاریتمی)
		ageDays := (now - fi.ModTime().Unix()) / 86400
		if ageDays < 1 {
			score += 2
		} else if ageDays < 7 {
			score += 1
		}
		// فایل‌های کوچک‌تر کمی بهترند (کانتکست سبک‌تر)
		if fi.Size() < 8*1024 {
			score += 0.5
		}
		// فایل‌های ورودی/مستند معمولاً نقطه‌ی شروع بهتری‌اند
		lower := name
		switch {
		case strings.HasPrefix(lower, "readme"), strings.HasPrefix(lower, "main."),
			strings.HasPrefix(lower, "index."), strings.HasPrefix(lower, "app."):
			score += 1
		}
		cands = append(cands, cand{path, score})
		return nil
	})

	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return len(cands[i].path) < len(cands[j].path)
	})
	if len(cands) > cap {
		cands = cands[:cap]
	}
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.path)
	}
	return out
}

// extractKeywords کلمات معنادار پیام را برای انتخاب هوشمند فایل برمی‌دارد.
// کلمات با طول ۴+ (فارسی و انگلیسی) بدون کلماتوقف رایج.
var stopWords = map[string]bool{
	"this": true, "that": true, "with": true, "from": true, "please": true,
	"file": true, "files": true, "folder": true, "code": true, "about": true,
	"این": true, "آن": true, "برای": true, "تا": true, "که": true, "را": true,
	"فایل": true, "پوشه": true, "کد": true, "کن": true, "شود": true,
	"باشد": true, "می": true, "های": true, "یک": true, "توی": true,
}

func extractKeywords(content string) []string {
	words := regexp.MustCompile(`[A-Za-z\x{0600}-\x{06FF}]{4,}`).FindAllString(content, -1)
	out := make([]string, 0, len(words))
	seen := map[string]bool{}
	for _, w := range words {
		lw := strings.ToLower(w)
		if stopWords[lw] || seen[lw] {
			continue
		}
		seen[lw] = true
		out = append(out, lw)
	}
	return out
}
