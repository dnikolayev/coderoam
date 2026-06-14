# Generic Agent Relay Instructions

Use this for any local AI client that can run shell commands and follow project
instructions.

Commands below use `coderoam` as installed by Homebrew; from a source checkout,
use `./bin/coderoam` instead. Pick one form and use it consistently in the
instruction file you hand to the agent.

```md
## Active WhatsApp Relay

This workspace may receive WhatsApp input through `coderoam`.

Required commands:

- Status:
  `coderoam active status`
- Pick this client's session id from the delivered WhatsApp prompt, or from the
  row in `active status` that belongs to this group. Do not reuse another
  client's session id.
- Each client needs its own clearly named WhatsApp group, alias, and session id.
  If a new lane is needed, create a new group with `coderoam active start
  --name "<Agent> Session" --alias <session-id> --session-id <session-id>
  --yes` instead of sharing an existing agent group.
- Drain at turn start:
  `coderoam inbox drain --format prompt --session-id <session-id>`
- Live watch, only when persistent stdout is continuously consumed:
  `coderoam inbox watch --format prompt --session-id <session-id>`
- Mark handled:
  `coderoam inbox done <id>`
- Important update:
  `coderoam notify --chat <session-id> --important --text "<message>"`

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

`coderoam runbook` also writes a condensed runbook into `CLAUDE.md`,
`AGENTS.md`, and `GEMINI.md` between the managed
`<!-- coderoam:relay:start -->` and `<!-- coderoam:relay:end -->` markers; keep
those markers in place so `coderoam runbook` can update that section without
touching your own instructions.
