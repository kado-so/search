# Install Kado Search

This file is generated from
`distribution/kado-installation.v1.gen.json`, which in turn derives identity
and version from `distribution/kado-search.manifest.json`.
Do not edit it directly.

Every supported surface loads the one Agent Skills package at
`skills/kado-search` and invokes the installed `kado`
executable. The CLI is required before the skill can perform Search; discover
its release availability at
[https://kado.so/install](https://kado.so/install).

CLI release availability is currently `unpublished`.
Do not claim that a downloadable CLI release exists until the canonical
metadata and detached signature resolve and verify. Once published, discover
checksums, provenance, per-platform SBOMs, archives, and generated local
installers through the signed release metadata at
[https://kado.so/install/releases/stable/release-metadata.json](https://kado.so/install/releases/stable/release-metadata.json).
Download the complete release bundle before running its generated installer;
no supported flow pipes a network response into a shell.

Installed release binaries support:

```bash
kado version --json
kado update --dry-run
kado update
kado uninstall --yes
```

Uninstall preserves the autonomous-agent credential by default. Credential
revocation is separate and happens only when `--purge-credentials` is explicit.
Every CLI, plugin, skill, update, and uninstall action requires explicit user
confirmation. The phrase `install kado.so` is a request to explain the
supported targets and request approval, not authorization to mutate the user's
environment.

## Agent Skills

Install:

```bash
npx skills add kado-so/search --skill kado-search
```

Uninstall:

```bash
npx skills remove kado-search
```

The standalone skill invocation name is `kado-search`.

## Codex Plugin

Install:

```bash
codex plugin marketplace add kado-so/search
codex plugin add kado-search@kado
```

Uninstall:

```bash
codex plugin remove kado-search@kado
codex plugin marketplace remove kado
```

The plugin ID is `kado-search@kado`. Codex presents its
skill under the plugin namespace `kado-search:kado-search`.

## Claude Code Plugin

Install:

```bash
claude plugin marketplace add kado-so/search
claude plugin install kado-search@kado
```

Uninstall:

```bash
claude plugin uninstall kado-search@kado
claude plugin marketplace remove kado
```

The plugin ID is `kado-search@kado` and the Claude skill
namespace is `/kado-search:kado-search`.

Plugin and skill removal does not remove the external Kado CLI or revoke its
installation identity. Use `kado auth revoke` only when the user explicitly
requests credential revocation.
