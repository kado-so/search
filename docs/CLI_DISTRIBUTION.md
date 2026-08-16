# Kado CLI Distribution Plan

This document records the intended build, installation, update, and bundled
skill model. It distinguishes the behavior already present in the repository
from work required before the first production release.

## Product decisions

- Direct agent installation is the primary path.
- Installation must not require Homebrew, npm, WinGet, Scoop, or a language
  runtime.
- Package managers are optional manual channels.
- The release is built with the exact Go toolchain in `go.mod`.
- Direct installations are self-updating.
- Package-manager installations remain managed by their package manager.
- The complete active skill catalog is bundled with the CLI and tested as one compatible unit.
- Windows direct update uses a helper handoff; it does not rename the running
  executable in-process.

## Release pipeline

Continuous integration runs on pull requests and `main`:

1. install the Go version from `go.mod`;
2. run `go test ./...`;
3. run `go vet ./...`;
4. check formatting and generated files; and
5. cross-compile every supported target with GoReleaser.

A production release begins when an operator pushes a semantic `vX.Y.Z` tag.
Merging to `main` runs CI but does not publish. The protected release workflow:

1. derive the version and source commit from the pushed tag;
2. require the tagged commit to be contained in protected `main`;
3. reject an existing GitHub Release for that version;
4. derive `SOURCE_DATE_EPOCH` from the commit;
5. enter a reviewer-protected release environment;
6. build all six targets once with the pinned GoReleaser version;
7. validate every signed artifact before publication;
8. verify all bundles and smoke-test on native runners;
9. expose the canonical build as one short-lived Actions artifact; and
10. publish the GitHub Release from that exact downloaded Actions artifact.

Publication from the GitHub Release to `kado.so` is a separate workflow owned
outside this repository. It uploads immutable versioned artifacts, verifies
their public bytes, and promotes signed stable/latest metadata last.

Versioned paths are immutable:

```text
https://kado.so/install/releases/<version>/<artifact>
```

The stable channel is promoted only after every referenced artifact is public:

```text
https://kado.so/install/releases/stable/release-metadata.json
https://kado.so/install/releases/stable/release-metadata.json.sig
```

Rollback republishes a previous release's existing signed metadata and
signature to the stable channel.

## Agent bootstrap

The install page returns a small machine-readable descriptor as well as human
instructions. It identifies the supported targets, immutable download URLs,
sizes, SHA-256 digests, release signature, and signing public key.

An agent:

1. identifies `darwin`, `linux`, or `windows` and `amd64` or `arm64`;
2. downloads the matching release with its own HTTPS capability or an
   operating-system-provided client;
3. places the candidate in a private temporary directory;
4. verifies the downloaded release;
5. installs into a user-writable destination, or invokes the signed updater
   when that destination already contains the Kado executable;
6. executes `kado skill install`;
7. creates or reuses authentication and verifies it with `kado auth status`;
   and
8. reports installation completion.

The bootstrap is the root-of-trust transition. The install descriptor and
public key must be served from canonical HTTPS without redirects. Once the
candidate runs, the existing `kado release verify` boundary authenticates the
complete release bundle.

The final agent experience should be one instruction, not a package-manager
choice:

```text
Install Kado Search from https://kado.so/install
```

## Bundled skill catalog design

The CLI embeds a monotonically revisioned catalog, every active skill variant
and its assets, canonical versions and content digests, and installation
adapters for supported agents.

The embedded copy is an offline fallback tested with the CLI. The authoritative
copy is independently published as signed metadata plus a safe `tar.gz` at:

```text
https://kado.so/install/skills/latest/catalog.json
https://kado.so/install/skills/latest/catalog.json.sig
https://kado.so/install/skills/<name>/<variant>/<version>/metadata.json
https://kado.so/install/skills/<name>/<variant>/<version>/metadata.json.sig
https://kado.so/install/skills/<name>/<variant>/<version>/<name>.tar.gz
```

The signed catalog authenticates additions and retirements. Each variant also
has independently signed metadata and a digest-verified archive. The CLI
selects an exact agent variant before the default `*` variant and reconciles
each skill independently.

Kado writes a small ownership record next to each installed skill containing:

- CLI release version;
- skill version;
- installed content digest;
- target agent;
- scope and destination; and
- installation timestamp.

Kado scans every known product-specific destination plus the portable
`~/.agents/skills/kado-search` destination. It reconciles an unregistered copy
only when the receipt names the canonical agent and the SHA-256 digest of the
actual relative paths and file bytes matches the receipt. For previously
registered copies, the registry and receipt must also match exactly. A moved,
deleted, locally modified, or externally managed skill causes a conflict report
and is never overwritten silently.

After a normal CLI invocation finishes, it schedules one process-wide
`kado skill update --background` worker when the six-hour, jittered check is
due. The worker refreshes every unmodified Kado-managed skill destination and,
for a direct launcher installation, downloads, verifies, and activates a newer
CLI payload. Concurrent invocations atomically reserve one worker slot.

`kado update` and skill maintenance have separate transactions:

1. install and verify the new immutable CLI payload;
2. schedule a compatible signed skill refresh;
3. report any skill conflicts or failures; and
4. retain the successfully verified CLI even if a skill needs manual repair.

## Installation ownership

Every installed executable needs an installation channel:

- `direct`
- `homebrew`
- `winget`
- `scoop`
- `deb`
- `rpm`
- `container`

Direct installers write a protected adjacent receipt. Packages stamp the
channel at build time or install their own non-direct receipt. A missing receipt
is accepted only by a canonical direct release as a legacy migration case; an
invalid or explicitly non-direct receipt disables launcher management.

For a direct installation, `kado update` installs a signed immutable payload
and appends an activation record. Automatic maintenance uses the same path.

## Launcher and Windows migration

The executable on `PATH` is a stable launcher. The same signed release binary
acts as both the launcher and payload, which keeps release protocol v1 and its
archive layout compatible with already-released clients. Payloads live under
`kado[.exe].d/versions/<version>/`; immutable activation records select the
newest complete payload. The launcher selects once at process start. Unix uses
`exec`, while Windows waits as the payload's console parent and returns its exit
status.

The first explicit update from the legacy Windows layout still needs the
existing helper because Windows locks the legacy running executable:

1. the running CLI downloads and verifies the candidate;
2. it writes the candidate next to the installed executable;
3. it starts the candidate in an internal `update-helper` mode with the parent
   PID, expected target digest, and an unguessable transaction token;
4. the original process exits;
5. the helper waits for that exact parent process to terminate;
6. it revalidates the target and candidate;
7. it moves the old executable to a rollback path and the launcher-capable
   candidate into place;
8. it starts `kado version --json`, which bootstraps and selects the immutable
   payload;
9. it restores the rollback on failed verification; and
10. it removes the helper and rollback on success.

After migration, Windows updates never replace the stable launcher and do not
need the helper. OS-backed update locks disappear after a crash. Old payload
cleanup is best effort because an overlapping Windows process may still hold
one open.

The helper is implemented in the release client and cross-compiles for both
Windows targets. The release workflow must pass its native locked-file and
replacement smoke tests before publication.

## Secondary channel list

Priority order:

1. agent-first direct installation from `kado.so`;
2. direct manual archive download;
3. Homebrew tap for macOS and Linux;
4. WinGet;
5. Scoop;
6. GitHub Releases mirror;
7. Debian repository;
8. RPM repository;
9. container image;
10. external Agent Skills, Codex, and Claude marketplaces.

npm is not a primary CLI channel because Kado is a native executable and
should not require Node. It remains useful as an optional skill-manager
transport through `npx skills`.

## Implementation status

Implemented in this repository:

- Go 1.26.4 pinning without Proto;
- embedded offline skill and signed authoritative skill archives;
- `kado skill install|status|update|uninstall`;
- parent-agent installation, additional-agent discovery, ownership receipts,
  and conflict-safe atomic refresh;
- asynchronous skill refresh and cached CLI update availability notices;
- agent-first Unix and Windows installers;
- Windows verified-candidate update handoff;
- Depot CI, GoReleaser construction, native smoke jobs, and GitHub
  Releases.

Required outside or after this repository change:

- implement the separate publisher that copies GitHub Release assets to the
  documented `kado.so` paths and promotes stable metadata last;
- configure the production signing seed in the existing protected
  `cli-release` GitHub environment;
- expand native Windows update tests for locked files, rollback, and interrupted
  cleanup;
- add macOS signing/notarization and Windows Authenticode packaging before broad
  manual distribution.
