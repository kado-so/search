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
- `internal/agentauth` owns protected-resource and authorization-server
  discovery plus authenticate-or-enroll. It requires the configured Kado
  resource and issuer, same-issuer canonical HTTPS endpoints, no redirects,
  exact case-sensitive/non-null duplicate-free bounded JSON, and a fresh replay
  nonce.
- Enrollment uses the Phase 02B v0.1 `agent-enrollment+jws` wire profile. The
  persistent management signer authorizes the exact
  `authenticate-or-enroll` payload and the server returns a direct active
  principal/credential/client result. Public keys use OKP Ed25519 JWKs and RFC
  7638 SHA-256 thumbprints.
- The client pins the Phase 02B discovery fixture byte-for-byte and requires
  exact response/status coupling: `201` means newly created and `200` means an
  existing credential.
- `keystore.Store.Create` is an atomic first-writer operation. File and
  keychain adapters serialize it across processes with a per-identity lock, and
  agent authentication uses `LoadOrCreateManagementSigner` so concurrent first
  runs cannot replace the winning installation identity.

## Notes for later goals

- Phase 02B Goal 2 publishes
  `contracts/agent-auth/v0.1/discovery.json` and `wire-profile.json`; Goal 3's
  pinned copies are byte-identical. `signed-enrollment.v0.1.json` is a
  deterministic non-secret vector generated exactly by Go and accepted by the
  Phase 02B TypeScript validator. Set `KADO_PHASE_02B_PROTOCOL` to the validator
  source path and optionally `KADO_TYPESCRIPT_RUNTIME` (default `bun`) to run
  that cross-repository integration test without a production dependency.
- Phase 02B Goal 4 is pinned byte-for-byte through
  `discovery.v0.1.json`, `admission-profile.v0.1.json`, and
  `token-profile.v0.1.json`. `Client.AcquireToken` performs admission with a
  fresh memory-only session signer, bounded deterministic Argon2id work, dual
  possession proofs, an exact `private_key_jwt` client-credentials exchange,
  and local JWKS/access-JWT verification.
- Admission limits cap memory, passes, parallelism, attempts, and elapsed time.
  The absolute access-token lifetime is the authoritative 300 seconds.
  `SessionToken` keeps the bearer value private and redacts formatting; callers
  use `AuthorizationHeader` only when constructing the protected request.
- Cross-repository validators use `KADO_PHASE_02B_ADMISSION` and
  `KADO_PHASE_02B_TOKEN`. Node's strip-only TypeScript runtime requires a
  temporary import-specifier/parameter-property adaptation; the test copies
  authoritative sources to a private temp directory without changing them.
- Phase 02B Goal 5 publishes
  `contracts/agent-auth/v0.1/credential-profile.json`; its pinned Go copy is
  byte-identical. Credential status and current-installation revocation use an
  exact `agent-credential+jws` management-key proof with a fresh replay nonce,
  endpoint binding, issuer, time bounds, and JTI.
- Server `200` status results contain only status plus opaque
  principal/credential/client identifiers. Revocation deletes the local
  management key only after an authoritative `revoked` result. A failed server
  or protocol exchange retains the key; a failed local deletion returns
  `ErrCredentialCleanup`, retaining the key so a retry can receive the
  tombstone-backed idempotent `revoked` result and finish cleanup.
- `kado auth status` and `kado auth revoke` initialize config, network, and
  keychain access lazily. Their output is restricted to validated opaque
  identifiers and state; private JWKs, assertions, bearer tokens, backend
  errors, and filesystem paths never reach ordinary output.
- File and keychain mutations share one per-record cross-process lock.
  Revocation retains an opaque snapshot of the management-key record and uses
  `Store.DeleteIfMatches` after server confirmation. A stale revoker can never
  delete a concurrently installed replacement; it returns
  `ErrCredentialChanged`, leaves the replacement intact, and asks the caller to
  retry if that newer installation should also be revoked.

## Phase 04A Search lifecycle client

- `internal/searchclient` owns the protected `/search` HTTP boundary. It sends
  only `Accept: application/vnd.kado.search.v1+json`, rejects redirects and
  foreign issuer/host links, bounds document/error bodies, preserves exact
  canonical response bytes, and validates every accepted document before it
  reaches lifecycle handling or output.
- The client derives status/clarify/cancel/retry operations from the
  server-provided `links.self` identity and follows pagination relation URLs
  exactly. It never reconstructs a cursor/page relation or follows a relation
  outside the configured HTTPS resource. Initial documents and every consumed
  lifecycle/pagination relation must retain the exact requested canonical
  query; same-origin links for a different query fail before another request.
- `kado search` reuses Phase 02C management-key enrollment and verified
  short-lived tokens. Admission now requests the exact sorted Phase 03A scope
  set: `search:cancel search:create search:read search:refine`.
- One rejected bearer request can force one token refresh. Only GET operations
  receive bounded transport/502/503/504 retry; form-encoded lifecycle mutations
  are not blindly replayed. Every bounded JSON response body is checked for
  current bearer reflection both as raw bytes and across all decoded nested
  string tokens before refresh, retry, diagnostics, or document output.
  Escaped credentials, malformed JSON, invalid/unpaired UTF-16 surrogates, and
  reflected credentials all fail closed without changing the existing
  document/error byte ceilings.
- `searchclient.Run` supports polling, clarification callbacks, one explicit
  retryable-failure retry, opaque pagination, explicit cancellation, and
  bounded cancellation after a local deadline or interrupt. Client-level
  lifecycle-operation and clarification-submission budgets remain enforced
  when `RunOptions.Timeout` is zero; a blocking clarifier is isolated so
  cancellation cannot hang `Run`.
- `internal/searchcontract` pins the released Search Document v1 manifest hash
  outside generated code, verifies every manifest artifact checksum, and
  embeds deterministically generated copies of the authoritative JSON Schema
  2020-12, JSON-LD 1.1 context, semantic-rule manifest, and all lifecycle
  fixtures from the sibling `kado-app` repository. `go generate
  ./internal/searchcontract` fails unless the source release matches that pin.
- Contract acceptance rejects duplicate JSON members, trailing data, invalid
  UTF-8, unsupported envelopes, schema drift, all 18 released semantic
  invariants, and non-canonical JSON-LD expansion/compaction. JSON-LD context
  loading is local-only. Unsupported majors retain a distinct bounded error
  containing only the parsed major number.
- JSON-LD 1.1 compaction has no `@container` for `result_set.items`; the
  authoritative processor therefore emits an empty array for zero items, the
  item object directly for one item, and an array for many. Semantic validation
  normalizes those cardinalities only after canonical expansion checks and
  preserves arbitrary `data` objects, arrays, scalars, and null.
- `internal/searchoutput` validates every page before returning any bytes.
  `--json` returns one isolated byte-for-byte canonical server document and
  disables pagination following. Human output is deterministic, terminal-safe,
  display-width-aware, and bounded. JSONL is a deterministic bounded
  projection with explicit pagination/link records and preserves each
  arbitrary `data` value as raw object, array, scalar, or null JSON.
- CLI output is rendered completely before one write. A broken pipe exits
  silently and successfully; other writes fail with a safe diagnostic.
  `diagnostic.TerminalSafeText` is the shared bounded sanitizer for terminal
  projections, Search Document failures, HTTP problems, and stderr; it removes
  C0/C1, format/bidi controls, and Zl/Zp while preserving ordinary Unicode.
  Cross-repository asset checks, released lifecycle fixtures, human/JSONL
  goldens, and exact JSON checks cover both cursor and page pagination.
