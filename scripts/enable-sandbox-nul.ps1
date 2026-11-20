# فعال‌سازی یک‌بارهٔ دسترسی \Device\Null برای سندباکس KodYar
# -------------------------------------------------------
# این اسکریپت با دسترسی Administrator اجرا می‌شود (از داخل خودِ برنامه با
# ShellExecute "runas" و یک‌بار تأیید UAC) و سه کار می‌کند:
#
#   ۱) ACE دستگاه NUL را برای SID سندباکس اعمال می‌کند (grant-sandbox-nul.ps1).
#   ۲) یک Scheduled Task سیستمی با /SC ONSTART و /RU SYSTEM می‌سازد تا همان
#      ACE در هر بوت، پیش از هر لاگون، خودکار و بی‌پنجره برگردد. kernel DACL
#      دستگاه NUL را با هر ری‌استارت بازنشانی می‌کند، پس این تکرار لازم است و
#      بدون آن go/git داخل AppContainer نمی‌توانند NUL را باز کنند.
#   ۳) مسیرهای قدیمی (Task کاربری ONLOGON و ورودی پوشه‌ی Startup) را پاک
#      می‌کند تا هیچ پنجره‌ی کنسولی هنگام ورود باز نشود.
#
# نتیجه در nul-grant.json (کنار همین اسکریپت) نوشته می‌شود تا برنامه بدون
# نیاز به دسترسی زیاد بتواند وضعیت را بخواند.
param(
    [Parameter(Mandatory = $true)][string]$Grant,
    [Parameter(Mandatory = $true)][string]$StateDir,
    [string]$TaskName = 'KodYar Sandbox NUL (System)',
    [string]$LegacyTask = 'KodYar Sandbox NUL',
    [string]$StartupDir = ''
)

$ErrorActionPreference = 'Continue'

function Write-State {
    param([bool]$Ok, [string]$Message)
    $obj = [ordered]@{
        ok    = $Ok
        at    = (Get-Date).ToUniversalTime().ToString('o')
        task  = $TaskName
        error = $Message
    }
    $path = Join-Path $StateDir 'nul-grant.json'
    try {
        ($obj | ConvertTo-Json -Compress) | Set-Content -LiteralPath $path -Encoding UTF8
    } catch {
        # نوشتن وضعیت نباید خودِ عملیات را خراب کند.
    }
}

if (-not (Test-Path -LiteralPath $Grant)) {
    Write-State -Ok $false -Message 'grant script not found'
    exit 2
}

# ۱) ACE دستگاه NUL برای SID سندباکس
$grantOut = & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $Grant 2>&1
$grantExit = $LASTEXITCODE

# ۲) Task سیستمی در هر بوت (پیش از لاگون) — بی‌پنجره
$taskCmd = 'powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "' + $Grant + '"'
$createOut = & schtasks.exe /Create /TN $TaskName /SC ONSTART /RU SYSTEM /RL HIGHEST /TR $taskCmd /F 2>&1
$createExit = $LASTEXITCODE
$queryExit = 0
& schtasks.exe /Query /TN $TaskName 2>&1 | Out-Null
$queryExit = $LASTEXITCODE

# ۳) پاک‌سازی مسیرهای قدیمی (Task کاربری و ورودی Startup)
& schtasks.exe /Delete /TN $LegacyTask /F 2>&1 | Out-Null
if ($StartupDir -and (Test-Path -LiteralPath $StartupDir)) {
    foreach ($name in @('KodYar-sandbox-nul.vbs', 'KodYar-sandbox-nul.cmd')) {
        $p = Join-Path $StartupDir $name
        if (Test-Path -LiteralPath $p) {
            Remove-Item -LiteralPath $p -Force -ErrorAction SilentlyContinue
        }
    }
}

if ($grantExit -ne 0 -or $createExit -ne 0 -or $queryExit -ne 0) {
    $msg = (($grantOut + $createOut) | Out-String).Trim()
    if ($msg.Length -gt 900) { $msg = $msg.Substring(0, 900) }
    Write-State -Ok $false -Message $msg
    exit 1
}

Write-State -Ok $true -Message ''
exit 0
