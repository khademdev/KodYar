# KodYar — Architecture Reference

Wails v2 desktop coding agent (Go + system WebView, Persian/RTL UI).
Single `package main` (~29.7k lines of production code + ~28k lines of tests);
frontend is embedded from `frontend/dist`
(no npm; JS is minified — never hand-edit, add new readable files instead).

## Lock order (mandatory — violations have deadlocked this app before)

```
a.genMu  →  a.changes.mu  →  a.mu  →  a.mcp.mu   (never acquire in reverse order)
a.permAskMu → a.changes.mu              (dialog serialization — see below)
errLogMu, a.configDirOnce, calibMu      (leaf locks; never held while taking others)
a.mcp.mu                                (leaf lock — MCP server registry, see below)
a.checkpointMu, a.readStore.mu, a.factsCache.mu  (leaf locks — Mission 3)
```

- `a.checkpointMu` (Mission 3, `checkpoint.go`) guards only two `map`s:
  which (conversation, turn) pairs were already attempted and which notices
  were already shown. Leaf: never held across git exec, disk I/O or
  `runtimeEmit`.
- `a.readStore.mu` (`fileread.go`) guards the bounded read-before-write LRU
  (≤500 paths × 16 conversations); `a.factsCache.mu` (`projectfacts.go`)
  guards the memoized project facts. Both are leaf locks held only while
  copying small structs — file I/O happens outside.

- `a.mcp.mu` (inside `a.mcp`, `mcpmanager.go`) guards the MCP server
  registry (live clients, per-server status/backoff bookkeeping, config
  fingerprints). It is a **leaf** lock: while holding it no other lock is
  taken, no server is talked to and no process is killed — `ensureStarted`,
  `toolMCPCall`, `stopAll` and friends snapshot what they need under the
  lock, release it, then do the long work outside. `a.mu` may be taken
  *before* it (copy configs under `a.mu`, then start servers), never
  while holding it. Inside one client, `writeMu` serializes stdin writes,
  `pendMu` guards the pending-response map AND the `closed` flag (single
  guardian — a mixed guard was a data race caught by `-race`).

- `a.mu` guards `a.settings` + conversation load/save (`prepareStream`
  requires it held). It is the innermost lock: `RevertFileChange` holds
  `changes.mu` while `resolveWorkspacePath` takes `a.mu`; entry points take
  `genMu` then `a.mu`.
- `calibMu` (inside `globalTokenCalib`, `tokencalib.go`) guards the
  per-conversation token-calibration LRU (≤64 convs, EMA ratio real/estimated
  clamped to [0.7, 2.5]). Leaf lock: no other lock is taken while holding it
  and it is never held across I/O. Step.
- `a.permAskMu` serializes permission dialogs (Step 4): only
  `requestPermissionConfirm` takes it, and it is deliberately held across the
  wait for the user's answer — that wait is exactly what must not overlap
  (parallel read-only tools can ask concurrently, e.g. two sensitive reads,
  and the frontend modal auto-rejects the previous ask). While holding it only
  `changes.mu` is taken, briefly. Order: `permAskMu → changes.mu`.
- **Never hold `a.mu` across network I/O, process exec, or `runtimeEmit`.**
  Copy needed values under the lock, unlock, then work (see `toolRunCommand`,
  `expandMentions`). `DeleteConversation` must stop+wait for the generation
  *before* taking `a.mu` (inverse order would deadlock).
- `a.gen` (active generation, guarded by `genMu`): single-flight — a new
  request while `a.gen != nil` is rejected. Cleared under `genMu` before the
  final emit; `genState.done` closes after the last disk write so
  `stopGenerationAndWait` can prove the goroutine fully exited.
- Every stream goroutine owns its `ctx`+`cancel` (`defer myGen.cancel()`),
  bounded channels (`confirms`, capacity 1) and timers that are always
  `defer timer.Stop()`.

## Agent loop data flow

1. `SendMessage` (bound) → `expandMentions` (no lock) → `genMu` single-flight
   check → under `a.mu`: append user msg, `prepareStream` (system prompt =
   env/agent prompt + git context + project rules; history window =
   `MaxHistory` msgs, leading `tool` msgs skipped, pinned msgs re-injected,
   old tool results compacted) → spawn `streamChat` goroutine → `chat:start`.
2. `streamChat` rounds (`0..maxRounds`, clamp 5–200 via `maxAgentRounds`
   setting — Step 2): POST `{baseURL}/chat/completions` (SSE; dial 20s /
   TLS 15s / response-header 90s) → per-delta `chat:delta` → accumulate
   `tool_calls` → no tools → `finishChat` (`chat:done`); tools → `chat:tool`
   per call → `execToolCtx` → `chat:tool_result` → `persistAgentRound`
   (mid-loop disk save) → next round. Step 2 also hardens the loop:
   invalid/truncated tool-call JSON gets a precise English error (truncation
   hinted via ctx when `finish_reason == "length"`), per-tool argument
   validation, a doom-loop guard (3rd identical consecutive call blocked,
   5th stops the turn with a Persian notice), and
   `stream_options.include_usage` with one 400-fallback + per-provider
   memory (`toolguard.go`). Step 3 adds token-aware context management
   (`ctxbudget.go`): budget = provider ContextWindow − output reserve − 10%;
   per-message token estimates are cached (`historyTokenCache`, includes
   tool_calls); at ≥80% of budget the API-copy of history is auto-compacted
   (old tool results pruned, then oldest units dropped — never system,
   pinned re-injections or the last user message; disk file untouched);
   `validateHistory` repairs protocol violations (tool pairing, stray
   system, empty content) with English log entries; a 400/413/422
   "context too long" error triggers one aggressive-compaction retry.
   Step 3b hardens this further: tool-argument validation and the
   executor's `gs/gi/gb` share ONE coercion implementation
   (`coerceString/coerceInt/coerceBool` in `toolguard.go`) so numeric
   strings (`"60"`) and string booleans (`"true"`) are accepted exactly
   as the executor always accepted them; when phase B drops units, exactly
   one English `[context notice]` marker (`contextDropMarker`) is placed
   after the system/pinned block (never dropped, idempotent) and the
   retained history never starts with an orphan assistant/tool; old
   tool-call `arguments` get their big string values (`content`,
   `old_string`, `new_string` > 400 runes) replaced by a short placeholder
   (JSON stays valid, id/name/pairing intact — API copy only); aggressive
   mode may also drop after-last-user units except the 3 most recent tool
   results (a single long agent turn can now shrink); token estimates are
   calibrated per conversation against real `prompt_tokens` (EMA, LRU 64,
   `tokencalib.go`) and applied only to budget logic via
   `calibratedBudget`; the `stream_options`/tools-unsupported/overflow
   fallbacks no longer consume `maxAgentRounds` rounds. Step 4 parallelizes
   tool execution: within one assistant message, runs of consecutive
   read-only calls (`list_dir`, `read_file`, `search_files`, `glob`,
   `git_status/git_diff/git_log/git_show`) execute concurrently with a worker
   cap of 4 (`toolbatch.go`); every other call (mutating, blocked by the loop
   guard, …) stays a sequential barrier. The doom-loop guard decides before
   dispatch, single-threaded in original order; results, history appends and
   the mid-loop disk save always land in the original call order, and each
   call's `chat:tool`/`chat:tool_result` pair stays keyed by `callId`
   (parallel calls may interleave). No lock is held across tool execution;
   all workers are joined before the round ends; permission asks are
   serialized via `a.permAskMu`.
   Tool inventory (agent.go): the built-in tools (`list_dir`, `read_file`,
   `write_file`, `edit_file` with the atomic multi-edit `edits[]` form,
   `move_path`, `delete_path`, `create_dir`, `search_files`, `glob`
   (mtime-desc ordering), `run_command` (foreground; `background:true`
   returns a process id), `process_output/process_stop/process_list`
   (incremental log tail, tree kill, status — `processes.go`),
   `git_status/git_diff/git_log/git_show`, `task`, `todo_write`,
   `web_fetch`, `diagnostics`) plus custom user tools
   and `mcp_<server>_<tool>` tools from enabled MCP servers
   (`mcpmanager.go`; lazy parallel start with a 15 s budget, backoff
   1s/2s/4s, give-up after 3 consecutive failures until settings are
   re-saved, whole-tree kill on change/disable/shutdown; a crashed server
   fires the client's `onExit` from `waitExit` — deliberate stops are
   flagged and don't count — removing it from the map and scheduling the
   backoff, so the crash-recovery lifecycle actually holds; stdin writes
   carry a 10 s watchdog (`mcpWriteTimeout`) that kills the tree of a
   server that stopped reading; `waitExit` joins `readDone` — the
   readLoop drain, capped at 2 s — before `cmd.Wait()` so a dying
   server's final responses are not lost). `web_fetch`
   (`webfetch.go`) enforces its SSRF guard at connect time on every
   redirect hop via a custom dialer `Control` (loopback/private/link-
   local/CGNAT/0.0.0.0/8/multicast/broadcast/reserved/benchmarking/
   documentation/ULA, IPv4-mapped and NAT64 forms unwrapped), ignores
   environment proxies, converts Windows-1256/ISO-8859-6 charsets, wraps
   output in an untrusted `<fetched_content>` envelope, and asks
   permission per URL. `todo_write` persists its checklist on the
   conversation (`todos` field, fork-copied) and emits `chat:todos`.
   `diagnostics` (`diagtool.go`) is read-only and talks LSP to a
   per-language server (`lsp.go`: gopls for Go, typescript-language-server
   for TS/JS; one lazily started process per language, killed by
   `diagMgr.stopAll()` during shutdown, console window hidden): initialize,
   `textDocument/didOpen`, collect `publishDiagnostics`, reply `null` to
   server-to-client requests, then report errors as
   `path:line:col: message (source)` plus a warning count. Bounded to 10
   files, 512 KB per file, 300 chars per message, 15 s per file and a 45 s
   total budget; no code actions, and a missing server returns an install
   hint instead of a silent fallback (the prompt then falls back to
   build/test).
3. Mutating tools flow through `writeFileWithConfirm` etc.: build new
   content in memory → diff → `requestFileConfirm` (event `agent:confirm`,
   waits on channel/ctx/3-min timeout; frontend answers via bound
   `RespondFileConfirm`) → backup to `backups/` → write → record in
   `changes.json` → emit `agent:change_applied` (rejection records a
   rejected change instead). Step 5 hardens `edit_file` matching
   (`editmatch.go`): after the exact byte-for-byte match fails, it retries
   cumulatively with line-ending normalization (CRLF/LF), then
   trailing-whitespace tolerance, then indentation tolerance — the last one
   only for a unique match, even with `replace_all`. Matches are found in a
   normalized view with an offset map back to the original bytes, so the
   file's BOM and line-ending style are always preserved (the replacement
   text is adapted to the matched region's EOL style) and all occurrences
   are applied in one in-memory build + one atomic write. The result message
   reports which strategy matched. Step 6 adds the `task` tool
   (`subagent.go`): the parent agent delegates ONE self-contained
   read-only exploration/analysis subtask to a headless sub-agent with a
   fresh context (`runSubagent` — no UI events, no disk writes, no
   conversation persistence). The sub-agent sees ONLY the 8 read-only
   tools (`subagentAllowedTools` — depth is 1 by construction: no nested
   `task`, no mutating tools, no `run_command`, no custom tools), runs
   under the same retry / context-budget / compaction / overflow-recovery
   / doom-loop-guard machinery as the main loop, and its final report
   (stats header + head+tail-capped 8K body, `subagentReport`) is
   returned to the parent model as the tool result. Round budget comes
   from `MaxSubagentRounds` (default 8, clamp 3–50) plus a hard 5-minute
   wall clock; user Stop propagates through the parent ctx. Permission
   default for `task` is **ask** (the modal shows the task excerpt);
   inside the sub-agent `requestPermissionConfirm` fast-rejects via the
   `subagentNoAsk` context marker, so sensitive reads are denied without
   any modal. Tool errors return to the sub-agent model as English
   guidance; transient provider errors retry with backoff (logged under
   the `subagent` scope).
4. `run_command` / custom tools / bound `RunCommand` (all three go through
   the same gate): `AllowCommands` master switch → `gateCommand` in
   `permissions.go` (order per segment + on the whole command:
   hard-block list → user rules `Permissions.CommandRules` (first glob
   match wins, an explicit allow also skips risky-confirm) → risky
   patterns → safe allow-list → default **ask**). The agent's `run_command`
   refuses `git diff/show/log` before the gate (`agentCommandBlocked`): those
   subcommands honour `diff=<driver>`/`diff.<driver>.textconv` from the repo
   and `diff.external` from `.git/config`, and would run an external program
   after a plain approval; the `git_diff`/`git_show`/`git_log` tools do the
   same job with `--no-ext-diff --no-textconv`. Then → `runShell` in `shell.go`
   (single runner: `cmd /S /C "chcp 65001 …"` + Job-Object tree kill on
   Windows, `sh -c` + Setpgid/SIGTERM→SIGKILL group kill on Unix; timeout
   60s default / 300s cap; `WaitDelay` 5s; bounded head+tail output
   buffer; incremental `chat:tool_output` events throttled ≤10/s).
   Tool-level permission overrides via `Permissions.Tools` gate
   `execToolCtx`; sensitive files (`.env*`, `*.pem`, … configurable via
   `Permissions.SensitiveGlobs`) require an ask before `read_file` /
   `@mention` and are skipped by `search_files`; `search_files`/`list_dir`/
   `glob` honor `.gitignore` (matcher in `gitignore.go`) plus the built-in
   skip list. Step 5 adds the read-only `glob` tool (names only, contents
   never read; `**` any-depth, slash-free patterns match the file name at
   any depth, slash patterns anchor to the search root; ≤300 results;
   sensitive file *names* are visible like `list_dir`). API keys are AES-GCM encrypted with a master key stored in the OS
   keyring (`crypto.go`, format `v2:`; lazy migration from the legacy
   derived key; legacy fallback if keyring unavailable).
5. Stop / errors: `StopGeneration` cancels ctx; mid-stream break → `savePartial`
   persists partial text (`Aborted: true`); `chat:error` on hard failures;
   `ResumeAgent` (bound) continues a half-finished agent run. Connection
   errors from `chatHTTPClient.Do` are classified (`classifyNetError`,
   `retry.go`): TLS certificate/verification failures, invalid URL/unsupported
   scheme and `context.Canceled` are never retried (clear Persian final
   error); DNS "no such host" is retried at most once; timeouts,
   reset/refused, EOF keep the full backoff policy.

## Agent accuracy and safety net (Mission 3, 1.8.0)

- **Read-before-write / stale guard** (`fileread.go`): every successful
  `read_file` records a fingerprint `(size, mtime, SHA-256)` per
  (conversation, absolute path) in a bounded LRU (≤500 paths × ≤16
  conversations, leaf lock `a.readStore.mu`). `write_file` on an existing
  file is rejected with an English re-read instruction unless the agent
  read (or created) it in this conversation and the on-disk hash still
  matches; `edit_file` fails on staleness and merely annotates when never
  read (exact `old_string` matching remains the second guard); `move_path`
  re-points the record, `delete_path` forgets it, conversation deletion
  drops it. Sub-agent reads are not recorded for the main agent.
- **Verification discipline** (`turnreview.go` + `chat.go`): the per-turn
  `turnRecorder` records every executed command with its exit code and
  marks a successful `isVerificationCommand` (build/test/lint/vet) as the
  turn's verification. When the model produces a final answer while files
  changed after the last successful verification (and ≥2 rounds remain),
  `streamChat` injects ONE English user nudge per turn
  (`verificationNudgeText`) and continues; the nudge never consumes the
  doom-loop guard or the round budget and never repeats. The end-of-turn
  summary (`chat:turn_summary`) reports files (± lines, protected chip,
  reverted state), executed commands with exit codes, verification
  (ran/ok/command), tokens, and `tokenCapHit/TokenCap`.
- **Project facts** (`projectfacts.go`): a short English "Project facts"
  block (ecosystem + suggested verification commands, parsed from
  `go.mod`/`package.json`/`Cargo.toml`/`pyproject*`/`pom.xml`/
  `build.gradle*`/`*.sln`/`composer.json`/`Makefile` — nothing executed,
  ≤64 KB per file) is injected once per turn into the system prompt, memoized
  by fingerprint under the leaf lock `a.factsCache.mu`.
- **Turn token cap** (A.4): `MaxTurnTokens` (0 = off) is enforced in the
  agent loop — cumulative prompt+completion across all rounds plus the
  sub-agent usage tally (`usageTally` via ctx). One Persian notice at 80%
  (`tokenCapWarnPercent`); past the cap the turn stops gracefully AFTER the
  current tool round is persisted (conversation stays resumable) and the
  summary carries `tokenCapHit/TokenCap`.
- **Project checkpoints** (`checkpoint.go`): before the FIRST mutating
  action of a turn (after user approval, outside every lock) — from a
  file-changing tool or a non-allow-listed command — a checkpoint commit is
  created from a temporary index that is filled **filter-free** (Mission 4
  pipeline — see the next section) and referenced as
  `refs/kodyar/checkpoints/<convID>-<turnID>`; ≤20 refs per
  workspace are kept, 10 s budget, any failure skips with one Persian
  notice (turn never blocks; non-git workspaces silently rely on the
  file-tool revert only). `PreviewCheckpointRestore(convID, turnID)` lists
  rewrite/trash/unchanged/skipped; `RestoreCheckpoint` applies the snapshot
  per file, streaming and byte-exact (next section) — it **never deletes**:
  current versions of rewritten/removed files go to
  `<configDir>/trash/<workspace-hash>/<timestamp>/` (swept at startup,
  >14 days or >500 MB), ignored files are untouched, path escapes are
  rejected (`safeCheckpointRel` + real-path parent resolution), symlinks
  are skipped or refused.

## Stability and defect elimination (Mission 4, 1.8.1)

Stability-only mission (no new features; every defect has
failing-before/passing-after tests):

- **Crash isolation** (`safego.go`): an unrecovered panic in any goroutine
  kills the whole process, so every non-test goroutine start runs through
  `safeGo(a, where, fn)` (goroutine with `defer recoverPanic`); synchronous
  sections use `callGuarded`. Recover logs the panic value and a bounded
  (8 KB) stack to `errors.log` (never secrets) and restores state: the
  `streamChat` goroutine emits the Persian internal-error notice, persists
  the partial answer like the abort path, releases the generation
  locks/flags and finishes the chat (UI never stays stuck "generating"); a
  panicking parallel tool worker returns
  `ERROR: internal error in tool <name> (see the error log).` as its tool
  result so the `tool_calls` ↔ `tool` pairing stays valid while other
  workers continue; a sub-agent panic becomes an error report to the
  parent; MCP read/stderr/manager loops mark that server dead (existing
  backoff) and fail pending calls fast; shell/checkpoint/web_fetch helpers
  return errors. Every exported `*App` method that does non-trivial work
  opens with `defer a.recoverBound("<Method>")` / `recoverBoundErr`
  (signatures unchanged; trivial getters are an explicit allow-list — the
  AST check `TestExportedAppMethodsHavePanicGuard` fails any unguarded
  method). Locks keep the Lock→`defer` Unlock pairing so a panic cannot
  leak one.
- **Stream stall watchdog** (`streamwatch.go`): every model stream (main
  chat and sub-agent) wraps its response body in `streamIdleGuard`, which
  timestamps the last successful read (any bytes — SSE keep-alive comments
  count) while one watchdog goroutine per stream cancels the request only
  after `streamIdleTimeoutSec` seconds of silence (`Settings.StreamIdleTimeoutSec`,
  default 180, clamp 30–900, editable in the «مدل و رفتار» tab; counting starts
  only after `Arm()` — i.e. once the response headers arrived, so a slow/queued
  provider is never mistaken for a stalled stream — and a watchdog cancel during
  connection setup goes through the pre-output retry policy instead of surfacing
  a raw `context canceled`. Package vars are injectable in tests; the timer and
  the goroutine are always stopped. A stall **before any output** is a retryable
  transient failure under the existing retry policy (Persian retry
  notice); **after** deltas/tool-call deltas were streamed it never
  auto-retries (duplicated side effects) — the partial answer is saved
  exactly like an abort (`Aborted: true`) with the Persian notice
  «دریافت پاسخ متوقف شد (بدون داده برای n ثانیه). می‌توانید «ادامه» را بزنید.»
  and resume stays available. User Stop is never reported as a stall.
- **Atomic writes and corruption recovery** (`crypto.go` `atomicWriteFile`,
  `atomicwrite_windows.go`, `recovery.go`): temp files are unique in the
  target directory (`os.CreateTemp`-style; the startup sweep recognizes
  both name styles); on Windows the final rename retries transient
  `ACCESS_DENIED`/`SHARING_VIOLATION`/`LOCK_VIOLATION` (`renameWithRetry`,
  jittered exponential backoff inside a 2 s budget) and then fails naming the
  file. Conversations
  and `changes.json` go through `atomicWriteFileWithBackup` — a
  one-generation `.bak` (rename-based, crash-safe ordering) before every
  replacement; on load failure the loader tries `.bak`, then **salvages**
  (`salvageConversation` stream-decodes the messages array and keeps every
  complete message), renames the damaged original to
  `<name>.corrupt-<timestamp>` (≤5 kept, `pruneCorruptFiles`) and shows
  the Persian notice «گفتگو آسیب دیده بود؛ n پیام بازیابی شد.».
  Disk-full/write failures surface a clear Persian notice and never drop
  the in-memory state or truncate the existing file.
  `validatePathForWrite` rejects Windows reserved names / long-path
  hazards with a clear tool error.
- **Honest read-before-write** (Stage 4.1): read records carry
  `Full bool` (`fileread.go`) — only a read that covered the whole file
  and was not truncated authorizes `write_file` on an existing file;
  `edit_file` keeps working with partial records (exact `old_string`
  matching is its guard) and the stale-hash check applies to both.
- **Filter-free checkpoints** (Stage 4.2): snapshot content never passes
  `git add` — blobs are created with `git hash-object -w --no-filters
  --stdin-paths` and the temporary index is filled with
  `git update-index --index-info` (deletions included, modes preserved);
  every checkpoint git call runs with `-c core.autocrlf=false -c
  core.safecrlf=false -c core.fsmonitor=false -c core.hooksPath=os.DevNull`
  and `GIT_CONFIG_NOSYSTEM=1`/`GIT_ATTR_NOSYSTEM=1`, so the user's global
  `core.autocrlf`, in-tree `.gitattributes` and clean/smudge filters (LFS)
  cannot alter bytes. Restore reads blobs raw via `git cat-file` — never
  `checkout-index`, which would smudge again. Symlinks stay skipped;
  binaries are raw. `TestCheckpointNoFilterByteFaithful` proves
  byte-identical trees under `core.autocrlf=true` + `.gitattributes` +
  a fake LFS filter + Persian/ZWNJ names and CRLF/LF/binary mixes.
- **Restore safety** (Stage 4.3): before any write/move,
  `resolveRestorePath` resolves the real path of the nearest existing
  parent (`filepath.EvalSymlinks` + Windows reparse checks) and requires
  it to stay inside the real workspace root — a directory that became a
  symlink/junction after the snapshot is refused and reported, never
  followed; an existing target that is itself a symlink is refused
  (`os.Lstat`). Nothing is materialized in bulk: per file, size is
  compared first, then content in 64 KiB chunks (`checkpointBlobChunk`,
  `bytes.Equal`), and restored bytes go through `atomicWriteStream`
  (unique temp file + fsync + atomic rename) — no file is ever buffered
  whole, so memory stays flat regardless of file size. Work is bounded by
  `checkpointMaxFiles` (5000) and `checkpointRestoreTime` (60 s); hitting
  a limit stops **before** modifying anything (preview first, no partial
  restore). Git children run through the same process-tree kill as shell
  commands (`startGitProcess` + `watchGitTree`; on timeout/cancel
  SIGTERM→SIGKILL to the whole group/Job, `blobBatch.Close` cancels
  before `Wait`) — no orphaned grandchildren holding pipes open.
- **Proof (Stage 5)**: 9 fuzz targets (`fuzz_test.go`; seed corpora
  committed under `testdata/fuzz/`), a bounded soak loop with
  goroutine/heap/fd/config-dir growth assertions
  (`TestSoakAgentLoop`), 9 fault-injection end-state scenarios
  (`TestFaultInjectionEndStates`: 429/5xx burst, malformed SSE, tool
  panic, disk-full, mid-stream stall, MCP crash/hang, git missing/hang
  with orphan check), the AST lock-order audit
  `TestLockAuditNoRuntimeEmitUnderAppLocks` (found and fixed a real
  `a.mu`-held-across-emit in `finishChat`'s save-failure path) and the
  `runWithDeadline` deadlock watchdog (full goroutine dump past the
  deadline) on every stream-driven scenario.

## Subsystems added in 1.9 – 1.13

- **Command sandbox (`sandbox*.go`)** — every `run_command`, sub-agent command
  and auto-verify run goes through `sandboxPolicyForCommand` → Windows
  AppContainer (workspace read/write, network per setting). Setup failure is
  fail-closed; git/MSYS is a documented exemption; `scripts/grant-sandbox-nul.ps1`
  (and app startup) grants `\Device\Null` so go/git can run inside.
- **Read-before-write + turn review (`fileread.go`, `writeguard.go`, `turnreview.go`,
  `verifycover.go`)** — a bounded per-conversation read store, hash checks against
  external edits, and a per-turn recorder of changed files/commands that feeds the
  real summary (`chat:turn_summary`) and the one-time verification nudge.
- **Auto-verify (`autoverify.go`)** — after a round that changed files, a fixed
  whitelisted build/typecheck command runs once per turn (gate + sandbox aware,
  never a binary from inside the repo); only failures are injected into the next
  round. Setting `autoVerify` (default on).
- **Sub-agents (`subagent.go`)** — headless read-only loop with its own rounds/token
  budget, `subagentNoAsk` context (no modals inside), verification-only
  `run_command`, and parallel `task` batches (limit `parallelTaskLimit()`, 2–3).
- **`apply_patch` (`patchtool.go`)** — unified-diff editing (multi-file, multi-hunk)
  with pre-write validation, ambiguity rejection, `dry_run`, and the same
  backup/diff/approval path as `write_file`/`edit_file`.
- **Context compaction (`ctxbudget.go`, `compactsummary.go`)** — tool-result/turn
  compaction plus a drop marker enriched with deterministic *pinned facts* and an
  optional one-shot model summary (best-effort, bounded, never breaks a turn).
- **Latency + prompt cache (`latency.go`, `promptcache.go`)** — per-turn timing
  (`chat:timing`, `GetLastTurnTiming`) and cache hints on the stable prompt block.
- **Frontend panels (`usagering.js`, `activity.js`, `sidebar.js`, `composer.js`,
  `errorcapture.js`, `sandboxui.js`)** — additive readable files loaded from
  `index.html`; minified `app.js`/`modals.js`/`mock.js`/`popovers.js` stay untouched.

## Events (backend → frontend, Wails runtime)

| Event | Payload (keys) | Emitted by |
|---|---|---|
| `chat:start` | conversation, userMessage | entry points (Send/Regenerate/ApprovePlan/Resume) |
| `chat:delta` | conversationId, delta, elapsed | stream loop |
| `chat:usage` | conversationId, contextTokens, tokenLimit, roundPrompt, roundCompletion, turnTokens, exact | after each streamed round in `streamChat` — feeds the live token ring (real prompt tokens of that request, or that round's estimate) |
| `chat:notice` | conversationId, text (Persian) | tools-unsupported, save warnings, auto-compaction, token-cap 80%/stop, checkpoint skipped |
| `chat:tool` | conversationId, callId, name, args | before each tool exec (parallel read-only calls may interleave; keyed by callId) |
| `chat:tool_result` | conversationId, callId, name, ok, result, ms | after each tool exec |
| `chat:done` | conversationId, message, conversation, tokens, aborted, tokensUsed, tokenLimit, recentUsage | finishChat |
| `chat:error` | conversationId, error | emitChatError |
| `agent:confirm` | change (FileChange) | file diff confirm; answered by `RespondFileConfirm(changeId, allow)` |
| `agent:permission_ask` | ask (permissionAsk: kind `command`/`sensitive_read`/`tool`/`web_host`; `url` for web_fetch, `command` carries name+args for MCP tools) | permission modal; answered by `RespondPermissionConfirm(askId, allow\|session\|always\|reject)`; `web_host` offers per-host rules only — a tool-wide web_fetch allow no longer exists (C.1), existing ones migrate back to ask |
| `chat:tool_output` | conversationId, callId, chunk | incremental run_command output (≤10/s, frontend may ignore) |
| `chat:todos` | conversationId, todos | live agent checklist after every successful `todo_write` (empty array = cleared); frontend ignores non-active conversations |
| `chat:turn_summary` | summary (TurnSummary: files/commands/verification/tokens/tokenCap) | end of every turn that changed files or ran commands; informational card UI (`turnsummary.js`: files, commands, verification, tokens, token cap) — the revert/checkpoint actions were removed from the UI |
| `agent:change_applied` / `agent:change_rejected` / `agent:change_reverted` | change / changeId, path / changeId | changes store |
| `conv:size_warn` | conversationId, kb, capKb | saveConversation (≥75% of cap, once per conv) |

`task` (Step 6) emits no extra events: the delegation shows up as a normal
`chat:tool`/`chat:tool_result` pair (icon `map`, args summary = description)
and the sub-agent itself is fully silent.

## Data on disk

Config dir = `os.UserConfigDir()/KodYar` (Win `%AppData%\KodYar`, macOS
`~/Library/Application Support/KodYar`, Linux `~/.config/KodYar`); override with
`KODYAR_CONFIG_DIR`; WebView profile in `config/webview`. Contents:

- `settings.json` — providers, workspace, switches, permission rules +
  tool overrides + sensitive globs (`permissions` section), MCP servers
  (`mcpServers`: name/command/args/enabled + **encrypted** env values —
  the UI only ever sees a mask), encrypted API keys (AES-GCM with an
  OS-keyring master key, `v2:` format; legacy derived-key fallback);
  atomic write `.tmp`+fsync+rename; corrupt file renamed `.corrupt`
  (keys survive).
- `conversations/<id>.json` — one per conversation; atomic write; images
  externalized to `conversations/img/*.b64` (`img://` refs in JSON);
  `conversations/.index.json` — self-healing metadata index.
- `changes.json` — applied/rejected agent file changes (≤500 records;
  >8MB serialized → keep recent half, drop old backups).
- `backups/*.orig` — pre-change file copies for revert (total ≤200MB, oldest
  pruned).
- `trash/<workspace-hash>/<timestamp>/` — rescue copies written by
  `RestoreCheckpoint` (restore never deletes); swept at startup (>14 days
  or >500 MB total).
- `errors.log` — user-visible error log (≤128KB, tail-kept rotation).

Workspace (user-chosen): rule files `.kodyar/rules.md` → `AGENTS.md` →
`CLAUDE.md` → `.cursorrules` → `.kodyarrules` (first match wins; ≤12KB
injected into the system prompt, UTF-8-safe cut; content cached by
(workspace, path, size, mtime) under `a.mu` — `rulesCache`, Step 5 — so
unchanged rule files are not re-read each turn). All model-supplied paths go
through `resolveWorkspacePath`: clean + `withinDir` (case-insensitive on
Windows) + symlink-aware `evalExisting`, and on Windows a final-path check
(`GetFinalPathNameByHandleW`, `pathresolve_windows.go`) that also catches
junctions/reparse points — `EvalSymlinks` alone reports a junction as an
ordinary directory, which would let a link inside the workspace point
outside it. UNC, `\\?\`, `\.\`, drive-relative, 8.3, ADS and reserved names
are covered by table tests (`workspace_guard*_test.go`).

Git tools are read-only, exec'd without a shell (ref whitelisted), 20s
timeout, and every call carries `-c core.fsmonitor=false -c
core.hooksPath=os.DevNull` (`gitSafeArgs`) so a `.git/config` written through
the agent cannot execute a command during `status`/`log`; `git diff`/`show`
add `--no-ext-diff --no-textconv` for the same reason (a `diff=<driver>`
attribute plus `diff.<driver>.textconv`, or `diff.external`, would otherwise
run an external program), and the pager is pinned by `GIT_PAGER=cat` in
`gitEnv`.

## CI

`.github/workflows/ci.yml`: `windows-latest` (gofmt gate, `go vet`,
`go test -race`, `go build -tags production` desktop compile),
`ubuntu-latest` (GTK/WebKit2GTK 4.1 dev packages, vet + test +
`go build -tags "production webkit2_41"`), and an optional cross-build
job with the Wails CLI pinned to the go.mod version. Actions pinned by
major version; concurrency cancels in-progress runs; Go modules cached
via setup-go. (Not executed locally — validated by offline YAML parse
and reading the Wails build-tag sources.)

## Frontend notes

`frontend/dist/assets/js/*.js` are minified (app.js, modals.js, mock.js, …);
`mock.js` provides browser Mock mode; new UI goes into new readable files
(e.g. `permissions.js`, `permsettings.js`, loaded from `index.html`). Bindings live in
`frontend/wailsjs/` — hand-maintained (`-skipbindings` build; deliberately tracked
in git, not ignored); every new exported `App` method must be added there
(`App.js`, `App.d.ts`, and any new struct types like `PermissionSettings`/`PermissionRule`
in `models.ts`). The whole set can be regenerated deterministically from the Go
sources after signature changes with `scripts/gen_wailsjs.py` (review the diff —
it maps Go types to TS per the header notes). UI strings are Persian; tool
results/prompts to the model stay English.

The settings modal's «دسترسی‌ها» tab (`permsettings.js`) manages the
permission system at runtime: `PermissionsView()` (copies under `a.mu` —
tools with defaults+overrides incl. orphaned custom-tool overrides, command
rules in order, sensitive globs), `SetToolPermission(tool, allow|ask|deny|default)`
(name-validated; `default` clears the override), `RemoveCommandRule(pattern)`
(revokes a persisted "always allow"), and `ResetPermissions()` (everything
back to safe defaults; the hard-block list is not part of settings and is
unaffected). Note: tool-level overrides never bypass the command gate
(`run_command` always passes `gateCommand`) or the diff-confirm modal
(driven by `ConfirmFileWrites`).

`agentsettings.js` also drives the «مدل و رفتار» round-budget fields:
`SetMaxAgentRounds` (5–200, default 25) and `SetMaxSubagentRounds`
(3–50, default 8, Step 6) — both clamped client-side too; `models.ts`
exposes them via `SettingsView` as `maxAgentRounds`/`maxSubagentRounds`.

The turn-summary card (`turnsummary.js`, class `ts-card`) renders
`chat:turn_summary` — file rows with ± counts and a protected chip,
command/verification rows, token totals and the token-cap chip. It is
purely informational: the revert panel (`changes.js` list, badge row) and
all revert/restore actions (per-file, per-turn, project checkpoint modal)
were removed from the product; `changes.js` now only owns the Diff confirm
modal (`#diff-overlay`) and `renderDiff`. The card and the diff modal are
covered by the UI contract test (`uicontract_test.go` — overlay ids,
visibility CSS, bindings sync) and the optional Playwright smoke test
(`frontend/tests/`, Mock mode, see its README).
