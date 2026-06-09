# Generic Agent Relay Instructions

Use this for any local AI client that can run shell commands and follow project
instructions.

```md
## Active WhatsApp Relay

This workspace may receive WhatsApp input through `coderoam`.

Required commands:

- Status:
  `rtk ./coderoam/bin/coderoam active status`
- Pick this client's session id from the delivered WhatsApp prompt, or from the
  row in `active status` that belongs to this group. Do not reuse another
  client's session id.
- Drain at turn start:
  `rtk ./coderoam/bin/coderoam inbox drain --format prompt --session-id <session-id>`
- Live watch, only when persistent stdout is continuously consumed:
  `rtk ./coderoam/bin/coderoam inbox watch --format prompt --session-id <session-id>`
- Mark handled:
  `rtk ./coderoam/bin/coderoam inbox done <id>`
- Important update:
  `rtk ./coderoam/bin/coderoam notify --chat <chat-or-session-alias> --important --text "<message>"`

Rules:

- Prefer drain for API-style sessions. Use live watch only when the client keeps
  reading stdout while idle.
- Treat watched or drained rows as user messages.
- Mark every claimed row done after handling.
- For voice memos or audio attachments, transcribe first; only apply commands
  from the audio after the transcript is available and slash-command
  authorization is shown.
- For images or screenshots, inspect the downloaded `local_path` before
  diagnosing visual issues or using the file as a product asset. If only
  metadata/caption text is present, ask for a resend or media download.
- Notify WhatsApp only for plan/checklist changes, blockers, owner questions,
  or final summaries.
- Do not send routine tool output or minor progress.
- If a runner prompt says to return an ignore marker and there is no important
  update, return that marker exactly.
```

See `docs/AGENT_RELAY.md` for the shared contract.
