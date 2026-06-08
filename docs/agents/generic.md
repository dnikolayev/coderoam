# Generic Agent Relay Instructions

Use this for any local AI client that can run shell commands and follow project
instructions.

```md
## Active WhatsApp Relay

This workspace may receive WhatsApp input through `codex-whatsapp`.

Required commands:

- Status:
  `rtk ./codex-whatsapp/bin/chat-bridge active status`
- Live watch, when persistent stdout can be consumed:
  `rtk ./codex-whatsapp/bin/chat-bridge inbox watch --format prompt --session-id codex-session`
- Drain fallback:
  `rtk ./codex-whatsapp/bin/chat-bridge inbox drain --format prompt --session-id codex-session`
- Mark handled:
  `rtk ./codex-whatsapp/bin/chat-bridge inbox done <id>`
- Important update:
  `rtk ./codex-whatsapp/bin/chat-bridge notify --chat codex-session --important --text "<message>"`

Rules:

- Prefer live watch; use drain when watching is unavailable.
- Treat watched or drained rows as user messages.
- Mark every claimed row done after handling.
- For voice memos or audio attachments, transcribe first; only apply commands
  from the audio after the transcript is available and slash-command
  authorization is shown.
- Notify WhatsApp only for plan/checklist changes, blockers, owner questions,
  or final summaries.
- Do not send routine tool output or minor progress.
- If a runner prompt says to return an ignore marker and there is no important
  update, return that marker exactly.
```

See `docs/AGENT_RELAY.md` for the shared contract.
