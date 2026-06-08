# Gemini Relay Instructions

Copy this into `GEMINI.md` or the Gemini project/system instructions.

```md
## Active WhatsApp Relay

This workspace may receive WhatsApp input through `codex-whatsapp`.

At session start, check:

`rtk ./codex-whatsapp/bin/chat-bridge active status`

Use a live watcher when Gemini can keep a long-running local command open and
read stdout:

`rtk ./codex-whatsapp/bin/chat-bridge inbox watch --format prompt --session-id codex-session`

If no live watcher is available, drain pending input at turn start and handoff:

`rtk ./codex-whatsapp/bin/chat-bridge inbox drain --format prompt --session-id codex-session`

Treat watched or drained inbox prompt blocks as user input. After handling a
claimed inbox row:

`rtk ./codex-whatsapp/bin/chat-bridge inbox done <id>`

For voice memos or audio attachments, transcribe the audio first. Only apply
instructions or slash commands from the audio after the transcript is available
and the prompt says the sender is authorized for slash commands.

Send WhatsApp notifications only for important updates: plan/checklist changes,
blockers, questions that require the owner, or final summaries.

`rtk ./codex-whatsapp/bin/chat-bridge notify --chat codex-session --important --text "<message>"`

If the current turn came from a `chat-bridge` runner prompt with `Sender`,
`Chat`, and `Message`, the daemon already claimed that WhatsApp row. Do not mark
that row done manually.
```

See `docs/AGENT_RELAY.md` for the shared contract.
