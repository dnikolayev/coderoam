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
coderoam setup
coderoam run
```

The setup wizard:

- selects WhatsApp as the messenger
- links the bridge account with QR if needed
- detects supported local agent clients
- asks which WhatsApp phone numbers may control the session
- requires exact confirmation of those numbers before sending invites
- creates a dedicated WhatsApp group and configures sender allowlisting

For docs or automation, print the manual command guide instead:

```sh
coderoam setup --print --agent auto --workdir /path/to/workspace --session-id codex-session
coderoam setup --print --agent codex --workdir /path/to/workspace --session-id codex-session
```

`active start` creates a dedicated WhatsApp group and direct-messages the group
invite link to `--participants` by default. The user opens that link to join the
session chat when WhatsApp privacy settings do not add them automatically. Pass
`--invite-to` to DM the link to a different phone number or WhatsApp JID.

In an API-style Codex session, drain the inbox at turn start:

```sh
coderoam inbox drain --format prompt --session-id codex-session
```

In a terminal client that continuously reads command output, you can instead run
a watcher:

```sh
coderoam inbox watch --format prompt --session-id codex-session
```

When someone sends a message in the session group, coderoam stores it in the
session inbox. `drain` prints unread rows and any same-session row that was
already claimed by a previous watcher. `watch` prints rows immediately only when
the client keeps reading stdout.

To keep a watcher alive across terminal restarts for a continuously-reading
client, install an OS user service:

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
available on the current machine, or `coderoam setup --print --agent auto` to
only print the manual commands.

## Authorized Senders

Active coding sessions should use sender allowlisting. `coderoam setup` enables
it after you confirm the intended phone numbers. WhatsApp may later deliver a
message using a privacy-preserving `@lid` sender ID; when that happens, the
inbox prompt tells the agent not to execute the message until you approve it
locally:

```sh
coderoam senders allow "<sender-id>" --admin
```

Approve only after confirming the sender is one of the people whose phone number
you entered during setup.

## Use An Existing WhatsApp Group

If the bridge account is already in the group:

```sh
coderoam chats list --groups
coderoam active enable "<group-id>" --alias codex-session --session-id codex-session --managed
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
