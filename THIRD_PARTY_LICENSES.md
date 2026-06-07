# Third-Party Licenses

This project depends on third-party Go modules. Run:

```sh
go list -m all
```

to inspect the exact dependency set for the current checkout.

Important direct dependencies:

- `go.mau.fi/whatsmeow` is MPL-2.0. Keep license notices for the WhatsApp transport clear in source and release artifacts.
- `github.com/spf13/cobra` is used for the CLI.
- `github.com/pelletier/go-toml/v2` is used for config parsing.
- `modernc.org/sqlite` and `github.com/mattn/go-sqlite3` support local SQLite storage.
- `github.com/mdp/qrterminal/v3` and `github.com/skip2/go-qrcode` support QR login output.

Release artifacts should include this file, `NOTICE`, and a generated module
license report or SBOM.
