# Changelog

This project follows a lightweight form of Keep a Changelog. Versions before
v1.0 may include breaking changes to config, database schema, and runner
protocols.

## Unreleased

## v0.1.4 - 2026-06-09

### Changed

- API-style Codex and agent sessions now prefer `inbox drain` at turn
  boundaries instead of detached watchers that can claim WhatsApp messages
  before the active turn consumes them.
- Pending runner interactions now accept option numbers, option text, custom
  free-form answers, and transcribed voice-note answers.
- Local `chat-bridge` binaries keep using legacy `chat-bridge` config, data,
  database, and log paths while `coderoam` binaries use `coderoam` paths.

### Fixed

- `inbox drain` now surfaces same-session rows already claimed by a previous
  watcher, preventing read-but-unseen WhatsApp messages from staying hidden.

## v0.1.3 - 2026-06-09

### Changed

- README/Homebrew onboarding now offers a short `curl | sh` installer backed by
  a checked-in `scripts/install.sh`.
- Active-session setup now leaves fallback runners unset by default so WhatsApp
  input is claimed by the live watcher instead of a blocking agent invocation.
- Resume-based active runners such as `codex-active` are no longer used as
  automatic fallback runners when no watcher is connected; messages stay queued
  for `inbox watch`, `inbox next`, or `inbox drain`.
- Minimal active-session acknowledgements now send a compact queued status when
  no live watcher is connected.

## v0.1.2 - 2026-06-09

### Added

- Top-level README onboarding now includes a one-line Homebrew install and
  `coderoam setup` command for fast copy-paste setup.
- Active inbox and runner prompts now guide agents to inspect downloaded
  image/screenshot `local_path` files before diagnosing visual issues or using
  them as product assets.
- Metadata-only image prompts now explicitly say visual content is unavailable
  and tell agents not to guess from captions alone.
- Media guidance is documented in README, setup, relay, and per-agent
  instruction files.
- Focused tests cover image/screenshot prompt behavior for active inbox,
  Codex, Claude, and generic agent runners.

## v0.1.1 - 2026-06-08

### Added

- `coderoam active start` now sends the created group invite link by direct
  message to the participant list, with `--invite-to` for an alternate
  recipient.
- `coderoam setup` now detects supported local agent CLIs on `PATH` and prints
  exact runner preset, active group, and instruction-file commands for Codex,
  Claude, Gemini, and OpenCode.
- Parallel active-session routing tests now verify that simultaneous messages
  for separate session groups are claimed only by the matching session watcher.

## v0.1.0 - 2026-06-08

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
- GPL-3.0 notice packaging for binary archives that include the WhatsApp
  transport dependency graph.

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
