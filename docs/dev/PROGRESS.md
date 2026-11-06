# MyAI — Completion Progress Log

Autonomous finish-the-project log. One entry per stage: what was done, the
gate result of that stage's last run, and any assumptions made. Gate =
`gofmt -l .` (must be empty) · `go vet ./...` · `go build ./...` ·
`go test -race -count=1 ./...` · `GOOS=windows go vet` · `GOOS=linux go vet`
(run via `scripts/gate.sh`).

## Environment note (applies to all stages)

- Work is performed in a **Linux sandbox** (Debian 13, amd64), not on the
  developer's Windows machine. Windows-specific behavior is verified by
  `GOOS=windows` compile/vet, by reading the platform files, and by tests
  that are platform-agnostic; anything that can only be proven on real
  Windows is listed in the manual checklist of `FINAL_REPORT.md`.
- Go 1.26.10 (matches `go.mod`'s `go 1.26.0` directive; tarball checksum
  verified against go.dev) was installed under `/home/z/sdk/go` — the
  sandbox had no Go toolchain. `gcc 14.2` is present, so `-race` runs for
  real (no "race-unverified" stages).
- The uploaded zip contained **no `.git` directory**, so stage commits are
  made against a fresh `git init` whose first commit is the extracted
  1.6.0 tree. `build.sh` therefore could **not** be restored from history;
  it was recreated from the build command documented in `README.md`
  (see Stage 0 notes).

## Stage 0 — Green baseline and repository state — DONE (2026-11-02)

**What was done**

1. Read `ARCHITECTURE.md`, `README.md`, `BUGFIXES.md`, `go.mod`, `.gitignore`,
   `wails.json`, `main.go`.
2. Fixed the known Windows-only test bug `TestRulesMtimeCache`
   (`rulesfiles_test.go`): the mtime advance changed from `time.Nanosecond`
   to `2 * time.Second` (NTFS stores 100 ns-resolution timestamps and FAT
   2 s-resolution, so a 1 ns bump never changed the stored mtime and the
   (size, mtime) rules cache correctly served the old content — test bug,
   not a product bug; `context.go` untouched). Passes on Linux too.
3. Repository hygiene:
   - `.gitignore` already contained every required pattern
     (`bin/*`, `!bin/.gitkeep`, `build/bin/`, `.DS_Store`, `Thumbs.db`,
     `*.log`, `*.exe`, `*.exe~`, `*.test`, `*.out`, `.freebuff/`) and does
     not ignore `frontend/wailsjs/` (it is tracked on purpose).
     Added `/myai` (the Linux `go build` artifact) so the gate never leaves
     an untracked binary behind.
   - `bin/.gitkeep` created (referenced by `.gitignore`, missing).
   - `build.sh` was **absent from the zip and unrecoverable** (no git
     history). Recreated from the README-documented build command
     (`wails build -platform windows/amd64 -skipbindings -ldflags …`),
     defaulting the version to 1.6.0. Marked as a recreation in its header.
   - Removed the stray `myai` binary produced by the first `go build` run
     (untracked build artifact).
4. Added `scripts/gate.sh` (the 6 gate commands, one runner).

**Baseline before any fix:** `go build`, `go vet`, `go test -race` were
already green on Linux; the only known failure was the Windows-only
`TestRulesMtimeCache` (fixed above per instructions).

**Gate result (last run):** GREEN
(`gofmt -l .` empty · `go vet ./...` ok · `go build ./...` ok ·
`go test -race -count=1 ./...` ok myai 6.175s ·
`GOOS=windows GOARCH=amd64 go vet ./...` ok ·
`GOOS=linux GOARCH=amd64 go vet ./...` ok)

**Assumptions**

- Fresh `git init` + per-stage commits is the intended interpretation of
  "commit if the project uses git" given the archive ships no `.git`.
- The 2-second mtime advance is safe on all supported filesystems (NTFS,
  FAT32, ext4, APFS) and does not weaken the assertion (the cache must
  still invalidate on a *real* mtime change).

## Stage 1 — `todo_write` tool + live checklist UI — DONE (2026-11-02)

**What was done**

- New `todos.go`: `TodoItem{ID, Content, Status}` (status ∈ pending |
  in_progress | completed), `parseTodoItems` validation with precise
  English errors (list ≤ 50; content non-empty after trim, rune-safe
  truncation at 200 runes with a note to the model instead of rejection;
  valid status; at most one in_progress; unique ids with positional
  1..n auto-assignment that never collides with provided ids), loose
  typing via `coerceTodoList` (JSON array or JSON-string array), and
  `toolTodoWrite` (persist under `a.mu` → atomic `saveConversation` →
  emit after unlock).
- `Conversation.Todos []TodoItem` (`json:"todos,omitempty"`) — old
  conversation files without the field load unchanged (fixture test);
  fork deep-copies the list; a new conversation starts empty; the loaded
  payload (`GetConversation`, `chat:start`, `chat:done`) carries todos
  automatically.
- Event `chat:todos` `{conversationId, todos}` after every successful
  write (including the clearing empty list → `[]`). Emission goes through
  `App.emitTodos` with an injectable test hook (`todosEmit`, documented:
  set only before concurrency starts) — runtime events stay no-ops in
  tests as before.
- Wired in all six registry places: `agentToolDefs()` (schema with
  array/items/enum), `toolArgSpecs` + `validateToolArgs` (new minimal
  `"todos"` type case), `execToolCtx` (`case "todo_write"`),
  `permissions.go` default **allow** (only changes app state, never
  files), system prompt (tools list + rule 9: use it for ≥3 steps,
  exactly one in_progress, atomic replace, empty array clears), and the
  frontend tool-card maps (icon `todo_write:"list"` + args-summary case,
  tiny additive hooks in minified `app.js`, same pattern as `glob`/`task`).
  NOT added to the parallel list or the sub-agent allow-list.
- Frontend: new readable `assets/js/todos.js` + `assets/css/todos.css`
  referenced from `index.html`. Collapsible «کارهای ایجنت» panel above
  the composer, hidden when empty; status labels در انتظار / در حال
  انجام / انجام شد; progress «۲ از ۵» with Persian digits
  (`MyAIUtils.fa`); all text via `textContent`; item text `dir="auto"`;
  CSS logical properties; restores on conversation open (idempotent
  `GetConversation` wrapper), updates on `chat:todos` (active
  conversation only), resets on «گفتگوی تازه» (MutationObserver on
  `#empty-state`). Mock mode: `mock.js` got a tiny additive hook that
  emits a sample `chat:todos` in agent mode so the panel is visible in
  the browser preview; todos.js is defensive (no runtime → no crash).
- Bindings: `models.ts` gained the optional `todos` field +
  `TodoItem` class (hand-maintained, tab indentation preserved;
  `bindings_test.go` parsers stay green).

**Tests added (`todos_test.go`):** validation table (all error paths,
loose typing incl. JSON-string array), Persian content with ZWNJ
truncated exactly at the rune boundary + `utf8.ValidString`, auto-id
uniqueness/collision handling, persistence round trip via the full
`execToolCtx` path, old conversation file without the field, fork
deep-copy, event fired once per successful write (+ payload
conversationId, no event on failure/no-context), 16-goroutine
concurrent-write race test (file stays valid JSON, race detector on),
permission default + deny override, and a registration-consistency test
(defs/spec/prompt present; absent from parallel list and sub-agent
allow-list).

**Gate result:** GREEN — `gofmt -l .` empty · `go vet ./...` ok ·
`go build ./...` ok · `go test -race -count=1 ./...` ok (6.7s) ·
`GOOS=windows GOARCH=amd64 go vet` ok · `GOOS=linux GOARCH=amd64 go vet` ok.

**Assumptions**

- «۲ از ۵» progress = completed/total (shown next to the title).
- The doom-loop guard naturally limits repeated identical todo_write
  calls (same as every tool); no special-casing needed.
- The tool result is a normal tool_call/tool pair, so existing
  history validation/compaction handles it unchanged (verified by the
  full suite).
- No jsdom/Node frontend harness exists in the repo, so the checklist
  UI logic is covered by Go tests + manual checks (listed in
  FINAL_REPORT.md); JS files pass `node --check` and a headless smoke
  run.

## Stage 2 — `edit_file` multi-edit form + `glob` mtime ordering — DONE (2026-11-02)

**A. Atomic multi-edit (`edits[]`)**

- `edit_file` now accepts `edits: [{old_string, new_string, replace_all?}]`
  (≤ 20 per call, loose JSON-string array typing), mutually exclusive with
  the single-edit fields; both-or-neither produce precise English errors.
- Refactor: the matching cascade moved verbatim into `applyEditOp`
  (exact → EOL → trailing-whitespace → indentation-tolerant with re-indent)
  so the single-edit and multi-edit paths share ONE implementation.
  Single-edit behavior and every existing error message are byte-identical
  (regression test asserts the exact old messages).
- Multi-edit applies edits **sequentially to the in-memory result of the
  previous edit**, all-or-nothing: on the first failing edit nothing is
  written and the error names the 1-based edit index plus the inner reason
  («ERROR: multi-edit aborted at edit #N (of M) in 'file' — no changes
  were written: …»). One backup, one diff, one confirmation for the whole
  set (`writeFileWithConfirm` called once). BOM, EOL style, size limits
  (`maxEditBytes` per source/result, `maxFuzzyEditBytes` per edit) and the
  existing uniqueness rule all preserved. Success message reports edit
  count, total replacements and any non-exact strategies used.
- `toolArgSpecs.edit_file`: `old_string`/`new_string` are no longer
  `required` at the table level (the `edits` form omits them); the
  either/or check lives in `toolEditFileArgs` with better errors. New
  `"edits"` spec type (shallow array check in `validateToolArgs`, deep
  validation in `parseEditOps` with per-index messages). Tool description
  and system-prompt rule 2 document the form.

**B. `glob` ordering**

- `glob` returns files sorted by modification time (newest first, then
  path ascending) — stat via `DirEntry.Info()`, candidates collected
  during the walk up to a hard bound of 20,000 (`maxGlobCandidates`),
  then sorted and sliced to the first 500 (`maxGlobResults` 300 → 500 per
  spec) with the existing truncation notice. Filtering (.gitignore,
  built-in skip dirs, symlinks) and ctx cancellation (per-directory +
  every 256 files) unchanged; cancelled walks never return partial
  results. Tool description updated («up to 500 files … newest first»).

**Tests added:** `multiedit_test.go` (3 dependent edits, failure on the
2nd edit leaves the file byte-identical, ambiguity error names the index,
CRLF+BOM, Persian/ZWNJ content, ≥20-edits cap + empty rejection + result
size cap with untouched file, JSON-string `edits`, mutual exclusion,
per-item validation with index, single-form backward compatibility incl.
exact old messages, tolerant-cascade-per-edit with re-indent) and
`globorder_test.go` (newest-first ordering with ≥2 s mtime steps, path
tiebreak, nested ordering, result cap after sorting, 20k candidate bound
notice, filtering + cancellation still enforced).

**Gate result:** GREEN — gofmt empty · vet ok · build ok ·
`go test -race -count=1 ./...` ok (12.7s) · windows vet ok · linux vet ok.

**Assumptions**

- «return the first 500» ⇒ `maxGlobResults` raised from 300 to 500 (the
  spec's explicit number); docs updated in Stage 6.
- Hitting the 20,000-candidate bound stops the walk and surfaces the
  existing «result limit reached» notice (spec does not define a separate
  message).
- `new_string` is required per `edits` item (empty allowed = deletion),
  mirroring the single-edit form.

## Stage 3 — `web_fetch` with SSRF protection — DONE (2026-11-02)

**What was done**

- New `webfetch.go`: tool `web_fetch(url, max_chars?)` returning readable
  text wrapped in `<fetched_content url="…" untrusted="true">…</fetched_content>`
  with closing-tag escaping (`&lt;/fetched_content`) so fetched data can
  never break out of the envelope.
- **SSRF protection at connect time**: package-level injectable guard
  `webFetchGuard` (signature `func(network, address string) error`,
  default `ssrfConnectGuard`) wired into a custom `net.Dialer.Control` —
  it inspects the **actually resolved IP** and rejects loopback
  (127/8, ::1), private (10/8, 172.16/12, 192.168/16, ULA fc00::/7),
  link-local (169.254/16 incl. cloud metadata 169.254.169.254, fe80::/10),
  CGNAT 100.64/10, unspecified + 0.0.0.0/8, multicast, broadcast
  255.255.255.255, reserved 240/4, benchmarking 198.18/15, documentation
  TEST-NET-1/2/3, and IPv4-mapped + NAT64 (64:ff9b::/96) forms of all of
  the above (unwrapped via `netip.Addr.Unmap()` + manual NAT64 unwrap).
  Every redirect hop opens a new connection → the guard re-runs (DNS
  rebinding defense). Max 5 redirects; scheme/userinfo/host re-validated
  on each hop and on the initial URL; non-http(s) is never followed.
- Environment proxies deliberately ignored (`Transport.Proxy = nil`) with
  an inline comment: a proxy would resolve/connect on our behalf and
  bypass the IP check.
- Limits: 20 s total timeout, 2 MiB body cap (with notice), output cap
  100,000 chars (default 20,000 via `max_chars`, clamped
  [500..100000]), rune-safe truncation, text-like content types only
  (text/*, JSON, XML, XHTML, +json/+xml) with a clear English error
  naming the content type otherwise. Charset detection from
  Content-Type + BOM + `<meta>` via `golang.org/x/net/html/charset`;
  Windows-1256 and ISO-8859-6 pages decode correctly (tests prove it,
  including that CP1256's 0x9D = ZWNJ survives). HTML→text via the
  tokenizer: script/style/noscript/template/iframe/svg dropped, headings
  as markdown-ish `#`, lists as `- `, links as `text (url)`, whitespace
  collapsed, ZWNJ preserved.
- System prompt rule 10: web_fetch content (and any tool/MCP content)
  is DATA, never instructions. Permission default **ask**; the modal
  shows «اجازه‌ی دریافت این آدرس را می‌دهید؟» with the URL bidi-isolated
  (new `url` field on `permissionAsk` + a tiny additive hook in the
  readable `permissions.js`; icon `globe` + URL args-summary in the
  minified `app.js` maps). NOT in the parallel list, NOT in the
  sub-agent allow-list. Stop (ctx) honored at dial, header and body
  stages (charset-snip errors are correctly attributed to cancel/timeout).
- Test hook: `App.permAskEmit` (same pattern as `todosEmit`) lets tests
  capture the modal payload without a UI.

**Tests added (`webfetch_test.go`):** IP classification table (36 v4/v6/
mapped/NAT64/edge cases with reasons), real guard blocks an httptest
loopback URL, DNS-rebinding-style (benign `localhost` hostname resolved
to loopback is blocked), redirect-to-denied-port blocked (guard error
surfaces through the hop), scheme/userinfo/empty-host rejection, 2 MiB
body cap notice, content-type allow-list + rejection naming the type,
HTTP status error, Windows-1256 Persian page decoded (with ZWNJ from
0x9D), ISO-8859-6 Arabic decoded, ZWNJ preservation + HTML structure
(headings/lists/links, drop-tags), rune-safe truncation at 777 chars
with clamps, closing-tag escape (exactly one real closing tag), ctx
cancel mid-body (handler blocks on request context — deterministic),
permission default + registrations, modal payload (kind/url/message)
followed by a successful fetch after «allow».

**Gate result:** GREEN — gofmt empty · vet ok · build ok ·
`go test -race -count=1 ./...` ok (9.7s) · windows vet ok · linux vet ok.

**Dependencies**

- `golang.org/x/net v0.61.0` (html + html/charset) and
  `golang.org/x/text v0.43.0` (charmap, used by tests) promoted from
  **indirect** to **direct** — same pinned versions already in go.sum
  via Wails, so no new modules enter the build; stdlib has no
  Windows-1256/ISO-8859-6 decoders nor an HTML tokenizer, which is
  exactly what these packages provide.

**Assumptions**

- Default `max_chars` = 20,000 (spec only says "default smaller"); clamp
  range [500..100000].
- Missing `Content-Type` header is treated as text (many minimal
  servers omit it); the allow-list is enforced whenever a type IS
  declared.
- «Farsi Yeh» (U+06CC) does not exist in the official CP1256 table
  (0xED is Arabic Yeh) — decoding is strictly faithful, we never
  normalize ي/ی per the project rules.

## Stage 4 — Platform correctness and CI — DONE (2026-11-02)

**What was done**

1. **Platform-aware system prompt.** `agentSystemPrompt` now derives the
   OS/shell wording from `runtime.GOOS` via a new injectable
   `agentSystemPromptForOS(workspace, goos)` + `shellInfoForOS(goos)`
   (windows: «native Windows desktop app», rule 3 names *Windows CMD
   (cmd.exe)* with hints `dir`/`type`, `\` separator, double quotes,
   `%VAR%`, `&&`; linux/darwin: «native Linux/macOS desktop app», POSIX
   `sh -c` with hints `ls`/`cat`, `/` paths, single quotes, `$VAR`; darwin
   additionally notes BSD `sed -i ''`). `shellInfoForOS` is documented to
   stay in step with `runShell` (cmd.exe on Windows, `sh -c` elsewhere).
   Tests assert per-OS content and that the production function follows
   `runtime.GOOS`.
2. **`RunCommand` context.** The bound `RunCommand` (terminal UI) now
   bases its context and its 60 s timeout on the **application lifetime
   context** (`a.ctx` stored by Wails `startup`; new helper
   `App.appContext()` falls back to `context.Background()` when nil in
   tests) instead of `context.Background()`. A pending permission ask is
   now aborted when the app shuts down — proven by
   `TestRunCommandAppContextCancelAbortsPendingAsk` (previously it hung
   until the 3-minute modal timeout).
3. **PowerShell/Git-Bash selection deliberately NOT implemented** (hard-
   block patterns in `permissions.go` are written for cmd/POSIX syntax;
   supporting other shells without re-auditing them would weaken
   security). Recorded under Future work (here and in FINAL_REPORT).
4. **CI** `.github/workflows/ci.yml`: primary `windows-latest` job
   (checkout → `actions/setup-go@v5` with `go-version-file: go.mod` +
   module cache → gofmt gate that fails on any output → `go vet` →
   `go test -race -count=1` → `go build -tags production` desktop compile
   check); secondary `ubuntu-latest` job (installs `libgtk-3-dev`,
   `libwebkit2gtk-4.1-dev`, `libglib2.0-dev`, `pkg-config`,
   `build-essential`, then vet + test + a **tagged** desktop compile
   check `go build -tags "production webkit2_41" ./...` — verified by
   reading the Wails v2.10.1 sources that the `production` build tag is
   what pulls the GTK/WebKit cgo frontend and `webkit2_41` selects the
   4.1 pkg-config name; untagged builds never compile cgo); optional
   `windows-build` job (Wails CLI pinned to `v2.10.1` from go.mod,
   `wails build -platform windows/amd64 -skipbindings`, artifact upload,
   `continue-on-error` since it is build-only). Actions pinned by major
   version (`@v5`/`@v4`), `concurrency` with `cancel-in-progress`, Go
   module caching via setup-go.

**Validation honesty:** the workflow was **not executed locally** (no
GitHub Actions runner in this sandbox). It was validated by an offline
YAML parse (PyYAML) plus structural assertions (job matrix, pinned
actions, gofmt gate, race flag, cache flags) and by reading the Wails
build-tag sources to confirm the tag choices; the exact runner behavior
on GitHub remains unverified.

**Tests added (`platform_test.go`):** per-OS prompt content (windows/
linux/darwin, including absence of the wrong platform's hints),
production prompt follows `runtime.GOOS`, shell-info/runShell
consistency, and the app-context cancel test described above.

**Gate result:** GREEN — gofmt empty · vet ok · build ok ·
`go test -race -count=1 ./...` ok · windows vet ok · linux vet ok.

**Assumptions**

- On Linux the valuable extra check is the *tagged* desktop compile
  (`production webkit2_41`); untagged vet/test stays as the fast lane —
  both run in the ubuntu job.
- `darwin` is covered by the injectable prompt tests, not by CI (no
  macOS runner included; product targets Windows primarily).

## Stage 5 — MCP client (stdio) — DONE (2026-11-02)

**What was done**

- **`mcp.go`** — JSON-RPC 2.0 over the child's stdio (newline-delimited;
  no raw newlines inside messages, enforced): `initialize` (client info,
  capabilities `{}`, protocolVersion `2025-06-18`; the server's returned
  version must be one of the four known versions 2024-10-07 … 2025-06-18
  or the connection fails with a clear error) →
  `notifications/initialized`; paginated `tools/list` (follows
  `nextCursor`, hard cap 200 tools/server); `tools/call {name,
  arguments}` with `content[]`/`isError` handling; server→client
  requests answered safely (`{}` to `ping`, JSON-RPC `-32601` to
  everything else — sampling/roots unsupported); unknown
  notifications/fields tolerated; stdin writes serialized by `writeMu`.
- **Security**: connect-time child environment is a minimal allow-list
  (PATH, SystemRoot, TEMP/TMP, HOME/USERPROFILE, USER, locale vars,
  COMSPEC/PATHEXT, APPDATA/LOCALAPPDATA, XDG_*) **plus** the configured
  env — never the app's settings/API keys. Commands with NUL/newline
  rejected; resolution via `exec.LookPath` (Windows `npx`→`.cmd` handled
  by the stdlib, no shell strings). Child stderr is captured in a 64 KB
  ring and written to the error log rate-limited (every 2 s). The whole
  process **tree** is killed via the shared `procTree` helpers
  (Setpgid/SIGTERM→SIGKILL on Unix, Job Object on Windows).
- **`mcpmanager.go`** — lifecycle: lazy start on first need (when
  building a turn's tool list) with a 15 s budget for initialize+list;
  servers start **in parallel**; a failing server logs, gets status
  «خطا» and does not break the turn. Unexpected exit → client removed,
  lazy restart with backoff 1 s/2 s/4 s; after 3 consecutive failures
  retries stop until settings are re-saved (config-fingerprint change
  resets the bookkeeping). In-flight calls on a dead server fail fast
  with an English error (pending map is failed on exit). Concurrent
  `ensureStarted` calls are single-flight. `reload` kills
  changed/disabled/removed servers and keeps healthy unchanged ones;
  `App.shutdown` stops everything. Lock order documented in
  ARCHITECTURE.md (`a.mcp.mu` is a leaf; long work happens outside).
- **Settings**: `MCPServers []MCPServerConfig` (`omitempty`; old
  settings load unchanged — fixture test). Env values are **encrypted at
  rest** with the same keyring-backed AES-GCM as API keys;
  `SettingsView` returns only a mask (`••••••••`) and `SaveMCPServers`
  keeps the stored ciphertext when the mask comes back unchanged.
  Names validated `[a-zA-Z0-9_-]{1,32}` + unique; Persian validation
  errors from the backend.
- **Exposure**: tool name `mcp_<server>_<tool>` sanitized to
  `[a-zA-Z0-9_-]`, ≤ 64 chars (truncate + stable FNV-1a base-36 hash on
  overflow/collision), description prefixed `[MCP:<server>] `, schema =
  the server's `inputSchema` (`type:object` forced, `$schema` dropped,
  >16 KB schemas replaced by a neutral object schema). Arguments are
  only checked to be a JSON object (the server validates). MCP tools
  are **sequential** (not in the parallel list) and invisible to the
  sub-agent; permission default **ask** (modal shows server, tool and a
  truncated args JSON in the bidi-isolated LTR box; «همیشه اجازه» via
  the existing permissions-tab override). Results: text items
  concatenated, images/audio/blobs → `[image omitted: <mime>, N bytes]`,
  `isError` → `ERROR: …`, output capped by the shared rune-safe
  tool-output limit, wrapped in `<mcp_content server="…" tool="…"
  untrusted="true">` with closing-tag escaping. Per-call timeout 60 s
  (configurable var, shortened in tests); Stop sends
  `notifications/cancelled` for the request id and returns promptly.
- **UI**: new readable `mcpsettings.js` + a new «MCP» tab in the
  settings modal (index.html got only the tab button + empty panel div —
  the panel is built entirely by JS): Persian JSON editor with sample,
  save via `SaveMCPServers` (backend Persian errors shown verbatim),
  per-server status lines (متصل / در حال اتصال / خطا: … / غیرفعال +
  tool count) via `MCPStatus()`, Mock-safe. Bindings updated by hand
  (`App.js`, `App.d.ts`, `models.ts` gained `MCPServerConfig`;
  bindings parsers stay green).

**Tests (21 new functions across `mcp_test.go`,
`mcp_posix_test.go`, `mcpfakeserver_test.go`)** — the fake MCP server is
a `TestHelperProcess`/re-exec helper supporting: healthy handshake with
2-page `tools/list`, bad protocol version, immediate crash, child-of-
child spawn (tree-kill proof), server→client ping/unsupported-request,
hanging tool, huge output, image, error and Persian text. Covered:
handshake + pagination + exposure ([MCP:fake] prefix, $schema dropped,
type object), bad-version failure + status, disabled server (no tools,
«غیرفعال» label), text/image-placeholder/isError/Persian-with-ZWNJ-
byte-exact/large-output-cap calls, hanging tool timeout (shortened via
the injectable var), user-cancel promptness, ping/-32601 replies keeping
the pipe alive, crash → backoff → give-up → re-save reset, unexpected
exit fails fast, name sanitization/64-char cap/stable hash, env
encryption + masking round trip + old-settings fixture, Persian
validation errors, permission default + registrations, **shutdown kills
the whole process tree** (group-emptiness proven with
`syscall.Kill(-pgid, 0)`, child-of-child included), goroutine-leak
check, untrusted-wrapper escaping, UI status labels, concurrent
single-flight start. A `-race` data race on the client `closed` flag
(writeMu vs pendMu) was found and fixed by unifying the guardian.

**Gate result:** GREEN — gofmt empty · vet ok · build ok ·
`go test -race -count=1 ./...` ok (14.2s) · windows vet ok · linux vet ok.

**Assumptions**

- "configurable constant" for the 60 s per-call timeout → a package
  `var` with a test setter (default unchanged).
- The MCP tab is a 9th settings tab; all its content lives in the new
  readable `mcpsettings.js` (index.html only gained the tab button, an
  empty panel div and the script tag).
- The tree-kill regression test is Unix-only (`//go:build !windows`,
  same precedent as the existing posix tests); the Windows Job-Object
  path is compile-checked and listed in the manual checklist.
- Hand-written settings with a plaintext env value pass it through
  (only `v2:` values must decrypt); UI-saved values are always
  encrypted.

## Stage 6 — Cleanup, consistency, docs, release 1.7.0 — DONE (2026-11-02)

**What was done**

1. **Tool-registry consistency tests** (`toolregistry_test.go`): every tool
   in `agentToolDefs()` must have a valid (non-deny) default permission, an
   argument spec (custom tools are exempt and are not in agentToolDefs),
   a mention in the generated system prompt, a valid parameters schema,
   and an entry in the frontend icon map (app.js parsed as **text**); a
   representative MCP tool must default to ask and get the `mcp_` icon
   fallback (`bolt`, added as a tiny hook in the minified icon function);
   the parallel list and the sub-agent allow-list may contain only
   existing read-only (default-allow) tools. **No drift was found** — the
   wiring discipline held through all five feature stages.
2. **Test files renamed by topic** (git mv, no logic changes):
   `phase1_test.go` → `gitcontext_test.go` (git context line: computed
   outside `a.mu`, output caps), `phase3_test.go` → `atomicwrites_test.go`
   (atomic image externalization; also received the atomic-write test
   that lived in phase1), `phase4_test.go` → `tempsweep_test.go`
   (startup temp-file sweep), `phase4_posix_test.go` →
   `procgroup_posix_test.go` (process-group ESRCH guard).
3. **Internal step/round numbering removed** from comments and docs
   (گام N / فاز N / ۳b-A6 / A1–A7 → behavior descriptions) via a
   comment-only rewriting script (`scripts/stage6_renumber.py`) that
   never touches code or leading whitespace. Two script bugs were caught
   and repaired during the work (an over-eager whitespace collapse that
   broke indentation — reverted cleanly from git; and a
   space-before-punctuation rule that glued `//go:build !windows` —
   fixed in all 5 affected files and re-verified with both cross-vets).
   Legitimate terms («فاز اتصال» = connection phase, «گام‌به‌گام» =
   step-by-step) were preserved; zero step references remain
   (`grep`-verified across *.go, README, ARCHITECTURE, BUGFIXES).
4. **Docs & release**: ARCHITECTURE.md gained the `chat:todos` event, the
   permission-ask `url`/MODAL payload changes, the full tool inventory
   paragraph (multi-edit, glob ordering, web_fetch guard, MCP lifecycle),
   the `mcpServers` settings entry, the MCP lock order (`a.mcp.mu` leaf)
   and a CI section. README gained the 1.7.0 news section + updated
   feature/tool lists + project structure. CHANGELOG.md created covering
   1.1.0 → 1.7.0 (earliest documented version). Version bumped
   1.6.0 → 1.7.0 in `main.go`, `wails.json`, `build.sh`, README and the
   CI build job (grep-verified: no 1.6.0 remains).
5. **Final self-audit** (all executed and passing):
   - full gate GREEN (see below); **race is real** (gcc 14 present, `-race`
     runs — no stage is race-unverified);
   - no TODO/FIXME/commented-out code in the new files;
   - no new `context.Background()` in request paths that should honor
     Stop (the four chat entry points keep their existing
     cancel-on-Stop gen machinery; `appContext()` falls back to
     Background only when the Wails startup ctx is nil, i.e. tests);
   - no `a.mu` held across network I/O/process exec/`runtimeEmit` in new
     code (conversation disk save under `a.mu` is the established
     `saveConversation` contract; MCP long work happens outside `mcp.mu`);
   - bindings in sync both ways (`TestWailsBindingsInSync` — Go ↔ JS ↔ TS
     counts, plus model-type coverage) — includes the new
     `SaveMCPServers`/`MCPStatus` and `MCPServerConfig`/`TodoItem` models;
   - `GOOS=windows` and `GOOS=linux` vet both pass (after the build-tag
     repair above);
   - old `settings.json` (without `mcpServers`) and old conversation
     files (without `todos`) load unchanged (fixture tests);
   - **could not verify locally**: real-Windows runtime behavior (Job
     Object tree-kill, actual WebView/modal rendering, real keyring),
     GitHub Actions execution of ci.yml, and real third-party MCP
     servers — all covered by reading, offline validation and the manual
     checklist in FINAL_REPORT.md.

**Gate result (final run):** GREEN — `gofmt -l .` empty · `go vet ./...` ok ·
`go build ./...` ok · `go test -race -count=1 ./...` ok (13.5s) ·
`GOOS=windows GOARCH=amd64 go vet ./...` ok ·
`GOOS=linux GOARCH=amd64 go vet ./...` ok.
Test suite size: **360 test functions in 41 files**.

## Final audit — full-project bug/resource/reliability review — DONE (2026-11-02, v1.7.1)

User-requested full review of the whole project (backend + frontend + resource
consumption + reliability) before release. Two independent deep audits
(backend Go, frontend JS/CSS/HTML) + a focused data-boundary security trace.

**Backend fixes (Go):**

1. **MCP crash recovery was dead code** (`onClientExit` never called): a
   crashed MCP server stayed in the manager map forever, the next
   `ensureStarted` spawned a fresh process and then killed it ("existing
   wins") and returned the dead client, and the status stuck at
   "connecting" permanently. Wired `onExit` through `startMCPClient` →
   `waitExit` (deliberate stops flagged via `atomic.Bool` so they don't
   count as failures), added dead-client replacement in the existing-wins
   branch, an isClosed guard before installing a just-started client, and
   instance-identity checks in `onClientExit` (no double counting).
   Backoff/give-up/re-save lifecycle now actually works (regression test
   with the new `fake-die` fake-server mode).
2. **`writeJSON` could block forever** on a hung server that stopped
   reading stdin (pipe buffer full) — wedging the tool worker, the whole
   agent turn, and even Stop. Added a watchdog (`mcpWriteTimeout`, 10s,
   test setter): write in a goroutine + `select` with a timer; on expiry
   the process tree is killed (process death closes the pipe and unblocks
   the writer). Regression test with the new `fake-silent` mode.
3. **`waitExit` vs `readLoop` tail-data race**: `cmd.Wait()` force-closes
   the stdout pipe while `readLoop` may still be draining the final
   responses of a dying server. `readLoop` now closes a `readDone`
   channel; `waitExit` joins it (capped at 2s for a grandchild holding
   stdout) before calling `Wait`.
4. **`runGitIn` had no `WaitDelay`**: a git child (credential helper,
   hook, filter) inheriting stdout could block `cmd.Run` past the 20s
   timeout forever. Now `cmd.WaitDelay = 5s` (same as shell.go).
5. **Sensitive-read modal used `context.Background()`** (uncancellable up
   to 3 min while holding `permAskMu`) — now `a.appContext()` like
   RunCommand, so app shutdown releases it immediately.
6. **Rules file was read in full before the 12KB cap** — under `a.mu`! A
   multi-GB AGENTS.md would have stalled every lock user. Now a bounded
   read (`io.LimitReader`, cap+1 to detect overflow).
7. **Emit-under-lock**: `conv:size_warn` was emitted under `a.mu`
   (saveConversation) and `agent:change_reverted` under `a.changes.mu`
   (RevertFileChange). First now emits from a one-shot bounded goroutine;
   second was refactored to emit after unlock (recordAppliedChange
   pattern).
8. **`scheduleRefresh` didn't debounce** — each tools/list_changed
   notification spawned its own refresh goroutine. Real single-flight now
   (refreshing/refreshQueued flags under `m.mu`).
9. **Timed-out MCP calls left their `pending` map entry** until a
   response or connection death — best-effort delete on ctx.Done now.
10. **`tree.attach` error path leaked the three pipes** — closed now
    (`Process.Wait` bypasses exec's cleanup).
11. **`ignoreStack` had no total cap** — a pathological tree with
    thousands of nested .gitignore files grew it unbounded and made
    `ignored()` O(total-rules) per file. Capped at 20000 rules.
12. **shutdown** now waits up to 2s for the stream goroutine
    (`stopGenerationAndWait`) so the last conversation save isn't cut mid-
    flight.
13. Timeout error message reported `mcpCallTimeout` even when the parent
    ctx expired first — now reports the actual elapsed wait.
14. Removed the dead `mcpReadTimeout` constant (never wired, misleading).

**Frontend fixes (readable additive files; minified app.js untouched):**

1. **CRITICAL — permission modal was invisible**: `permissions.js` never
   added the `.open` class; `.modal-overlay` defaults to
   `opacity:0;pointer-events:none`, so every `agent:permission_ask`
   (command execution, sensitive reads, web_fetch, MCP) was un-approvable
   and silently auto-rejected after the 3-minute timeout. Reproduced live
   in a headless browser by the audit. One-line fix + Go regression test
   (text-parse, same pattern as toolregistry_test.go).
2. **todos panel stale on conversation switch**: the GetConversation
   patch ran its `.then` before app.js set `activeId`, so the
   "active conversation only" guard rejected the freshly opened
   conversation. Now deferred by one macrotask (`setTimeout 0`).
3. **MCP editor showed the pre-save config** after saving (snapshot in
   `MyAIState.view.mcpServers` never refreshed; re-saving silently
   reverted edits). Now re-fetches `SettingsView` and re-populates the
   editor after a successful save.
4. **Saving an untouched SAMPLE wrote it as real config** — empty server
   list now saves `[]` unless the JSON actually differs from the sample.

**Security hardening (data boundary — backend, no minified-JS edits):**

A focused trace showed the unescaped id/color interpolations in the
minified UI files ARE reachable with attacker-controlled content: a
shared/planted `settings.json` (provider `id`/`color` are never validated
on load) or a planted conversation file (index rebuild took the `id` from
file contents) could inject script into the WebView, which has full
access to the Go bridge. Fixed at the source:

- `sanitizeProviders` on load: invalid id → regenerated from the name
  (slugify charset, Persian ids stay valid), invalid color → default,
  API key migrated to the new id; `SaveProvider` rejects bad ids/colors.
- `buildIndexFromFiles`/reconcile now require `validConvID` — planted
  conversation files with malicious ids are skipped.

**Tests added (`finalaudit_test.go` + `mcpfakeserver_test.go`):** 9 new
tests — MCP crash→backoff→recovery end-to-end, write watchdog kills a
hung server, foreign-client exit not counted, provider-field
sanitization (malicious fixture), SaveProvider rejection, conversation
index guard, and three frontend text-assertions (permission `.open`,
todos macrotask defer, MCP snapshot refresh). Fake server gained
`fake-die` and `fake-silent` modes.

**Known accepted (documented, not fixed):** `time.After(5s)` in
`stopGenerationAndWait` (one-shot, infrequent path); `execTool`'s
`context.Background()` (test-only caller); detached copy-button timer in
markdown.js (harmless no-op); minified-file id/color interpolations now
unreachable with unsafe data (backend validates) — full `esc()` wrapping
deferred to the next regeneration of those bundles.

**Gate result (final):** GREEN — `gofmt -l .` empty · `go vet ./...` ok ·
`go build ./...` ok · `go test -race -count=1 ./...` ok (11.4s) ·
`GOOS=windows GOARCH=amd64 go vet ./...` ok ·
`GOOS=linux GOARCH=amd64 go vet ./...` ok.
Test suite size: **369 test functions** (was 360). Version → **1.7.1**.

---

# مأموریت ۲ — «ایجنت قابل‌اعتماد» (امنیت، UX، دقت، اثبات) — v1.7.1 → v1.8.0

ثبت خودکار ادامه‌ی پروژه مطق مشخصات «Make the Agent Trustworthy». اصل
محصولی همه‌ی تصمیم‌ها: «از کاربر کمتر بپرس — کارها را ایمن، برگشت‌پذیر،
مرئی و راستی‌آزمایی‌شده کن. فقط وقتی بپرس که کاری خطرناک، برگشت‌ناپذیر
یا خارج از پوشه‌ی کاری باشد.»

## محیط (برای همه‌ی مراحل این مأموریت)

- Sandbox لینوکس (Debian 13, amd64)؛ Go 1.26.10 مجدداً زیر ‎/home/z/sdk/go‎
  نصب شد (چون sandbox بازی‌چرخشده بود و SDK بیرون از پوشه‌ی مانده حذف
  شده بود). gcc موجود → ‎-race‎ واقعی.
- Baseline (کامیت ‎4dd7949‎) قبل از شروع Stage 1 دوباره سبز تأیید شد.

## Stage 1 — سخت‌سازی دروازه‌ی دستورات (SECURITY) — DONE (2026-11-03)

**چه شد:** جایگزینی کامل «glob روی رشته‌ی نرمال‌شده» با تجزیه‌گر واقعیِ
شله‌آگاه + لیست‌سفید ساختاری.

- `shellparse.go` (بازنویسی کامل؛ پیش‌نویس نیمه‌کاره‌ی همین فایل از
  نشست قبلی مکمل/جایگزین شد): `parseShellCommand(cmd, flavor)` — دو شله‌ی
  `cmd`/`posix` (تست‌ها هر دو را روی هر OS اجرا می‌کنند از طریق
  `currentCommandFlavor`). قواعد واقعی: کوتیشن‌ها (‎'…"…"‎، ‎""…""‎ در cmd)،
  escape (بک‌اسلش/caret)، جداکننده‌ها (‎&& || | |& ; & \n‎ — «&» تکی در
  «هر دو» شله جداکننده است، «;» در cmd نویسه‌ی عادی)، تغییرمسیرها با
  پیشوند fd و شکل‌های dup (‎2>&1‎/‎>&2‎) و مقصد کوتیشن‌آگاه، heredoc و
  هر ساختار باز → عدم قطعیت → ask. گسترش‌ها: ‎$(cmd)‎/backtick/(…)/<(…)‎
  با payload بازگشتی؛ ‎${…}‎/‎$VAR‎/‎$((…))‎/‎%VAR%‎/‎!VAR!‎ (هر % یا ! در
  cmd محافظه‌کارانه) → پرچم hasExpansion. «نما» (view) بازسازی‌شده از
  argv برای لایه‌های regex — یعنی escape/کوتیشن دیگر regex ها را دور
  نمی‌زند.
- `cmdgate.go` (جدید): لیست‌سفید ساختاری `safeCommandSpec` (argv0/sub/
  sub2/flags whitelist/denied/positional) — git status|diff|log|show|
  branch(فقط فهرست)|rev-parse|remote(-v) با لیست پرچم صریح و
  ‎--output/--ext-diff/-c/--upload-pack/-C/--git-dir/--work-tree‎ ممنوع؛
  go build|test|vet با ‎-v -run -count -race -short -timeout -cover
  -bench -tags -p‎ و ‎-exec/-toolexec/-vettool/-o‎ ممنوع؛ npm test و
  npm run test|lint|build|typecheck بدون پاس‌دادن آرگومان (‎--‎ ممنوع)؛
  ls/dir/echo/pwd/whoami/hostname/where/which؛ نسخه‌های ‎--version‎ دقیق.
  **cat/type/head/tail/wc/npx حذف شدند** (ایجنت read_file دارد — D3).
  تغییرمسیر فقط به NUL/‎/dev/null‎/dup (D2). positional مطلق فقط داخل
  پوشه‌ی کاری، «..» گذرنده و «~» ممنوع. توکن/مقصد حساس (الگوهای حساس +
  پوشه‌های ‎.ssh/.aws/.gnupg/.kube/.config/gcloud‎ + ‎%APPDATA%‎ + ‎~/‎)
  → حداقل ask با دلیل «دسترسی به فایل حساس» و بدون گزینه‌ی «همیشه»
  (فیلد `NoAlways` در payload مودال + مخفی‌شدن دکمه در permissions.js).
- قواعد D4: لیست سیاه هرگز قابل override نیست؛ الگوی پرریسک حتی با
  قاعده‌ی allow عام → ask مگر خودِ متن الگو توکن پرریسک را صریح داشته
  باشد («git push --force*» پذیرفته می‌شود، «git push*» نه)؛ **و
  کاهش سطح مشابه برای خطر ساختاری** (تغییرمسیر فایل/پرچم ممنوع/
  گسترش/پس‌زمینه) حتی زیر قاعده‌ی allow عام — مگر قاعده همان توکن را
  صریح داشته باشد. `deriveAlwaysPattern` فقط برای جفت‌های تأییدشده
  (git add|commit|status|diff|log|fetch، npm install|run|test، go build|
  test|vet|mod tidy، cargo build|test|check، dotnet build|test، pip
  install، pytest، make <target>) الگوی عام می‌سازد؛ برای بقیه فقط
  قاعده‌ی «دستورِ دقیق» — هرگز برای push/reset/clean/rm/del/curl/wget/
  powershell/cmd/sh/bash/python -c/node -e/npx/sudo/chmod/kill/taskkill.
- `deriveAlwaysPattern`/`requestCommandPermission`/`gateCommand` — امضای
  `evaluateCommandPermissions(cmd, rules)` حفظ شد؛ `evalCommandPerm`
  حالا با الگوهای حساس کاربر + پوشه‌ی کاری + قاعده‌های نشست ارزیابی
  می‌کند. پاسخ جدید «session» (اجازه تا پایان نشست، حافظه‌ای —
  `appendSessionCommandRule`) + دکمه‌ی «در این نشست» در مودال.
- ۳.۲.۸: پرامپت سیستمی درست شد — ابزارهای فایل محدود به پوشه‌اند اما
  run_command «gated but NOT sandboxed» و مدل باید قبل از اجرا اثر
  دستور را دقیق بگوید.
- باگ‌های قدیمی پیدا و رفع شد ضمن کار: الگوی hard-block «‎del /f /s /q
  C:\‎» هرگز match نمی‌شد (regex بک‌اسلش می‌خواست در حالی که
  normalizeSegment بک‌اسلش را حذف می‌کند؛ همچنین ‎/f‎ در کلاس نبود) —
  هر دو اصلاح شد؛ الگوی عمومی «هرچیز | sh» به لیست سیاه اضافه شد
  (خروجیِ سمت چپ pipe به‌عنوان کد اجرا می‌شود).

**تست‌ها:** `shellparse_test.go` (۹ تابع: جداکننده‌ها/کوتیشن/تغییرمسیر/
caret/گسترش/پس‌زمینه/fork bomb/payload مفسر/مرزها + متن فارسی و ZWNJ) و
`cmdgate_test.go` (بازتولید D1–D6 با و بدون قواعد عام کاربر؛ **کورپوس
۲۷۶ موردی** در هر دو شله؛ خاصیت ترکیب «هرگز شل‌ترنشدن» روی همه‌ی
جداکننده‌ها × ۱۵ دستور + تساوی کامل برای ترکیب‌های بدون الگوی
چندجزئی؛ ۴۰۰ ترکیب تصادفی با seed ثابت ۲۰۲۶۱۱۰۳ چاپ‌شونده هنگام خطا؛
ترتیب قواعد؛ حساس بدون always؛ قاعده‌ی نشست؛ جدول never-list الگو؛
سازگاری مشخصه‌ها؛ مسیرهای فارسی). `permissions_test.go` به‌روزرسانی
مشروع شد: TestSplitCommandSegments حذف (تابع حذف شده)، مورد «git push*
  allow → git push --force» از allow به ask تغییر یافت (دقیقاً D4-b)،
  جدول TestDeriveAlwaysPattern برای مشتق امن جدید، فراخوانی‌های
  requestCommandPermission با پارامتر sens.

**انحراف مستندشده:** خاصیت «تساوی کامل» ترکیب فقط برای ترکیب‌های بدون
الگوی چندجزئی (خانواده‌ی ‎curl|sh‎ و «هرچیز|sh») برقرار است — این
الگوها ترکیب را «سخت‌گیرانه‌تر» از اجزا می‌کنند که جهتِ امن درست است؛
تست، عدم‌شل‌شدن (‎>=‎) را برای همه‌ی جداکننده‌ها اثبات می‌کند. قاعده‌ی
«دستورِ دقیق» برای دستورهای مرکب فقط جزء اول را پوشش می‌دهد (قواعد
per-simple ارزیابی می‌شوند — عمداً، تا «git status*» نتواند «git status
&& rm -rf ~» را تأیید کند).

**نتیجه‌ی گیت:** سبز — ‎gofmt -l .‎ خالی · ‎go vet ./...‎ ok ·
‎go build ./...‎ ok · ‎go test -race -count=1 ./...‎ ok (19.2s) ·
‎GOOS=windows GOARCH=amd64 go vet ./...‎ ok · ‎GOOS=linux GOARCH=amd64
go vet ./...‎ ok.

## Stage 2 — حالت ویرایش، مسیرهای محافظت‌شده و بازبینی پایان نوبت — DONE (2026-11-03)

**توجهِ پیوست‌شدگی:** این مرحله در دو نشست نوشته شد؛ نشست اول (که با
پایان زمینه قطع شد) هسته‌ی Go را با اسکریپت‌های وصله نوشت؛ نشست دوم
(ادامه‌ی همین سند) ساختار را از روی commit مرحله‌ی ۱ + اسکریپت‌های وصله
بازسازی کرد، سه شکست گیت را رفع کرد، بایندینگ‌ها/تست‌ها/پشتیبانی Mock را
تکمیل کرد و سه باگ واقعی پیدا و رفع کرد. همه‌ی ذکرشده‌ها پایین‌تر آمده‌اند.

**چه شد (۴.۱ — حالت ویرایش):**

- `Settings.EditMode` («review_each» | «auto») + `ProtectedGlobs` +
  `MaxTurnTokens` (این‌آخر متعلق به ۵.۳ است اما ستون تنظیمش همین‌جا
  گذاشته شد تا UI یکجا شود). **پیش‌فرض نصب تازه: auto** (تغییر عمدی رفتار پیشین)؛ مهاجرت: فایل قدیمی بدون `editMode` → از
  `confirmFileWrites` مشتق می‌شود؛ `SetConfirmFileWrites` کار می‌کند و
  همیشه `EditMode` را همگام نگه می‌دارد؛ `SetEditMode` مقدار نامعتبر را
  با خطای فارسی رد می‌کند.
- **باگ مهاجرت (رفع‌شده در نشست دوم):** `loadSettings` روی
  `defaultSettings()` unmarshal می‌کند؛ چکِ `EditMode == ""` هرگز برای
  فایل قدیمی صادق نبود (پیش‌فرض از قبل «auto» بود) → مهاجرت مرده بود.
  حالا با probe روی کلید JSON تشخیص داده می‌شود؛ مقدار نامعتبر → auto.
- مسیرهای محافظت‌شده (۴.۱): `.git/**`, `.github/**`, `.myai/**`,
  `.vscode/**`, `.idea/**`, `.env*`, `*.pem`, `*.key`, `*.pfx`,
  `package.json`, `Makefile`, `*.mk`, `justfile`, `Taskfile.y*ml`,
  `*.csproj`, `pom.xml`, `build.gradle*` — حتی در auto مودال می‌خواهند
  با یادداشت فارسی (`ConfirmNote`)؛ الگوی بدون «/» با نام فایل در هر
  عمقی تطبیق می‌کند؛ الگوی مسیردار جزء‌به‌جزء از ریشه (با `**`).
- حذف سنگین (۴.۱): پوشه، فایل بزرگ‌تر از ۵ مگابایت، و هرچه بکاپش ممکن
  نشود → همیشه تأیید (بکاپ حالا «پیش از» تأیید گرفته می‌شود تا شکستِ
  بکاپ خودش به پرسش تبدیل شود، نه خطای بعد از پرسش). حذف بیش از ۲۰
  فایل ساختاراً ناممکن است (حذف بازگشتی مسدود است — مستند شد).
- پیام نتیجه‌ی ابزار حالا صادق است (رفع نشست دوم): در auto بدون پرسش →
  «auto-applied (edit mode 'auto'; review the end-of-turn summary)»؛
  فقط وقتی واقعاً پرسیده شده → «approved by the user».

**چه شد (۴.۲ — مدل نوبت و خلاصه‌ی پایان نوبت):**

- هر `streamChat` یک `TurnID` صعودیِ atomic می‌گیرد (`turnCounter`) و از
  طریق ctx به همه‌ی ابزارها (و زیرایجنت) می‌رسد؛ همه‌ی `FileChange`های
  همان نوبت با آن علامت می‌خورند + `ContentSHA256` چیزی که ایجنت نوشت.
- `turnRecorder` (از طریق ctx): دستورات اجراشده (≤ ۲۰۰، با کد خروج) و
  توالیِ «آخرین تغییر فایل»/«آخرین راستی‌آزماییِ موفق» — الگوهای
  راستی‌آزمایی = go build/test/vet، npm test/run build|lint|typecheck،
  cargo، dotnet، pytest/unittest، mvn/gradle، make، tsc.
- پایان هر نوبت (خاتمه‌ی عادی، خطا، قطع جریان): `chat:turn_summary`
  {conversationId, turnId, files, commands, verification, tokens} —
  **همیشه از رکوردهای ابزار، هرگز از متن مدل**؛ نسخه‌ی محدود (آخرین ۵۰)
  در خود گفتگو ذخیره می‌شود و با بازکردن گفتگو `chat:turn_summaries`
  دوباره پخش می‌شود.
- API جدید: `GetTurnChanges(convID, turnID)` و `RevertTurnChanges(convID,
  turnID)` → گزارش {reverted, conflicts, errors}. **قاعده‌ی تعارض:** اگر
  محتوای فعلی با هشِ چیزی که ایجنت نوشته فرق داشته باشد (کاربر بعدش
  دستکاری کرده) فایل بازنویسی نمی‌شود و به‌عنوان تعارض گزارش می‌شود؛
  فایلِ حذف‌شده‌ای که کاربر دوباره ساخته هم تعارض است.
- **باگ هش جابه‌جایی (رفع‌شده در نشست دوم):** `movePathWithConfirm` هش
  را پس از rename از `src` می‌خواند (دیگر وجود ندارد) → هش همیشه خالی و
  تشخیص تعارضِ move هیچ‌وقت کار نمی‌کرد؛ حالا از `dst` خوانده می‌شود.
- فرانت‌اند (فایل‌های خوانای جدید، minified دست‌نخورده جز دو خط
  index.html): `turnsummary.js` (کارت «خلاصه‌ی واقعی این نوبت»: فایل‌ها با
  +/− و برچسب «محافظت‌شده»، دستورها با کد خروج، چیپ هشدار «پس از آخرین
  تغییر، تست یا ساخت اجرا نشده»، دکمه‌های «برگرداندن همه‌ی تغییرات این
  نوبت» و per-file، دیافراگ diff با `GetFileChangeDiff`؛ مودال diff با
  کلاس `open` واقعاً مرئی می‌شود — رفع الگوی باگ ۱.۷.۱)،
  `turnsummary.css`، `editmode.js` (انتخابگر دوگزینه‌ای با توضیح یک‌خطی در
  تب پوشه + فیلد «سقف توکن هر نوبت»). پشتیبانی Mock: هوک افزایشی انتهای
  `mock.js` (بدون دست‌زدن به بدنه‌ی minified) — نمونه‌ی
  `chat:turn_summary` در حالت ایجنت + بایندینگ‌های ساختگی چهار متد جدید.
- بایندینگ‌ها دستی به‌روز شدند (پروتکل ARCHITECTURE.md): `App.js` /
  `App.d.ts` چهار متد + `models.ts` (هر دو نسخه) فیلدهای جدید `FileChange`
  و کلاس `TurnChangesReport` — تست همگامی سبز.

**تست‌های به‌روزرسانی‌شده (تغییر عمدی رفتار پیش‌فرض):**
`TestConfirmFlowReject`/`TestConfirmFlowAccept` صراحتاً `review_each` ست
می‌کنند (مودال کامل را می‌آزمایند)، `TestDeleteAndRevertEmptyDir`
تأییدگر خودکار می‌بندد (حذف پوشه حالا همیشه می‌پرسد)؛
`TestSetConfirmFileWrites` همگامی دوطرفه با EditMode و ماندگاری روی
دیسک را هم می‌سنجد.

**تست‌های افزوده (`turnreview_test.go` — ۱۶ تابع):** مهاجرت EditMode
(جدول ۵ حالت: قدیمی روشن/خاموش، جدیدِ هر دو مقدار، نامعتبر) + probe
کلید JSON؛ پیش‌فرض auto؛ اعتبارسنجی/نرمال‌سازی SetEditMode + ماندگاری؛
clamp سقف توکن (۰/کف/سقف/داخل محدوده)؛ جدول ۳۶ موردی مسیرهای محافظت‌شده
(فارسی، ویندوزی `src\.git\config`، basename در هر عمق، غیرمطابق‌ها)؛
auto: مسیر عادی بی‌مودال + پیام صادق، مسیر محافظت‌شده مودال + پرچم +
یادداشت فارسی؛ حذف سنگین (پوشه/فایل ۵MB+)؛ جدول isVerificationCommand؛
پنجره‌ی راستی‌آزمایی (بعد از تغییر OK، قبل از تغییر فقط Ran، تست خراب
هیچ، ضبط‌کننده‌ی nil بی‌خطر)؛ سقف ۲۰۰ دستور؛ payload کامل خلاصه‌ی نوبت؛
سقف ۵۰ خلاصه؛ برگرداندن بایت‌دقیق (BOM+CRLF+فارسی با ZWNJ؛ ایجاد+ویرایش؛
دوباره = no-op)؛ تعارض ویرایش (ویرایش کاربر برنده می‌ماند)؛ تعارض حذف
(فایل بازساخته‌شده دست‌نخورده می‌ماند)؛ GetTurnChanges + شناسه‌ی نامعتبر؛
رکوردهای قدیمی بدون turnId (بارگذاری + عدم حضور در کوئری نوبت)؛ یکتایی/
صعودی بودن TurnID با ۶۴ گوروتین هم‌زمان.

**تست مرورگر هدلس (نشست دوم):** `mock_smoke` با Playwright — سرو
استاتیک `frontend/dist`، وضعیت Mock (Provider+کلید+حالت ایجنت)، ارسال
پیام، انتظار برای پایان پاسخ؛ کارت خلاصه «قابل‌مشاهده» است (نه فقط در
DOM): عنوان، ۲ ردیف فایل، برچسب محافظت‌شده، نکته‌ی راستی‌آزمایی، دکمه‌ی
برگرداندن — همه visible و بدون خطای صفحه (اسکرین‌شات ضبط شد).

**فرض‌ها و انحراف‌ها:**

- `.git/**` و الگوهای مسیردار از **ریشه‌ی پوشه‌ی کاری** تطبیق می‌کنند؛
  `src/.git/config` (submodule) محافظت نمی‌شود — خوانش طبیعی
  مشخصات؛ اگر لازم شد الگوی `**/.git/**` به فهرست اضافه شود.
- «حذف بیش از ۲۰ فایل» ساختاراً ناممکن است (حذف بازگشتی مسدود) —
  مستند شد و پوشش تستی ندارد.
- نسخه همچنان **1.7.1** است؛ ارتقای سراسری به 1.8.0 مطابق مشخصات به
  مرحله‌ی ۶ (مستندات/انتشار) این مأموریت موکول شد که هنوز اجرا نشده.
- دو رکورد «خلاصه‌ی نوبت» نامرئی در `mock.js` (بدون بک‌اند) با پیام
  پیش‌نمایش جواب می‌دهند — الگوی todos.js.

**وضعیت مراحل بعدی این مأموریت (صادقانه):** مرحله‌ی ۳ (خواندن-پیش از
نوشتن، تذکرِ (nudge) راستی‌آزمایی، تحلیل پروژه، اعمال سقف توکن در حلقه)، ۴
(checkpoint با git)، ۵ (باقی‌مانده‌های سخت‌سازی: web_fetch per-host و
SSRF و MCP env)، و ۶ (e2e ساخت‌یافته، تست قرارداد UI، مستندات و
انتشار 1.8.0) هنوز پیاده‌سازی نشده‌اند؛ ادامه از همین‌جا.

**نتیجه‌ی گیت (آخرین اجرا):** سبز — ‎gofmt -l .‎ خالی · ‎go vet ./...‎
ok · ‎go build ./...‎ ok · ‎go test -race -count=1 ./...‎ ok (۱۵.۹s) ·
‎GOOS=windows GOARCH=amd64 go vet ./...‎ ok · ‎GOOS=linux GOARCH=amd64
go vet ./...‎ ok.

# مأموریت ۳ — «اتمام کارهای باقی‌مانده» (1.7.1 → 1.8.0)

## Mission 3 — Stage 0 — پايه و پاک‌سازی — DONE (2026-11-04)

**چه شد:**

- **گيت پايه قرمز بود و رفع شد:** `TestCommandGateWorkspaceAware` روی ویندوز
  رد می‌شد (`ls /home/user/proj/src` با ws داده‌شده باید allow باشد). ریشه:
  `commandPathInsideWorkspace` پس از یکسان‌سازی جداکننده‌ها به «/» از
  `filepath.Clean` استفاده می‌کرد که رفتارش وابسته به سیستم‌عامل است — روی
  ویندوز دوباره `\` می‌سازد و مقایسه‌ی پیشوندی مسیرهای POSIX (و مسیرهای
  `C:\…` شله‌ی cmd) می‌شکست. حالا `path.Clean` (همیشه اسلش) استفاده می‌شود.
  منطق امنیتی تغییر نکرده؛ فقط بازگشتِ شکل مسیر.
- **یافته‌ی جانبی (تست، محیط):** دو تست MCP (`TestMCPCallTimeoutOnHangingTool`،
  `TestMCPCallUserCancelSendsCancelledNotification`) بدون `-race` رد می‌شدند:
  سرور ساختگی در حالت `slow` با `select {}` هنگ می‌کرد و Go در فرایند فرزند
  «all goroutines are asleep - deadlock!» اعلام می‌کرد و می‌مرد (با `-race`
  گوروتین‌های اضافیِ race این تشخیص را منتفی می‌کردند). همین رفتار در درخت
  دست‌نخورده‌ی HEAD هم بازتولید شد (با `git worktree` تأیید شد) — پیش‌از‌این
  وجود داشت. اصلاح: `for { time.Sleep(time.Hour) }` (هنگ ابدی، ولی تایمر
  runtime را زنده نگه می‌دارد). رفتار تست عوض نشد.
- **بازبینی «انجام‌شده‌ها» (فقط خواندن):** `evaluateCommandPermissions`،
  `parseShellCommand`، `turnRecorder`، `isVerificationCommand`،
  `chat:turn_summary`، `RevertTurnChanges`، `EditMode` موجود و سیم‌کشی‌شده‌اند.
  قلاب‌های تست: `todosEmit`، `permAskEmit`، پاسخ‌دهندگان مودال Diff.

**گيت:** سبز — gofmt خالی · vet ok · build ok · `go test -race -count=1` ok
(۱۰۱.۵s) · vet ویندوز/لینوکس ok.

## Mission 3 — Stage A — دقت ایجنت (خواندن‌پیش‌از‌نوشتن، تذکر، واقعیت‌های پروژه، سقف توکن، پرامپت) — DONE (2026-11-04)

**A.1 — محافظ خواندن‌پیش‌از‌نوشتن و فایل کهنه (`fileread.go`، جدید):**

- `fileReadStore`: حافظه‌ی اثر انگشت (`size`, `mtime`, `SHA-256`) با LRU — سقف
  ۵۰۰ رکورد در هر گفتگو و ۱۶ گفتگو (کل)؛ کلید = (convID, مسیر مطلق). قفل
  داخلی‌اش **قفل برگ** است (هیچ قفل دیگری زیرش، بدون I/O) — به سند ترتیب
  قفل‌ها اضافه شد.
- `read_file` هشِ کامل محتوا را حین خواندن (بدون I/O دوم) می‌سازد و ثبت
  می‌کند؛ فایل خالی هم ثبت می‌شود. خواندن‌های زیرایجنت ثبت نمی‌شوند
  (`subagentNoAsk`).
- `write_file` روی فایلِ موجود: اگر رکوردی نباشد → رد با راهنمای `read_file`؛
  اگر هش رکورد با محتوای فعلی نخواند → رد با «فایل از زمان خواندن عوض شده».
  ساخت فایل تازه همیشه آزاد؛ فایلی که ایجنت خودش ساخته/نوشته «دیده‌شده» است.
- `edit_file` (تک و چند): کهنه‌بودن را می‌بیند؛ اگر هرگز خوانده نشده باشد
  مجاز است ولی در نتیجه یادآوری می‌کند (تطبیق دقیق `old_string` محافظ دوم).
- `move_path` مقصد را ثبت و مبدأ را فراموش می‌کند؛ `delete_path` رکورد را
  پاک می‌کند؛ حذف گفتگو/همه‌ی گفتگوها رکوردها را دور می‌ریزد (بدون نشت).
- تست‌ها (`fileread_test.go` — ۹ تابع): بازنویسی ناخوانده رد، کهنه رد،
  ساخت‌توسط‌ایجنت مجاز، ویرایش ناخوانده + یادآوری، ویرایش کهنه رد،
  BOM+CRLF+فارسی/ZWNJ بایت‌به‌بایت، زیرایجنت نمی‌شمارد، LRU/گفتگوها،
  جداسازی گفتگوها، مسیرهای فارسی با فاصله/نیم‌فاصله، move/delete.

**A.2 — تذکر راستی‌آزمایی (یک‌بار در نوبت):** در `streamChat`، وقتی مدل پاسخ
نهایی می‌دهد، فایلی تغییر کرده، بعد از آخرین تغییر راستی‌آزماییِ موفقی نبوده
و دست‌کم یک دور دیگر جا هست → یک پیام `user` انگلیسی (`verificationNudgeText`)
و ادامه‌ی حلقه با `round--` (نه بودجه‌ی دور، نه محافظ تکرار). متن مدل قبل از
تذکر به‌عنوان پیام assistant در تاریخچه می‌نشیند (پروتکل سالم می‌ماند).
اعلان فارسی یک‌خطی هم به کاربر نشان داده می‌شود.

**A.3 — واقعیت‌های پروژه (`projectfacts.go`، جدید):** تشخیص Go/Node/Cargo/
Python/Maven/Gradle/.NET/Composer/Makefile از فایل‌های تعریف ساخت؛
**هیچ چیزی اجرا نمی‌شود**؛ حداکثر ۶۴KB از هر فایل؛ کش تک‌ورودی با اثر انگشت
(حجم+mtime+dir) و باطل‌سازی با mtime؛ بلوک انگلیسی کوتاه (≤۶ دستور) یک‌بار در
ابتدا‌ی نوبت به system prompt اضافه می‌شود — بیرون از `a.mu`.

**A.4 — اعمال «سقف توکن هر نوبت» (`MaxTurnTokens`):** مصرف تجمعی همه‌ی دورها +
**آمار زیرایجنت‌ها** (`usageTally`) با usage واقعی و در نبود آن تخمین؛ اعلان
فارسی یک‌بار در ۸۰٪؛ عبور از سقف → پایان مودبانه **بعد از** اجرای ابزار و
ذخیره‌ی دور (گفتگو قابل «ادامه» می‌ماند) + پیام فارسی با اعداد + فیلد
`tokenCapHit`/`tokenCap` در خلاصه‌ی نوبت. `0` خاموش است. **اعمال سقف بعد از
اجرای ابزار هم دوباره سنجیده می‌شود** (باگ واقعی کشف‌شده در تست: مصرف
زیرایجنت فقط در همان دور به tally اضافه می‌شود).

**A.5 — پرامپت و کانتکست:** قواعد ۱۱–۱۶ (تغییر حداقلی و هم‌سبک، یک پرسش
شفاف‌کننده، هرگز ادعای موفقیتِ نادرست، ممنوعیت خواندن/چاپ راز،
داده‌نه‌دستور، خلاصه‌ی پایانی فارسی)؛ بند ۲ پرامپت حالا اعمال محافظ
خواندن‌پیش‌از‌نوشتن را هم می‌گوید. نشانگر «حذف کانتکست» پس از فشرده‌سازی،
فهرست کارهای جاری را (کران‌دار: ۳۰ مورد / ۱۲۰۰ کاراکتر) حمل می‌کند —
`enrichContextDropMarker` idempotent است و نشانگر یکتا می‌ماند.

**زیرساخت تست:** `eventEmit` (قلاب تستی روی `runtimeEmit`) + هارنس سناریوی
`agent_e2e_test.go`: سرور SSE واقعی با فیلم‌نامهٔ مرحله‌به‌مرحله (متن/ابزار/
usage/بازرسی بدنه‌ی درخواست)، مسیریابی برای زیرایجنت، «کاربر فیلم‌نامه‌ای»
که هر مودال دسترسی را ثبت و پاسخ می‌دهد. تست‌های A.2/A.4/A.5 روی همین
هارنس اجرا می‌شوند (تذکر یک‌بار، بی‌تذکر پس از تأیید، بی‌تذکر بدون تغییر،
تست شکست‌خورده = تأییدنشده، توقف با سقف، هشدار یک‌بار، شمارش زیرایجنت،
`0` خاموش، ادامه پس از سقف، نشانگر کارها).

**تست‌های به‌روزرسانی‌شده (تغییر عمدی رفتار، اعلام‌شده):**
`TestMultiEditSingleFormBackwardCompatible` حالا اول `read_file` می‌زند (تست
همان پیام بی‌تغییرِ قبلی را می‌سنجد، نه یادآوری)،
`TestRevertTurnChangesByteExact` هم پیش از بازنویسی فایل موجود می‌خواند.

**گيت:** سبز — gofmt خالی · vet ok · build ok · `go test -race -count=1` ok
(۱۱۳.۰s) · vet ویندوز/لینوکس ok.

## Mission 3 — Stage B — نقطه‌ی بازگشت پروژه (undo برای اثرهای جانبی دستورها) — DONE (2026-11-04)

**هدف:** پیش از اولین عمل تغییردهنده‌ی هر نوبت، از کل پروژه snapshot بگیر تا حتی
اثرهای جانبی `run_command` هم برگشت‌پذیر باشند — **بدون** دست‌زدن به HEAD،
ایندکس یا working tree کاربر در زمان ساخت، و **بدون** حذف داده در زمان بازگردانی.

**ساخت (`checkpoint.go`):** فقط اگر `git` موجود و پوشه‌ی کاری work tree باشد؛
ایندکس موقت با `GIT_INDEX_FILE` → `git add -A` (`.gitignore` رعایت می‌شود) →
`git write-tree` → `git commit-tree` (هویت از env، والد = HEAD اگر باشد) →
`git update-ref refs/myai/checkpoints/<convID>-<turnID>`. سقف زمانی ۱۰s،
خروجی git کران‌دار (`newCappedGitBuffer`)، پاک‌سازی فایل ایندکس موقت؛
خطا/تایم‌اوت = رد شدن با یک اعلان فارسی، **هرگز** بلاک‌کردن نوبت. حداکثر ۲۰
ref در هر پوشه (قدیمی‌ترها prune می‌شوند).

**محرک (lazy، یک‌بار در هر نوبت):** `writeFileWithConfirm`/`movePathWithConfirm`/
`deletePathWithConfirm` پس از تأیید کاربر و پیش از بکاپ، و `gateCommand` روی
شاخه‌ی `permAsk` **پس از** تأیید تا مودال تأخیر نگیرد. دستورهای خودکارِ
فقط‌خواندنی (`go test`, `git status`, …) snapshot نمی‌سازند.

**پیش‌نمایش/بازگردانی (دو متد exported + بایندینگ دستی):**
`PreviewCheckpointRestore(convID, turnID)` → `CheckpointRestorePreview`
(`rewrite` / `trash` / `unchanged` / `skipped` / `error`)، و
`RestoreCheckpoint(convID, turnID)` → `CheckpointRestoreReport`
(`restored` / `trashed` / `skipped` / `errors` / `trashDir`).
snapshot در پوشه‌ی موقتِ کانفیگ materialize می‌شود (`git archive`/checkout-index
با بافر کران‌دار) و مقایسه بایت‌به‌بایت است.

**بازگردانی هرگز حذف نمی‌کند:** فایل‌های اضافه/تغییریافته اول به
`<configDir>/trash/<workspace-hash>/<timestamp>/` منتقل می‌شوند (خودِ
بازگردانی هم undo-پذیر است)؛ فایل‌های ignore‌شده لمس نمی‌شوند؛ مسیرهای فرار
(`..`, مطلق, `C:\…`) رد می‌شوند (`safeCheckpointRel`/`checkpointAbs`)؛
symlink‌ها skip می‌شوند. سبد زباله در startup پاک‌سازی می‌شود
(قدیمی‌تر از ۱۴ روز یا بیش از ۵۰۰MB best-effort).

**قفل:** `checkpointMu` (برگ) فقط برای mapهای `checkpointTurns`/`checkpointNotified`؛
هیچ I/O و هیچ git ای زیر آن نیست. در lock order همان ردیف «قفل‌های برگ» ثبت شد.

**UI:** در کارت «خلاصه‌ی واقعی این نوبت» دکمه‌ی
«بازگردانی کل پروژه به قبل از این نوبت» → مودال پیش‌نمایش (تعدادها + فهرست‌ها،
مسیرها `dir="ltr"` + isolate) → «بازگردانی کن» (btn-danger) + «انصراف».
مودال با همان `modal-overlay`/`open` ساخته می‌شود (اورلی بدون `open` نامرئی
می‌ماند — درس انتشار قبلی). چیپ «سقف توکن این نوبت پر شد» هم به کارت اضافه شد
(فیلدهای A.4). Mock-mode: `PreviewCheckpointRestore`/`RestoreCheckpoint` نمونه.

**تست‌ها (`checkpoint_test.go`، با مخزن git واقعی موقت؛ بدون git → skip):**
ساخت نقطه‌ی بازگشت HEAD/ایندکس/`git status --porcelain` را بایت‌به‌بایت دست‌نخورده
می‌گذارد؛ والد = HEAD؛ بازگردانی پس از ویرایش+حذف+فایل تازه بایت‌به‌بایت و
سبد زباله پر؛ فایل ignore‌شده لمس نمی‌شود؛ BOM+CRLF+فارسی/ZWNJ و نام‌های
فارسی حفظ می‌شود؛ prune = ۲۰؛ پوشه‌ی غیر-git بی‌اثر؛ زمینه‌ی لغوشده → خطا و
بدون ref نیمه‌کاره؛ مخزن بدون کامیت؛ رد مسیرهای فرار؛ sweep سبد زباله.

**باگ واقعی کشف‌شده در تست:** `PreviewCheckpointRestore` پوشه‌ی materialize را
پیش از مقایسه پاک می‌کرد و همه‌ی فایل‌ها را «بدون تغییر» می‌دید → `defer cleanup()`.

**گيت:** سبز — gofmt خالی · vet ok · build ok · `go test -race -count=1` ok
(۱۵۰.۰s) · vet ویندوز/لینوکس ok. (گیت یک‌بار با `checkpoint_test.go` غیر-gofmt
هم «سبز» گزارش شد؛ نقص `scripts/gate.sh` در مرحله C اصلاح می‌شود.)

## Mission 3 — Stage C — سخت‌سازی باقی‌مانده‌ها (C.1–C.6) — DONE (2026-11-04)

**C.1 — دسترسی web_fetch بر پایه‌ی دامنه (کانال نشت داده بسته شد):**
`webfetchperm.go` جدید: `PermissionSettings.WebHosts []PermissionRule`
(Pattern = میزبان یا `*.domain`)، `sessionWebHosts` در حافظه،
`gateWebFetch` با مودال اختصاصی `kind=web_host` و سه گزینه («یک‌بار» /
«در این نشست» / «همیشه برای این دامنه») — **هیچ گزینه‌ی سراسری‌ای وجود ندارد**.
`SetToolPermission("web_fetch","allow")` خطای فارسی می‌دهد و
`toolPermDecisionLocked` هم override سراسری را «ask» می‌خواند (دفاع دوم).
مهاجرت: هر `tools.web_fetch="allow"` قدیمی در `loadSettings` حذف و در لاگ
خطاها ثبت می‌شود (هرگز بی‌صدا حفظ نمی‌شود). `RemoveWebHostRule` + فهرست
دامنه‌ها در تب دسترسی‌ها + حذف گزینه‌ی «اجازه» برای web_fetch در UI.
SSRF و اعتبارسنجی URL دست‌نخورده‌اند — این لایه فقط «آیا می‌پرسیم؟» را
تعیین می‌کند.

**C.2 — HTML→متن:** لینکِ بازِ بدون `</a>` بقیه‌ی صفحه را نمی‌بلعد:
`flushLink` هنگام شروع هر بلوک/تیتر/li/br/لینک جدید و در EOF اجرا می‌شود
(«متن (url)» مثل لینک سالم). متغیر بلااستفاده‌ی `closingTagEscapeRe` و
import `regexp` حذف شدند.

**C.3 — محدوده‌های SSRF:** `192.0.0.0/24`، `192.88.99.0/24`، `fec0::/10`،
`2001::/32` (Teredo)، `64:ff9b:1::/48` (NAT64 محلی)، `::/96`
(IPv4-compatible) و 6to4 `2002::/16` (با بررسی IPv4 نهفته‌اش) مسدود شدند.
جدول تست شامل این‌ها + رگرسیون موارد قبلی + میزبان‌های عمومیِ مجاز.

**C.4 — محیط فرزند MCP:** لیست سفید به دو گروه تقسیم شد
(`mcpBaseEnvKeys`, `mcpProxyEnvKeys`): `HTTP(S)_PROXY/NO_PROXY/ALL_PROXY`
(هر دو حالت)، `SSL_CERT_FILE/DIR`، `NODE_EXTRA_CA_CERTS`، `SystemDrive`,
`windir`, `ProgramFiles(x86)`, `ProgramW6432`, `ALLUSERSPROFILE`,
`PROCESSOR_ARCHITECTURE`, `NUMBER_OF_PROCESSORS`. تست: همه منتقل می‌شوند و
هیچ متغیر بیرون از لیست (از جمله `OPENAI_API_KEY`) نمی‌رسد.

**C.5 — .git تودرتو:** `**/.git/**` به `defaultProtectedGlobs` اضافه شد تا
`src/.git/config` (submodule) هم مودال تأیید بگیرد. تست مسیرهای محافظت‌شده/
عادی.

**C.6 — بهداشت مخزن:** `scripts/stage{2,6}_*.py` حذف شدند (فقط
`gate.sh` و `gen_wailsjs.py` ماندند)؛ `FINAL_REPORT.md` و `PROGRESS.md` به
`docs/dev/` منتقل شدند (ارجاع در `changes_test.go` اصلاح شد)؛
`.gitignore` بازبینی شد (خروجی ساخت و `.freebuff/` نادیده،
`frontend/wailsjs/` عمداً ردیابی می‌شود). **نقص گیت اصلاح شد:**
`scripts/gate.sh` حالا `gofmt -l .` را «باید خالی» می‌سنجد
(`run_expect_empty`) — پیش‌تر فایلِ غیرفرمت را هم سبز اعلام می‌کرد (همین
مرحله یک بار واقعاً RED داد و اصلاح شد). پیشوند PATH کهنه‌ی محیط توسعه هم از
اسکریپت حذف شد.

**تست‌های به‌روزرسانی‌شده (تغییر عمدی رفتار، اعلام‌شده):**
`TestWebFetchPermissionModalPayload` (kind از `tool` به `web_host`، الگو =
دامنه)، `TestPathMatchesProtectedGlobs` (`src/.git/config` و
`src\.git\config` حالا true).

**گيت:** سبز — gofmt خالی · vet ok · build ok · `go test -race -count=1` ok
(۱۰۶.۲s) · vet ویندوز/لینوکس ok.

## Mission 3 — Stage D — اثبات، مستندات و انتشار — DONE (2026-11-04)

**D.1 — سناریوهای سرتاسری ساخت‌یافته (`agent_e2e_test.go`):**

- هارنس مشترک (از Stage A) توسعه یافت: سربر صفحه‌ی `scenarioStep` دو فیلد
  `Hold/Reached` گرفت (برای «توقف در لحظه‌ی مشخص») و `e2eEnv` حالا پاسخ‌دهنده‌ی
  مودال Diff (`agent:confirm` → ثبت مسیر + `RespondFileConfirm`) را هم دارد.
- سناریوهای ۱ تا ۱۱ مشخصات:
  1. `TestScenarioNormalFixVerifiedNoModals` (از نشست قبل، تکمیل/نگه‌داری شد) — اصلاح عادی در auto: صفر مودال، خلاصه «تأییدشده».
  2. تذکر یک‌بار + پذیرش پاسخ دوم + خلاصه «تأییدنشده» → همان ادعا را `TestNudgeFiresOnceWhenUnverified` روی همین هارنس اثبات می‌کند (بدون تست تکراری؛ در گزارش نگاشت شد).
  3. `TestScenarioWriteUnreadRejectedThenAccepted` — نوشتن روی فایل نخوانده رد، بعد از read پذیرفته.
  4. `TestScenarioExternalEditBetweenReadAndWriteRejected` — تغییر بیرونی دقیقاً در Check مرحله‌ی نوشتن (قطعاً بعد از read) → ردِ کهنه + محتوای کاربر محفوظ.
  5. `TestScenarioCommandGateBypassAttempts` — هشت تلاش دورزدن × دو شله (تزریقی): هر مورد دقیقاً یک ask ثبت‌شده، نتیجه‌ی ابزار `ERROR:` و هیچ اجرایی (فایل‌های نشان `x`/`marker.cs` سالم، بدون `out.txt`/`f`/`node_modules` بیرون و داخل پوشه).
  6. `TestScenarioProtectedPathsAskInAutoMode` — سه مودال Diff برای package.json/.env/src/.git/config و صفر مودال برای مسیر عادی.
  7. `TestScenarioRevertAllRestoresAndConflict` — بازگرداندن نوبت: فایل ساخته‌شده برمی‌گردد، فایل دست‌کاری‌شده‌ی کاربر «تعارض» و دست‌نخورده.
  8. `TestScenarioCheckpointDestructiveCommandRestore` — نقطه‌ی بازگشت → `run_command` تأییدشده با تغییرمسیر → بازگردانی بایت‌به‌بایت + سبد زباله‌ی واقعاً پر.
  9. سقف توکن + ادامه → `TestTokenCapStopsTurnGracefully` + `TestTokenCapAllowsResume` (Stage A؛ نگاشت شد).
  10. `TestScenarioMidTurnCompactionKeepsTodosAndProtocol` — فشرده‌سازی خودکار در نوبت دوم؛ هر درخواست `validateHistory` سالم، نشانگر «حذف کانتکست» حداکثر یکی و حامل فهرست کارها، اعلان فشرده‌سازی یک‌بار.
  11. `TestScenarioStopAtThreeMomentsClean` — توقف حین استریم اول / حین استریم بعد از دور ابزار / حین اجرای `sleep` واقعی؛ بدون نشت گوروتین (شمارش با بستن سرورها به خط پایه برمی‌گردد)، فایل ~۵۰KB کامل نوشته می‌شود و گفتگو قابل‌بارگذاری می‌ماند.
- همه با `-race`، بدون شبکه‌ی واقعی، بدون خواب >~۲۰۰ms (بزرگ‌ترین انتظارها: رسیدن به مرحله/پاسخ مودال با مهلت تستی).

**دو کشف مهم در نوشتن سناریوها (رفع/مستند شد):**
- **گیت پایه قرمز بود:** `TestMCPChildEnvAllowList` روی لینوکس همیشه رد می‌شد — تست هر دو واریانت
  `HTTP_PROXY` و `http_proxy` را ست می‌کند و سپس با کلیدِ جمع‌شده‌ی case-insensitive مقایسه می‌کرد، پس
  مقدارِ واریانت دوم همیشه بر چکِ واریانت اول می‌چربید. روی لینوکس این دو متغیر مستقل‌اند و
  `mcpChildEnv` هر دو را با مقدار خودش می‌فرستد (رفتار درست). مقایسه به کلیدِ دقیق اصلاح شد
  (اشکال تست، نه کد) و چک لیست‌سفید همچنان case-insensitive ماند. در `git worktree` روی
  کامیت Stage C بازتولید شد — یعنی ورودی «گیت سبز» آن مرحله برای این تست دقیق نبود؛ اینجا صادقانه ثبت می‌شود.
- **فشرده‌سازی فاز B فقط واحدهای «قبل از آخرین پیام کاربر» را حذف می‌کند و حداقل
  `minKeepMessages=12` پیام نگه می‌دارد** — پس سناریوی درستِ «فشرده‌سازی میانه‌ی نوبت»
  باید محتوای سنگین را در نوبت‌های قبلی و چند دور ابزار کوچک داشته باشد (طرح سناریو ۱۰ این‌طور نوشته شد).

**انحراف مستندشده در سناریوی ۵:** «echo %COMSPEC%» در شله‌ی **POSIX** بر اساس کورپوس
بازبینی‌شده‌ی Stage 1 مجاز است (در sh رشته‌ی `%VAR%` واژه‌ای است و echo ساده بی‌خطر است) —
سناریو برای همین یک جفتِ بی‌خطر «اجازه + خروجی واژه‌ای» را می‌پذیرد و در بقیه‌ی ۱۵ جفت اجرای صفر را اثبات می‌کند.

**D.2 — تست قرارداد UI + تست مرورگری:**
- `uicontract_test.go` (تست Go، همیشه اجرا می‌شود): هر id ارجاعی JS در HTML هست یا در JS ساخته می‌شود
  (رنگ‌های hex فیلتر)؛ هر کلاس toggleشده‌ی JS قاعده‌ی CSS دارد؛ قاعده‌ی کلیدی `.modal-overlay.open`
  باید واقعاً visible+کلیک‌پذیر باشد (opacity/pointer-events یا display غیر none — نگهبان باگ مودال نامرئی ۱.۷.۱)؛
  پنج اورلی استاتیک هر دو سو «زنده»‌اند؛ هر متد Wails که JS صدا می‌زند در `App.js` و روی `App` هست، هر export بایندینگ در
  `App.d.ts` و Go هم هست؛ مودال‌های این مأموریت (پیش‌نمایش checkpoint، گزینه‌ی web_host، پشتیبانی mock) پوشش دارند.
- تست مرورگری Playwright در `frontend/tests/` (فقط توسعه؛ خارج از ساخت برنامه):
  `serve.js` بدون وابستگی، `playwright.config.js`، `smoke.spec.js` با چهار سناریو —
  مودال Diff، مودال دسترسی، کارت خلاصه‌ی نوبت (فایل‌ها/چیپ محافظت/راستی‌آزمایی/دکمه‌ها) و
  مودال پیش‌نمایش نقطه‌ی بازگشت — هر چهار **در کرومیوم واقعی این‌جا اجرا و سبز شدند** (قابل‌مشاهده + قابل‌کلیک + سیم‌کشی به بریج mock با spy).
  README اجرا را مستند می‌کند. کشف جانبی: پنل «تغییرات فایل» با اولین badge تغییر معلق به‌صورت عمدی
  خودکار باز می‌شود (اسکریپت inline در index.html) — تست‌ها آن را می‌بندند تا سایه نیندازد.
- CI: job غیربلاک‌کننده‌ی `browser-smoke` (continue-on-error) اضافه شد؛ **ضمناً خطای خرابی قدیمی
  `branches: ain]` در فیلتر push (corruption واقعی فایل) به `branches: [main]` اصلاح شد** و YAML با parse اعتبارسنجی شد.

**D.3 — مستندات و انتشار:**
- README: بخش دوزبانه‌ی «مدل امنیتی / Security model» (صادقانه: ابزارهای فایل محدود به پوشه‌ی کاری؛
  دستورها گیت‌دار ولی **sandbox نیستند**؛ تست/ساختِ خودکار کدِ تازه‌نوشته‌ی ایجنت را اجرا می‌کند و
  شبکه‌ی ایمنی بازبینی پایان نوبت + نقطه‌ی بازگشت است؛ حالت‌های ویرایش؛ مسیرهای محافظت‌شده؛
  checkpoint/سبد زباله؛ نظم راستی‌آزمایی؛ سقف توکن؛ مدل اعتماد web_fetch/MCP؛ گردش‌کار پیشنهادی)
  + بخش «تازه‌های 1.8.0». ARCHITECTURE.md: بخش «Agent accuracy and safety net» + جدول رویدادها
  (chat:turn_summary، انواع ask، متن‌های chat:notice) + trash روی دیسک + کارت خلاصه. CHANGELOG.md: 1.8.0 کامل.
- نسخه سراسری **1.8.0**: `main.go`، `wails.json`، `build.sh`، ldflags در CI.
  `grep -rn "1\.7\.1"` فقط ارجاع تاریخی در مستندات و متن تستِ نگهبان باگ می‌دهد — درست است.
- تست سازگاری tool-registry: ابزار/رویداد تازه‌ای در این مأموریت اضافه نشد (checkpoint و web_host
  ابزار جدید نیستند)؛ `TestToolRegistryConsistency` و `TestUIContract` سبز.

**D.4 — خودبازبینی نهایی:**
- گیت کامل (خروجی واقعی آخرین اجرا):

```
=== gofmt -l . ===                [OK] (خالی)
=== go vet ./... ===              [OK]
=== go build ./... ===            [OK]
=== go test -race -count=1 ./... === ok  myai  22.096s  [OK]
=== env GOOS=windows GOARCH=amd64 go vet ./... === [OK]
=== env GOOS=linux   GOARCH=amd64 go vet ./... === [OK]
GATE: GREEN
```

- بدون TODO/FIXME/کد کامنت‌شده در فایل‌های جدید؛ بدون نگه‌داشتن `a.mu` روی I/O در کد جدید
  (کد جدید تست است + اسکریپت مرورگری)؛ همگامی بایندینگ‌ها با `TestUIContract` اثبات می‌شود؛
  fixtureهای تنظیمات/گفتگوی قدیمی و `changes.json` در تست‌های موجود سبز (`v13_test.go`،
  `history_test.go`، `changes_test.go` و مهاجرت‌های settings)؛ کورپوس ۲۷۶ موردی
  (`TestCommandGateCorpus`) و ۱۱ سناریوی D.1 سبز؛ مجموعه: **۴۷۸ تابع تست در ۵۱ فایل**.
- **نمی‌شود این‌جا اثبات کرد (صادقانه):** اجرای واقعی GitHub Actions (رانر نیست — YAML parse +
  تست محلی Playwright به‌جای آن)؛ رفتار واقعی ویندوز (WebView2، Job-Object، مسیرهای فارسی
  در CMD) — با vet متقاطع و چک‌لیست دستی پوشش داده می‌شود.

**فرض‌ها:** تست مرورگری با node/npm قابل‌اجرا بود و همین‌جا سبز شد؛ دکمه‌ی «اعمال تغییر»
(#diff-accept) و بسته‌شدن پنل تغییرات (#changes-close) به‌عنوان انتخابگرهای تست استفاده شدند.

**گیت:** سبز — خروجی بالا (۴.۱؛ `-race` واقعی با gcc 14.2).

---

# Mission 4 — پایداری و حذف نقص (Stability & defect elimination, v1.8.0)

هدف این مأموریت **فقط پایداری** است: نه قابلیت تازه، نه ابزار تازه. مأموریت
مرحله‌به‌مرحله و با پروتکل توقف پیش می‌رود (هر مرحله: گیت کامل → ثبت در همین
فایل → گزارش مرحله → توقف).

## Mission 4 — Stage 0: مبنا (Baseline)

**۱) خواندن مبنا:** `ARCHITECTURE.md`، `README.md`، `docs/dev/FINAL_REPORT.md`،
انتهای همین `PROGRESS.md` و `go.mod` (Go 1.26، Wails v2.10.1) خوانده شد.

**۲) گیت مبنا RED بود؛ ریشه در تست‌های وابسته به سکو بود، نه در کد محصول.**
`runShell` در ویندوز همیشه `cmd` را اجرا می‌کند (شله‌ی تزریقی `withFlavor` فقط
تحلیل/دروازه‌ی دستور را عوض می‌کند)، پس دو انتظار نادرست بود:

- `TestScenarioCommandGateBypassAttempts/posix` انتظار داشت `echo %COMSPEC%`
  واژه‌ای چاپ شود؛ cmd آن را به `C:\Windows\system32\cmd.exe` بسط می‌دهد.
- `TestScenarioCheckpointDestructiveCommandRestore` انتظار `"side-effect\n"`
  داشت؛ cmd با CRLF و یک فاصله پیش از بازالشت می‌نویسد (`"side-effect \r\n"`).

**اصلاح عمدی و مستند (بدون حذف/سست‌کردن تست):** زیرتست POSIX روی ویندوز
`t.Skip` می‌شود (روی سکوهای POSIX همان تست اجرا می‌شود؛ معنای اجرایی POSIX با
شله‌ی cmd قابل اثبات نیست)، و بررسی اثر جانبی سناریوی ۸ با `TrimSpace` انجام
می‌شود. مقصود هر دو تست — «دستور بی‌مجوز هرگز اجرا نشود» و «اثر جانبی وجود
داشته باشد و با بازگردانی پاک شود» — دست‌نخورده است.

**۳) بررسی وجود فایل‌های مأموریت (فقط خواندن، بدون بازسازی):** `fileread.go`،
`checkpoint.go`، `turnreview.go`، `cmdgate.go`/`shellparse.go`، `toolbatch.go`،
`subagent.go`، `mcp.go`/`mcpmanager.go`، `retry.go`، `agent_e2e_test.go` و
`uicontract_test.go` — همه موجود و در گیت شرکت می‌کنند.

## Mission 4 — Stage 1: جداسازی خرابی (Crash isolation)

**ریشه‌ی نقص:** جز `errorlog.go` هیچ `recover()` ای نبود. در Go یک panic
مهارنشده در هر goroutine کل فرایند را می‌کشد — از جمله کار ذخیره‌نشده‌ی کاربر.
workerهای موازی ابزار، گوروتین استریم، حلقه‌ی خواندن MCP، دیده‌بان‌های shell و
checkpoint و هر متد متصل به Wails روی گوروتین خودشان اجرا می‌شوند.

**چه شد:**

- `safego.go` (تازه): `safeGo(a, where, fn)`، `recoverPanic(a, where)`،
  `callGuarded(a, where, fn) error`، و دو محافظ متد متصل
  `(*App).recoverBound(where)` / `(*App).recoverBoundErr(where, *error)`.
  پشته‌ی ثبت‌شده با `cutUTF8` حداکثر ۸KB است (مرز کاراکتر UTF-8 حفظ می‌شود)،
  هیچ محتوای فایل/کلید لاگ نمی‌شود، و `panicRecordHook` برای شمارش قطعی در
  تست‌ها وجود دارد.
- **همه‌ی گوروتین‌های کد غیرتست** با `safeGo` اجرا می‌شوند و هر کدام وضعیت را
  بازمی‌گردانند:
  - `chat.go`: هر پنج `go a.streamChat(...)` → `safeGo`; داخل خودِ `streamChat`
    یک `defer` مهار اضافه شد که پیام فارسی `«خطای داخلی رخ داد؛ جزئیات در لاگ
    خطاها ثبت شد.»` را با `emitChatError` می‌فرستد (gen آزاد می‌شود، UI در
    «در حال تولید» نمی‌ماند) و پاک‌سازی‌های موجود (`done`/`cancel`/`gen=nil`)
    به‌ترتیب LIFO درست اجرا می‌شوند.
  - `toolbatch.go`: هر worker نتیجه‌ی خودش را همیشه می‌نویسد — در صورت panic
    `ERROR: internal error in tool <name> (see the error log).` — پس جفت‌بندی
    `tool_calls`↔`tool` سالم می‌ماند و بقیه‌ی workerها ادامه می‌دهند.
  - `subagent.go`: `runSubagent` خروجی‌های نام‌دار گرفت و panic به
    «ERROR: internal error in sub-agent (see the error log).» تبدیل می‌شود.
  - `mcp.go`: `readLoop` (failPending + markClosed)، `waitExit` (همان پاک‌سازی
    یک‌باره با `exitOnce` و onExit)، گوروتین نوشتن stdin (همیشه نتیجه می‌فرستد
    تا Write/watchdog معلق نماند)، `drainStderr`، `listChanged` و هر دو
    `time.AfterFunc` (kill/stop) محافظت شدند.
  - `mcpmanager.go`: گوروتین `scheduleRefresh` (پرچم refreshing همیشه آزاد
    می‌شود) و گوروتین کشف ابزار هر سرور (نتیجه همیشه — حتی خالی — به کانال
    می‌رود تا جمع‌کننده معلق نماند).
  - `shell.go`: گوروتین `cmd.Wait` نتیجه را حتی در صورت panic می‌فرستد
    (فراخواننده هرگز برای همیشه منتظر نمی‌ماند) و دیده‌بان کشتن درخت با
    `safeGo`.
- **متدهای متصل به Wails:** هر ۶۰ متد صادرشده‌ی `*App` در ابتدای بدنه
  `defer a.recoverBound("<Name>")` دارد (امضاها دست‌نخورده). تست AST
  `panicguard_test.go` فهرست فایل‌های غیرتست را با `go/parser` می‌خواند و اگر
  متد صادرشده‌ای محافظ نداشته باشد تست را قرمز می‌کند (allow-list فعلاً خالی
  است؛ نگهبانِ نگهبان: حداقل ۵۰ متد محافظت‌شده).

**گیت واقعی (آخرین اجرا، `scripts/gate.sh`):**

```
=== gofmt -l . ===                [OK] (خالی)
=== go vet ./... ===              [OK]
=== go build ./... ===            [OK]
=== go test -race -count=1 ./... === ok  myai  131.226s  [OK]  (اجرای کامل پیش از نهایی‌شدن تست‌ها: 134.023s)
=== env GOOS=windows GOARCH=amd64 go vet ./... === [OK]
=== env GOOS=linux   GOARCH=amd64 go vet ./... === [OK]
GATE: GREEN
```

**تست‌های تازه:** `safego_test.go` — `TestSafeGoRecoversPanicAndKeepsWorking`،
`TestCallGuardedConvertsPanicToError`، `TestRecoverBoundKeepsAppAlive`،
`TestToolWorkerPanicKeepsPairingAndOthersRun` (پیام انگلیسیِ نتیجه‌ی ابزار،
ادامه‌ی بقیه‌ی workerها، بدون نشت goroutine)، `TestSubagentPanicReturnsErrorReport`.
`panicguard_test.go` — `TestExportedAppMethodsHavePanicGuard`.

**فرض‌ها/انحراف‌ها:**

- محافظ متدهای متصل فقط **ثبت + زنده‌ماندن** می‌کند و مقدار صفر برمی‌گرداند؛
  خروجی‌های نام‌دار برای برگرداندن خطا اضافه نشد تا ۶۰ امضای متد (API فرانت‌اند)
  دست‌نخورده بماند. `recoverBoundErr` آماده است و در مرحله‌های بعد برای متدهایی
  که این کار بی‌خطر است به‌کار می‌رود.
- تست‌های تزریق panic قطعی‌اند (قلاب `panicRecordHook` و `toolExecFn` موجود)؛
  هیچ شبکه/خواب طولانی ندارد.

**محدودیت شناخته‌شده (صادقانه):** بخش‌هایی که `a.mu`/`genMu` را با `Unlock`
صریح (نه `defer`) آزاد می‌کنند — به‌ویژه پنج نقطه‌ی ورودی چت — در صورت panic
وسط بخش قفل‌شده می‌توانند قفل را نگه دارند؛ برنامه کشته نمی‌شود ولی می‌تواند
قفل شود. این مورد در بازبینی قفل‌های Stage 5 (lock/hang audit) با تبدیل به
`defer`/پرچم بررسی و رفع می‌شود. رفتار واقعی ویندوز (WebView2/Job Object) هم
مثل قبل فقط با vet متقاطع و چک‌لیست دستی پوشش دارد.

**Stage 1 complete. Waiting for your instruction to continue.**

## Mission 4 — Stage 2: نگهبان سکون استریم (Stream stall watchdog)

**ریشه‌ی نقص:** `chatHTTPClient` فقط `ResponseHeaderTimeout` داشت (۹۰ ثانیه).
اگر سرویس‌دهنده/پروکسی **وسط استریم** داده را قطع می‌کرد و اتصال را نمی‌بست،
خواندن بدنه تا ابد بلاک می‌ماند و تنها دکمه‌ی توقف کاربر آن را پایان می‌داد —
«فکر کردن بی‌پایان». همین برای استریم زیرایجنت هم صادق بود.

**چه شد:**

- `streamwatch.go` (تازه): `streamIdleGuard` بدنه‌ی پاسخ را می‌پیچد؛ هر بایت
  خوانده‌شده (حتی خط کامنت keep-alive پروتکل) `lastRead` را به‌روز می‌کند.
  یک goroutine نگهبان با ticker بررسی می‌کند؛ فقط اگر در مهلت هیچ بایتی نیاید
  `ctx` مشتق‌شده لغو و پرچم `stalled` ست می‌شود. `Stop()` تایمر/goroutine را
  قطعی می‌بندد و منتظر خروج می‌ماند (بدون نشت). پیش‌فرض **۱۲۰ ثانیه**
  (`streamIdleDefaultSec`)، clamp **۳۰..۹۰۰** (`streamIdleMinSec/MaxSec`)،
  همه تزریقی در تست‌ها.
- تنظیم پنهان `Settings.StreamIdleTimeoutSec` (`json:"streamIdleTimeoutSec,omitempty"`،
  بدون UI) در شروع هر نوبت/درخواست خوانده می‌شود؛ `0` = پیش‌فرض. فایل‌های
  تنظیمات قدیمی بدون این فیلد بی‌خطا بار می‌شوند.
- `chat.go`: هر دور استریم `idleGuard` مخصوص خودش می‌گیرد (`reqCtx` مشتق‌شده)؛
  بدنه با `guard.Body` خوانده می‌شود و در همه‌ی مسیرها (خطای dial، HTTP غیر۲۰۰،
  پایان استریم) `Stop` صدا زده می‌شود. رفتار:
  - **سکون پیش از هر خروجی/دلتا/usage** → خطای موقتِ قابل‌تلاش‌مجدد با همان
    سیاست `retryMaxAttempts`؛ اعلان فارسی «پاسخ از سرویس‌دهنده قطع شد؛ تلاش
    مجدد ۲ از ۴ …» و تلاش مجدد بدون سوزاندن دور.
  - **سکون پس از خروجی** → بدون تلاش مجدد (جلوگیری از تکرار اثر جانبی)؛
    `savePartial` پاسخ نیمه‌کاره را با `Aborted: true` ذخیره می‌کند، خلاصه‌ی
    نوبت منتشر و خطای فارسی «دریافت پاسخ متوقف شد (بدون داده برای n ثانیه).
    می‌توانید «ادامه» را بزنید.» فرستاده می‌شود؛ «ادامه» در دسترس می‌ماند.
  - **توقف کاربر** فوری است و هرگز سکون گزارش نمی‌شود (پرچم نگهبان منبع
    حقیقت است، نه `ctx.Err`).
- `subagent.go`: همان نگهبان در `subagentRequest` (خواندن مهلت در شروع درخواست)؛
  سکون به `subagentStallError` تبدیل و در حلقه‌ی زیرایجنت به گزارش خطای
  انگلیسیِ قابل‌استفاده به والد («stream stalled … retry or split the task»)
  با `ok=false` می‌شود — نه hang، نه کشتن برنامه.

**گیت واقعی (آخرین اجرا، `scripts/gate.sh`):**

```
=== gofmt -l . ===                [OK] (خالی)
=== go vet ./... ===              [OK]
=== go build ./... ===            [OK]
=== go test -race -count=1 ./... === ok  myai  157.107s  [OK]
=== env GOOS=windows GOARCH=amd64 go vet ./... === [OK]
=== env GOOS=linux   GOARCH=amd64 go vet ./... === [OK]
GATE: GREEN
```

**تست‌های تازه (`streamwatch_test.go`):**
`TestStreamStallBeforeFirstByteRetries` (سکون قبل از بایت اول → دقیقاً یک
تلاش مجدد و پاسخ کامل)، `TestStreamStallMidStreamSavesPartialNoRetry`
(بدون تلاش مجدد، پیام `Aborted` با متن نیمه‌کاره، پیام فارسی در لاگ خطاها با
ارقام فارسی)، `TestStreamKeepAlivePreventsWatchdog` (کامنت keep-alive مانع
لغو)، `TestStreamStallDoesNotOverrideUserStop` (توقف کاربر ≠ سکون)،
`TestSubagentStallReturnsErrorReport`، `TestStreamIdleClampAndDefault`
(pوشش ۰/منفی/۵/۳۰/۱۲۰/۹۰۱/۱۰۰۰۰۰)، `TestStreamWatchdogNoGoroutineLeak`.

**فرض‌ها/انحراف‌ها:** مهلت برای کل نوبت ثابت خوانده می‌شود (هم‌راستا با بقیه‌ی
تنظیماتِ نوبت). تست‌ها مهلت ۱ ثانیه و backoff صفر تزریق می‌کنند؛ هیچ شبکه‌ی
واقعی/خواب طولانی ندارند (کل مجموعه ~۱۰ ثانیه).

**محدودیت:** سکون «فاز هدر» (پیش از دریافت ۲۰۰) همچنان با مهلت موجود
`ResponseHeaderTimeout` (۹۰ ثانیه) پوشش دارد و زیر چتر نگهبان نیست؛ رفتار
تلاش مجدد همان `dialStream` است.

**Stage 2 complete. Waiting for your instruction to continue.**

## Mission 4 — Stage 3: نوشتن اتمیک مقاوم و بازیابی داده

**ریشه‌ی نقص‌ها:**
1. `atomicWriteFile` نام موقت ثابت (`path+".tmp"`) داشت؛ دو نویسنده‌ی هم‌زمان
   روی همان فایل موقت می‌نوشتند و می‌توانستند خروجی یکدیگر را خراب کنند.
2. روی ویندوز `os.Rename` روی فایل مقصدی که ویرایشگر/آنتی‌ویروس/ایندکسر
   لحظه‌ای باز نگه داشته، گذرا با ACCESS_DENIED / SHARING_VIOLATION /
   LOCK_VIOLATION شکست می‌خورد و ذخیره‌ی گفتگو بی‌صدا شکست می‌خورد.
3. گفتگو یا `changes.json` خراب/بریده (کرش یا قطع برق وسط نوشتن) کامل از دست
   می‌رفت: لودر خطای JSON می‌داد و changes فقط به `.corrupt` تغییر نام
   می‌داد؛ هیچ نسخه‌ی پشتیبان و هیچ بازیابی‌ای نبود.
4. خطای دیسک پر پیام مبهم داشت و مشخص نبود فایل قبلی سالم مانده یا نه.
5. نام‌های ممنوع ویندوز (CON/NUL/COM1…، نقطه/فاصله‌ی پایانی) خطاهای مبهم
   یا در بدترین حالت نوشتن روی دستگاه شبه‌فایل می‌دادند.

**چه شد:**

- `crypto.go`: `atomicWriteFile` بازنویسی شد — فایل موقت یکتا با
  `os.CreateTemp(dir, base+".*.tmp")` → `Chmod` → `Write` → `Sync` → `Close`
  → `renameWithRetry`. هر خطا فقط فایل موقت را حذف می‌کند؛ مقصد هرگز
  truncate نمی‌شود. قلاب تست `atomicWriteHook` برای تزریق ENOSPC.
- `atomicwrite_windows.go` / `atomicwrite_other.go` (تازه): `renameWithRetry`
  با تشخیص خطاهای گذرا (errnoهای ۵/۳۲/۳۳ و `windows.ERROR_*`) و ۶ تلاش
  ۲۵→۴۰۰ میلی‌ثانیه (~۰.۸ ثانیه)؛ روی POSIX همان `os.Rename`.
- `recovery.go` (تازه): `atomicWriteFileWithBackup` با چرخش یک‌نسلی `.bak` به
  ترتیبی که هرگز حالت «نه main نه .bak» پیش نیاید (main → .bak، بعد
  tmp → main)؛ `moveToCorrupt` + `pruneCorruptFiles` (حداکثر ۵ نسخه‌ی
  `name.corrupt-<ts>`، داده‌ی کاربر هرگز حذف نمی‌شود)؛ `salvageConversation`
  با `json.Decoder` تا آخرین پیام کامل؛ اعلان فارسی «گفتگو آسیب دیده بود؛
  n پیام بازیابی شد.» با ارقام فارسی.
- `conversations.go`: `loadConversation` زنجیره‌ی main → `.bak` → salvage و
  بازسازی main از پشتیبان؛ `saveConversation` با پشتیبان و خطای فارسی
  «ذخیره‌ی گفتگو روی دیسک ناموفق بود (فایل قبلی دست‌نخورده است)»؛ گفتگو در
  حافظه حفظ می‌شود. `sweepStaleTempFiles` (فراخوانی از `app.go`) هر دو سبک
  نام موقت را در راه‌اندازی جارو می‌کند.
- `changes.go`: `loadChangesLocked` زنجیره‌ی main → `.bak` → بازنشانی با
  اعلان فارسی؛ `appendChangeLocked` با پشتیبان و اعلان خطای ذخیره؛
  اعتبارسنجی مسیر در `writeFileWithConfirm` و `movePathWithConfirm`.
- `checkpoint.go`: `checkpointAbs` نام/طول مسیر ویندوز را پیش از نوشتن رد
  می‌کند (خطای روشن به‌جای شکست مبهم).
- `pathnames.go` (تازه): `validatePathName`/`validatePathForWrite` — نام‌های
  رزروشده، نویسه‌های `<>:"|?*`، کاراکترهای کنترلی، نقطه/فاصله‌ی پایانی و
  سقف ۲۵۹ نویسه. روی POSIX فقط مسیر خالی رد می‌شود تا مسیرهای قانونی
  یونیکس رد نشوند.

**تست‌های تازه (`stage3_test.go`):**
`TestAtomicWriteConcurrentUniqueTemp` (۲۴ نویسنده‌ی هم‌زمان، JSON سالم، بدون
`.tmp` جامانده)، `TestSweepRecognizesUniqueTempNames`،
`TestConversationBackupRotationAndRecovery` (نسل قبلی در `.bak` و بازیابی از
آن)، `TestConversationSalvageTruncated` (۲ پیام کامل + حفظ دقیق ZWNJ +
`.corrupt-*` + بازنویسی main + اعلان فارسی)، `TestCorruptFilesAreBounded`،
`TestDiskFullKeepsExistingFileAndMemory` (ENOSPC: فایل بایت‌به‌بایت دست‌نخورده،
حافظه حفظ، خطای فارسی)، `TestChangesJSONRecoversFromBackup`،
`TestValidatePathName`، `TestValidatePathForWriteLongPath`.

**گیت واقعی (اجرای `scripts/gate.sh`):**
```
=== gofmt -l . ===
[OK] gofmt -l .
=== go vet ./... ===
[OK] go vet ./...
=== go build ./... ===
[OK] go build ./...
=== go test -race -count=1 ./... ===
ok  	myai	155.322s
[OK] go test -race -count=1 ./...
=== env GOOS=windows GOARCH=amd64 go vet ./... ===
[OK] env GOOS=windows GOARCH=amd64 go vet ./...
=== env GOOS=linux GOARCH=amd64 go vet ./... ===
[OK] env GOOS=linux GOARCH=amd64 go vet ./...
GATE: GREEN
```

**فرض‌ها/انحراف‌ها:** `.bak` فقط یک نسل نگه می‌دارد (سقف رشد دیسک)؛
`atomicWriteHook` فقط برای تست است و در runtime nil می‌ماند؛ اعتبارسنجی نام
ویندوز تنها روی ویندوز اعمال می‌شود.

**محدودیت:** اگر هم main و هم `.bak` خراب باشند، بازیابی فقط پیام‌های کامل
تا محل خرابی را نگه می‌دارد و فایل اصلی در `.corrupt-*` دست‌نخورده می‌ماند.
رکورد changes در حافظه پس از خطای دیسک پر تا پایان اجرا باقی می‌ماند ولی روی
دیسک نیست (اعلان فارسی داده می‌شود).

**Stage 3 complete. Waiting for your instruction to continue.**

## Mission 4 — Stage 4: رفع یافته‌های تأییدشده‌ی بازبینی

### 4.1 خواندن جزئی نباید «خواندن کامل» حساب شود

**ریشه:** هر `read_file` موفق، هش **کل** فایل را ثبت می‌کرد — حتی وقتی مدل
فقط `start_line..end_line` را خوانده یا خروجی بریده شده بود. پس از دیدن ۲۰ خط
از فایل ۵۰۰۰ خطی، `write_file` کل فایل اجازه داشت و بخش دیده‌نشده بی‌صدا
نابود می‌شد.

**رفع:** `fileReadRecord.Full` اضافه شد؛ `toolReadFile` آن را فقط وقتی ست
می‌کند که خواندن از خط ۱ تا انتهای فایل و بدون هیچ برشی (بایت/کاراکتر) باشد.
`checkWriteGuard` در `intentOverwrite` رکورد غیرـFull را با پیام انگلیسی
«only partially read … read the entire file first, or use edit_file» رد
می‌کند؛ `edit_file` با رکورد جزئی مجاز می‌ماند (تطبیق دقیق `old_string`
محافظ آن است) و بررسی هش کهنه سر جایش است. رکوردهای نوشتن/جابه‌جایی
(`writeFileWithConfirm`، `movePathWithConfirm`) `Full: true` هستند.

**تست‌ها (`readguard_test.go`):** `TestPartialReadRejectsWholeFileOverwrite`،
`TestTruncatedReadRejectsOverwrite`، `TestFullReadAllowsOverwrite`،
`TestFullReadAfterPartialAllowsOverwrite`، `TestEditAfterPartialIsAllowed`،
`TestEmptyFileReadIsFull`، `TestStaleAfterPartialReadStillRejected`،
`TestPersianCRLFBOMFullReadThenOverwrite` (BOM + CRLF + ZWNJ بایت‌به‌بایت)،
`TestLargeFilePartialReadGuard`.

### 4.2 چک‌پوینت بایت‌به‌بایت (بدون فیلتر)

**ریشه:** snapshot با `git add -A` ساخته می‌شد و بازگردانی با
`checkout-index`؛ در نتیجه `core.autocrlf` کاربر، `.gitattributes`
(`text`/`eol`) و فیلترهای clean/smudge (Git LFS) روی محتوا اعمال می‌شدند —
خط‌پایان‌ها عوض می‌شدند، فایل‌های LFS خراب برمی‌گشتند و پیش‌نمایش تقریباً همه
را «rewrite» نشان می‌داد.

**رفع:**
- `checkpointBuildTree`: فهرست از `git ls-files -z -c -o
  --exclude-standard`، blobها با `git hash-object -w --no-filters
  --stdin-paths` (بدون هیچ فیلتر/EOL)، و ایندکس موقت با
  `git update-index -z --add --index-info`؛ سپس `write-tree` (بدون `git add`).
  حالت اجراپذیر حفظ می‌شود (`100755`)، پیوندها کنار گذاشته می‌شوند.
- همه‌ی فراخوانی‌های git با `-c core.autocrlf=false -c core.safecrlf=false
  -c core.fsmonitor=false -c core.hooksPath=</dev/null>` و `GIT_ATTR_NOSYSTEM=1`.
- بازگردانی با `git cat-file --batch` (خام) انجام می‌شود، نه `checkout-index`.

**تست‌ها (`checkpointfidelity_test.go`):** `TestCheckpointNoFilterByteFaithful`
(مخزن با `core.autocrlf=true`، `.gitattributes` با `eol=crlf` و
`filter=broken` ناموجود، CRLF/LF/باینری/فارسی با ZWNJ → snapshot، تغییر، حذف،
فایل تازه، بازگردانی: همه بایت‌به‌بایت، فایل تازه در سبد، نسخه‌ی قبلی در
سبد)؛ `TestCheckpointPreviewListsOnlyChanged` (پیش‌نمایش فقط فایل واقعاً
تغییریافته).

### 4.3 بازگردانی ایمن و کران‌دار

**ریشه:** بررسی مسیر فقط لفظی (`withinDir`) بود؛ اگر پوشه‌ای بعد از snapshot
به symlink/junction بیرونی تبدیل می‌شد، بازگردانی بیرون پروژه می‌نوشت. علاوه
بر آن `materializeCheckpoint` کل snapshot را در پوشه‌ی موقت کپی می‌کرد و
مقایسه `os.ReadFile ×2` + `string(a)==string(b)` بود — برای فایل‌های بزرگ
حافظه/دیسک را می‌بلعید.

**رفع:**
- `resolveRestorePath` (و `checkpointAbs` روی آن): حل ریشه‌ی واقعی و
  نزدیک‌ترین والد موجود با `EvalSymlinks`، رد هر جزء reparse/symlink
  (`isReparsePoint` ویندوزی بر پایه‌ی `FILE_ATTRIBUTE_REPARSE_POINT`)، رد
  هدف symlink، و بررسی نام/طول مسیر ویندوز. مسیرهای ردشده در
  `Skipped`/`Errors` گزارش می‌شوند و بقیه ادامه می‌یابند.
- حذف کامل materialize: مقایسه و بازگردانی **جریانی** با
  `git cat-file --batch`: اول اندازه، بعد تکه‌های ۶۴KiB با `bytes.Equal`؛
  نوشتن با `atomicWriteStream` (فایل موقت یکتا + sync + rename) و بدون
  بافر کل محتوا.
- برنامه (plan) پیش از هر تغییری ساخته می‌شود؛ سقف فایل
  (`checkpointMaxFiles`) و بررسی مهلت/لغو هم پیش از نخستین نوشتن انجام
  می‌شود تا بازگردانی نیمه‌کاره رخ ندهد.

**تست‌ها:** `TestResolveRestorePathRefusesSymlinkedParent`،
`TestResolveRestorePathRefusesOutsideResolution` (FS جعلی، روی همه‌ی
سکوها)، `TestResolveRestorePathAcceptsPlainPath`،
`TestRestoreLargeFileMemoryBounded` (فایل ۳۲MB؛ رشد heap کمتر از نصف حجم
فایل در preview و بازگردانی جریانی)، `TestCheckpointLimitExceededChangesNothing`
(سقف فایل → خطا و هیچ تغییری).

### 4.4 بهداشت

- `README.txt` (یادداشت بسته‌ی 2026-10-30، بدون هیچ ارجاعی) حذف شد.
- فایل‌های تست با نام نسخه/فاز به نام موضوعی منتقل و بدنه‌ها بدون تغییر
  جابه‌جا شدند: `v13_test.go` → `providermigration_test.go` +
  `conversationdelete_test.go`؛ `finalaudit_test.go` → `mcpsafeguards_test.go`
  + `frontendhooks_test.go` + `settingssecurity_test.go`؛ `finalfix_test.go`
  → `settingsrevert_test.go` + `toolcustomize_test.go` + `appresume_test.go`.
- `grep -rn "TODO\|FIXME\|XXX" --include=*.go` خالی است (بدون بدهی تازه).
- پس از جابه‌جایی فایل‌ها، `TestErrorLog` در گیت کامل قرمز شد: مسیر
  `errors.log` در `errLogPath` کش می‌شد و تست‌ها به پوشه‌ی موقتِ حذف‌شده‌ی
  تست قبلی می‌نوشتند. `resolveErrorLogPath` حالا کش را وقتی پوشه‌اش وجود
  ندارد باطل می‌کند (در runtime هم اگر کاربر پوشه را حذف کند مفید است) و
  `newTestApp`/`newPanicTestApp`/`streamTestApp` با `resetErrorLogPath` هر
  تست را ایزوله می‌کنند. Assertionها دست‌نخورده‌اند.

**گیت واقعی (اجرای `scripts/gate.sh`):**

```
=== gofmt -l . ===                [OK] (خالی)
=== go vet ./... ===              [OK]
=== go build ./... ===            [OK]
=== go test -race -count=1 ./... === ok  myai  187.407s  [OK]
=== env GOOS=windows GOARCH=amd64 go vet ./... === [OK]
=== env GOOS=linux   GOARCH=amd64 go vet ./... === [OK]
GATE: GREEN
```

**فرض‌ها/انحراف‌ها:**
- همه‌ی پیوندها/junctionها در مسیر بازگردانی رد می‌شوند (نه فقط آن‌هایی که به
  بیرون اشاره می‌کنند)؛ سخت‌گیرانه‌تر و ساده‌تر از متن سند، و ایمن‌تر.
- تست ۲۰۰MB سند به ۳۲MB کاهش یافت تا گیت سریع بماند؛ خاصیت اثبات‌شده
  (نبودِ تخصیص متناسب با حجم) یکی است.
- تست ساخت junction واقعی روی ویندوز (نیازمند دسترسی/ابزار بیرونی) اجرا
  نشد؛ منطق با FS جعلی و `isReparsePoint` واقعی پوشش داده شد.
- `checkpointMaxFiles` از const به var تبدیل شد تا تست کران ممکن باشد.

**محدودیت:** اگر مهلت ۶۰ ثانیه **وسط** حلقه‌ی نوشتن تمام شود، فایل‌های
نوشته‌شده تا آن لحظه بازگردانی‌شده‌اند (هرچند همه در سبد زباله قابل‌برگشت
هستند)؛ بررسی مهلت پیش از شروع تضمین می‌کند این حالت ناشی از «بودجه» نباشد.
رفتار واقعی ویندوز (junction/آنتی‌ویروس) همچنان فقط با vet متقاطع و
چک‌لیست دستی پوشش دارد.

**Stage 4 complete. Waiting for your instruction to continue.**

## Mission 4 — Stage 5: اثبات فشار، خیساندن و فاز (Stress, soak and fuzz proof)

**وضعیت شروع:** Stage 5 نیمه‌کاره رها شده بود (تغییرات uncommitted در
`agent_e2e_test.go`/`toolbatch.go` و دو فایل جدید بدون ثبت در همین لاگ).
هر دو تست نیمه‌کاره RED بود: `TestSoakAgentLoop` (مرحله‌ی «آماده.» مرحله‌ی
`write_file` را می‌بلعید و هرگز فایلی نوشته نمی‌شد) و زیرتست disk-full
(انتظار نادرستِ «SendMessage بدون خطا در ENOSPC» در حالی که ذخیره‌ی پیام
کاربر همگام و پیش از استریم است و خطای شفاف فارسی برمی‌گرداند — رفتار
درستِ Stage 3). هر دو بر اساس معماری واقعی اصلاح شدند؛ هیچ assertionی
سست نشد.

**۱) فاز/robustness (`fuzz_test.go`، ۹ هدف `testing.F`):**
`FuzzParseShellCommand` (قطعیت + سازگاری ساختاری simples/seps)،
`FuzzReadSSEChunks` (UTF-8 معتبر دلتاها/ابزار/خطا؛ حفظ بایت‌به‌بایت
فارسی/ZWNJ)، `FuzzApplyEditOpAndReindent` (بدون panic؛ «ویرایش موفق بدون
تغییر» رخ نمی‌دهد؛ حفظ UTF-8)، `FuzzHTMLToReadableText` (بدون NUL، خروجی
کران‌دار ≤ ۸×ورودی+۸KiB)، `FuzzGitignoreMatcher` (قطعیت تطبیق قاعده)،
`FuzzSafeCheckpointRel` (مسیر «امن» هرگز از پوشه‌ی کاری بیرون نمی‌زند)،
`FuzzValidateHistory` (نقش‌های معتبر، بدون رشد تاریخچه؛ فشرده‌سازی هم)،
`FuzzParseToolArgs` (نتیجه‌ی موفق JSON معتبر و marshalable)، و
`FuzzCommandCompositionNeverLoosens` (خاصیت «ترکیب هرگز شُل نمی‌شود» دروازه:
اگر ترکیب allow شد، هر دو جزء باید allow باشند — دو flavor). بذرها هم
در فایل (f.Add) و هم به‌صورت **کورپوس کامیت‌شده در `testdata/fuzz/`** (۱۸
فایل برای هر ۹ هدف؛ شامل فارسی/ZWNJ، CRLF، quote ناقص، dotdot و JSON
خراب) — در `go test` عادی گیت اجرا می‌شوند و هر کدام < ۲۰۰ms.

**۲) خیساندن (`soak_test.go`/`TestSoakAgentLoop`):** ۵ دور کامل حلقه‌ی واقعی
ایجنت روی هارنس سرتاسری (نوشتن ابزار → برگرداندن تغییر → چک‌پوینت → جهش
فایل → بازگردانی → فشرده‌سازی کانتکست → Stop وسط استریم) با `-race`. پس از
دو دور گرم‌شدن، **گوروتین، heap، توصیف‌کننده‌های باز (/proc/self/fd — فقط
لینوکس) و ورودی‌های پوشه‌ی config** نباید رشد کنند (کران‌های کوچک: +۸
گوروتین، +۳۲MB، +۱۶ fd، +۱۲۰ ورودی). حالت بلند توسعه‌دهنده با
`MYAI_SOAK_ITERATIONS`؛ در گیت عادی ~۱ ثانیه (≤ ۲۰s).

**۳) تزریق خرابی (`TestFaultInjectionEndStates`، ۹ سناریو روی هارنس واقعی؛
هر سناریو با `runWithDeadline` دیده‌بانی می‌شود و باید با وضعیت سازگار تمام
شود — نه «در حال تولید»، جفت‌بندی tool سالم، JSON معتبر):**
- `rate_limit_burst_then_success`: انفجار 429+503 → تلاش مجدد → پاسخ واقعی.
- `malformed_sse_ends_cleanly`: SSE خراب → پایان تمیز.
- `tool_panic_keeps_pairing`: panic ابزار → نتیجه‌ی «internal error» همان
  ابزار، ابزار دیگر ادامه؛ جفت‌بندی سالم (مسیر ترتیبی `callToolGuarded`).
- `disk_full_mid_turn_surfaces_notice_and_keeps_file`: دیسک پر از دومین
  ذخیره‌ی گفتگو → اعلان فارسی «هشدار: ذخیره‌ی این گفتگو روی دیسک ناموفق
  بود…»، فایل قبلی سالم (JSON معتبر، پیام کاربر سر جاش، بدون برش).
- `stall_mid_stream_saves_partial_no_retry`: تزریق سکون واقعی وسط استریم از
  طریق هارنس (فیلد تازه‌ی `Stall` در `scenarioStep`: پس از متن، اتصال باز
  می‌ماند و هیچ بایتی نمی‌آید؛ `withFastStreamIdle(1)`) → پاسخ نیمه‌کاره با
  `Aborted` ذخیره، بدون تلاش مجدد (۱ درخواست)، خطای فارسی
  «دریافت پاسخ متوقف شد…» در لاگ خطاها.
- `mcp_server_crash_fails_tool_and_turn_ends`: سرور MCP که بلافاصله crash
  می‌کند → نتیجه‌ی ERROR برای ابزار، نوبت با پاسخ نهایی ادامه و تمام می‌شود.
- `mcp_hanging_tool_times_out_and_turn_ends`: ابزار MCP همیشه‌هنگ با مهلت
  تزریقی ۱.۲s → خطای «timed out after»، پایان تمیز نوبت.
- `git_missing_clear_error_then_recovers`: PATH خالی → چک‌پوینت با خطای
  روشنِ نام‌برنده‌ی git، بدون panic؛ با برگشت PATH دوباره کار می‌کند.
- `git_hang_times_out_and_leaves_no_orphan`: git جعلی (اسکریپت با `exec
  sleep`؛ PID در فایل) + مهلت تزریقی ۴۰۰ms → خطای «git … timed out» در
  <۳s، و بررسی یتیم: PID ثبت‌شده می‌میرد (کشتن توسط CommandContext؛
  بدون فرایند بازمانده).

**۴) ممیزی قفل/هنگ (Stage 5.4):**
- دیده‌بان بن‌بست `runWithDeadline` (دامپ کامل گوروتین در عبور از مهلت) —
  روی حلقه‌ی soak و همه‌ی سناریوهای استریم‌دار تزریق خرابی اعمال شد.
- `lockaudit_test.go` — ممیزی AST (go/parser) روی تمام فایل‌های غیرتستی:
  قاعده‌ی ARCHITECTURE.md «a.mu (و genMu و checkpointMu) هرگز روی
  runtimeEmit/eventEmit/EventsEmit نگه داشته نمی‌شود» با دنبال‌کردن
  Lock/Unlock/defer Unlock/TryLock-در-شرط روی مسیر مستقیم هر تابع؛
  allowlist عمداً خالی؛ نگهبان حضور فایل‌های داغ هم دارد.
- **یافته‌ی واقعی و رفع:** `finishChat` (chat.go) هنگام شکست ذخیره‌ی پایان
  نوبت، `chat:notice` را **زیر قفل a.mu** می‌فرستاد (مسیر افزوده‌ی Stage 3)
  — نقض قرارداد ترتیب قفل. اصلاح: پرچم `saveFailed` زیر قفل، emit بیرون از
  قفل و پیش از `chat:done` (ترتیب رویدادها تغییر نکرده است).

**گیت واقعی (اجرای `scripts/gate.sh`):**

```
=== gofmt -l . ===                [OK] (خالی)
=== go vet ./... ===              [OK]
=== go build ./... ===            [OK]
=== go test -race -count=1 ./... === ok  myai  31.717s  [OK]
=== env GOOS=windows GOARCH=amd64 go vet ./... === [OK]
=== env GOOS=linux   GOARCH=amd64 go vet ./... === [OK]
GATE: GREEN
```

۵۲۹ تست/۹ هدف فاز/۱۸ فایل کورپوس؛ بدون وابستگی تازه (فقط کتابخانه‌ی استاندارد)؛
`grep -rn "TODO\|FIXME\|XXX" --include=*.go` خالی.

**فایل‌های تغییر/افزوده:**
- `fuzz_test.go` (جدید) — ۹ هدف فاز با بذرهای درون‌فایلی.
- `testdata/fuzz/**` (جدید، ۱۸ فایل) — کورپوس کامیت‌شده‌ی هر ۹ هدف.
- `soak_test.go` (جدید) — soak، `runWithDeadline`، `assertTurnEndsClean`،
  ۹ سناریوی تزریق خرابی، `initRepo`.
- `lockaudit_test.go` (جدید) — ممیزی AST ترتیب قفل.
- `agent_e2e_test.go` — فیلد `Stall` در `scenarioStep` + رفتار سرور
  (سکون بدون بستن اتصال) + `addSteps` (از کار نیمه‌کاره قبلی، تکمیل شد).
- `toolbatch.go` — `callToolGuarded` (مهار panic در مسیر ترتیبی ابزار؛ از
  کار نیمه‌کاره قبلی، تکمیل شد).
- `checkpoint.go` — `checkpointTimeout` از const به var (تزریق مهلت کوتاه
  در تست هنگِ git؛ مقدار runtime همان ۱۰s).
- `chat.go` — emit اعلان ذخیره‌ی ناموفق از زیر قفل a.mu به بیرون منتقل شد
  (یافته‌ی ممیزی قفل).

**فرض‌ها/انحراف‌ها:**
- انتظارهای دو تست نیمه‌کاره‌ی ورودی با معماری واقعی اصلاح شد (نه سست شد):
  soak حالا مرحله‌ی تذکر راستی‌آزمایی را هم در فیلم‌نامه حساب می‌کند؛
  disk-full از دومین ذخیره تزریق می‌شود (ذخیره‌ی پیام کاربر همگام است و
  خطای همگامِ فارسیِ آن رفتار درستِ Stage 3 است) و «حفظ پاسخ در حافظه» به
  assertion درستِ «اعلان فارسی + فایل سالم + پایان تمیز نوبت» تغییر کرد
  (گفتگو کش درون‌حافظه ندارد؛ هر خواندن از دیسک است).
- سناریوی MCP در سطح حلقه‌ی واقعی ایجنت پوشش داده شد؛ backoff/ری‌استارت و
  die-بعد-از-نصب پیش‌تر در Stage 1 (`mcp_test.go`) تست شده و تکرار نشد.
- سنجش fd فقط روی لینوکس فعال است (سکوهای دیگر ۰ برمی‌گردانند و سنجش رد
  می‌شود)؛ روی ویندوز معادل Task Manager در چک‌لیست دستی است.
- تست یتیم-git بر پایه‌ی `/proc` است و روی ویندوز skip می‌شود؛ رفتار
  ویندوز با vet متقاطع و چک‌لیست دستی پوشش دارد.

**محدودیت‌ها:** ممیزی قفل درون‌یک‌تابعی است (قفلِ گرفته‌شده در تابع دیگر و
آزادشده در این تابع دیده نمی‌شود؛ closureها با وضعیت تازه بررسی می‌شوند) —
مکمل آن `-race` و بازبینی است. فاز در حالت `-fuzz` کامل اجرا نشد (فقط
بذرها/کورپوس در گیت؛ حالت کشف پیوسته برای توسعه‌دهنده با `go test -fuzz`
باقی می‌ماند). رفتار واقعی ویندوز (junction، آنتی‌ویروس، Task Manager)
در sandbox قابل اجرا نیست و در چک‌لیست نهایی Mission 4 آمده است.

**چک‌لیست دستی این مرحله برای انسان (UI فارسی):**
- (الف) اجرای طولانی: چند ده نوبت پیاپی (شامل نوشتن/بازگردانی/چک‌پوینت) و
  مشاهده‌ی Task Manager — رشد پیوسته‌ی حافظه/هندل نباید دیده شود.
- (ب) دیسک پر (یا پوشه‌ی config فقط‌خواندنی) وسط نوبت → اعلان فارسی
  «هشدار: ذخیره‌ی این گفتگو روی دیسک ناموفق بود…»، برنامه زنده، فایل
  گفتگوی قبلی سالم.
- (ج) قطع شبکه/پروکسی ساکت وسط پاسخ → پایان با پیام «دریافت پاسخ متوقف
  شد…» و امکان «ادامه» (پوشش Stage 2؛ در این مرحله با هارنس اثبات شد).
- (د) سرور MCP خراب/هنگ در تنظیمات → نتیجه‌ی خطا برای همان ابزار، نوبت
  عادی تمام می‌شود، برنامه زنده.

**Stage 5 complete. Waiting for your instruction to continue.**

## Mission 4 — Stage 5 (تکمیل/مهر نهایی): اثبات فرزندِ فرزند و نگهبان همه‌ی سناریوها — DONE (2026-11-06)

بازبینی نهایی Stage 5 سه شکاف واقعی نسبت به متن سند پیدا کرد و رفع شد
(هیچ assertionی سست نشد؛ یک رفتار غلطِ محصول اصلاح شد):

۱) **یافته‌ی محصولی — یتیمِ git هنگ‌کرده:** `runGitCheckpoint` /
`runGitCheckpointInput` / `newBlobBatch` فقط با `exec.CommandContext`
کار می‌کردند که فقط **فرزند مستقیم** را می‌کشد؛ نوه‌های git (هوک/پوسته)
یتیم می‌ماندند و پایپ stdout را باز نگه می‌داشتند (Wait تا پایان
WaitDelay معطل می‌ماند). رفع: همان الگوی `runShell` — `prepareProcessTree`
+ `cmd.Cancel = terminate` + نگهبان SIGKILL بعد از `shellGracePeriod`
(کمک‌تابع‌های تازه‌ی `startGitProcess`/`watchGitTree`)، و در
`blobBatch.Close` لغو **پیش از** Wait تا git هنگ‌کرده هم بلاک‌نشونده
بکشته شود. اثبات قرمز→سبز: تست strengthened، پیش از رفع با
«۳.۴s > ۳s» و بقای فرزند پس‌زمینه قرمز بود و پس از رفع در ۰.۴۶s سبز شد.
(سناریوی `git_hang_times_out_and_leaves_no_orphan` حالا با اسکریپتی که
فرزند پس‌زمینه می‌سازد، بررسی «فرزندِ فرزند» را با الگوی
`TestRunShellTimeoutKillsChildTree` انجام می‌دهد.)

۲) پای «تست» چرخه‌ی soak (بند ۷.۲ سند: edit → **test** → revert → …)
اضافه شد: هر دور soak حالا یک `run_command` واقعی (`echo soak-ok`) از
طریق دروازه‌ی دستور و `runShell` اجرا و خروجی‌اش راستی‌آزمایی می‌شود —
spawn/teardown فرایند و پایپ‌ها هم زیر `-race` در حلقه قرار گرفت.

۳) نگهبان بن‌بست (`runWithDeadline`) روی دو سناریوی استریم‌دارِ باقی‌مانده
(`rate_limit_burst_then_success`، `malformed_sse_ends_cleanly`) هم اعمال
شد تا «هر سناریو بیش از مهلتش، دامپ گوروتین بدهد» واقعاً روی همه برقرار
باشد.

**گیت واقعی (اجرای `scripts/gate.sh`):**

```
=== gofmt -l . ===                [OK] (خالی)
=== go vet ./... ===              [OK]
=== go build ./... ===            [OK]
=== go test -race -count=1 ./... === ok  myai  32.335s  [OK]
=== env GOOS=windows GOARCH=amd64 go vet ./... === [OK]
=== env GOOS=linux   GOARCH=amd64 go vet ./... === [OK]
GATE: GREEN
```

**فایل‌های تغییر:** `checkpoint.go` (کشتن درخت فرایند git در ۳ نقطه + ۲
کمک‌تابع)، `soak_test.go` (پای run_command در soak، فرزندِ فرزند git،
نگهبان ۲ سناریو، `waitPIDGone`). بدون وابستگی تازه؛ بدون تغییر امضای
متد صادرشده؛ `grep TODO/FIXME/XXX` خالی.

**Stage 5 complete. Waiting for your instruction to continue.**
