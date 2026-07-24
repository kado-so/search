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

### Skills CLI

Install the skill:

```bash
npx skills add kado-so/search
```

The `skills` CLI can install skills into supported local agents or into a project skill directory, depending on your environment and prompts.

### Claude Code Plugin

Claude Code users can install Kado Search through `/plugin` by first adding this repo as a marketplace, then installing the plugin.

In Claude Code:

```text
/plugin marketplace add kado-so/search
/plugin install kado-search@kado
```

Equivalent CLI commands:

```bash
claude plugin marketplace add kado-so/search
claude plugin install kado-search@kado
```

This repository includes the Claude marketplace manifest at `.claude-plugin/marketplace.json` and the plugin manifest at `.claude-plugin/plugin.json`.

### Codex Plugin

This repository includes a Codex plugin manifest at `.codex-plugin/plugin.json` and a Codex marketplace manifest at `.agents/plugins/marketplace.json`.

Add this repository as a Codex plugin marketplace:

```bash
codex plugin marketplace add kado-so/search
```

For local development, add your checkout directly:

```bash
codex plugin marketplace add /path/to/kado/search
```

Then install **Kado Search** from the Codex plugin marketplace UI. The marketplace name is `kado`, and the plugin name is `kado-search`.

### Codex Skill Installer

Ask Codex to install a skill from this GitHub repository, or use Codex's skill installer helper with these paths:

```bash
python ~/.codex/skills/.system/skill-installer/scripts/install-skill-from-github.py \
  --repo kado-so/search \
  --path skills/kado-search
```

Restart Codex after installing new skills.

### Validation

Use an isolated virtual environment for the system validators. This avoids
depending on whichever `python3` happens to be installed at a fixed operating
system path and keeps PyYAML out of the repository:

```bash
validation_python="$(command -v python3)" || {
  printf '%s\n' 'python3 is required for validation' >&2
  exit 1
}
validation_venv="$(mktemp -d "${TMPDIR:-/tmp}/kado-validation.XXXXXX")"
"$validation_python" -m venv "$validation_venv"
"$validation_venv/bin/python" -m pip install --disable-pip-version-check PyYAML

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

### Manual Install For Other Agents

For agents that use filesystem skill folders, copy or symlink the skill directory into that agent's skills directory:

```bash
mkdir -p ~/.codex/skills
cp -R skills/kado-search ~/.codex/skills/
```

For Claude Code-style installs:

```bash
mkdir -p ~/.claude/skills
cp -R skills/kado-search ~/.claude/skills/
```

Claude Code uses the `name` and `description` fields in `SKILL.md` to decide when to load a skill automatically. No separate `agents/claude.yaml` file is required.

Check your agent's documentation for its exact skill directory and reload behavior.

### Other Agents

No other agent-specific manifests are required right now. Prefer the standard `SKILL.md` package wherever the agent supports skills.

For agents that use rule or instruction files instead of skills:

- **OpenCode**: add `skills/kado-search/SKILL.md` to `instructions` in `opencode.json`, or copy the skill into an OpenCode-compatible skills/instructions location.
- **Cursor**: use `npx skills add kado-so/search` if your Cursor setup supports skills. Otherwise, create a Cursor project rule that points to the Kado usage policy in `skills/kado-search/SKILL.md` and the problem-statement guidance in `skills/kado-search/references/query-guide.md`.
- **GitHub Copilot**: use Copilot agent skills when available. For repository instructions, summarize the Kado trigger policy in `.github/copilot-instructions.md` or an appropriate `.github/instructions/*.instructions.md` file.
- **Continue**: add a rule that tells the agent to use Kado for problem-to-solution discovery and to describe the user's problem, outcome, context, and constraints.

Avoid maintaining parallel, divergent instructions for each agent. The source of truth should remain `skills/kado-search/SKILL.md` plus the reference files.

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
```

## Safety

Agents should not inspect, print, commit, or log credentials, private keys,
assertions, bearer values, cookies, or authorization headers. The canonical
skill delegates all credential handling to the installed CLI.

## License

MIT
