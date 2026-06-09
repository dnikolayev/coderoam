# Contributing

Thanks for considering a contribution to `coderoam`.

Contributions must preserve the local-first, personal-use posture of this
project. This is not a bulk messaging platform, a SaaS automation backend, or a
consumer WhatsApp API product.

For questions before opening a pull request, use GitHub issues or the community
Discord server: https://discord.gg/kkV6ZmkRHA

Do not add features for:

- Bulk messaging.
- Contact scraping.
- Anti-ban evasion.
- Stealth monitoring.
- Hosted multi-tenant access to consumer WhatsApp accounts.
- Bypassing WhatsApp limits, consent, or platform controls.

## Development Setup

Use the Go version declared in `go.mod`.

```sh
go mod download
go test ./...
go build -o bin/coderoam ./cmd/coderoam
```

Useful local checks:

```sh
go test ./...
go test ./internal/router ./internal/db ./internal/transport/whatsappweb
bin/coderoam doctor
```

Real WhatsApp testing should be manual, low-volume, and performed only with a
dedicated test account and test group.

## Change Guidelines

- Keep the default behavior conservative.
- Require explicit group allowlisting for any WhatsApp interaction.
- Do not turn WhatsApp text into shell commands.
- Keep session files, QR codes, and local databases outside the repository.
- Add focused tests for routing, persistence, runner protocol, redaction, and
  transport parsing changes.
- Keep docs honest about unofficial transport risk and account restrictions.

## Branch Model

- Open pull requests against `dev`.
- Keep day-to-day development on `dev` or feature branches based on `dev`.
- Keep `main` as the release/build branch.
- Promote `dev` to `main` only after review, tests, and any release checks pass.

## Pull Requests

Before opening a pull request:

- Run the full test suite.
- Target the `dev` branch unless maintainers explicitly request otherwise.
- Update docs for user-facing behavior, config changes, or safety changes.
- Note whether any real WhatsApp testing was performed.
- Avoid unrelated formatting churn.

```sh
go test ./...
```

For behavior touching routing, runners, persistence, or privacy, include focused tests.
