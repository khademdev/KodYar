// ابزار apply_patch — ویرایش ساختاری با unified diff (گام ۷).
//
// مشکل: edit_file فقط با «تطبیق متن» کار می‌کند؛ روی فایل‌های بزرگ و
// چندفایلی مدل باید بلوک‌های old_string را بایت‌به‌بایت بازتولید کند، نتیجه:
// شکست تطبیق، تلاش‌های تکراری و کندی. پچ یکپارچه اجازه می‌دهد مدل تغییر را
// با شمارهٔ خط و بافت (context) بفرستد — چند فایل، چند hunk، در یک فراخوانی.
//
// قرارداد:
//   - ورودی: متن unified diff استاندارد (`--- a/x`, `+++ b/x`, `@@ -l,s +l,s @@`).
//     ساخت فایل با `--- /dev/null` و حذف با `+++ /dev/null`.
//   - اعتبارسنجی پیش از هر نوشتن: مسیرها داخل پوشهٔ کاری، هر hunk با بافت
//     خودش در فایل پیدا می‌شود، تطبیقِ مبهم رد می‌شود (بدون حدس).
//   - dry_run=true فقط گزارش می‌دهد (هیچ فایلی نوشته و هیچ تأییدی گرفته نمی‌شود).
//   - نوشتن با همان مسیر امن write_file/edit_file انجام می‌شود: بکاپ، Diff،
//     تأیید کاربر، ثبت تغییر و قابلیت بازگردانی — برای هر فایل جداگانه.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	maxPatchBytes = 512 * 1024 // سقف متن پچ
	maxPatchFiles = 20         // سقف فایل‌های یک پچ
)

// patchLine یک خط بدنه‌ی hunk: ' ' بافت، '-' حذف، '+' افزودن.
type patchLine struct {
	kind      byte
	text      string
	noNewline bool // نشانگر "\ No newline at end of file"
}

// patchHunk یک بخش تغییر با شماره‌خط‌های مبدأ/مقصد.
type patchHunk struct {
	oldStart, oldCount int
	newStart, newCount int
	lines              []patchLine
}

// patchFile تغییرهای یک فایل.
type patchFile struct {
	oldName, newName string
	create, delete   bool
	hunks            []patchHunk
}

// parsePatchHeaderLine شماره‌خط‌های «@@ -a,b +c,d @@» را می‌خواند.
func parsePatchHeaderLine(line string) (h patchHunk, err error) {
	rest := strings.TrimPrefix(line, "@@")
	rest = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "@@"))
	fields := strings.Fields(rest)
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "-") || !strings.HasPrefix(fields[1], "+") {
		return h, fmt.Errorf("malformed hunk header %q (expected @@ -start,count +start,count @@)", line)
	}
	parseRange := func(s string) (start, count int, e error) {
		s = s[1:] // حذف - یا +
		if i := strings.Index(s, ","); i >= 0 {
			count, e = strconv.Atoi(s[i+1:])
			if e != nil {
				return 0, 0, fmt.Errorf("bad line count in %q: %v", s, e)
			}
			s = s[:i]
		} else {
			count = 1
		}
		start, e = strconv.Atoi(s)
		if e != nil {
			return 0, 0, fmt.Errorf("bad line number in %q: %v", s, e)
		}
		return start, count, nil
	}
	if h.oldStart, h.oldCount, err = parseRange(fields[0]); err != nil {
		return h, err
	}
	if h.newStart, h.newCount, err = parseRange(fields[1]); err != nil {
		return h, err
	}
	return h, nil
}

// cleanPatchPath نام فایل را از هدر پچ بیرون می‌کشد (a/ و b/ و برچسب زمان).
func cleanPatchPath(raw string) string {
	p := strings.TrimSpace(raw)
	if i := strings.IndexAny(p, "\t"); i >= 0 {
		p = p[:i]
	}
	p = strings.Trim(p, `"'`)
	if p == "/dev/null" {
		return "/dev/null"
	}
	for _, pref := range []string{"a/", "b/"} {
		if strings.HasPrefix(p, pref) {
			return p[len(pref):]
		}
	}
	return p
}

// parseUnifiedPatch متن پچ را به فهرست فایل‌ها و hunkها تبدیل می‌کند.
// سطرهای ناشناسِ بین بخش‌ها (index، diff --git، mode…) نادیده گرفته می‌شوند.
func parseUnifiedPatch(text string) ([]patchFile, error) {
	var files []patchFile
	var cur *patchFile
	var hunk *patchHunk
	// نویسهٔ پایان خطِ انتهاییِ متن، یک رشتهٔ خالیِ ساختگی تولید می‌کند که
	// نباید عضوی از بدنهٔ hunk شمرده شود (وگرنه شمارش خط‌ها یکی زیاد می‌شود).
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.TrimSuffix(normalized, "\n")
	lines := strings.Split(normalized, "\n")
	for _, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		switch {
		case strings.HasPrefix(line, "--- "):
			files = append(files, patchFile{})
			cur = &files[len(files)-1]
			cur.oldName = cleanPatchPath(strings.TrimPrefix(line, "--- "))
			hunk = nil
		case strings.HasPrefix(line, "+++ "):
			if cur == nil {
				return nil, fmt.Errorf("'+++ %s' without a '--- ' line before it", line)
			}
			cur.newName = cleanPatchPath(strings.TrimPrefix(line, "+++ "))
			cur.create = cur.oldName == "/dev/null"
			cur.delete = cur.newName == "/dev/null"
			cur.oldName = strings.TrimSpace(cur.oldName)
			cur.newName = strings.TrimSpace(cur.newName)
		case strings.HasPrefix(line, "@@"):
			if cur == nil || (cur.oldName == "" && cur.newName == "") {
				return nil, fmt.Errorf("hunk header %q outside a file section", line)
			}
			h, err := parsePatchHeaderLine(line)
			if err != nil {
				return nil, err
			}
			cur.hunks = append(cur.hunks, h)
			hunk = &cur.hunks[len(cur.hunks)-1]
		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file" — روی آخرین خطِ خوانده‌شده می‌نشیند.
			if hunk != nil && len(hunk.lines) > 0 {
				hunk.lines[len(hunk.lines)-1].noNewline = true
			}
		case hunk != nil:
			if line == "" {
				// خط خالی در بدنه = خط بافت خالی (unified diff آن را « » می‌نویسد)
				hunk.lines = append(hunk.lines, patchLine{kind: ' ', text: ""})
				continue
			}
			switch line[0] {
			case ' ', '-', '+':
				hunk.lines = append(hunk.lines, patchLine{kind: line[0], text: line[1:]})
			default:
				// متنِ پچ معمولاً با یک خط جدید (مثل «-- ») تمام می‌شود؛
				// داخل hunk هر چیز دیگری نشانه‌ی پچ ناقص است.
				return nil, fmt.Errorf("unexpected line inside a hunk: %q", line)
			}
		case strings.HasPrefix(line, "diff --git"), strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "new file mode"), strings.HasPrefix(line, "deleted file mode"),
			strings.HasPrefix(line, "old mode"), strings.HasPrefix(line, "new mode"),
			strings.HasPrefix(line, "similarity index"), strings.HasPrefix(line, "rename "):
			// فراداده‌ی git — نادیده
		case strings.TrimSpace(line) == "":
			// فاصلهٔ بین بخش‌ها
		default:
			return nil, fmt.Errorf("unexpected line before any file section: %q", line)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no patch found; expected at least one '--- ' / '+++ ' / '@@' section")
	}
	for i := range files {
		f := &files[i]
		if f.create && f.delete {
			return nil, fmt.Errorf("file #%d is both created and deleted", i+1)
		}
		if !f.create && f.oldName == "" {
			return nil, fmt.Errorf("file #%d has no source path", i+1)
		}
		if !f.delete && f.newName == "" {
			return nil, fmt.Errorf("file #%d has no target path", i+1)
		}
		if len(f.hunks) == 0 {
			return nil, fmt.Errorf("file #%d (%s) has no hunks", i+1, patchPathOf(*f))
		}
		for j, h := range f.hunks {
			var oldN, newN int
			for _, l := range h.lines {
				switch l.kind {
				case ' ':
					oldN++
					newN++
				case '-':
					oldN++
				case '+':
					newN++
				}
			}
			if oldN != h.oldCount || newN != h.newCount {
				return nil, fmt.Errorf("hunk #%d of '%s': header says -%d,+%d line(s) but the body has -%d,+%d",
					j+1, patchPathOf(*f), h.oldCount, h.newCount, oldN, newN)
			}
		}
	}
	return files, nil
}

// patchPathOf مسیر نمایشی یک بخش پچ.
func patchPathOf(f patchFile) string {
	if f.delete {
		return f.oldName
	}
	if f.newName != "" {
		return f.newName
	}
	return f.oldName
}

// splitPatchLines فایل را به خطوط (بدون نویسهٔ پایان خط) و پرچمِ خطِ پایانی
// تجزیه می‌کند تا اسلوب EOL فایل حفظ شود.
func splitPatchLines(s string) (lines []string, endsNL bool) {
	if s == "" {
		return nil, false
	}
	lines = strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
		endsNL = true
	}
	return lines, endsNL
}

// applyHunks hunkها را به‌ترتیب روی محتوای فایل اعمال می‌کند. جای هر hunk
// اول در موقعیت اعلام‌شده و سپس (اگر جابه‌جا شده باشد) در کل فایل جستجو
// می‌شود؛ تطبیق چندگانه «مبهم» است و رد می‌شود.
func applyHunks(rel, src string, hunks []patchHunk, create bool) (string, error) {
	lines, endsNL := splitPatchLines(src)
	if create && len(lines) > 0 {
		return "", fmt.Errorf("'%s' is marked as a new file but it already exists — use a normal patch instead (or delete it first).", rel)
	}
	offset := 0
	noFinalNL := false
	for hi, h := range hunks {
		var oldBlock, newBlock []string
		for _, l := range h.lines {
			switch l.kind {
			case ' ':
				oldBlock = append(oldBlock, l.text)
				newBlock = append(newBlock, l.text)
			case '-':
				oldBlock = append(oldBlock, l.text)
			case '+':
				newBlock = append(newBlock, l.text)
			}
		}
		if len(h.lines) > 0 {
			last := h.lines[len(h.lines)-1]
			if last.noNewline && last.kind != '-' {
				noFinalNL = true
			}
		}
		matchAt := func(i int) bool {
			if i < 0 || i+len(oldBlock) > len(lines) {
				return false
			}
			for k, want := range oldBlock {
				if lines[i+k] != want {
					return false
				}
			}
			return true
		}
		pos := h.oldStart - 1
		if h.oldCount == 0 {
			pos = h.oldStart // hunk خالصِ افزودن: بعد از خط oldStart
		}
		pos += offset
		if !matchAt(pos) {
			var found []int
			for i := 0; i+len(oldBlock) <= len(lines); i++ {
				if len(oldBlock) == 0 {
					break
				}
				if matchAt(i) {
					found = append(found, i)
					if len(found) > 1 {
						break
					}
				}
			}
			switch {
			case len(found) == 1:
				pos = found[0]
			case len(found) > 1:
				return "", fmt.Errorf("hunk #%d of '%s' is ambiguous: its context matches in more than one place. Add more surrounding lines to the hunk.", hi+1, rel)
			default:
				return "", fmt.Errorf("hunk #%d of '%s' does not match the file (expected around line %d). Re-read the file and regenerate the patch with the current content.", hi+1, rel, h.oldStart)
			}
		}
		out := make([]string, 0, len(lines)-len(oldBlock)+len(newBlock))
		out = append(out, lines[:pos]...)
		out = append(out, newBlock...)
		out = append(out, lines[pos+len(oldBlock):]...)
		offset += len(newBlock) - len(oldBlock)
		lines = out
	}
	res := strings.Join(lines, "\n")
	// فایل تازه‌ساخته یا فایلی که نویسهٔ پایان خط داشت، با پایان خط تمام
	// می‌شود؛ مگر اینکه پچ صریحاً "\ No newline at end of file" داشته باشد.
	if (endsNL || create) && len(lines) > 0 && !noFinalNL {
		res += "\n"
	}
	return res, nil
}

// patchPlan یک فایل آماده‌ی نوشتن پس از اعتبارسنجی.
type patchPlan struct {
	rel          string
	f            patchFile
	after        string
	added, total int
}

// toolApplyPatch اجرای ابزار apply_patch: تجزیه، اعتبارسنجی، dry-run و نوشتن.
func (a *App) toolApplyPatch(ctx context.Context, patchText string, dryRun bool) (string, bool) {
	if strings.TrimSpace(patchText) == "" {
		return "ERROR: 'patch' is empty; send a unified diff (--- / +++ / @@).", false
	}
	if len(patchText) > maxPatchBytes {
		return fmt.Sprintf("ERROR: patch is too large (%d bytes); max is %d bytes. Split it into smaller patches.", len(patchText), maxPatchBytes), false
	}
	files, err := parseUnifiedPatch(patchText)
	if err != nil {
		return "ERROR: invalid patch: " + err.Error(), false
	}
	if len(files) > maxPatchFiles {
		return fmt.Sprintf("ERROR: patch touches %d files; max is %d per call. Split it into smaller patches.", len(files), maxPatchFiles), false
	}

	// ---------- اعتبارسنجی کامل پیش از هر نوشتن ----------
	plans := make([]patchPlan, 0, len(files))
	seen := map[string]bool{}
	for i, f := range files {
		rel := patchPathOf(f)
		if _, err := a.resolveWorkspacePath(rel); err != nil {
			return fmt.Sprintf("ERROR: file #%d (%s): %v", i+1, rel, err), false
		}
		key := normalizeToolPath(rel)
		if seen[key] {
			return fmt.Sprintf("ERROR: '%s' appears more than once in the patch; merge its hunks into one section.", rel), false
		}
		seen[key] = true
		var before string
		if !f.create {
			src, rerr := a.readEditSource(rel)
			if rerr != nil {
				return fmt.Sprintf("ERROR: file #%d (%s): %v", i+1, rel, rerr), false
			}
			before = src
		}
		if f.delete {
			plans = append(plans, patchPlan{rel: rel, f: f, after: ""})
			continue
		}
		after, aerr := applyHunks(rel, before, f.hunks, f.create)
		if aerr != nil {
			return fmt.Sprintf("ERROR: %v (no files were written)", aerr), false
		}
		if len(after) > maxEditBytes {
			return fmt.Sprintf("ERROR: '%s' would become too large (%d bytes); max is %d bytes.", rel, len(after), maxEditBytes), false
		}
		_, added, _ := unifiedDiff("a/"+rel, "b/"+rel, before, after)
		plans = append(plans, patchPlan{rel: rel, f: f, after: after, added: added, total: len(after)})
	}

	if dryRun {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("DRY RUN OK: the patch applies cleanly to %d file(s). No files were written.\n", len(plans)))
		for _, p := range plans {
			switch {
			case p.f.delete:
				b.WriteString(fmt.Sprintf("- %s: would be DELETED\n", p.rel))
			case p.f.create:
				b.WriteString(fmt.Sprintf("- %s: would be CREATED (%d bytes, +%d lines)\n", p.rel, p.total, p.added))
			default:
				b.WriteString(fmt.Sprintf("- %s: would be MODIFIED (+%d lines, %d bytes)\n", p.rel, p.added, p.total))
			}
		}
		return strings.TrimRight(b.String(), "\n"), true
	}

	// ---------- نوشتن (هر فایل با بکاپ/Diff/تأیید خودش) ----------
	var done []string
	for _, p := range plans {
		var msg string
		var ok bool
		if p.f.delete {
			msg, ok = a.deletePathWithConfirm(ctx, p.rel)
		} else {
			intent := intentEdit
			if p.f.create {
				intent = intentOverwrite
			}
			msg, ok = a.writeFileWithConfirm(ctx, p.rel, p.after, intent)
		}
		if !ok {
			rest := len(plans) - len(done)
			if rest > 0 {
				rest--
			}
			return fmt.Sprintf("ERROR: apply_patch stopped at '%s': %s (already written: %s; %d file(s) untouched)",
				p.rel, strings.TrimPrefix(msg, "ERROR: "), listOrNone(done), rest), false
		}
		done = append(done, p.rel)
	}
	return fmt.Sprintf("OK: applied patch to %d file(s): %s (+changes recorded, revertible).", len(done), strings.Join(done, ", ")), true
}

// listOrNone فهرست فارسی‌پسند برای پیام خطا.
func listOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

// patchFileExists بررسی وجود فایل برای پیام‌های دقیق‌تر حذف.
func patchFileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
