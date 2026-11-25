# تست دودِ مرورگری KodYar (اختیاری — فقط توسعه)

این پوشه بخشی از ساخت برنامه نیست؛ تست‌های Playwright این‌جا چک می‌کنند که
مودال‌ها و کارت‌های کلیدی UI واقعاً **قابل‌مشاهده** و **قابل‌کلیک** باشند و
دکمه‌هایشان به بریج (در حالت Mock) سیم‌کشی شده باشند:

1. **مودال Diff** (`#diff-overlay`) — باز شدن با رویداد `agent:confirm`،
   نمایش مسیر، کلیک «تأیید» → `RespondFileConfirm` روی بریج.
2. **مودال دسترسی** (`.modal-overlay.perm-ask`) — باز شدن با
   `agent:permission_ask`، کلیک «اجازه یک‌بار» → `RespondPermissionConfirm`.
3. **کارت «خلاصه‌ی واقعی این نوبت»** (`.ts-card`) — پس از یک نوبت ایجنت
   کامل در حالت Mock: فایل‌ها، چیپ محافظت‌شده، راستی‌آزمایی ثبت‌شده، و
   دکمه‌ی «بازگردانی این نوبت» که پیش‌نمایش و سپس بازگردانی را روی بریج
   Mock صدا می‌زند.

تست معادلِ همیشه‌روان (بدون مرورگر، متنِ index.html/JS/CSS) در ریشه‌ی مخزن
است: `uicontract_test.go` — این دو مکمل یکدیگرند.

## اجرا (اولین بار)

```bash
cd frontend/tests
npm ci                 # فقط @playwright/test را از lockfile کامیت‌شده می‌آورد
npx playwright install chromium
npm test
```

### راه جایگزین: npm به رجیستری وصل نمی‌شود

اگر `npm install` با `ECONNRESET`/timeout شکست بخورد ولی `curl` به همان
رجیستری جواب می‌دهد، بسته‌های رسمی را مستقیم از tarball نصب کنید (همان نسخه‌ای
که `package.json` می‌خواهد؛ بدون lockfile):

```bash
cd frontend/tests
rm -rf node_modules
mkdir -p node_modules/@playwright/test node_modules/playwright node_modules/playwright-core
V=1.49.1   # ← همان نسخه‌ی @playwright/test در package.json
curl -sSL https://registry.npmjs.org/@playwright/test/-/test-$V.tgz           | tar -xz -C node_modules/@playwright/test --strip-components=1
curl -sSL https://registry.npmjs.org/playwright/-/playwright-$V.tgz           | tar -xz -C node_modules/playwright --strip-components=1
curl -sSL https://registry.npmjs.org/playwright-core/-/playwright-core-$V.tgz | tar -xz -C node_modules/playwright-core --strip-components=1
node node_modules/playwright-core/cli.js install chromium   # مرورگر (نه npx)
node node_modules/@playwright/test/cli.js test              # اجرا
```

`node_modules/` و `test-results/` در `.gitignore` هستند؛ این راه چیزی به مخزن
اضافه نمی‌کند.

## اجرا (بعدی)

```bash
npm test               # سرور استاتیک داخلی روی 5179 خودکار بالا می‌آید
npm test -- --headed   # دیدن مرورگر
```

## حالت Mock

اسکریپت `frontend/dist/assets/js/mock.js` وقتی صفحه بدون Wails (در مرورگر
معمولی) باز شود فعال می‌شود: بک‌اند ساختگی با Provider نمونه، جریان پاسخ،
مودال Diff و داده‌های نمونه‌ی نقطه‌ی بازگشت. تست‌ها وضعیت اولیه را در
localStorage می‌ریزند تا حتماً deterministic باشند.

## CI

این تست‌ها در CI **بلاک‌کننده** هستند: شکست job `Browser smoke (frontend)`
کل CI را قرمز می‌کند. به همین دلیل نصب وابستگی‌ها قطعی است — `npm ci` از
`package-lock.json` کامیت‌شده (نسخه‌ها قفل‌اند) و کش npm در `setup-node` با
`cache-dependency-path` روی همان lockfile. اگر وابستگی عوض شد، lockfile را
هم با `npm install --package-lock-only` به‌روز و کامیت کنید؛ تست
`TestBrowserSmokeInstallsFromLockfile` برگشتن به `npm install` شناور را
می‌گیرد.

نکتهٔ مهم دربارهٔ lockfile: npm روی ویندوز ورودی وابستگی اختیاری macOS
(`node_modules/fsevents`، نسخهٔ 2.3.2) را از lockfile حذف می‌کند و بعد
`npm ci` با خطای «Missing: fsevents@ from lock file» قرمز می‌شود. اگر
lockfile را از نو ساختنید، همان ورودی را (با `resolved`/`integrity` از
رجیستری) برگردانید؛ تست `TestBrowserSmokeInstallsFromLockfile` نبودش را
می‌گیرد.
