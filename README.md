# Kado Search

This repository contains the cross-platform `kado` CLI and the `kado-search`
agent skill.

[Kado](https://kado.so) helps teams discover and compare solutions, specialist
capabilities, and reusable resources—from software, APIs, and agents to skills,
templates, services, vendors, agencies, and architecture options.

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

The client starts and manages Search through kado-app's authenticated `/search`
resource. It accepts the app's same-origin product-execution and pinned public
Search links, keeps lifecycle mutations on the private resource, and never
reconstructs opaque cursors. Requests, response bodies, retries,
cancellation, and lifecycle operations are bounded.

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

Each detected calling agent has its own long-lived management key. Keys are
stored in macOS Keychain, Windows Credential Manager, or a Secret
Service-compatible keyring on Linux and BSD. Session signing keys remain
memory-only. A permission-restricted file backend is available on every
platform, including systems without a working OS keyring.

Authentication discovers and validates the protected-resource and
authorization-server metadata before using advertised endpoints. Enrollment
uses persistent and session Ed25519 keys, bounded Argon2id admission, possession
proofs, and `private_key_jwt` token exchange. Access JWTs are verified locally.

Safe authentication state can be inspected with:

```bash
kado auth create
kado auth link
kado auth status
kado auth identities
kado --agent codex auth status
```

`auth create` creates the selected identity if it does not exist and completes
mandatory admission when kado-app requires it; an authenticated Search also
creates the identity transparently when needed.

`auth link` opens short-lived browser approval flows that connect every locally
configured agent identity to the signed-in human Kado account. Use
`kado --agent <identity> auth link` to link only one identity. Linking does not
expose or replace any agent's existing credentials.

## Local metadata

The CLI creates a random, non-secret host ID next to its configuration.
Enrollment includes that ID plus a bounded hostname, operating-system username,
and canonical calling-agent name. Every service request includes
`X-Kado-Agent`.

Agent detection uses the local process ancestry first and recognized
environment markers second. Raw process and environment data never leave the
machine. If no supported caller is detected, the CLI selects `default`.
`--agent <name>` overrides detection.

## Development

The module requires the Go toolchain pinned by the `toolchain` directive in
`go.mod`.

```bash
go build ./cmd/kado
go test ./...
go vet ./...
```

Safe non-secret configuration uses `config.json`. `KADO_CONFIG_DIR` selects its
absolute directory, defaulting to the platform user configuration directory
plus `kado`.

The platform defaults are `~/Library/Application Support/kado` on macOS,
`%AppData%\kado` on Windows, and `$XDG_CONFIG_HOME/kado` on Linux (falling
back to `~/.config/kado` when `XDG_CONFIG_HOME` is unset).

Optional `config.json`:

```json
{
  "base_url": "https://kado.so",
  "credentials": {
    "backend": "file",
    "directory": "./secrets"
  }
}
```

The default backend is `"os"`. The file directory may be absolute or relative
to `config.json`; it defaults to `./secrets`. File-backed secrets use private
Unix modes or a private Windows ACL and are additionally protected with DPAPI
on Windows. `host.json` and `identities.json` are non-secret local state.

## Releases and self-update

GoReleaser cross-compiles binaries for Linux, macOS, and Windows. Kado's release
finalizer packages and signs those binaries and generates SHA-256 checksums,
SPDX SBOMs, SLSA/in-toto provenance, and local installers.

Installed release binaries verify the signed metadata, selected platform
archive, and candidate executable identity before replacement:

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

`skills/kado-search/SKILL.md` contains the offline Search guidance bundled into
release builds. `kado skill install` prefers the latest compatible signed skill
from `kado.so` and falls back to that embedded copy without requiring npm, a
plugin marketplace, or another package manager. Kado tracks and asynchronously
updates only the skill copies it owns.

## Installation

The primary installation flow is agent-first: an agent downloads the one
platform-specific Kado release, verifies and installs it, then asks Kado to
install its bundled skill. The canonical installation boundary is
[kado.so/install](https://kado.so/install).

See [installation documentation](docs/INSTALL.md) for the directly
maintained Agent Skills, Codex, and Claude Code plugin instructions.
See [CLI distribution plan](docs/CLI_DISTRIBUTION.md) for the agent-first
bootstrap, bundled skill, package-manager, and Windows update design.

## Repository layout

```text
cmd/kado/                 CLI entrypoint
internal/agentauth/       autonomous authentication
internal/agentkey/        management and session signing keys
internal/agentidentity/   local-only calling-agent detection
internal/keystore/        OS and permission-restricted file stores
internal/localstate/      host ID and known identity registry
internal/releaseclient/   signed self-update and uninstall
internal/searchclient/    Search lifecycle client
internal/searchcontract/  JSON Schema, JSON-LD, and semantic validation
internal/searchoutput/    human, JSON, and JSONL rendering
skills/kado-search/       Search-only agent skill
tools/release/            deterministic release builder
docs/                     installation, architecture, and release operations
```
