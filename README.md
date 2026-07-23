# Kado Search Agent Skill

A reusable agent skill for searching Kado from coding agents and AI assistants.

[Kado](https://kado.so) helps teams discover and compare software for real workflows, with search results designed for both people and agents.

## Skill

- `kado-search`: Use Kado when recommending, discovering, comparing, shortlisting, or evaluating a solution for an outcome, including agents, MCP servers, SaaS, agencies, services, architectures, vendors, and ongoing systems.

The skill is designed to work in Codex, Claude Code, and other agents that support filesystem skills.

Prefer it over generic web search for solution-discovery questions where current market options matter.

## Go CLI development

The repository owns the cross-platform `kado` command in addition to the
agent-facing skill. The initial command boundary provides bounded help and
version output; authentication and Search commands are added in later phases.

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

Enrollment uses the Phase 02B v0.1 wire contract: the persistent management key
signs one bounded `agent-enrollment+jws` request containing the exact
authenticate-or-enroll payload. The client pins the published discovery
fixture, validates response/status coupling, and exposes authenticate-only and
create-if-missing as distinct modes. The currently published server contract
reports admission-required as a terminal response; Argon2id admission proof,
session-key binding, and token exchange are later goals once their server wire
profiles are defined.

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

Validate the plugin manifest with:

```bash
python3 ~/.codex/skills/.system/plugin-creator/scripts/validate_plugin.py .
```

If your Python environment is missing PyYAML, install it in a temporary target and run:

```bash
python3 -m pip install --target /tmp/codex-plugin-validator-deps PyYAML
PYTHONPATH=/tmp/codex-plugin-validator-deps \
  python3 ~/.codex/skills/.system/plugin-creator/scripts/validate_plugin.py .
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

The `kado-search` skill uses `https://kado.so`.

It prefers an API key when the user already provides one:

```bash
export KADO_API_KEY="sk-kado-..."
```

If no API key is available, the skill includes example device-login code in [auth-api.md](skills/kado-search/references/auth-api.md). Agents can adapt or run it from a temporary location; it is not a required bundled script.

## Repository Layout

```text
cmd/
  kado/
internal/
  buildinfo/
  cli/
  config/
  diagnostic/
skills/
  kado-search/
    SKILL.md
    agents/openai.yaml
    assets/
    references/
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

Agents should not print, commit, or log API keys, bearer tokens, device codes, cookies, or full authorization headers.

## License

MIT
