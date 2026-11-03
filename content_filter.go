package main

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// orphanCloseTagRe تگ بستهٔ یتیم (بدون تگ باز متناظر) — مثل «</tool_call>»
// تنها — را انتخاب می‌کند تا از محتوای قابل‌نمایش حذف شود. جفت‌های سالم
// پیش از این توسط stripBalancedCI به‌طور کامل حذف شده‌اند.
var orphanCloseTagRe = regexp.MustCompile(`(?i)</\s*(?:tool_call|function_call)\s*>`)

func stripToolCallArtifacts(s string) string {
	if s == "" {
		return s
	}
	low := strings.ToLower(s)
	hasTool := strings.Contains(s, "TOOL_CALL") || strings.Contains(low, "tool_call") || strings.Contains(low, "function_call")
	hasOrphanThink := strings.Contains(low, "think>") || strings.Contains(low, "thinking>")
	if !hasTool && !hasOrphanThink {
		return s
	}
	out := s
	if hasTool {
		out = stripBalanced(out, "[TOOL_CALL]", "[/TOOL_CALL]")
		out = stripBalanced(out, "[TOOL_CALL]", "[TOOL_CALL]")
		out = stripBalancedCI(out, "<tool_call", "</tool_call>")
		out = stripBalancedCI(out, "<function_call", "</function_call>")
		out = stripBalanced(out, "<|tool_call_begin|>", "<|tool_call_end|>")
		// تگ بستهٔ یتیم (بدون تگ باز) — مثل «</tool_call>» تنها
		out = orphanCloseTagRe.ReplaceAllString(out, "")
		// تگ باز باقی‌مانده تا انتهای متن
		if i := indexCI(out, "[TOOL_CALL]"); i >= 0 {
			out = out[:i]
		}
		if i := indexCI(out, "<tool_call"); i >= 0 {
			out = out[:i]
		}
		if i := indexCI(out, "<|tool_call_begin|>"); i >= 0 {
			out = out[:i]
		}
		out = stripIDToolMarkers(out)
	}
	out = stripOrphanThinkMarkers(out)
	return strings.TrimSpace(collapseBlankLines(out))
}

func indexCI(s, sub string) int {
	return strings.Index(strings.ToLower(s), strings.ToLower(sub))
}

func stripOrphanThinkMarkers(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, line := range strings.Split(s, "\n") {
		trim := strings.TrimSpace(line)
		low := strings.ToLower(trim)
		if strings.HasPrefix(low, "think>") {
			rest := strings.TrimSpace(trim[len("think>"):])
			if rest == "" {
				continue
			}
			line = rest
		} else if strings.HasPrefix(low, "thinking>") {
			rest := strings.TrimSpace(trim[len("thinking>"):])
			if rest == "" {
				continue
			}
			line = rest
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String()
}

func stripBalanced(s, open, close string) string {
	return stripBalancedOpt(s, open, close, false)
}

// stripBalancedOpt if dropOpen is true and close missing, removes from open to EOF.
func stripBalancedOpt(s, open, close string, dropOpen bool) string {
	for {
		i := strings.Index(s, open)
		if i < 0 {
			return s
		}
		rest := s[i+len(open):]
		j := strings.Index(rest, close)
		if j < 0 {
			if dropOpen {
				return s[:i]
			}
			return s
		}
		s = s[:i] + rest[j+len(close):]
	}
}

func stripBalancedCI(s, openPrefix, close string) string {
	for {
		low := strings.ToLower(s)
		openL := strings.ToLower(openPrefix)
		closeL := strings.ToLower(close)
		i := strings.Index(low, openL)
		if i < 0 {
			return s
		}
		gt := strings.IndexByte(s[i:], '>')
		startContent := i
		if gt >= 0 {
			startContent = i + gt + 1
		} else {
			return s[:i]
		}
		restLow := low[startContent:]
		j := strings.Index(restLow, closeL)
		if j < 0 {
			return s[:i]
		}
		end := startContent + j + len(close)
		if end < len(s) && s[end-1] != '>' {
			if k := strings.IndexByte(s[end:], '>'); k >= 0 {
				end = end + k + 1
			}
		}
		if end > len(s) {
			end = len(s)
		}
		s = s[:i] + s[end:]
	}
}

func stripIDToolMarkers(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '(' && i+4 <= len(s) && strings.HasPrefix(s[i:], "(id:") {
			j := i + 4
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			if j < len(s) && s[j] == ')' {
				k := j + 1
				if k < len(s) && s[k] == '?' {
					k++
				}
				for k < len(s) && (s[k] == ' ' || s[k] == '\t') {
					k++
				}
				up := strings.ToUpper(s[k:])
				if strings.HasPrefix(up, "[TOOL_CALL]") {
					i = k + len("[TOOL_CALL]")
					continue
				}
				if strings.HasPrefix(up, "[/TOOL_CALL]") {
					i = k + len("[/TOOL_CALL]")
					continue
				}
				i = k
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	out := strings.TrimSpace(b.String())
	out = strings.ReplaceAll(out, "md)", "")
	out = strings.ReplaceAll(out, "json)", "")
	out = strings.TrimSpace(out)
	for strings.Contains(out, " ?") || strings.HasPrefix(out, "? ") || out == "?" {
		out = strings.ReplaceAll(out, " ?", "")
		out = strings.TrimPrefix(out, "? ")
		out = strings.TrimPrefix(out, "?")
		out = strings.TrimSpace(out)
	}
	return out
}

func collapseBlankLines(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevEmpty := false
	for _, line := range strings.Split(s, "\n") {
		empty := strings.TrimSpace(line) == ""
		if empty && prevEmpty {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
		prevEmpty = empty
	}
	return b.String()
}

func splitThinkContent(s string) (visible, thinking string) {
	if s == "" {
		return "", ""
	}
	var vis, th strings.Builder
	rest := s
	for {
		openIdx, openLen := findThinkOpen(rest)
		if openIdx < 0 {
			vis.WriteString(rest)
			break
		}
		vis.WriteString(rest[:openIdx])
		rest = rest[openIdx+openLen:]
		closeIdx, closeLen := findThinkClose(rest)
		if closeIdx < 0 {
			th.WriteString(rest)
			rest = ""
			break
		}
		th.WriteString(rest[:closeIdx])
		rest = rest[closeIdx+closeLen:]
	}
	return strings.TrimSpace(vis.String()), strings.TrimSpace(th.String())
}

func findThinkOpen(s string) (idx, length int) {
	cands := []string{"<think>", "<thinking>", "<reason>", "<reasoning>"}
	best := -1
	bestLen := 0
	low := strings.ToLower(s)
	for _, t := range cands {
		i := strings.Index(low, t)
		if i >= 0 && (best < 0 || i < best) {
			best = i
			bestLen = len(t)
		}
	}
	if best < 0 {
		if i := strings.Index(low, "<think"); i >= 0 {
			end := strings.IndexByte(s[i:], '>')
			if end > 0 {
				return i, end + 1
			}
		}
	}
	return best, bestLen
}

func findThinkClose(s string) (idx, length int) {
	cands := []string{"</think>", "</thinking>", "</reason>", "</reasoning>"}
	best := -1
	bestLen := 0
	low := strings.ToLower(s)
	for _, t := range cands {
		i := strings.Index(low, t)
		if i >= 0 && (best < 0 || i < best) {
			best = i
			bestLen = len(t)
		}
	}
	return best, bestLen
}

func cleanAssistantContent(raw string) (visible, thinking string) {
	stripped := stripToolCallArtifacts(raw)
	return splitThinkContent(stripped)
}

func cutUTF8Safe(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max > len(s) {
		return s
	}
	i := max
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}
