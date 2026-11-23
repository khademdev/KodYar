## KodYar 1.0.2

**Cheaper, clearer thinking**
- Agent turns now send `reasoning_effort` (default `low`, switchable to medium/high/auto in Settings → Model) so the model acts instead of over-thinking. The same policy covers the main turn, `task` sub-agents and summarizer/compaction requests, and falls back automatically for providers that reject the parameter.
- The thinking panel stays open and live during a turn (elapsed time + estimated tokens), collapses when done, and its duration/token estimate is saved on each message so it survives reopening the conversation.

**Clearer live activity**
- Activity rows show a live timer, the last `run_command` output line, and `+added/−removed` diff stats.

**Stream-cut handling**
- A stream closed after `finish_reason` no longer shows a false "partial answer" warning; `finish_reason=length` still asks to continue. Every cut is logged with provider, model, bytes and finish reason for diagnosis.

**Screenshot vision**
- Screenshots are attached for vision-capable models, shown in the tool card with a "seen by model / saved for user" badge, and a settings switch controls the policy.

Full details: `CHANGELOG.md`.
