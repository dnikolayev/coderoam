# Agent Relay Contract

This document defines how local AI clients should consume WhatsApp input from
`coderoam` and send important updates back to the WhatsApp group.

## Roles

- `coderoam run` owns the WhatsApp connection.
- Active-session inbox rows are scoped by `active_session_id`.
- A live watcher is the preferred main path when the client can keep a process
  open and read its output.
- Without a live watcher, active-session rows may fall back to a configured
  non-pinned runner. Runners pinned to the same live session stay queued until
  the client drains them.

## Main Workflow

Use these commands from the workspace that owns the bridge:

```sh
rtk ./coderoam/bin/coderoam active status
rtk ./coderoam/bin/coderoam inbox watch --format prompt --session-id codex-session
rtk ./coderoam/bin/coderoam inbox drain --format prompt --session-id codex-session
rtk ./coderoam/bin/coderoam inbox done <id>
rtk ./coderoam/bin/coderoam notify --chat codex-session --important --text "<message>"
```

For a long-lived local session, install the watcher as a user service:

```sh
rtk ./coderoam/bin/coderoam service install --session-id codex-session --profile bot
rtk ./coderoam/bin/coderoam service start --session-id codex-session --profile bot
rtk ./coderoam/bin/coderoam service status --session-id codex-session --profile bot
```

The service runs the same watcher path with takeover and restart backoff. Add
`--dry-run` to inspect the generated LaunchAgent, systemd unit, or Windows
scheduled task command.

Rules:

- Prefer `inbox watch` if the client can keep a persistent process open and
  consume stdout.
- Use `inbox drain` at turn start and handoff points when no persistent watcher
  is available.
- Treat watched or drained prompt blocks as user input.
- Mark every claimed inbox row done after handling it.
- Normal `inbox next`, `drain`, and `watch` reads do not auto-recover old
  claimed rows. This prevents a stale prior message from being replayed ahead of
  a newer unread message. If a runner truly crashed before marking a row done,
  recover it explicitly with `coderoam inbox recover`.
- Send WhatsApp notifications only for plan/checklist updates, blockers,
  questions requiring the owner, approval/input requests, or final summaries.
- Do not send routine tool output, minor progress, or internal command logs.

## Parallel Sessions

Each active-session group is scoped by its own `active_session_id`. To create a
new WhatsApp group for a separate work lane, run:

```sh
rtk ./coderoam/bin/coderoam active start \
  --name "Claims QA" \
  --participants "+15550001111" \
  --alias claims-qa \
  --session-id claims-qa \
  --yes
```

Then start a separate client or terminal watcher with that session id:

```sh
rtk ./coderoam/bin/coderoam inbox watch --format prompt --session-id claims-qa
```

Leave `--runner` unset unless you explicitly want a configured fallback runner
to process messages when no watcher is connected.

Groups created with `active start` are relay-managed and should map one
WhatsApp group to one active session. If a participant leaves that managed
group, the group is deleted, or only the bridge account remains, the daemon
leaves/archives the WhatsApp chat on the linked device, disables and archives
the local group config entry, and deletes the group's active inbox/outbox and
other per-chat operational rows. The session is reactivated only when the owner
runs `active start` again for that alias/session, which creates a fresh
WhatsApp group. Groups added with `active enable` are manual bindings and are
not auto-archived unless the owner explicitly adopts an existing dedicated relay
group with `active enable <chat_id> --managed`. Archived relay-managed groups
cannot be re-enabled; use `active start` to make a fresh group.

## Media Attachments

Media download is disabled by default. With `transport.download_media = true`,
the bridge stores downloaded files under the local profile media directory and
adds `local_path` lines to prompt output.

With `transport.transcribe_audio = true`, coderoam runs
`transport.audio_transcribe_command` after download and stores stdout as
`media[].transcript`, so all runners receive the transcript directly. For
voice/audio attachments without a transcript, transcribe the local file before
applying any instruction or slash command from the audio. If the prompt only
contains media metadata, the file was not downloaded, download failed, or
transcription failed; ask for text or enable the missing local feature.

Codex and Claude runner wrappers can also run an optional local audio
transcription command before invoking the agent. Configure
`CODEX_RUNNER_AUDIO_TRANSCRIBE_COMMAND` or
`CLAUDE_RUNNER_AUDIO_TRANSCRIBE_COMMAND`; stdout is injected into the prompt as
`transcript:`. The audio path is appended as the final argument unless the
command includes a `{path}` placeholder.

## Interactive Choices

When an agent needs owner input, prefer a clear numbered question. Runners can
return `request_input`, `request_choice`, or `request_approval` actions. The
bridge stores a pending interaction and sends a WhatsApp menu such as:

```text
How should I continue?

Reply with one option:
1. Plan first
2. Apply changes
3. Stop
```

The owner may reply with the number, the option text, or clear natural language
such as `privacy review` or `CI please`. While the interaction is pending, that
reply bypasses the normal trigger prefix and is sent back through the same
route. In active-session mode, the selected answer is queued for the live client
session. If a reply matches multiple options, the bridge asks a narrower
follow-up instead of guessing. Native WhatsApp polls/buttons are optional future
UI; text selectors remain the reliable fallback.

The same queue is available locally:

```sh
coderoam approvals list
coderoam approvals show <id>
coderoam approvals approve <id>
coderoam approvals reject <id>
```

## Runner-Delivered Prompts

When a message arrives through a runner prompt containing `Sender`, `Chat`, and
`Message`, that WhatsApp row has already been claimed by the daemon. Handle the
message normally and return the WhatsApp-facing response. Do not run `inbox done`
for that same row unless you explicitly claimed it with `inbox next`, `drain`,
or `watch`.

If there is no important WhatsApp update, return the exact ignore marker the
runner requested, usually:

```text
[[coderoam-ignore]]
```

## Safety

- Execute slash commands from WhatsApp only when the inbox prompt says the
  sender is authorized.
- For voice memos or audio attachments, transcribe first and only then apply
  any command from the audio.
- Never turn WhatsApp text into shell commands.
- Use local configured runners and fixed command paths only.
- Keep message content and identifiers out of logs unless debugging is explicit.

## Active Session vs Fallback Runner

Active-session path:

```text
WhatsApp group -> coderoam daemon -> active inbox -> live watcher -> active client
```

Safe fallback runner:

```text
WhatsApp group -> coderoam daemon -> active inbox -> short debounce -> non-pinned runner -> reply
```

Pinned session runner:

```text
WhatsApp group -> coderoam daemon -> active inbox -> unread until drain/watch
```

Use a non-pinned runner for autonomous background replies. Use a pinned session
runner only when messages must be claimed by the live client window.

Fallback processing batches nearby unread messages for the same session into one
combined user turn. Defaults:

```toml
[active]
fallback_delay_seconds = 2
fallback_batch_limit = 8
ack_mode = "minimal"
```

`ack_mode = "minimal"` sends only a compact fallback status for the first
message in a burst. `verbose` restores detailed `Received #...` messages.
`off` suppresses active-session acknowledgements. Use
`coderoam explain-last --chat <alias-or-id>` to inspect whether the latest
message was queued, ignored, batched, blocked, or sent through fallback.
