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

## Phase 04A Kado Search skill

- `skills/kado-search/SKILL.md` is the repository's only canonical skill
  instruction source. It invokes the installed `kado` CLI exclusively; API
  keys, device login, direct HTTP, browser credentials, copied tokens, and
  temporary auth scripts have no fallback path.
- Progressive details live one level below the skill in `query-guide.md`,
  `cli-guide.md`, and `response-guide.md`. The lifecycle guide owns output-mode
  selection, clarification, bounded retry, pagination, timeout/cancellation,
  installation, and safe authentication troubleshooting. Normal agent
  recommendation/synthesis uses lossless multi-page `--jsonl`; human output is
  only for quick/operator inspection and `--json` is one exact document.
- `tests/test_kado_search_skill.py` validates skill/frontmatter/reference
  structure, single ownership, obsolete-flow absence, OpenAI metadata,
  representative trigger/lifecycle evaluations, and a built CLI flag smoke.
  Run it with Python `-B`; repository-scoped ignores cover test bytecode caches.
  README validation uses a `command -v`-resolved interpreter and disposable
  venv so both system validators receive PyYAML without a repository dependency.

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

## Phase 04A distribution manifests

- `distribution/kado-search.manifest.json` is the only source for plugin/skill
  display metadata, semantic version, icons, marketplace policy, homepage, and
  exact plugin/marketplace/repository identities. Its Draft 2020-12 schema is
  `distribution/kado-search.manifest.schema.json`.
- Install/uninstall commands are not accepted as manifest strings. The
  generator derives exact literal token tuples from `kado-search`, marketplace
  `kado`, and GitHub source `kado-so/search`, validates every token against a
  shell-metacharacter-free grammar, and only then joins them for generated
  documentation.
- Draft 2020-12 validation is mandatory before generation or drift checking.
  The documented isolated environment installs
  `tools/requirements-validation.txt`; missing `jsonschema` fails with that
  instruction rather than skipping the schema gate.
- `tools/generate_distribution_manifests.py` generates/checks the Codex plugin
  and marketplace, Claude plugin and marketplace, OpenAI skill metadata,
  Agent Skills frontmatter, and `distribution/INSTALL.md`. It preserves the
  canonical `skills/kado-search/SKILL.md` body rather than creating another
  instruction copy.
- Both marketplaces intentionally use the repository root source `./`. Codex
  and Claude clean-install tests prove that path, and it packages the one
  cross-platform `skills/kado-search` owner without duplicated bodies or
  out-of-package symlinks.
- The stable plugin ID is `kado-search@kado`. Plugin skill namespaces are
  `kado-search:kado-search` in Codex and `/kado-search:kado-search` in Claude;
  standalone Agent Skills installs retain `kado-search`.
- Generated Claude metadata uses the conservative fields accepted by the local
  supported Claude validator as well as the current specification. The current
  Agent Skills source still records the installed-CLI compatibility condition,
  but generated frontmatter omits the optional `compatibility` field because
  the bundled older validator rejects it; the canonical skill body and install
  reference retain the requirement and `https://kado.so/install`.
- `KADO_DISTRIBUTION_INSTALL_SMOKE=1 python3 -B -m unittest
  tests.test_distribution_manifests.DistributionInstallSmokeTests -v` builds a
  temporary `kado` and proves isolated Codex, Claude, and Agent Skills
  install/discovery/uninstall flows. The release builder stamps the same source
  version into binaries and owns binary URLs, checksums, update, and CLI
  uninstall behavior.

## Phase 04A CLI releases

- `.prototools` pins Go 1.26.4. `tools/release` refuses a different toolchain
  and reads version, repository, executable, and install URL only from
  `distribution/kado-search.manifest.json`.
- One deterministic build produces direct versioned binaries and safe
  `tar.gz`/ZIP archives for Linux, macOS, and Windows on amd64 and arm64.
  `CGO_ENABLED=0`, fixed source time, `-trimpath`, disabled VCS auto-stamping,
  an empty Go build ID, canonical JSON, and fixed archive headers make repeat
  builds byte-identical.
- Release output includes SHA-256 checksums, per-target SPDX 2.3 SBOMs, one
  SLSA v1/in-toto provenance statement, canonical `kado.release.v1` metadata,
  a detached Ed25519 signature, the public verifier, and generated local
  install/uninstall instructions and scripts. The builder self-verifies every
  target before atomically exposing the output directory.
- `KADO_RELEASE_SIGNING_KEY` is the only signing input. It is a base64 Ed25519
  seed read from the environment, excluded from build subprocess environments,
  never accepted as an argument or printed, and not present in the repository.
  CI dry runs generate one ephemeral key at runtime; production signing is a
  separately documented protected-secret boundary and CI does not publish.
- Release binaries stamp version, commit, UTC time, target, signing-key ID,
  signing public key, and stable metadata URL. `kado version --json` exposes
  deterministic non-secret provenance. The candidate's bundled
  `kado release verify --directory` validates the downloaded local bundle
  without requiring OpenSSL or another platform verifier.
- Release protocol v1 deliberately has no in-band signing-key rotation. The
  embedded key must match both signed metadata and the candidate binary; a key
  change requires an out-of-band reinstall from the reviewed official install
  boundary.
- `kado update` accepts only same-origin HTTPS metadata/assets, rejects
  redirects and size overflows, verifies the trusted signature, checksums,
  SBOM/provenance, archive paths/types/modes, and candidate executable before a
  same-directory atomic replace. An exclusive per-executable lock serializes
  replace/uninstall. Failures before commit restore the prior binary and
  verified candidate; post-commit cleanup failures retain the verified new
  binary and never expose an empty or corrupt executable. Downgrades require
  `--allow-downgrade`; `--dry-run` performs the full verification without
  writes, including when already current.
- Generated install scripts refuse to overwrite an existing binary so they
  cannot bypass update/downgrade policy. Generated uninstall scripts and
  `kado uninstall --yes` remove only the executable. Credentials are preserved
  unless `--purge-credentials` is explicit, in which case authenticated
  revocation must succeed before removal.
- `.github/workflows/cli-release.yml` runs Go test/vet/race on Linux, macOS,
  and Windows. Every platform builds a generated signed bundle, executes its
  native installer and uninstaller (including parsed PowerShell), verifies the
  installed candidate and bundle, preserves a credential sentinel, and updates
  between real signed native binaries. A separate job validates Goal 4
  metadata, builds all six targets twice with one ephemeral key, and compares
  every byte. CI never uploads or publishes output. `docs/RELEASING_CLI.md`
  owns the signing, reproducibility, verification, rollback, downgrade,
  uninstall, and publication boundary.

## Phase 06A autonomous-agent client hardening

- `Limits` now bounds total response-header bytes in addition to response
  bodies, HTTP time, proof/challenge/session/token lifetimes, clock skew,
  Argon2 memory, passes, parallelism, attempts, and elapsed solving time.
  Standard transports are cloned with `MaxResponseHeaderBytes`; custom
  transports still receive an application-level aggregate header check.
- Client limit validation includes the aggregate Argon2 memory-work ceiling.
  The solver checks cancellation and elapsed time before and after every
  derivation, clears rejected derived keys, and never accepts server parameters
  above the configured memory/pass/parallelism maxima.
- Access-token verification now requires exact `nbf = iat`, applies the same
  30-second clock-skew rule as the TypeScript verifier, and retains exact
  issuer/audience/principal/client/session/mode/scope/JTI/lifetime and Ed25519
  `kid`/`typ` checks. The pinned cross-language token profile records `nbf`, the
  clock-skew maximum, the trusted previous-key schedule fields, timed JWKS
  omission, and the 360-second active/previous signing-key overlap rule.
- OAuth token exhaustion is pinned as the extension error
  `rate_limited` with HTTP 429 and is exposed to callers as
  `ErrTokenRateLimited`; a status/code mismatch remains a generic
  authentication failure.
- Bounded fuzz targets exercise strict JSON decoding and attacker-controlled
  challenge parameters. Token flow tests cover not-before tampering and future
  clock skew; formatting tests continue to prove that bearer/session
  credentials never appear through `String`, `GoString`, or error rendering.
- The Search repository remains the sole owner of the `kado-search` skill. Its
  pinned validator suite proves that API-key, device-flow, copied-token,
  browser-cookie, and direct-HTTP implementations are absent from the skill.
## Phase 05 installation description

- `distribution/kado-installation.v1.gen.json` is the one machine installation
  description owned by this repository. It is generated by
  `tools/generate_distribution_manifests.py` from the Phase 04A distribution
  identity plus the versioned Draft 2020-12 schema at
  `distribution/kado-installation.v1.schema.json`.
- The description owns supported agent/plugin targets, six CLI platforms,
  exact tokenized install/removal operations, explicit approval policy,
  capability summary, release-integrity discovery, and canonical Search/auth
  links. `distribution/INSTALL.md` derives its human instructions from those
  same command arrays and release state.
- `kado-installation.v1.manifest.gen.json` binds the exact description, schema,
  and Phase 04A distribution source bytes by SHA-256 and size. The CLI release
  remains explicitly `unpublished`; consumers must not invent concrete release
  artifacts or claim installability until signed metadata is published.
- The schema encodes exact agent identities/commands and the complete six-entry
  platform matrix with `const`/`contains` constraints where Draft 2020-12 can
  express them. Generator semantic validation additionally compares the full
  description with its source-derived canonical value, covering uniqueness,
  approval requirements, owner URLs, and source artifact SHA-256/size.
- The paired app worktree consumes byte-identical generated copies through
  `tools/installation-description/generate.ts`. Run its check with
  `KADO_SEARCH_REPOSITORY` for explicit cross-repository equivalence.

## Phase 05 literal installation evaluation

- `tests/fixtures/install-prompt-matrix.v1.json` is the reproducible Goal 5
  matrix. Its validator rejects unknown versions/labels/fields, weak
  thresholds, duplicate IDs, incomplete hosts/scenarios, modified prompt
  variants, missing literal-host coverage, and absent negative cases. The
  matrix expands to 48 cases across Codex, Claude Code, Agent Skills, and an
  Agent Skills-compatible host.
- `tools/install_prompt_evaluator.py` defines the shared `HostAdapter`
  transcript protocol. The deterministic adapter returns content-addressed,
  non-secret captured events; the outcome parser receives no scenario, fault,
  expected-outcome, or pass labels. Evidence validation replays every
  transcript and independently recomputes actual outcomes, pass flags, rates,
  thresholds, full host/scenario coverage, canonical package/version values,
  publication digests, and exact installed lifecycle operations/checks.
- The published evaluation fixture goes through the Goal 4 byte pipeline:
  schema release consts, regenerated description bytes, source-integrity
  rebinding, manifest digest/size rebinding, Draft 2020-12 validation, and a
  publication digest. It rejects every non-release semantic change and is not
  a bare in-memory availability toggle.
- Before that transition, publication construction calls the shared
  `generate_distribution_manifests` source-schema, source-semantic, and
  installation-description validators and requires byte equality with its
  generated description/manifest plus the exact canonical Goal 1 schema and
  distribution source. Recomputed hashes cannot bless schema title/keyword
  drift, arbitrary source bytes, foreign integrity/manifest URLs, path or owner
  changes, or product/version/skill identity drift. Evaluation and evidence
  validation both reconstruct and compare the exact published pair.
- The current canonical source remains `unpublished` and
  `installable: false`; all 16 current-source cases correctly end
  `non-installable` with zero install operations. The published fixture
  requires explicit approval before modeling plugin/skill installation,
  Phase 04A
  signature/checksum/provenance/SBOM verification, CLI status/version, skill
  discovery, update guidance, and credential-preserving uninstall guidance.
- Evidence validates against
  `tests/fixtures/install-prompt-evidence.v1.schema.json`. The agreed threshold
  is >=95% overall, 100% for literal-host cases, and 100% for safety-critical
  cases. The evaluated matrix achieved 48/48 and 100% in every category.
  `docs/INSTALL_PROMPT_EVALUATION.md` documents reproduction and safety
  boundaries.
- `tools/live_install_prompt_evaluator.py` is separately gated and clearly
  labels live isolated deny-mode evidence. Codex receives the exact prompt
  ephemerally under a read-only sandbox; Claude receives it in bare plan mode
  with all tools disabled. Both discard raw responses after hashing and feed
  strict signals into the same transcript parser. Agent Skills is accurately
  reported as lacking a free-form prompt interface. In this shell both API-key
  environment variables were absent: Codex submitted the prompt but returned
  an unusable response, Claude reported authentication unavailable, and Agent
  Skills reported unsupported prompt interface. Those isolated attempts
  granted no approval and performed no installation.
- A separate content-addressed Codex capture records the exact imperative
  prompt as valid user consent. It installed only the skill; the required CLI
  remained unavailable because the canonical release is unpublished, and the
  CLI status/version check failed. The derived result is
  `partial-installation`; removal of the globally installed test skill
  completed and its absence was verified. Codex remains a correctness failure.
  The parser requires that exact ordered sequence and rejects reordered,
  duplicated, missing, extra, or timestamp-augmented events even after the
  capture digest is recomputed. No raw response, local path, credential,
  timestamp, or signing material is retained. The live gate remains **NOT
  MET**, and deterministic replay is never counted as live success.
- Actual isolated local-source smokes passed for installed Codex 0.144.5,
  Claude Code 2.1.92, and Agent Skills through `npx` 11.12.1. Each used a
  temporary home/project, discovered the canonical package and skill, checked
  version 0.1.0 and CLI compatibility, and uninstalled cleanly. No real host
  configuration or external release was changed.
- Full post-hardening search validation passed: 44 Python tests (3 opt-in
  package smokes skipped in the ordinary run), all Go packages including signed
  release install/update/downgrade/tamper/rollback/uninstall coverage, the
  skill validator, Codex plugin validator, and both Claude manifest validators.
  The three opt-in clean-host package smokes also passed separately.

## Phase 07 Goal 1 cutover qualification

- Search commit `cfc6b7d6d77e9222e5bdcde61051358abc4959c8`, tree
  `ec29b9b4b392777d2bb33acb1c0977bc09b2fb2e`, is the exact frozen client,
  skill, plugin, and distribution source paired with app product commit
  `440369b309d2bab37dc4e27f14e5eb40613cd63e`, tree
  `28a51a715614cd0f68dac37379af07dbd20bf82d`. The app's Phase 07 kickoff
  commit is evidence-only and not a source input.
- The app-owned generated qualification inventory binds Search Document v1,
  all five byte-identical agent-auth profile pairs, every Search-owned
  distribution/plugin/skill metadata artifact, all phase evidence, and the
  Search-to-app installation copies. The qualified module is
  `github.com/kado-so/search`, CLI `0.1.0`, with the exact Go `1.26.4`
  toolchain. Installation remains truthfully `unpublished` and
  `installable: false`.
- Serial `go test -count=1 ./...`, `go vet ./...`,
  `go test -race -count=1 ./...`, and a temporary-output CLI build pass.
  `KADO_NATIVE_RELEASE_SMOKE=1` also passes. One release-client provenance case
  failed only while the ordinary and race suites were run concurrently; its
  targeted rerun and the complete serial suite passed immediately.
- Distribution generation, the canonical skill validator, plugin validator,
  44 Python tests, and 3 separately enabled clean-host Codex/Claude/Agent
  Skills installation smokes pass. The ordinary Python run intentionally skips
  those 3 opt-in smokes. Regenerating the Go Search Document assets from the
  paired app contract produces no diff.
- Two release builds from the frozen commit and its committed source epoch used
  one ephemeral Ed25519 seed and produced byte-identical artifacts for all six
  Darwin/Linux/Windows amd64/arm64 targets. Both bundles verified as CLI
  `0.1.0`; temporary keys, binaries, and release directories were removed.
  Nothing was published, pushed, installed globally, or deployed.
- App-side qualification passed the final transformed Search suite with 624
  tests and 60 intentional integration skips, Search admin with 117 tests and
  25 database-gated skips plus 2 telemetry tests, and the twice-run visual
  matrix with 62 passes and 46 intentional viewport/project skips per run.
  The sparse app baseline still lacks its root TypeScript config and retains
  previously recorded typecheck/build failures; those are explicitly not
  described as full-CI success.
- Phase 05 live installation remains pending for Phase 07 Goal 4. External
  configuration/recovery drills remain deferred to Goal 3 and production
  observation drills to Goal 5. No Goal 2 acceptance, publication, release,
  deployment, reset, or agent-to-user linking work was performed.
