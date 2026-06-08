# Changelog

This project follows a lightweight form of Keep a Changelog. Versions before
v1.0 may include breaking changes to config, database schema, and runner
protocols.

## Unreleased

### Added

- Active-session relay for routing WhatsApp messages into the current Codex
  session without spawning a competing assistant process.
- Automatic active-session acknowledgements for fresh WhatsApp inbox messages.
- Read-receipt support through the WhatsApp transport. In active-session mode,
  read receipts are queued only after Codex claims the inbox message.
- Media metadata/caption extraction for media-only WhatsApp messages.
- Open-source publication docs, support policy, release guide, GitHub templates,
  CI workflow, and repository ignore rules.
- Active-session slash-command authorization labels in the inbox prompt.
- `coderoam inbox drain` for claiming all unread active-session WhatsApp
  input at the start of a Codex turn.
- Session-aware active inbox rows and `--session-id` claim flags for active
  Codex relay sessions.
- `coderoam inbox watch` for long-running, exclusive, session-scoped active
  inbox delivery to local agents.
- Active-session groups can now fall back to their configured runner when no
  live watcher is connected.
- Shared relay instructions for Codex, Claude, Gemini, OpenCode, and generic
  local agent clients.
- Release workflow for native platform archives, checksums, CycloneDX SBOM, and
  a generated Homebrew-core candidate formula.

### Changed

- Active inbox prompt output now detects slash commands such as `/goal` and
  highlights them as explicit Codex commands.
- Strict sender allowlisting now accepts both `allowed_sender_ids` and
  `admin_sender_ids`.
- Active-session acknowledgements now explicitly say the bridge received the
  message and is waiting for the named active Codex session to claim it.

### Security

- Expanded documentation for session storage, runner boundaries, privacy, and
  third-party license handling.
