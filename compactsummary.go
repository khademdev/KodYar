// خلاصه‌سازی مدل‌محورِ تاریخچهٔ حذف‌شده + «واقعیت‌های سنجاق‌شده» (گام ۸).
//
// مشکل: compactHistoryForBudget بافت قدیمی را (پس از فشرده‌سازی نتایج ابزار)
// فقط «حذف» می‌کند و یک نشانگر ثابت می‌گذارد؛ در کارهای طولانی تصمیم‌ها،
// مسیرهای مهم و خطاهای بازِ حل‌نشده گم می‌شدند و مدل دوباره از صفر می‌گشت.
//
// راه‌حل دو لایه:
//
//	۱) واقعیت‌های سنجاق‌شده (قطعی، بدون مدل و بدون هزینه): فایل‌های
//	   تغییرکرده/خوانده‌شدهٔ مهم، دستورهای راستی‌آزمایی و خطاهای بازِ حل‌نشده
//	   از همان پیام‌های حذف‌شده استخراج و به نشانگر فشرده‌سازی چسبانده می‌شوند.
//	۲) خلاصهٔ مدل (بهترین-تلاش): یک درخواست یک‌بارهٔ بدون ابزار، خروجی ≤۱۲۰۰
//	   کاراکتر؛ اگر شکست بخورد یا مهلت بگذرد، همان واقعیت‌های سنجاق‌شده
//	   می‌مانند (رفتار قبلی، بدون شکستن نوبت).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	compactSummaryTimeout  = 30 * time.Second
	compactSummaryMaxOut   = 1200  // سقف کاراکتر خلاصهٔ مدل
	compactSummaryInLimit  = 24000 // سقف کاراکتر ورودی خلاصه‌ساز
	compactFactsLimit      = 14    // سقف واقعیت‌های سنجاق‌شده
	compactOpenErrorsLimit = 5
	compactFactTextLimit   = 220
	compactMarkerPartLimit = 3000 // سقف متن افزوده به نشانگر
)

// toolPathsForFacts مسیرهای یک فراخوانی ابزار را برای فهرست واقعیت‌ها
// برمی‌گرداند (فقط ابزارهای مرتبط با فایل/پچ).
func toolPathsForFacts(name, argsJSON string) []string {
	var args map[string]interface{}
	if json.Unmarshal([]byte(argsJSON), &args) != nil {
		return nil
	}
	str := func(k string) string {
		s, _ := args[k].(string)
		return strings.TrimSpace(s)
	}
	switch name {
	case "write_file", "edit_file", "delete_path", "read_file", "create_dir", "move_path":
		out := []string{}
		if p := str("path"); p != "" {
			out = append(out, p)
		}
		if name == "move_path" {
			if p := str("from"); p != "" {
				out = append(out, p)
			}
			if p := str("to"); p != "" {
				out = append(out, p)
			}
		}
		return out
	case "apply_patch":
		var out []string
		for _, line := range strings.Split(str("patch"), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "+++ ") {
				p := cleanPatchPath(strings.TrimPrefix(line, "+++ "))
				if p != "" && p != "/dev/null" {
					out = append(out, p)
				}
			}
			if len(out) >= 6 {
				break
			}
		}
		return out
	}
	return nil
}

// firstErrorLine اولین خط «ERROR:» یک نتیجهٔ ابزار را (کران‌دار) برمی‌گرداند.
func firstErrorLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ERROR:") {
			return cutUTF8(line, compactFactTextLimit)
		}
	}
	return ""
}

// pinnedFactsFrom واقعیت‌های سنجاق‌شدهٔ یک بازهٔ حذف‌شده را استخراج می‌کند:
// فایل‌های تحت اثر، دستورهای راستی‌آزمایی و خطاهای ابزار. ترتیب ورودی حفظ
// می‌شود (قدیمی → جدید) و تکراری‌ها فقط یک‌بار می‌آیند؛ سقف دارد تا خودِ
// نشانگر بافت را نخورد.
func pinnedFactsFrom(msgs []chatMessage, limit int) []string {
	if limit <= 0 {
		limit = compactFactsLimit
	}
	var out []string
	seen := map[string]bool{}
	add := func(kind, val string) {
		if val == "" || len(out) >= limit {
			return
		}
		key := kind + "|" + val
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, kind+": "+val)
	}
	errCount := 0
	for _, m := range msgs {
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				for _, p := range toolPathsForFacts(tc.Function.Name, tc.Function.Arguments) {
					add("file touched", p)
				}
				if tc.Function.Name == "run_command" {
					var args map[string]interface{}
					if json.Unmarshal([]byte(tc.Function.Arguments), &args) == nil {
						if c, _ := args["command"].(string); strings.TrimSpace(c) != "" && isVerificationCommand(c) {
							add("verification ran", cutUTF8(strings.TrimSpace(c), compactFactTextLimit))
						}
					}
				}
			}
			continue
		}
		if m.Role == "tool" && errCount < compactOpenErrorsLimit {
			if e := firstErrorLine(historyContentString(m.Content)); e != "" {
				errCount++
				add("open error", strings.TrimSpace(strings.TrimPrefix(e, "ERROR:")))
			}
		}
	}
	return out
}

// compactTranscript متن کوتاه و کران‌دارِ پیام‌های حذف‌شده برای خلاصه‌ساز.
func compactTranscript(msgs []chatMessage, limit int) string {
	if limit <= 0 {
		limit = compactSummaryInLimit
	}
	var b strings.Builder
	for _, m := range msgs {
		role := m.Role
		text := historyContentString(m.Content)
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var names []string
			for _, tc := range m.ToolCalls {
				names = append(names, tc.Function.Name)
			}
			text = "[called " + strings.Join(names, ", ") + "] " + text
		}
		line := role + ": " + cutUTF8(strings.Join(strings.Fields(text), " "), 600)
		if b.Len()+len(line)+1 > limit {
			b.WriteString("\n…[truncated]")
			break
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// oneShotChat یک درخواست chat/completions غیراستریمی با تنظیمات فعلی
// Provider/مدل می‌فرستد و متن پاسخ را برمی‌گرداند. برای کارهای کمکی داخلی
// (خلاصه‌سازی) استفاده می‌شود؛ هیچ ابزاری نمی‌فرستد.
func (a *App) oneShotChat(ctx context.Context, system, user string, maxTokens int) (string, error) {
	a.mu.Lock()
	provider := a.findProvider(a.settings.Provider)
	model := a.settings.Model
	var baseURL, key string
	if provider != nil {
		baseURL = provider.BaseURL
		if model == "" && len(provider.Models) > 0 {
			model = provider.Models[0]
		}
		if !provider.NoKey {
			key = a.decryptKey(provider.ID)
		}
	}
	a.mu.Unlock()
	if provider == nil || strings.TrimSpace(baseURL) == "" || strings.TrimSpace(model) == "" {
		return "", fmt.Errorf("no provider/model configured")
	}
	if key == "" && !provider.NoKey {
		return "", fmt.Errorf("provider key is missing")
	}

	body, err := json.Marshal(map[string]interface{}{
		"model": model,
		"messages": []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		"stream":     false,
		"max_tokens": maxTokens,
	})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, compactSummaryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := chatHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("summarizer HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("summarizer returned no choices")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

// summarizeDroppedMessages بهترین-تلاش: خلاصهٔ مدل از پیام‌های حذف‌شده.
// خطا/مهلت/پاسخ خالی → "" (فقط واقعیت‌های سنجاق‌شده می‌مانند).
func (a *App) summarizeDroppedMessages(ctx context.Context, msgs []chatMessage, pinned []string) string {
	if len(msgs) == 0 {
		return ""
	}
	transcript := compactTranscript(msgs, compactSummaryInLimit)
	if strings.TrimSpace(transcript) == "" {
		return ""
	}
	system := "You compress conversation history for an AI coding agent that is still working on the same task. " +
		"Write a dense factual digest of the removed part so the agent keeps working without re-reading it. " +
		"Preserve: decisions taken and why, exact file paths, identifiers/function names, what was verified and its result, " +
		"open errors and unresolved questions, and the next step that was planned. " +
		"Never invent anything and do not add advice. Output plain English bullets, at most 1200 characters."
	var b strings.Builder
	if len(pinned) > 0 {
		b.WriteString("Pinned facts extracted from the removed part (keep them all):\n")
		for _, p := range pinned {
			b.WriteString("- " + p + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("Removed conversation part:\n")
	b.WriteString(transcript)
	out, err := a.oneShotChat(ctx, system, b.String(), 700)
	if err != nil || strings.TrimSpace(out) == "" {
		if err != nil {
			logAppErrorFor(a, "context", "summarizer failed: "+err.Error())
		}
		return ""
	}
	return cutUTF8(strings.TrimSpace(out), compactSummaryMaxOut)
}
