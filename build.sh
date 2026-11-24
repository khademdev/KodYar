#!/usr/bin/env bash
# ساخت بستهٔ ویندوزی KodYar (کامپایل متقاطع خالص Go — بدون نیاز به MinGW).
#
# توجه: این فایل در بایگانی zip پروژه وجود نداشت و از تاریخچهٔ git هم
# قابل بازیابی نبود؛ این نسخه بازسازی‌شده بر اساس دستور ساخت مستند در
# README.md است (wails build -platform windows/amd64 -skipbindings).
#
# پیش‌نیاز: Wails CLI v2 (هم‌نسخه با go.mod):
#   go install github.com/wailsapp/wails/v2/cmd/wails@v2.10.1
set -euo pipefail
cd "$(dirname "$0")"

# بسته همیشه windows/amd64 است. اگر این متغیر از محیط نیامده باشد (مثلاً روی
# ماشین ۳۸۶)، resourceی که goversioninfo می‌سازد با کامپایل هدف جور در نمی‌آید
# و لینکر خطای «unknown relocation type 7» می‌دهد — پس خود اسکریپت پین می‌کند.
export GOARCH=amd64

# نسخه از فایل VERSION (تک‌منبع) خوانده می‌شود؛ آرگومان اول فقط برای
# بازنویسی موقت (مثلاً بیلد یک تگ) است.
VERSION="${1:-$(cat VERSION)}"

# اجزای عددی نسخه برای resource ویندوز (x.y.z → x, y, z)
VER_MAJOR=0; VER_MINOR=0; VER_PATCH=0
if [[ "$VERSION" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+) ]]; then
    VER_MAJOR="${BASH_REMATCH[1]}"
    VER_MINOR="${BASH_REMATCH[2]}"
    VER_PATCH="${BASH_REMATCH[3]}"
fi

echo "==> KodYar ${VERSION}: building windows/amd64 (skipbindings) ..."

# متادیتای نسخهٔ ویندوز (Properties → Details و SmartScreen). resourceی که
# Wails از wails.json می‌سازد در ویندوز خوانده نمی‌شد (FileVersion خالی)؛ این
# .syso مستقل با goversioninfo از همان VERSION ساخته می‌شود تا عددی جدا از
# ldflags وجود نداشته باشد.
VERDIR="$(mktemp -d)"
cleanup() {
    rm -rf "$VERDIR"
    rm -f resource.syso
}
trap cleanup EXIT
cat > "$VERDIR/versioninfo.json" <<EOF
{
  "FixedFileInfo": {
    "FileVersion":    {"Major": ${VER_MAJOR}, "Minor": ${VER_MINOR}, "Patch": ${VER_PATCH}, "Build": 0},
    "ProductVersion": {"Major": ${VER_MAJOR}, "Minor": ${VER_MINOR}, "Patch": ${VER_PATCH}, "Build": 0}
  },
  "StringFileInfo": {
    "CompanyName":     "Meysam Khadem Shams",
    "FileDescription": "KodYar",
    "FileVersion":     "${VERSION}.0",
    "ProductName":     "KodYar",
    "ProductVersion":  "${VERSION}.0",
    "LegalCopyright":  "Copyright (c) 2026 Meysam Khadem Shams"
  },
  "VarFileInfo": {"Translation": {"LangID": "0409", "CharsetID": "04B0"}}
}
EOF
# مانیفست Wails یک قالب است ({{.Name}}, {{.Info.ProductVersion}}) که خود Wails
# پیش از جاسازی رندر می‌کرد. حالا که بیلد با -nopackage است، خودمان رندر
# می‌کنیم — مانیفست خامِ قالب‌دار باعث خطای side-by-side می‌شود.
sed -e "s/{{.Name}}/KodYar/g" \
    -e "s/{{.Info.ProductVersion}}/${VERSION}/g" \
    build/windows/wails.exe.manifest > "$VERDIR/wails.exe.manifest"

# اگر goversioninfo از قبل نصب شده باشد (GOPATH/bin یا bin/windows_amd64) از
# همان باینری استفاده می‌شود تا بیلد بدون شبکه هم کار کند؛ `go run ...@v1.7.0`
# برای گرفتن خودِ ابزار به پراکسی ماژول نیاز دارد.
GOVERSIONINFO_BIN="$(go env GOPATH)/bin/windows_amd64/goversioninfo.exe"
[ -x "$GOVERSIONINFO_BIN" ] || GOVERSIONINFO_BIN="$(go env GOPATH)/bin/goversioninfo"
if [ -x "$GOVERSIONINFO_BIN" ]; then
    "$GOVERSIONINFO_BIN" -64 \
        -icon build/windows/icon.ico \
        -manifest "$VERDIR/wails.exe.manifest" \
        -o resource.syso "$VERDIR/versioninfo.json"
else
    echo "==> goversioninfo محلی پیدا نشد؛ از ماژول اجرا می‌شود (نیازمند شبکه)"
    go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.7.0 -64 \
        -icon build/windows/icon.ico \
        -manifest "$VERDIR/wails.exe.manifest" \
        -o resource.syso "$VERDIR/versioninfo.json"
fi

# -nopackage: Wails خودش یک بخش .rsrc می‌سازد (آیکون + نسخهٔ ناخوانا) و Go با دو
# بخش .rsrc خطای «too many .rsrc sections» می‌دهد؛ پس resource ما تنها منبع است.
wails build -platform windows/amd64 -skipbindings -nopackage \
    -ldflags "-s -w -H windowsgui -X main.version=${VERSION}"

echo "==> done: build/bin/KodYar.exe"
