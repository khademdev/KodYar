// خروجی‌گیری از گفتگو (اولویت ۷) — Markdown و JSON.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ExportConversationMD گفتگو را به Markdown خوانا تبدیل می‌کند.
// تصاویر پایه۶۴ جایگزین نشانه‌ی [تصویر] می‌شوند تا فایل سبک بماند.
func exportConversationMD(c *Conversation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", c.Title)
	fmt.Fprintf(&b, "> خروجی‌گرفته از MyAI — %s\n\n", faDateTimeStatic(c.CreatedAt))
	fmt.Fprintf(&b, "**تعداد پیام‌ها:** %d\n\n---\n\n", len(c.Messages))
	for _, m := range c.Messages {
		switch m.Role {
		case "user":
			b.WriteString("## 🧑 کاربر\n\n")
			b.WriteString(m.Content + "\n\n")
			if len(m.Images) > 0 {
				fmt.Fprintf(&b, "*(%d تصویر پیوست‌شده)*\n\n", len(m.Images))
			}
			if m.Pinned {
				b.WriteString("📌 *سنجاق‌شده*\n\n")
			}
		case "assistant":
			b.WriteString("## 🤖 MyAI\n\n")
			if strings.TrimSpace(m.Content) != "" {
				b.WriteString(m.Content + "\n\n")
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "`🛠 %s(%s)`\n\n", tc.Name, tc.Args)
			}
			if m.Aborted {
				b.WriteString("*(متوقف‌شده)*\n\n")
			}
		case "tool":
			fmt.Fprintf(&b, "<details><summary><code>🛠 %s</code> — نتیجه‌ی ابزار", m.ToolName)
			if !m.ToolOK {
				b.WriteString(" (ناموفق)")
			}
			b.WriteString("</summary>\n\n```\n")
			out := m.Content
			if len(out) > 4000 {
				// برش با احترام به مرز کاراکترهای UTF-8 (فارسی چندبایتی است)
				out = cutUTF8(out, 4000) + "\n…[بریده شده در خروجی]"
			}
			b.WriteString(out)
			b.WriteString("\n```\n\n</details>\n\n")
		}
	}
	return b.String()
}

// faDateTimeStatic بدون وابستگی به فرانت‌اند — تاریخ شمسی ساده.
func faDateTimeStatic(ts int64) string {
	t := time.Unix(ts, 0)
	return t.Format("2006-01-02 15:04")
}

// GetConversationExport خروجی MD یا JSON متن گفتگو را برمی‌گرداند (برای ذخیره در UI).
func (a *App) GetConversationExport(convID, format string) (string, error) {
	a.mu.Lock()
	c, err := a.loadConversation(convID)
	a.mu.Unlock()
	if err != nil {
		return "", err
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "md", "markdown", "":
		return exportConversationMD(c), nil
	case "json":
		return exportConversationJSON(c)
	default:
		return "", fmt.Errorf("فرمت ناشناخته: %s (md یا json)", format)
	}
}

// exportConversationJSON خروجی JSON کامل (ساختارمند و تورفته).
func exportConversationJSON(c *Conversation) (string, error) {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
