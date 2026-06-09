# Homebrew

coderoam is a CLI app, so Homebrew packaging uses a formula.

There are two Homebrew paths:

- The checked-in `Formula/coderoam.rb` is the tap fallback. It installs from
  `HEAD` while the project is moving quickly.
- Tagged releases generate a source-built Homebrew-core candidate formula from
  `.github/homebrew-core/coderoam.rb.template`. The generated formula includes
  the real release tarball `sha256`.

## Install From This Repository Tap

Because this repository is the application repository, not a dedicated
`homebrew-coderoam` tap, use an explicit tap URL:

```sh
curl -fsSL https://raw.githubusercontent.com/dnikolayev/coderoam/main/scripts/install.sh | sh
```

The script runs the tap install and then starts `coderoam setup`. To run the
Homebrew install manually:

```sh
brew tap dnikolayev/coderoam https://github.com/dnikolayev/coderoam.git
brew install --HEAD dnikolayev/coderoam/coderoam
```

Check the install:

```sh
coderoam version
coderoam setup
```

`coderoam setup` prints the exact commands needed before mobile chat sessions
can work.

## After Install

coderoam does not connect to a messenger automatically. For WhatsApp:

```sh
coderoam init
coderoam auth login --profile bot --qr
coderoam runners preset codex-active --id codex-active --workdir /path/to/workspace --yes
coderoam active start --name "Coderoam Session" --participants "+15550001111" --alias codex-session --session-id codex-session --yes
coderoam run
```

In the terminal that owns the active agent session:

```sh
coderoam inbox watch --format prompt --session-id codex-session
```

Read the full setup guide in [SETUP.md](SETUP.md).

## Homebrew Core Readiness

Homebrew core expects new formulae to have a stable, tagged version and pass the
formula audit. For `v0.1.4`, create and push the tag only after release preflight
passes:

```sh
git tag -a v0.1.4 -m "coderoam v0.1.4"
git push origin v0.1.4
```

The `Release` workflow will upload:

- platform archives
- `checksums.txt`
- `coderoam_<version>_sbom.cdx.json`
- `coderoam_<version>_source.tar.gz`
- `coderoam-homebrew-core.rb`

Validate the generated core formula from a local throwaway tap:

```sh
brew tap-new local/coderoam-core
cp coderoam-homebrew-core.rb "$(brew --repository local/coderoam-core)/Formula/coderoam.rb"
brew audit --strict --new --online local/coderoam-core/coderoam
brew install --build-from-source local/coderoam-core/coderoam
brew test local/coderoam-core/coderoam
brew uninstall local/coderoam-core/coderoam
brew untap local/coderoam-core
```

For Homebrew core submission, copy the generated formula into the
`Homebrew/homebrew-core` formula tree and rerun:

```sh
brew audit --strict --new --online coderoam
brew install --build-from-source coderoam
brew test coderoam
```

If Homebrew-core acceptability fails because the project is still too new or too
niche, keep using the repository tap and revisit core after adoption grows.
