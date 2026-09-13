# Complete Kado bundle contract (G8)

Search owns this release/installation format. MCP owns the private maintenance
protocol and home registration. G9 connects the public MCP dispatcher and channel
installers to these APIs. No production publication or activation-v2 migration is
part of G8.

## Authenticated unit

- `kado.release.v3` signs six target archives, their payload-manifest descriptors,
  A2A hashes, per-target Node archive identity, SPDX documents and provenance.
- `kado.payload.v1` (`bundle.gen.json` plus its Ed25519 signature) inventories
  every file, size, SHA-256 and portable mode. Fixed roles identify Kado, A2A,
  private Node, MCP CLI, bridge, maintenance entry and native host.
- `kado.version.v2` adds the exact MCP source version/commit, dependency lock hash
  and Node version/archive hash to executable identity. Candidate verification
  checks these against the signed release before activation.
- Limits: 20,000 files, 8 MiB manifest, 256 MiB individual file, 768 MiB expanded
  content, 384 MiB compressed archive. Archives list manifest and signature first,
  then files in manifest order. Only regular files are accepted. Traversal, links,
  reparse points, duplicate/case-colliding paths, Windows path aliases, unlisted
  files/directories, wrong modes and incomplete payloads are rejected.
- The complete tree is verified before selection or maintenance execution.
  A damaged native addon or JS dependency invalidates the entire version.
  Node never comes from PATH; injection-related runtime/module/loader environment
  variables are removed and `--no-global-search-paths` is supplied.

The older pair schemas remain for existing historical tests and callers. The
successor installer never migrates or selects them. A successor executable cannot
bootstrap an activation-v2 pair. Bare candidate `version --json` and
`release verify --directory` remain available for installer verification; ordinary
commands require a verified extracted unit or a complete managed installation.

## Build inputs

On a clean committed MCP checkout, after a frozen dependency install:

```text
node scripts/packaging/build.mjs <node-platform>-<arch> <new-output> --release-component
```

The builder checks the pinned toolchain and recompiles from clean output. It uses
the frozen Node/native-addon input checksums, includes licenses/NOTICE, records
the materialized package inventory, and excludes qualification peers. It does
not publish. Qualification builds without this flag retain the controlled peers.

The Search release builder now requires:

```text
--mcp-components <directory> --mcp-commit <exact-40-character-commit>
--mcp-digests <six-target-json-index>
```

Component directories are named `windows-amd64`, `windows-arm64`, `darwin-amd64`,
`darwin-arm64`, `linux-amd64`, `linux-arm64`. The independently supplied digest
index maps `OS/ARCH` to the SHA-256 of that runner's `payload.gen.json`. Component
content cannot supply its own trusted digest. The builder rejects mismatched
commits, hashes and cross-target source/runtime versions, stamps component
identity into Kado, and signs the resulting complete archives. The SPDX inventory
includes materialized npm packages, Node, native files and all payload files;
provenance binds MCP commit, component input digests and final manifest digests.

## Activation and recovery

`releaseclient.Manager.Install` is the fresh direct-install API. `Update` handles
subsequent new-format updates/repair and explicit downgrade. G9 wires channel
scripts to these operations. Package-managed receipts continue to refuse direct
lifecycle operations.

```text
kado[.exe]                         stable launcher, published last on fresh install
kado.install.json                  direct ownership receipt (schema 1)
kado[.exe].d/
  versions/<version>/              immutable complete tree
  activations-v3/<generation>.json version and signed-manifest digest
  identities-v3/<version>.json     persistent same-version collision ledger
  registered-homes-v1.json         all homes that started installed bridges
  sessions-v1.lifecycle*           MCP registration/maintenance gate
```

An OS update lock serializes writers. Files and directories are staged and synced
before version publication; the activation rename is the logical commit. Readers
select the newest valid whole unit. Two valid versions and all session-referenced
versions are retained. Interrupted staging is unselectable and later cleaned up.
The identity ledger survives pruning. Signed version/activation evidence also
prevents changing a version's bytes if publication was interrupted before its
ledger write. Repair uses identical authenticated bytes and refuses to replace a
session-referenced damaged version. Rollback changes only the active payload;
credentials and state remain current.

Windows maintenance runs from a separate verified temporary tree, so its private
Node executable does not block replacement of the candidate directory.

## All-home session protection

Installed bridge startup takes session lifecycle → installation gate → home
maintenance gate. Before spawning it durably registers its canonical state home
in the installation inventory. Temporary staging payloads cannot start sessions.

The installer executes only its verified maintenance role. A bounded private
stdio request selects `hold` or `close-hold`, with component roots computed from
the trusted installation layout. Maintenance acquires the installation gate,
reads every registered home and holds all home gates until the parent releases
stdin. An unreadable/missing/corrupt inventory or owner record blocks cleanup.
An empty/default home is never treated as evidence that other homes are unused.

Uninstall first requests authenticated owned-session close, before acquiring the
maintenance gates. It re-inspects under the gates and fails busy if references
remain. No persisted PID authorizes a signal. Foreign installations are excluded;
ambiguous paths into this installation block cleanup. Each deletion rechecks that
the maintenance process is alive. Busy files and incomplete cleanup are reported,
not silently ignored. A failed uninstall retains its format marker and cannot
fall into legacy bootstrap. Credentials, profiles and named-session configuration
are outside the removal boundary. The empty coordination directory and Unix
`.update.lock` may remain to preserve writer-lock identity across reinstall.

The lease protocol is private version 1. G9's public dispatcher must use these
verified roles and installation layout; it must not bypass home registration,
launch arbitrary paths read from state, or prune based only on the default home.
G9's Windows self-uninstall flow must arrange removal from an external installer
or verified helper after the invoking executables exit. The G8 API reports locked
files as busy; it does not claim success or schedule unchecked deletion.

## Validation

The generated Linux container image runs Kado under `tini`, which reaps exited
session processes. The package qualification executes the authenticated MCP
tutorial in the actual image; version-only smoke checks do not verify session
cleanup. Headless container credentials require an explicit protected-file store
or an available Secret Service, as described in the MCP usage guide.
One-shot containers are suitable for direct URL calls. Named sessions require a
long-lived container: run subsequent commands in that same container and close
sessions before stopping it. Persisting a profile volume does not keep a bridge
process alive after its container exits.

See [G8 validation](../../mcp/docs/G8_VALIDATION.md) for the actual local/native
evidence and the distinction between completed runs and the pending CI matrix.
