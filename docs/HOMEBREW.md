# Homebrew

coderoam is a CLI app, so Homebrew packaging uses a formula.

The formula in this repository is a tap-ready `--HEAD` formula. It lets early
users install from the current `main` branch while the project is still before a
tagged stable release.

## Install From This Repository Tap

Because this repository is the application repository, not a dedicated
`homebrew-coderoam` tap, use an explicit tap URL:

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
coderoam active start --name "Coderoam Session" --participants "+15550001111" --alias codex-session --session-id codex-session --runner codex-active --yes
coderoam run
```

In the terminal that owns the active agent session:

```sh
coderoam inbox watch --format prompt --session-id codex-session
```

Read the full setup guide in [SETUP.md](SETUP.md).

## Homebrew Core Readiness

Homebrew core expects new formulae to have a stable, tagged version and pass the
formula audit. Before submitting coderoam to `Homebrew/homebrew-core`:

1. Create a stable GitHub release, for example `v0.1.0`.
2. Replace the `head`-only formula with a stable `url` and `sha256`.
3. Run the checks from the tap:

```sh
brew audit --strict --new --online --formula dnikolayev/coderoam/coderoam
brew install --build-from-source dnikolayev/coderoam/coderoam
brew test dnikolayev/coderoam/coderoam
```

The current formula intentionally keeps the early install path in a tap until
that stable release exists.
