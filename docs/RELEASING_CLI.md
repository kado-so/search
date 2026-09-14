# Kado CLI release

Search owns the public CLI, complete-bundle signing, installers and publication.
MCP owns its maintained runtime component; it is not published to npm separately.
The current release scope is direct installation from GitHub and `kado.so` on
Linux, macOS and Windows, each on amd64 and arm64. Homebrew, Scoop, WinGet,
deb/rpm and container releases and their channel-specific jobs are deferred.
Their build support remains available for a later approved release.

## Reviewed inputs and versions

The proposed first stable MCP-capable CLI is **0.2.0**, with MCP component
**0.1.0** and private Node **24.20.0**. These are release choices, not published
versions. See [release readiness](MCP_RELEASE_READINESS.md) for remaining gates.

1. Review and squash-merge the MCP version/qualification changes first.
2. Set `third_party/mcp/source.lock.json` to that exact merged commit, version,
   dependency-lock SHA-256 and Node pin. Never use a branch, an uncommitted source
   identity, or a guessed future commit. The component builder requires a clean
   committed checkout; `scripts/verify-mcp-source.mjs` checks the complete lock.
3. Keep the official A2A source pinned by `third_party/a2a-cli/upstream.lock.json`.
4. Review and squash-merge Search, qualify its exact merged source, and only then
   approve a release tag. A source commit/push is separate from publication.

The MCP checkout uses frozen pnpm dependencies and the pinned Node version.
`scripts/packaging/build.mjs` in MCP emits a native component with
`--release-component`; qualification-only peers are excluded. The reusable
`mcp-components.yml` builds and exercises six native targets using the narrowly
scoped `KADO_MCP_READ_TOKEN`. It uploads component archives and independent
manifest digests. Search's `tools/mcp-components` verifies and assembles them:

```text
go run ./tools/mcp-components --input mcp-inputs --out mcp-components
```

For a local dry run, provide an ephemeral Ed25519 seed through
`KADO_RELEASE_SIGNING_KEY` in the process environment. Do not pass a secret on
the command line or use the production key locally. Supply actual reviewed
commits and absolute input paths in the following command (one line on any OS):

```text
go run ./tools/release --version 0.2.0 --commit <search-commit> --source-date-epoch <commit-epoch> --a2a-source <official-checkout> --mcp-components <component-directory> --mcp-commit <mcp-commit> --mcp-digests <component-directory>/digests.gen.json --out <new-release-directory>
```

The output must be absent or empty. The builder stages the complete output and
publishes the local directory with one rename; it does not upload anything.
A test-signed candidate is qualification evidence, never a public release.

## Complete bundle and signatures

`kado.release.v3` authenticates six native archives, their manifest descriptors,
A2A identity, MCP commit/version/lock, per-target Node archive identity, SPDX
SBOMs and SLSA-shaped provenance (without claiming a SLSA level).
`kado.payload.v1` inventories the complete tree, including Kado, official A2A,
private Node, MCP code, native session host, addon, dependencies and licenses.
Unix archives use tar.gz; Windows uses ZIP. They are not five-file A2A pairs.
`kado.version.v2` reports the matching component identities.
See [the bundle contract](MCP_BUNDLE.md) for exact roles, limits and validation.

The protected `cli-release` environment supplies `KADO_RELEASE_SIGNING_KEY`
(base64 32-byte Ed25519 seed). The public key is embedded into the binaries;
only metadata signed by that embedded key is accepted. Key rotation requires
an out-of-band reviewed reinstall. Never print, persist or reuse the production
seed for local qualification.

The user deferred Apple Developer ID/notarization and Windows Authenticode on
2026-09-14. Retain Kado's existing Ed25519 release-verification approach, extended
to authenticate the complete MCP bundle. Neither platform-signing setup nor
notarization is a release prerequisite for this scope. Do not add certificate
provisioning, platform-signing transformations or security-setting changes.
Preserve the original vendor-signed Node bytes and its keychain identity.

Cin already has working macOS signing/notarization infrastructure, available as
a reference for a future separately approved change. Its credentials are not
needed or copied for this release. If platform signing is added later, perform
it before calculating final payload hashes and release signatures.

## Native qualification

CI builds a complete candidate plus a future-version candidate using a public
test key. `.github/actions/qualify-complete` installs the exact native archive,
exercises the MCP tutorial, keyring and offline four-skill catalog, then tests
upgrade, repair, denied implicit downgrade, explicit rollback and uninstall.
The release workflow requalifies the exact production-signed candidate before
publication. Earlier CI results do not qualify changed signed bytes.

Retain the existing Kado native release-test baseline documented in INSTALL.md.
Older OS versions are not part of this release support claim. Public-install
checks should record OS prompts under the existing unsigned Kado distribution
policy; platform signing is explicitly deferred.
Current evidence and the pinned upstream runtime floors are listed in
[MCP release readiness](MCP_RELEASE_READINESS.md).

Direct installation selects a host archive and verifies its signed metadata,
bytes and stamped identity. It stages an immutable complete tree and exposes
the stable launcher last. Future updates select whole units through
`activations-v3`; credentials and active-session payloads remain protected.
`kado update --allow-downgrade` is required for rollback within this format.
Fresh installs are the approved baseline. Pair-only release/activation-v2
installations have no automatic migration across this bundle boundary.

`kado uninstall --yes` removes managed executables and activations while
preserving credentials. `--purge-credentials` requires authenticated revocation;
a failure retains the installation. MCP profiles follow the documented
credential policy. On Windows, use the generated external uninstall script to
remove a running launcher. Skill removal is a separate operation.

## Publication and recovery

**A pushed `v*.*.*` tag starts publication. Do not push one before explicit
publication approval and closure of the remaining release gates.**
`.github/workflows/release.yml` verifies that the tag commit belongs to `main`,
builds from locked sources, uses protected signing, and runs native tests.
Its publisher logs into Azure using GitHub OIDC, uploads and verifies immutable
objects, creates the GitHub Release, then promotes CLI and skill channels.
It is not an independently configured external publisher.

| Artifact | Public path |
| --- | --- |
| Immutable CLI artifacts | `/install/releases/<version>/<name>` |
| Install/uninstall shell and PowerShell scripts | `/install.sh`, `/install.ps1`, `/uninstall.sh`, `/uninstall.ps1` |
| Stable metadata and signature | `/install/releases/stable/release-metadata.json[.sig]` |
| Immutable skill artifacts | `/install/skills/<name>/<variant>/<version>/...` |
| Immutable catalog revision | `/install/skills/catalogs/<revision>/catalog.json[.sig]` |
| Latest catalog and signature | `/install/skills/latest/catalog.json[.sig]` |

Before promotion, retain the previous channel objects, signatures, content types,
cache-control values and hashes in protected operational storage. Preserve the
previous GitHub release and immutable blobs. Verify the signing public key and
new catalog compatibility before uploading; never overwrite a version with
different bytes. `scripts/publish-azure-assets.sh` rejects immutable collisions.

Promotion updates multiple objects and is not a single atomic transaction.
If a response is lost, inspect actual Azure/GitHub state before retrying. On
failure restore the captured channel objects as a coherent previous set and
verify their public hashes. Keep newly uploaded immutable artifacts for audit.
If the previous release is pair-only, restoring the channel stops new installs;
it does not downgrade existing complete-format installs. Use an authenticated
complete-format rollback or forward fix for those clients.

Verify public URLs after promotion, including intermediary caches, instead of
assuming an upload invalidated them. Immutable objects use long-lived caching;
channel objects use `no-cache,must-revalidate`. Install from the public URL on
all three OS families and both architectures; verify help/version, all four
skills, OAuth and production Search-to-MCP direct/named invocation and cleanup.
No package-manager repository PRs or distribution promotion are part of this
release scope.
