# Setup

coderoam needs a connected messenger before an AI coding session can continue
from mobile. Installing the CLI is not enough: you must link a messenger account
and bind a chat to a local runner/session.

WhatsApp is the implemented transport today. `telegram`, `slack`, and
`google-chat` are reserved transport names that report clear setup/status errors
until their adapters are implemented.

## Quick WhatsApp Setup

Build or install coderoam, then run:

```sh
coderoam init
coderoam auth login --profile bot --qr
coderoam runners preset codex-active --id codex-active --workdir /path/to/workspace --yes
coderoam active start --name "Coderoam Session" --participants "+15550001111" --alias codex-session --session-id codex-session --runner codex-active --yes
coderoam run
```

`coderoam setup` also scans `PATH` for supported local agent clients and prints
matching runner preset commands. To focus on one client or one workspace:

```sh
coderoam setup --agent auto --workdir /path/to/workspace --session-id codex-session
coderoam setup --agent codex --workdir /path/to/workspace --session-id codex-session
```

`active start` creates a dedicated WhatsApp group and direct-messages the group
invite link to `--participants` by default. The user opens that link to join the
session chat when WhatsApp privacy settings do not add them automatically. Pass
`--invite-to` to DM the link to a different phone number or WhatsApp JID.

In the agent terminal that should own the session:

```sh
coderoam inbox watch --format prompt --session-id codex-session
```

When someone sends a message in the session group, coderoam stores it in the
session inbox. The watcher prints it for the active agent to handle.

To keep that watcher alive across terminal restarts, install an OS user service:

```sh
coderoam service install --session-id codex-session --profile bot
coderoam service start --session-id codex-session --profile bot
coderoam service status --session-id codex-session --profile bot
```

Use `--dry-run` with any service command to inspect the generated LaunchAgent,
systemd unit, or Windows scheduled task command before changing local service
state.

## Other CLI Agents

Codex and Claude have dedicated wrappers. OpenCode, Gemini, and arbitrary CLI
agents use the generic `agent-runner` wrapper:

```sh
coderoam runners preset opencode --id opencode --workdir /path/to/workspace
coderoam runners preset gemini --id gemini --workdir /path/to/workspace
coderoam runners preset agent --id my-agent --workdir /path/to/workspace --agent-command my-agent-cli --agent-arg run
```

Use the `*-code` variants with `--yes` only when that local agent may edit files.
Run `coderoam setup --agent auto` to detect which of these client commands are
available on the current machine.

## Use An Existing WhatsApp Group

If the bridge account is already in the group:

```sh
coderoam chats list --groups
coderoam active enable "<group-id>" --alias codex-session --session-id codex-session --managed
coderoam groups set-runner "<group-id>" codex-active
coderoam run
```

Use `--managed` only for a dedicated relay group that coderoam may archive when
the session is no longer usable. Leave it off for ordinary groups.

## Check What Is Missing

```sh
coderoam status
coderoam doctor
coderoam setup
```

If no messenger is linked, status and doctor point back to `coderoam setup`.

## Voice Notes

The same local media path is used for screenshots and images. When
`download_media = true`, prompt output includes `media[].local_path`; coding
agents should inspect that file before diagnosing UI issues or using it as a
product asset. If only metadata/caption text appears, the visual content was not
downloaded and the agent should ask for a resend or media download.

Voice transcription is optional and local. Install tools:

```sh
brew install ffmpeg whisper-cpp
```

Then add this to the config:

```toml
[transport]
download_media = true
transcribe_audio = true
audio_transcribe_command = "coderoam-transcribe --model /path/to/ggml-base.en.bin"
audio_transcribe_timeout_seconds = 120
```

The transcript is passed to runners as `media[].transcript`.
