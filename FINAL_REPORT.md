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
