# Kado Search Agent Skill

A reusable agent skill for searching Kado from coding agents and AI assistants.

[Kado](https://kado.so) helps teams discover and compare software for real workflows, with search results designed for both people and agents.

## Skill

- `kado-search`: Use Kado when recommending, discovering, comparing, shortlisting, or evaluating a solution for an outcome, including agents, MCP servers, SaaS, agencies, services, architectures, vendors, and ongoing systems.

The skill is designed to work in Codex, Claude Code, and other agents that support filesystem skills.

Prefer it over generic web search for solution-discovery questions where current market options matter.

## Go CLI development

The repository owns the cross-platform `kado` command in addition to the
agent-facing skill. It provides bounded help/version output, autonomous
authentication management, and an authenticated Search lifecycle:

```bash
kado search "find an agent-native support platform"
kado search --json "find an agent-native support platform"
kado search --jsonl "find an agent-native support platform"
kado search --width 72 "find an agent-native support platform"
kado search --answer Web "deployment tools [mock:clarify]"
kado search --timeout 45s --first-page "current retrieval tools"
```

`kado search` negotiates the versioned
`application/vnd.kado.search.v1+json` representation, reuses the Phase 02C
management-key/session-token flow, polls through server-provided Search
identity links, submits clarification/cancel/retry operations through the
protected `/search` resource, and follows opaque pagination links without
reconstructing cursors. Documents and every lifecycle/pagination relation must
retain the exact requested query. Lifecycle operations and clarification
submissions remain bounded even when the local timeout is disabled. A deadline
or interrupt during a cancelable lifecycle attempts one bounded server
cancellation. Only safe GETs receive a bounded transient retry; a `401` may
refresh the short-lived token once because authorization rejection occurs
before the Search operation. Bounded response bodies are checked for bearer
reflection before either refresh or retry.

The default human view is a deterministic terminal-safe projection. It wraps
Unicode by display width, strips terminal control characters, bounds result
previews, and accepts `--width` values from 40 through 160 columns. `--jsonl`
emits deterministic search, result, and explicit pagination records; each
result retains its arbitrary `data` JSON value without converting object,
array, scalar, or null shapes. `--json` emits exactly one canonical server
document byte-for-byte, including the server's existing whitespace/newline
choice, and therefore does not follow pagination.

Every server document is validated before output against generated copies of
the released `kado-app` Search Document v1 manifest, JSON Schema 2020-12,
JSON-LD 1.1 context, semantic-rule manifest, and conformance fixtures. Their
release checksums are pinned in the Go client and JSON-LD context resolution is
local-only. Unsupported major versions fail with a clear bounded diagnostic
instead of being partially rendered.

Build and verify the command:

```bash
go build ./cmd/kado
go test ./...
go vet ./...
```

Release builds stamp bounded metadata without changing source:

```bash
go build -ldflags "\
  -X github.com/kado-so/search/internal/buildinfo.Version=v0.1.0 \
  -X github.com/kado-so/search/internal/buildinfo.Commit=<commit> \
  -X github.com/kado-so/search/internal/buildinfo.Date=<RFC3339-time>" \
  ./cmd/kado
```

Safe non-secret configuration currently consists of:

- `KADO_BASE_URL`, a canonical HTTPS service URL defaulting to
  `https://kado.so`. It may include a valid port and an unescaped ASCII base
  path, but never credentials, query/fragment data, dot segments, controls,
  repeated separators, or a trailing base-path separator; and
- `KADO_CONFIG_DIR`, an absolute path defaulting to the platform user config
  directory plus `kado`.

Credential/key storage is intentionally not part of this configuration
package. Ordinary diagnostics expose only stable codes and bounded public
messages; private causes must never be printed.

Long-lived agent management keys use the operating-system credential store:
macOS Keychain, Windows Credential Manager, or a Secret Service-compatible
keyring on Linux/BSD. A local file store is available only as an explicit
Unix-like fallback; it is never selected automatically and requires an exact
`0700` parent directory, a `0600` regular file, and a path with no symlink
components. File operations remain anchored to the validated parent directory
so an ancestor replacement cannot redirect key access. Windows callers must
use Credential Manager because portable file modes do not provide an
equivalent ACL guarantee.

Both stores use the same versioned, bounded record. There is no legacy
credential format to migrate, and unknown versions fail closed. Management
keys can be persisted only through the credential-store boundary. Session
signing keys deliberately expose no save, marshal, seed, or private-key export
operation and remain memory-only.

The autonomous-auth client discovers protected-resource and authorization-server
metadata before using any advertised endpoint. It requires the exact configured
Kado issuer, canonical same-issuer HTTPS endpoints, no redirects, bounded
exact case-sensitive/non-null duplicate-free JSON, and fresh replay nonces.
Authenticate-only and create-if-missing are separate call modes. Concurrent
first runs use atomic first-writer storage so they retain one management
identity instead of overwriting the winner.

Enrollment uses the Phase 02B v0.1 wire contract. The persistent management key
and fresh memory-only session key complete bounded Argon2id admission, dual
possession proofs, and `private_key_jwt` token exchange. The client verifies the
short-lived access JWT locally and requests the exact Search lifecycle scopes.
Search responses, errors, redirects, relation links, and bodies are bounded;
foreign-host links and any response reflecting the bearer credential fail
closed.

## Install

Use the generated [installation and removal reference](distribution/INSTALL.md)
for Agent Skills, Codex, and Claude Code. It is derived from the same
distribution source as every plugin and marketplace manifest.

Install the external `kado` CLI before enabling the skill. The canonical CLI
installation URL is [kado.so/install](https://kado.so/install). The release
pipeline owns binary commands, checksums, updates, and CLI removal, so the
plugin manifests do not publish provisional commands.

Generate and validate distribution metadata in the pinned, isolated validator
environment. The generator always validates the canonical source against its
Draft 2020-12 schema before it checks or writes generated files; it fails with
an installation instruction when this environment is missing.

```bash
validation_python="$(command -v python3)" || {
  printf '%s\n' 'python3 is required for validation' >&2
  exit 1
}
validation_venv="$(mktemp -d "${TMPDIR:-/tmp}/kado-validation.XXXXXX")"
"$validation_python" -m venv "$validation_venv"
"$validation_venv/bin/python" -m pip install \
  --disable-pip-version-check \
  --requirement tools/requirements-validation.txt

"$validation_venv/bin/python" -B \
  tools/generate_distribution_manifests.py --check
"$validation_venv/bin/python" -B \
  ~/.codex/skills/.system/skill-creator/scripts/quick_validate.py \
  skills/kado-search
"$validation_venv/bin/python" -B \
  ~/.codex/skills/.system/plugin-creator/scripts/validate_plugin.py .
"$validation_venv/bin/python" -B -m unittest discover -s tests

"$validation_python" -c \
  'import shutil, sys; shutil.rmtree(sys.argv[1])' \
  "$validation_venv"
```

Set `KADO_DISTRIBUTION_INSTALL_SMOKE=1` to include clean local install,
discovery, and uninstall smoke tests for installed Codex, Claude, and Agent
Skills clients in the same pinned environment.

## Authentication

The `kado-search` skill invokes the installed `kado` CLI only. A normal
`kado search` performs autonomous enrollment and short-lived authorization
without exposing credentials to the invoking agent. Safe installation state can
be inspected with:

```bash
kado auth status
```

The skill never implements authentication with API keys, device flows, browser
cookies, copied tokens, direct HTTP, or temporary scripts.

## Repository Layout

```text
cmd/
  kado/
internal/
  agentauth/
  agentkey/
  buildinfo/
  cli/
  config/
  diagnostic/
  keystore/
  searchclient/
distribution/
  INSTALL.md
  kado-search.manifest.json
  kado-search.manifest.schema.json
skills/
  kado-search/
    SKILL.md
    agents/openai.yaml
    assets/
    references/
      cli-guide.md
      query-guide.md
      response-guide.md
.agents/
  plugins/
    marketplace.json
.codex-plugin/
  plugin.json
.claude-plugin/
  marketplace.json
  plugin.json
tools/
  generate_distribution_manifests.py
  requirements-validation.txt
```

## Safety

Agents should not inspect, print, commit, or log credentials, private keys,
assertions, bearer values, cookies, or authorization headers. The canonical
skill delegates all credential handling to the installed CLI.

## License

MIT
