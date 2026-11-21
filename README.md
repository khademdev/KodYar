<div align="center" dir="rtl">

<img src="build/appicon.png" alt="KodYar" width="110" />

# KodYar

**دستیار هوشمند کدنویسی ایجنتی — اپلیکیشن دسکتاپ بومی (Native)**

بدون Electron · رابط ۱۰۰٪ فارسی و راست‌به‌چپ · سندباکس واقعی ویندوز · چندمدلی

<p>
  <a href="https://github.com/khademdev/KodYar/releases/latest"><img src="https://img.shields.io/github/v/release/khademdev/KodYar?style=for-the-badge&label=release&color=7c5cff" alt="Release" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-3ddc97?style=for-the-badge" alt="License: MIT" /></a>
  <img src="https://img.shields.io/badge/platform-Windows%20%7C%20macOS%20%7C%20Linux-4c8dff?style=for-the-badge" alt="Platforms" />
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go 1.26" />
  <img src="https://img.shields.io/badge/Wails-v2.10.1-DF0000?style=for-the-badge" alt="Wails v2" />
</p>
<p>
  <a href="https://github.com/khademdev/KodYar/stargazers"><img src="https://img.shields.io/github/stars/khademdev/KodYar?style=for-the-badge&logo=github&color=ffcc4d" alt="Stars" /></a>
  <a href="https://github.com/khademdev/KodYar/releases"><img src="https://img.shields.io/github/downloads/khademdev/KodYar/total?style=for-the-badge&color=2ea44f" alt="Downloads" /></a>
  <a href="https://www.coffeebede.com/khademdev"><img src="https://img.shields.io/badge/SUPPORT%20THE%20PROJECT-CoffeeBede-FFDD00?style=for-the-badge&logo=buymeacoffee&logoColor=black" alt="حمایت از پروژه" /></a>
</p>

**[⬇️ دانلود آخرین نسخه](https://github.com/khademdev/KodYar/releases/latest)** · **[☕ حمایت از پروژه](https://www.coffeebede.com/khademdev)** · **[تغییرات نسخه‌ها](CHANGELOG.md)**

</div>

<div dir="rtl">

## ❤️ حمایت از پروژه

KodYar یک پروژهٔ مستقل و متن‌باز است که بدون اسپانسر و در وقت آزاد توسعه داده می‌شود.
اگر این ابزار برایتان مفید بود، با یک قهوه از ادامهٔ راهش حمایت کنید:

<p align="center">
  <a href="https://www.coffeebede.com/khademdev">
    <img src="https://img.shields.io/badge/SUPPORT%20THE%20PROJECT-CoffeeBede-FFDD00?style=for-the-badge&logo=buymeacoffee&logoColor=black" alt="حمایت از پروژه در CoffeeBede" height="34" />
  </a>
</p>

<p align="center"><b><a href="https://www.coffeebede.com/khademdev">https://www.coffeebede.com/khademdev</a></b></p>

همین دکمهٔ حمایت داخل خود برنامه هم هست: پایین سایدبار ← «حمایت از پروژه».

---

## در این صفحه

- **KodYar چیست؟** — یک نگاه سریع
- **نسخهٔ ۱.۰.۱** — عیب‌یابی کد با زبان‌سرور و هارنس ارزیابی ایجنت
- **نسخهٔ ۱.۰.۰** — نکته‌های کلیدی نخستین انتشار عمومی
- **ویژگی‌ها** — حالت ایجنت و چت
- **مدل امنیتی** — سندباکس، دروازهٔ دسترسی و محدودیت‌های صادقانه
- **نصب و اجرا** — باینری آماده یا ساخت از سورس
- **ساختار پروژه، داده‌ها، میان‌برها و MCP**
- **مستندات، مجوز و حمایت از پروژه**

## KodYar چیست؟

KodYar یک اپلیکیشن دسکتاپ **کاملاً native** برای گفتگو با مدل‌های هوش مصنوعی و **کار ایجنتی روی پروژه‌ها**ست؛
ساخته‌شده با **Wails v2 (Go + WebView سیستمی)** — بدون Electron، بدون وابستگی به مرورگر خارجی
و با مصرف حداقلی منابع. رابط کاربری **۱۰۰٪ فارسی و راست‌به‌چپ** است و ظاهر آن مطابق mockupهای طراحی
(تم تاریک مدرن با اکسنت بنفش) پیاده‌سازی شده است.

- 🧠 **ایجنت واقعی روی پروژه** — خواندن/نوشتن/ویرایش اتمیک، `apply_patch`، جستجو و ایندکس نمادها، `git_*`، اجرای دستور با اجازه
- 🛡️ **سندباکس سیستمی** — هر دستور داخل AppContainer ویندوز (یا bubblewrap روی لینوکس) با ACL هدفمند
- 💬 **چت چندمدلی** — هر سرویس سازگار با API نوع OpenAI؛ کشف خودکار مدل‌ها و پنجرهٔ کانتکست
- 🇮🇷 **رابط فارسی راست‌به‌چپ** — گزارش زندهٔ کارها، برنامهٔ کار، دایرهٔ مصرف توکن و خلاصهٔ واقعی هر نوبت
- 🔒 **داده روی دستگاه شما** — کلیدها با AES-GCM رمز می‌شوند و هیچ چیزی جز درخواست خودتان به Provider بیرون نمی‌رود

## نسخهٔ ۱.۰.۱ — عیب‌یابی کد با زبان‌سرور و هارنس ارزیابی

- **ابزار `diagnostics`** — ایجنت حالا با gopls (Go) و typescript-language-server (TS/JS)
  صحبت می‌کند و خطاهای واقعی فایل را با شمارهٔ خط، **قبل از build**، می‌گیرد؛
  محدود به ۱۰ فایل/۴۵ ثانیه، فقط‌خواندنی، و در نبود سرور زبان یک پیام نصب روشن
  برمی‌گرداند. قاعدهٔ ۴ پرامپت هم عوض شد: اول `diagnostics`، بعد build/test.
- **هارنس ارزیابی ایجنت** — ۱۵ سناریوی کوچک با ریپوی نمونه و معیار موفقیت که
  نرخ موفقیت، تعداد دور، توکن و ابزارهای اشتباه را گزارش می‌کند؛ هم با مدل
  نمایش‌نامه‌ای (آفلاین و قطعی) و هم با **مدل واقعی** از طریق متغیرهای محیطی.
- **مقایسهٔ دو اجرا** — دو گزارش JSON را کنار هم می‌گذارد و تفاوت نرخ موفقیت،
  دور، توکن و ابزار اشتباه را نشان می‌دهد؛ تا «بهتر شد» یک عدد داشته باشد.

تاریخچهٔ کامل این نسخه: [CHANGELOG.md](CHANGELOG.md)

## نسخهٔ ۱.۰.۰ — نخستین انتشار عمومی

این نخستین انتشار عمومی KodYar است؛ هر چیزی که در بخش «ویژگی‌ها» می‌آید در همین نسخه کار می‌کند.
چند نکتهٔ کلیدی این نسخه:

- **ایجنت واقعی روی پروژه** — ابزارهای خواندن/نوشتن/ویرایش اتمیک/`apply_patch`، جستجو و ایندکس نمادها، ابزارهای `git_*`،
  اجرای دستور با اجازهٔ صریح (و فرایند پس‌زمینه + لاگ زنده)، `web_search`/`web_fetch`، برنامهٔ کار (`todo_write`)،
  زیرایجنت فقط‌خواندنی (`task`)، ابزارهای MCP، چک‌پوینت و بازگردانی، راستی‌آزمایی خودکار و دروازهٔ پایان کار.
- **چت چندمدلی** — تشخیص خودکار مدل‌های هر سرویس‌دهنده، انتخاب‌گر مدل با جستجو و **گروه کشویی هر Provider**،
  مصرف توکن به تفکیک مدل، کش پرامپت و اندازه‌گیری تأخیر هر نوبت.
- **امنیت** — دروازهٔ دسترسی (دستور/فایل حساس/دامنه)، سندباکس AppContainer برای دستورها، رمزنگاری AES-GCM کلیدها
  با کلید اصلی keyring، و خواندن‌پیش‌از‌نوشتن با کشف تغییر بیرونی (SHA-256).
- **پایداری** — مهار panic در همهٔ گوروتین‌ها، نگهبان سکون استریم، نوشتن اتمیک داده‌ها با `.bak` و نجات گفتگوی
  آسیب‌دیده، بازگردانی چک‌پوینت بایت‌به‌بایت و بدون حذف.
- **رابط فارسی راست‌به‌چپ** — تم تاریک، گزارش زندهٔ کارها، برنامهٔ کار، دایرهٔ مصرف توکن، خلاصهٔ نوبت و لاگ خطاها.
- **در همین انتشار** — نام پوشه/آدرس به‌جای «.» در کارت‌های ابزار و نوار فعالیت، و نمایش زندهٔ استدلال مدل
  (پیش‌تر در مدل‌های reasoning تا پایان دور هیچ «در حال فکر کردن» دیده نمی‌شد).

تاریخچهٔ کامل تغییرات (شامل نسخه‌های داخلی پیش از انتشار): [CHANGELOG.md](CHANGELOG.md)

## ویژگی‌ها

### حالت ایجنت (شبیه OpenCode)

- **سوییچ «حالت ایجنت» در سایدبار** — وقتی فعال باشد، مدل به مجموعه‌ای از ابزارهای سیستم دسترسی پیدا
  می‌کند و می‌تواند در چند مرحله‌ی پشت‌سرهم روی پروژه کار کند:
  `list_dir`، `read_file`، `write_file`، `edit_file` (جایگزینی دقیق یا چندگانه‌ی اتمیک با آرایه‌ی `edits`
  با کنترل یکتایی و تطبیق‌گر مقاوم CRLF/فاصله/تورفتگی)، `move_path`، `delete_path`، `create_dir`، `search_files` (جستجوی متن/regex در
  کل پروژه؛ با فیلتر `include` روی مسیر/نوع فایل، `context` برای خطوط اطراف و `case_sensitive` برای تطبیق دقیق حروف — پیش‌فرض غیرحساس)، `glob` (یافتن فایل با الگو؛ جدیدترین تغییر اول) و `run_command`
  (اجرای دستور در پوشه‌ی پروژه با اجازه‌ی صریح کاربر؛ با `background:true` فرایند پس‌زمینه
  شروع می‌شود و شناسه برمی‌گردد — سپس `process_output` لاگ تازه را می‌خواند، `process_wait`
  تا خروج/مهلت صبر می‌کند، `process_input` به stdin همان فرایند می‌نویسد (CLI تعاملی مثل
  `npm create vite`/`gh auth login` و کلیدهای dev server؛ فقط وقتی سندباکس خاموش است)،
  `wait_for_port` تا آماده‌شدن پورت صبر می‌کند، `browser_open`/`browser_console`/`browser_screenshot`
  صفحه را در یک کروم/اج نصب‌شدهٔ سیستم (headless، از طریق CDP) باز می‌کنند، خطاهای کنسول/شبکه
  را می‌دهند و PNG را داخل `screenshots/` ذخیره می‌کنند؛ اگر مرورگری نصب نباشد پیام روشن می‌دهند. `process_stop`
  کل درخت فرایند را تمیز می‌کشد و `process_list` وضعیت را نشان می‌دهد) + ابزارهای `git_*` (وضعیت/diff/تاریخ/شاخه/کامیت)،
  **`task` (زیرایجنت فقط‌خواندنی با کانتکست تازه برای زیرکارهای اکتشافی)**،
  **`todo_write` (برنامهٔ کار زنده)**، **`web_fetch` (دریافت امن مستندات/صفحات از وب)** و
  **`find_symbol` و `find_references` (ایندکس نمادها: نماد کجاست تعریف شده و کجا استفاده می‌شود — تطبیق مرز-کلمه‌ای)**،
  **`diagnostics` (خطا/هشدار واقعی فایل از زبان‌سرور — gopls و typescript-language-server — پیش از build؛ برای Python/PHP/Rust/.NET بررسی خودکار پایان نوبت — `py_compile`/`php -l`/`cargo`/`dotnet` — از جمله در monorepoیی که ماژول در ریشه نیست)**، **ابزارهای `mcp_*` (سرورهای MCP خارجی)**
- **زیرایجنت (task)** — برای جستجو/تحلیل‌های گسترده، ایجنت می‌تواند کار را به یک زیرایجنت فقط‌خواندنی
  بسپارد که با کانتکست تازه کار می‌کند و گزارش ساختارمند برمی‌گرداند؛ سقف دورها قابل تنظیم است
  (تب «مدل و رفتار»)، هر واگذاری با مودال اجازه تأیید می‌شود و فایل‌های حساس داخلش خوانده نمی‌شوند
- **حلقه‌ی ایجنت واقعی** — مدل فراخوانی ابزار می‌فرستد، نتیجه به او برگردانده می‌شود و تا رسیدن به پاسخ
  نهایی ادامه می‌یابد (حداکثر ۲۵ دور برای جلوگیری از حلقه‌ی بی‌پایان)
- **کارت‌های فعالیت ابزار** — هر خواندن/نوشتن/اجرا به‌صورت کارت مستقل با نام ابزار، خلاصه‌ی آرگومان،
  وضعیت موفق/خطا، زمان اجرا و نتیجه‌ی قابل باز/بسته شدن نمایش داده می‌شود
- **نوار «گزارش زنده»** — سطح همیشه‌رو کار ایجنت: هر عملیات یک جمله‌ی فارسی
  («در حال خواندن `agent.go`»، «فایل `x` ویرایش شد»، «حذف `y` ناموفق بود — دلیل») با نشانگر
  در حال انجام/موفق/ناموفق و زمان اجرا؛ در پایان نوبت خودکار جمع می‌شود و شمارنده‌ی «۱۲ عملیات»
  می‌ماند. هر خط، برچسب وظیفه‌ی متناظر («وظیفهٔ ۲ از ۵») را از پنل «برنامهٔ کار» دارد و
  `todo_write` به یک خط جمع‌شده («برنامهٔ کار به‌روزرسانی شد — ۲ از ۵») تبدیل می‌شود تا فهرست
  کارها دو بار نمایش داده نشود. روشن/خاموش‌کردنش در تب «مدل و رفتار» است و ذخیره می‌شود
- **پنل «برنامهٔ کار»** — چک‌لیست `todo_write` با پیشرفت «۲ از ۵»؛ پیش‌فرض جمع‌شده است تا با
  گزارش زنده اشتباه نشود و فقط وقتی چک‌لیستی وجود دارد ظاهر می‌گردد
- **راستی‌آزمایی پوششی** — تذکر پایان نوبت فقط نمی‌پرسد «تستی اجرا شد؟»؛ دایرکتوری‌های
  تغییرکرده را می‌گوید، «فقط build» را از «تست» جدا می‌کند و یک دستور تست هدفمند پیشنهاد
  می‌دهد (مثل `go test ./pkg/a/...`) تا اثبات تغییر، به همان بخشی بچسبد که عوض شده است
- **کش پرامپت (اختیاری)** — با روشن‌کردن `promptCache` در تنظیمات، بلوک ثابت پرامپت
  (system + ابزارها) برای سرویس‌دهنده‌های سازگار با `cache_control` نشانه‌گذاری می‌شود؛
  اگر ارائه‌دهنده رد کند، یک‌بار بدون آن تلاش می‌شود و تا restart فراموش می‌شود
- **امنیت مسیر** — همه‌ی ابزارهای فایل به‌شدت داخل «پوشه‌ی کاری» محدود می‌شوند (خروج با `..` یا مسیر
  مطلق بیرون از پروژه مسدود است) و اجرای دستور فقط با سوییچ «اجازه اجرای دستور» انجام می‌شود
- **ذخیره‌ی تدریجی** — هر دور ابزار بلافاصله روی دیسک ذخیره می‌شود؛ بستن برنامه در میانه‌ی کار،
  فعالیت انجام‌شده را از بین نمی‌برد
- **سازگاری هوشمند** — اگر مدل/سرویسِ انتخابی از فراخوانی ابزار پشتیبانی نکند، به‌صورت خودکار یک‌بار
  بدون ابزار تلاش می‌شود و به کاربر اطلاع داده می‌شود

### چت

- **صفحه‌ی چت با حالت خالی زیبا** — آیکون تراشه‌ی AI درخشان، پیام خوش‌آمد، چیپ‌های پیشنهاد سریع
  (نوشتن قطعه کد، بررسی پروژه با ایجنت، ایده‌پردازی، اشکال‌زدایی، بازبینی امنیت کد)
- **مکالمه‌ی کامل** — حباب کاربر سمت راست (تیره‌تر)، پاسخ AI سمت چپ با رندر کامل **Markdown**،
  جدول، نقل‌قول و **بلوک کد با شماره‌ی خط، هایلایت سینتکس و دکمه‌ی کپی**
- **پاسخ استریمی** — نشانگر «در حال فکر کردن…»، کرسر تایپ زنده، دکمه‌ی «توقف» (هم در composer و
  هم کنار پاسخ) + «تولید دوباره» واقعی روی آخرین پاسخ
- **سایدبار راست** — دکمه‌ی «گفتگوی تازه»، جستجوی گفتگوها، لیست گفتگوها با تاریخ نسبی فارسی
  («امروز 10:42»، «۲ روز پیش»)، نشان «جدید»، حذف تکی و «حذف همه» با تأیید
- **مودال تنظیمات با ۳ تب**:
  - **اتصال** — انتخاب Provider (با آیکون)، مدل (با پیشنهاد خودکار)، کلید API با نمایش/مخفی،
    تست اتصال و ذخیره‌ی رمزنگاری‌شده‌ی کلید (AES-GCM)
  - **Provider ها** — افزودن/ویرایش/حذف سرویس‌دهنده‌های سازگار با API نوع OpenAI؛ فرم Provider فقط
    **نام سرویس و کلید API** را می‌پرسد و آدرس/مدل‌ها/کانتکست را خودکار از سرویس‌دهنده کشف می‌کند؛
    بعد از ذخیره، Provider تازه فعال و اولین مدل چت خودکار انتخاب می‌شود
    (بدون Provider پیش‌فرض — کاربر خودش تعریف می‌کند)
  - **پوشه‌ی کاری (مسیر پروژه)** — **تایپ مستقیم مسیر** (مثلاً `C:\Projects\MyApp`) یا انتخاب با
    دیالوگ سیستم‌عامل؛ اگر مسیر وجود نداشته باشد هنگام ذخیره **به‌صورت خودکار ساخته می‌شود** و
    خطاها همراه با علت واقعی نمایش داده می‌شوند + سوییچ «اجازه اجرای دستور»
- **سوییچ «حالت ایجنت» در چت‌باکس** — هماهنگ با تنظیمات؛ «اجرای دستور» فقط در تنظیمات (تب پوشه‌ی کاری) است و اجرای دستور فقط در
  پوشه‌ی کاری و فقط با اجازه انجام می‌شود
- **نشانگر مصرف توکن** — پیلِ «کانتکست» در نوار بالا: دایرهٔ درصد + کسر خوانا
  («۱۲هزار از ۲۰۰هزار») و برچسب مشخص که نشان می‌دهد عدد مربوط به «پنجرهٔ کانتکست همین
  گفتگو» است، نه سهمیهٔ حساب. کلیک روی آن پاپ‌اور توضیح فارسی باز می‌کند: عدد کانتکست
  (دقیق/تخمینی)، مصرف تجمعی Provider و مدل فعال، تاریخ آخرین صفر کردن، و دکمهٔ
  دو مرحله‌ای «شمارنده را صفر کن (پس از شارژ سرویس)». مصرف تجمعی روی دیسک ذخیره
  می‌شود و با بستن برنامه یا قطع/وصل اتصال صفر نمی‌شود
- **حسابداری مصرف** — هر دور فقط یک‌بار ثبت می‌شود (جمع prompt+completion همان دور)؛
  اعداد به تفکیک Provider و مدل، و مصرف هر گفتگو جدا ذخیره می‌شود
- **انتخابگر مدل** — پاپ‌اور گروه‌بندی‌شده بر اساس Provider (با جستجو) از دکمه‌ی مدل در چت‌باکس.
  «محیط پاسخ‌دهی» از رابط برداشته شد؛ رفتار با حالت چت/ایجنت و «دستور سیستمی» در تنظیمات تعیین می‌شود
- **مدیریت BiDi** — کدها، مسیر فایل‌ها و URLها همیشه با `unicode-bidi: isolate` و جهت LTR
  نمایش داده می‌شوند و متن فارسی راست‌چین می‌ماند

## مدل امنیتی / Security model

**فارسی — صادقانه و بدون اغراق:**

- **ابزارهای فایل فقط داخل پوشه‌ی کاری** — همه‌ی مسیرهای داده‌شده به ابزارها از ریشه‌ی پروژه حل می‌شوند؛ خروج با `..` یا مسیر مطلق/فرار مسدود است. فایل‌های حساس (`.env*`، `*.pem` و…) و مسیرهای محافظت‌شده (.git حتی تودرتو، فایل‌های تعریف ساخت مثل `package.json`) حتی در «ویرایش خودکار» مودال تأیید می‌گیرند.
- **دستورها گیت‌دار هستند و سندباکس ویندوزی دارند** — اجرای دستور از سه لایه می‌گذرد: تجزیه‌گر شله‌آگاه (عدم‌قطعیت = پرسش)، لیست‌سفید ساختاری (git/go/npm با پرچم صریح) و بررسی توکن حساس/مسیر محافظت‌شده. در ویندوز می‌توانید از تنظیمات → پوشه، «سندباکس اجرای دستور» را روشن کنید: هر دستور داخل یک **AppContainer واقعی** (SID اختصاصی، ACL هدفمند، Job Object) اجرا می‌شود — خواندن/نوشتن فقط در پوشه‌ی کاری و خانه‌ی موقت ایزوله، پروفایل کاربر ممنوع و شبکه با کلید «اجازه‌ی شبکه». اگر روشن باشد و راه‌اندازی شکست بخورد، دستور **اجرا نمی‌شود** (fail-closed). **پیش‌فرض روشن است**؛ ابزارهای نیازمند NUL (go/git) یک‌بار فعال‌سازی با تأیید Administrator می‌خواهند: دکمهٔ «فعال‌سازی دسترسی NUL» در همان تب، با یک تأیید UAC، ACE دستگاه NUL را اعمال و یک Task سیستمی در هر بوت ثبت می‌کند (جزئیات در «محدودیت شناخته‌شده»).
- **دستورهای تست/ساختِ خودکارِ اجازه‌ی بدون پرسش کد اجرا می‌کنند** — `go test`، `npm test` و مانند آن‌ها بدون مودال اجرا می‌شوند چون بی‌خطر به نظر می‌رسند؛ اما آن‌ها همان کدی را اجرا می‌کنند که ایجنت چند لحظه قبل نوشته است. **شبکه‌ی ایمنی واقعی، بازبینی پایان نوبت است**: کارت «خلاصه‌ی واقعی این نوبت» را بخوانید و پیش‌نمایش Diff مسیرهای محافظت‌شده را تأیید کنید. دکمه‌ی «بازگردانی این نوبت» در همان کارت، پیش از هر تغییری پیش‌نمایش می‌دهد و سپس فایل‌های همان نوبت را به وضعیت قبل برمی‌گرداند (فایل‌های تازه حذف نمی‌شوند؛ به سبد زباله می‌روند). با این حال روی شاخه‌ی git کار کنید و پیش از نوبت‌های سنگین وضعیت را کامیت کنید.
- **حالت‌های ویرایش** — «بازبینی هر تغییر» مودال Diff برای هر نوشتن می‌دهد؛ «ویرایش خودکار» فقط مسیرهای محافظت‌شده و حذف سنگین را می‌پرسد و بقیه را در خلاصه‌ی نوبت گزارش می‌کند.
- **نظم راستی‌آزمایی و سقف توکن** — ایجنت بعد از تغییر فایل بدون تست/ساختِ موفق یک تذکر یک‌باره می‌گیرد؛ «سقف توکن هر نوبت» مصرف همه‌ی دورها + زیرایجنت‌ها را می‌سنجد و نوبت را مودبانه متوقف می‌کند.
- **اعتماد به محتوای بیرونی** — خروجی `web_fetch` و ابزارهای MCP در پوشش `untrusted` به مدل می‌رسد (فقط داده، نه دستور)؛ گارد SSRF اتصال را در هر پرش بازرسی می‌کند؛ اجازه‌ی web_fetch فقط برای هر دامنه ذخیره می‌شود (هرگز برای کل ابزار). سرورهای MCP فرایند قابل‌اعتمادِ شما هستند اما با محیط حداقلی بالا می‌آیند (بدون کلیدهای API برنامه) و هر فراخوانی ابزارشان مودال اجازه دارد.
- **داده‌ها روی دستگاه شما** — کلید API با AES-GCM و کلید میزبان در Keyring سیستم‌عامل رمز می‌شود؛ محیطی متغیرهای MCP رمزنگاری‌شده ذخیره و در UI فقط ماسک می‌شوند؛ هیچ چیزی جز آدرس Provider به بیرون رفت نمی‌کند (و آن هم فقط به سرویس‌دهنده‌ی خودتان).
- **گردش‌کار پیشنهادی** — روی یک شاخه‌ی git کار کنید، حالت ویرایش را آگاهانه انتخاب کنید، در پایان هر نوبت کارت خلاصه را بخوانید و در صورت شک، تغییرات را با git (diff/checkout) بازبینی کنید.
- **پایداری** — خطای داخلی برنامه را نمی‌کشد (مهار panic در همه‌ی گوروتین‌ها و متدهای بایند‌شده)، سکون سرویس‌دهنده با نگهبان ۱۸۰ ثانیه‌ای تمام می‌شود، فایل گفتگو/`changes.json` با `.bak` و نجات پیام‌های سالم بازیابی می‌شوند و بازگردانی چک‌پوینت با `EvalSymlinks`/رد reparse از نوشتن بیرونِ پروژه جلوگیری می‌کند. این‌ها تضمین‌های پایداری‌اند، نه جداسازی سیستمی؛ سندباکس واقعی همان بند دوم است.

<details>
<summary><b>محدودیت‌های شناخته‌شده‌ی سندباکس (جزئیات فنی)</b></summary>

- در AppContainer دستورهای داخلی `cmd` و نوشتن در پوشه‌ی کاری کار می‌کنند و دسترسی بیرون از پوشه واقعاً بسته است (تست‌های `TestSandbox*`).
- دستورهای ساده مستقیم (بدون cmd) اجرا می‌شوند و پس از اجرای یک‌باره‌ی `scripts/grant-sandbox-nul.ps1` (که ACE دستگاه NUL را برای SID سندباکس اضافه می‌کند) این‌ها داخل AppContainer تأیید شده‌اند: `go version`، `go vet ./...`، `go test ./...` و `git --version`.
- بدون اجرای آن اسکریپت، ابزارهای نیازمند NUL با `open NUL: Access is denied` متوقف می‌شوند. DACL دستگاه NUL در حافظه‌ی کرنل است و با هر ری‌استارت بازنشانی می‌شود. نوشتن DACL یک شیء کرنل به دسترسی Administrator نیاز دارد، پس برنامه در «تنظیمات → پوشه‌ی کاری» یک دکمهٔ یک‌باره دارد: با یک تأیید UAC، ACE اعمال و یک Task سیستمی (`ONSTART`/`SYSTEM`) ساخته می‌شود که در هر بوت، پیش از هر لاگون و بی‌پنجره، خودش تکرارش می‌کند؛ مسیرهای قدیمی (Task کاربری و ورودی پوشه‌ی Startup) در همان اجرا پاک می‌شوند. تا وقتی این فعال‌سازی انجام نشده، برنامه با یک پروب واقعی داخل AppContainer وضعیت را می‌سنجد و در همان صفحه و در پیام خطای دستور صریحاً می‌گوید که `go`/`git` تا فعال‌سازی کار نمی‌کنند.
- `dir` — هم به‌تنهایی و هم داخل زنجیره‌های `&&`/`||`/`&` — با یک لیست‌کننده‌ی داخلی که فقط به پوشه‌ی کاری محدود است اجرا می‌شود (`vol` و اطلاعات volume همچنان در دسترس نیست)؛ دستورهای مرکب (`&&`، `||`، `|`، تغییرمسیر) از cmd اجرا می‌شوند و حالا با `CREATE_NO_WINDOW` فرزندان خارجی (git/go/node) هندل‌های std معتبر می‌گیرند.
- سازگاری git: runtime‌ی MSYS نمی‌تواند شیء نام‌دارش را در AppContainer بسازد (NtCreateDirectoryObject با 0xC0000022 شکست می‌خورد)؛ بنابراین دستورهای git — و ترکیب‌های صرفاً git — آگاهانه و مستند خارج از AppContainer ولی همچنان تحت دروازه‌ی دسترسی و مودال اجازه اجرا می‌شوند و این استثنا یک‌بار در «لاگ خطاها» ثبت می‌شود. همین استثنا در تنظیمات → پوشه، زیر «اجازه‌ی شبکه در سندباکس»، به‌صورت یک یادداشت به کاربر نشان داده می‌شود. هر دستور دیگر (go/node/npm/…) سندباکس‌شده می‌ماند.

</details>

<details>
<summary><b>English — stated honestly, without overstatement</b></summary>

- **File tools are confined to the workspace.** Every model-supplied path is resolved against the workspace root; `..`, absolute and escape paths are rejected. Sensitive files (`.env*`, `*.pem`, …) and protected paths (`.git` including nested submodule ones, build-definition files like `package.json`) always ask, even in the "auto" edit mode.
- **Commands are gated, with an opt-in Windows sandbox.** Command execution passes a shell-aware parser (uncertainty → ask), a structural allow-list (git/go/npm with explicit flags) and sensitive-token/protected-path checks. In Settings → Folder you can enable the command sandbox: each command then runs inside a real **AppContainer** (dedicated SID, targeted ACLs, Job Object) — read/write only in the workspace and an isolated temp home, the user profile is inaccessible, and network access follows the "allow network" switch. If it is on and cannot start, the command is **not run** (fail-closed). Turning it off in Settings removes the ACEs it added for its sandbox SID (tracked in a local grant cache), so no extra access is left behind. It is **on by default** on Windows. The NUL-device grant needs Administrator once: a button in Settings → Folder applies it after a single UAC prompt and registers a SYSTEM `ONSTART` task that re-applies it silently at every boot (the old per-user task and the Startup-folder entry are removed in the same run). A real probe inside the AppContainer measures whether the grant is in effect, and both the settings tab and command errors state plainly that go/git cannot run until it is. On Linux the same switch uses **bubblewrap** (bwrap): a private user/pid/net/ipc/uts namespace, the whole filesystem read-only except the workspace and an isolated temporary home, `/tmp` as a private tmpfs, a cleared environment with an allowlist (no API keys) and no capabilities; network follows the "allow network" switch. If bwrap is not installed the command is **not run** and the error says so (fail-closed). MCP servers use the same bwrap isolation with real stdio pipes. macOS has no OS-level isolation yet and uses the gated path; the UI states this honestly.
- **Auto-allowed test/build commands execute code the agent may have just written.** `go test`, `npm test` and similar are auto-allowed because they look harmless — yet they run whatever the agent just wrote. The real safety net is the end-of-turn review: read the "real turn summary" card and confirm the Diff preview for protected paths. The "restore this turn" button in the same card previews every affected file and then reverts that turn's edits (new files go to the trash, never deleted); still, work on a git branch and commit before heavy turns.
- **Edit modes** — "review each change" shows a diff modal for every write; "auto" only asks for protected paths and heavy deletes and reports everything else in the turn summary.
- **Verification discipline & token cap** — a final answer without a successful build/test since the last file change triggers a one-time verification nudge; the per-turn token cap (max tokens per turn) counts all rounds plus sub-agents and stops the turn gracefully.
- **Untrusted content** — `web_fetch` and MCP results reach the model inside an `untrusted` envelope (data, never instructions); the SSRF guard re-checks every redirect hop; web_fetch and web_search permissions are stored **per host** only — never tool-wide. web_search (DuckDuckGo HTML, no API key) returns titles, URLs and snippets inside a `<search_results untrusted="true">` envelope with the same SSRF guard, and every source carries its full URL so the model can cite it. MCP servers are your trusted local processes but run with a minimal environment (never the app's API keys) and every tool call asks.
- **Your data stays on your machine** — API keys are AES-GCM encrypted with an OS-keyring master key; MCP env vars are encrypted and masked in the UI; nothing leaves the device except your own provider requests.
- **Recommended workflow** — work on a git branch, choose the edit mode deliberately, read the turn summary card after every turn, and when in doubt inspect the changes with git (`diff`/`checkout`) before continuing.
- **Stability** — an internal error no longer kills the app (panic recovery in every goroutine and bound method), a silently stalled provider stream ends after a 180 s idle watchdog, conversation/`changes.json` files get `.bak` + salvage recovery with a Persian notice, and checkpoint restore refuses symlinked/junction parents (real-path resolution) and streams file comparisons so memory stays flat. These are resilience guarantees, not OS-level isolation; the real sandbox is the AppContainer bullet above.
- **Known sandbox limitations** — inside AppContainer, `cmd` built-ins such as `echo`/`type`, `dir` (served by a built-in lister confined to the workspace, including inside `&&`/`||`/`&` chains, and `dir > file` / `dir >> file` redirects that can only write inside the workspace), direct executables and writes in the workspace work, and access outside the workspace is genuinely blocked (see `TestSandbox*`). `vol`/volume information stays unavailable because it needs access to the drive root. Composite commands (`&&`, `||`, `|`, redirections) run through `cmd`, which now passes valid std handles to external children (`CREATE_NO_WINDOW` instead of `DETACHED_PROCESS`). Git compatibility: the MSYS runtime cannot create its named shared-memory object inside an AppContainer (`NtCreateDirectoryObject` fails with 0xC0000022), so git commands — and pure-git composites — run outside AppContainer while still passing the permission gate; the exemption is logged once in the error log. A lone read-only `cygpath` call is exempt for the same reason (path conversion works with the sandbox on), while `cygpath` with a redirect, a pipe or any second command stays sandboxed. The exemption is also shown to the user as a note under Settings → Folder → “Allow network in sandbox”. Every other command (go/node/npm/…) stays sandboxed.

</details>

## نصب و اجرا

### ⬇️ سریع‌ترین راه — باینری آماده

از صفحهٔ [Releases](https://github.com/khademdev/KodYar/releases/latest) فایل `KodYar.exe` را دانلود و اجرا کنید
(ویندوز ۱۰/۱۱، بدون نیاز به نصب — WebView2 روی ویندوز ۱۱ و اکثر ویندوزهای ۱۰ از قبل نصب است).

### 🛠️ ساخت از سورس

**پیش‌نیازها**

- **Go 1.26+** — https://go.dev/dl (نسخهٔ موجود در `go.mod`)
- **Wails CLI v2.10.1** (همان نسخهٔ `go.mod`) — نصب:
  ```bash
  go install github.com/wailsapp/wails/v2/cmd/wails@v2.10.1
  ```
- **پلتفرم:**
  - **ویندوز ۱۰/۱۱**: WebView2 Runtime + کنسول Go
  - **مک**: Xcode Command Line Tools (`xcode-select --install`)
  - **لینوکس**: `libgtk-3` و `libwebkit2gtk` (نسخهٔ ۴.۱ روی اوبونتو ۲۴.۰۴ / ۴.۰ روی توزیع‌های قدیمی‌تر)
    ```bash
    # دبیان/اوبونتو ۲۴.۰۴
    sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev
    # و برای کامپایل با پکیج ۴.۱ تگ بیلد لازم است:
    go build -tags "production webkit2_41" ./...
    ```

برای اطمینان از آماده بودن محیط: `wails doctor`

```bash
git clone https://github.com/khademdev/KodYar.git
cd KodYar

# حالت توسعه (hot-reload)
wails dev

# ساخت باینری نهایی برای سیستم خودتان
wails build

# کامپایل متقاطع برای ویندوز از لینوکس/مک (بدون نیاز به MinGW — خالص Go)
wails build -platform windows/amd64 -skipbindings -ldflags "-s -w -X main.version=1.0.1"

# خروجی این‌جا ساخته می‌شود:
#   ویندوز: build/bin/KodYar.exe
#   مک:     build/bin/KodYar.app
#   لینوکس: build/bin/KodYar
```

> نکته‌ی مهم: خروجی ویندوز (`KodYar.exe`) کاملاً خالص Go است و برای کامپایل متقاطع به ابزار
> اضافه‌ای نیاز ندارد. خروجی لینوکس به بسته‌های توسعه‌ی `libwebkit2gtk-4.1-dev` روی ماشین
> build نیاز دارد (باینریِ لینوکس باید روی همان لینوکس ساخته شود).

> پوشه‌ی `build/bin` محل خروجی کامپایل شماست؛ باینریِ آماده‌ی ویندوز در همین بسته تحویل شده است.

### 🖥️ پیش‌نمایش سریع UI در مرورگر (اختیاری)

فرانت‌اند به‌صورت مستقل هم اجرا می‌شود (با بک‌اند Mock و داده‌ی نمونه) — برای بازبینی طراحی
بدون کامپایل:

```bash
cd frontend/dist
python3 -m http.server 8899
# سپس http://127.0.0.1:8899 را باز کنید
```

داخل Wails واقعی، لایه‌ی Mock به‌صورت خودکار غیرفعال می‌شود و به بک‌اند Go وصل می‌شوید.

## ساختار پروژه

```
kodyar/
├── main.go                  # نقطه‌ی ورود Wails (پنجره، AssetServer، Bind)
├── app.go                   # هسته‌ی App + دیالوگ انتخاب پوشه
├── settings.go              # تنظیمات + ذخیره‌سازی + نمای عمومی برای فرانت‌اند
├── providers.go             # CRUD سرویس‌دهنده‌ها (با کلید API) + مهاجرت Providerهای پیش‌فرض قدیمی
├── conversations.go         # مخزن گفتگوها (هر گفتگو یک فایل JSON)
├── chat.go                  # موتور استریم SSE + حلقه‌ی ایجنت (tool calling) + Plan/Vision
├── agent.go                 # ابزارهای ایجنت: فایل/پوشه/جستجو/glob/اجرای دستور + امنیت مسیر
├── todos.go                 # ابزار todo_write — برنامهٔ کار زنده
├── webfetch.go              # ابزار web_fetch — دریافت وب با محافظت SSRF در لحظه‌ی اتصال
├── mcp.go / mcpmanager.go   # کلاینت MCP (stdio/JSON-RPC) + مدیر چرخه‌ی حیات سرورها
├── lsp.go                   # کلاینت LSP سبک (JSON-RPC/stdio) + مدیر سرور زبان (یک سرور per زبان)
├── diagtool.go              # ابزار diagnostics — خطا/هشدار فایل از gopls و typescript-language-server
├── editmatch.go             # تطبیق‌گر مقاوم edit_file (آبشار exact → EOL → فاصله → تورفتگی)
├── toolbatch.go             # اجرای موازی ابزارهای مستقل با تشخیص تداخل مسیر (سقف ۴ worker)
├── ctxbudget.go             # بودجه/فشرده‌سازی/بازیابی کانتکست آگاه از توکن
├── tokencalib.go            # کالیبراسیون تخمین توکن با مصرف واقعی
├── retry.go                 # تلاش مجدد backoff برای خطاهای موقت اتصال
├── changes.go               # چرخه‌ی تأیید Diff + بکاپ + Revert تغییرات فایل
├── diff.go                  # الگوریتم diff یکپارچه (Myers)
├── context.go               # قوانین پروژه + @mention (انتخاب هوشمند فایل)
├── history.go               # هرس تاریخچه + فشرده‌سازی + Fork + Pin
├── git.go                   # ابزارهای git (خواندنی + شاخه/کامیت مودال‌دار) + آگاهی از برنچ
├── errorlog.go              # لاگ شفاف خطاها (errors.log) + نمایشگر تنظیمات
├── security.go              # لیست سیاه دستورات + ابزارهای سفارشی + escape امن
├── commands.go              # اجرای دستور در پوشه‌ی کاری (با اجازه‌ی صریح)
├── crypto.go                # رمزنگاری AES-GCM کلیدهای API
├── helpers.go               # ابزارهای کوچک
├── evalsuite_test.go        # هارنس ارزیابی ایجنت — ۱۵ سناریو با معیار موفقیت (mock و مدل واقعی)
├── evalcompare_test.go      # مقایسهٔ دو گزارش JSON ارزیابی (نرخ موفقیت/دور/توکن/ابزار اشتباه)
├── *_test.go                # ۷۸۷ تست Go در ۱۱۹ فایل (go test ./...)
├── go.mod / go.sum
├── wails.json               # پیکربندی Wails (بدون مرحله build فرانت‌اند)
├── build/appicon.png        # آیکون اپلیکیشن
├── build/bin/               # خروجی کامپایل (KodYar.exe و …)
└── frontend/dist/           # رابط کاربری (بدون نیاز به npm؛ مستقیم embed می‌شود)
    ├── index.html
    └── assets/
        ├── css/app.css      # سیستم طراحی RTL با پالت پروژه
        ├── js/              # app، modals، changes، mention، popovers، markdown، icons، mock، slots، utils، todos، mcpsettings، …
        ├── vendor/          # marked، DOMPurify، highlight.js (آفلاین، embed شده)
        └── fonts/           # Vazirmatn + JetBrains Mono (woff2 محلی)
```

## داده‌ها و تنظیمات

تنظیمات و گفتگوها در پوشه‌ی استاندارد پیکربندی کاربر ذخیره می‌شوند:

| سیستم‌عامل | مسیر |
|---|---|
| ویندوز | `%AppData%\KodYar` |
| مک | `~/Library/Application Support/KodYar` |
| لینوکس | `~/.config/KodYar` |

- `settings.json` — تنظیمات، Providerها و کلیدهای API (**رمزنگاری‌شده** با AES-GCM بر پایه‌ی
  مشخصات ماشین)
- `conversations/*.json` — گفتگوها

برای بازنشانی کامل، کافی است این پوشه را حذف کنید.

## معماری ارتباط فرانت‌اند و بک‌اند

- متدهای `App` (فایل‌های Go) به‌صورت خودکار توسط Wails به `window.go.main.App` بایند می‌شوند
  (`SettingsView`، `SendMessage`، `SaveConnection`، `SaveProvider`، `SelectFolder`، …)
- استریم پاسخ با **رویدادهای Wails** به فرانت‌اند می‌رسد:
  - `chat:start` — شروع پاسخ (همراه گفتگوی تازه‌ساخته‌شده)
  - `chat:delta` — هر تکه‌ی متن
  - `chat:tool` / `chat:tool_result` — شروع/پایان هر فراخوانی ابزار ایجنت
  - `chat:notice` — اطلاعیه‌ها (مثلاً پشتیبانی‌نشدن ابزار توسط مدل)
  - `chat:done` — پایان (با مصرف توکن)
  - `chat:error` — خطا
- هر API سازگار با OpenAI (`/chat/completions`) پشتیبانی می‌شود؛ حالت ایجنت از **فراخوانی تابع
  (Function Calling)** استاندارد OpenAI استفاده می‌کند، پس مدل باید ابزار را پشتیبانی کند
  (gpt-4o، llama-3.3، qwen2.5 و…). در صورت پشتیبانی‌نکردن، برنامه به‌صورت خودکار به چت معمولی برمی‌گردد.
  علاوه بر این، هر سرویسی (OpenRouter، Together، سرویس‌های ایرانی و…) از تب
  «Provider ها» قابل افزودن است.

## میان‌برها

| میان‌بر | عملکرد |
|---|---|
| `Enter` | ارسال پیام |
| `Shift+Enter` | خط جدید |
| `Ctrl + ,` | باز کردن تنظیمات |
| `Esc` | بستن مودال‌ها و پاپ‌اورها |

## MCP داخل سندباکس

سرورهای MCP هم مثل دستورهای شل داخل AppContainer اجرا می‌شوند: SID اختصاصی،
ACL فقط روی پوشه‌ی کاری و خانه‌ی ایزوله، Job Object با KILL_ON_JOB_CLOSE،
محیط حداقلی و شبکه فقط وقتی «اجازه‌ی شبکه» روشن باشد. stdin/stdout/stderr سه
pipe واقعی هستند، پس JSON-RPC خط‌به‌خط و ابزارهای سرور با سندباکس روشن هم کار
می‌کنند. اگر آماده‌سازی سندباکس شکست بخورد، سرور اجرا نمی‌شود (fail-closed) و
خطا در وضعیت همان سرور نمایش داده می‌شود؛ هیچ‌وقت بیرون از سندباکس بالا نمی‌آید.

## مستندات

| سند | موضوع |
|---|---|
| [CHANGELOG.md](CHANGELOG.md) | تاریخچهٔ کامل تغییرات نسخه‌ها |
| [ARCHITECTURE.md](ARCHITECTURE.md) | معماری زیرسیستم‌ها و تصمیم‌های طراحی |
| [BUGFIXES.md](BUGFIXES.md) | فهرست باگ‌های رفع‌شده و علت ریشه‌ای |

## مجوز / License

این پروژه تحت **مجوز MIT** منتشر می‌شود — متن کامل در [LICENSE](LICENSE).
استفاده، تغییر و توزیع آزاد است به شرط حفظ اعلان کپی‌رایت؛ نرم‌افزار «همان‌گونه که هست»
و بدون هیچ ضمانتی ارائه می‌شود.

**توسعه‌دهنده و ارتباط:** Meysam Khadem Shams — [@khademdev](https://github.com/khademdev) · [meysam76shams@gmail.com](mailto:meysam76shams@gmail.com)

<p align="center">
  اگر KodYar به کارتان آمد، یک ⭐ به مخزن و یک ☕ در
  <a href="https://www.coffeebede.com/khademdev">CoffeeBede</a>
  بهترین تشکر است.
</p>

</div>
