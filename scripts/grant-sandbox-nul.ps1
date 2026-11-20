# دسترسی دستگاه NUL برای SID سندباکس KodYar
# ----------------------------------------
# توکن AppContainer به \Device\Null دسترسی ندارد و همین باعث می‌شود
# `go test` / `go vet` / `git` داخل سندباکس با «open NUL: Access is denied»
# شکست بخورند. این اسکریپت DACL دستگاه NUL را با یک ACE برای SID سندباکس
# به‌روز می‌کند (Everyone:GA حفظ می‌شود).
#
# تأیید واقعی: پس از اجرا، این تست‌ها را بزنید (داخل AppContainer):
#   go test -run 'TestSandbox' -v .
# و در خودِ اپ یک دستور ساده مثل `go version` و `go vet ./...` را در حالت
# سندباکس اجرا کنید.
#
# نکته: شیء دستگاه NUL در حافظه‌ی کرنل است و با هر ری‌استارت بازنشانی
# می‌شود؛ این اسکریپت را بعد از هر بوت (یا با Task Scheduler در Startup)
# اجرا کنید. برای تغییر DACL یک شیء کرنل معمولاً به Administrator نیاز است.
#
# اجرا:  powershell -ExecutionPolicy Bypass -File scripts\grant-sandbox-nul.ps1
param([string]$ProfileName = "KodYar.Sandbox.v1")

$ErrorActionPreference = "Stop"

Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;

public static class NulAcl {
    [DllImport("userenv.dll", CharSet = CharSet.Unicode)]
    public static extern int DeriveAppContainerSidFromAppContainerName(string name, out IntPtr sid);

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern bool ConvertStringSecurityDescriptorToSecurityDescriptor(
        string sddl, uint revision, out IntPtr sd, out uint size);

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode)]
    public static extern int ConvertSidToStringSid(IntPtr sid, out IntPtr str);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern IntPtr CreateFileW(string name, uint access, uint share,
        IntPtr sa, uint disposition, uint flags, IntPtr template);

    [DllImport("advapi32.dll", SetLastError = true)]
    public static extern bool SetKernelObjectSecurity(IntPtr handle,
        uint securityInformation, IntPtr securityDescriptor);

    [DllImport("kernel32.dll")]
    public static extern IntPtr LocalFree(IntPtr p);

    [DllImport("kernel32.dll")]
    public static extern bool CloseHandle(IntPtr h);
}
"@

$sidPtr = [IntPtr]::Zero
$hr = [NulAcl]::DeriveAppContainerSidFromAppContainerName($ProfileName, [ref]$sidPtr)
if ($hr -ne 0) {
    throw ("DeriveAppContainerSidFromAppContainerName failed: 0x{0:X8}" -f $hr)
}
$strPtr = [IntPtr]::Zero
[NulAcl]::ConvertSidToStringSid($sidPtr, [ref]$strPtr) | Out-Null
$sid = [Runtime.InteropServices.Marshal]::PtrToStringUni($strPtr)
Write-Host "Sandbox SID: $sid"

# DACL: Everyone GA (پیش‌فرض NUL) + SID سندباکس GA
$sddl = "D:(A;;GA;;;WD)(A;;GA;;;$sid)"
$sd = [IntPtr]::Zero
$sdSize = 0
if (-not [NulAcl]::ConvertStringSecurityDescriptorToSecurityDescriptor(
        $sddl, 1, [ref]$sd, [ref]$sdSize)) {
    throw ("SDDL -> security descriptor failed: {0}" -f
        [Runtime.InteropServices.Marshal]::GetLastWin32Error())
}

$access = [uint32]3221487616  # 0xC0040000 = GENERIC_READ|GENERIC_WRITE|WRITE_DAC
$handle = [NulAcl]::CreateFileW("NUL", $access, 3, [IntPtr]::Zero, 3, 0, [IntPtr]::Zero)
if ($handle -eq [IntPtr]::new(-1)) {
    $code = [Runtime.InteropServices.Marshal]::GetLastWin32Error()
    [NulAcl]::LocalFree($sd) | Out-Null
    throw ("CreateFile(NUL) failed: {0} — با دسترسی Administrator اجرا کنید" -f $code)
}

$DACL_SECURITY_INFORMATION = 4
$ok = [NulAcl]::SetKernelObjectSecurity($handle, $DACL_SECURITY_INFORMATION, $sd)
$err = [Runtime.InteropServices.Marshal]::GetLastWin32Error()
[NulAcl]::CloseHandle($handle) | Out-Null
[NulAcl]::LocalFree($sd) | Out-Null

if (-not $ok) {
    throw ("SetKernelObjectSecurity(NUL) failed: {0} — با دسترسی Administrator اجرا کنید" -f $err)
}
Write-Host "OK: NUL DACL updated for the sandbox SID ($sid)"
Write-Host "Verify: go test -run 'TestSandbox' -v ."
