# Changelog

This project follows a lightweight form of Keep a Changelog. Versions before
v1.0 may include breaking changes to config, database schema, and runner
protocols.

## Unreleased

## v0.1.15 - 2026-06-11

### Changed

- Updated the WhatsApp Web transport dependency to the latest available
  whatsmeow snapshot and verified reconnect/auth plus outbox sending with the
  local WhatsApp daemon.
- Updated `golang.org/x/sys` and the GitHub Actions golangci-lint action.

## v0.1.14 - 2026-06-11

### Changed

- Removed legacy runtime-name compatibility: all binaries now use coderoam
  config, data, database, and log paths.

## v0.1.13 - 2026-06-10

### Changed

- Moved install, quick-start, and common how-to commands near the top of the
  README so new users can install and create a mobile coding chat without
  scrolling through detailed reference sections first.

### Fixed

- Active-session configuration now rejects one session id or alias being shared
  by multiple WhatsApp groups, and rejects one group being configured for
  multiple active sessions. This prevents Codex, Claude, Gemini, OpenCode, or
  other agent lanes from reading each other's WhatsApp queues.

### Tests

- Added CLI and config regression coverage for duplicate active-session ids,
  duplicate active aliases, duplicate chat/session bindings, invalid hand-edited
  configs, and Codex/Claude queue isolation probes.

## v0.1.12 - 2026-06-10

### Fixed

- The run daemon now shares live config through deep-cloned snapshots, so group
  lifecycle updates cannot race with read-receipt/outbox processing or leak
  mutable runner and sender allowlist state between goroutines.
- Unix run-lock acquisition now detects an unlinked/recreated lock file before
  accepting the lock, and release warns when an outside actor replaced the lock
  while held.
- Windows takeover now fails fast with a clear unsupported message instead of
  timing out through the generic lock-acquisition error.
- Release checksums are now generated through a temporary file, avoiding a
  possible `checksums.txt` self-entry, and macOS notarization is skipped unless
  signing secrets are also present.

### Tests

- Added daemon config snapshot, run-lock stale-inode, run-lock eviction warning,
  and Windows takeover refusal regression coverage.

## v0.1.11 - 2026-06-09

### Fixed

- Router config reloads now use immutable snapshots, so a reload during
  WhatsApp message handling cannot mix Codex and Claude group/runner settings
  inside one message path.

### Tests

- Added race coverage for concurrent router config reloads and message handling.
- Parallelized safe unit tests to catch accidental shared state across agent
  client sessions sooner.

## v0.1.10 - 2026-06-09

### Fixed

- Active-session inbox reads now require a resolved session id when multiple
  mobile agent groups are configured, preventing a Claude, Codex, Gemini, or
  OpenCode client from accidentally claiming another client's WhatsApp queue.
- Blank-session database claims now stay limited to legacy blank-session rows
  instead of matching named active-session rows.

### Tests

- Added regression coverage for parallel active-session queue isolation,
  router fallback shutdown, JSONL runner stop/restart races, and WhatsApp
  transport parsing/decision helpers.

## v0.1.9 - 2026-06-09

### Changed

- The large CLI command implementation is split into smaller command-focused
  files for easier review and maintenance.
- WhatsApp transport decision logic is extracted from live transport plumbing so
  parsing, QR event handling, read-receipt targets, and group validation can be
  tested without a live client.

### Fixed

- Scheduled active-session fallback goroutines now stop deterministically before
  the router closes its database store, avoiding shutdown-time closed-database
  errors.

## v0.1.8 - 2026-06-09

### Added

- CI now runs vet, race tests, golangci-lint, and CGO-free smoke builds for
  every supported release target.
- Dependabot and a project Makefile cover routine dependency and local quality
  workflows.

### Changed

- The WhatsApp session store now uses the pure-Go SQLite driver, matching the
  app database and enabling portable release builds without per-target C
  toolchains.
- README, support, contribution, and per-agent relay docs now emphasize the
  first-run setup flow, separate active-session IDs per client, and generated
  runbook markers.

### Fixed

- `coderoam run` locking now uses OS advisory locks on a stable file so racing
  daemons cannot both claim a stale pid file.
- JSONL runner shutdown no longer races with the subprocess wait goroutine
  under the Go race detector.

## v0.1.7 - 2026-06-09

### Added

- `coderoam runbook` writes/update-safe relay instructions for Claude, Codex,
  Gemini, and OpenCode workspaces.
- `coderoam setup` installs those local runbook sections by default, with
  `--no-runbook` available when a workspace should not be touched.
- `coderoam run` now enforces a per-profile daemon lock and exposes
  `--takeover` for deliberate replacement of an already-running bridge daemon.

### Changed

- `coderoam init` is now idempotent when a config already exists: it ensures the
  profile database exists, refreshes unsupported old session-encryption defaults
  when needed, and points users back to setup or runner presets instead of
  failing.
- Agent relay docs now tell Codex, Claude, Gemini, OpenCode, and generic agents
  to use the session id for their own group instead of reusing `codex-session`.
- The Codex setup path now defaults to `codex-code` for background fallback
  lanes and suppresses routine WhatsApp replies by default.
- `active start` can default invite recipients from configured phone-addressable
  owner allowlists while skipping privacy `@lid` and group IDs.

### Fixed

- QR login keeps the connection open briefly after pairing so WhatsApp can
  finish linked-device registration before a standalone `auth login` exits.
- QR login refreshes expired QR batches within a longer login window instead of
  failing quickly with a stale QR image.

## v0.1.6 - 2026-06-09

### Fixed

- Interactive setup agent selection now exits cleanly if stdin closes before a
  selection is provided.

## v0.1.5 - 2026-06-09

### Added

- `coderoam setup` now runs an interactive first-run wizard that links
  WhatsApp, configures an agent, confirms authorized phone numbers before
  invites are sent, creates the active session group, and enables sender
  allowlisting.
- `coderoam setup --print` keeps the previous manual command guide for docs,
  scripts, and non-interactive install output.
- `coderoam senders allow <sender-id-or-phone> [--admin]` authorizes observed
  WhatsApp sender IDs, including first-message `@lid` IDs.

### Changed

- The repository tap formula now installs the latest stable release by default,
  and `scripts/install.sh` defaults to stable installs while keeping `--head`
  for contributor builds.
- The pipe installer now detaches Homebrew commands from script stdin so
  setup guidance still prints when using `curl ... | sh`.
- Active-session groups now queue messages from unrecognized senders for local
  verification instead of dropping them before the owner can approve the actual
  WhatsApp sender ID.
- Agent replies are normalized into WhatsApp-friendly plain text so numbered
  choices, headings, and links remain readable in mobile chat.

### Security

- Agent runner prompts that begin with `-` now get a `--` separator before the
  prompt argument so message text is not parsed as CLI flags.
- The generic audio transcriber runner no longer invokes configured
  transcriber commands through a shell.
- Local message databases and WhatsApp session databases are chmodded to
  owner-only permissions on open, where supported by the OS.
- `store_sessions_encrypted` now defaults to `false` until encrypted session
  storage is actually implemented.

## v0.1.4 - 2026-06-09

### Changed

- API-style Codex and agent sessions now prefer `inbox drain` at turn
  boundaries instead of detached watchers that can claim WhatsApp messages
  before the active turn consumes them.
- Pending runner interactions now accept option numbers, option text, custom
  free-form answers, and transcribed voice-note answers.

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
