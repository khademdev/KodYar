// مدیریت تاریخچه‌ی گفتگو — هرس نتایج ابزارهای قدیمی، فشرده‌سازی، سقف حجم،
// Fork گفتگو و Pin پیام (اولویت ۳).
//
// سه لایه‌ی مستقل:
// 1. هرس API (pruneHistoryForAPI): نتایج ابزارهای قدیمی فقط در «کانتکست
// ارسالی به مدل» فشرده می‌شوند؛ فایل ذخیره‌شده دست‌نخورده می‌ماند.
// پیام‌های Pinشده هم اگر از پنجره‌ی MaxHistory خارج باشند برمی‌گردند.
// 2. فشرده‌سازی دیسک (CompactConversation): جایگزینی نتایج ابزار قدیمی با
// خلاصه در خود فایل گفتگو — با دکمه‌ی کاربر یا خودکار هنگام عبور از سقف.
// 3. سقف سخت حجم (در saveConversation): عبور از سقف → فشرده‌سازی خودکار؛
// اگر باز هم بزرگ بود → حذف قدیمی‌ترین پیام‌ها (حداقل ۱۲ پیام می‌ماند).
package main

import (
	"errors"
	"strings"
	"time"
)

// محدودیت‌های هرس
const (
	compactKeepHead  = 240  // کاراکتر اول نتیجه‌ی ابزار که در خلاصه می‌ماند
	compactMinSize   = 600  // نتایج کوچک‌تر از این هرس نمی‌شوند (بی‌فایده است)
	sizeWarnFraction = 0.75 // هشدار از ۷۵٪ سقف
	minKeepMessages  = 12   // حداقل پیام‌های نگه‌داری‌شده در سقف سخت
)

// compactToolResult نتیجه‌ی ابزار را به خلاصه‌ی کوتاه تبدیل می‌کند.
// خط اول + چند خط کلیدی حفظ می‌شود تا مدل بفهمد چه اتفاقی افتاده بود.
func compactToolResult(content string) string {
	if len(content) <= compactMinSize {
		return content
	}
	head := content
	if len(head) > compactKeepHead {
		head = cutUTF8(head, compactKeepHead)
	}
	// برش در آخرین خط کامل
	if i := strings.LastIndex(head, "\n"); i > compactKeepHead/2 {
		head = head[:i]
	}
	return head + "\n…[older tool output pruned to save context]"
}

// ---------------------------------------------------------------- Pin

// SetMessagePinned وضعیت Pin یک پیام را عوض می‌کند و گفتگوی به‌روزشده
// را برمی‌گرداند. پیام‌های Pinشده همیشه در کانتکست مدل می‌مانند حتی اگر
// از پنجره‌ی MaxHistory خارج شوند.
func (a *App) SetMessagePinned(convID, messageID string, pinned bool) (*Conversation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.loadConversation(convID)
	if err != nil {
		return nil, err
	}
	found := false
	for i := range c.Messages {
		if c.Messages[i].ID == messageID {
			c.Messages[i].Pinned = pinned
			found = true
			break
		}
	}
	if !found {
		return nil, errors.New("پیام موردنظر پیدا نشد")
	}
	if err := a.saveConversation(c); err != nil {
		return nil, err
	}
	return c, nil
}

// pinnedOutsideWindow پیام‌های Pinشده‌ای را برمی‌گرداند که از پنجره‌ی
// تاریخچه حذف شده‌اند (index < start) — باید بعد از system prompt برگردند.
func pinnedOutsideWindow(msgs []Message, start int) []Message {
	var out []Message
	for i := 0; i < start && i < len(msgs); i++ {
		if msgs[i].Pinned {
			out = append(out, msgs[i])
		}
	}
	return out
}

// ---------------------------------------------------------------- Fork

// ForkConversation شاخه‌ی جدیدی از گفتگو می‌سازد که فقط تا پیام مشخص‌شده
// را دارد (خود پیام هم شامل است). عنوان با پسوند «(شاخه)» ذخیره می‌شود.
func (a *App) ForkConversation(convID, fromMessageID string) (*Conversation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	src, err := a.loadConversation(convID)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i := range src.Messages {
		if src.Messages[i].ID == fromMessageID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, errors.New("پیام موردنظر برای شاخه‌گرفتن پیدا نشد")
	}
	fork := &Conversation{
		ID:        newID(),
		Title:     src.Title + " (شاخه)",
		CreatedAt: time.Now().Unix(),
		Messages:  make([]Message, idx+1),
	}
	copy(fork.Messages, src.Messages[:idx+1])
	// چک‌لیست کارهای ایجنت (todo_write) هم در شاخه کپی می‌شود — کپی عمیق
	// تا اسلایس مشترک با گفتگوی مادر نماند.
	if len(src.Todos) > 0 {
		fork.Todos = make([]TodoItem, len(src.Todos))
		copy(fork.Todos, src.Todos)
	}
	// Pin فقط برای پیام‌های داخل پنجره معنا دارد؛ در شاخه نگه داشته می‌شود.
	if err := a.saveConversation(fork); err != nil {
		return nil, err
	}
	return fork, nil
}

// ---------------------------------------------------------------- فشرده‌سازی دیسک

// CompactConversation نتایج ابزارهای قدیمی (به‌جز ToolResultKeep مورد آخر)
// و Attached پیام‌های قدیمی (به‌جز آخرین پیام کاربر) را در «فایل ذخیره‌شده»
// فشرده می‌کند. گفتگوی به‌روزشده را برمی‌گرداند. عملیات قابل بازگشت نیست.
func (a *App) CompactConversation(convID string) (*Conversation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, err := a.loadConversation(convID)
	if err != nil {
		return nil, err
	}
	a.compactConversationLocked(c)
	if err := a.saveConversation(c); err != nil {
		return nil, err
	}
	return c, nil
}

// compactConversationLocked فشرده‌سازی در حافظه — باید با a.mu قفل‌شده صدا زده شود.
func (a *App) compactConversationLocked(c *Conversation) {
	keep := a.settings.ToolResultKeep
	if keep <= 0 {
		keep = 6
	}

	// ایندکس ترتیبی اولین نتیجه‌ی ابزاری که کامل می‌ماند
	toolCount := 0
	for _, m := range c.Messages {
		if m.Role == "tool" {
			toolCount++
		}
	}
	fullFrom := toolCount - keep

	// آخرین پیام کاربر (Attached آن حفظ می‌شود)
	lastUserIdx := -1
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == "user" {
			lastUserIdx = i
			break
		}
	}

	seen := 0
	for i := range c.Messages {
		m := &c.Messages[i]
		switch m.Role {
		case "tool":
			if seen < fullFrom {
				m.Content = compactToolResult(m.Content)
				m.Compacted = true
			}
			seen++
		case "user":
			if i != lastUserIdx {
				// متن اصلی پیام دست‌نخورده می‌ماند؛ کانتکست فایل و تصاویر
				// قدیمی حذف می‌شوند (حجم زیاد base64)
				m.Attached = ""
				m.Images = nil
			}
		}
	}
}
