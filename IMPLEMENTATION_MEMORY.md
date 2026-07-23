# Implementation Memory

## Phase 02C client foundation

- The Go module is `github.com/kado-so/search`; the command entrypoint is
  `cmd/kado`.
- Extend command behavior through `internal/cli.Run`. Ordinary command errors
  must pass through `internal/diagnostic.Public`; unknown errors are redacted.
- `internal/config` owns only the HTTPS Kado base URL and platform config
  directory. Do not add keys, assertions, or tokens to `config.Config`.
- Release metadata is stamped into `internal/buildinfo.Version`, `Commit`, and
  `Date` with `go build -ldflags -X`.
- The local Proto shim has no repository pin. Verification used the installed
  Go binary at `/Users/vaishnav/.proto/tools/go/1.26.4/bin/go`; the module itself
  declares Go 1.24 compatibility.

## Notes for later goals

- Goal 2 should place credential persistence behind its own storage interface;
  OS keychain and permission-restricted file fallback do not belong in
  `internal/config`.
- Goal 3 should reuse `Config.BaseURL` as the validated HTTPS service base and
  add injectable HTTP behavior in a dedicated client/discovery package. Its
  path is `/` or a canonical unescaped ASCII prefix without a trailing slash;
  append endpoint segments without decoding or cleaning the base again.
