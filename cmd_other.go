//go:build !windows

package main

import "os/exec"

// configureShell در سیستم‌های غیر ویندوزی کاری لازم ندارد.
func configureShell(cmd *exec.Cmd, command string) {}
