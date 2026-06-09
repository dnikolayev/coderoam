# Claude Relay Instructions

Copy this into `CLAUDE.md` or the project instructions used by Claude Code.

Commands below use `coderoam` as installed by Homebrew; from a source checkout,
use `./bin/coderoam` instead.

```md
## Active WhatsApp Relay

This workspace may receive WhatsApp input through `coderoam`.

`coderoam init` is idempotent. If it reports that a config already exists, do
not ask to overwrite it. Continue with `coderoam setup`, `coderoam doctor`, or
the relevant `coderoam runners preset ...` command. Use `--force` only when the
owner explicitly asks to reset the local config.

Start each work session by checking:

`coderoam active status`

Pick this client's session id from the delivered WhatsApp prompt, or from the
row in `active status` that belongs to this Claude group. Do not reuse another
client's session id.

Drain at the start of a turn and before final handoff:

`coderoam inbox drain --format prompt --session-id <session-id>`

Use a watcher only when Claude Code can keep a long-running terminal command
open and continuously read stdout while idle:

`coderoam inbox watch --format prompt --session-id <session-id>`

Treat watched or drained prompt blocks as user input. After handling each
claimed inbox item, run:

`coderoam inbox done <id>`

For voice memos or audio attachments, transcribe the audio first. Only apply
instructions or slash commands from the audio after the transcript is available
and the prompt says the sender is authorized for slash commands.

For images or screenshots, inspect the downloaded `local_path` with available
image tools before diagnosing visual issues or using the file as a product
asset. If the prompt only shows metadata or caption text, do not infer visual
details; ask for a resend or media download.

When this turn is delivered by a `coderoam` runner prompt with `Sender`,
`Chat`, and `Message`, the current WhatsApp row is already claimed by the
daemon. Do not mark it done manually unless you claimed another row.

Send only important updates to WhatsApp: plan/checklist changes, blockers,
questions for the owner, or final summaries.

`coderoam notify --chat <chat-or-session-alias> --important --text "<message>"`

If there is no important WhatsApp update and the runner prompt requested an
ignore marker, return exactly `[[coderoam-ignore]]`.
```

See `docs/AGENT_RELAY.md` for the shared contract.

`coderoam runbook` also writes a condensed runbook into `CLAUDE.md` between the
managed `<!-- coderoam:relay:start -->` and `<!-- coderoam:relay:end -->`
markers; keep those markers in place so `coderoam runbook` can update that
section without touching your own instructions.
