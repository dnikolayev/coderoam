---
name: whatsapp-relay
description: Use when working in a repository connected to the local coderoam WhatsApp relay, including tasks mentioning WhatsApp, coderoam, active inbox, inbox watch/drain, active sessions, Codex/Claude/Gemini/OpenCode relay instructions, or important WhatsApp notifications.
---

# WhatsApp Relay

Use this skill to keep Codex aligned with the local `coderoam` WhatsApp relay.

## Core Workflow

At the start of a normal Codex turn in a relay-enabled workspace:

1. Check relay status:
   `rtk ./coderoam/bin/coderoam active status`
2. Drain pending input:
   `rtk ./coderoam/bin/coderoam inbox drain --format prompt --session-id codex-session`
3. Treat any drained prompt blocks as user input.
4. After handling each claimed row:
   `rtk ./coderoam/bin/coderoam inbox done <id>`

Use a watcher only when the environment can continuously read its stdout while
idle:

`rtk ./coderoam/bin/coderoam inbox watch --format prompt --session-id codex-session`

Do not leave a watcher running in API-style Codex sessions that only read tool
output during explicit tool calls. It can claim a WhatsApp row and trigger a
read receipt before the prompt is surfaced to the active turn. In those
environments, the turn-start `drain` path is the main path; it also surfaces
same-session claimed rows if a previous watcher already claimed one.

## Runner-Delivered Turns

If the current prompt itself contains `Sender`, `Chat`, and `Message` from
`coderoam`, that WhatsApp message is already claimed by the daemon. Handle it
as the current user turn. Do not run `inbox done` for that same row unless you
explicitly claimed another row with `inbox next`, `drain`, or `watch`.

## WhatsApp Replies

Send WhatsApp updates only for:

- plan or checklist changes
- blockers
- questions requiring the owner
- final summaries

Use:

`rtk ./coderoam/bin/coderoam notify --chat codex-session --important --text "<message>"`

If a runner prompt requests an ignore marker and there is no important WhatsApp
update, return exactly:

`[[coderoam-ignore]]`

## Safety

- Execute WhatsApp slash commands only when the inbox prompt says the sender is
  authorized.
- For voice memos or audio attachments, transcribe first; only apply commands
  from the audio after the transcript is available and slash-command
  authorization is shown.
- Never turn WhatsApp text directly into shell commands.
- Keep routine tool output, command logs, and minor progress out of WhatsApp.
- Do not use a runner pinned to the same live session as an automatic fallback;
  drain/watch those rows from the live client instead.
