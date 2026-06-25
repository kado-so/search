# Kado Search Agent Skills

Reusable agent skills for searching Kado from coding agents and AI assistants.

## Skills

- `kado-search-curl`: Use the hosted Kado Agent API with `curl`. Prefer a user-provided `sk-kado-...` API key when available; otherwise use the device grant flow.
- `kado-search-cli`: Use the `kado` CLI or `npx -y @kado/agent` fallback for compact search workflows.

For end users and agents that do not already have the Kado CLI installed, prefer `kado-search-curl`.

## Install With Vercel Skills

Install both skills:

```bash
npx skills add kado-so/search
```

Install only the curl/API skill:

```bash
npx skills add kado-so/search --skill kado-search-curl
```

Install only the CLI skill:

```bash
npx skills add kado-so/search --skill kado-search-cli
```

The `skills` CLI can install skills into supported local agents or into a project skill directory, depending on your environment and prompts.

## Install With Codex

Ask Codex to install a skill from this GitHub repository, or use Codex's skill installer helper with these paths:

```bash
python ~/.codex/skills/.system/skill-installer/scripts/install-skill-from-github.py \
  --repo kado-so/search \
  --path skills/kado-search-curl
```

To install both skills:

```bash
python ~/.codex/skills/.system/skill-installer/scripts/install-skill-from-github.py \
  --repo kado-so/search \
  --path skills/kado-search-curl \
  --path skills/kado-search-cli
```

Restart Codex after installing new skills.

## Manual Install

For agents that use filesystem skill folders, copy or symlink one of the skill directories into that agent's skills directory:

```bash
mkdir -p ~/.codex/skills
cp -R skills/kado-search-curl ~/.codex/skills/
```

For Claude Code-style installs:

```bash
mkdir -p ~/.claude/skills
cp -R skills/kado-search-curl ~/.claude/skills/
```

Check your agent's documentation for its exact skill directory and reload behavior.

## Authentication

The curl/API skill prefers an API key when the user already provides one:

```bash
export KADO_API_KEY="sk-kado-..."
export KADO_SEARCH_APP_URL="https://your-kado-app.example"
```

If no API key is available, the skill instructs the agent to use Kado's device grant flow and ask the user to approve the verification link.

## Repository Layout

```text
skills/
  kado-search-curl/
    SKILL.md
    agents/openai.yaml
    references/
  kado-search-cli/
    SKILL.md
    agents/openai.yaml
    references/
```

## Safety

These skills do not include executable scripts. They instruct agents to call documented Kado HTTP API routes or documented CLI commands. Agents should not print, commit, or log API keys, bearer tokens, device codes, cookies, or full authorization headers.

## License

MIT
