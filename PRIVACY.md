# Privacy

`chat-bridge` is local-first. By default it does not send telemetry, logs, credentials, or chat content to any external service except WhatsApp itself and the configured local runner.

## Stored Locally

The app stores:

- Config.
- Allowed group IDs and aliases.
- Runner configuration.
- WhatsApp session database.
- SQLite operational database with messages, hashes, invocations, outbox state, and audit events.

Default macOS paths are under:

- `~/Library/Application Support/chat-bridge/`
- `~/Library/Logs/chat-bridge/`

## Message Text

By default, message text is stored locally for debugging and deduplication context. You can disable local message text storage:

```toml
[retention]
store_message_text = false
store_message_hash = true
```

When disabled, raw stored message JSON also has text fields blanked.

## Media

Media download is disabled by default:

```toml
[transport]
download_media = false
```

In the current MVP, media messages are detected and queued as local text
metadata such as media type, MIME type, file name, size, and caption when
available. Media files are not downloaded by default.

If future media download support is enabled, media must stay local, use
retention limits, and be exposed to runners only by explicit configuration.

## Deleting Data

Run `chat-bridge auth logout` to invalidate the linked WhatsApp session, then
delete the profile directory from the app data path if you want to remove local
history.

Typical macOS cleanup:

```sh
rm -rf "$HOME/Library/Application Support/chat-bridge/profiles/<profile>"
rm -f "$HOME/Library/Logs/chat-bridge/chat-bridge.log"
```

## Telemetry

There is no telemetry by default. The app should not phone home, upload logs, or
send diagnostics to the project maintainers.
