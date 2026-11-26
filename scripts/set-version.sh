#!/usr/bin/env bash
# نسخه را یک‌جا بالا می‌برد: VERSION، wails.json و پکیج تست مرورگری
# (+ بازسازی package-lock.json). جای این‌که سه فایل دستی همگام شوند.
#
# استفاده: scripts/set-version.sh 1.0.6
set -euo pipefail
cd "$(dirname "$0")/.."

NEW="${1:-}"
if [[ ! "$NEW" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "usage: scripts/set-version.sh MAJOR.MINOR.PATCH (got \"${NEW}\")" >&2
    exit 2
fi

OLD="$(tr -d '[:space:]' < VERSION)"
echo "==> version ${OLD} -> ${NEW}"

# VERSION تنها منبع عدد نسخه است.
printf '%s\n' "$NEW" > VERSION

# wails.json: productVersion همان چیزی است که در Properties ویندوز دیده می‌شود.
sed -E 's/("productVersion"[[:space:]]*:[[:space:]]*")[^"]*"/\1'"$NEW"'"/' \
    wails.json > wails.json.tmp && mv wails.json.tmp wails.json

# پکیج تست مرورگری هم نسخه دارد؛ TestVersionSingleSource آن را می‌سنجد.
sed -E 's/^([[:space:]]*"version"[[:space:]]*:[[:space:]]*")[^"]*"/\1'"$NEW"'"/' \
    frontend/tests/package.json > frontend/tests/package.json.tmp &&
    mv frontend/tests/package.json.tmp frontend/tests/package.json

# lockfile نسخه را در دو جا تکرار می‌کند؛ بازسازی با npm خودش هر دو را می‌نویسد.
if command -v npm >/dev/null 2>&1; then
    (cd frontend/tests && npm install --package-lock-only --no-audit --no-fund >/dev/null)
else
    echo "ERROR: npm not found; regenerate frontend/tests/package-lock.json manually" >&2
    exit 1
fi

# تأیید: هر سه فایل باید همین عدد را داشته باشند.
for f in VERSION wails.json frontend/tests/package.json frontend/tests/package-lock.json; do
    if ! grep -qF "$NEW" "$f"; then
        echo "ERROR: ${f} does not carry ${NEW}" >&2
        exit 1
    fi
done

echo "==> synced: VERSION, wails.json, frontend/tests/{package.json,package-lock.json}"
echo "    next: add a \"## ${NEW}\" section to CHANGELOG.md"
echo "          (TestVersionMatchesChangelog and TestVersionSingleSource check it)"
