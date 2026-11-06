//go:build !windows

package main

import "os/exec"

// hideConsoleWindow روی سیستم‌های غیر ویندوزی عملی ندارد — آن‌ها پنجره‌ی
// کنسول ندارند. برای هم‌آوردی امضای نسخه‌ی ویندوزی نگه داشته می‌شود.
func hideConsoleWindow(cmd *exec.Cmd) { _ = cmd }
