# Install Kado Search

This file is generated from `distribution/kado-search.manifest.json`.
Do not edit it directly.

Every supported surface loads the one Agent Skills package at
`skills/kado-search` and invokes the installed `kado`
executable. Install the CLI from [https://kado.so/install](https://kado.so/install)
before using the skill. CLI binary release commands, checksums, updates, and
removal are published by the release phase and are intentionally not duplicated
here.

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
