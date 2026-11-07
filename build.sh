#!/usr/bin/env bash
# ساخت بستهٔ ویندوزی MyAI (کامپایل متقاطع خالص Go — بدون نیاز به MinGW).
#
# توجه: این فایل در بایگانی zip پروژه وجود نداشت و از تاریخچهٔ git هم
# قابل بازیابی نبود؛ این نسخه بازسازی‌شده بر اساس دستور ساخت مستند در
# README.md است (wails build -platform windows/amd64 -skipbindings).
#
# پیش‌نیاز: Wails CLI v2 (هم‌نسخه با go.mod):
#   go install github.com/wailsapp/wails/v2/cmd/wails@v2.10.1
set -euo pipefail
cd "$(dirname "$0")"

VERSION="${1:-1.0}"

echo "==> MyAI ${VERSION}: building windows/amd64 (skipbindings) ..."
wails build -platform windows/amd64 -skipbindings \
    -ldflags "-s -w -H windowsgui -X main.version=${VERSION}"

echo "==> done: build/bin/MyAI.exe"
