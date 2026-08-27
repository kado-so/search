# Kado CLI Release Boundary

The search repository owns CLI release construction and publication. Release
operators provide the exact semantic version; repository, executable, and
install URL are fixed in `tools/release`.

## Supported targets

Every release contains direct versioned binaries, versioned archives, an SPDX
2.3 SBOM per target, and one SLSA v1/in-toto provenance statement for:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`
- `windows/arm64`

Unix archives are `tar.gz` files. Windows archives are ZIP files. Each archive
contains exactly five root entries: `kado[.exe]`, `kado-a2a[.exe]`, `LICENSE`,
`LICENSE-A2A-CLI`, and `INSTALL-CLI.md`. Executables use mode `0755`; support
files use `0644`; timestamps and entry order are deterministic.

The release workflow also builds every package identity: `homebrew`, `winget`,
`scoop`, `deb`, `rpm`, and `container`. Homebrew receives both architectures
for macOS and Linux; WinGet and Scoop receive both Windows architectures; and
deb, rpm, and containers receive both Linux architectures. Package artifacts
live under `packages/<channel>/`. They deliberately do not reuse the direct
updater's six-target metadata contract. Instead, each compact package directory
contains only its supported paired archives, an Ed25519-signed checksum list,
the release public key, and the manager-native definition generated from the
exact archive URLs and hashes: `kado.rb`, `kado.json`, a clean three-file
WinGet `manifests/` directory, a Debian builder, RPM specs, or a container
Dockerfile. Each Kado executable is stamped with that closed channel while
retaining the exact sibling size and SHA-256.

## Signing boundary

Release metadata uses a detached Ed25519 signature. The production signing seed
exists only in the protected release environment as
`KADO_RELEASE_SIGNING_KEY`, encoded as one base64 32-byte seed. The builder
reads it from the process environment, never accepts it as an argument, and
never prints it. No private or test signing key is stored in this repository.

The corresponding public key and its SHA-256 key ID are non-secret. The builder
stamps them into every executable and writes `release-public-key.pem` for
independent verification. In-band signing-key rotation is deliberately
unsupported in release protocol v2: an installed binary accepts only metadata
signed by its embedded key. The metadata carries that key's ID, and the
replacement executable must carry a public key with the same derived ID.
Rotating the release key therefore
requires an out-of-band reinstall from the reviewed official
`https://kado.so/install` boundary. Existing binaries cannot self-update
across a key rotation, even when a release is signed by the old key.

Local dry runs use an ephemeral key. A production release must use the
protected production seed and must not reuse a dry-run key.

## Local dry run

Use the Go version pinned by the `toolchain` directive in `go.mod`. Provide a
Git checkout of the official A2A CLI containing the commit in
`third_party/a2a-cli/upstream.lock.json`. The release tool verifies the origin,
commit or tag, source/tree/patch checksums, license, module checksums, and shared
toolchain. It then builds each A2A executable first, hashes it, builds matching
Kado with that identity stamped in, and signs the combined release:

```bash
release_seed_file="$(mktemp "${TMPDIR:-/tmp}/kado-release-seed.XXXXXX")"
openssl rand -base64 32 >"$release_seed_file"
chmod 600 "$release_seed_file"
export KADO_RELEASE_SIGNING_KEY="$(tr -d '\n' <"$release_seed_file")"
go run ./tools/release \
  --version 0.1.0 \
  --commit 0123456789abcdef0123456789abcdef01234567 \
  --source-date-epoch 1784851200 \
  --a2a-source /absolute/path/to/a2a-cli \
  --out dist/release
```

To reproduce one package-owned candidate, add a closed channel and use an
isolated output directory:

```bash
go run ./tools/release \
  --version 0.1.0 \
  --commit 0123456789abcdef0123456789abcdef01234567 \
  --source-date-epoch 1784851200 \
  --a2a-source /absolute/path/to/a2a-cli \
  --install-channel homebrew \
  --out dist/release/packages/homebrew
```

Delete the temporary seed and dry-run directories after verification. The
builder requires absent or empty output directories and installs its complete
output directory with one rename, so a partial build never looks complete.

## Release contents and verification

`release-metadata.json` is canonical JSON and binds:

- version, source commit, UTC build time, and signing-key identity;
- exact A2A repository/module, tag or snapshot version, commit, source archive,
  source tree, patched tree, module files, license, toolchain, display patch,
  and build time;
- each platform archive and SPDX 2.3 SBOM by URL, size, and SHA-256;
- each embedded A2A executable by exact size and SHA-256; and
- the SLSA v1-shaped in-toto provenance descriptor without claiming a SLSA
  level.

`release-metadata.json.sig` authenticates those exact bytes. The archive digest
authenticates both executables and all three support files as one unit. Signed
SBOM and provenance descriptors authenticate the standalone supply-chain
documents. Direct Kado binaries, `checksums.txt`, the install guide, and
platform install/uninstall scripts remain standalone operator artifacts.

The generated `INSTALL-CLI.md`, `install.sh`, and `install.ps1` implement the
agent-first bootstrap from canonical `kado.so` HTTPS endpoints. For a new
installation they select the host target, download stable signed metadata and
the immutable versioned archive, run verification through the candidate, and
install the sidecar first and expose Kado last in a user-owned directory. When
a current paired Kado installation already occupies the expected destination,
they invoke its signed updater. A failed legacy update reports the required
one-time uninstall/reinstall migration. Both successful paths configure user
PATH when needed, reconcile the signed skill catalog across detected
harnesses and `~/.agents/skills`, and create or reuse and verify authentication.
`kado update` verifies the signed archive descriptor, safely extracts the
candidate, and checks its stamped release identity. A current direct
installation activates complete pairs from immutable version directories
without replacing the stable launcher.

## Runtime update and removal policy

`kado update` fetches only canonical same-origin HTTPS metadata, its detached
signature, and the selected platform archive. It rejects redirects, oversized
responses, unsupported targets, bad signatures, archive digest mismatches,
unsafe archive paths/types/modes, and mismatched candidate identity before
activation. Downgrades fail unless `--allow-downgrade` is explicit. `--dry-run`
performs all verification without changing files. One OS-backed lock serializes
the complete transaction. Both executables are finalized and revalidated in a
new version directory before an activation-v2 record publishes their exact
paths, sizes, and hashes. Previous complete activations remain available for
fallback.

Pre-A2A direct installations require the documented one-time signed
uninstall/reinstall migration. Their old `kado update` is not a supported path
across the bundle boundary.

`kado uninstall --yes` removes both executables and managed activation state
while preserving configuration and autonomous-agent credentials.
`--purge-credentials` first performs the
existing authenticated revocation; if revocation fails, the executable remains.
The generated uninstall scripts provide the same policy and are the preferred
removal path on Windows, where a running executable can be locked.

For a package-owned build, both lifecycle commands refuse before any release
or credential mutation and print the owning manager command. A package must
install the two real files together in its private directory and expose only
Kado where the manager supports a private sidecar. The release matrix invokes
every native target through its link or junction, verifies delegation, checks
the refusal text and unchanged hashes, rejects a tampered sidecar, restores it,
and proves delegation recovers.

The CLI release also owns a version-compatible embedded copy of the Search
skill. Installing, refreshing, or removing that copy is a distinct local
operation from credential revocation. A CLI update refreshes the bundled source
and may sync installations previously managed by Kado; it must not overwrite a
skill managed by another package manager.

## Publication boundary

The release builder creates and verifies release directories but does not
upload or publish them. External publication is a separate operator action
after review of:

1. detached signature verification;
2. checksums, SBOMs, and provenance;
3. clean install, update, downgrade-policy, rollback, and uninstall tests; and
4. native Linux and Windows verification.

The protected GitHub workflow publishes the canonical build as one GitHub
Release. A separate publisher, configured independently from this repository,
must copy those exact release assets to `kado.so` using this mapping:

| Release artifact | Public location |
| --- | --- |
| every immutable artifact | `/install/releases/<cli-version>/<name>` |
| `install.sh` | `/install.sh` |
| `install.ps1` | `/install.ps1` |
| `release-metadata.json` | `/install/releases/stable/release-metadata.json` |
| `release-metadata.json.sig` | `/install/releases/stable/release-metadata.json.sig` |

The publisher uploads and verifies immutable CLI objects first and promotes
stable CLI metadata last. Skill publication is an independent workflow and is
not an input or output of this CLI release build.
