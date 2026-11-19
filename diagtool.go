// MyAI — ابزار فقط‌خواندنی `diagnostics`: خطاهای تایپ/کامپایل را از سرور
// زبان (gopls برای Go، typescript-language-server برای TS/JS) می‌گیرد تا
// ایجنت در همان دور بفهمد ویرایشش شکسته است یا نه — بدون ساختن/تست کردن.
//
// دامنهٔ عمداً کم:
//   - فقط خطاها (severity=Error) با شمارش هشدارها؛ بدون code action/fix.
//   - یک سرور به‌ازای هر زبان (start-once، kill در shutdown) — lsp.go.
//   - اگر سرور زبان نصب نباشد، پیام راهنمای نصب برمی‌گردد و ایجنت به
//     build/test برمی‌گردد (پرامپت همین را می‌گوید).
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	diagToolName = "diagnostics"

	// سقف‌های ابزار — خروجی کوچک و پیش‌بینی‌پذیر بمانَد.
	maxDiagFiles     = 10
	maxDiagFileBytes = 512 * 1024
	maxDiagMessage   = 300
	diagWaitPerFile  = 15 * time.Second
	diagTotalBudget  = 45 * time.Second
)

// diagLangFor زبان سرور لازم برای یک فایل را برمی‌گرداند ("" = پشتیبانی نشده).
func diagLangFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		return "ts"
	}
	return ""
}

// diagLanguageID شناسهٔ زبان LSP برای didOpen.
func diagLanguageID(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".mts", ".cts":
		return "typescript"
	}
	return "plaintext"
}

// collectDiagTargets فایل‌های قابل‌بررسی زیر یک مسیر را برمی‌گرداند:
// اگر مسیر فایل باشد فقط خودش، اگر پوشه باشد پیمایش تا سقف maxDiagFiles.
func collectDiagTargets(abs string, isDir bool) []string {
	if !isDir {
		if diagLangFor(abs) == "" {
			return nil
		}
		return []string{abs}
	}
	var out []string
	_ = filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(out) >= maxDiagFiles {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if path == abs {
				return nil
			}
			name := d.Name()
			if searchSkipDirs[name] || name == "node_modules" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if diagLangFor(path) == "" {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	sort.Strings(out)
	return out
}

// formatDiagLine یک تشخیص را به «path:line:col: message (source)» تبدیل می‌کند
// (شماره‌ها ۱-محور، مثل بقیهٔ ابزارها).
func formatDiagLine(rel string, d lspDiagnostic) string {
	msg := strings.TrimSpace(strings.ReplaceAll(d.Message, "\n", " "))
	msg = cutUTF8(msg, maxDiagMessage)
	line := fmt.Sprintf("%s:%d:%d: %s", rel, d.Range.Start.Line+1, d.Range.Start.Character+1, msg)
	if d.Source != "" {
		line += " (" + d.Source + ")"
	}
	return line
}

// toolDiagnostics اجرای ابزار: مسیر اختیاری (فایل یا پوشه، نسبی به workspace).
func (a *App) toolDiagnostics(ctx context.Context, rel string) (string, bool) {
	a.mu.Lock()
	ws := strings.TrimSpace(a.settings.WorkspacePath)
	a.mu.Unlock()
	if ws == "" {
		return "ERROR: workspace directory is not configured.", false
	}

	target, err := a.resolveWorkspacePath(rel)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	info, err := os.Stat(target)
	if err != nil {
		return "ERROR: " + err.Error(), false
	}
	files := collectDiagTargets(target, info.IsDir())
	if len(files) == 0 {
		return "No Go or TypeScript/JavaScript files found at " + relDisplay(ws, target) + ".", true
	}

	deadline := time.Now().Add(diagTotalBudget)
	var b strings.Builder
	ran := false
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return b.String() + "\n(cancelled)", ran
		}
		if time.Now().After(deadline) {
			b.WriteString("(stopped: diagnostics time budget exhausted)\n")
			break
		}
		relPath := relDisplay(ws, f)
		lang := diagLangFor(f)
		client, cerr := diagMgr.get(lang, ws)
		if cerr != nil {
			return b.String() + fmt.Sprintf("%s: %v", relPath, cerr), ran
		}
		text, rerr := readFileTextCapped(f, maxDiagFileBytes)
		if rerr != nil {
			b.WriteString(fmt.Sprintf("%s: cannot read file (%v)\n", relPath, rerr))
			continue
		}
		diags, derr := client.openAndWait(fileURI(f), diagLanguageID(f), text, diagWaitPerFile)
		if derr != nil {
			b.WriteString(fmt.Sprintf("%s: %v\n", relPath, derr))
			continue
		}
		ran = true
		errorsFound := 0
		warnings := 0
		for _, d := range diags {
			switch d.Severity {
			case 1:
				errorsFound++
				b.WriteString(formatDiagLine(relPath, d) + "\n")
			case 2:
				warnings++
			}
		}
		if errorsFound == 0 {
			b.WriteString(fmt.Sprintf("%s: no errors", relPath))
			if warnings > 0 {
				b.WriteString(fmt.Sprintf(" (%d warning(s))", warnings))
			}
			b.WriteString("\n")
		}
	}
	out := strings.TrimRight(b.String(), "\n")
	if !ran && out == "" {
		return "No language server produced diagnostics (see the messages above).", false
	}
	return out, true
}

// readFileTextCapped فایل را تا سقف می‌خواند و باینری‌ها را رد می‌کند.
func readFileTextCapped(path string, limit int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, limit)
	n, _ := f.Read(buf)
	data := buf[:n]
	if looksBinary(data) {
		return "", fmt.Errorf("binary file")
	}
	return string(data), nil
}
