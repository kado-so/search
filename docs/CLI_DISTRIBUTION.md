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
- Windows direct update leaves the stable launcher untouched and publishes a
  complete immutable executable-pair activation.

## Release pipeline

Continuous integration runs on pull requests and `main`:

1. install the Go version from `go.mod`;
2. run `go test ./...`;
3. run `go vet ./...`;
4. check formatting and generated files; and
5. cross-compile every supported Kado/A2A executable pair with the pinned Go
   toolchain and deterministic flags.

A production release begins when an operator pushes a semantic `vX.Y.Z` tag.
Merging to `main` runs CI but does not publish. The protected release workflow:

1. derive the version and source commit from the pushed tag;
2. require the tagged commit to be contained in protected `main`;
3. reject an existing GitHub Release for that version;
4. derive `SOURCE_DATE_EPOCH` from the commit;
5. enter a reviewer-protected release environment;
6. verify the locked official A2A source, build all six A2A targets first, and
   then build all six matching Kado targets with their exact sidecar identity;
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
channel at build time. A missing receipt is accepted only by a canonical
direct release as a legacy migration case; an invalid or explicitly non-direct
receipt disables launcher management.

For a current direct installation, `kado update` installs a signed immutable
Kado/A2A executable pair and appends an activation-v2 record that authenticates
both members. Automatic maintenance uses the same path.

Package-managed `kado update` and `kado uninstall` calls fail before release
or credential state is initialized. The diagnostic names the owner and exact
manager command. Container builds instead direct the caller to the deployment
tool because Kado cannot know whether Docker, Podman, Kubernetes, or another
orchestrator owns that image.

All channels use one runtime rule: canonicalize the real Kado executable and
accept only the regular, non-symlink `kado-a2a` sibling whose bounded size and
SHA-256 match the values stamped into Kado. No channel searches `PATH`, the
current directory, or environment overrides for the sidecar.

## Launcher and pre-A2A migration

The Kado executable on `PATH` remains the stable launcher. Immutable payloads
live under `kado[.exe].d/versions/<version>/` as complete `kado` and `kado-a2a`
pairs. Activation v2 is the logical commit: it records the exact relative path,
bounded size, and SHA-256 digest of each role. Readers select the newest valid
pair without locking and fall back to an older complete pair after corruption
or interruption.

There is no automatic compatibility bridge from a pre-A2A direct install. Its
old updater understands only the previous single-executable release contract.
The supported migration is to close every Kado process, use the current signed
uninstaller without credential purge, and run the current signed installer.
The uninstaller removes executables and managed activation state but preserves
configuration, identities, and credentials by default.

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
- Depot CI, deterministic paired release construction, native smoke jobs, and
  GitHub Releases;
- closed direct/Homebrew/WinGet/Scoop/deb/rpm/container build identities;
- manager-native Homebrew, Scoop, and WinGet definitions plus Debian, RPM, and
  container build definitions, backed by compact signed-checksum package
  artifact sets; and
- six-target package-pair release gates covering canonical links/junctions,
  lifecycle refusal, tamper rejection, repair, and unchanged pair hashes.

Required outside or after this repository change:

- implement the separate publisher that copies GitHub Release assets to the
  documented `kado.so` paths and promotes stable metadata last;
- configure the production signing seed in the existing protected
  `cli-release` GitHub environment;
- expand native Windows update tests for locked files, rollback, and interrupted
  cleanup;
- add macOS signing/notarization and Windows Authenticode packaging before broad
  manual distribution.
