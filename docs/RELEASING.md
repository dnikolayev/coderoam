# Releasing

This document is the release checklist for public artifacts.

## Preflight

```sh
go mod tidy
go test ./...
go build -o bin/chat-bridge ./cmd/chat-bridge
bin/chat-bridge doctor
```

Check that:

- README scope and WhatsApp/Meta disclaimer are still accurate.
- `CHANGELOG.md` has an entry for the release.
- `LICENSE`, `NOTICE`, and `THIRD_PARTY_LICENSES.md` are included.
- No profile data, session databases, QR images, logs, or local config secrets
  are staged.
- Real WhatsApp testing used only a dedicated test account and low-volume test
  group.

## Suggested Artifacts

- `chat-bridge_<version>_darwin_arm64.tar.gz`
- `chat-bridge_<version>_darwin_amd64.tar.gz`
- `chat-bridge_<version>_linux_amd64.tar.gz`
- `chat-bridge_<version>_linux_arm64.tar.gz`
- `chat-bridge_<version>_windows_amd64.zip`
- `checksums.txt`
- SBOM or module license report

## macOS

For public macOS releases, prefer signed and notarized artifacts. Unsigned test
builds are acceptable only for development snapshots and should be labeled as
such.

## Release Notes

Release notes must include:

- Whether the release changes config, database schema, runner protocol, or
  transport behavior.
- Any known WhatsApp transport limitations.
- The same personal/local-use warning as the README.
- Upgrade and rollback notes when applicable.
