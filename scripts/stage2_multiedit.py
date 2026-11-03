#!/usr/bin/env python3
"""Stage 2 patch A: edit_file multi-edit form in agent.go.

Replaces the monolithic toolEditFile with:
  - editOp type + applyEditOp (full cascade, reusable)
  - readEditSource helper
  - toolEditFileArgs dispatcher (single vs multi form, mutual exclusion)
  - parseEditOps (array validation, loose JSON-string typing)
  - toolEditFileMulti (sequential in-memory application, all-or-nothing)
Byte-exact preservation of all existing single-edit error messages.
"""
import re

path = "agent.go"
src = open(path).read()

TAB = "\t"

# ---------------------------------------------------------------- 1) dispatch
old = (
    '\tcase "edit_file":\n'
    '\t\treturn a.toolEditFile(ctx, gs("path"), gs("old_string"), gs("new_string"), gb("replace_all"))\n'
)
new = (
    '\tcase "edit_file":\n'
    "\t\t// شکل تک‌ویرایش و چندویرایش (edits) — تفکیک/اعتبارسنجی در toolEditFileArgs\n"
    "\t\treturn a.toolEditFileArgs(ctx, args)\n"
)
assert src.count(old) == 1, "dispatch anchor"
src = src.replace(old, new)

# ------------------------------------------- 2) replace toolEditFile function
# locate the full old function block (from its doc comment to its closing brace)
start = src.index("// toolEditFile ویرایش دقیق بخشی از فایل")
end = src.index("// commitEditFile خروجی نهایی ویرایش را")
old_block = src[start:end]

new_block = '''// editOp یک ویرایش واحد — یا از شکل تک‌ویرایش (old_string/new_string) یا
// یکی از آیتم‌های آرایه‌ی edits.
type editOp struct {
	oldStr     string
	newStr     string
	replaceAll bool
}

// applyEditOp یک ویرایش را با کامل‌ترین آبشار تطبیق روی src اعمال می‌کند:
// exact → نرمال‌سازی CRLF/LF → تحمل فاصله‌ی انتهایی → تحمل تورفتگی (فقط با
// تطبیق یکتا؛ در آن صورت متن جایگزین هم‌راستا می‌شود). خروجی: محتوای جدید،
// تعداد رخدادهای جایگزین‌شده، استراتژی برنده و errMsg.
//
// errMsg عمداً بدون پیشوند «ERROR: » است تا فراخوانی‌کننده‌ها (شکل
// تک‌ویرایش و چندویرایش) هرکدام پیام خودشان را بسازند — متنِ خطاها با
// نسخه‌ی قبلی (تک‌ویرایش) بایت‌به‌بایت یکسان می‌ماند.
func applyEditOp(rel, src string, op editOp) (out string, count int, strategy string, errMsg string) {
	oldStr := op.oldStr
	if oldStr == "" {
		return "", 0, "", "old_string is empty."
	}
	// سطح ۱: تطبیق دقیق — رفتار همیشگی، بدون هیچ تغییری
	if cnt := strings.Count(src, oldStr); cnt > 0 {
		if cnt > 1 && !op.replaceAll {
			return "", 0, "", fmt.Sprintf("old_string appears %d time(s) in '%s'. Include more surrounding lines to make it unique, or pass replace_all=true.", cnt, rel)
		}
		return strings.ReplaceAll(src, oldStr, op.newStr), cnt, "", ""
	}

	// سطوح سهل‌گیرتر — وابسته به نما/نگاشت (editmatch.go). برای فایل‌های
	// بزرگ‌تر از سقف تطبیق مقاوم، این سطوح اجرا نمی‌شوند: حافظه‌ی نگاشت بازه
	// کران‌دار می‌ماند و مدل باید old_string دقیق بفرستد (Bug 7).
	if len(src) > maxFuzzyEditBytes {
		return "", 0, "", fmt.Sprintf("old_string not found in '%s' (the exact byte-for-byte match failed). This file is too large (%d bytes; tolerant matching limit is %d bytes) for indent/EOL/whitespace-tolerant matching. Use an exact old_string — byte-for-byte, including the file's real indentation — or edit a smaller range of the file.", rel, len(src), maxFuzzyEditBytes)
	}
	for _, lv := range editMatchLevels[1:] {
		needle := normalizeEditNeedle(oldStr, lv.ignoreTrail, lv.ignoreIndent)
		if needle == "" {
			continue // old_string بعد از نرمال‌سازی خالی است — تطبیق بی‌معناست
		}
		v := buildNormView(src, lv.ignoreTrail, lv.ignoreIndent)
		regions := findEditRegions(src, v, needle, lv.ignoreIndent)
		if len(regions) == 0 {
			continue
		}
		if len(regions) > 1 && (lv.requireUnique || !op.replaceAll) {
			if lv.requireUnique {
				return "", 0, "", fmt.Sprintf("old_string appears %d time(s) in '%s' when matching with %s; this tolerant fallback requires a UNIQUE match. Include more surrounding lines or copy the exact indentation.", len(regions), rel, lv.name)
			}
			return "", 0, "", fmt.Sprintf("old_string appears %d time(s) in '%s' (after %s). Include more surrounding lines to make it unique, or pass replace_all=true.", len(regions), rel, lv.name)
		}
		// سطح «تحمل تورفتگی»: تورفتگیِ مدل ممکن است با فایل فرق کند (۴ در برابر ۸
		// فاصله، یا فاصله در برابر تب)؛ در آن صورت new_string هم با تورفتگی واقعی
		// همان خط هم‌راستا می‌شود تا ساختار (پایتون/YAML) خراب نشود.
		newForRegions, strategy := op.newStr, lv.name
		if lv.ignoreIndent && len(regions) == 1 {
			if adjusted, from, to, reindented := indentTolerantReplacement(src, oldStr, regions[0], op.newStr); reindented {
				newForRegions = adjusted
				strategy += "; replacement re-indented from " + from + " to " + to
			}
		}
		return applyEditRegions(src, newForRegions, regions), len(regions), strategy, ""
	}

	return "", 0, "", "old_string not found in '" + rel + "' even with tolerant matching " +
		"(tried: exact, line-ending normalization, trailing-whitespace tolerance, indentation tolerance). " +
		"Read the file again with read_file, then copy the snippet byte-for-byte including surrounding lines."
}

// maxEditOps سقف تعداد ویرایش‌های آرایه‌ی edits در هر فراخوانی.
const maxEditOps = 20

// readEditSource فایلِ هدف ویرایش را باز و بررسی می‌کند (مسیر داخل
// workspace، فایل بودن، سقف حجم) و محتوای آن را برمی‌گرداند. خطاها همان
// متن قبلیِ toolEditFile را دارند تا پیام‌های رو به مدل تغییر نکنند.
func (a *App) readEditSource(rel string) (string, error) {
	p, err := a.resolveWorkspacePath(rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", errors.New(err.Error() + " — read the file first (maybe the path is wrong).")
	}
	if info.IsDir() {
		return "", errors.New("'" + rel + "' is a directory.")
	}
	if info.Size() > maxEditBytes {
		return "", errors.New("file too large to edit in memory.")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// toolEditFileArgs نقطه‌ی ورود edit_file: شکل تک‌ویرایش
// (old_string + new_string [+ replace_all]) و شکل چندویرایش (edits) —
// هرگز هر دو با هم. تفکیک و پیام‌های دقیق انگلیسی همین‌جاست.
func (a *App) toolEditFileArgs(ctx context.Context, args map[string]interface{}) (string, bool) {
	gs := func(key string) string {
		s, _ := coerceString(args[key])
		return s
	}
	gb := func(key string) bool {
		b, _ := coerceBool(args[key])
		return b
	}
	rel := gs("path")

	_, hasSingle := args["old_string"]
	editsRaw, hasEdits := args["edits"]
	if editsRaw == nil {
		hasEdits = false // «edits»: null مثل غایب رفتار می‌کند
	}
	if hasSingle && hasEdits {
		return "ERROR: invalid arguments for edit_file: pass either the single-edit fields (old_string + new_string [+ replace_all]) or the multi-edit field \\"edits\\" — not both.", false
	}
	if !hasSingle && !hasEdits {
		return "ERROR: invalid arguments for edit_file: missing required field \\"old_string\\" (single edit) or \\"edits\\" (multi edit).", false
	}
	if !hasEdits {
		return a.toolEditFile(ctx, rel, gs("old_string"), gs("new_string"), gb("replace_all"))
	}
	ops, verr := parseEditOps(editsRaw)
	if verr != nil {
		return "ERROR: invalid arguments for edit_file: " + verr, false
	}
	return a.toolEditFileMulti(ctx, rel, ops)
}

// parseEditOps آرایه‌ی edits را با تحمل نوع ضعیف (آرایه، یا رشته‌ی JSON
// حاوی آرایه) اعتبارسنجی می‌کند — خطاها آیتم را با شماره‌ی ۱-مبنا نام
// می‌برند تا مدل بلافاصله بداند کدام ویرایش اشتباه است. متن‌ها هیچ
// نرمال‌سازی‌ای نمی‌شوند (فارسی/ZWNJ بایت‌به‌بایت می‌ماند).
func parseEditOps(raw interface{}) ([]editOp, string) {
	arr, ok := coerceJSONArray(raw)
	if !ok {
		return nil, "field \\"edits\\" must be an array of {old_string, new_string, replace_all?} items (a JSON string containing the array is also accepted)."
	}
	if len(arr) == 0 {
		return nil, "field \\"edits\\" must contain at least one edit."
	}
	if len(arr) > maxEditOps {
		return nil, fmt.Sprintf("field \\"edits\\" must contain at most %d edit(s), got %d.", maxEditOps, len(arr))
	}
	ops := make([]editOp, 0, len(arr))
	for i, rawItem := range arr {
		obj, isObj := rawItem.(map[string]interface{})
		if !isObj {
			return nil, fmt.Sprintf("edits[%d] must be a JSON object with fields {old_string, new_string, replace_all?}, got %s.", i, jsonTypeName(rawItem))
		}
		var op editOp
		oldv, present := obj["old_string"]
		if !present || oldv == nil {
			return nil, fmt.Sprintf("edits[%d] is missing required field \\"old_string\\".", i)
		}
		s, isStr := coerceString(oldv)
		if !isStr {
			return nil, fmt.Sprintf("edits[%d] field \\"old_string\\" must be a string, got %s.", i, jsonTypeName(oldv))
		}
		if strings.TrimSpace(s) == "" {
			return nil, fmt.Sprintf("edits[%d] field \\"old_string\\" must not be empty.", i)
		}
		op.oldStr = s
		newv, npresent := obj["new_string"]
		if !npresent || newv == nil {
			return nil, fmt.Sprintf("edits[%d] is missing required field \\"new_string\\" (an empty string is allowed and deletes the matched text).", i)
		}
		ns, isStr := coerceString(newv)
		if !isStr {
			return nil, fmt.Sprintf("edits[%d] field \\"new_string\\" must be a string, got %s.", i, jsonTypeName(newv))
		}
		op.newStr = ns
		if bv, bpresent := obj["replace_all"]; bpresent && bv != nil {
			b, isB := coerceBool(bv)
			if !isB {
				return nil, fmt.Sprintf("edits[%d] field \\"replace_all\\" must be a boolean (true/false, \\"true\\"/\\"false\\" or 0/1), got %s.", i, jsonTypeName(bv))
			}
			op.replaceAll = b
		}
		ops = append(ops, op)
	}
	return ops, ""
}

// toolEditFile ویرایش دقیق بخشی از فایل (شکل تک‌ویرایش) — آبشار تطبیق در
// applyEditOp و نتیجه از همان مسیر تأیید/بکاپ/writeFileWithConfirm عبور
// می‌کند تا Diff و Revert یکپارچه بماند.
func (a *App) toolEditFile(ctx context.Context, rel, oldStr, newStr string, replaceAll bool) (string, bool) {
	src, err := a.readEditSource(rel)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	out, cnt, strategy, errMsg := applyEditOp(rel, src, editOp{oldStr: oldStr, newStr: newStr, replaceAll: replaceAll})
	if errMsg != "" {
		return "ERROR: " + errMsg, false
	}
	return a.commitEditFile(ctx, rel, out, cnt, strategy)
}

// toolEditFileMulti چندویرایشِ اتمیک: ویرایش‌ها به‌ترتیب روی نتیجه‌ی
// درون‌حافظه‌ایِ ویرایشِ قبلی اعمال می‌شوند (ویرایش بعدی متنِ حاصل از
// قبلی را می‌بیند)؛ اگر هر کدام شکست بخورد هیچ چیزی روی دیسک نوشته
// نمی‌شود (همه یا هیچ) و خطا می‌گوید ویرایش شمارهٔ چند و به چه علتی
// شکست خورده است. کل مجموعه فقط «یک» بکاپ، «یک» Diff و «یک» تأیید
// می‌گیرد؛ BOM و سبک EOL فایل با همان آبشار تطبیق بایت‌به‌بایت حفظ
// می‌شوند و سقف‌های حجمی (maxEditBytes / maxFuzzyEditBytes) روی هر
// ویرایش و روی خروجی نهایی اعمال می‌شوند.
func (a *App) toolEditFileMulti(ctx context.Context, rel string, ops []editOp) (string, bool) {
	src, err := a.readEditSource(rel)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	cur := src
	strategies := make([]string, len(ops))
	total := 0
	for i, op := range ops {
		out, cnt, strategy, errMsg := applyEditOp(rel, cur, op)
		if errMsg != "" {
			return fmt.Sprintf("ERROR: multi-edit aborted at edit #%d (of %d) in '%s' — no changes were written: %s", i+1, len(ops), rel, errMsg), false
		}
		if len(out) > maxEditBytes {
			return fmt.Sprintf("ERROR: multi-edit aborted at edit #%d (of %d): the resulting file would be too large (%d bytes); max is %d bytes. Use smaller replacements or split the work.", i+1, len(ops), len(out), maxEditBytes), false
		}
		cur = out
		strategies[i] = strategy
		total += cnt
	}
	msg, ok := a.writeFileWithConfirm(ctx, rel, cur)
	if ok && strings.HasPrefix(msg, "OK") {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("OK: applied %d edit(s) (%d replacement(s)) in '%s'", len(ops), total, rel))
		used := false
		for i, st := range strategies {
			if st == "" {
				continue
			}
			if !used {
				b.WriteString(" — matched with: ")
				used = true
			} else {
				b.WriteString("; ")
			}
			b.WriteString(fmt.Sprintf("edit #%d: %s", i+1, st))
		}
		b.WriteString(" (+change recorded, revertible).")
		msg = b.String()
	}
	return msg, ok
}

'''
src = src[:start] + new_block + src[end:]

open(path, "w").write(src)
print("agent.go toolEditFile block replaced")
