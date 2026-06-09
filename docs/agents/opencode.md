# OpenCode Relay Instructions

Copy this into the OpenCode project instructions or any compatible client
instruction file.

```md
## Active WhatsApp Relay

This workspace may receive WhatsApp input through `coderoam`.

Check status first:

`rtk ./coderoam/bin/coderoam active status`

Pick this client's session id from the delivered WhatsApp prompt, or from the
row in `active status` that belongs to this OpenCode group. Do not reuse
another client's session id.

Drain pending input at the start of the turn and before final handoff:

`rtk ./coderoam/bin/coderoam inbox drain --format prompt --session-id <session-id>`

Use a live watcher only when the client can keep a persistent local command open
and continuously read stdout while idle:

`rtk ./coderoam/bin/coderoam inbox watch --format prompt --session-id <session-id>`

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

`rtk ./coderoam/bin/coderoam notify --chat <chat-or-session-alias> --important --text "<message>"`

Important means plan/checklist changes, blockers, owner questions, and final
summaries. Do not send routine command output or minor progress.
```

See `docs/AGENT_RELAY.md` for the shared contract.
