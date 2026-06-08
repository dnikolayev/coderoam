# Security

Report vulnerabilities privately to the repository owner. Until a public
security contact is configured, do not file public issues for credential leaks,
session handling problems, message disclosure, or command-execution bugs.

Suggested report content:

- Affected version or commit.
- Local OS and install method.
- Reproduction steps.
- Impact and whether WhatsApp session data or chat content is exposed.
- Any logs with phone numbers, tokens, and message bodies redacted.

## Threat Model

`coderoam` is a local-first bridge. It should not expose a hosted API, scrape contacts, or provide third parties with access to a consumer WhatsApp account.

The main risks are:

- WhatsApp session theft.
- Accidental message disclosure through logs or runner payloads.
- Runner command misuse.
- Platform/account restrictions from unofficial WhatsApp Web automation.

## Credentials

WhatsApp session material is stored outside the repository under the profile data directory. The app must never print session tokens, QR payloads after login, or pairing codes in logs.

Encrypted storage through macOS Keychain, Linux Secret Service/libsecret, and Windows DPAPI/Credential Manager is planned but not complete in this MVP. Treat the session database as sensitive.

Default profile/session paths:

- macOS: `~/Library/Application Support/coderoam/profiles/<profile>/`
- Linux: `~/.config/coderoam/profiles/<profile>/`
- Windows: `%APPDATA%\coderoam\profiles\<profile>\`

Never commit files named like `whatsapp-session.sqlite3`,
`coderoam.sqlite3`, `*.sqlite3-wal`, `*.sqlite3-shm`, or QR PNG images.

## Runner Boundary

The bridge must invoke a fixed command from local config and pass WhatsApp
content through stdin/JSON. Do not add code that evaluates WhatsApp text as a
shell command, command-line argument template, environment variable name, or
path without explicit local configuration.

Bad:

```sh
sh -c "$WHATSAPP_MESSAGE"
```

Required shape:

```text
WhatsApp message -> JSON stdin -> configured executable
```

## WhatsApp-Origin Agent Instructions

If a runner or active Codex/Claude session can edit files, sender allowlisting
should be enabled:

```toml
[security]
require_sender_allowlist = true
admin_sender_ids = ["<trusted-sender>@lid"]
allowed_sender_ids = ["<trusted-sender>@lid"]
```

Active-session slash commands such as `/goal` must be treated as executable
agent instructions only when the inbox prompt reports that the sender is
authorized. Messages from unauthorized senders should be ignored or handled as
plain, non-executable chat context.

## Account Safety

There is no guarantee of WhatsApp account safety. The personal-account transport is unofficial and may violate WhatsApp/Meta terms. Use a dedicated account and low-volume personal usage only.

To unlink the device, use WhatsApp on the phone: Settings -> Linked Devices -> select the linked device -> Log Out. You can also run:

```sh
coderoam auth logout
```

If you suspect the local session has leaked, unlink it from the phone first,
then delete the local profile directory and relink with a new dedicated account.
