# Claude Relay Instructions

Copy this into `CLAUDE.md` or the project instructions used by Claude Code.

```md
## Active WhatsApp Relay

This workspace may receive WhatsApp input through `coderoam`.

Start each work session by checking:

`rtk ./coderoam/bin/coderoam active status`

Drain at the start of a turn and before final handoff:

`rtk ./coderoam/bin/coderoam inbox drain --format prompt --session-id codex-session`

Use a watcher only when Claude Code can keep a long-running terminal command
open and continuously read stdout while idle:

`rtk ./coderoam/bin/coderoam inbox watch --format prompt --session-id codex-session`

Treat watched or drained prompt blocks as user input. After handling each
claimed inbox item, run:

`rtk ./coderoam/bin/coderoam inbox done <id>`

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

`rtk ./coderoam/bin/coderoam notify --chat codex-session --important --text "<message>"`

If there is no important WhatsApp update and the runner prompt requested an
ignore marker, return exactly `[[coderoam-ignore]]`.
```

See `docs/AGENT_RELAY.md` for the shared contract.
