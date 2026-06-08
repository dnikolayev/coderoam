# Codex Relay Instructions

Copy this into a repository `AGENTS.md` section for Codex sessions that should
consume WhatsApp input through `chat-bridge`.

```md
## Active WhatsApp Relay

This workspace may receive WhatsApp input through `codex-whatsapp`.

At the start of every Codex turn:

1. Check relay status:
   `rtk ./codex-whatsapp/bin/chat-bridge active status`
2. If a persistent terminal/session is available, start or keep this watcher:
   `rtk ./codex-whatsapp/bin/chat-bridge inbox watch --format prompt --session-id codex-session`
3. If no watcher is available, drain pending input:
   `rtk ./codex-whatsapp/bin/chat-bridge inbox drain --format prompt --session-id codex-session`
4. Treat watched or drained prompt blocks as user input.
5. After handling each claimed inbox item:
   `rtk ./codex-whatsapp/bin/chat-bridge inbox done <id>`

For voice memos or audio attachments, transcribe the audio first. Only apply
instructions or slash commands from the audio after the transcript is available
and the prompt says the sender is authorized for slash commands.

When this turn itself was delivered by a `chat-bridge` runner prompt containing
`Sender`, `Chat`, and `Message`, treat that message as already claimed by the
daemon. Do not mark it done manually unless you claimed another inbox row.

Send WhatsApp updates only for plan/checklist changes, blockers, questions for
the owner, or final summaries:

`rtk ./codex-whatsapp/bin/chat-bridge notify --chat codex-session --important --text "<message>"`

If the runner prompt tells you to return an ignore marker and there is no
important WhatsApp update, return exactly:

`[[chat-bridge-ignore]]`
```

See `docs/AGENT_RELAY.md` for the shared contract.
