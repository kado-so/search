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
contains only `kado` or `kado.exe`,
`LICENSE`, and `INSTALL-CLI.md`, with fixed safe modes and timestamps.

## Signing boundary

Release metadata uses a detached Ed25519 signature. The production signing seed
exists only in the protected release environment as
`KADO_RELEASE_SIGNING_KEY`, encoded as one base64 32-byte seed. The builder
reads it from the process environment, never accepts it as an argument, and
never prints it. No private or test signing key is stored in this repository.

The corresponding public key and its SHA-256 key ID are non-secret. The builder
stamps them into every executable and writes `release-public-key.pem` for
independent verification. In-band signing-key rotation is deliberately
unsupported in release protocol v1: an installed binary accepts only metadata
signed by its embedded key. The metadata carries that key's ID, and the
replacement executable must carry a public key with the same derived ID.
Rotating the release key therefore
requires an out-of-band reinstall from the reviewed official
`https://kado.so/install` boundary. Existing binaries cannot self-update
across a key rotation, even when a release is signed by the old key.

Local dry runs use an ephemeral key. A production release must use the
protected production seed and must not reuse a dry-run key.

## Local dry run

Use the Go version pinned by the `toolchain` directive in `go.mod`. Generate
prebuilt binaries with the pinned GoReleaser version, then finalize them with
an ephemeral signing seed:

```bash
release_seed_file="$(mktemp "${TMPDIR:-/tmp}/kado-release-seed.XXXXXX")"
openssl rand -base64 32 >"$release_seed_file"
chmod 600 "$release_seed_file"
export KADO_RELEASE_SIGNING_KEY="$(tr -d '\n' <"$release_seed_file")"
go run ./tools/release \
  --version 0.1.0 \
  --commit 0123456789abcdef0123456789abcdef01234567 \
  --source-date-epoch 1784851200 \
  --write-goreleaser-env "$release_seed_file.env"
set -a
. "$release_seed_file.env"
set +a

goreleaser build --clean --snapshot
go run ./tools/release \
  --version 0.1.0 \
  --commit 0123456789abcdef0123456789abcdef01234567 \
  --source-date-epoch 1784851200 \
  --prebuilt dist/goreleaser \
  --out dist/release
```

Delete the temporary seed and dry-run directories after verification. The
builder requires absent or empty output directories and installs its complete
output directory with one rename, so a partial build never looks complete.

## Release contents and verification

`release-metadata.json` is canonical JSON and binds:

- version, source commit, UTC build time, and signing-key identity;
- each platform archive by URL, size, and SHA-256.

`release-metadata.json.sig` authenticates those exact bytes. Direct binaries,
`checksums.txt`, SPDX SBOMs, SLSA/in-toto provenance, the install guide, and
platform install/uninstall scripts remain standalone artifacts for operators
and package systems. They are intentionally outside the self-updater's signed
metadata and runtime trust path.

The generated `INSTALL-CLI.md`, `install.sh`, and `install.ps1` implement the
agent-first bootstrap from canonical `kado.so` HTTPS endpoints. They select the
host target, download stable signed metadata and the immutable versioned
archive, run verification through the candidate, install into a user-owned
directory, configure user PATH when needed, install the signed Search skill,
and create or reuse and verify authentication. `kado update` verifies the
signed archive descriptor, safely extracts the candidate, checks its stamped
release identity, retains the existing executable as a rollback file, and only
then performs the replacement.

## Runtime update and removal policy

`kado update` fetches only canonical same-origin HTTPS metadata, its detached
signature, and the selected platform archive. It rejects redirects, oversized
responses, unsupported targets, bad signatures, archive digest mismatches,
unsafe archive paths/types/modes, and mismatched candidate identity before
replacement. Downgrades fail unless `--allow-downgrade` is explicit.
`--dry-run` performs all verification without changing files. A per-executable
exclusive update lock prevents concurrent replace/uninstall transactions. A
pre-commit failure restores the installed binary and verified candidate. A
post-commit rollback-cleanup or directory-sync failure retains the verified new
executable; no failure path installs an empty or corrupt file.

`kado uninstall --yes` removes only the executable and preserves configuration
and autonomous-agent credentials. `--purge-credentials` first performs the
existing authenticated revocation; if revocation fails, the executable remains.
The generated uninstall scripts provide the same policy and are the preferred
removal path on Windows, where a running executable can be locked.

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
| `kado-search.tar.gz` | `/install/skills/kado-search/<skill-version>/kado-search.tar.gz` and `/install/skills/kado-search/latest/kado-search.tar.gz` |
| `skill-metadata.json` | `/install/skills/kado-search/latest/metadata.json` |
| `skill-metadata.json.sig` | `/install/skills/kado-search/latest/metadata.json.sig` |

The publisher uploads immutable CLI and skill objects first, verifies their
public bytes, copies the latest skill archive, publishes signed skill metadata,
and promotes stable CLI metadata last.
