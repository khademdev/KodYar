// تطبیق‌گر.gitignore — سبک و درست برای search_files و list_dir.
//
// پشتیبانی: کامنت‌ها (#)، نفی (!)، الگوی فقط-پوشه (اسلش انتهایی)، الگوی
// لنگرشده (اسلش ابتدایی یا اسلش میانی)، glob تک‌جزئی (* و ? و [..]) و
// ** بین اجزا. فایل‌های.gitignore تودرتو (هر پوشه) پشتیبانی می‌شوند؛
// قاعده‌های پوشه‌ی عمیق‌تر دیرتر push می‌شوند و در ارزیابیِ «آخرین تطبیق
// برنده است» اولویت دارند — همان رفتار git برای حالت‌های رایج.
//
// محدودیت‌های آگاهانه (در گزارش فاز ذکر شده):
// -.git/info/exclude و excludeهای سراسری خوانده نمی‌شوند.
// - مانند خود git، اگر «پوشه‌ی» والد ignore شده باشد، نفیِ فرزند ممکن
// نیست (چون کل زیردرخت SkipDir می‌شود).
package main

import (
	"os"
	"path/filepath"
	"strings"
)

// محدودیت‌های اعمال‌شده روی فایل‌های.gitignore (جلوگیری از رشد بی‌رویه)
const (
	gitignoreMaxFileBytes  = 64 * 1024 // سقف هر فایل.gitignore
	gitignoreMaxRules      = 5000      // سقف قاعده‌های هر فایل.gitignore
	gitignoreMaxStackRules = 20000     // سقف کل قاعده‌های پشته در یک پیمایش —
	// درخت‌های پاتولوژیک (هزاران.gitignore
	// تودرتو) نباید حافظه و هزینه‌ی O(n)
	// هر بررسی ignored() را بی‌کران کنند
)

// ignoreRule یک قاعده‌ی تجزیه‌شده از یک فایل.gitignore.
type ignoreRule struct {
	negated  bool     // با! شروع می‌شود
	dirOnly  bool     // اسلش انتهایی — فقط پوشه‌ها
	anchored bool     // اسلش میانی/ابتدایی — نسبت به baseDir لنگر شده
	baseDir  string   // مسیر پوشه‌ی صاحبِ فایل نسبت به ریشه (با اسلش؛ "" = ریشه)
	pattern  string   // متن الگو (پس از حذف! و اسلش‌های ابتدا/انتها)
	segments []string // اجزای الگو (برای حالت لنگرشده)
}

// parseIgnoreRule یک خط.gitignore را تجزیه می‌کند.
// ok=false یعنی خط خالی/کامنت است.
func parseIgnoreRule(line, baseDir string) (*ignoreRule, bool) {
	line = strings.TrimSpace(line)
	// اسپیس انتهایی escapeنشده مثل خود git حذف می‌شود (ساده‌سازی امن)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil, false
	}
	r := &ignoreRule{baseDir: baseDir}
	if strings.HasPrefix(line, "!") {
		r.negated = true
		line = strings.TrimSpace(strings.TrimPrefix(line, "!"))
		if line == "" {
			return nil, false
		}
	}
	if strings.HasSuffix(line, "/") {
		r.dirOnly = true
		line = strings.TrimSuffix(line, "/")
		if line == "" {
			return nil, false
		}
	}
	// اسلش ابتدایی = لنگر به baseDir
	anchored := false
	if strings.HasPrefix(line, "/") {
		anchored = true
		line = strings.TrimPrefix(line, "/")
	}
	// اسلش میانی هم لنگر می‌کند
	if strings.Contains(line, "/") {
		anchored = true
	}
	r.anchored = anchored
	r.pattern = line
	r.segments = strings.Split(line, "/")
	return r, true
}

// loadGitIgnoreFile قاعده‌های یک فایل.gitignore را می‌خواند (با سقف حجم).
// dir = مسیر مطلق پوشه‌ی صاحب فایل؛ baseDir = همان نسبت به ریشه‌ی workspace.
func loadGitIgnoreFile(dir, baseDir string) []*ignoreRule {
	raw, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil || len(raw) == 0 || len(raw) > gitignoreMaxFileBytes {
		return nil
	}
	var rules []*ignoreRule
	for _, line := range strings.Split(string(raw), "\n") {
		if r, ok := parseIgnoreRule(line, baseDir); ok {
			rules = append(rules, r)
			if len(rules) >= gitignoreMaxRules {
				break
			}
		}
	}
	return rules
}

// ignoreStack مجموعه‌ی قاعده‌های.gitignore جمع‌آوری‌شده حین پیمایش.
// قاعده‌ها به ترتیب ریشه→عمق push می‌شوند؛ «آخرین تطبیق برنده است».
type ignoreStack struct {
	rules []*ignoreRule
}

// newIgnoreStack یک پشته‌ی خالی می‌سازد.
func newIgnoreStack() *ignoreStack { return &ignoreStack{} }

// push قاعده‌های یک پوشه را اضافه می‌کند (اگر فایل.gitignore داشته باشد).
// پس از سقف کل پشته، فایل‌های بعدی نادیده گرفته می‌شوند — پیمایش ادامه
// می‌یابد و عمق‌های پایین‌تر فقط با قاعده‌های موجود فیلتر می‌شوند.
func (s *ignoreStack) push(absDir, relDir string) {
	if len(s.rules) >= gitignoreMaxStackRules {
		return
	}
	if rs := loadGitIgnoreFile(absDir, relDir); len(rs) > 0 {
		s.rules = append(s.rules, rs...)
		if over := len(s.rules) - gitignoreMaxStackRules; over > 0 {
			s.rules = s.rules[:len(s.rules)-over]
		}
	}
}

// ignored بررسی می‌کند که مسیر rel (نسبت به ریشه، با اسلش) ignore است یا نه.
// isDir برای قاعده‌های فقط-پوشه لازم است.
func (s *ignoreStack) ignored(rel string, isDir bool) bool {
	if len(s.rules) == 0 || rel == "" || rel == "." {
		return false
	}
	ignored := false
	for _, r := range s.rules {
		if r.matches(rel, isDir) {
			ignored = !r.negated
		}
	}
	return ignored
}

// matches بررسی تطبیق یک قاعده با مسیر نسبی rel.
// قاعده‌ی فقط-پوشه (dirOnly) علاوه بر خودِ پوشه، هر مسیری را که زیر یک
// پوشه‌ی منطبق باشد هم می‌گیرد (رفتار git: build/ کل زیردرخت را می‌گیرد).
func (r *ignoreRule) matches(rel string, isDir bool) bool {
	// مسیر باید زیر baseDir باشد
	sub := rel
	if r.baseDir != "" {
		if !strings.HasPrefix(rel, r.baseDir+"/") {
			return false
		}
		sub = rel[len(r.baseDir)+1:]
	}
	if sub == "" {
		return false
	}
	segs := strings.Split(sub, "/")
	if r.dirOnly {
		// تطبیق با خود مسیر (اگر پوشه است) یا با هر والدِ منطبق
		for i := 1; i <= len(segs); i++ {
			cand := segs[:i]
			last := i == len(segs)
			if last && !isDir {
				continue // آخرین جزء فایل است — قاعده‌ی فقط-پوشه فقط به والد نگاه می‌کند
			}
			if r.anchored {
				if matchSegs(r.segments, cand) {
					return true
				}
			} else if globMatch(r.pattern, segs[i-1]) {
				return true
			}
		}
		return false
	}
	if r.anchored {
		return matchSegs(r.segments, segs)
	}
	// بدون اسلش: تطبیق با basename در هر عمقی زیر baseDir
	return matchGlobSeg(r.pattern, segs[len(segs)-1])
}

// matchGlobSeg تطبیق glob تک‌جزئی: * (هرچیز)، ? (یک کاراکتر)، [..] (کلاس).
func matchGlobSeg(pattern, name string) bool {
	// پیاده‌سازی بازگشتی ساده و بدون regex (پرهیز از تخصیص در مسیر گرم)
	return globMatch(pattern, name)
}

// globMatch تطبیق الگوی تک‌جزئی (بدون /) — * و ? و [a-z] / [!a-z].
func globMatch(pattern, s string) bool {
	px, sx := 0, 0
	starPx, starSx := -1, -1
	nextPx := 0
	for sx < len(s) {
		if px < len(pattern) {
			switch c := pattern[px]; c {
			case '*':
				starPx = px
				starSx = sx
				nextPx = px + 1
				px++
				continue
			case '?':
				px++
				sx++
				continue
			case '[':
				// کلاس کاراکتر — [abc]، [a-z]، [!abc]
				end, ok, match := matchCharClass(pattern, px, s[sx])
				if ok {
					if match {
						px = end
						sx++
						continue
					}
					// عدم تطبیق → backtrack به *
				}
			default:
				if c == s[sx] {
					px++
					sx++
					continue
				}
			}
		}
		if starPx >= 0 {
			starSx++
			sx = starSx
			px = nextPx
			continue
		}
		return false
	}
	for px < len(pattern) && pattern[px] == '*' {
		px++
	}
	return px == len(pattern)
}

// matchCharClass کلاس [..] را از px (موقعیت '[') با کاراکتر ch تطبیق می‌دهد.
// خروجی: ایندکس بعد از ']'، آیا کلاس معتبر بود، آیا تطبیق داشت.
func matchCharClass(pattern string, px int, ch byte) (int, bool, bool) {
	i := px + 1
	negate := false
	if i < len(pattern) && (pattern[i] == '!' || pattern[i] == '^') {
		negate = true
		i++
	}
	matched := false
	first := true
	for i < len(pattern) {
		c := pattern[i]
		if c == ']' && !first {
			// پایان کلاس
			if negate {
				return i + 1, true, !matched
			}
			return i + 1, true, matched
		}
		first = false
		// بازه‌ی a-z
		if i+2 < len(pattern) && pattern[i+1] == '-' && pattern[i+2] != ']' {
			if ch >= c && ch <= pattern[i+2] {
				matched = true
			}
			i += 3
			continue
		}
		if c == ch {
			matched = true
		}
		i++
	}
	return 0, false, false // کلاس بسته نشد — نامعتبر
}

// matchSegs تطبیق زنجیره‌ی اجزا با پشتیبانی ** (هر عمق).
// «**» تنهای انتهایی (مثل tmp/**) طبق git فقط «داخلِ» پوشه را می‌گیرد —
// حداقل یک جزء لازم است؛ ** ابتدایی/میانی صفر جزء هم می‌پذیرد.
func matchSegs(pat, path []string) bool {
	if len(pat) == 0 {
		return len(path) == 0
	}
	if pat[0] == "**" {
		if len(pat) == 1 {
			return len(path) >= 1
		}
		for i := 0; i <= len(path); i++ {
			if matchSegs(pat[1:], path[i:]) {
				return true
			}
		}
		return false
	}
	if len(path) == 0 {
		return false
	}
	if !globMatch(pat[0], path[0]) {
		return false
	}
	return matchSegs(pat[1:], path[1:])
}
