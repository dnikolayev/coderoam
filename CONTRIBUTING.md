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

Use the Go version declared in `go.mod` (currently 1.26.4).

```sh
go mod download
go build -o bin/coderoam ./cmd/coderoam
go test ./...
```

The Makefile wraps the common workflows:

```sh
make build      # build bin/coderoam
make test       # go test ./...
make test-race  # go test -race ./...
make lint       # static analysis
make fmt        # format the tree
make vet        # go vet ./...
make ci         # the full local CI sequence
```

Tests need NO WhatsApp account. The suite uses the fake transport and isolates
all state in per-test `t.TempDir()` directories, so it never touches your real
config, profiles, or session files.

To run a single package's tests:

```sh
go test ./internal/app
go test ./internal/router ./internal/db ./internal/transport/whatsappweb
```

To exercise the bridge end to end without WhatsApp, use `coderoam doctor` plus
the local dry run (`test-route` with the fake transport):

```sh
bin/coderoam doctor
bin/coderoam --config /tmp/coderoam.toml init
bin/coderoam --config /tmp/coderoam.toml runners add default \
  --mode process-once-json \
  --command "$(pwd)/bin/echo-runner"
bin/coderoam --config /tmp/coderoam.toml groups allow "fake-group@g.us" --alias fake
bin/coderoam --config /tmp/coderoam.toml test-route \
  --chat "fake-group@g.us" \
  --sender "fake-sender@s.whatsapp.net" \
  --text "@bridge ping"
```

`coderoam doctor` checks the local config, profile database, WhatsApp login
state, and configured runner commands, and prints what is missing.

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
