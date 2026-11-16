//go:build windows

// اجرای سرور MCP داخل AppContainer با stdio دوطرفه.
//
// چرا: سرور MCP کدِ خارجی است؛ اگر بیرون از سندباکس اجرا شود، با دسترسی
// کامل کاربر بالا می‌آید. این فایل همان قواعد سندباکس دستورها را برای یک
// فرایند بلندمدت به کار می‌برد: SID اختصاصی AppContainer، ACL محدود (پوشه‌ی
// کاری + خانه‌ی ایزوله)، Job Object با KILL_ON_JOB_CLOSE، محیط حداقلی و
// شبکه فقط با capability internetClient. stdin/stdout/stderr سه pipe واقعی
// هستند تا JSON-RPC خط‌به‌خط مثل حالت عادی کار کند.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// sandboxMCPProcess فرایند MCP داخل AppContainer.
type sandboxMCPProcess struct {
	stdin  *os.File
	stdout *os.File
	stderr *os.File

	proc windows.Handle
	job  windows.Handle

	closeOnce sync.Once
}

// sandboxMCPEnvironment محیط سرور MCP را با overrides سندباکس می‌سازد
// (TEMP/HOME/LOCALAPPDATA ایزوله و MYAI_SANDBOX=1).
func sandboxMCPEnvironment(env []string, home string, allowNetwork bool) []uint16 {
	netFlag := "1"
	if !allowNetwork {
		netFlag = "0"
	}
	merged := map[string]string{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			merged[strings.ToUpper(kv[:i])] = kv[i+1:]
		}
	}
	overrides := map[string]string{
		"TEMP":                 filepath.Join(home, "tmp"),
		"TMP":                  filepath.Join(home, "tmp"),
		"TMPDIR":               filepath.Join(home, "tmp"),
		"HOME":                 home,
		"USERPROFILE":          home,
		"LOCALAPPDATA":         filepath.Join(home, "AppData", "Local"),
		"APPDATA":              filepath.Join(home, "AppData", "Roaming"),
		"GOPATH":               filepath.Join(home, "go"),
		"GOCACHE":              filepath.Join(home, "go", "cache"),
		"npm_config_cache":     filepath.Join(home, "npm-cache"),
		"MYAI_SANDBOX":         "1",
		"MYAI_SANDBOX_NETWORK": netFlag,
	}
	for k, v := range overrides {
		merged[strings.ToUpper(k)] = v
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+merged[k])
	}
	// StringToUTF16 با NUL داخلی panic می‌کند؛ بلوک env عمداً NUL دارد.
	block := strings.Join(parts, "\x00") + "\x00"
	return append(utf16.Encode([]rune(block)), 0)
}

// pipeEnds یک pipe می‌سازد و سرِ فرزند را inheritable و سرِ والد را غیرِ آن
// برمی‌گرداند؛ stdin با childRead=true، stdout/stderr با false.
func pipeEnds(childRead bool) (child, parent windows.Handle, err error) {
	sa := &windows.SecurityAttributes{
		Length:        uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		InheritHandle: 1,
	}
	var r, w windows.Handle
	if err := windows.CreatePipe(&r, &w, sa, 0); err != nil {
		return 0, 0, err
	}
	if childRead {
		if err := windows.SetHandleInformation(w, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
			windows.CloseHandle(r)
			windows.CloseHandle(w)
			return 0, 0, err
		}
		return r, w, nil
	}
	if err := windows.SetHandleInformation(r, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		windows.CloseHandle(r)
		windows.CloseHandle(w)
		return 0, 0, err
	}
	return w, r, nil
}

// startSandboxedMCPProcess فرایند را در AppContainer با سه pipe اجرا می‌کند.
// شکست در هر مرحله = فرایند اجرا نمی‌شود (fail-closed).
func startSandboxedMCPProcess(exe string, args []string, dir string, env []string, allowNetwork bool) (*sandboxMCPProcess, error) {
	sidStr, sid, err := sandboxIdentity()
	if err != nil {
		return nil, fmt.Errorf("sandbox unavailable: %w", err)
	}
	home, err := sandboxHome()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(dir) == "" {
		dir = home
	}
	if err := sandboxGrantACE(sidStr, home, "F", true); err != nil {
		return nil, err
	}
	if err := sandboxGrantACE(sidStr, dir, "F", true); err != nil {
		return nil, err
	}
	sandboxGrantTraverse(sidStr, home)
	sandboxGrantTraverse(sidStr, dir)
	sandboxEnsureToolAccess(sidStr)

	appName, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return nil, err
	}
	lineBuf, err := windows.UTF16FromString(composeWindowsCommandLine(append([]string{exe}, args...)))
	if err != nil {
		return nil, err
	}
	dirBuf, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return nil, err
	}
	envBlock := sandboxMCPEnvironment(env, home, allowNetwork)

	stdinChild, stdinParent, err := pipeEnds(true)
	if err != nil {
		return nil, fmt.Errorf("sandbox mcp stdin: %w", err)
	}
	stdoutChild, stdoutParent, err := pipeEnds(false)
	if err != nil {
		windows.CloseHandle(stdinChild)
		windows.CloseHandle(stdinParent)
		return nil, fmt.Errorf("sandbox mcp stdout: %w", err)
	}
	stderrChild, stderrParent, err := pipeEnds(false)
	if err != nil {
		for _, h := range []windows.Handle{stdinChild, stdinParent, stdoutChild, stdoutParent} {
			windows.CloseHandle(h)
		}
		return nil, fmt.Errorf("sandbox mcp stderr: %w", err)
	}
	cleanup := func() {
		for _, h := range []windows.Handle{stdinChild, stdinParent, stdoutChild, stdoutParent, stderrChild, stderrParent} {
			windows.CloseHandle(h)
		}
	}

	list, listBuf, err := newAttributeList(2)
	if err != nil {
		cleanup()
		return nil, err
	}
	defer procDeleteAttr.Call(list)

	caps := make([]windows.SIDAndAttributes, 0, 1)
	if allowNetwork {
		if capSID, cerr := sidFromString(internetClientSID); cerr == nil {
			caps = append(caps, windows.SIDAndAttributes{Sid: capSID, Attributes: seGroupEnabled})
		}
	}
	var capPtr *windows.SIDAndAttributes
	if len(caps) > 0 {
		capPtr = &caps[0]
	}
	secCaps := securityCapabilities{AppContainerSid: sid, Capabilities: capPtr, CapabilityCount: uint32(len(caps))}
	if r, _, e := procUpdateAttr.Call(list, 0, attrSecurityCapabilities,
		uintptr(unsafe.Pointer(&secCaps)), unsafe.Sizeof(secCaps), 0, 0); r == 0 {
		cleanup()
		return nil, fmt.Errorf("sandbox mcp: security capabilities attribute: %v", e)
	}
	handles := []windows.Handle{stdinChild, stdoutChild, stderrChild}
	if r, _, e := procUpdateAttr.Call(list, 0, attrHandleList,
		uintptr(unsafe.Pointer(&handles[0])), uintptr(len(handles))*unsafe.Sizeof(handles[0]), 0, 0); r == 0 {
		cleanup()
		return nil, fmt.Errorf("sandbox mcp: handle list attribute: %v", e)
	}

	si := startupInfoEx{}
	si.StartupInfo.Cb = uint32(unsafe.Sizeof(si))
	si.StartupInfo.Flags = windows.STARTF_USESTDHANDLES
	si.StartupInfo.StdInput = stdinChild
	si.StartupInfo.StdOutput = stdoutChild
	si.StartupInfo.StdErr = stderrChild
	si.AttributeList = list

	var pi windows.ProcessInformation
	flags := uint32(flagUnicodeEnvironment | flagExtendedStartupInfo | flagSuspended | flagNoWindow)
	if err := windows.CreateProcess(appName, &lineBuf[0], nil, nil, true, flags,
		&envBlock[0], dirBuf, (*windows.StartupInfo)(unsafe.Pointer(&si)), &pi); err != nil {
		cleanup()
		return nil, fmt.Errorf("sandbox mcp: create process: %w", err)
	}
	runtimeKeepAlive(listBuf, secCaps, caps, handles, envBlock, lineBuf)
	windows.CloseHandle(stdinChild)
	windows.CloseHandle(stdoutChild)
	windows.CloseHandle(stderrChild)

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		for _, h := range []windows.Handle{stdinParent, stdoutParent, stderrParent, pi.Process, pi.Thread} {
			windows.CloseHandle(h)
		}
		return nil, fmt.Errorf("sandbox mcp: job object: %w", err)
	}
	var jobInfo windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	jobInfo.BasicLimitInformation.LimitFlags = jobLimitKillOnClose | jobLimitActiveProcess
	jobInfo.BasicLimitInformation.ActiveProcessLimit = sandboxMaxProcesses
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&jobInfo)), uint32(unsafe.Sizeof(jobInfo))); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(job)
		for _, h := range []windows.Handle{stdinParent, stdoutParent, stderrParent, pi.Process, pi.Thread} {
			windows.CloseHandle(h)
		}
		return nil, fmt.Errorf("sandbox mcp: job limits: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, pi.Process); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(job)
		for _, h := range []windows.Handle{stdinParent, stdoutParent, stderrParent, pi.Process, pi.Thread} {
			windows.CloseHandle(h)
		}
		return nil, fmt.Errorf("sandbox mcp: assign job: %w", err)
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(job)
		for _, h := range []windows.Handle{stdinParent, stdoutParent, stderrParent, pi.Process, pi.Thread} {
			windows.CloseHandle(h)
		}
		return nil, fmt.Errorf("sandbox mcp: resume: %w", err)
	}
	windows.CloseHandle(pi.Thread)

	return &sandboxMCPProcess{
		stdin:  os.NewFile(uintptr(stdinParent), "mcp-stdin"),
		stdout: os.NewFile(uintptr(stdoutParent), "mcp-stdout"),
		stderr: os.NewFile(uintptr(stderrParent), "mcp-stderr"),
		proc:   pi.Process,
		job:    job,
	}, nil
}

// terminate کل درخت را می‌کشد (job با KILL_ON_JOB_CLOSE).
func (p *sandboxMCPProcess) terminate() {
	if p == nil || p.job == 0 {
		return
	}
	_ = windows.TerminateJobObject(p.job, 1)
}

// wait منتظر خروج فرایند می‌ماند و منابع را آزاد می‌کند.
func (p *sandboxMCPProcess) wait() error {
	if p == nil {
		return nil
	}
	_, _ = windows.WaitForSingleObject(p.proc, windows.INFINITE)
	var code uint32 = 1
	_ = windows.GetExitCodeProcess(p.proc, &code)
	p.close()
	if code != 0 {
		return fmt.Errorf("exit status %d", code)
	}
	return nil
}

// close هندل‌ها و فایل‌ها را یک‌بار می‌بندد.
func (p *sandboxMCPProcess) close() {
	if p == nil {
		return
	}
	p.closeOnce.Do(func() {
		for _, f := range []*os.File{p.stdin, p.stdout, p.stderr} {
			if f != nil {
				_ = f.Close()
			}
		}
		if p.proc != 0 {
			_ = windows.CloseHandle(p.proc)
		}
		if p.job != 0 {
			_ = windows.CloseHandle(p.job)
		}
	})
}
