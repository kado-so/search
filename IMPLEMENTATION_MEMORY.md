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
- Long-lived key persistence is owned by `internal/keystore`, not config. Use
  `NewOSKeychainStore` by default; it targets the `kado.so` service and the
  versioned `agent-management-key.v1` account.
- `NewFileStore` is an explicit Unix-like fallback only. It never activates
  automatically, requires an absolute path with no symlink components, an exact
  `0700` immediate parent directory, and an exact `0600` non-symlink regular
  file. Operations use an anchored `os.Root` handle after component identity
  validation so ancestor replacement cannot redirect access. Windows remains
  keychain-only because portable Go file modes do not establish equivalent
  credential ACLs.
- Credential records and management-key payloads both start at version 1.
  There is no legacy format to migrate; malformed and unknown versions fail
  closed. Storage errors render safe classifications without paths, backend
  text, or key contents.
- `internal/agentkey` owns Ed25519 signing. Management signers persist only
  through `keystore.Store`; session signers have no persistence or marshaling
  API and remain memory-only.
- Storage and persistence errors retain trusted `errors.Is`/`Unwrap` causes but
  keep those causes behind pointer-held private details and safe formatters so
  `%v`, `%+v`, `%#v`, and `%s` cannot reflect backend secrets.

## Notes for later goals

- Goal 3 should reuse `Config.BaseURL` as the validated HTTPS service base and
  add injectable HTTP behavior in a dedicated client/discovery package. Its
  path is `/` or a canonical unescaped ASCII prefix without a trailing slash;
  append endpoint segments without decoding or cleaning the base again.
- Goal 3 should load or create the management signer through the Goal 2
  keystore boundary. It must not add key material to `config.Config` or persist
  session signers.
