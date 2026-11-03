// ابزار todo_write — چک‌لیست کارهای ایجنت (مرحلهٔ todo).
//
// هدف: برای کارهای چندمرحله‌ای، مدل فهرست کارهایش را صریح نگه می‌دارد و
// کاربر آن را به‌صورت زنده بالای composer می‌بیند (پنل «کارهای ایجنت»).
//
// طراحی:
// - todo_write کل فهرست را «اتمیک» جایگزین می‌کند (نه ویرایش نقطه‌ای)؛
// مدل هر بار فهرست کامل را می‌فرستد — ساده، بدون merge و بدون تداخل.
// - اعتبارسنجی با پیام‌های دقیق انگلیسی (رو به مدل) در همین فایل؛
// پذیرش نوع‌ها loose است (آرایه ممکن است به‌صورت رشتهٔ JSON بیاید —
// coerceTodoList) و متن فارسی بایت‌به‌بایت (با ZWNJ) حفظ می‌شود؛ برشِ
// متن بلند فقط روی مرز کاراکتر (rune) انجام می‌شود.
// - ماندگاری: فیلد اختیاری `todos` روی خود گفتگو (conversations.go)؛
// ذخیره از همان مسیر اتمیک saveConversation می‌گذرد، در Fork کپی
// می‌شود و گفتگوی تازه به‌طور طبیعی خالی است.
// - رویداد `chat:todos` بعد از هر نوشتنِ موفق؛ فرانت‌اند رویدادِ
// گفتگوهای غیرفعال را نادیده می‌گیرد. emit از طریق emitTodos قابل
// جایگزینی در تست است (بدون UI واقعی، شمارش رویداد ممکن می‌شود).
//
// زبان: خطاهای این فایل رو به مدل و انگلیسی‌اند؛ متن‌های UI فارسی در
// فرانت‌اند (todos.js) هستند.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// وضعیت‌های مجاز یک آیتم.
const (
	todoStatusPending    = "pending"
	todoStatusInProgress = "in_progress"
	todoStatusCompleted  = "completed"
)

// محدودیت‌های todo_write.
const (
	maxTodoItems        = 50  // حداکثر تعداد آیتم در هر نوشتن
	maxTodoContentRunes = 200 // حداکثر طول متن هر آیتم (rune) — بلندتر بریده می‌شود
	maxTodoIDRunes      = 64  // حداکثر طول شناسهٔ دلخواه مدل
)

// TodoItem یک کار از چک‌لیست ایجنت — روی خود گفتگو ذخیره می‌شود.
type TodoItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"` // pending | in_progress | completed
}

// validTodoStatus وضعیت مجاز آیتم را بررسی می‌کند.
func validTodoStatus(s string) bool {
	switch s {
	case todoStatusPending, todoStatusInProgress, todoStatusCompleted:
		return true
	}
	return false
}

// coerceJSONArray آرگومان آرایه‌ای را با تحمل نوع ضعیف به []interface{} می‌رساند:
// خودِ آرایه، یا رشته‌ای که یک آرایهٔ JSON را در خود دارد (مدل‌های متن‌باز
// گاهی آرایه را به‌صورت رشته می‌فرستند). قرارداد مشترک todo_write (todos)
// و edit_file (edits).
func coerceJSONArray(v interface{}) ([]interface{}, bool) {
	switch t := v.(type) {
	case []interface{}:
		return t, true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil, false
		}
		var arr []interface{}
		if err := json.Unmarshal([]byte(s), &arr); err == nil {
			return arr, true
		}
	}
	return nil, false
}

// coerceTodoList آرگمان `todos` را با تحمل نوع ضعیف به آرایهٔ خام می‌رساند:
// آرایهٔ JSON، یا رشته‌ای که خودش یک آرایهٔ JSON را دارد (همان قرارداد
// coerceJSONArray). ok=false یعنی با هیچ شکل قابل‌قبولی آرایه نیست.
func coerceTodoList(v interface{}) ([]interface{}, bool) {
	return coerceJSONArray(v)
}

func parseTodoItems(raw interface{}) (items []TodoItem, truncated bool, err error) {
	arr, ok := coerceTodoList(raw)
	if !ok {
		return nil, false, errors.New("ERROR: invalid arguments for todo_write: field \"todos\" must be an array of {id?, content, status} items (a JSON string containing the array is also accepted).")
	}
	if len(arr) > maxTodoItems {
		return nil, false, fmt.Errorf("ERROR: invalid arguments for todo_write: todos must contain at most %d items, got %d.", maxTodoItems, len(arr))
	}

	items = make([]TodoItem, 0, len(arr))
	usedIDs := make(map[string]bool, len(arr))
	var inProgressIdx = -1
	for i, raw := range arr {
		obj, isObj := raw.(map[string]interface{})
		if !isObj {
			return nil, false, fmt.Errorf("ERROR: invalid arguments for todo_write: item %d must be a JSON object with fields {id?, content, status}, got %s.", i+1, jsonTypeName(raw))
		}
		var it TodoItem

		// شناسهٔ اختیاری — رشته یا عدد؛ خالی/غایب = تخصیص خودکار
		if idv, present := obj["id"]; present && idv != nil {
			id, isStr := coerceString(idv)
			if !isStr {
				return nil, false, fmt.Errorf("ERROR: invalid arguments for todo_write: item %d field \"id\" must be a string, got %s.", i+1, jsonTypeName(idv))
			}
			id = strings.TrimSpace(id)
			if r := []rune(id); len(r) > maxTodoIDRunes {
				id = string(r[:maxTodoIDRunes])
			}
			it.ID = id
		}

		contentv, present := obj["content"]
		if !present || contentv == nil {
			return nil, false, fmt.Errorf("ERROR: invalid arguments for todo_write: item %d is missing required field \"content\".", i+1)
		}
		content, isStr := coerceString(contentv)
		if !isStr {
			return nil, false, fmt.Errorf("ERROR: invalid arguments for todo_write: item %d field \"content\" must be a string, got %s.", i+1, jsonTypeName(contentv))
		}
		content = strings.TrimSpace(content)
		if content == "" {
			return nil, false, fmt.Errorf("ERROR: invalid arguments for todo_write: item %d field \"content\" must not be empty.", i+1)
		}
		if r := []rune(content); len(r) > maxTodoContentRunes {
			content = string(r[:maxTodoContentRunes])
			truncated = true
		}
		it.Content = content

		statusv, present := obj["status"]
		if !present || statusv == nil {
			return nil, false, fmt.Errorf("ERROR: invalid arguments for todo_write: item %d is missing required field \"status\".", i+1)
		}
		status, isStr := coerceString(statusv)
		if !isStr {
			return nil, false, fmt.Errorf("ERROR: invalid arguments for todo_write: item %d field \"status\" must be a string, got %s.", i+1, jsonTypeName(statusv))
		}
		status = strings.TrimSpace(status)
		if !validTodoStatus(status) {
			return nil, false, fmt.Errorf("ERROR: invalid arguments for todo_write: item %d field \"status\" must be one of \"pending\" | \"in_progress\" | \"completed\", got %q.", i+1, status)
		}
		it.Status = status

		// یکتایی شناسه‌ها — شناسهٔ خالی بعداً خودکار تخصیص می‌یابد
		if it.ID != "" {
			if usedIDs[it.ID] {
				return nil, false, fmt.Errorf("ERROR: invalid arguments for todo_write: duplicate id %q (item %d); ids must be unique or omitted.", it.ID, i+1)
			}
			usedIDs[it.ID] = true
		}

		if status == todoStatusInProgress {
			if inProgressIdx >= 0 {
				return nil, false, fmt.Errorf("ERROR: invalid arguments for todo_write: at most one item may be \"in_progress\" (items %d and %d both are).", inProgressIdx, i+1)
			}
			inProgressIdx = i + 1
		}
		items = append(items, it)
	}

	// تخصیص موقعیتی «1..n» به شناسه‌های غایب — بدون برخورد با شناسه‌های
	// داده‌شده (عدد بعدی آزاد انتخاب می‌شود تا یکتایی تضمین بماند).
	next := 1
	for i := range items {
		if items[i].ID != "" {
			continue
		}
		for {
			cand := strconv.Itoa(next)
			next++
			if !usedIDs[cand] {
				items[i].ID = cand
				usedIDs[cand] = true
				break
			}
		}
	}
	return items, truncated, nil
}

// todoCounts شمارش آیتم‌ها به تفکیک وضعیت (برای پیام نتیجه به مدل).
func todoCounts(items []TodoItem) (pending, inProgress, completed int) {
	for _, it := range items {
		switch it.Status {
		case todoStatusPending:
			pending++
		case todoStatusInProgress:
			inProgress++
		case todoStatusCompleted:
			completed++
		}
	}
	return
}

// toolTodoWrite اجرای ابزار todo_write: اعتبارسنجی → ذخیرهٔ اتمیک روی گفتگو
// → رویداد chat:todos → پیام تأیید کوتاه انگلیسی (شمار هر وضعیت).
//
// شناسهٔ گفتگو از همان قرارداد run_command (withToolCallInfo در ctx) می‌آید؛
// بدون آن (فراخوانی مستقیم/تست بدون info) ابزار کاربردی ندارد و خطای
// انگلیسی برمی‌گردد. قفل: بارگذاری/ذخیره زیر a.mu، emit بعد از بازشدن قفل
// (قاعدهٔ «هرگز a.mu روی I/O/رویداد» — سند ترتیب قفل‌ها ARCHITECTURE.md).
func (a *App) toolTodoWrite(ctx context.Context, raw interface{}) (string, bool) {
	items, truncated, err := parseTodoItems(raw)
	if err != nil {
		return err.Error(), false
	}
	if ctx != nil && ctx.Err() != nil {
		return "ERROR: cancelled by the user.", false
	}
	info, _ := ctx.Value(ctxKeyToolCall{}).(*toolCallCtxInfo)
	convID := ""
	if info != nil {
		convID = info.convID
	}
	if convID == "" {
		return "ERROR: todo_write is only available inside an agent conversation turn.", false
	}

	a.mu.Lock()
	conv, lerr := a.loadConversation(convID)
	if lerr != nil {
		a.mu.Unlock()
		return "ERROR: could not load the conversation to store todos: " + lerr.Error(), false
	}
	conv.Todos = items
	if serr := a.saveConversation(conv); serr != nil {
		a.mu.Unlock()
		return "ERROR: failed to save the todo list: " + serr.Error(), false
	}
	a.mu.Unlock()

	// کپی برای emit بعد از بازشدن قفل — فرانت‌اند نباید به اسلایس داخلی دست بزند.
	snapshot := make([]TodoItem, len(items))
	copy(snapshot, items)
	a.emitTodos(convID, snapshot)

	pending, inProgress, completed := todoCounts(items)
	msg := fmt.Sprintf("OK: todo list updated — %d item(s): %d completed, %d in_progress, %d pending. The user sees this checklist live in the UI.",
		len(items), completed, inProgress, pending)
	if truncated {
		msg += " Note: item content longer than " + strconv.Itoa(maxTodoContentRunes) + " characters was rune-safely truncated."
	}
	return msg, true
}

// emitTodos رویداد chat:todos را با {conversationId, todos} منتشر می‌کند.
// a.todosEmit فقط در تست‌ها (پیش از هر هم‌زمانی) جایگزین می‌شود تا شمارش و
// محتوای رویداد بدون UI واقعی بررسی شود؛ در حالت عرفی nil است و مسیر
// استاندارد runtimeEmit اجرا می‌شود.
func (a *App) emitTodos(convID string, items []TodoItem) {
	if a.todosEmit != nil {
		a.todosEmit(convID, items)
		return
	}
	if items == nil {
		items = []TodoItem{} // پاک‌کردن فهرست هم رویداد با آرایهٔ خالی می‌فرستد
	}
	runtimeEmit(a, "chat:todos", map[string]interface{}{
		"conversationId": convID,
		"todos":          items,
	})
}

// setTodosForTest تنظیم هک emit مخصوص تست‌ها — باید پیش از شروع
// گوروتین‌های هم‌زمان صدا زده شود (بدون قفل خوانده می‌شود).
func (a *App) setTodosForTest(fn func(convID string, items []TodoItem)) {
	a.todosEmit = fn
}

// todoItemsValidUTF8 بررسی کمکی تست: همهٔ متن‌ها باید UTF-8 معتبر بمانند.
func todoItemsValidUTF8(items []TodoItem) bool {
	for _, it := range items {
		if !utf8.ValidString(it.Content) || !utf8.ValidString(it.ID) {
			return false
		}
	}
	return true
}
