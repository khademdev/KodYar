# MyAI — Final Report (Finish-the-Project)

Autonomous completion of MyAI 1.6.0 → **1.7.0**, followed by a
user-requested **full-project audit (v1.7.1)**: backend + frontend +
resource consumption + reliability, ending in a release ZIP. Work
performed in a Linux sandbox (Debian 13, Go 1.26.10, gcc 14 → real
`-race`); Windows-specific behavior verified by cross-vet, reading
platform files, and the manual checklist below. Per-stage details:
`PROGRESS.md`; commit history: git log.

## 0. Final audit (1.7.1) — summary

Two independent deep audits (Go backend, frontend JS/CSS/HTML) plus a
data-boundary security trace found **16 real defects**, all fixed with
regression tests (9 new; suite 360 → **369**), gate GREEN. Highlights:

- **CRITICAL (frontend):** the permission-ask modal never became visible
  (missing `.open` class) — every permission request auto-rejected after
  the 3-minute timeout. Fixed + regression-tested.
- **HIGH (MCP):** crash-recovery was dead code (`onClientExit` never
  called) — a crashed server was permanently unusable with status stuck
  at "connecting"; stdin writes to a hung server could wedge the whole
  agent turn beyond even Stop's reach. Both fixed with end-to-end tests
  (new `fake-die`/`fake-silent` fake-server modes).
- **MEDIUM:** git commands could hang past their timeout (no WaitDelay);
  the sensitive-read modal was uncancellable; rules files were read in
  full under `a.mu`; dying MCP servers could lose their final responses.
- **Security (data boundary):** a shared/planted `settings.json` or
  conversation file could inject script into the WebView (full Go-bridge
  access) via unescaped id/color interpolations — provider fields are now
  sanitized on load/rejected on save, and the conversation index only
  accepts pattern-valid ids.
- **Frontend:** todos panel went stale on conversation switch; the MCP
  editor showed pre-save config and could silently revert edits.
- Resource hardening: real single-flight tool refresh, bounded rules
  reads, capped ignore-stack, pending-map cleanup, FD-leak fix on the MCP
  attach-error path, shutdown waits for the in-flight save.

## 1. Status per stage

| Stage | Scope | Status | Gate (last run) |
|---|---|---|---|
| 0 | Green baseline + repo hygiene | **DONE** | GREEN |
| 1 | `todo_write` + live checklist UI | **DONE** | GREEN |
| 2 | `edit_file` multi-edit + glob mtime ordering | **DONE** | GREEN |
| 3 | `web_fetch` with SSRF protection | **DONE** | GREEN |
| 4 | Platform-aware prompt, RunCommand ctx, CI | **DONE** | GREEN |
| 5 | MCP client (stdio) + manager | **DONE** | GREEN |
| 6 | Consistency, renames, docs, 1.7.0 | **DONE** | GREEN |

Final gate output (verbatim, last run):

```
=== gofmt -l . ===                [OK] (empty)
=== go vet ./... ===              [OK]
=== go build ./... ===            [OK]
=== go test -race -count=1 ./... === ok  myai  13.501s  [OK]
=== env GOOS=windows GOARCH=amd64 go vet ./... === [OK]
=== env GOOS=linux   GOARCH=amd64 go vet ./... === [OK]
GATE: GREEN
```

**Race status: fully verified** — every stage ran `go test -race` with a
real C toolchain; no stage is race-unverified. (One genuine data race on
the MCP client `closed` flag was found by the detector and fixed.)

Suite size grew from **262 test functions / 31 files** to **360 / 41**.

## 2. Files added/changed (grouped by stage)

**Stage 0** — `rulesfiles_test.go` (mtime-resolution fix), `.gitignore`
(`/myai` entry), `bin/.gitkeep`, `build.sh` (recreated — see assumptions),
`scripts/gate.sh`, `PROGRESS.md`.

**Stage 1** — new: `todos.go`, `todos_test.go`,
`frontend/dist/assets/js/todos.js`, `frontend/dist/assets/css/todos.css`.
Changed: `agent.go` (tool def + dispatch + prompt rule 9), `toolguard.go`
(spec + `"todos"` type), `permissions.go` (default allow),
`conversations.go` (`Todos` field), `history.go` (fork deep-copy),
`app.go` (`todosEmit` hook), `index.html`, minified `app.js` +
`mock.js` (tiny additive hooks), `models.ts` (TodoItem).

**Stage 2** — `agent.go` (editOp/applyEditOp/readEditSource/
toolEditFileArgs/parseEditOps/toolEditFileMulti; glob rewritten to
mtime-desc with 20 000-candidate bound and 500-result cap),
`toolguard.go` (`edits` spec), `multiedit_test.go`,
`globorder_test.go`, `scripts/stage2_*.py` (patch scripts).

**Stage 3** — new: `webfetch.go`, `webfetch_test.go`. Changed:
`agent.go`, `toolguard.go`, `permissions.go` (`web_fetch` → ask,
`permissionAsk.URL`), `app.go` (`permAskEmit` hook), readable
`permissions.js` (url display hook), minified `app.js` (icon + args
summary), `go.mod` (x/net + x/text promoted to direct).

**Stage 4** — `agent.go` (platform-aware `agentSystemPromptForOS` +
`shellInfoForOS`), `commands.go` (`appContext()`), `platform_test.go`,
`.github/workflows/ci.yml`.

**Stage 5** — new: `mcp.go`, `mcpmanager.go`, `mcp_test.go`,
`mcp_posix_test.go`, `mcpfakeserver_test.go`,
`frontend/dist/assets/js/mcpsettings.js`. Changed: `app.go` (`mcp`
field, shutdown), `settings.go` (`MCPServers`, masking, defaults),
`chat.go` (MCP tools in streamChat), `agent.go` (MCP dispatch + modal
payload), `permissions.go` (explicit `mcp_` ask), `index.html` (MCP tab),
`app.css` (status rows), bindings (`App.js`, `App.d.ts`, `models.ts`).

**Stage 6** — `toolregistry_test.go`, renamed test files
(`gitcontext/atomicwrites/tempsweep/procgroup_posix_test.go`),
`scripts/stage6_renumber.py`, `README.md`, `ARCHITECTURE.md`,
`CHANGELOG.md` (new), version bump in `main.go`/`wails.json`/
`build.sh`/CI.

## 3. Tests added (what they prove)

- **`todos_test.go`** — validation table (all error paths, JSON-string
  arrays), **Persian content with ZWNJ truncated exactly on the rune
  boundary** (`utf8.ValidString` asserted), auto-id uniqueness,
  persistence round trip through the full `execToolCtx` path, **old
  conversation file without the field loads**, fork deep-copy, event
  fired once per successful write (+ payload conv-id, none on failure),
  16-goroutine concurrent-write race (file stays valid JSON), permission
  default + deny override, registration in every wiring point.
- **`multiedit_test.go`** — 3 dependent edits applied sequentially,
  failure on edit 2 leaves the file **byte-identical**, ambiguity error
  names the edit index, CRLF + BOM preserved, Persian/ZWNJ content,
  20-edit cap + empty rejection + result-size cap, loose JSON-string
  `edits`, mutual exclusion with precise errors, **single-edit form
  byte-identical backward compatibility (exact old messages asserted)**,
  tolerant cascade with re-indent inside multi-edit.
- **`globorder_test.go`** — newest-first ordering with ≥2 s mtime steps,
  path tie-break, nested paths, 500-result cap after sorting, 20 000-
  candidate bound notice, filtering + cancellation still enforced.
- **`webfetch_test.go`** — IP classification table (36 cases: v4/v6/
  mapped/NAT64/edge ranges with reasons), **real guard blocks a
  loopback httptest URL**, redirect-to-denied-port blocked, **DNS-
  rebinding style** (`localhost` hostname resolved to loopback is
  blocked), scheme/userinfo/empty-host rejection, 2 MiB body cap,
  content-type allow-list + rejection naming the type, HTTP status
  errors, **Windows-1256 Persian page decoded (incl. ZWNJ from 0x9D)**,
  ISO-8859-6 Arabic, HTML→text structure (headings/lists/links,
  drop-tags), rune-safe truncation at 777 chars with clamps,
  **closing-tag escape** (exactly one real `</fetched_content>`), ctx
  cancel mid-body (deterministic, handler blocks on request ctx),
  permission default + **modal payload captured via the permAskEmit
  hook (URL + Persian message)** followed by a successful fetch after
  «allow».
- **`platform_test.go`** — per-OS prompt content (windows/linux/darwin,
  wrong-platform hints absent), production prompt follows `runtime.GOOS`,
  shell-info/runShell consistency, **cancelling the app lifetime context
  aborts a pending permission ask** (RunCommand no longer hangs on
  shutdown).
- **`mcp_test.go` / `mcp_posix_test.go` / `mcpfakeserver_test.go`** —
  fake MCP server via the standard re-exec pattern (handshake, paginated
  tools/list, text/image/error/Persian/huge/hanging tools, crash,
  child-of-child spawn, server→client requests): handshake + pagination
  + `[MCP:fake]` exposure ($schema dropped, type object), bad protocol
  version fails clearly, disabled server exposes nothing («غیرفعال»),
  text/image-placeholder/`isError`/**Persian-with-ZWNJ byte-exact
  through the pipe**/output-cap calls, hanging-tool timeout (injectable,
  default 60 s), user-cancel promptness + `notifications/cancelled`,
  ping/`-32601` replies, crash → backoff 1s/2s/4s → give-up after 3 →
  reset on settings re-save, unexpected exit fails fast, name
  sanitization/64-char cap/stable hash, **env encryption + UI masking
  round trip (mask keeps ciphertext) + old-settings fixture**, Persian
  validation errors, permission default + registrations, **shutdown
  kills the whole process tree (child-of-child proven via
  `syscall.Kill(-pgid, 0)`)**, goroutine-leak check, untrusted-wrapper
  escaping, Persian status labels, concurrent single-flight start.
- **`toolregistry_test.go`** — registry consistency across all six
  wiring points incl. **parsing the minified app.js as text** for
  icon/label coverage, MCP icon fallback, and read-only-only parallel /
  sub-agent lists.

## 4. Dependencies added

- `golang.org/x/net v0.61.0` (html + html/charset) and
  `golang.org/x/text v0.43.0` (charmap, tests) — **promoted from
  indirect to direct**; these exact versions were already pinned in
  go.sum via Wails, so no new modules enter the build. Justification:
  the stdlib has neither an HTML tokenizer nor Windows-1256/ISO-8859-6
  decoders, both required by `web_fetch` (BSD-3-Clause, part of the Go
  ecosystem). No other new dependencies.

## 5. Assumptions, deviations, known limitations, future work

**Assumptions / deviations**

- The zip contained **no `.git`** and no `build.sh`; git was re-inited
  (first commit = extracted tree) and `build.sh` was **recreated** from
  the README-documented build command (not restored from history).
- «۲ از ۵» progress = completed/total; default `web_fetch` max_chars =
  20 000 (clamped [500..100 000]); glob result cap raised 300 → 500 per
  the spec's explicit number; the 60 s MCP per-call timeout is a
  configurable var (shortened in tests only).
- Go 1.26.10 was installed into the sandbox (checksum-verified from
  go.dev) because the environment had no Go toolchain at all.
- The MCP settings tab is a 9th tab in the settings modal; its content
  lives entirely in the new readable `mcpsettings.js`.

**Known limitations**

- The CI workflow **was not executed** (no GitHub runner in the sandbox)
  — validated by offline YAML parse + structural assertions + reading
  the Wails build-tag sources (`production` pulls the GTK cgo frontend;
  `webkit2_41` selects the 4.1 pkg-config name — verified empirically
  that tagged builds fail here only due to missing system packages).
- The process-tree regression test is Unix-only (`//go:build !windows`);
  the Windows Job-Object path is compile-checked and in the manual
  checklist.
- Real-Windows runtime behavior (WebView rendering, real keyring,
  `.cmd` resolution of `npx`, Job-Object kill) was not executed here.

**Future work**

- **PowerShell / Git-Bash shell selection deliberately NOT implemented**
  — the hard-block patterns in `permissions.go` are written for cmd /
  POSIX syntax; supporting other shells without re-auditing them would
  weaken security.
- Possible follow-ups (out of scope): per-URL «always allow» for
  web_fetch, MCP server auto-discovery, streaming MCP tool output.

## 6. Manual test checklist for the human (Persian UI)

**چک‌لیست دستی روی ماشین ویندوزی — مراحل دقیق و نتیجه‌ی مورد انتظار:**

1. **چک‌لیست کارهای ایجنت**: حالت ایجنت را روشن کنید و یک کار چندمرحله‌ای
   بدهید («سه فایل README بساز، هر کدام را تست کن و سپس جمع‌بندی بنویس»).
   *انتظار*: بالای جعبه‌ی پیام پنل «کارهای ایجنت» با وضعیت‌ها ظاهر می‌شود؛
   همزمان با کار ایجنت آیتم‌ها «در حال انجام/انجام شد» می‌شوند و شمارنده‌ی
   «n از m» به‌روز می‌شود؛ برنامه را ببندید و همان گفتگو را باز کنید —
   چک‌لیست دقیقاً برمی‌گردد. «گفتگوی تازه» پنل را پاک می‌کند. جمع‌شدن/
   بازشدن با کلیک روی سربرگ کار می‌کند.
2. **ویرایش چندگانه**: یک فایل متنی بسازید و از ایجنت بخواهید «سه تغییر
   مختلف در یک فراخوانی با ابزار edit_file و آرایه‌ی edits اعمال کند»؛
   سپس بخواهید یکی از ویرایش‌ها عمداً متن ناموجود بفرستد.
   *انتظار*: حالت موفق یک مودال Diff (یک بکاپ/یک تأیید) و هر سه تغییر
   نشان می‌دهد؛ حالت ناموفق فایل را **بایت‌به‌بایت دست‌نخورده** می‌گذارد و
   پیام خطا شماره‌ی ویرایش ناموفق را می‌گوید.
3. **glob و Stop**: در پوشه‌ی پروژه‌ای بزرگ `glob **/*` بگیرید.
   *انتظار*: نتایج جدیدترین اول است (یک فایل را لمس کنید و دوباره
   بگیرید — اول می‌آید)؛ حین پاسخ دکمه‌ی «توقف» را بزنید — پاسخ فوراً با
   «لغو شد» تمام می‌شود.
4. **web_fetch**: از ایجنت بخواهید یک صفحه‌ی مستندات واقعی (مثلاً
   `https://pkg.go.dev/fmt`) را بخواند؛ سپس `http://127.0.0.1:8080` و
   `http://169.254.169.254/latest/meta-data/` را بدهید؛ در پایان یک صفحه‌ی
   فارسی با کدگذاری windows-1256.
   *انتظار*: صفحه‌ی عادی با مودال «اجازه‌ی دریافت این آدرس را می‌دهید؟» و
   آدرسِ جدا/چپ‌به‌راست شروع می‌شود و متن خوانا برمی‌گردد؛ آدرس‌های
   127.0.0.1 و 169.254.169.254 **حتماً با خطای اتصال مسدود می‌شوند**؛
   صفحه‌ی فارسی قدیمی با حروف درست (بدون علامت‌سؤال) نمایش داده می‌شود.
5. **MCP**: در تنظیمات ← تب «MCP» یک سرور اضافه کنید (مثلاً
   `{"name":"fs","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","C:\\temp"],"enabled":true}`)
   و ذخیره کنید؛ از ایجنت بخواهید فایلی را با ابزار آن سرور بخواند؛ سپس
   سرور را غیرفعال کنید؛ در آخر یک سرور با فرمان اشتباه بسازید.
   *انتظار*: پس از ذخیره، وضعیت «در حال اتصال» و سپس «متصل» + «n ابزار»؛
   فراخوانی ابزار با مودال «اجازه‌ی فراخوانی ابزار … از سرور MCP …؟» و
   آرگومان‌ها در جعبه‌ی چپ‌به‌راست؛ غیرفعال‌کردن وضعیت را «غیرفعال» می‌کند؛
   فرمان اشتباه بعد از سه تلاش «خطا» می‌شود و با ذخیره‌ی دوباره دوباره
   تلاش می‌کند. **میان فراخوانی، فرایند سرور را در Task Manager بکشید** —
   فراخوانی فوراً خطای انگلیسی می‌دهد. **برنامه را ببندید و در Task Manager
   هیچ فرایند node/npx جامانده نباشد.**
6. **پرامپت پلتفرمی**: روی ویندوز از ایجنت بپرسید «چه دستوری برای دیدن
   فایل‌ها استفاده می‌کنی؟». *انتظار*: پاسخ به CMD اشاره می‌کند (dir/type)
   نه دستورهای لینوکسی.
7. **ری‌استارت با تنظیمات قدیمی**: پوشه‌ی `%AppData%\MyAI` یک فایل
   settings.json بدون فیلد `mcpServers` و یک گفتگوی قدیمی بدون `todos`
   بگذارید و برنامه را باز کنید. *انتظار*: بدون خطا بالا می‌آید؛ گفتگوی
   قدیمی کامل خوانده می‌شود و چک‌لیست خالی است؛ همه‌ی تنظیمات قبلی
   (Providerها/کلیدها/دسترسی‌ها) سر جایشان‌اند.

---

# Mission 2 + Mission 3 — «اعتمادپذیری ایجنت» و «اتمام کارهای باقی‌مانده» (1.7.1 → **1.8.0**) — DONE (2026-11-04)

مشخصات در سند مأموریت؛ لاگ کامل هر مرحله با خروجی واقعی گیت: `PROGRESS.md`
(بخش‌های Mission 2 و Mission 3). وضعیت نهایی هر مرحله:

## 1. وضعیت مراحل

| مرحله | دامنه | وضعیت |
|---|---|---|
| M2-1 | دروازه‌ی دستورات شله‌آگاه (shellparse + cmdgate + permissions/security) | **DONE** |
| M2-2 | حالت‌های ویرایش + مسیرهای محافظت‌شده + بازبینی پایان نوبت + بایندینگ‌ها | **DONE** |
| M3-0 | پایه: گیت قرمز پایه رفع شد (مسیر ویندوزی + تست MCP هنگ‌کننده) + بازبینی «انجام‌شده‌ها» | **DONE** |
| M3-A | دقت ایجنت: خواندن‌پیش‌از‌نوشتن/کهنه، تذکر راستی‌آزمایی، واقعیت‌های پروژه، اعمال سقف توکن، قواعد پرامپت + کارها در نشانگر فشرده‌سازی | **DONE** |
| M3-B | نقطه‌ی بازگشت پروژه با git موقت + پیش‌نمایش/بازگردانی بدون حذف + سبد زباله + UI | **DONE** |
| M3-C | سخت‌سازی: web_fetch per-host، flush لینک HTML، محدوده‌های SSRF اضافی، env فرزند MCP، `.git` تودرتو، بهداشت مخزن + اصلاح گیت (gofmt واقعاً سنجیده شود) | **DONE** |
| M3-D | اثبات و انتشار: ۱۱ سناریوی e2e، تست قرارداد UI + تست مرورگری Playwright + CI، مستندات «مدل امنیتی»، نسخه 1.8.0، خودبازبینی | **DONE** |

**گیت نهایی (خروجی واقعی، آخرین اجرا):**

```
=== gofmt -l . ===                [OK] (خالی)
=== go vet ./... ===              [OK]
=== go build ./... ===            [OK]
=== go test -race -count=1 ./... === ok  myai  22.096s  [OK]
=== env GOOS=windows GOARCH=amd64 go vet ./... === [OK]
=== env GOOS=linux   GOARCH=amd64 go vet ./... === [OK]
GATE: GREEN
```

وضعیت race: **کاملاً راستی‌آزمایی‌شده** (gcc 14.2 واقعی؛ هیچ مرحله‌ای race-unverified نیست).
مجموعه‌ی نهایی: **۴۷۸ تابع تست در ۵۱ فایل** (ورودی Mission 2: ۳۶۹/۴۱).
تست مرورگری Playwright: ۴ سناریو در کرومیوم واقعی **سبز** (همین محیط).

## 2. فایل‌های افزوده/تغییرکرده در Mission 3 (به تفکیک مرحله)

- **M3-0/A:** جدید: `fileread.go`، `projectfacts.go`، `fileread_test.go`،
  `projectfacts_test.go`، هسته‌ی `agent_e2e_test.go` (هارنس SSE فیلم‌نامه‌ای + کاربر فیلم‌نامه‌ای). تغییر: `chat.go`
  (تذکر/سقف/واقعیت‌های پروژه/نشانگر حامل کارها)، `changes.go` + `turnreview.go` (سیم‌کشی محافظ)، `agent.go`
  (ثبت اثر انگشت در read/write/move/delete)، `context.go`/`ctxbudget.go`، `settings.go`، `history.go`.
- **M3-B:** جدید: `checkpoint.go`، `checkpoint_test.go`؛ UI در `turnsummary.js` + مودال
  `checkpoint-overlay`؛ بایندینگ دستی `PreviewCheckpointRestore`/`RestoreCheckpoint` (App.js/App.d.ts/models.ts)؛
  sweep سبد زباله در startup.
- **M3-C:** جدید: `webfetchperm.go`، `webfetchperm_test.go`؛ تغییر: `webfetch.go` (flush لینک + SSRF ranges)،
  `mcp.go` (دو گروه env)، `permissions.go` (defence-in-depth برای web_fetch)، `settings.go` (مهاجرت)،
  `turnreview.go` (`**/.git/**`)، `scripts/gate.sh` (gofmt باید خالی باشد)، انتقال `FINAL_REPORT.md`/`PROGRESS.md`
  به `docs/dev/`، حذف اسکریپت‌های توسعه‌ای مرحله‌ای.
- **M3-D:** جدید: `uicontract_test.go`، `frontend/tests/` (serve.js، smoke.spec.js،
  playwright.config.js، package.json، README.md)، بقیه‌ی سناریوهای `agent_e2e_test.go`. تغییر: `mcp_test.go`
  (اصلاح تستِ اشتباهِ قابل‌اثبات)، `.github/workflows/ci.yml` (job مرورگری غیربلاک‌کننده + اصلاح
  `branches: [main]` خراب)، `README.md` (مدل امنیتی دوزبانه + تازه‌های 1.8.0)، `ARCHITECTURE.md`،
  `CHANGELOG.md`، نسخه 1.8.0 در `main.go`/`wails.json`/`build.sh`/CI.

## 3. تست‌های افزوده در Mission 3 (چه چیزی را اثبات می‌کنند)

- `fileread_test.go` (۹ تابع) — رد بازنویسی ناخوانده/کهنه، مجاز بودن ساخت، BOM+CRLF+فارسی/ZWNJ
  بایت‌به‌بایت، جداسازی زیرایجنت، LRU و گفتگوها، مسیرهای فارسی با فاصله/نیم‌فاصله، move/delete، عدم نشت بین گفتگوها.
- `projectfacts_test.go` — همه‌ی اکوسیستم‌ها، فایل‌های خراب/غول، ابطال کش با mtime (گام ≥۲ ثانیه)، مسیرهای فارسی.
- `agent_e2e_test.go` — تذکر (یک‌بار/پس از تست شکست‌خورده/بدون تغییر)، سقف توکن (توقف/هشدار یک‌بار/زیرایجنت/صفر/ادامه)،
  نشانگر حامل کارها (idempotent)، و ۱۱ سناریوی D.1 با اثبات روی دیسک/رویدادها/بدنه‌ی درخواست (جزئیات در PROGRESS.md، ورودی Stage D).
- `checkpoint_test.go` — ساخت نقطه‌ی بازگشت بدون دست‌زدن به HEAD/ایندکس/status، بازگردانی بایت‌به‌بایت، سبد زباله،
  ignore، فارسی/CRLF/BOM، prune=۲۰، غیر-git بی‌اثر، لغو، رد مسیر فرار.
- `webfetchperm_test.go` + بخش‌های `webfetch_test.go`/`mcp_test.go`/`turnreview_test.go` — per-host،
  محدوده‌های SSRF، env فرزند MCP، `.git` تودرتو.
- `uicontract_test.go` — قرارداد UI (idها، قاعده‌ی visibility، همگامی بایندینگ‌ها، پوشش مودال‌های جدید).
- `frontend/tests/smoke.spec.js` — مودال Diff/دسترسی/کارت خلاصه/مودال نقطه‌ی بازگشت واقعاً
  visible+کلیک‌پذیر با بریج mock (اجراشده در کرومیوم).
- **وابستگی جدید: هیچ.**

## 4. تغییرات رفتاری که کاربر می‌بیند

- نوشتن روی فایلِ نخوانده/کهنه با پیام خطای انگلیسیِ «دوباره بخوان» رد می‌شود؛ ویرایش فایلِ نخوانده فقط یادآوری می‌دهد.
- پایان نوبتِ بدون تست/ساختِ موفق → یک تذکر یک‌باره در حلقه‌ی ایجنت + هشدار در کارت خلاصه‌ی نوبت.
- «سقف توکن هر نوبت» حالا واقعاً توقف می‌کند (با اعلان فارسی و امکان «ادامه») و در ۸۰٪ هشدار می‌دهد.
- کارت خلاصه‌ی نوبت: دکمه‌ی «بازگردانی کل پروژه به قبل از این نوبت» + چیپ «سقف توکن این نوبت پر شد».
- web_fetch: مودال با سه گزینه‌ی هر دامنه؛ «همیشه برای کل ابزار» دیگر وجود ندارد و قاعده‌ی قدیمی به پرسش برمی‌گردد (مهاجرت).
- مسیرهای محافظت‌شده (حالا شامل `src/.git/config` یعنی submodule) حتی در حالت خودکار مودال می‌گیرند.
- اعلان‌های فارسی: فشرده‌سازی خودکار کانتکست، هشدار/توقف سقف توکن، رد شدن نقطه‌ی بازگشت (بدون git یا خطا).
- **مهاجرت:** settings.json و گفتگوهای قدیمی بدون تغییر بار می‌شوند؛ `tools.web_fetch="allow"` قدیمی به `ask`
  برمی‌گردد و در لاگ خطاها ثبت می‌شود؛ `confirmFileWrites` قدیمی به حالت ویرایش معادل مهاجرت می‌کند.

## 5. فرض‌ها، انحراف‌ها، محدودیت‌ها و کارهای آینده

**فرض‌ها/انحراف‌های کلیدی (فهرست کامل در PROGRESS.md):**
- سناریوی D.1.5: «echo %COMSPEC%» در شله‌ی POSIX طبق کورپوس بازبینی‌شده‌ی Stage 1 مجاز است
  (واژه‌ای و بی‌خطر در sh) — فقط همان یک جفت، با خروجی واژه‌ای اثبات‌شده؛ ۱۵ جفت دیگر: اجرای صفر.
- `TestMCPChildEnvAllowList` قابل‌اثباتاً اشتباه بود (مقایسه‌ی case-folded وقتی دو واریانت مستقل روی لینوکس ست می‌شوند)
  — عمداً اصلاح شد و در PROGRESS.md ثبت شد.
- سناریوی «فشرده‌سازی میانه‌ی نوبت» به ساختار واقعی فاز B (حذف فقط قبل از آخرین کاربر + حداقل ۱۲ پیام) طراحی شد.
- تست مرورگری هم‌زمان کشف کرد که پنل تغییرات با اولین badge معلق خودکار باز می‌شود (رفتار عمدی برنامه) — تست‌ها این را می‌بندند.

**محدودیت‌های شناخته‌شده و کارهای آینده (صادقانه):**
- **دستورها sandbox نشده‌اند** — گیت سه‌لایه دارد اما دستور تأییدشده با دسترسی کامل کاربر اجرا می‌شود؛
  سندباکس اختیاری Docker/WSL2 ویژگی آینده است.
- **انتخاب PowerShell / Git-Bash پیاده‌سازی نشده** — الگوهای سختِ مسدود برای cmd/POSIX نوشته شده‌اند؛
  پشتیبانی از شله‌های دیگر بدون بازبینی امنیتی، امنیت را ضعیف می‌کند.
- GitHub Actions اجرا نشد (رانر نیست)؛ رفتار واقعی ویندوز (WebView2، Job-Object، مسیرهای فارسی در CMD)
  با vet متقاطع + چک‌لیست دستی زیر پوشش داده می‌شود.

## 6. چک‌لیست پذیرش دستی برای انسان (UI فارسی — مراحل دقیق و نتیجه‌ی مورد انتظار)

1. **(a) حالت خودکار بدون مودال:** در تنظیمات ← «پوشه‌ی کاری» حالت ویرایش را «ویرایش خودکار» بگذارید؛ در یک مخزن git از ایجنت بخواهید یک قابلیت کوچک بسازد و `go test` را اجرا کند. *انتظار:* بدون هیچ مودالی کار می‌کند؛ پایان کار کارت «خلاصه‌ی واقعی این نوبت» فایل‌ها و راستی‌آزمایی موفق را نشان می‌دهد.
2. **(b) حالت بازبینی هر تغییر:** حالت را «بازبینی هر تغییر» بگذارید و همان کار را بخواهید. *انتظار:* مودال Diff با پیش‌نمایش می‌آید، دکمه‌ها کلیک‌پذیرند و پس از «اعمال تغییر» جریان ادامه می‌یابد.
3. **(c) تلاش دورزدن دروازه:** از ایجنت بخواهید (و در جعبه‌ی اجرای دستی هم امتحان کنید):
   `echo ok & npm install x`، `echo x > ..\out.txt`، `type .env`. *انتظار:* هر سه با مودال اجازه (یا رد ساختاری) مواجه می‌شوند؛ «همیشه اجازه» برای تغییرمسیر و فایل حساس پیشنهاد نمی‌شود؛ هیچ فایلی بیرون از پروژه ساخته نمی‌شود.
4. **(d) مسیر محافظت‌شده در حالت خودکار:** بخواهید `package.json`، `.env` یا `src/.git/config` (submodule) را ویرایش کند. *انتظار:* با وجود حالت خودکار، مودال Diff با یادداشت «این مسیر محافظت‌شده است…» می‌آید.
5. **(e) محافظ خواندن‌پیش‌از‌نوشتن:** بخواهید فایلی را که هرگز نخوانده بازنویسی کند، و در میانه‌ی یک کار خودتان فایلی را که خوانده عوض کنید. *انتظار:* هر دو با پیام انگلیسی «read_file دوباره…» رد می‌شوند و محتوای شما محفوظ می‌ماند.
6. **(f) تذکر راستی‌آزمایی:** نوبتی را بدون اجرای تست تمام کنید. *انتظار:* تذکر دقیقاً یک‌بار در جریان می‌آید («build/test موفق اجرا نشده…») و کارت خلاصه هشدار می‌دهد.
7. **(g) برگرداندن نوبت و تعارض:** «برگرداندن همه‌ی تغییرات این نوبت» را بزنید؛ سپس خودتان فایلی را عوض کنید و دوباره برگردانید. *انتظار:* بار اول بایت‌به‌بایت برمی‌گردد؛ بار دوم فایلِ شما دست‌نخورده می‌ماند و در فهرست «تعارض» می‌آید.
8. **(h) نقطه‌ی بازگشت و سبد زباله:** در یک مخزن git یک نوبت شامل ویرایش + یک دستور تغییردهنده (مثل `echo x > gen.txt`) اجرا کنید، سپس «بازگردانی کل پروژه به قبل از این نوبت» را بزنید. *انتظار:* پیش‌نمایش rewrite/trash/unchanged را نشان می‌دهد؛ پس از بازگردانی درخت بایت‌به‌بایت برمی‌گردد و نسخه‌های نجات‌یافته در `%AppData%\MyAI\trash\…` هستند؛ HEAD/ایندکس/`git status` کاربر در طول کار دست‌نخورده مانده‌اند.
9. **(i) سقف توکن:** «سقف توکن هر نوبت» را مقدار کوچکی بگذارید و کاری بزرگ بخواهید. *انتظار:* اعلان ۸۰٪ یک‌بار؛ سپس توقف مودبانه با اعداد فارسی؛ دکمه‌ی «ادامه» کار می‌کند.
10. **(j) قانون هر دامنه‌ی web_fetch:** یک صفحه‌ی وب بخواهید و «همیشه برای این دامنه» را بزنید. *انتظار:* در تب «دسترسی‌ها» فقط همین دامنه ذخیره می‌شود؛ دامنه‌ی دوم دوباره می‌پرسد؛ گزینه‌ی «همیشه برای کل ابزار» وجود ندارد.
11. **(k) بستن برنامه:** پس از پایان کار، برنامه را ببندید و Task Manager را چک کنید. *انتظار:* هیچ فرایند node/git/MCP جامانده‌ای نماند.
12. **(l) تنظیمات و گفتگوهای قدیمی:** با settings.json قدیمی (بدون فیلدهای جدید، با `confirmFileWrites`) و گفتگوهای قدیمی اجرا کنید. *انتظار:* همه‌چیز بار می‌شود؛ حالت ویرایش معادل مهاجرت می‌کند؛ قاعده‌ی سراسری web_fetch قدیمی به «پرسش» برمی‌گردد و در لاگ خطاها ثبت شده است.

---

# Mission 4 — «پایداری و حذف نقص؛ بدون قابلیت تازه» (1.8.0 → **1.8.1**) — DONE (2026-11-06)

سند مأموریت: `MyAI_Stability_Prompt.md`؛ لاگ کامل هر مرحله با خروجی واقعی گیت:
`PROGRESS.md` (بخش‌های Mission 4). مأموریت فقط پایداری بود — **هیچ قابلیت،
ابزار، تنظیمِ UI یا وابستگی تازه‌ای اضافه نشد** (`go.mod` در هیچ کامیت Mission 4
دست نخورد). محیط: همان sandbox لینوکسی (Go 1.26، gcc 14.2 → `-race` واقعی)؛
رفتار خاص ویندوز فقط با vet متقاطع، تست‌های مستقل از سکو و چک‌لیست دستی بند ۷
زیر پوشش دارد.

## 1. وضعیت مراحل

| مرحله | دامنه | وضعیت |
|---|---|---|
| 0 | مبنا: گیت RED سه‌جا — ریشه تست‌های وابسته به سکو بود، نه کد محصول | **DONE** |
| 1 | جداسازی خرابی: `safego.go` + نگهبان همه‌ی متدهای متصل به Wails | **DONE** |
| 2 | نگهبان سکون استریم (پایان «فکر کردن بی‌پایان») | **DONE** |
| 3 | نوشتن اتمیک مقاوم + بازیابی داده (`.bak`/نجات/`.corrupt-*`) | **DONE** |
| 4 | خواندن جزئی، چک‌پوینت بدون فیلتر، بازگردانی ایمن، بهداشت | **DONE** |
| 5 | فاز/soak/تزریق خرابی + ممیزی قفل (+ تکمیل: کشتن درخت فرایند git) | **DONE** |
| 6 | مستندات، انتشار 1.8.1، خودبازبینی نهایی | **DONE** |

گیت نهایی (خروجی واقعی، آخرین اجرا در Stage 6):

```
=== gofmt -l . ===                [OK] (خالی)
=== go vet ./... ===              [OK]
=== go build ./... ===            [OK]
=== go test -race -count=1 ./... === ok  myai  35.033s  [OK]
=== env GOOS=windows GOARCH=amd64 go vet ./... === [OK]
=== env GOOS=linux   GOARCH=amd64 go vet ./... === [OK]
GATE: GREEN
```

وضعیت race: **کاملاً راستی‌آزمایی‌شده** — همه‌ی مراحل با `-race` واقعی اجرا
شدند؛ هیچ مرحله‌ای race-unverified نیست. مجموعه‌ی نهایی: **۵۲۹ تابع Test/Fuzz
در ۶۵ فایل** + **۹ هدف fuzz** با ۱۸ فایل کورپوس کامیت‌شده (ورودی Mission 4:
۴۷۸/۵۱). شمارش پس از Stage 6 به‌طور مستقل بازتأیید شد.

## 2. نقص‌های تأییدشده: ریشه → رفع → تست اثبات

**۱) بدون recover — panic هر گوروتینی کل برنامه را می‌کشت (Stage 1).**
ریشه: جز `errorlog.go` هیچ `recover()`ای نبود؛ workerهای موازی ابزار، گوروتین
استریم، حلقه‌های MCP و هر متد متصل به Wails روی گوروتین خودشان اجرا می‌شدند.
رفع: `safego.go` تازه — `safeGo`/`recoverPanic`/`callGuarded` و نگهبان متدهای
متصل `recoverBound`/`recoverBoundErr`؛ همه‌ی گوروتین‌های کد غیرتست با `safeGo`
و رفتار بازگردانی وضعیت (نتیجه‌ی ابزار همیشه نوشته می‌شود، UI در «در حال تولید»
گیر نمی‌کند، سرور MCP مرده علامت می‌خورد)، هر ۶۰ متد صادرشده‌ی `*App` نگهبان
دارد (امضاها دست‌نخورده). تست‌ها: `TestSafeGoRecoversPanicAndKeepsWorking`،
`TestToolWorkerPanicKeepsPairingAndOthersRun`، `TestSubagentPanicReturnsErrorReport`،
`TestRecoverBoundKeepsAppAlive` و نگهبان AST
`TestExportedAppMethodsHavePanicGuard`.

**۲) «فکر کردن بی‌پایان» — سکون وسط استریم (Stage 2).**
ریشه: کلاینت HTTP فقط `ResponseHeaderTimeout` (۹۰s) داشت؛ سکون سرویس‌دهنده/پروکسی
وسط استریم بدون بستن اتصال، خواندن بدنه را تا ابد بلاک می‌کرد. رفع:
`streamwatch.go` — `streamIdleGuard` دور بدنه‌ی هر استریم (اصلی و زیرایجنت)؛
پیش‌فرض ۱۲۰s، کران ۳۰–۹۰۰، تنظیم پنهان `streamIdleTimeoutSec`. سکون پیش از
خروجی → خطای موقتِ قابل‌تلاش‌مجدد؛ پس از خروجی → ذخیره‌ی نیمه‌کاره + بدون تلاش
مجدد + «ادامه»؛ توقف کاربر هرگز سکون گزارش نمی‌شود. تست‌ها:
`TestStreamStallBeforeFirstByteRetries`، `TestStreamStallMidStreamSavesPartialNoRetry`،
`TestStreamKeepAlivePreventsWatchdog`، `TestStreamStallDoesNotOverrideUserStop`،
`TestSubagentStallReturnsErrorReport`، `TestStreamIdleClampAndDefault`،
`TestStreamWatchdogNoGoroutineLeak`.

**۳) نوشتن اتمیک ویندوز (Stage 3.1).**
ریشه: نام موقت ثابت `path+".tmp"` (تصادم دو نویسنده‌ی هم‌زمان) و `os.Rename`
تک‌تلاش که روی مقصدِ لحظه‌ای باز (ویرایشگر/آنتی‌ویروس/ایندکسر) گذرا با
ACCESS_DENIED/SHARING_VIOLATION/LOCK_VIOLATION شکست می‌خورد. رفع: تمپ یکتای
`os.CreateTemp` + Sync + `renameWithRetry` (۶ تلاش، ۲۵→۴۰۰ms، errnoهای ۵/۳۲/۳۳
— فقط ویندوز؛ POSIX همان rename). تست‌ها: `TestAtomicWriteConcurrentUniqueTemp`
(۲۴ نویسنده‌ی هم‌زمان)، `TestSweepRecognizesUniqueTempNames`.

**۴) خرابی گفتگو/`changes.json` = از دست رفتن داده (Stage 3.2).**
ریشه: JSON خراب فقط خطای خام می‌داد؛ هیچ `.bak` و هیچ بازیابی‌ای نبود. رفع:
`recovery.go` — `atomicWriteFileWithBackup` با چرخش یک‌نسلی `.bak` به ترتیب
کرش‌ایمن؛ `salvageConversation` با `json.Decoder` تا آخرین پیام کامل؛
`.corrupt-<ts>` با سقف ۵؛ زنجیره‌ی main → `.bak` → نجات در هر دو لودر؛ اعلان
فارسی «گفتگو آسیب دیده بود؛ n پیام بازیابی شد.» تست‌ها:
`TestConversationBackupRotationAndRecovery`، `TestConversationSalvageTruncated`
(ZWNJ بایت‌به‌بایت)، `TestCorruptFilesAreBounded`، `TestChangesJSONRecoversFromBackup`.

**۵) دیسک پر / خطای نوشتن (Stage 3.3).**
ریشه: پیام مبهم و نامشخص بودن سالم‌ماندن فایل قبلی. رفع: هر خطا فقط فایل موقت
را پاک می‌کند؛ مقصد هرگز truncate نمی‌شود؛ وضعیت حافظه حفظ می‌شود؛ خطای فارسی
«ذخیره‌ی گفتگو روی دیسک ناموفق بود (فایل قبلی دست‌نخورده است)». تست‌ها:
`TestDiskFullKeepsExistingFileAndMemory` + سناریوی تزریق خرابی
`disk_full_mid_turn_surfaces_notice_and_keeps_file`.

**۶) نام‌های ممنوع/مسیرهای بلند ویندوز (Stage 3.4).**
ریشه: نام‌های رزروشده (CON/NUL/COM1…)، نقطه/فاصله‌ی پایانی و مسیرهای >۲۵۹ نویسه
خطای مبهم یا نوشتن روی شبه‌دستگاه می‌دادند. رفع: `pathnames.go` —
`validatePathName`/`validatePathForWrite` (روی POSIX فقط مسیر خالی رد می‌شود)؛
`checkpointAbs` از آن استفاده می‌کند. تست‌ها: `TestValidatePathName`،
`TestValidatePathForWriteLongPath`.

**۷) خواندن جزئی مجوز بازنویسی کامل می‌شد (Stage 4.1).**
ریشه: هر `read_file` موفق هش **کل** فایل را ثبت می‌کرد؛ پس از دیدن ۲۰ خط از فایل
۵۰۰۰ خطی، `write_file` کل فایل ممکن بود و بخش دیده‌نشده بی‌صدا نابود می‌شد. رفع:
رکورد `Full bool` فقط برای خواندن کاملِ بدون برش؛ `intentOverwrite` رکورد
غیرـFull را رد می‌کند («read the entire file first, or use edit_file»)؛
`edit_file` با رکورد جزئی مجاز می‌ماند (تطبیق دقیق `old_string` محافظ آن است).
تست‌ها: `TestPartialReadRejectsWholeFileOverwrite`، `TestTruncatedReadRejectsOverwrite`،
`TestFullReadAllowsOverwrite`، `TestFullReadAfterPartialAllowsOverwrite`،
`TestEditAfterPartialIsAllowed`، `TestStaleAfterPartialReadStillRejected`،
`TestPersianCRLFBOMFullReadThenOverwrite` (BOM+CRLF+ZWNJ بایت‌به‌بایت)،
`TestLargeFilePartialReadGuard`.

**۸) چک‌پوینت بایت‌به‌بایت نبود (Stage 4.2).**
ریشه: snapshot با `git add -A` و بازگردانی با `checkout-index` → `core.autocrlf`
کاربر، `.gitattributes` و فیلترهای clean/smudge (LFS) روی محتوا اعمال می‌شدند؛
خط‌پایه‌ها عوض می‌شدند و LFS خراب برمی‌گشت. رفع: پایپ‌لاین بدون فیلتر —
`git hash-object -w --no-filters --stdin-paths` + `git update-index -z --add
--index-info` + `write-tree` (بدون `git add`)؛ همه‌ی فراخوانی‌های git با
`-c core.autocrlf=false -c core.safecrlf=false -c core.fsmonitor=false -c
core.hooksPath=<خالی>` و `GIT_ATTR_NOSYSTEM=1`؛ بازگردانی خام با `git cat-file
--batch`. تست‌ها: `TestCheckpointNoFilterByteFaithful` (autocrlf=true +
`.gitattributes` + CRLF/LF/باینری/فارسی → درخت بایت‌به‌بایت)،
`TestCheckpointPreviewListsOnlyChanged`.

**۹) بازگردانی ناایمن به symlink و بی‌کران در حافظه (Stage 4.3).**
ریشه: بررسی مسیر فقط لفظی (`withinDir`) بود — symlink/junction بعد از snapshot
→ نوشتن بیرون پروژه — و materialize کل snapshot + `os.ReadFile×2` + مقایسه‌ی
رشته‌ای، حافظه/دیسک را می‌بلعید. رفع: `resolveRestorePath` (EvalSymlinks + رد
reparse/هدف symlink + بررسی نام/طول)؛ مقایسه و نوشتن **جریانی** ۶۴KiB با
`atomicWriteStream`؛ برنامه (plan) پیش از نخستین نوشتن و با کران فایل/مهلت.
تست‌ها: `TestResolveRestorePathRefusesSymlinkedParent`،
`TestResolveRestorePathRefusesOutsideResolution`، `TestRestoreLargeFileMemoryBounded`،
`TestCheckpointLimitExceededChangesNothing`.

**۱۰) بهداشت (Stage 4.4).** `README.txt` منسوخ حذف شد؛ فایل‌های تستِ
نسخه‌دار به نام موضوعی منتقل شدند (`v13_test.go` → `providermigration_test.go` +
`conversationdelete_test.go`؛ `finalaudit_test.go` → `mcpsafeguards_test.go` +
`frontendhooks_test.go` + `settingssecurity_test.go`؛ `finalfix_test.go` →
`settingsrevert_test.go` + `toolcustomize_test.go` + `appresume_test.go` —
بدنه‌ها بدون تغییر)؛ `rg 'TODO|FIXME|XXX'` صفر؛ رفع ابطال‌نشدن کش
`errLogPath` (`resolveErrorLogPath` کش را وقتی پوشه نیست باطل می‌کند).

**۱۱) یافته‌های Stage 5 (خارج از فهرست اولیه، واقعی).**
- ممیزی AST ترتیب قفل (`lockaudit_test.go`) نقض واقعی یافت: `finishChat`
  اعلان `chat:notice` را **زیر قفل `a.mu`** می‌فرستاد → پرچم `saveFailed` زیر
  قفل، emit بیرون از قفل و پیش از `chat:done`.
- نوه‌های git هنگ‌کرده یتیم می‌ماندند و پایپ stdout را باز نگه می‌داشتند (Wait
  تا پایان WaitDelay معطل) → کشتن درخت فرایند git در هر سه نقطه
  (`startGitProcess`/`watchGitTree` + لغو پیش از Wait در `blobBatch.Close`)؛
  اثبات قرمز→سبز واقعی: تست پیش از رفع با «۳.۴s > ۳s» و بقای فرزند پس‌زمینه
  قرمز بود و پس از رفع در ۰.۴۶s سبز شد.

## 3. فایل‌های افزوده/تغییریافته به تفکیک مرحله (از `git diff-tree`)

- **Stage 0+1 (`38e42d4`):** جدید: `safego.go`، `safego_test.go`،
  `panicguard_test.go`. تغییر: `chat.go`، `toolbatch.go`، `subagent.go`،
  `mcp.go`، `mcpmanager.go`، `shell.go`، `app.go`، `changes.go`،
  `checkpoint.go`، `commands.go`، `context.go`، `conversations.go`،
  `errorlog.go`، `export.go`، `history.go`، `permissions.go`، `providers.go`،
  `security.go`، `settings.go`، `turnreview.go`، `webfetchperm.go`،
  `agent_e2e_test.go` (اصلاح مستند دو انتظار وابسته به سکو).
- **Stage 2 (`ab41adf`):** جدید: `streamwatch.go`، `streamwatch_test.go`.
  تغییر: `chat.go`، `subagent.go`، `settings.go`.
- **Stage 3 (`f3b469a`):** جدید: `recovery.go`، `pathnames.go`،
  `atomicwrite_windows.go`، `atomicwrite_other.go`، `stage3_test.go`. تغییر:
  `crypto.go`، `conversations.go`، `changes.go`، `checkpoint.go`.
- **Stage 4 (`86de28d`):** جدید: `readguard_test.go`،
  `checkpointfidelity_test.go`، `reparse_windows.go`، `reparse_other.go` و
  ۷ فایل تستِ انتقالی. حذف: `README.txt`، `v13_test.go`، `finalaudit_test.go`،
  `finalfix_test.go`. تغییر: `fileread.go`، `agent.go`، `checkpoint.go`،
  `crypto.go`، `changes.go`، `errorlog.go`، `agent_test.go`، `safego_test.go`،
  `streamwatch_test.go`.
- **Stage 5 (`e99a483`):** جدید: `fuzz_test.go`، `soak_test.go`،
  `lockaudit_test.go`، `testdata/fuzz/**` (۱۸ کورپوس). تغییر:
  `agent_e2e_test.go` (تزریق `Stall`)، `toolbatch.go` (`callToolGuarded`)،
  `checkpoint.go` (`checkpointTimeout` const→var)، `chat.go` (رفع نقض قفل).
- **Stage 5 تکمیل (`13ea4cb`):** تغییر: `checkpoint.go` (کشتن درخت فرایند
  git)، `soak_test.go`.
- **Stage 6 (`e8642a6`):** `ARCHITECTURE.md`، `CHANGELOG.md`، `README.md`،
  `main.go`، `wails.json`، `build.sh`، `.github/workflows/ci.yml`،
  `turnreview_test.go` (خط نسخه)، `docs/dev/PROGRESS.md`.

**وابستگی تازه: هیچ** — فقط کتابخانه‌ی استاندارد؛ `go.mod` دست نخورد.

## 4. تست‌های افزوده و اثبات‌ها (خلاصه)

نام دقیق تست هر نقص در بند ۲ آمده است. موارد فارسی/RTL: حفظ ZWNJ بایت‌به‌بایت
در نجات گفتگو (`TestConversationSalvageTruncated`)، BOM+CRLF+فارسی در محافظ
خواندن (`TestPersianCRLFBOMFullReadThenOverwrite`) و چک‌پوینت
(`TestCheckpointNoFilterByteFaithful`)، مسیرهای فارسی در اعتبارسنج نام
(`TestValidatePathName`) و کورپوس‌های فارسی/ZWNJ در فازها. به‌علاوه:

- **فاز (۹ هدف `testing.F`):** `FuzzParseShellCommand`، `FuzzReadSSEChunks`،
  `FuzzApplyEditOpAndReindent`، `FuzzHTMLToReadableText`،
  `FuzzGitignoreMatcher`، `FuzzSafeCheckpointRel`، `FuzzValidateHistory`،
  `FuzzParseToolArgs`، `FuzzCommandCompositionNeverLoosens` — بذرهای
  درون‌فایلی + کورپوس کامیت‌شده؛ در گیت عادی هر بذر < ۲۰۰ms؛ حالت کشف پیوسته
  (`go test -fuzz`) کار توسعه‌دهنده است.
- **soak (`TestSoakAgentLoop`):** ۵ دور حلقه‌ی واقعی ایجنت (نوشتن → تست واقعی
  `run_command` → برگرداندن → چک‌پوینت → جهش فایل → بازگردانی → فشرده‌سازی →
  Stop) زیر `-race` با کران‌های رشد (گوروتین +۸، heap +۳۲MB، fd +۱۶، ورودی
  config +۱۲۰)؛ حالت بلند با `MYAI_SOAK_ITERATIONS`.
- **تزریق خرابی (۹ سناریو در `TestFaultInjectionEndStates`):** 429/5xx، SSE
  خراب، panic ابزار، دیسک پر، سکون وسط استریم از طریق هارنس، crash/هنگ MCP،
  git ناموجود/هنگ با بررسی یتیم — همه با دیده‌بان بن‌بست `runWithDeadline` و
  الزام پایان سازگار (نه «در حال تولید»، جفت‌بندی ابزار سالم، JSON معتبر).
- **ممیزی‌های AST:** `TestExportedAppMethodsHavePanicGuard` (allowlist خالی) و
  `TestLockAuditNoRuntimeEmitUnderAppLocks` (a.mu/genMu/checkpointMu هرگز روی
  emit).

## 5. تغییرات رفتاری که کاربر می‌بیند

- سکون سرویس‌دهنده دیگر «فکر کردن بی‌پایان» نیست: پیش از خروجی → تلاش مجدد با
  اعلان «پاسخ از سرویس‌دهنده قطع شد؛ تلاش مجدد …»؛ پس از خروجی → پاسخ
  نیمه‌کاره ذخیره و «دریافت پاسخ متوقف شد (بدون داده برای n ثانیه). می‌توانید
  «ادامه» را بزنید.»
- panic داخلی برنامه را نمی‌کشد: پیام فارسی «خطای داخلی رخ داد؛ جزئیات در لاگ
  خطاها ثبت شد.» و ادامه‌ی کار (ابزار خراب فقط نتیجه‌ی خطای خودش را می‌دهد).
- گفتگوی خراب/بریده خودش بازیابی می‌شود («گفتگو آسیب دیده بود؛ n پیام بازیابی
  شد.») و پشت هر ذخیره یک نسل `.bak` است؛ دیسک پر → اعلان فارسی و فایل قبلی سالم.
- بازنویسی کامل فایل پس از خواندن جزئی/بریده رد می‌شود (پیام انگلیسی «read the
  entire file first, or use edit_file»)؛ `edit_file` همان‌طور که بود کار می‌کند.
- بازگردانی چک‌پوینت دیگر خط‌پایه‌ها را عوض نمی‌کند و پیش‌نمایش فقط تغییرهای
  واقعی را نشان می‌دهد؛ پوشه‌ی symlink/junction در مسیر بازگردانی رد و گزارش می‌شود.
- **مهاجرت: هیچ** — تنظیمات/گفتگو/`changes.json` قدیمی بدون تغییر بار می‌شوند؛
  `streamIdleTimeoutSec` جدیدِ `omitempty` با صفر = پیش‌فرض ۱۲۰ ثانیه.

## 6. فرض‌ها، انحراف‌ها، محدودیت‌ها (صادقانه)

- **دستورها sandbox نشده‌اند** — گیت سه‌لایه‌ی دستورات، بازبینی پایان نوبت و
  چک‌پوینت‌ها شبکه‌ی ایمنی هستند؛ تضمین‌های پایداری 1.8.1 (مهار panic، نگهبان
  سکون، نوشتن اتمیک) جایگزین سندباکس نیستند.
- گیت مبنا (Stage 0) RED بود؛ ریشه دو انتظار تستِ وابسته به سکو بود نه کد
  محصول — اصلاح مستند (`t.Skip` زیرتست POSIX روی ویندوز + `TrimSpace`) بدون
  سست‌شدن assertion. دو تست نیمه‌کاره‌ی ورودی Stage 5 هم با معماری واقعی اصلاح
  شدند (نه سست).
- همه‌ی پیوندها/junctionهای مسیر بازگردانی رد می‌شوند (سخت‌گیرانه‌تر از متن
  سند و ایمن‌تر)؛ تست ۲۰۰MB به ۳۲MB کاهش یافت (همان خاصیت، گیت سریع‌تر)؛
  `checkpointMaxFiles` const→var برای تست کران.
- مهلت سکون در شروع نوبت ثابت خوانده می‌شود؛ سکون «فاز هدر» زیر همان
  `ResponseHeaderTimeout` (۹۰s) است. `.bak` فقط یک نسل؛ اگر main و `.bak` هر
  دو خراب باشند فقط پیام‌های کامل تا محل خرابی نجات می‌یابند و اصل در
  `.corrupt-*` می‌ماند.
- راستی‌آزمایی‌نشده: رفتار واقعی ویندوز (junction، آنتی‌ویروس، WebView2، Job
  Object، مسیرهای >۲۶۰ در NTFS) — فقط vet متقاطع + تست‌های مستقل از سکو +
  چک‌لیست دستی بند ۷؛ GitHub Actions اجرا نشد (رانر نیست).

## 7. چک‌لیست پذیرش دستی برای انسان (UI فارسی — مراحل دقیق و نتیجه‌ی مورد انتظار)

1. **(a) سکون/قطعی شبکه وسط پاسخ:** از ایجنت پاسخ بلند بخواهید و در میانه‌ی
   استریم شبکه را قطع کنید (یا پروکسی ساکت بگذارید). *انتظار:* حداکثر پس از
   ~۱۲۰ ثانیه پایان مودبانه با «دریافت پاسخ متوقف شد…»؛ اگر خروجی داده شده بود،
   پاسخ نیمه‌کاره ذخیره شده و «ادامه» کار می‌کند؛ UI در «در حال تولید» گیر
   نمی‌کند.
2. **(b) خطای داخلی (معادل دستی قلاب تست):** یک سرور MCP با فرمان ناموجود در
   تنظیمات بسازید و ابزارش را صدا بزنید. *انتظار:* فقط همان ابزار نتیجه‌ی خطا
   می‌دهد؛ برنامه زنده می‌ماند، پیام فارسی خطا دیده می‌شود و در «لاگ خطاها»
   ثبت است؛ UI در «در حال تولید» گیر نمی‌کند.
3. **(c) فایل باز در ویرایشگر/پوشه‌ی اسکن آنتی‌ویروس:** فایلی را در ویرایشگر
   باز نگه دارید و از ایجنت بخواهید همان را ذخیره کند (چند بار). *انتظار:*
   دیگر شکست گذرا دیده نمی‌شود (تلاش مجدد ~۰.۸ ثانیه‌ای ویندوز)؛ در نهایت یا
   ذخیره موفق است یا خطای فارسیِ نام‌برنده‌ی فایل — هرگز فایل نیم‌نوشته/خراب.
4. **(d) خرابی دستی گفتگو:** برنامه را ببندید، یکی از فایل‌های JSON گفتگو در
   `%AppData%\MyAI` را وسطش برش بزنید و برنامه را باز کنید. *انتظار:* گفتگو با
   پیام‌های کامل بازیابی‌شده باز می‌شود + اعلان «گفتگو آسیب دیده بود؛ n پیام
   بازیابی شد.»؛ فایل بریده در `.corrupt-<زمان>` و نسخه‌ی قبلی در `.bak` باقی است.
5. **(e) محافظ خواندن کامل:** از ایجنت بخواهید فقط خطوط ۱–۲۰ یک فایل بزرگ را
   بخواند و بعد کل فایل را بازنویسی کند. *انتظار:* رد می‌شود با پیام انگلیسی
   «read the entire file first, or use edit_file»؛ محتوای شما دست‌نخورده.
6. **(f) چک‌پوینت در مخزن autocrlf:** در مخزنی با `core.autocrlf=true` و
   `.gitattributes`، نوبتی اجرا کنید که فایل بسازد/ویرایش/حذف کند، سپس
   «بازگردانی کل پروژه به قبل از این نوبت». *انتظار:* پیش‌نمایش فقط فایل‌های
   واقعاً تغییرکرده را نشان می‌دهد (بدون rewrite انبوه)؛ پس از بازگردانی درخت
   بایت‌به‌بایت برمی‌گردد (CRLF/LF عوض نمی‌شود).
7. **(g) پوشه‌ی symlink/junction:** داخل پروژه یک junction به بیرون بسازید و
   بازگردانی چک‌پوینت را اجرا کنید. *انتظار:* آن مسیر در «ردشده‌ها» گزارش
   می‌شود و چیزی بیرون از پروژه نوشته نمی‌شود.
8. **(h) اجرای طولانی:** ۳۰+ نوبت پیاپی (شامل نوشتن/بازگردانی/چک‌پوینت) اجرا
   کنید و Task Manager را ببینید. *انتظار:* رشد پیوسته‌ی حافظه/هندل دیده
   نمی‌شود.
9. **(i) بستن برنامه:** پس از پایان کار برنامه را ببندید و Task Manager را
   چک کنید. *انتظار:* هیچ فرایند git/node/MCP جامانده‌ای نماند.
10. **(j) نسخه و داده‌ی قدیمی:** در «درباره» نسخه‌ی 1.8.1 دیده شود؛ گفتگوها،
    تنظیمات و `changes.json` قدیمی بعد از ارتقا بدون اعلان خرابی باز شوند.
