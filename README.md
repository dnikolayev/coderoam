# coderoam

Run each AI coding session in its own mobile group chat.

`coderoam` is a local-first bridge between selected WhatsApp groups and local
CLI coding agents. It lets you continue a coding session from your phone, invite
other people into the session chat, and keep each work lane isolated in its own
group. It works with any CLI agent that accepts a prompt as an argument or on
stdin; Codex, Claude Code, Gemini, and OpenCode have built-in presets.

Community: [join the Discord server](https://discord.gg/kkV6ZmkRHA) for setup
help, ideas, and early user feedback.

Security and privacy: read [SECURITY.md](SECURITY.md) and
[PRIVACY.md](PRIVACY.md) before linking an account. All state is local: the
config, message database, and WhatsApp session files stay on your machine.

## Install

Copy-paste on macOS or Linux with [Homebrew](https://brew.sh) installed:

```sh
curl -fsSL https://raw.githubusercontent.com/dnikolayev/coderoam/main/scripts/install.sh | sh
```

That installs the latest stable tagged release and prints the short setup
guide. Without Homebrew, use a release archive or source build from the
platform section below. Pass `--head` only when you intentionally want the
moving `main` branch build.

If you prefer not to run a remote script, read
[`scripts/install.sh`](scripts/install.sh) and run the Homebrew commands in
[`docs/HOMEBREW.md`](docs/HOMEBREW.md) manually:

```sh
brew tap dnikolayev/coderoam https://github.com/dnikolayev/coderoam.git
brew install dnikolayev/coderoam/coderoam
```

Binary name convention: Homebrew installs put `coderoam` on PATH; a source
checkout builds `./bin/coderoam`. Examples in this README use `coderoam`.

## Quick Start

Most users need this flow:

```sh
coderoam setup
coderoam doctor
coderoam run
```

`coderoam setup` configures an agent, asks which WhatsApp numbers are allowed,
requires you to confirm those numbers exactly, links WhatsApp with QR if needed,
and creates a dedicated session group. By default the group name follows the
selected agent, such as "Codex Session" or "Claude Session", so parallel lanes
are visually distinct on your phone.

For docs, scripts, or a non-interactive preview:

```sh
coderoam setup --print --agent codex --workdir /path/to/workspace --session-id codex-session
```

Use one WhatsApp group per active coding session. Codex and Claude should have
different group chats and different session ids, for example `codex-session`
and `claude-session`; `coderoam` rejects active-session configs that cross those
wires.

## Common How-Tos

| I want to... | Start here |
| ------------ | ---------- |
| Install and create the first mobile coding chat | `coderoam setup` |
| Continue Codex from WhatsApp | `coderoam setup --agent codex --workdir /path/to/workspace --session-id codex-session` |
| Continue Claude from WhatsApp | `coderoam setup --agent claude --workdir /path/to/workspace --session-id claude-session` |
| Add a second isolated session | `coderoam active start --name "Claude Session" --participants "+15550001111" --alias claude-session --session-id claude-session --yes` |
| Check whether messages are queued | `coderoam active status` |
| Pull WhatsApp messages into the current terminal session | `coderoam inbox drain --format prompt --session-id <session-id>` |
| Send an important update back to the group | `coderoam notify --chat <session-id> --important --text "Update..."` |
| Enable voice notes, images, or screenshots | See [Media](#media-voice-notes-images-screenshots) |
| Debug a missing response | Start with `coderoam doctor`, then see [Troubleshooting](#troubleshooting) |

## Account risk: read this before installing

This project is a local personal automation bridge. It is not affiliated with,
endorsed by, or authorized by WhatsApp or Meta.

The default personal-account transport uses WhatsApp Web / linked-device behavior
through the unofficial `whatsmeow` library. This may violate WhatsApp/Meta terms,
may stop working at any time, and may place your account at risk. Use a dedicated
account and keep usage low-volume.

Do not use this project for bulk messaging, scraping, spam, surveillance,
stalkerware, or contacting people without consent.

## Prerequisites

- A dedicated WhatsApp account/number for the bridge. Do not link your personal
  account.
- Go matching the version in [`go.mod`](go.mod) (currently 1.26.x) - only when
  building from source. Homebrew and release-archive installs do not need Go.
- OPTIONAL: the agent CLIs you want to drive from WhatsApp - `codex`, `claude`,
  `gemini`, `opencode`. None of them is required to install or test the bridge,
  and any other CLI agent works through the generic runner wrapper.

## Platform Support

| Platform                    | Release binaries | Recommended install             |
| --------------------------- | ---------------- | ------------------------------- |
| macOS arm64 (Apple Silicon) | yes (tar.gz)     | Homebrew tap or install script  |
| macOS amd64 (Intel)         | yes (tar.gz)     | Homebrew tap or install script  |
| Linux amd64                 | yes (tar.gz)     | release archive or source build |
| Linux arm64                 | yes (tar.gz)     | release archive or source build |
| Windows amd64               | yes (zip)        | release archive or source build |

Release archives are published for every tagged release with checksums and an
SBOM. Other platforms build from source with the Go version in `go.mod`.

## Build From Source

```sh
go build -o bin/coderoam ./cmd/coderoam
go build -o bin/coderoam-transcribe ./cmd/coderoam-transcribe
go build -o bin/agent-runner ./examples/agent-runner
go build -o bin/echo-runner ./examples/echo-runner
go build -o bin/codex-runner ./examples/codex-runner
go build -o bin/claude-runner ./examples/claude-runner
```

## Setup Details

```sh
coderoam setup
```

`coderoam setup` is the friendly first-run wizard. It configures the selected
agent, asks which WhatsApp numbers are authorized, requires exact confirmation
of those numbers, links WhatsApp with QR if needed, and creates the dedicated
session group. Run it in an interactive terminal.

After setup, verify the result and start the bridge:

```sh
coderoam doctor
coderoam run
```

`coderoam doctor` checks the local config, profile database, WhatsApp login
state, and every configured runner command, and prints what is missing. Run it
first whenever something does not work.

For docs or automation, print the manual command guide instead of running the
wizard:

```sh
coderoam setup --print --agent codex --workdir /path/to/workspace --session-id <session-id>
```

See [docs/SETUP.md](docs/SETUP.md) for the full setup walkthrough and
[docs/HOMEBREW.md](docs/HOMEBREW.md) for the tap fallback, release workflow,
and Homebrew-core formula path.

Manual initialization is also available:

```sh
coderoam init
```

`init` is idempotent. If a config already exists, it keeps the existing local
settings, ensures the profile database exists, and points you back to
`coderoam setup` or `coderoam runners preset ...`. Use `--force` only when you
intend to reset local config.

## Configure a Runner

A runner is the local command that receives WhatsApp messages. Add any
executable directly:

```sh
coderoam runners add default \
  --mode process-once-json \
  --command /path/to/your/runner
```

The easier path is a built-in preset. Read-only presets exist for each
supported agent CLI:

```sh
coderoam runners preset codex --id default --workdir /path/to/workspace
coderoam runners preset claude --id default --workdir /path/to/workspace
coderoam runners preset gemini --id gemini --workdir /path/to/workspace
coderoam runners preset opencode --id opencode --workdir /path/to/workspace
```

Each agent also has a `*-code` variant (`codex-code`, `claude-code`,
`gemini-code`, `opencode-code`) that lets the agent edit files in the
workspace. Coding presets require an explicit `--yes`:

```sh
coderoam runners preset codex-code \
  --id default \
  --workdir /path/to/workspace \
  --yes
```

The `*-code` presets are the safer default for a dedicated WhatsApp group that
should keep working when no live watcher is attached. They post only important
updates back to WhatsApp by default.

For any other CLI that can accept a prompt as an argument or stdin, use the
generic `agent` preset (or `agent-code --yes` for agents that may edit files):

```sh
coderoam runners preset agent \
  --id my-agent \
  --workdir /path/to/workspace \
  --agent-command my-agent-cli \
  --agent-arg run
```

Claude presets require the local Claude CLI to be logged in first: run
`claude` and complete its `/login` flow.

Switch an allowlisted group to a configured runner at any time:

```sh
coderoam groups set-runner "<group-id>" codex-code
```

### Codex resume presets

To continue an existing Codex session from WhatsApp, use `codex-active`. This
resumes the most recent recorded Codex session and sends each WhatsApp message
as the next user turn. The active presets only post important updates back to
WhatsApp, such as plan/checklist changes, blockers, questions that need the
user, approval/input requests, or final summaries. This direct runner path
waits for Codex to finish a turn, so use the inbox relay below for long active
sessions that should not block WhatsApp:

```sh
coderoam runners preset codex-active \
  --id codex-active \
  --workdir /path/to/workspace \
  --yes

coderoam groups set-runner "<group-id>" codex-active
```

For a specific Codex session id, use the `codex-session` preset with
`--session-id "<codex-session-uuid>"` instead; that avoids accidentally
resuming another recent Codex session.

## Active Sessions: Continue This Session From WhatsApp

For a currently active agent session, prefer the inbox relay. This stores
WhatsApp input locally and lets the active session pull it between work
chunks:

```sh
coderoam active enable "<group-id>" \
  --alias <session-id> \
  --session-id <session-id>
coderoam inbox drain --format prompt --session-id <session-id>
coderoam inbox done 1
coderoam notify --chat <session-id> --important --text "Plan updated..."
```

Use `inbox watch` only with clients that continuously read watcher stdout while
idle. For API-style sessions, `inbox drain` is safer because a detached
watcher can claim a WhatsApp message and make it appear read before the prompt
reaches the active turn.

To start another parallel work lane, create a dedicated WhatsApp group and bind
it to a unique active session id. One concrete worked example, using Codex:

```sh
coderoam runners preset codex-code --id codex-code --workdir /path/to/workspace --yes
coderoam active start \
  --name "Codex Session" \
  --participants "+15550001111" \
  --alias codex-session \
  --session-id codex-session \
  --yes
coderoam inbox watch --format prompt --session-id codex-session
```

The same pattern works for every agent. Configure that agent's preset, then
start a lane with its own alias and session id:

```sh
coderoam runners preset <preset> --id <runner-id> --workdir /path/to/workspace --yes
coderoam active start \
  --name "<group name>" \
  --participants "<your-phone-number>" \
  --alias <session-id> \
  --session-id <session-id> \
  --yes
coderoam inbox watch --format prompt --session-id <session-id>
```

For example, Claude can use `claude-code` with session id `claude-session`
while Codex uses `codex-code` with `codex-session`, and Gemini or OpenCode
lanes follow the same shape. All lanes can use the same authorized owner, but
they must not share the same `active_session_id`; otherwise both clients read
from the same local inbox lane.

`active start` creates a dedicated WhatsApp group for that session and sends the
group invite link by direct message to the `--participants` list. Use
`--invite-to "<your-phone-number>"` to send the link somewhere else. The user
must open that WhatsApp link to enter the session chat when WhatsApp privacy
settings do not add them automatically.

`active start` leaves the fallback runner blank by default, so messages stay
queued for the live watcher. Pass `--runner <id>` only when you want the bridge
to use a configured fallback runner if no watcher is connected.

Groups created by `active start` are relay-managed. Each session should have its
own group, alias, and `active_session_id`. If a participant leaves that managed
group, the group is deleted, or only the bridge account remains, `coderoam
run` leaves/archives the chat on the linked WhatsApp device, disables and marks
the local group entry archived, and deletes per-chat operational rows such as
active inbox, active outbox, pending interactions, messages, invocations, and
outbox entries. The same alias/session is reactivated only by an explicit local
`active start`, which creates a fresh WhatsApp group and replaces the archived
config entry. Groups enabled manually with `active enable` are not
relay-managed and are not auto-archived unless the owner explicitly adopts an
existing dedicated relay group with `active enable <group-id> --managed`.
Archived relay-managed groups cannot be re-enabled; use `active start` to make a
fresh group.

The relay avoids running `codex exec resume` while another Codex turn is active.
`coderoam run` owns the WhatsApp connection and sends queued notifications
from the active outbox. Active inbox rows are tagged with a session id so
multiple active agent sessions do not claim each other's WhatsApp input. For
active-session groups, the bridge sends a WhatsApp read receipt only after
`coderoam inbox watch --session-id <session-id>`,
`coderoam inbox next --session-id <session-id>`, or
`coderoam inbox drain --session-id <session-id>` claims the message for that
active agent session. `inbox drain` is the safest turn-boundary path for
API-style clients because it also surfaces same-session rows that were already
claimed by a previous watcher. `inbox watch` keeps one exclusive local consumer
connected per session and emits claimed messages immediately; use it only when
that consumer continuously reads stdout. Use `--format jsonl` for
machine-readable local agent integrations. Media-only messages are queued with
local metadata/captions rather than downloaded by default.

When no live watcher is connected and the active-session group has a safe
fallback runner, the bridge waits briefly for related WhatsApp messages and
sends them to the runner as one combined turn. Configure this behavior with:

```toml
[active]
fallback_delay_seconds = 2
fallback_batch_limit = 8
ack_mode = "minimal" # minimal | verbose | off
```

The default `minimal` acknowledgement mode sends a compact status when a message
is queued without a live watcher or when fallback starts, and suppresses routine
"received" messages for healthy live watchers. Use `verbose` to restore
detailed `Received #...` messages, or `off` to suppress active-session
acknowledgements.

If no live watcher is connected and the active-session group has a safe runner
configured, the daemon uses that runner as an automatic fallback. Do not use an
active Codex resume runner, such as `codex-active` with `CODEX_RUNNER_RESUME`,
or a runner pinned to the same live session, such as `codex-session` with
`CODEX_RUNNER_SESSION_ID`, as automatic fallback: it can block the bridge or
claim a WhatsApp row without surfacing it in the open window. These runners stay
queued for `inbox watch`, `inbox next`, or `inbox drain` instead. Use
`codex-code` or another non-pinned runner when you want automatic background
WhatsApp replies.

To inspect the last routing decision for a chat, run:

```sh
coderoam explain-last --chat <group-alias-or-id>
```

This reports whether the last message was queued, ignored, blocked, batched, or
sent to a fallback runner, with redacted sender/chat identifiers.

Reusable client instructions live in:

- `docs/SETUP.md`
- `docs/HOMEBREW.md`
- `docs/AGENT_RELAY.md`
- `docs/agents/codex.md`
- `docs/agents/claude.md`
- `docs/agents/gemini.md`
- `docs/agents/opencode.md`
- `docs/agents/generic.md`
- `skills/whatsapp-relay/`

`coderoam setup --agent auto --workdir /path/to/workspace` detects installed
client commands (`codex`, `claude`, `gemini`, `opencode`) and prints the matching
`runners preset` command plus the instruction file to copy into that client.

## Media: Voice Notes, Images, Screenshots

To let agents inspect voice notes or other media, explicitly enable local media
download:

```toml
[transport]
download_media = true
transcribe_audio = true
audio_transcribe_command = "/path/to/coderoam-transcribe --model /path/to/ggml-base.en.bin"
audio_transcribe_timeout_seconds = 120
```

Downloaded files stay in the local profile media directory, and prompt output
includes `local_path` lines for agent tooling. If an audio attachment only shows
metadata, the file was not downloaded or the download failed.

Images and screenshots use the same `local_path` flow. Agents should inspect the
downloaded file with available image tools before diagnosing UI issues, matching
a screenshot, or copying the file into a web/product feature. If only metadata or
a caption is present, the visual content is unavailable and the agent should ask
for a resend or request `transport.download_media = true` instead of guessing.

When `transcribe_audio` is enabled, coderoam runs the configured local command
after download and stores stdout as `media[].transcript`, so runners receive the
transcript directly in JSON and prompt output. The command receives the audio
path as the final argument unless it contains a `{path}` placeholder.

Codex and Claude runner wrappers can also transcribe downloaded voice/audio files
as a fallback. Set `CODEX_RUNNER_AUDIO_TRANSCRIBE_COMMAND` or
`CLAUDE_RUNNER_AUDIO_TRANSCRIBE_COMMAND` to a local command; stdout is added to
the prompt as `transcript:`. Agents must transcribe voice memos before treating
audio content as instructions or slash commands; if transcription is unavailable,
ask for text instead.

## Important-only notifications

The Codex, Claude, and generic agent wrappers can suppress routine assistant
output. When important-only mode is enabled, the wrapper tells the assistant to
reply only for important WhatsApp updates: plan/checklist changes, blockers,
questions that need the user, approval/input requests, or final summaries. If
there is no important update, the assistant is instructed to return the exact
ignore marker. The wrapper converts that marker into a runner `ignore` action,
and `coderoam` sends no WhatsApp message.

Enabled by built-in presets:

- `codex-active`
- `codex-code`
- `codex-session`
- `claude`
- `claude-code`
- `opencode`
- `opencode-code`
- `gemini`
- `gemini-code`
- `agent`
- `agent-code`

Optional runner environment variables:

- `CODEX_RUNNER_WORKDIR`: working directory for `codex exec`.
- `CODEX_RUNNER_MODEL`: model passed to Codex.
- `CODEX_RUNNER_SANDBOX`: Codex sandbox, default `read-only`.
- `CODEX_RUNNER_RESUME`: set to `last` to call `codex exec resume --last`.
- `CODEX_RUNNER_SESSION_ID`: resume a specific Codex session id.
- `CODEX_RUNNER_RESUME_ALL`: include all cwd sessions when resolving `--last`.
- `CODEX_RUNNER_SYSTEM_PROMPT`: instruction prepended to WhatsApp messages.
- `CODEX_RUNNER_APPROVAL_POLICY`: optional Codex approval policy; coding presets use `never`.
- `CODEX_RUNNER_IMPORTANT_ONLY`: set to `true` to suppress routine WhatsApp replies.
- `CODEX_RUNNER_IGNORE_MARKER`: exact marker that means "send no WhatsApp reply"; default `[[coderoam-ignore]]`.
- `CODEX_RUNNER_AUDIO_TRANSCRIBE_COMMAND`: optional local command that receives a downloaded audio path and writes the transcript to stdout.
- `CODEX_RUNNER_AUDIO_TRANSCRIBE_TIMEOUT_SECONDS`: optional audio transcription timeout; default `120`.
- `CLAUDE_RUNNER_WORKDIR`: working directory for `claude -p`.
- `CLAUDE_RUNNER_MODEL`: model passed to Claude.
- `CLAUDE_RUNNER_PERMISSION_MODE`: Claude permission mode, default `default`.
- `CLAUDE_RUNNER_SYSTEM_PROMPT`: instruction prepended to WhatsApp messages.
- `CLAUDE_RUNNER_IMPORTANT_ONLY`: set to `true` to suppress routine WhatsApp replies.
- `CLAUDE_RUNNER_IGNORE_MARKER`: exact marker that means "send no WhatsApp reply"; default `[[coderoam-ignore]]`.
- `CLAUDE_RUNNER_AUDIO_TRANSCRIBE_COMMAND`: optional local command that receives a downloaded audio path and writes the transcript to stdout.
- `CLAUDE_RUNNER_AUDIO_TRANSCRIBE_TIMEOUT_SECONDS`: optional audio transcription timeout; default `120`.
- `AGENT_RUNNER_COMMAND`: executable for the generic wrapper, for example `opencode`, `gemini`, or another CLI.
- `AGENT_RUNNER_ARGS_JSON`: JSON array of arguments passed before the prompt, for example `["run"]` or `["-p"]`.
- `AGENT_RUNNER_ARGS`: simpler whitespace-split argument fallback when JSON is not needed.
- `AGENT_RUNNER_PROMPT_MODE`: `arg` to append the prompt as the final argument, or `stdin` to pipe the prompt.
- `AGENT_RUNNER_WORKDIR`: working directory for the generic agent process.
- `AGENT_RUNNER_SYSTEM_PROMPT`: instruction prepended to WhatsApp messages.
- `AGENT_RUNNER_IMPORTANT_ONLY`: set to `true` to suppress routine WhatsApp replies.
- `AGENT_RUNNER_IGNORE_MARKER`: exact marker that means "send no WhatsApp reply"; default `[[coderoam-ignore]]`.
- `AGENT_RUNNER_AUDIO_TRANSCRIBE_COMMAND`: optional local command that receives a downloaded audio path and writes the transcript to stdout.
- `AGENT_RUNNER_AUDIO_TRANSCRIBE_TIMEOUT_SECONDS`: optional audio transcription timeout; default `120`.

## Active WhatsApp Slash Commands

When using active-session mode, WhatsApp messages are queued into the local
inbox for the currently active agent session. Slash commands such as `/goal ...`
are not executed by the daemon itself. They are highlighted by
`coderoam inbox next --format prompt` and
`coderoam inbox drain --format prompt --session-id <session-id>` so the active
agent session treats them as explicit user commands after it claims the inbox
row.

This avoids running a second competing agent process while one turn is already
active.

For coding or goal-style workflows, enable sender allowlisting so only trusted
WhatsApp senders can instruct local agents. The setup wizard enables this by
default after you confirm authorized phone numbers:

```toml
[security]
require_sender_allowlist = true
admin_sender_ids = ["<trusted-sender>@lid"]
allowed_sender_ids = ["<trusted-sender>@lid"]
```

The prompt output labels senders as authorized or blocked based on these lists.
If WhatsApp reports a new `@lid` sender ID on the first message, approve it only
after confirming the person locally:

```sh
coderoam senders allow "<sender-id>" --admin
```

If a voice memo or audio attachment may contain a slash command, the active
agent must transcribe the audio first and only apply the command after the
transcript is available and the sender is authorized.

## WhatsApp Login

`coderoam setup` handles login for you. To log in manually:

```sh
coderoam auth login --profile bot --qr
```

By default, QR login also writes and opens a PNG image:

```text
~/Library/Application Support/coderoam/profiles/bot/whatsapp-session.sqlite3.qr.png
```

You can choose the QR image path or disable auto-open:

```sh
coderoam auth login --qr --qr-image /tmp/coderoam-qr.png --open-qr=false
```

Pair-code login is also available if your WhatsApp account supports it:

```sh
coderoam auth login --pair-code "<your-phone-number>"
```

## Groups

List groups:

```sh
coderoam chats list --groups
```

Allow one group:

```sh
coderoam groups allow "<group-id>" --alias "test-group"
```

If the bridge account is not in any group yet, you can create a small local test
group from the terminal:

```sh
coderoam groups create \
  --name "Codex Bridge" \
  --participants "<your-phone-number>" \
  --alias "codex" \
  --runner default \
  --yes
```

Group creation is disabled unless `--yes` is present and can only be triggered
from the local CLI.

If WhatsApp creates the group but cannot add the participant because of privacy
settings, send an invite link:

```sh
coderoam groups send-invite "<group-id>" --to "<your-phone-number>"
```

Run the bridge:

```sh
coderoam run
```

When someone in the allowlisted group sends:

```text
@bridge ping
```

the bridge sends `ping` to the configured local runner and posts the runner reply back to that group.

## Local Dry Run

The fake transport allows testing the route without WhatsApp:

```sh
coderoam --config /tmp/coderoam.toml init
coderoam --config /tmp/coderoam.toml runners add default \
  --mode process-once-json \
  --command "$(pwd)/bin/echo-runner"
coderoam --config /tmp/coderoam.toml groups allow "fake-group@g.us" --alias fake
coderoam --config /tmp/coderoam.toml test-route \
  --chat "fake-group@g.us" \
  --sender "fake-sender@s.whatsapp.net" \
  --text "@bridge ping"
```

## Runner Protocol

For `process-once-json`, the runner receives one JSON object on stdin:

```json
{
  "version": "1.0",
  "request_id": "req_...",
  "profile_id": "bot",
  "text": "ping",
  "chat_id": "<group-id>",
  "sender_id": "<sender-id>"
}
```

The runner returns:

```json
{
  "version": "1.0",
  "actions": [
    {
      "type": "reply",
      "text": "pong"
    }
  ]
}
```

For persistent agents, use `process-jsonl`. The bridge starts the configured
command once per chat/session, sends one request JSON object per line, and reads
one response JSON object per line:

```toml
[runner.default]
mode = "process-jsonl"
command = "/usr/local/bin/my-chat-cli"
args = ["--stdio"]
restart_on_crash = true
max_restarts_per_hour = 10
```

The response can be the same structured response shown above, or a compact JSONL
event such as:

```json
{"type":"reply","request_id":"req_...","text":"pong"}
```

Interactive runner actions are also supported. Use `request_choice` when the
agent needs the owner to select from options:

```json
{
  "version": "1.0",
  "actions": [
    {
      "type": "request_choice",
      "text": "How should I continue?",
      "options": ["Plan first", "Apply changes", "Stop"],
      "expires_seconds": 900
    }
  ]
}
```

The bridge stores a pending interaction and sends a numbered WhatsApp menu.
Those numbers are shortcuts, not a forced UI. The owner can reply with `1`,
`2`, the option text, clear natural language such as `privacy review` or `CI
please`, or any custom free-text answer. Voice notes work too when local media
download and transcription are enabled: the transcript is routed back to the
same runner without requiring the normal trigger prefix.

Local approval controls:

```sh
coderoam approvals list
coderoam approvals show <id>
coderoam approvals approve <id>
coderoam approvals reject <id>
```

## Sending a One-Off Message

After WhatsApp login succeeds, this sends from the linked bridge account:

```sh
coderoam send --to "<your-phone-number>" --text "coderoam is ready"
```

## Storage

Default paths per OS:

macOS:

- config: `~/Library/Application Support/coderoam/config.toml`
- app database: `~/Library/Application Support/coderoam/profiles/<profile>/coderoam.sqlite3`
- WhatsApp session database: `~/Library/Application Support/coderoam/profiles/<profile>/whatsapp-session.sqlite3`
- logs: `~/Library/Logs/coderoam/coderoam.log`

Linux (respects `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, and `XDG_STATE_HOME` when
set):

- config: `~/.config/coderoam/config.toml`
- app and session databases: `~/.local/share/coderoam/profiles/<profile>/`
- logs: `~/.local/state/coderoam/coderoam.log`

Windows:

- config: `%APPDATA%\coderoam\config.toml`
- app and session databases: `%APPDATA%\coderoam\profiles\<profile>\`
- logs: `%LOCALAPPDATA%\coderoam\logs\coderoam.log`

WhatsApp session material is stored outside the repository. Do not commit profile data or session databases.

## Troubleshooting

**The QR code expired or login timed out.** `coderoam auth login --qr` keeps
the QR login scannable for 5 minutes, requesting fresh codes as WhatsApp
rotates each one roughly every 20 seconds. If the window passes, run the
command again and scan promptly from WhatsApp > Settings > Linked Devices.

**Runner command not found.** `coderoam doctor` prints `runner.<id>: command
not found` with the exact executable it looked for. Install that agent CLI
(`codex`, `claude`, `gemini`, `opencode`, or your own), or reconfigure the
runner with `coderoam runners preset ...`/`coderoam runners add ...` pointing
at an existing command. If `coderoam` itself is not found, remember the binary
name convention above: source checkouts run `./bin/coderoam`.

**Where is my data stored?** Everything stays local; see
[Storage](#storage) for the per-OS config, database, session, and log paths.

**How do I unlink or log out the WhatsApp session?** Run `coderoam auth
logout`, or remove the linked device from the phone in WhatsApp > Settings >
Linked Devices. If WhatsApp reports `401: logged out from another device`,
reset the local session files before linking again:

```sh
coderoam auth reset --yes
coderoam auth login --qr
```

This deletes only the WhatsApp session files for the active profile. It does
not delete the app config, allowed groups, runner config, or message database.

**`coderoam setup` hangs.** The wizard is interactive and waits for input; run
it in a real terminal, not through a script or non-interactive shell. If it
sits on the QR step, the 5-minute login window may have passed - rerun and
scan promptly. `coderoam setup --print ...` prints the manual commands without
any interaction, and `coderoam doctor` shows which setup step is incomplete.

**"a coderoam daemon is already running".** Only one `coderoam run` daemon may
own the messenger connection per machine; every agent session is a consumer of
that daemon, so this message usually means everything is fine. Use
`coderoam run --takeover` only to deliberately replace the running daemon.

**Something else?** `coderoam doctor`, `coderoam status`, `coderoam health`,
and `coderoam logs tail` are the first diagnostics to run. See
[SUPPORT.md](SUPPORT.md) for what to include when asking for help.

## Status

Current implementation: v0.1.15 parallel mobile agent sessions.

Project maturity: early MVP. The public API, config shape, database schema, and
runner protocol can still change before v1.0.

Implemented:

- Go CLI binary named `coderoam`.
- Local config with conservative defaults.
- WhatsApp Web transport using whatsmeow with QR and pair-code login.
- Group allowlisting.
- Prefix trigger, default `@bridge`.
- Process-once text and process-once JSON runners.
- Persistent JSONL runners.
- SQLite persistence for profiles, chats, senders, messages, runner invocations, outbox, and audit events.
- Incoming message deduplication by WhatsApp message ID.
- Fake transport and `test-route` for local testing without WhatsApp.
- Kill switch through `coderoam pause`, `resume`, and `kill`.
- Direct `send --to ... --text ...` helper for one-off status messages from the linked account.
- Local-confirmed group creation and invite-link sending.
- Built-in Codex and Claude runner presets, including explicit coding modes.
- Important-only WhatsApp notification mode for active Codex and Claude runners.
- Active-session inbox relay for continuing the current Codex chat from WhatsApp without spawning a competing Codex process.
- One-command active-session group creation for parallel Codex work lanes.
- Local approval/input queue commands.
- Cross-platform active-session watcher service definitions.
- Tagged-release workflow with checksums, SBOM, and optional macOS signing/notarization secrets.
- Reserved Telegram, Slack, and Google Chat transport names with explicit not-yet-implemented status/errors.

Partially implemented:

- Media messages are detected and queued as local metadata/caption text.
  Downloading media files is disabled by default and available only by explicit
  local configuration.

Not implemented yet:

- Participant management beyond create/invite flows.
- Encrypted session storage integration with Keychain/libsecret/DPAPI.
- Full Telegram, Slack, and Google Chat message adapters.
- Homebrew core submission.

## Open-Source Readiness

Before publishing a repository or release, check:

- `README.md` explains the local/personal-use scope and unofficial transport
  risk.
- `LICENSE`, `NOTICE`, `THIRD_PARTY_LICENSES.md`, and
  `licenses/GPL-3.0.txt` are present.
- `SECURITY.md`, `PRIVACY.md`, `SUPPORT.md`, `CONTRIBUTING.md`, and
  `CODE_OF_CONDUCT.md` are present.
- Active-session coding workflows use `require_sender_allowlist = true`.
- `.gitignore` excludes `bin/`, local profile data, logs, session databases,
  QR images, and other generated files.
- CI runs `go test ./...`.
- Release artifacts include checksums, third-party license notices, GPL-3.0 text
  for the WhatsApp transport dependency graph, and the tagged source archive.

## Safety Defaults

- New groups are ignored until explicitly allowlisted.
- Direct messages are ignored by the router unless configured through a group/chat allowlist path.
- The default trigger is `@bridge`; always-on mode is disabled.
- The app never shells out with WhatsApp text. It invokes a fixed configured executable and sends message content on stdin.
- The kill switch file stops message processing: `coderoam pause` creates it and `coderoam resume` removes it.
