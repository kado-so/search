# Kado CLI Release Boundary

The search repository owns reproducible CLI release construction. It does not
publish a release as part of the build. Release operators provide the exact
semantic version; repository, executable, and install URL are fixed in
`tools/release`.

## Supported targets

Every release contains direct versioned binaries, versioned archives, an SPDX
2.3 SBOM per target, and one SLSA v1/in-toto provenance statement for:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`
- `windows/arm64`

Unix archives are deterministic `tar.gz` files. Windows archives are
deterministic ZIP files. Each archive contains only `kado` or `kado.exe`,
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

Local dry runs generate one ephemeral key at runtime and reuse it for both
reproducibility builds. A production release must use the protected production
seed and must not reuse a dry-run key.

## Reproducible dry run

Use the Go version pinned in `.prototools`. Choose the source commit and its
canonical UTC source timestamp. Generate a temporary signing seed without
printing it:

```bash
release_seed_file="$(mktemp "${TMPDIR:-/tmp}/kado-release-seed.XXXXXX")"
openssl rand -base64 32 >"$release_seed_file"
chmod 600 "$release_seed_file"
export KADO_RELEASE_SIGNING_KEY="$(tr -d '\n' <"$release_seed_file")"

go run ./tools/release \
  --version 0.1.0 \
  --commit 0123456789abcdef0123456789abcdef01234567 \
  --source-date-epoch 1784851200 \
  --out dist/release-a
go run ./tools/release \
  --version 0.1.0 \
  --commit 0123456789abcdef0123456789abcdef01234567 \
  --source-date-epoch 1784851200 \
  --out dist/release-b

diff -qr dist/release-a dist/release-b
```

Delete the temporary seed and dry-run directories after verification. The
builder requires absent or empty output directories and installs its complete
output directory with one rename, so a partial build never looks complete.

The exact Go version, `-trimpath`, disabled VCS auto-stamping, empty Go build
ID, `CGO_ENABLED=0`, fixed timestamps, stable target order, canonical JSON, and
stable archive headers make identical inputs byte-identical.

## Release contents and verification

`release-metadata.json` is canonical JSON and binds:

- version, source commit, UTC build time, and signing-key identity;
- each platform archive by URL, size, and SHA-256.

`release-metadata.json.sig` authenticates those exact bytes. Direct binaries,
`checksums.txt`, SPDX SBOMs, SLSA/in-toto provenance, the install guide, and
platform install/uninstall scripts remain standalone artifacts for operators
and package systems. They are intentionally outside the self-updater's signed
metadata and runtime trust path.

The generated `INSTALL-CLI.md`, `install.sh`, and `install.ps1` require release
files to be downloaded before execution. They do not contain a curl-pipe-shell
flow. Initial installation uses a same-directory candidate and refuses to
overwrite an existing binary, so it cannot bypass update/downgrade policy.
`kado update` verifies the signed archive descriptor, safely extracts the
candidate, checks its stamped release identity, retains the existing executable
as a rollback file, and only then performs the atomic rename.

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

Plugin/skill removal remains independent from CLI removal and credential
revocation.

## Publication boundary

The release builder creates and verifies release directories but does not
upload or publish them. External publication is a separate operator action
after review of:

1. byte-identical double-build output;
2. detached signature verification;
3. checksums, SBOMs, and provenance;
4. clean install, update, downgrade-policy, rollback, and uninstall tests; and
5. native Linux, macOS, and Windows verification.
