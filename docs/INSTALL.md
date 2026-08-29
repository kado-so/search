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
4. install the Kado and `kado-a2a` executable pair in a user-writable
   directory, or run their signed paired updater when a current direct
   installation already exists;
5. run `kado skill install` to install the general Kado CLI skill and the latest
   compatible signed Search skill, with bundled copies as an offline fallback;
6. run idempotent `kado auth create` to create an identity or reuse an active
   existing credential; and
7. run `kado auth status` to verify the configured identity.

Rerunning the installer is also the repair path: after a successful update (or
an already-current result), it synchronizes skills in every detected harness
and `~/.agents/skills/`, then recreates or reuses and verifies authentication.

The agent must not need a language runtime or third-party package manager.
Initial bootstrap uses operating-system facilities or the agent's own HTTPS
download capability. After bootstrap, the Kado executable owns verification,
skill installation, and direct-install updates.

Direct installations use a stable Kado launcher and an adjacent `kado-a2a`
sidecar beside a private `kado[.exe].d` directory of immutable executable-pair
versions. An activation record authenticates both members, and a later CLI
start observes either the previous complete pair or the new complete pair.

Pre-A2A direct installations cannot cross this bundle boundary with their old
`kado update`. Close all Kado processes, run the current signed uninstall
script with confirmation but without credential purge, and then run the current
signed installer. This one-time reinstall preserves configuration, identities,
and credentials.

The target user locations are:

- macOS and Linux: `$HOME/.local/bin/kado` and `$HOME/.local/bin/kado-a2a`
- Windows: `%LOCALAPPDATA%\Kado\kado.exe` and
  `%LOCALAPPDATA%\Kado\kado-a2a.exe`

The installation flow must explain how to add that directory to `PATH` when it
is not already present. It must remain non-interactive when the agent supplies
explicit destination and confirmation options.

## Bundled skills

Each Kado CLI release embeds offline fallbacks for `kado-search`,
`kado-cli-non-search`, and `kado-a2a`, and prefers the latest compatible signed
skills published by `kado.so`. The commands are:

```text
kado skill install
kado skill status
kado skill update
kado skill uninstall
```

`install` defaults to `--all`: it installs `kado-cli-non-search`, `kado-search`,
and `kado-a2a` for the calling agent, every locally
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

## Package-manager distribution

Release builds also produce channel-stamped pairs and current package
definitions for Homebrew, WinGet, Scoop, Debian, RPM, and containers. Each
manager keeps the real `kado` and `kado-a2a` files together in one owned
directory. Homebrew and Linux packages expose only a public Kado symlink;
Scoop exposes only its Kado shim; WinGet declares only the Kado portable alias;
and the container image starts the real Kado member of its private pair.

Package-owned binaries do not run Kado's direct updater or uninstaller. They
stop before release or credential state is opened and print the owning
manager's exact command. Use `brew`, `winget`, `scoop`, `apt`, or `dnf` for the
corresponding lifecycle. Replace or remove a container through its deployment
tool. The manager may relocate, relink, or repair the package because Kado
canonicalizes the running executable and verifies only the fixed sibling in
that real directory.

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
