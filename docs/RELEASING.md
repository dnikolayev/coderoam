# Releasing

This document is the release checklist for public artifacts.

## Preflight

Releases are built from `main`. Normal development and pull requests target
`dev`; promote `dev` to `main` only after review, tests, and release preflight
checks pass.

```sh
go mod tidy
go test ./...
go vet ./...
go build -o bin/coderoam ./cmd/coderoam
bin/coderoam doctor
git diff --check
```

Generate release compliance artifacts from the exact release commit:

```sh
mkdir -p dist/compliance
go list -m all > dist/compliance/go-modules.txt
go run github.com/google/go-licenses@latest report ./... > dist/compliance/third-party-license-report.txt
go run github.com/google/osv-scanner/cmd/osv-scanner@v1.9.2 scan --recursive --skip-git .
go run github.com/anchore/syft/cmd/syft@latest dir:. -o spdx-json=dist/compliance/coderoam.spdx.json
```

Block the release if the checked-in inventory still has unresolved rows:

```sh
if grep -n '| review |' THIRD_PARTY_LICENSES.md; then
  echo "Resolve third-party license review rows before release." >&2
  exit 1
fi
```

Check that:

- README scope and WhatsApp/Meta disclaimer are still accurate.
- `docs/HOMEBREW.md` reflects the current formula/release path.
- `CHANGELOG.md` has an entry for the release.
- `LICENSE`, `NOTICE`, `THIRD_PARTY_LICENSES.md`, and
  `licenses/GPL-3.0.txt` are included in each binary archive.
- The tagged source archive is attached to the GitHub release.
- GPL-3.0 obligations are accepted for binary releases because the WhatsApp
  transport dependency graph includes `go.mau.fi/libsignal`.
- MPL dependency notices are preserved in source and binary distributions.
- No profile data, session databases, QR images, logs, or local config secrets
  are staged.
- `coderoam doctor` reports no broad profile/session permissions for the
  release test profile.
- Real WhatsApp testing used only a dedicated test account and low-volume test
  group.

## Suggested Artifacts

- `coderoam_<version>_darwin_arm64.tar.gz`
- `coderoam_<version>_darwin_amd64.tar.gz`
- `coderoam_<version>_linux_amd64.tar.gz`
- `coderoam_<version>_linux_arm64.tar.gz`
- `coderoam_<version>_windows_amd64.zip`
- `checksums.txt`
- SBOM or module license report
- `coderoam_<version>_source.tar.gz`
- `coderoam-homebrew-core.rb`, after the tag exists

## macOS

For public macOS releases, prefer signed and notarized artifacts. Unsigned test
builds are acceptable only for development snapshots and should be labeled as
such.

The `Release` workflow signs macOS binaries when all of these secrets are
configured:

- `APPLE_CERTIFICATE_BASE64`
- `APPLE_CERTIFICATE_PASSWORD`
- `APPLE_DEVELOPER_ID`

It submits the macOS binaries for notarization when these are also configured:

- `APPLE_ID`
- `APPLE_APP_SPECIFIC_PASSWORD`
- `APPLE_TEAM_ID`

If the secrets are missing, the release still builds unsigned archives.

## GitHub Release Workflow

Tag pushes matching `v*` run `.github/workflows/release.yml`. Every target
cross-compiles with `CGO_ENABLED=0` (sqlite is pure Go via
`modernc.org/sqlite`), so only two runner types are needed:

- `macos-15` for darwin arm64 and darwin amd64 (codesign and notarytool only
  run on macOS)
- `ubuntu-24.04` for linux amd64, linux arm64, and windows amd64

Each archive includes the user binaries, README, security/privacy docs, license
notices, and `licenses/GPL-3.0.txt`. A separate `sbom` job generates the
CycloneDX SBOM. The publish job combines archives, generates `checksums.txt`,
downloads the tagged source tarball, and renders the Homebrew-core candidate
formula with the real source `sha256`.

Use manual dispatch for dry-run packaging before a tag:

```sh
gh workflow run release.yml -f version=v0.1.11
```

Manual dispatch does not create a GitHub release or a final Homebrew-core
formula because the GitHub source tarball checksum exists only after the tag is
published.

## Release Notes

Release notes must include:

- Whether the release changes config, database schema, runner protocol, or
  transport behavior.
- Any known WhatsApp transport limitations.
- The same personal/local-use warning as the README.
- Upgrade and rollback notes when applicable.
