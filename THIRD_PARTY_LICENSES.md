# Third-Party Licenses

This repository depends on third-party Go modules. This file is an inventory for
the current checkout, generated from:

```sh
go list -m all
```

For release artifacts, include this file, `NOTICE`, `LICENSE`, and a generated
SBOM or license report that bundles the exact upstream license texts for the
released dependency graph.

## Important Licensing Notes

- `go.mau.fi/whatsmeow` and related `go.mau.fi` modules are MPL-2.0 licensed.
  Keep the WhatsApp transport in clearly separated packages and preserve MPL
  notices in source and binary distributions.
- `go.mau.fi/libsignal` has a GPL-3.0 license file in the local module cache.
  The project accepts GPL-3.0 obligations for binary releases that include this
  dependency. Release archives must include `licenses/GPL-3.0.txt`, this file,
  `NOTICE`, `LICENSE`, and the tagged source archive for the exact release.
- The local project license is MIT. The MIT license applies to this repository's
  own code, not to third-party dependencies or the combined binary artifact.
- Regenerate the release license report from the exact release commit and compare
  it to this inventory before publishing artifacts.

## Direct Dependencies

| Module | Version | License | Local license file |
| --- | --- | --- | --- |
| `github.com/mdp/qrterminal/v3` | `v3.2.1` | MIT | `LICENSE` |
| `github.com/pelletier/go-toml/v2` | `v2.3.1` | MIT | `LICENSE` |
| `github.com/spf13/cobra` | `v1.10.2` | Apache-2.0 | `LICENSE.txt` |
| `go.mau.fi/whatsmeow` | `v0.0.0-20260604205742-c6a4b703e48f` | MPL-2.0 | `LICENSE` |
| `google.golang.org/protobuf` | `v1.36.11` | BSD-style | `LICENSE` |
| `modernc.org/sqlite` | `v1.52.0` | BSD-style | `LICENSE` |

## Full Module Inventory

Note: `github.com/mattn/go-sqlite3` appears below because `go list -m all`
still resolves it as a test-time indirect dependency of `go.mau.fi/util`
(dbutil's litestream tests). It is not linked into any released binary
(`go version -m` lists `modernc.org/sqlite` only).

| Module | Version | License | Local license file |
| --- | --- | --- | --- |
| `filippo.io/edwards25519` | `v1.2.0` | BSD-style | `LICENSE` |
| `github.com/DATA-DOG/go-sqlmock` | `v1.5.2` | BSD-style | `LICENSE` |
| `github.com/agnivade/levenshtein` | `v1.2.1` | MIT | `License.txt` |
| `github.com/andreyvit/diff` | `v0.0.0-20170406064948-c7f18ee00883` | MIT | `LICENSE` |
| `github.com/beeper/argo-go` | `v1.1.2` | MIT | `LICENSE` |
| `github.com/coder/websocket` | `v1.8.14` | ISC | `LICENSE.txt` |
| `github.com/coreos/go-systemd/v22` | `v22.7.0` | Apache-2.0 | `LICENSE` |
| `github.com/cpuguy83/go-md2man/v2` | `v2.0.6` | MIT | `LICENSE.md` |
| `github.com/davecgh/go-spew` | `v1.1.1` | ISC-like | `LICENSE` |
| `github.com/dustin/go-humanize` | `v1.0.1` | MIT | `LICENSE` |
| `github.com/elliotchance/orderedmap/v3` | `v3.1.0` | MIT | `LICENSE` |
| `github.com/golang/protobuf` | `v1.5.0` | BSD-style | `LICENSE` |
| `github.com/google/go-cmp` | `v0.7.0` | BSD-style | `LICENSE` |
| `github.com/google/pprof` | `v0.0.0-20250317173921-a4b03ec1a45e` | Apache-2.0 | `LICENSE` |
| `github.com/google/uuid` | `v1.6.0` | BSD-style | `LICENSE` |
| `github.com/hashicorp/golang-lru/v2` | `v2.0.7` | MPL-2.0 | `LICENSE` |
| `github.com/inconshreveable/mousetrap` | `v1.1.0` | Apache-2.0 | `LICENSE` |
| `github.com/mattn/go-colorable` | `v0.1.14` | MIT | `LICENSE` |
| `github.com/mattn/go-isatty` | `v0.0.20` | MIT | `LICENSE` |
| `github.com/mattn/go-sqlite3` | `v1.14.45` | MIT | `LICENSE` |
| `github.com/mdp/qrterminal/v3` | `v3.2.1` | MIT | `LICENSE` |
| `github.com/ncruces/go-strftime` | `v1.0.0` | MIT | `LICENSE` |
| `github.com/pelletier/go-toml/v2` | `v2.3.1` | MIT | `LICENSE` |
| `github.com/petermattis/goid` | `v0.0.0-20260330135022-df67b199bc81` | Apache-2.0 | `LICENSE` |
| `github.com/pkg/errors` | `v0.9.1` | BSD-style | `LICENSE` |
| `github.com/pmezard/go-difflib` | `v1.0.0` | BSD-style | `LICENSE` |
| `github.com/remyoudompheng/bigfft` | `v0.0.0-20230129092748-24d4a6f8daec` | BSD-style | `LICENSE` |
| `github.com/rs/xid` | `v1.6.0` | MIT | `LICENSE` |
| `github.com/rs/zerolog` | `v1.35.1` | MIT | `LICENSE` |
| `github.com/russross/blackfriday/v2` | `v2.1.0` | BSD-style | `LICENSE.txt` |
| `github.com/sergi/go-diff` | `v1.3.1` | MIT; Apache-2.0 notice | `LICENSE`, `APACHE-LICENSE-2.0` |
| `github.com/skip2/go-qrcode` | `v0.0.0-20200617195104-da1b6568686e` | MIT | `LICENSE` |
| `github.com/spf13/cobra` | `v1.10.2` | Apache-2.0 | `LICENSE.txt` |
| `github.com/spf13/pflag` | `v1.0.9` | BSD-style | `LICENSE` |
| `github.com/stretchr/testify` | `v1.11.1` | MIT | `LICENSE` |
| `github.com/vektah/gqlparser/v2` | `v2.5.27` | MIT | `LICENSE` |
| `go.mau.fi/libsignal` | `v0.2.2` | GPL-3.0 | `LICENSE` |
| `go.mau.fi/util` | `v0.9.9` | MPL-2.0 | `LICENSE` |
| `go.mau.fi/whatsmeow` | `v0.0.0-20260604205742-c6a4b703e48f` | MPL-2.0 | `LICENSE` |
| `go.yaml.in/yaml/v3` | `v3.0.4` | MIT; Apache-2.0 | `LICENSE` |
| `golang.org/x/crypto` | `v0.52.0` | BSD-style | `LICENSE` |
| `golang.org/x/exp` | `v0.0.0-20260508232706-74f9aab9d74a` | BSD-style | `LICENSE` |
| `golang.org/x/mod` | `v0.36.0` | BSD-style | `LICENSE` |
| `golang.org/x/net` | `v0.55.0` | BSD-style | `LICENSE` |
| `golang.org/x/sync` | `v0.20.0` | BSD-style | `LICENSE` |
| `golang.org/x/sys` | `v0.45.0` | BSD-style | `LICENSE` |
| `golang.org/x/term` | `v0.43.0` | BSD-style | `LICENSE` |
| `golang.org/x/text` | `v0.37.0` | BSD-style | `LICENSE` |
| `golang.org/x/tools` | `v0.45.0` | BSD-style | `LICENSE` |
| `golang.org/x/tools/go/expect` | `v0.1.1-deprecated` | BSD-style | `LICENSE` |
| `golang.org/x/tools/go/packages/packagestest` | `v0.1.1-deprecated` | BSD-style | `LICENSE` |
| `google.golang.org/protobuf` | `v1.36.11` | BSD-style | `LICENSE` |
| `gopkg.in/check.v1` | `v0.0.0-20161208181325-20d25e280405` | BSD-style | `LICENSE` |
| `gopkg.in/yaml.v3` | `v3.0.1` | Apache-2.0 | `LICENSE` |
| `modernc.org/cc/v4` | `v4.28.2` | BSD-style | `LICENSE` |
| `modernc.org/ccgo/v4` | `v4.34.0` | BSD-style | `LICENSE` |
| `modernc.org/fileutil` | `v1.4.0` | BSD-style | `LICENSE` |
| `modernc.org/gc/v2` | `v2.6.5` | BSD-style | `LICENSE` |
| `modernc.org/gc/v3` | `v3.1.2` | BSD-style | `LICENSE` |
| `modernc.org/goabi0` | `v0.2.0` | BSD-style | `LICENSE` |
| `modernc.org/libc` | `v1.72.3` | MIT | `LICENSE`, `LICENSE-3RD-PARTY.md` |
| `modernc.org/mathutil` | `v1.7.1` | BSD-style | `LICENSE` |
| `modernc.org/memory` | `v1.11.0` | BSD-style | `LICENSE`, `LICENSE-GO`, `LICENSE-LOGO`, `LICENSE-MMAP-GO` |
| `modernc.org/opt` | `v0.2.0` | BSD-style | `LICENSE` |
| `modernc.org/sortutil` | `v1.2.1` | BSD-style | `LICENSE` |
| `modernc.org/sqlite` | `v1.52.0` | BSD-style | `LICENSE` |
| `modernc.org/strutil` | `v1.2.1` | BSD-style | `LICENSE` |
| `modernc.org/token` | `v1.1.0` | BSD-style | `LICENSE` |
| `rsc.io/qr` | `v0.2.0` | BSD-style | `LICENSE` |
