// KodYar — مهارت‌ها (Skills): گردآوری فایل‌های SKILL.md پروژه و تزریق یک
// فهرست فشرده به system prompt.
//
// چرا این‌طور: پرامپت سیستمی باید کوتاه بماند ولی مدل باید بداند چه
// workflowهایی در پروژه تعریف شده‌اند. راه‌حل همان «افشای تدریجی» است:
// فقط name/توضیح یک‌خطی/مسیر به پرامپت می‌رود و متن کامل مهارت تنها وقتی
// خوانده می‌شود که مدل با read_file همان مسیر را باز کند — بدون ابزار
// جدید، بدون کپی محتوای سنگین در هر نوبت.
//
// ساختار روی دیسک (هر ریشه، یک پوشه برای هر مهارت):
//
//	<root>/<skill-name>/SKILL.md
//
// ریشه‌ها به ترتیب اولویت بررسی می‌شوند: .kodyar/skills، .claude/skills،
// .agents/skills، skills. نام تکراری از ریشه‌ی با اولویت بالاتر برنده است.
package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxSkills           = 40       // حداکثر مهارت در فهرست
	maxSkillDescRunes   = 180      // سقف طول توضیح یک‌خطی
	maxSkillsBlockBytes = 6 * 1024 // سقف حجم کل بلوک تزریق‌شده
	maxSkillMetaBytes   = 8 * 1024 // سقف خواندن فایل برای فراداده
	maxSkillLineBytes   = 400      // سقف هر خط فهرست
)

// skillRoots ریشه‌های جست‌وجوی مهارت‌ها، به ترتیب اولویت.
var skillRoots = []string{
	filepath.Join(".kodyar", "skills"),
	filepath.Join(".claude", "skills"),
	filepath.Join(".agents", "skills"),
	"skills",
	// سازگاری با پروژه‌های قدیمی‌تر (نام پوشه پیش از تغییر برند).
	filepath.Join(legacyAppDirName, "skills"),
}

// skillFileNames نام‌های پذیرفته‌شده‌ی فایل مهارت در پوشه‌ی هر مهارت.
var skillFileNames = []string{"SKILL.md", "skill.md", "Skill.md"}

// SkillInfo یک مهارت کشف‌شده برای فهرست پرامپت.
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"` // مسیر نسبی با اسلش
}

// parseSkillMeta فراداده‌ی فایل مهارت را استخراج می‌کند.
// اگر frontmatter YAML ساده (--- / name / description / ---) باشد از آن،
// وگرنه نام از اولین سرتیتر و توضیح از اولین خط متنی گرفته می‌شود.
// عمداً هیچ وابستگی YAML اضافه نشده: فقط کلیدهای تک‌خطی لازم است.
func parseSkillMeta(text string) (name, desc string) {
	body := text
	if strings.HasPrefix(text, "---") {
		if nl := strings.IndexByte(text, '\n'); nl >= 0 {
			after := text[nl+1:]
			if end := strings.Index(after, "\n---"); end >= 0 {
				for _, line := range strings.Split(after[:end], "\n") {
					k, v, ok := strings.Cut(line, ":")
					if !ok {
						continue
					}
					key := strings.ToLower(strings.TrimSpace(k))
					val := strings.Trim(strings.TrimSpace(v), "\"'")
					switch key {
					case "name":
						if name == "" {
							name = val
						}
					case "description":
						if desc == "" {
							desc = val
						}
					}
				}
				body = after[end+4:]
			}
		}
	}
	if name == "" {
		name = firstSkillHeading(body)
	}
	if desc == "" {
		desc = firstSkillText(body)
	}
	return name, desc
}

// firstSkillHeading اولین سرتیتر markdown را برمی‌گرداند.
func firstSkillHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			return strings.TrimSpace(strings.TrimLeft(t, "#"))
		}
	}
	return ""
}

// firstSkillText اولین خط متنی غیرسرتیتر/غیرکد را برمی‌گرداند.
func firstSkillText(body string) string {
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence || t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "---") {
			continue
		}
		return t
	}
	return ""
}

// clampSkillDesc توضیح را به سقف rune برش می‌دهد (فارسی چندبایتی است).
func clampSkillDesc(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) > maxSkillDescRunes {
		return strings.TrimSpace(string(r[:maxSkillDescRunes])) + "…"
	}
	return s
}

// findSkillFile فایل مهارت را در پوشه‌ی یک مهارت پیدا می‌کند.
func findSkillFile(dir string) (string, bool) {
	for _, name := range skillFileNames {
		full := filepath.Join(dir, name)
		if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
			return full, true
		}
	}
	return "", false
}

// readSkillMeta فایل مهارت را (تا سقف) می‌خواند و فراداده‌اش را برمی‌گرداند.
func readSkillMeta(path string) (string, string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	data, _ := io.ReadAll(io.LimitReader(f, maxSkillMetaBytes))
	if len(data) == 0 {
		return "", ""
	}
	name, desc := parseSkillMeta(string(data))
	return clampSkillDesc(name), clampSkillDesc(desc)
}

// discoverSkillsLocked مهارت‌های workspace را برمی‌گرداند.
// باید با a.mu قفل‌شده صدا زده شود (مثل بقیه‌ی سازندگان بلوک پرامپت).
// خطاها بی‌صدا رد می‌شوند: نبودِ پوشه‌ی skills حالت عادی است.
func (a *App) discoverSkillsLocked() []SkillInfo {
	if a.settings.SkillsDisabled {
		return nil
	}
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	if ws == "" {
		return nil
	}
	cleanWs := filepath.Clean(ws)
	seen := map[string]bool{}
	out := make([]SkillInfo, 0, 8)

	for _, rel := range skillRoots {
		root := filepath.Clean(filepath.Join(cleanWs, rel))
		if !withinDir(root, cleanWs) {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			file, ok := findSkillFile(filepath.Join(root, e.Name()))
			if !ok {
				continue
			}
			// حفاظت symlink/خروج از workspace — همان قرارداد بقیه‌ی فایل‌ها.
			realWs := evalExisting(cleanWs)
			realFile := evalExisting(file)
			if realWs == "" || realFile == "" || !withinDir(realFile, realWs) {
				continue
			}
			name, desc := readSkillMeta(file)
			if name == "" {
				name = e.Name()
			}
			key := strings.ToLower(name)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, SkillInfo{
				Name:        name,
				Description: desc,
				Path:        filepath.ToSlash(relDisplay(cleanWs, file)),
			})
			if len(out) >= maxSkills {
				return out
			}
		}
	}
	return out
}

// skillsPromptBlockLocked بلوک <available_skills> برای system prompt.
// خروجی خالی یعنی مهارتی وجود ندارد (هزینه‌ی صفر روی پرامپت).
func (a *App) skillsPromptBlockLocked() string {
	skills := a.discoverSkillsLocked()
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<available_skills>\n")
	b.WriteString("Reusable workflows defined for this project. Each entry is a SKILL.md file:\n")
	b.WriteString("when the user's task matches one of them, FIRST read that file with read_file and follow its steps exactly; ")
	b.WriteString("if none matches, ignore this list. Never load a skill you will not use, and never edit these files unless asked.\n")
	for _, s := range skills {
		line := "- " + s.Name
		if s.Description != "" {
			line += " — " + s.Description
		}
		line += " (" + s.Path + ")\n"
		line = cutUTF8(line, maxSkillLineBytes)
		if b.Len()+len(line) > maxSkillsBlockBytes {
			break
		}
		b.WriteString(line)
	}
	b.WriteString("</available_skills>")
	return b.String()
}
