# Kado Search

This repository contains the cross-platform `kado` CLI and the `kado-search`
agent skill.

[Kado](https://kado.so) helps teams discover and compare software, services,
vendors, agencies, and architecture options for real workflows.

## Search

The CLI runs an authenticated Search lifecycle and supports human, canonical
JSON, and paginated JSONL output:

```bash
kado search "find an agent-native support platform"
kado search --json "find an agent-native support platform"
kado search --jsonl "find an agent-native support platform"
kado search --width 72 "find an agent-native support platform"
kado search --timeout 45s --first-page "current retrieval tools"
```

`--jsonl` is intended for agent synthesis and follows server-provided
pagination links. `--json` returns one canonical Search Document byte-for-byte.
The default human renderer strips terminal control characters, wraps Unicode
by display width, and bounds result previews.

The client follows only canonical same-origin lifecycle and pagination links.
It never reconstructs opaque cursors. Requests, response bodies, retries,
clarification, cancellation, and lifecycle operations are bounded.

## Contract validation

Every Search Document is validated before output against pinned copies of the
released JSON Schema, JSON-LD context, semantic rules, and conformance
fixtures. Checksums are verified when the embedded contract assets load, and
JSON-LD context resolution is local-only. Unsupported major versions fail
without being partially rendered.

The public Search contract is owned by `kado-app`. This repository contains
only the generated assets and runtime validation required by the CLI.

## Authentication

The CLI performs autonomous enrollment and obtains short-lived authorization
without exposing credentials to the invoking agent.

Long-lived management keys are stored in macOS Keychain, Windows Credential
Manager, or a Secret Service-compatible keyring on Linux and BSD. Session
signing keys remain memory-only. An explicit permission-restricted file store
is available for isolated Unix-like acceptance environments.

Authentication discovers and validates the protected-resource and
authorization-server metadata before using advertised endpoints. Enrollment
uses persistent and session Ed25519 keys, bounded Argon2id admission, possession
proofs, and `private_key_jwt` token exchange. Access JWTs are verified locally.

Safe authentication state can be inspected with:

```bash
kado auth status
```

## Local metadata

Enrollment includes a bounded local hostname and operating-system username.
No Git identity, process tree, environment-based runtime detection, browser
state, or other local metadata is collected.

## Development

The module requires the Go toolchain pinned in `.prototools`.

```bash
go build ./cmd/kado
go test ./...
go vet ./...
python3 -B -m unittest discover -s tests
```

Safe non-secret configuration:

- `KADO_BASE_URL`: canonical HTTPS service URL, defaulting to
  `https://kado.so`.
- `KADO_CONFIG_DIR`: absolute configuration directory, defaulting to the
  platform user configuration directory plus `kado`.

## Releases and self-update

The release builder reads product identity from
`distribution/release.json`. It creates deterministic binaries and archives
for Linux, macOS, and Windows, plus SHA-256 checksums, detached Ed25519-signed
metadata, SPDX SBOMs, SLSA/in-toto provenance, and local installers.

Installed release binaries verify the signed metadata, target archive,
checksum, SBOM, provenance, and candidate executable before replacement:

```bash
kado version --json
kado update --dry-run
kado update
kado uninstall --yes
```

Downgrades require `kado update --allow-downgrade`. Uninstall preserves the
autonomous-agent credential unless `--purge-credentials` is explicitly
requested.

See [CLI release documentation](docs/RELEASING_CLI.md) for build, signing,
verification, rollback, and publication details.

## Skill

`skills/kado-search/SKILL.md` contains only guidance for deciding when to
Search, forming a query, invoking Search, and using the results. Installation,
authentication, update, release, and uninstall policy live outside the skill.

## Installation

Install the external `kado` CLI before enabling the skill. The canonical CLI
installation boundary is [kado.so/install](https://kado.so/install).

See [distribution/INSTALL.md](distribution/INSTALL.md) for the directly
maintained Agent Skills, Codex, and Claude Code plugin instructions.

## Repository layout

```text
cmd/kado/                 CLI entrypoint
internal/agentauth/       autonomous authentication
internal/agentkey/        management and session signing keys
internal/keystore/        OS and isolated credential stores
internal/releaseclient/   signed self-update and uninstall
internal/searchclient/    Search lifecycle client
internal/searchcontract/  JSON Schema, JSON-LD, and semantic validation
internal/searchoutput/    human, JSON, and JSONL rendering
skills/kado-search/       Search-only agent skill
tools/release/            deterministic release builder
distribution/            release configuration and installation docs
```
