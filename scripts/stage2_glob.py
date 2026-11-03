#!/usr/bin/env python3
"""Stage 2 patch B: glob ordering by mtime (newest first), then path.

- maxGlobResults 300 -> 500 (returned after sorting, per spec)
- new hard candidate bound maxGlobCandidates = 20000 during the walk
- stat via DirEntry.Info(); sort (mtime desc, path asc); slice to 500
- filtering (gitignore/skipDirs/symlinks) and ctx cancellation unchanged
"""
path = "agent.go"
src = open(path).read()

# ---- 1) constants: result cap 500 + new candidate bound
old = '''	maxGlobResults        = 300              // سقف نتایج ابزار glob (گام ۵؛ هم‌راستا با سقف مطلق search_files)'''
new = '''	maxGlobResults        = 500              // سقف نتایج برگردانده‌شده‌ی glob (پس از مرتب‌سازی بر اساس mtime)
	maxGlobCandidates     = 20000            // سقف کاندیدهای جمع‌آوری‌شده حین پیمایش glob (قبل از مرتب‌سازی)'''
assert src.count(old) == 1, "glob const anchor"
src = src.replace(old, new)

# ---- 2) toolGlob: collect candidates with mtime, sort newest-first
start = src.index("func (a *App) toolGlob(")
# the function ends right before toolRunCommand's doc comment
end = src.index("// toolRunCommand اجرای دستور")
old_fn = src[start:end]

new_fn = '''// globCandidate یک فایل منطبق با الگو + زمان تغییر (برای مرتب‌سازی).
type globCandidate struct {
	rel   string
	mtime int64 // UnixNano
}

func (a *App) toolGlob(ctx context.Context, pattern, rel string) (string, bool) {
	pattern = strings.TrimSpace(pattern)
	if pattern != "" {
		pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "./")
	}
	if pattern == "" || pattern == "." || pattern == "/" {
		return "ERROR: empty glob pattern.", false
	}
	root, err := a.resolveWorkspacePath(rel)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	info, err := os.Stat(root)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	if !info.IsDir() {
		return "ERROR: glob path must be a directory.", false
	}

	a.mu.Lock()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()

	anchored := strings.Contains(pattern, "/")
	var patSegs []string
	if anchored {
		patSegs = strings.Split(pattern, "/")
	}

	// .gitignore از ریشه‌ی workspace تا ریشه‌ی جستجو (مثل list_dir)
	ig := newIgnoreStack()
	ig.push(ws, "")
	relBase := relDisplay(ws, root)
	if relBase != "." && relBase != "" {
		cur, baseAcc := ws, ""
		for _, seg := range strings.Split(relBase, "/") {
			cur = filepath.Join(cur, seg)
			if baseAcc == "" {
				baseAcc = seg
			} else {
				baseAcc += "/" + seg
			}
			ig.push(cur, baseAcc)
		}
	}

	// کاندیدها با mtime جمع می‌شوند (تا سقف maxGlobCandidates) و بعد از
	// پیمایش مرتب می‌شوند: newest-first و بعد مسیر. برخلاف قبل، پیمایش
	// با رسیدن به تعداد نتیجه متوقف نمی‌شود چون ترتیب نهایی فقط بعد از
	// دیدن همه‌ی فایل‌ها معلوم است.
	candidates := make([]globCandidate, 0, 256)
	candidatesCapped := false
	filesSeen := 0
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// بررسی لغو پیش از هر پوشه — درخت بزرگ با Stop فوراً متوقف می‌شود.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if path != root {
				if searchSkipDirs[d.Name()] {
					return filepath.SkipDir
				}
				relWs := relDisplay(ws, path)
				if ig.ignored(relWs, true) {
					return filepath.SkipDir
				}
				ig.push(path, relWs)
			}
			return nil
		}
		if len(candidates) >= maxGlobCandidates {
			candidatesCapped = true
			return filepath.SkipAll
		}
		// بررسی لغو هر ۲۵۶ فایل (ارزان؛ پوشه‌ی پر از فایل هم قطع می‌شود)
		filesSeen++
		if filesSeen%256 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		relWs := relDisplay(ws, path)
		if ig.ignored(relWs, false) {
			return nil
		}
		var hit bool
		if anchored {
			hit = matchSegs(patSegs, strings.Split(relDisplay(root, path), "/"))
		} else {
			hit = globMatch(pattern, d.Name())
		}
		if !hit {
			return nil
		}
		// stat از طریق خود DirEntry (بدون مسیر اضافه)؛ خطا (مثلاً حذف میان
		// پیمایش) فقط همان فایل را رد می‌کند
		fi, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		candidates = append(candidates, globCandidate{rel: relWs, mtime: fi.ModTime().UnixNano()})
		return nil
	})
	// پیمایش با لغو متوقف شد → نتایج جزئی به‌عنوان موفقیت برنمی‌گردند
	if walkErr != nil && (ctx.Err() != nil || errors.Is(walkErr, context.Canceled) || errors.Is(walkErr, context.DeadlineExceeded)) {
		return "ERROR: cancelled by the user.", false
	}
	if len(candidates) == 0 {
		return fmt.Sprintf("No files found matching '%s' (searched '%s').", pattern, relDisplay(ws, root)), true
	}

	// مرتب‌سازی: جدیدترین (بر اساس mtime) اول؛ در تساوی، مسیر صعودی —
	// قطعیت خروجی برای تست‌پذیری و پایداری برای مدل.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].mtime != candidates[j].mtime {
			return candidates[i].mtime > candidates[j].mtime
		}
		return candidates[i].rel < candidates[j].rel
	})
	returned := candidates
	capped := candidatesCapped
	if len(returned) > maxGlobResults {
		returned = returned[:maxGlobResults]
		capped = true
	}
	out := fmt.Sprintf("Found %d file(s) matching '%s' (newest first):\\n", len(returned), pattern)
	for _, c := range returned {
		out += c.rel + "\\n"
	}
	out = strings.TrimRight(out, "\\n")
	if capped {
		out += "\\n...[result limit reached — narrow the pattern]"
	}
	return truncateToolOutput(out), true
}

'''
src = src[:start] + new_fn + src[end:]

open(path, "w").write(src)
print("toolGlob rewritten")
