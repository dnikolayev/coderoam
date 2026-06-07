# chat-bridge

Local WhatsApp group bridge for CLI applications.

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

- Go CLI binary named `chat-bridge`.
- Local config with conservative defaults.
- WhatsApp Web transport using whatsmeow with QR and pair-code login.
- Group allowlisting.
- Prefix trigger, default `@bridge`.
- Process-once text and process-once JSON runners.
- SQLite persistence for profiles, chats, senders, messages, runner invocations, outbox, and audit events.
- Incoming message deduplication by WhatsApp message ID.
- Fake transport and `test-route` for local testing without WhatsApp.
- Kill switch through `chat-bridge pause`, `resume`, and `kill`.
- Direct `send --to ... --text ...` helper for one-off status messages from the linked account.
- Local-confirmed group creation and invite-link sending.
- Built-in Codex and Claude runner presets, including explicit coding modes.
- Important-only WhatsApp notification mode for active Codex and Claude runners.
- Active-session inbox relay for continuing the current Codex chat from WhatsApp without spawning a competing Codex process.

Partially implemented:

- Media messages are detected and queued as local metadata/caption text.
  Downloading or forwarding media files is still disabled by default and not
  part of the MVP.

Not implemented yet:

- Participant management beyond create/invite flows.
- Persistent JSONL runners.
- Approval queue.
- Encrypted session storage integration with Keychain/libsecret/DPAPI.
- Signed release packaging.

## Quick Start

Prerequisites:

- Go matching the version in `go.mod`.
- A dedicated WhatsApp account/number for the bridge.
- A local terminal runner, or one of the included example runners.

Build:

```sh
go build -o bin/chat-bridge ./cmd/chat-bridge
go build -o bin/echo-runner ./examples/echo-runner
go build -o bin/codex-runner ./examples/codex-runner
go build -o bin/claude-runner ./examples/claude-runner
```

Initialize local config:

```sh
bin/chat-bridge init
```

Configure a runner:

```sh
bin/chat-bridge runners add default \
  --mode process-once-json \
  --command /path/to/your/runner
```

To connect WhatsApp messages to Codex CLI, use the included `codex-runner` wrapper:

```sh
bin/chat-bridge runners add default \
  --mode process-once-json \
  --command "$(pwd)/bin/codex-runner"
```

The easier path is a built-in preset:

```sh
bin/chat-bridge runners preset codex --id default --workdir /path/to/workspace
```

To let Codex edit files in that workspace from WhatsApp, explicitly choose the coding preset:

```sh
bin/chat-bridge runners preset codex-code \
  --id default \
  --workdir /path/to/workspace \
  --yes
```

To continue an existing Codex session from WhatsApp, use `codex-active`.
This resumes the most recent recorded Codex session and sends each WhatsApp
message as the next user turn. The active presets only post important updates
back to WhatsApp, such as plan/checklist changes, blockers, questions that need
the user, or final summaries:

```sh
bin/chat-bridge runners preset codex-active \
  --id codex-active \
  --workdir /path/to/workspace \
  --yes

bin/chat-bridge groups set-runner "1203630xxxxx@g.us" codex-active
```

For a specific Codex session id:

```sh
bin/chat-bridge runners preset codex-session \
  --id codex-session \
  --workdir /path/to/workspace \
  --session-id "019e..." \
  --yes

bin/chat-bridge groups set-runner "1203630xxxxx@g.us" codex-session
```

Use a specific session id if you need to avoid accidentally resuming another
recent Codex session.

For a currently active Codex session, prefer the inbox relay. This stores
WhatsApp input locally and lets the active Codex session pull it between work
chunks:

```sh
bin/chat-bridge active enable "1203630xxxxx@g.us" \
  --alias codex-session \
  --session-id codex-session
bin/chat-bridge groups set-runner "1203630xxxxx@g.us" codex-active
bin/chat-bridge inbox watch --format prompt --session-id codex-session
bin/chat-bridge inbox done 1
bin/chat-bridge notify --chat codex-session --important --text "Plan updated..."
```

The relay avoids running `codex exec resume` while another Codex turn is active.
`chat-bridge run` owns the WhatsApp connection and sends queued notifications
from the active outbox. Each fresh active-session WhatsApp message also gets an
automatic queued acknowledgement so the group is never left silent while Codex
has not pulled the inbox yet. Active inbox rows are tagged with a session id so
multiple active agent sessions do not claim each other's WhatsApp input. For
active-session groups, the bridge sends a WhatsApp read receipt only after
`chat-bridge inbox watch --session-id <id>`,
`chat-bridge inbox next --session-id <id>`, or
`chat-bridge inbox drain --session-id <id>` claims the message for that active
agent session. Until then, the bridge acknowledgement says the message is
waiting for that session to claim it. `inbox watch` keeps one exclusive local
consumer connected per session and emits claimed messages immediately; use
`--format jsonl` for machine-readable local agent integrations. Media-only
messages are queued with local metadata/captions rather than downloaded by
default.

If no live watcher is connected and the active-session group has a runner
configured, the daemon uses that runner as a fallback instead of leaving the
message stuck in the inbox. For Codex, `codex-active` is the usual fallback
because it resumes the latest local Codex session and uses important-only
WhatsApp replies.

Reusable client instructions live in:

- `docs/AGENT_RELAY.md`
- `docs/agents/codex.md`
- `docs/agents/claude.md`
- `docs/agents/gemini.md`
- `docs/agents/opencode.md`
- `docs/agents/generic.md`

Claude Code is supported through `claude-runner`:

```sh
bin/chat-bridge runners preset claude --id default --workdir /path/to/workspace
```

To let Claude edit files in that workspace from WhatsApp:

```sh
bin/chat-bridge runners preset claude-code \
  --id default \
  --workdir /path/to/workspace \
  --yes
```

Switch the allowlisted group to a configured runner:

```sh
bin/chat-bridge groups set-runner "1203630xxxxx@g.us" codex-code
bin/chat-bridge groups set-runner "1203630xxxxx@g.us" claude-code
```

Claude support requires the local Claude CLI to be logged in first:

```sh
claude
```

Then complete Claude's `/login` flow.

## Important-only notifications

The Codex and Claude wrappers can suppress routine assistant output. When
important-only mode is enabled, the wrapper tells the assistant to reply only for
important WhatsApp updates: plan/checklist changes, blockers, questions that
need the user, or final summaries. If there is no important update, the
assistant is instructed to return the exact ignore marker. The wrapper converts
that marker into a runner `ignore` action, and `chat-bridge` sends no WhatsApp
message.

Enabled by built-in presets:

- `codex-active`
- `codex-session`
- `claude`
- `claude-code`

Optional runner environment variables:

- `CODEX_RUNNER_WORKDIR`: working directory for `codex exec`.
- `CODEX_RUNNER_MODEL`: model passed to Codex.
- `CODEX_RUNNER_SANDBOX`: Codex sandbox, default `read-only`.
- `CODEX_RUNNER_RESUME`: set to `last` to call `codex exec resume --last`.
- `CODEX_RUNNER_SESSION_ID`: resume a specific Codex session id.
- `CODEX_RUNNER_RESUME_ALL`: include all cwd sessions when resolving `--last`.
- `CODEX_RUNNER_SYSTEM_PROMPT`: instruction prepended to WhatsApp messages.
- `CODEX_RUNNER_IMPORTANT_ONLY`: set to `true` to suppress routine WhatsApp replies.
- `CODEX_RUNNER_IGNORE_MARKER`: exact marker that means "send no WhatsApp reply"; default `[[chat-bridge-ignore]]`.
- `CLAUDE_RUNNER_WORKDIR`: working directory for `claude -p`.
- `CLAUDE_RUNNER_MODEL`: model passed to Claude.
- `CLAUDE_RUNNER_PERMISSION_MODE`: Claude permission mode, default `default`.
- `CLAUDE_RUNNER_SYSTEM_PROMPT`: instruction prepended to WhatsApp messages.
- `CLAUDE_RUNNER_IMPORTANT_ONLY`: set to `true` to suppress routine WhatsApp replies.
- `CLAUDE_RUNNER_IGNORE_MARKER`: exact marker that means "send no WhatsApp reply"; default `[[chat-bridge-ignore]]`.

## Active WhatsApp Slash Commands

When using active-session mode, WhatsApp messages are queued into the local
inbox for the currently active Codex session. Slash commands such as `/goal ...`
are not executed by the daemon itself. They are highlighted by
`chat-bridge inbox next --format prompt` and
`chat-bridge inbox watch --format prompt --session-id <id>` so the active agent
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
on these lists.

Login with WhatsApp:

```sh
bin/chat-bridge auth login --profile bot --qr
```

By default, QR login also writes and opens a PNG image:

```text
~/Library/Application Support/chat-bridge/profiles/bot/whatsapp-session.sqlite3.qr.png
```

You can choose the QR image path or disable auto-open:

```sh
bin/chat-bridge auth login --qr --qr-image /tmp/chat-bridge-qr.png --open-qr=false
```

Pair-code login is also available if your WhatsApp account supports it:

```sh
bin/chat-bridge auth login --pair-code "+380XXXXXXXXX"
```

If WhatsApp reports `401: logged out from another device`, reset the local
session before linking again:

```sh
bin/chat-bridge auth reset --yes
bin/chat-bridge auth login --qr
```

This deletes only the WhatsApp session files for the active profile. It does not
delete the app config, allowed groups, runner config, or message database.

List groups:

```sh
bin/chat-bridge chats list --groups
```

Allow one group:

```sh
bin/chat-bridge groups allow "1203630xxxxx@g.us" --alias "test-group"
```

If the bridge account is not in any group yet, you can create a small local test
group from the terminal:

```sh
bin/chat-bridge groups create \
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
bin/chat-bridge groups send-invite "1203630xxxxx@g.us" --to "+380506171414"
```

Run the bridge:

```sh
bin/chat-bridge run
```

When someone in the allowlisted group sends:

```text
@bridge ping
```

the bridge sends `ping` to the configured local runner and posts the runner reply back to that group.

## Local Dry Run

The fake transport allows testing the route without WhatsApp:

```sh
bin/chat-bridge --config /tmp/chat-bridge.toml init
bin/chat-bridge --config /tmp/chat-bridge.toml runners add default \
  --mode process-once-json \
  --command "$(pwd)/bin/echo-runner"
bin/chat-bridge --config /tmp/chat-bridge.toml groups allow "fake-group@g.us" --alias fake
bin/chat-bridge --config /tmp/chat-bridge.toml test-route \
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

## Sending a One-Off Message

After WhatsApp login succeeds, this sends from the linked bridge account:

```sh
bin/chat-bridge send --to "+380506171414" --text "chat-bridge is ready"
```

## Storage

Default paths:

- macOS config: `~/Library/Application Support/chat-bridge/config.toml`
- macOS app database: `~/Library/Application Support/chat-bridge/profiles/<profile>/chat-bridge.sqlite3`
- macOS WhatsApp session database: `~/Library/Application Support/chat-bridge/profiles/<profile>/whatsapp-session.sqlite3`
- macOS logs: `~/Library/Logs/chat-bridge/chat-bridge.log`

WhatsApp session material is stored outside the repository. Do not commit profile data or session databases.

## Open-Source Readiness

Before publishing a repository or release, check:

- `README.md` explains the local/personal-use scope and unofficial transport
  risk.
- `LICENSE`, `NOTICE`, and `THIRD_PARTY_LICENSES.md` are present.
- `SECURITY.md`, `PRIVACY.md`, `SUPPORT.md`, `CONTRIBUTING.md`, and
  `CODE_OF_CONDUCT.md` are present.
- Active-session coding workflows use `require_sender_allowlist = true`.
- `.gitignore` excludes `bin/`, local profile data, logs, session databases,
  QR images, and other generated files.
- CI runs `go test ./...`.
- Release artifacts include checksums and third-party license notices.

## Safety Defaults

- New groups are ignored until explicitly allowlisted.
- Direct messages are ignored by the router unless configured through a group/chat allowlist path.
- The default trigger is `@bridge`; always-on mode is disabled.
- The app never shells out with WhatsApp text. It invokes a fixed configured executable and sends message content on stdin.
- The kill switch file stops message processing: `chat-bridge pause` creates it and `chat-bridge resume` removes it.
