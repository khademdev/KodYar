// دروازهٔ پایان کار ایجنت (Finish Gate).
//
// مشکل: حلقهٔ ایجنت هر وقت مدل «هیچ ابزاری صدا نمی‌زد» نوبت را تمام می‌کرد،
// حتی اگر چک‌لیست todo_write آیتم باز داشت یا بعد از آخرین تغییر فایل هیچ
// راستی‌آزماییِ موفقی اجرا نشده بود. نتیجه: تحویل کار نیمه‌کاره با اطمینان
// کامل و دوباره‌کاری گران.
//
// این فایل دو چیز اضافه می‌کند:
//  1. todoResumeBlock: چک‌لیست بازِ گفتگو در شروع هر نوبت ایجنت به مدل داده
//     می‌شود (تا پس از «توقف»، نوبت جدید یا فشرده‌سازی، طرح کار فراموش نشود).
//  2. finishGateNudge: اگر مدل می‌خواهد تمام کند ولی آیتم باز دارد، یک پیام
//     کاربری هدایت‌گر داده می‌شود و حلقه ادامه پیدا می‌کند — حداکثر
//     maxFinishGateNudges بار در هر نوبت.
//
// حدود و مهارها:
//   - فقط حالت ایجنت و فقط وقتی ابزارها در دسترس‌اند (useTools)؛ چت معمولی
//     و Plan Mode اصلاً وارد گیت نمی‌شوند.
//   - سقف دو تذکر؛ هر تذکر یک دور از بودجهٔ maxRounds مصرف می‌کند و با پر شدن
//     سقف توکن نوبت (tokenCapHit) یا توقف کاربر هرگز اجرا نمی‌شود.
//   - متن‌ها انگلیسی و کران‌دارند (تعداد آیتم/طول هر آیتم) تا پرامپت باد نکند.
package main

import (
	"fmt"
	"strings"
)

const (
	// maxFinishGateNudges حداکثر تذکر «چک‌لیست باز است» در یک نوبت.
	maxFinishGateNudges = 2
	// سقف‌های متن: کوتاه بماند.
	finishGateMaxItems       = 6
	finishGateMaxItemRunes   = 120
	finishGateResumeMaxRunes = 1100
	finishGateNudgeMaxRunes  = 1400
)

// openTodoItems آیتم‌های باز (pending/in_progress) را به ترتیب اصلی برمی‌گرداند.
// آیتم‌های completed و ورودی خالی نادیده گرفته می‌شوند.
func openTodoItems(items []TodoItem) []TodoItem {
	var out []TodoItem
	for _, it := range items {
		if strings.TrimSpace(it.Content) == "" {
			continue
		}
		if it.Status == todoStatusCompleted {
			continue
		}
		out = append(out, it)
	}
	return out
}

// todoLine یک خط فشرده برای فهرست (کران‌دار).
func todoLine(it TodoItem, n int) string {
	status := strings.TrimSpace(it.Status)
	if status == "" {
		status = todoStatusPending
	}
	content := cutUTF8(strings.TrimSpace(it.Content), finishGateMaxItemRunes)
	return fmt.Sprintf("%d. [%s] %s", n, status, content)
}

// todoResumeBlock بلوک انگلیسیِ شروع نوبت؛ "" وقتی آیتم بازی نیست.
func todoResumeBlock(items []TodoItem) string {
	open := openTodoItems(items)
	if len(open) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Task checklist carried over from earlier in this conversation (todo_write). ")
	b.WriteString("Do not start over and do not end the turn while items are still open:\n")
	for i, it := range open {
		if i >= finishGateMaxItems {
			fmt.Fprintf(&b, "… and %d more open item(s).\n", len(open)-i)
			break
		}
		b.WriteString(todoLine(it, i+1))
		b.WriteString("\n")
	}
	b.WriteString("Continue from the in_progress item, keep the checklist updated with todo_write ")
	b.WriteString("(it replaces the whole list), and clear it with an empty array [] only when everything is done.")
	return cutUTF8(b.String(), finishGateResumeMaxRunes)
}

// finishGateNudge متن تذکر پایان کار؛ "" یعنی گیت لازم نیست.
//
// open: آیتم‌های باز؛ needsVerify: بعد از آخرین تغییر فایل راستی‌آزمایی موفق
// نبوده؛ changed: فایل‌های تغییرکرده (برای اشارهٔ کوتاه).
func finishGateNudge(open []TodoItem, needsVerify bool, changed []string) string {
	if len(open) == 0 {
		// راستی‌آزمایی تنها، کار تذکر راستی‌آزمایی موجود است (یک‌بار در نوبت).
		return ""
	}
	var b strings.Builder
	b.WriteString("You tried to end the turn, but the task checklist is not closed:\n")
	for i, it := range open {
		if i >= finishGateMaxItems {
			fmt.Fprintf(&b, "… and %d more open item(s).\n", len(open)-i)
			break
		}
		b.WriteString(todoLine(it, i+1))
		b.WriteString("\n")
	}
	b.WriteString("\nKeep working now: finish the remaining items (use the tools as needed), ")
	b.WriteString("update the checklist with todo_write, and clear it with [] when every item is completed.\n")
	if needsVerify {
		b.WriteString("You also have no successful verification after your last file change; ")
		if dirs := shortChangedList(changed, 4); dirs != "" {
			b.WriteString("run the project's build/test command covering " + dirs + " ")
		} else {
			b.WriteString("run the project's build/test command ")
		}
		b.WriteString("before finishing.\n")
	}
	return cutUTF8(b.String(), finishGateNudgeMaxRunes)
}

// finishGateNotice متن فارسیِ اعلان برای کاربر (دیده می‌شود، کوتاه).
func finishGateNotice(openCount, attempt int) string {
	return "چک‌لیست " + faDigits(openCount) + " کار باز دارد؛ ایجنت آن‌ها را ادامه می‌دهد (تذکر " +
		faDigits(attempt) + " از " + faDigits(maxFinishGateNudges) + ")."
}

// shortChangedList فهرست کوتاه فایل‌های تغییرکرده برای متن تذکر.
func shortChangedList(changed []string, max int) string {
	if len(changed) == 0 {
		return ""
	}
	n := len(changed)
	if n > max {
		n = max
	}
	items := make([]string, 0, n)
	for _, f := range changed[:n] {
		f = strings.TrimSpace(f)
		if f != "" {
			items = append(items, "`"+f+"`")
		}
	}
	out := strings.Join(items, ", ")
	if len(changed) > max {
		out += " and others"
	}
	return out
}
