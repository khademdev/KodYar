// الگوریتم Diff خط‌محور برای پیش‌نمایش تغییرات فایل (سبک unified diff).
//
// پیاده‌سازی: Myers O(ND) با ثبت trace برای بازسازی مسیر، به‌همراه
// بهینه‌سازی‌های عملی:
// 1. حذف پیشوند/پسوند مشترک (بیشترین بخش تغییرات ایجنت این شکلی است)
// 2. بودجه‌ی محدود برای D (تعداد ادیت) — اگر دو فایل بیش از حد متفاوت
// باشند، به‌جای حافظه‌ی سنگین، کل بلوک میانی به‌صورت یک hunk جایگزینی
// گزارش می‌شود. این رفتار برای UI کاملاً قابل‌قبول است.
//
// خروجی: فرمت unified diff استاندارد (همان چیزی که git diff تولید می‌کند)
// تا در UI با رنگ‌بندی + و − قابل رندر باشد و برای مدل هم آشنا باشد.
package main

import (
	"fmt"
	"strings"
)

// محدودیت‌های diff
const (
	diffContextLines = 3    // خطوط زمینه در هر طرف تغییر (مثل git diff)
	diffMaxD         = 2500 // سقف تعداد ادیت‌ها قبل از fallback به جایگزینی کل بلوک
	diffMaxOutput    = 64 * 1024
)

// نوع‌های عملیات diff
const (
	diffKeep = iota // خط مشترک
	diffAdd         // خط اضافه‌شده (در نسخه‌ی جدید)
	diffDel         // خط حذف‌شده (از نسخه‌ی قدیم)
)

// diffOp یک عملیات تک‌مرحله‌ای در اسکریپت diff.
type diffOp struct {
	kind int
	line string
}

// splitLines متن را به خطوط می‌شکند؛ مانند strings.Split اما بدون ایجاد خط
// خالی انتهایی برای متن‌هایی که با newline تمام می‌شوند (رفتار git).
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	// «a\nb\n» → ["a","b"] نه ["a","b",""]
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// diffOps اسکریپت diff بین دو لیست خط را می‌سازد.
//
// مسیر بازگشتی: ابتدا پیشوند/پسوند مشترک حذف می‌شوند؛ اگر هر دو بلوک
// میانی خالی باشند diff تمام است. اگر یکی خالی باشد، insert/delete خالص
// است. در غیر این صورت Myers با بودجه‌ی D اجرا می‌شود و در صورت عبور از
// بودجه، جایگزینی کامل بلوک میانی برمی‌گردد.
func diffOps(a, b []string) []diffOp {
	// ۱. پیشوند مشترک
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	// ۲. پسوند مشترک
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	am := a[pre : len(a)-suf]
	bm := b[pre : len(b)-suf]

	var ops []diffOp
	for _, l := range a[:pre] {
		ops = append(ops, diffOp{diffKeep, l})
	}
	mid := myersOps(am, bm)
	if mid == nil {
		// fallback: جایگزینی کامل بلوک میانی
		for _, l := range am {
			ops = append(ops, diffOp{diffDel, l})
		}
		for _, l := range bm {
			ops = append(ops, diffOp{diffAdd, l})
		}
	} else {
		ops = append(ops, mid...)
	}
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, diffOp{diffKeep, l})
	}
	return ops
}

// myersOps الگوریتم Myers greedy با ثبت trace. nil یعنی «بیش از بودجه».
func myersOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return []diffOp{}
	}
	if n == 0 {
		ops := make([]diffOp, 0, m)
		for _, l := range b {
			ops = append(ops, diffOp{diffAdd, l})
		}
		return ops
	}
	if m == 0 {
		ops := make([]diffOp, 0, n)
		for _, l := range a {
			ops = append(ops, diffOp{diffDel, l})
		}
		return ops
	}
	max := n + m
	if max > diffMaxD {
		return nil // بودجه‌ی ادیت تمام؛ فراخواننده fallback می‌کند
	}
	offset := max
	v := make([]int, 2*max+1) // خط‌های دیاگونال؛ ایندکس‌ها از -max تا +max
	var trace [][]int

outer:
	for d := 0; d <= max; d++ {
		if d > diffMaxD {
			return nil
		}
		vc := make([]int, len(v))
		copy(vc, v)
		trace = append(trace, vc)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1] // حرکت به پایین (insert از b)
			} else {
				x = v[offset+k-1] + 1 // حرکت به راست (delete از a)
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				break outer
			}
		}
	}

	// بازسازی مسیر از انتها به ابتدا
	var rev []diffOp
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		vv := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && vv[offset+k-1] < vv[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := vv[offset+prevK]
		prevY := prevX - prevK

		// خطوط دیاگونال مشترک بین (prevX,prevY) و (x,y)
		for x > prevX && y > prevY {
			rev = append(rev, diffOp{diffKeep, a[x-1]})
			x--
			y--
		}
		if d > 0 {
			if x == prevX {
				// insert از b
				rev = append(rev, diffOp{diffAdd, b[prevY]})
			} else {
				// delete از a
				rev = append(rev, diffOp{diffDel, a[prevX]})
			}
			x, y = prevX, prevY
		}
	}
	// ترتیب معکوس → درست
	ops := make([]diffOp, len(rev))
	for i := range rev {
		ops[i] = rev[len(rev)-1-i]
	}
	return ops
}

// diffStats تعداد خطوط افزوده/حذف‌شده.
func diffStats(ops []diffOp) (added, removed int) {
	for _, op := range ops {
		switch op.kind {
		case diffAdd:
			added++
		case diffDel:
			removed++
		}
	}
	return
}

// unifiedDiff خروجی متن unified diff با هدر فایل‌ها می‌سازد.
// fromName/toName نام‌های نمایشی هستند (مثلاً «a/main.go» و «b/main.go»).
// خروجی به diffMaxOutput بایت محدود می‌شود تا UI و حافظه امن بمانند.
//
// رویکرد استاندارد: ۱) یافتن «ناحیه‌های تغییر» (اجرای متوالی غیرkeep)؛
// ۲) افزودن contextLines خط زمینه به هر طرف؛ ۳) ادغام نواحی هم‌پوشان؛
// ۴) چاپ هر ناحیه به‌صورت یک hunk با شماره‌های درست.
func unifiedDiff(fromName, toName, before, after string) (string, int, int) {
	a := splitLines(before)
	b := splitLines(after)
	ops := diffOps(a, b)
	added, removed := diffStats(ops)

	var sb strings.Builder
	sb.WriteString("--- " + fromName + "\n")
	sb.WriteString("+++ " + toName + "\n")

	// ۱. یافتن نواحی تغییر: [start, end) غیرkeep
	type region struct{ start, end int }
	var regions []region
	i := 0
	for i < len(ops) {
		if ops[i].kind == diffKeep {
			i++
			continue
		}
		j := i
		for j < len(ops) && ops[j].kind != diffKeep {
			j++
		}
		regions = append(regions, region{i, j})
		i = j
	}
	if len(regions) == 0 {
		return "", added, removed // بدون تغییر
	}

	// ۲+۳. افزودن context و ادغام نواحی هم‌پوشان/نزدیک
	const ctx = diffContextLines
	merged := make([]region, 0, len(regions))
	for _, r := range regions {
		s := r.start - ctx
		if s < 0 {
			s = 0
		}
		e := r.end + ctx
		if e > len(ops) {
			e = len(ops)
		}
		if n := len(merged); n > 0 && s <= merged[n-1].end {
			if e > merged[n-1].end {
				merged[n-1].end = e
			}
		} else {
			merged = append(merged, region{s, e})
		}
	}

	// ۴. چاپ hunks — شماره‌های شروع از ابتدای ops شمرده می‌شوند
	for _, r := range merged {
		aStart, bStart := 1, 1
		for k := 0; k < r.start; k++ {
			switch ops[k].kind {
			case diffKeep:
				aStart++
				bStart++
			case diffAdd:
				bStart++
			case diffDel:
				aStart++
			}
		}
		aCount, bCount := 0, 0
		for k := r.start; k < r.end; k++ {
			switch ops[k].kind {
			case diffKeep:
				aCount++
				bCount++
			case diffAdd:
				bCount++
			case diffDel:
				aCount++
			}
		}
		sb.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", aStart, aCount, bStart, bCount))
		for k := r.start; k < r.end; k++ {
			switch ops[k].kind {
			case diffAdd:
				sb.WriteString("+" + ops[k].line + "\n")
			case diffDel:
				sb.WriteString("-" + ops[k].line + "\n")
			default:
				sb.WriteString(" " + ops[k].line + "\n")
			}
		}
	}

	out := sb.String()
	if len(out) > diffMaxOutput {
		out = cutUTF8(out, diffMaxOutput) + "\n...[diff truncated]"
	}
	return out, added, removed
}
