# Codex Relay Instructions

Use this as the relay setup text for Codex sessions that should consume
WhatsApp input through `coderoam`.

```md
## Active WhatsApp Relay

This workspace may receive WhatsApp input through `coderoam`.

At the start of every Codex turn:

1. Check relay status:
   `rtk ./coderoam/bin/coderoam active status`
2. If a persistent terminal/session is available, start or keep this watcher:
   `rtk ./coderoam/bin/coderoam inbox watch --format prompt --session-id codex-session`
3. If no watcher is available, drain pending input:
   `rtk ./coderoam/bin/coderoam inbox drain --format prompt --session-id codex-session`
4. Treat watched or drained prompt blocks as user input.
5. After handling each claimed inbox item:
   `rtk ./coderoam/bin/coderoam inbox done <id>`

For voice memos or audio attachments, transcribe the audio first. Only apply
instructions or slash commands from the audio after the transcript is available
and the prompt says the sender is authorized for slash commands.

When this turn itself was delivered by a `coderoam` runner prompt containing
`Sender`, `Chat`, and `Message`, treat that message as already claimed by the
daemon. Do not mark it done manually unless you claimed another inbox row.

Send WhatsApp updates only for plan/checklist changes, blockers, questions for
the owner, or final summaries:

`rtk ./coderoam/bin/coderoam notify --chat codex-session --important --text "<message>"`

If the runner prompt tells you to return an ignore marker and there is no
important WhatsApp update, return exactly:

`[[coderoam-ignore]]`
```

See `docs/AGENT_RELAY.md` for the shared contract.
