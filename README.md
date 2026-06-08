# coderoam

Run each AI coding session in its own mobile group chat.

`coderoam` is a local-first bridge between selected WhatsApp groups and local
CLI coding agents. It lets you continue a coding session from your phone, invite
other people into the session chat, and keep each work lane isolated in its own
group.

This project is a local personal automation bridge. It is not affiliated with,
endorsed by, or authorized by WhatsApp or Meta.

The default personal-account transport uses WhatsApp Web / linked-device behavior
through the unofficial `whatsmeow` library. This may violate WhatsApp/Meta terms,
may stop working at any time, and may place your account at risk. Use a dedicated
account and keep usage low-volume.

Do not use this project for bulk messaging, scraping, spam, surveillance,
stalkerware, or contacting people without consent.

## Status

Current implementation: v0.1-style local text/JSON bridge foundation.

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

## Quick Start

Prerequisites:

- Go matching the version in `go.mod`.
- A dedicated WhatsApp account/number for the bridge.
- A local terminal runner, or one of the included example runners.

Build:

```sh
go build -o bin/coderoam ./cmd/coderoam
go build -o bin/coderoam-transcribe ./cmd/coderoam-transcribe
go build -o bin/agent-runner ./examples/agent-runner
go build -o bin/echo-runner ./examples/echo-runner
go build -o bin/codex-runner ./examples/codex-runner
go build -o bin/claude-runner ./examples/claude-runner
```

Homebrew tap install:

```sh
brew tap dnikolayev/coderoam https://github.com/dnikolayev/coderoam.git
brew install --HEAD dnikolayev/coderoam/coderoam
coderoam setup
```

See [docs/HOMEBREW.md](docs/HOMEBREW.md) for the tap fallback, release
workflow, and Homebrew-core formula path.

Initialize local config:

```sh
bin/coderoam init
```

Configure a runner:

```sh
bin/coderoam runners add default \
  --mode process-once-json \
  --command /path/to/your/runner
```

To connect WhatsApp messages to Codex CLI, use the included `codex-runner` wrapper:

```sh
bin/coderoam runners add default \
  --mode process-once-json \
  --command "$(pwd)/bin/codex-runner"
```

The easier path is a built-in preset:

```sh
bin/coderoam runners preset codex --id default --workdir /path/to/workspace
```

To let Codex edit files in that workspace from WhatsApp, explicitly choose the coding preset:

```sh
bin/coderoam runners preset codex-code \
  --id default \
  --workdir /path/to/workspace \
  --yes
```

To continue an existing Codex session from WhatsApp, use `codex-active`.
This resumes the most recent recorded Codex session and sends each WhatsApp
message as the next user turn. The active presets only post important updates
back to WhatsApp, such as plan/checklist changes, blockers, questions that need
the user, approval/input requests, or final summaries:

```sh
bin/coderoam runners preset codex-active \
  --id codex-active \
  --workdir /path/to/workspace \
  --yes

bin/coderoam groups set-runner "1203630xxxxx@g.us" codex-active
```

For a specific Codex session id:

```sh
bin/coderoam runners preset codex-session \
  --id codex-session \
  --workdir /path/to/workspace \
  --session-id "019e..." \
  --yes

bin/coderoam groups set-runner "1203630xxxxx@g.us" codex-session
```

Use a specific session id if you need to avoid accidentally resuming another
recent Codex session.

If you installed through Homebrew, replace `bin/coderoam` with `coderoam` in the
commands below.

For a currently active Codex session, prefer the inbox relay. This stores
WhatsApp input locally and lets the active Codex session pull it between work
chunks:

```sh
bin/coderoam active enable "1203630xxxxx@g.us" \
  --alias codex-session \
  --session-id codex-session
bin/coderoam groups set-runner "1203630xxxxx@g.us" codex-active
bin/coderoam inbox watch --format prompt --session-id codex-session
bin/coderoam inbox done 1
bin/coderoam notify --chat codex-session --important --text "Plan updated..."
```

To start another parallel work lane, create a dedicated WhatsApp group and bind
it to a unique active session id:

```sh
bin/coderoam active start \
  --name "Claims QA" \
  --participants "+15550001111" \
  --alias claims-qa \
  --session-id claims-qa \
  --yes
```

Then run a watcher for that session id in the Codex window that should own that
work lane:

```sh
bin/coderoam inbox watch --format prompt --session-id claims-qa
```

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
existing dedicated relay group with `active enable <chat_id> --managed`.
Archived relay-managed groups cannot be re-enabled; use `active start` to make a
fresh group.

The relay avoids running `codex exec resume` while another Codex turn is active.
`coderoam run` owns the WhatsApp connection and sends queued notifications
from the active outbox. Active inbox rows are tagged with a session id so
multiple active agent sessions do not claim each other's WhatsApp input. For
active-session groups, the bridge sends a WhatsApp read receipt only after
`coderoam inbox watch --session-id <id>`,
`coderoam inbox next --session-id <id>`, or
`coderoam inbox drain --session-id <id>` claims the message for that active
agent session. `inbox watch` keeps one exclusive local consumer connected per
session and emits claimed messages immediately; use `--format jsonl` for
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

The default `minimal` acknowledgement mode sends one short fallback status
message per burst and suppresses routine "received" messages for live watchers
or groups with no safe fallback runner. Use `verbose` to restore detailed
`Received #...` messages, or `off` to suppress active-session acknowledgements.

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

If no live watcher is connected and the active-session group has a safe runner
configured, the daemon uses that runner as an automatic fallback. Do not use a
runner pinned to the same live session, such as a `codex-session` preset with
`CODEX_RUNNER_SESSION_ID`, as an automatic fallback: it can claim a WhatsApp row
without surfacing it in the open window. Pinned session runners stay queued for
`inbox watch`, `inbox next`, or `inbox drain` instead. Use `codex-code` or
another non-pinned runner when you want automatic background WhatsApp replies.

To inspect the last routing decision for a chat, run:

```sh
bin/coderoam explain-last --chat codex-session
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

Claude Code is supported through `claude-runner`:

```sh
bin/coderoam runners preset claude --id default --workdir /path/to/workspace
```

To let Claude edit files in that workspace from WhatsApp:

```sh
bin/coderoam runners preset claude-code \
  --id default \
  --workdir /path/to/workspace \
  --yes
```

Switch the allowlisted group to a configured runner:

```sh
bin/coderoam groups set-runner "1203630xxxxx@g.us" codex-code
bin/coderoam groups set-runner "1203630xxxxx@g.us" claude-code
```

Claude support requires the local Claude CLI to be logged in first:

```sh
claude
```

Then complete Claude's `/login` flow.

OpenCode, Gemini, and other CLI agents are supported through the generic
`agent-runner` wrapper. Built-in presets:

```sh
bin/coderoam runners preset opencode --id opencode --workdir /path/to/workspace
bin/coderoam runners preset opencode-code --id opencode-code --workdir /path/to/workspace --yes
bin/coderoam runners preset gemini --id gemini --workdir /path/to/workspace
bin/coderoam runners preset gemini-code --id gemini-code --workdir /path/to/workspace --yes
```

For any other CLI that can accept a prompt as an argument or stdin:

```sh
bin/coderoam runners preset agent \
  --id my-agent \
  --workdir /path/to/workspace \
  --agent-command my-agent-cli \
  --agent-arg run
```

Use `agent-code --yes` only for agents that may edit files.

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
inbox for the currently active Codex session. Slash commands such as `/goal ...`
are not executed by the daemon itself. They are highlighted by
`coderoam inbox next --format prompt` and
`coderoam inbox watch --format prompt --session-id <id>` so the active agent
session treats them as explicit user commands after it claims the inbox row.

This avoids running a second competing Codex process while one Codex turn is
already active.

For coding or goal-style workflows, enable sender allowlisting so only trusted
WhatsApp senders can instruct local agents:

```toml
[security]
require_sender_allowlist = true
admin_sender_ids = ["<trusted-sender>@lid"]
allowed_sender_ids = ["<trusted-sender>@lid"]
```

The prompt output labels slash-command messages as authorized or blocked based
on these lists. If a voice memo or audio attachment may contain a slash command,
the active agent must transcribe the audio first and only apply the command
after the transcript is available and the sender is authorized.

Login with WhatsApp:

```sh
bin/coderoam auth login --profile bot --qr
```

By default, QR login also writes and opens a PNG image:

```text
~/Library/Application Support/coderoam/profiles/bot/whatsapp-session.sqlite3.qr.png
```

You can choose the QR image path or disable auto-open:

```sh
bin/coderoam auth login --qr --qr-image /tmp/coderoam-qr.png --open-qr=false
```

Pair-code login is also available if your WhatsApp account supports it:

```sh
bin/coderoam auth login --pair-code "+380XXXXXXXXX"
```

If WhatsApp reports `401: logged out from another device`, reset the local
session before linking again:

```sh
bin/coderoam auth reset --yes
bin/coderoam auth login --qr
```

This deletes only the WhatsApp session files for the active profile. It does not
delete the app config, allowed groups, runner config, or message database.

List groups:

```sh
bin/coderoam chats list --groups
```

Allow one group:

```sh
bin/coderoam groups allow "1203630xxxxx@g.us" --alias "test-group"
```

If the bridge account is not in any group yet, you can create a small local test
group from the terminal:

```sh
bin/coderoam groups create \
  --name "Codex Bridge" \
  --participants "+380506171414" \
  --alias "codex" \
  --runner default \
  --yes
```

Group creation is disabled unless `--yes` is present and can only be triggered
from the local CLI.

If WhatsApp creates the group but cannot add the participant because of privacy
settings, send an invite link:

```sh
bin/coderoam groups send-invite "1203630xxxxx@g.us" --to "+380506171414"
```

Run the bridge:

```sh
bin/coderoam run
```

When someone in the allowlisted group sends:

```text
@bridge ping
```

the bridge sends `ping` to the configured local runner and posts the runner reply back to that group.

## Local Dry Run

The fake transport allows testing the route without WhatsApp:

```sh
bin/coderoam --config /tmp/coderoam.toml init
bin/coderoam --config /tmp/coderoam.toml runners add default \
  --mode process-once-json \
  --command "$(pwd)/bin/echo-runner"
bin/coderoam --config /tmp/coderoam.toml groups allow "fake-group@g.us" --alias fake
bin/coderoam --config /tmp/coderoam.toml test-route \
  --chat "fake-group@g.us" \
  --sender "380506171414@s.whatsapp.net" \
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
  "chat_id": "1203630xxxxx@g.us",
  "sender_id": "380506171414@s.whatsapp.net"
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
The owner can reply with `1`, `2`, the option text, or clear natural language
such as `privacy review` or `CI please`; that answer is routed back to the same
runner without requiring the normal trigger prefix. If the answer matches more
than one option, the bridge asks a narrower follow-up instead of guessing.

Local approval controls:

```sh
bin/coderoam approvals list
bin/coderoam approvals show <id>
bin/coderoam approvals approve <id>
bin/coderoam approvals reject <id>
```

## Sending a One-Off Message

After WhatsApp login succeeds, this sends from the linked bridge account:

```sh
bin/coderoam send --to "+380506171414" --text "coderoam is ready"
```

## Storage

Default paths:

- macOS config: `~/Library/Application Support/coderoam/config.toml`
- macOS app database: `~/Library/Application Support/coderoam/profiles/<profile>/coderoam.sqlite3`
- macOS WhatsApp session database: `~/Library/Application Support/coderoam/profiles/<profile>/whatsapp-session.sqlite3`
- macOS logs: `~/Library/Logs/coderoam/coderoam.log`

WhatsApp session material is stored outside the repository. Do not commit profile data or session databases.

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
