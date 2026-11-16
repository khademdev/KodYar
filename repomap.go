// نقشهٔ مخزن (repo map) + ایندکس نمادها + ابزارهای find_symbol / find_references.
//
// چرا: ایجنت تا پیش از این فقط با search_files/glob «متنی» می‌گشت و هیچ
// تصویری از ساختار پروژه نداشت؛ هر نوبت کورکورانه جست‌وجو می‌کرد. این فایل
// یک نقشهٔ فشرده (بودجهٔ کران‌دار) از نمادهای هر فایل می‌سازد، آن را در
// system prompt تزریق می‌کند و دو ابزار ارزان برای «نماد کجاست» و
// «کجا استفاده شده» فراهم می‌کند.
//
// طراحی:
//   - پیمایش با همان قواعد ignore پروژه (gitignore.go) و همان فهرست پوشه‌های
//     ردشدهٔ جست‌وجو (searchSkipDirs)؛ فایل‌های بزرگ/باینری/حساس وارد نمی‌شوند
//     (فقط امتدادهای کد شناخته‌شده پارس می‌شوند، پس .env و کلیدها هرگز
//     خوانده نمی‌شوند).
//   - Go با go/parser (دقیق)؛ بقیهٔ زبان‌ها با regex لنگرشده در ابتدای خط —
//     «دقت مهم‌تر از پوشش» است، پس هر الگو فقط شکل‌های قطعی را می‌گیرد.
//   - رتبه‌بندی: تعداد ارجاع نام نماد در یک پاس خطی روی محتوای فایل‌های داخل
//     بودجه + تعداد نمادها منهای عمق مسیر. ترتیب کاملاً قطعی است.
//   - کش: TTL کوتاه (repoMapTTL) تا در یک نوبت (پرامپت اصلی + زیرایجنت)
//     یک‌بار ساخته شود; هر نوبت تازه از صفر ساخته می‌شود تا نقشه کهنه نماند.
package main

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// سقف‌های نقشه — کار برای هر نوبت باید در حد چند میلی‌ثانیه بماند.
const (
	maxRepoFiles         = 4000            // سقف فایل‌های کد واردشده به نقشه
	maxRepoFileBytes     = 256 * 1024      // فایل بزرگ‌تر پارس نمی‌شود
	maxRepoTokens        = 1500            // بودجهٔ پیش‌فرض تزریق در پرامپت
	maxDeclsPerFile      = 6               // حداکثر نماد نمایش‌داده‌شده از هر فایل
	maxDeclNameRunes     = 48              // برش نام نماد
	maxRepoScanBytes     = 6 * 1024 * 1024 // سقف بایت کل برای شمارش ارجاع‌ها
	repoMapCharsPerToken = 4               // تخمین محافظه‌کارانه (متن انگلیسی)
	repoMapLineChars     = 110             // سقف نویسهٔ هر خط نقشه
	repoMapTTL           = 3 * time.Second
	repoMapDefUTokens    = 700 // بودجهٔ کوچک‌تر برای زیرایجنت
)

// repoCutRunes رشته را به حداکثر max نویسه (rune) می‌برد — نام نماد ممکن
// است غیر-ASCII باشد و برش بایتی وسط کاراکتر بیفتد.
func repoCutRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// repoLangByExt فقط امتدادهای کدِ شناخته‌شده را می‌پذیرد (امنیت: .env و
// فایل‌های بدون امتداد هرگز خوانده نمی‌شوند).
var repoLangByExt = map[string]string{
	".go": "go",
	".js": "js", ".mjs": "js", ".cjs": "js", ".jsx": "js",
	".ts": "ts", ".tsx": "ts",
	".py": "py", ".pyi": "py",
	".rs":   "rs",
	".java": "java", ".kt": "kt", ".kts": "kt",
	".swift": "swift",
	".c":     "c", ".h": "c", ".cc": "cpp", ".cpp": "cpp", ".hpp": "cpp",
	".cs": "cs", ".php": "php", ".rb": "rb", ".lua": "lua", ".dart": "dart",
}

// repoDeclPattern الگوی «ابتدای خط» برای زبان‌های غیر Go، همراه با نوع
// نماد. عمداً محدود و محافظه‌کارانه‌اند تا نقشه پر از نویز نشود.
type repoDeclPattern struct {
	re   *regexp.Regexp
	kind string // func | type
}

var repoDeclPatterns = []repoDeclPattern{
	{regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`), "func"},
	{regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`), "type"},
	{regexp.MustCompile(`^(?:export\s+)?(?:interface|type|enum)\s+([A-Za-z_$][\w$]*)`), "type"},
	{regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*=>`), "func"},
	{regexp.MustCompile(`^(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)`), "func"},           // Rust
	{regexp.MustCompile(`^(?:pub\s+)?(?:struct|enum|trait|mod)\s+([A-Za-z_]\w*)`), "type"}, // Rust
	{regexp.MustCompile(`^(?:async\s+)?def\s+([A-Za-z_]\w*)`), "func"},                     // Python
	{regexp.MustCompile(`^class\s+([A-Za-z_]\w*)`), "type"},                                // Python
	{regexp.MustCompile(`^(?:public|private|protected|internal|static|final|abstract|sealed|data|open|partial)[\w\s]*?\b(?:class|interface|enum|struct|record)\s+([A-Za-z_]\w*)`), "type"},
	{regexp.MustCompile(`^func\s+([A-Za-z_]\w*)`), "func"}, // زبان‌های دیگر با func
}

// repoDecl یک نماد در نقشه (Name برای متدها به شکل Type.Method).
type repoDecl struct {
	Name string
	Kind string // func | method | type
}

// repoFile یک فایل در نقشه.
type repoFile struct {
	Path  string
	Lang  string
	Decls []repoDecl
	Refs  int
	Size  int64
}

// repoMap نتیجهٔ ساخت نقشه.
type repoMap struct {
	Files    []repoFile
	MaxChars int
}

// repoMapCache کش کوتاه‌عمرِ نقشه (TTL).
type repoMapCache struct {
	mu        sync.Mutex
	workspace string
	tokens    int
	m         repoMap
	builtAt   time.Time
	valid     bool
}

// repoDecls استخراج نمادهای یک فایل بر اساس زبان.
func repoDecls(lang string, src []byte) []repoDecl {
	if lang == "go" {
		return repoGoDecls(src)
	}
	return repoRegexDecls(src)
}

// repoGoDecls نمادهای Go: توابع، متدها (Type.Method) و تایپ‌های سطح‌بالا.
// const/var عمداً نیستند تا نقشه شلوغ نشود.
func repoGoDecls(src []byte) []repoDecl {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil || f == nil {
		return nil
	}
	// اول توابع/متدها، بعد تایپ‌ها: در فایل‌های پرتایپ (مثل همین پروژه) اگر
	// ترتیب فایل را نگه می‌داشتیم، سقف ۶ نماد فقط به تایپ‌ها می‌رسید و توابع
	// (که برای ناوبری مهم‌ترند) هرگز در نقشه نمی‌آمدند.
	var funcs, types []repoDecl
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name == nil || d.Name.Name == "" {
				continue
			}
			name := d.Name.Name
			kind := "func"
			if d.Recv != nil && len(d.Recv.List) > 0 {
				if recv := receiverTypeName(d.Recv.List[0].Type); recv != "" {
					name = recv + "." + name
					kind = "method"
				}
			}
			if len(funcs) < maxDeclsPerFile {
				funcs = append(funcs, repoDecl{Name: repoCutRunes(name, maxDeclNameRunes), Kind: kind})
			}
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name != nil && len(types) < maxDeclsPerFile {
					types = append(types, repoDecl{Name: repoCutRunes(ts.Name.Name, maxDeclNameRunes), Kind: "type"})
				}
			}
		}
	}
	out := funcs
	for _, d := range types {
		if len(out) >= maxDeclsPerFile {
			break
		}
		out = append(out, d)
	}
	return out
}

// receiverTypeName نام تایپ گیرنده را برمی‌گرداند (T یا *T → T).
func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.IndexExpr: // جنریک: T[P]
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	}
	return ""
}

// repoRegexDecls الگوهای لنگرشده را روی خطوط اعمال می‌کند (سقف نماد در فایل).
func repoRegexDecls(src []byte) []repoDecl {
	var out []repoDecl
	seen := map[string]bool{}
	lines := bytes.Split(src, []byte("\n"))
	for _, raw := range lines {
		if len(out) >= maxDeclsPerFile {
			break
		}
		line := strings.TrimSpace(string(raw))
		if line == "" || len(line) > 200 {
			continue
		}
		for _, p := range repoDeclPatterns {
			m := p.re.FindStringSubmatch(line)
			if len(m) < 2 || m[1] == "" || seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			out = append(out, repoDecl{Name: repoCutRunes(m[1], maxDeclNameRunes), Kind: p.kind})
			break
		}
	}
	return out
}

// countIdentifierRefs شمارش خطیِ تکرار نام نمادها در محتوا. هر توکن شناسه
// (حداکثر ۶۴ نویسه) فقط اگر در مجموعهٔ نمادها باشد شمرده می‌شود.
func countIdentifierRefs(contents [][]byte, symbols map[string]bool) map[string]int {
	freq := make(map[string]int, len(symbols))
	isIdent := func(b byte) bool {
		return b == '_' || b == '$' ||
			(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
	}
	for _, src := range contents {
		i := 0
		for i < len(src) {
			if !isIdent(src[i]) {
				i++
				continue
			}
			start := i
			for i < len(src) && isIdent(src[i]) && i-start < 64 {
				i++
			}
			w := string(src[start:i])
			// نام‌های ۱–۲ نویسه‌ای (d، n، i) در کد همه‌جا هستند و شمارششان
			// رتبه‌بندی را خراب می‌کند — فقط شناسه‌های معنادار شمرده شوند.
			if len(w) >= 3 && symbols[w] {
				freq[w]++
			}
		}
	}
	return freq
}

// repoScore امتیاز رتبه‌بندی یک فایل: ارجاع بیشتر + نماد بیشتر − عمق مسیر.
// فایل‌های تست جریمه دارند (نماد زیاد ولی برای ناوبری کم‌ارزش‌اند) تا
// نقشه با `*_test.go` پر نشود.
func repoScore(f repoFile) int {
	return f.Refs*2 + len(f.Decls)*2 - strings.Count(f.Path, "/")
}

// repoIsTestFile آیا این فایل تست است؟ (در رتبه‌بندی و ارجاع‌شماری
// کم‌ارزش‌تر است.)
func repoIsTestFile(path string) bool {
	return strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".test.ts") ||
		strings.HasSuffix(path, ".spec.ts") || strings.HasSuffix(path, ".test.js") ||
		strings.HasSuffix(path, "_test.py") || strings.HasSuffix(path, "_test.rs") ||
		strings.HasPrefix(path, "testdata/")
}

// buildRepoMap پیمایش و ساخت نقشه؛ برگشت بدون خطا (هر خطای فایل نادیده).
func buildRepoMap(ws string, maxTokens int) repoMap {
	ws = strings.TrimSpace(ws)
	maxChars := maxTokens * repoMapCharsPerToken
	if maxTokens <= 0 {
		maxChars = maxRepoTokens * repoMapCharsPerToken
	}
	out := repoMap{MaxChars: maxChars}
	if ws == "" {
		return out
	}

	ig := newIgnoreStack()
	ig.push(ws, "")

	var files []repoFile
	var contents [][]byte
	scanned := 0

	_ = filepath.WalkDir(ws, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != ws {
				if searchSkipDirs[d.Name()] {
					return filepath.SkipDir
				}
				rel := relDisplay(ws, path)
				if ig.ignored(rel, true) {
					return filepath.SkipDir
				}
				ig.push(path, rel)
			}
			return nil
		}
		if len(files) >= maxRepoFiles {
			return filepath.SkipAll
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel := relDisplay(ws, path)
		if ig.ignored(rel, false) {
			return nil
		}
		lang := repoLangByExt[strings.ToLower(filepath.Ext(rel))]
		if lang == "" {
			return nil
		}
		fi, ferr := d.Info()
		if ferr != nil || fi.Size() > maxRepoFileBytes {
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		head := src
		if len(head) > 8192 {
			head = head[:8192]
		}
		if bytes.IndexByte(head, 0) >= 0 {
			return nil // باینری
		}
		decls := repoDecls(lang, src)
		if len(decls) == 0 {
			return nil
		}
		files = append(files, repoFile{Path: rel, Lang: lang, Decls: decls, Size: fi.Size()})
		if scanned+len(src) <= maxRepoScanBytes {
			contents = append(contents, src)
			scanned += len(src)
		}
		return nil
	})

	if len(files) == 0 {
		return out
	}

	symbols := make(map[string]bool, len(files)*3)
	for _, f := range files {
		for _, d := range f.Decls {
			symbols[d.Name] = true
			// برای متدها نام کوتاه هم شمرده شود (فراخوانی u.Rename).
			if i := strings.Index(d.Name, "."); i > 0 && i+1 < len(d.Name) {
				symbols[d.Name[i+1:]] = true
			}
		}
	}
	freq := countIdentifierRefs(contents, symbols)
	for i := range files {
		sum := 0
		for _, d := range files[i].Decls {
			sum += freq[d.Name]
			if j := strings.Index(d.Name, "."); j > 0 && j+1 < len(d.Name) {
				sum += freq[d.Name[j+1:]]
			}
		}
		files[i].Refs = sum
	}

	sort.SliceStable(files, func(i, j int) bool {
		// فایل‌های تست همیشه بعد از فایل‌های اصلی می‌آیند (نام تست‌ها
		// ارجاع زیاد ولی ارزش ناوبری کم دارند و نقشه را شلوغ می‌کنند).
		ti, tj := repoIsTestFile(files[i].Path), repoIsTestFile(files[j].Path)
		if ti != tj {
			return !ti
		}
		si, sj := repoScore(files[i]), repoScore(files[j])
		if si != sj {
			return si > sj
		}
		return files[i].Path < files[j].Path
	})
	out.Files = files
	return out
}

// repoMapFor ساخت/کش نقشه با TTL کوتاه — یک‌بار در هر نوبت.
func (a *App) repoMapFor(ws string) repoMap {
	ws = strings.TrimSpace(ws)
	if ws == "" {
		return repoMap{MaxChars: maxRepoTokens * repoMapCharsPerToken}
	}
	c := &a.repoMapCache
	c.mu.Lock()
	if c.valid && c.workspace == ws && c.tokens == maxRepoTokens && time.Since(c.builtAt) < repoMapTTL {
		m := c.m
		c.mu.Unlock()
		return m
	}
	c.mu.Unlock()

	m := buildRepoMap(ws, maxRepoTokens)

	c.mu.Lock()
	c.workspace, c.tokens, c.m, c.builtAt, c.valid = ws, maxRepoTokens, m, time.Now(), true
	c.mu.Unlock()
	return m
}

// promptBlock بلوک انگلیسی تزریق‌شونده در system prompt. ("": چیزی نیست)
func (m repoMap) promptBlock() string {
	if len(m.Files) == 0 {
		return ""
	}
	maxChars := m.MaxChars
	if maxChars <= 0 {
		maxChars = maxRepoTokens * repoMapCharsPerToken
	}
	var b strings.Builder
	b.WriteString("Repository map (symbols parsed from files — a hint, not ground truth; read a file before editing it):\n")
	used := b.Len()
	shown := 0
	for _, f := range m.Files {
		line := "- " + f.Path
		if len(f.Decls) > 0 {
			names := make([]string, 0, len(f.Decls))
			for _, d := range f.Decls {
				names = append(names, d.Name)
			}
			line += " — " + strings.Join(names, ", ")
		}
		line = repoCutRunes(line, repoMapLineChars)
		if used+len(line)+1 > maxChars {
			break
		}
		b.WriteString(line + "\n")
		used += len(line) + 1
		shown++
	}
	if rest := len(m.Files) - shown; rest > 0 {
		b.WriteString(fmt.Sprintf("- … (+%d more code files)\n", rest))
	}
	return b.String()
}

// repoMapPromptLine بلوک آماده برای prepareStream/subagent ("": چیزی نیست).
// فراخواننده باید آن را «بیرون از a.mu» صدا بزند (I/O دارد).
func (a *App) repoMapPromptLine(ws string) string {
	return a.repoMapFor(ws).promptBlockWithTokens(maxRepoTokens)
}

// repoMapPromptLineBudget مثل بالا با بودجهٔ کوچک‌تر (زیرایجنت).
func (a *App) repoMapPromptLineBudget(ws string, tokens int) string {
	if tokens <= 0 {
		tokens = repoMapDefUTokens
	}
	m := buildRepoMap(ws, tokens)
	return m.promptBlockWithTokens(tokens)
}

// promptBlockWithTokens مثل promptBlock با بودجهٔ صریح (کش مشترک را
// دستکاری نمی‌کند تا نقشهٔ اصلی با بودجهٔ کوچک خراب نشود).
func (m repoMap) promptBlockWithTokens(tokens int) string {
	if tokens <= 0 {
		tokens = maxRepoTokens
	}
	cp := m
	cp.MaxChars = tokens * repoMapCharsPerToken
	return cp.promptBlock()
}

// ---------------------------------------------------------------- ابزارها

// repoKindOf نوع نماد را از شکل نام برمی‌گرداند (فالبک برای داده‌های قدیمی).
func repoKindOf(name string) string {
	if strings.Contains(name, ".") {
		return "method"
	}
	return "func"
}

// repoMapSubagentBlock بلوک نقشه برای پرامپت زیرایجنت (بودجهٔ کوچک‌تر و
// بدون دست‌زدن به کش نقشهٔ اصلی).
func (a *App) repoMapSubagentBlock(ws string) string {
	if strings.TrimSpace(ws) == "" {
		return ""
	}
	block := a.repoMapPromptLineBudget(ws, repoMapDefUTokens)
	if block == "" {
		return ""
	}
	return "\n" + block
}

// toolFindSymbol جست‌وجوی نماد در ایندکس نقشه (نام کوتاه، بودجهٔ کم).
func (a *App) toolFindSymbol(query, kind string, limit int) (string, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return `ERROR: invalid arguments for find_symbol: field "query" must not be empty.`, false
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	a.mu.Lock()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()
	if ws == "" {
		return "ERROR: no workspace folder is set; ask the user to choose one in settings first.", false
	}

	m := a.repoMapFor(ws)
	lower := strings.ToLower(query)

	type hit struct {
		path  string
		decl  string
		kind  string
		refs  int
		score int
	}
	var hits []hit
	for _, f := range m.Files {
		for _, d := range f.Decls {
			k := d.Kind
			if k == "" {
				k = repoKindOf(d.Name)
			}
			if kind != "" && kind != k {
				continue
			}
			// هم نام کامل (Type.Method) و هم نام کوتاه (Method) تطبیق داده می‌شود.
			short := d.Name
			if i := strings.Index(short, "."); i > 0 && i+1 < len(short) {
				short = short[i+1:]
			}
			best := 0
			for _, cand := range []string{strings.ToLower(d.Name), strings.ToLower(short)} {
				switch {
				case cand == lower:
					if 3 > best {
						best = 3
					}
				case strings.HasPrefix(cand, lower):
					if 2 > best {
						best = 2
					}
				case strings.Contains(cand, lower):
					if 1 > best {
						best = 1
					}
				}
			}
			if best == 0 {
				continue
			}
			hits = append(hits, hit{path: f.Path, decl: d.Name, kind: k, refs: f.Refs, score: best})
		}
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No symbol matched %q in the repository map (%d code files indexed). Try search_files or glob for its text.", query, len(m.Files)), true
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if hits[i].refs != hits[j].refs {
			return hits[i].refs > hits[j].refs
		}
		if hits[i].path != hits[j].path {
			return hits[i].path < hits[j].path
		}
		return hits[i].decl < hits[j].decl
	})
	var b strings.Builder
	fmt.Fprintf(&b, "Symbols matching %q (%d indexed code files; %d hit(s)):\n", query, len(m.Files), len(hits))
	for i, h := range hits {
		if i >= limit {
			fmt.Fprintf(&b, "… (+%d more)\n", len(hits)-limit)
			break
		}
		fmt.Fprintf(&b, "- %s: %s %s\n", h.path, h.kind, h.decl)
	}
	b.WriteString("Use find_references to see where a symbol is used, or read_file for the definition.")
	return b.String(), true
}

// toolFindReferences جست‌وجوی مرز-کلمه‌ای یک شناسه در فایل‌های پروژه —
// برخلاف search_files، تطبیق به «شناسهٔ کامل» محدود است تا foo در fooBar
// پیدا نشود.
func (a *App) toolFindReferences(ctx context.Context, symbol, include string, limit int) (string, bool) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return `ERROR: invalid arguments for find_references: field "symbol" must not be empty.`, false
	}
	if limit <= 0 {
		limit = 40
	}
	if limit > 100 {
		limit = 100
	}
	re, cerr := regexp.Compile(`\b` + regexp.QuoteMeta(symbol) + `\b`)
	if cerr != nil {
		return "ERROR: invalid symbol: " + cerr.Error(), false
	}

	a.mu.Lock()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	sensitive := a.sensitiveGlobsLocked()
	a.mu.Unlock()
	if ws == "" {
		return "ERROR: no workspace folder is set; ask the user to choose one in settings first.", false
	}
	root, err := a.resolveWorkspacePath(".")
	if err != nil {
		return "ERROR: " + err.Error(), false
	}

	include = strings.TrimSpace(include)
	ig := newIgnoreStack()
	ig.push(root, "")

	var b strings.Builder
	hits := 0
	filesHit := 0
	scanned := 0
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if path != root {
				if searchSkipDirs[d.Name()] {
					return filepath.SkipDir
				}
				rel := relDisplay(ws, path)
				if ig.ignored(rel, true) {
					return filepath.SkipDir
				}
				ig.push(path, rel)
			}
			return nil
		}
		if hits >= limit {
			return filepath.SkipAll
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel := relDisplay(ws, path)
		if ig.ignored(rel, false) {
			return nil
		}
		if sensitiveFileReason(path, sensitive) != "" {
			return nil
		}
		fi, ferr := d.Info()
		if ferr != nil || fi.Size() > maxSearchFileLen {
			return nil
		}
		scanned++
		if include != "" && !matchSearchInclude(include, rel) {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		head := data
		if len(head) > 8192 {
			head = head[:8192]
		}
		if bytes.IndexByte(head, 0) >= 0 {
			return nil
		}
		fileHits := 0
		for i, line := range strings.Split(string(data), "\n") {
			if line == "" || !re.MatchString(line) {
				continue
			}
			if hits >= limit {
				break
			}
			if fileHits == 0 {
				filesHit++
			}
			fileHits++
			hits++
			fmt.Fprintf(&b, "- %s:%d: %s\n", rel, i+1, repoCutRunes(strings.TrimSpace(line), 160))
		}
		return nil
	})
	if walkErr != nil && ctx.Err() != nil {
		return "ERROR: cancelled while searching references.", false
	}

	if hits == 0 {
		return fmt.Sprintf("No references to %q were found (word-boundary match, %d file(s) scanned).", symbol, scanned), true
	}
	head := fmt.Sprintf("References to %q (%d hit(s) in %d file(s); %d file(s) scanned):\n", symbol, hits, filesHit, scanned)
	return head + b.String(), true
}
