# Gemini Relay Instructions

Copy this into `GEMINI.md` or the Gemini project/system instructions.

Commands below use `coderoam` as installed by Homebrew; from a source checkout,
use `./bin/coderoam` instead.

```md
## Active WhatsApp Relay

This workspace may receive WhatsApp input through `coderoam`.

At session start, check:

`coderoam active status`

Pick this client's session id from the delivered WhatsApp prompt, or from the
row in `active status` that belongs to this Gemini group. Do not reuse another
client's session id.

Drain pending input at turn start and handoff:

`coderoam inbox drain --format prompt --session-id <session-id>`

Use a live watcher only when Gemini can keep a long-running local command open
and continuously read stdout while idle:

`coderoam inbox watch --format prompt --session-id <session-id>`

Treat watched or drained inbox prompt blocks as user input. After handling a
claimed inbox row:

`coderoam inbox done <id>`

For voice memos or audio attachments, transcribe the audio first. Only apply
instructions or slash commands from the audio after the transcript is available
and the prompt says the sender is authorized for slash commands.

For images or screenshots, inspect the downloaded `local_path` with available
image tools before diagnosing visual issues or using the file as a product
asset. If the prompt only shows metadata or caption text, do not infer visual
details; ask for a resend or media download.

Send WhatsApp notifications only for important updates: plan/checklist changes,
blockers, questions that require the owner, or final summaries.

`coderoam notify --chat <chat-or-session-alias> --important --text "<message>"`

If the current turn came from a `coderoam` runner prompt with `Sender`,
`Chat`, and `Message`, the daemon already claimed that WhatsApp row. Do not mark
that row done manually.
```

See `docs/AGENT_RELAY.md` for the shared contract.

`coderoam runbook` also writes a condensed runbook into `GEMINI.md` between the
managed `<!-- coderoam:relay:start -->` and `<!-- coderoam:relay:end -->`
markers; keep those markers in place so `coderoam runbook` can update that
section without touching your own instructions.
