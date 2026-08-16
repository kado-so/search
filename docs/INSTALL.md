# Install Kado Search

Kado's primary installation path is designed for an agent to complete without
Homebrew, npm, WinGet, or another package manager.

## Agent-first installation

macOS and Linux:

```sh
curl -fsSL https://kado.so/install/install.sh | sh
```

An agent may use `wget -qO-` instead. Windows:

```powershell
irm https://kado.so/install.ps1 | iex
```

The canonical page at [kado.so/install](https://kado.so/install) exposes one
release descriptor an agent can use to:

1. detect the host operating system and architecture;
2. download the matching versioned Kado release from `kado.so`;
3. verify the signed release metadata, archive checksum, and executable
   identity;
4. install the executable in a user-writable directory;
5. run `kado skill install` to install the latest compatible signed Search
   skill, with the bundled copy as an offline fallback;
6. run idempotent `kado auth create` to create an identity or reuse an active
   existing credential; and
7. run `kado auth status` to verify the configured identity.

The agent must not need a language runtime or third-party package manager.
Initial bootstrap uses operating-system facilities or the agent's own HTTPS
download capability. After bootstrap, the Kado executable owns verification,
skill installation, and direct-install updates.

The target user locations are:

- macOS and Linux: `$HOME/.local/bin/kado`
- Windows: `%LOCALAPPDATA%\Kado\kado.exe`

The installation flow must explain how to add that directory to `PATH` when it
is not already present. It must remain non-interactive when the agent supplies
explicit destination and confirmation options.

## Bundled skill

Each Kado CLI release embeds an offline `kado-search` fallback and prefers the
latest compatible signed skill published by `kado.so`. The commands are:

```text
kado skill install
kado skill status
kado skill update
kado skill uninstall
```

`install` defaults to `--all`: it installs for the calling agent, every locally
detected supported harness, and the portable `~/.agents/skills/` location. An
explicit `--agent` adds a requested identity. Product-specific user locations
are used for Codex, Claude Code, Cursor, Gemini CLI, Antigravity, GitHub
Copilot, OpenCode, Goose, Aider, and Amp. Detected skill-capable identities
without a product-specific directory use `~/.agents/skills/`.

Gemini CLI and Antigravity have distinct user locations. Selecting either one
installs both `~/.gemini/skills/kado-search` and
`~/.gemini/config/skills/kado-search`.

Kado records which local skill installations it owns. `status`, `update`, and
`uninstall` also scan every known destination, verify its ownership receipt
against its canonical agent and path, hash the actual paths and file contents,
and reconcile valid newly discovered Kado installations into the registry.
Registry, receipt, or filesystem drift is reported and is never overwritten.
The same home-relative layout is used on Windows, macOS, and Linux.

A successful direct CLI update automatically syncs Kado-managed skill copies.
If a skill update fails, the new CLI remains installed and reports the repair
command. Skill removal does not remove the CLI or revoke credentials.

## Future optional distribution channels

Only agent-first installation and direct downloads are in the initial
distribution scope. These channels may be added later:

1. a Kado-owned Homebrew tap for macOS and Linux;
2. WinGet for Windows;
3. Scoop for Windows;
4. Debian and RPM repositories for managed Linux systems;
5. container images for CI and ephemeral agents.

GitHub Releases are published now as a mirror of the exact `kado.so` release
artifacts, but are not the CLI's runtime update origin.

## Existing skill-manager mirrors

The directly maintained skill-manager mirrors remain available as secondary
paths.

Agent Skills:

```bash
npx skills add kado-so/search --skill kado-search
npx skills update kado-search
```

Codex:

```bash
codex plugin marketplace add kado-so/search
codex plugin add kado-search@kado
codex plugin marketplace upgrade kado
```

Claude Code:

```bash
claude plugin marketplace add kado-so/search
claude plugin install kado-search@kado
claude plugin update kado-search@kado
```

These are secondary distribution paths. Their lifecycle remains owned by the
selected external manager rather than by `kado skill update`.
