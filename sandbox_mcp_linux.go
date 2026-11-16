//go:build linux

// سرور MCP داخل bubblewrap روی لینوکس: همان سیاست سندباکس دستورها
// (فایل‌سیستم فقط-خواندنی به‌جز پوشه‌ی کاری و خانه‌ی ایزوله، شبکه فقط با اجازه)
// با stdin/stdout/stderr واقعی تا JSON-RPC دوطرفه کار کند. نبودن bwrap خطاست
// (fail-closed) و سرور اجرا نمی‌شود.
package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// sandboxMCPProcess فرایند MCP داخل bwrap.
type sandboxMCPProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	home   string

	closeOnce sync.Once
}

func startSandboxedMCPProcess(exe string, args []string, dir string, env []string, allowNetwork bool) (*sandboxMCPProcess, error) {
	bwrap, err := sandboxLinuxBwrap()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(dir) == "" {
		dir = os.TempDir()
	}
	if st, serr := os.Stat(dir); serr != nil || !st.IsDir() {
		return nil, &os.PathError{Op: "sandbox mcp", Path: dir, Err: os.ErrNotExist}
	}
	home, err := os.MkdirTemp("", "myai-mcp-home-")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0o700); err != nil {
		os.RemoveAll(home)
		return nil, err
	}
	bargs := sandboxLinuxBwrapExecArgs(dir, home, allowNetwork, append([]string{exe}, args...),
		sandboxLinuxEnvVars(env, home, allowNetwork))

	cmd := exec.Command(bwrap, bargs...)
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		os.RemoveAll(home)
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		os.RemoveAll(home)
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		os.RemoveAll(home)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		os.RemoveAll(home)
		return nil, err
	}
	return &sandboxMCPProcess{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderrPipe, home: home}, nil
}

// terminate کل درخت را می‌کشد (bwrap با --die-with-parent فرزندانش را می‌برد).
func (p *sandboxMCPProcess) terminate() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Kill()
}

// wait منتظر خروج می‌ماند و منابع را آزاد می‌کند.
func (p *sandboxMCPProcess) wait() error {
	if p == nil {
		return nil
	}
	err := p.cmd.Wait()
	p.close()
	return err
}

// close لوله‌ها و خانه‌ی موقت را یک‌بار می‌بندد.
func (p *sandboxMCPProcess) close() {
	if p == nil {
		return
	}
	p.closeOnce.Do(func() {
		for _, c := range []io.Closer{p.stdin, p.stdout, p.stderr} {
			if c != nil {
				_ = c.Close()
			}
		}
		if p.home != "" {
			_ = os.RemoveAll(p.home)
		}
	})
}
