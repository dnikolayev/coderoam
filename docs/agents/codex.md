# Codex Relay Instructions

Use this as the relay setup text for Codex sessions that should consume
WhatsApp input through `coderoam`.

```md
## Active WhatsApp Relay

This workspace may receive WhatsApp input through `coderoam`.

`coderoam init` is idempotent. If it reports that a config already exists, treat
the workspace as already initialized and continue with `coderoam setup`,
`coderoam doctor`, or the relevant `coderoam runners preset ...` command. Do
not use `--force` unless the owner explicitly asks to reset local config.

At the start of every Codex turn:

1. Check relay status:
   `rtk ./coderoam/bin/coderoam active status`
2. Pick this client's session id from the delivered WhatsApp prompt, or from
   the row in `active status` that belongs to this Codex group. Do not reuse
   another client's session id.
3. Drain pending input:
   `rtk ./coderoam/bin/coderoam inbox drain --format prompt --session-id <session-id>`
4. Treat drained prompt blocks as user input.
5. After handling each claimed inbox item:
   `rtk ./coderoam/bin/coderoam inbox done <id>`

Do not leave `inbox watch` running in a Codex API/tool session unless this
Codex client can continuously consume that process output while idle. A watcher
that is running but unread can claim WhatsApp rows and make them appear read
before the prompt reaches the active Codex turn. Use `drain` at turn start for
this environment. If `drain` reports an already claimed row, handle it and mark
it done, or requeue it if another consumer should handle it.

For voice memos or audio attachments, transcribe the audio first. Only apply
instructions or slash commands from the audio after the transcript is available
and the prompt says the sender is authorized for slash commands.

For images or screenshots, inspect the downloaded `local_path` with available
image tools before diagnosing visual issues or using the file as a product
asset. If the prompt only shows metadata or caption text, do not infer visual
details; ask for a resend or media download.

When this turn itself was delivered by a `coderoam` runner prompt containing
`Sender`, `Chat`, and `Message`, treat that message as already claimed by the
daemon. Do not mark it done manually unless you claimed another inbox row.

Send WhatsApp updates only for plan/checklist changes, blockers, questions for
the owner, or final summaries:

`rtk ./coderoam/bin/coderoam notify --chat <chat-or-session-alias> --important --text "<message>"`

If the runner prompt tells you to return an ignore marker and there is no
important WhatsApp update, return exactly:

`[[coderoam-ignore]]`
```

See `docs/AGENT_RELAY.md` for the shared contract.
