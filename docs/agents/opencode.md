# OpenCode Relay Instructions

Copy this into the OpenCode project instructions or any compatible client
instruction file.

```md
## Active WhatsApp Relay

This workspace may receive WhatsApp input through `coderoam`.

Check status first:

`rtk ./coderoam/bin/coderoam active status`

Prefer a live watcher when the client can keep a persistent local command open:

`rtk ./coderoam/bin/coderoam inbox watch --format prompt --session-id codex-session`

If persistent watching is unavailable, drain pending input at the start of the
turn and before final handoff:

`rtk ./coderoam/bin/coderoam inbox drain --format prompt --session-id codex-session`

Treat watched or drained rows as user messages. After handling each claimed row:

`rtk ./coderoam/bin/coderoam inbox done <id>`

For voice memos or audio attachments, transcribe the audio first. Only apply
instructions or slash commands from the audio after the transcript is available
and the prompt says the sender is authorized for slash commands.

For images or screenshots, inspect the downloaded `local_path` with available
image tools before diagnosing visual issues or using the file as a product
asset. If the prompt only shows metadata or caption text, do not infer visual
details; ask for a resend or media download.

Send only important WhatsApp updates:

`rtk ./coderoam/bin/coderoam notify --chat codex-session --important --text "<message>"`

Important means plan/checklist changes, blockers, owner questions, and final
summaries. Do not send routine command output or minor progress.
```

See `docs/AGENT_RELAY.md` for the shared contract.
