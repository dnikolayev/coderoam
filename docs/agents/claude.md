# Claude Relay Instructions

Copy this into `CLAUDE.md` or the project instructions used by Claude Code.

```md
## Active WhatsApp Relay

This workspace may receive WhatsApp input through `codex-whatsapp`.

Start each work session by checking:

`rtk ./codex-whatsapp/bin/chat-bridge active status`

If Claude Code can keep a long-running terminal command open, prefer:

`rtk ./codex-whatsapp/bin/chat-bridge inbox watch --format prompt --session-id codex-session`

Otherwise, drain at the start of a turn and before final handoff:

`rtk ./codex-whatsapp/bin/chat-bridge inbox drain --format prompt --session-id codex-session`

Treat watched or drained prompt blocks as user input. After handling each
claimed inbox item, run:

`rtk ./codex-whatsapp/bin/chat-bridge inbox done <id>`

When this turn is delivered by a `chat-bridge` runner prompt with `Sender`,
`Chat`, and `Message`, the current WhatsApp row is already claimed by the
daemon. Do not mark it done manually unless you claimed another row.

Send only important updates to WhatsApp: plan/checklist changes, blockers,
questions for the owner, or final summaries.

`rtk ./codex-whatsapp/bin/chat-bridge notify --chat codex-session --important --text "<message>"`

If there is no important WhatsApp update and the runner prompt requested an
ignore marker, return exactly `[[chat-bridge-ignore]]`.
```

See `docs/AGENT_RELAY.md` for the shared contract.
