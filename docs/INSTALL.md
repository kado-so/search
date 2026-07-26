# Install Kado Search

The Search skill requires the external `kado` CLI. Install it from the
canonical [Kado installation page](https://kado.so/install) before installing
the skill or plugin.

## Agent Skills

```bash
npx skills add kado-so/search --skill kado-search
```

Remove it with:

```bash
npx skills remove kado-search
```

## Codex

```bash
codex plugin marketplace add kado-so/search
codex plugin add kado-search@kado
```

Remove it with:

```bash
codex plugin remove kado-search@kado
codex plugin marketplace remove kado
```

## Claude Code

```bash
claude plugin marketplace add kado-so/search
claude plugin install kado-search@kado
```

Remove it with:

```bash
claude plugin uninstall kado-search@kado
claude plugin marketplace remove kado
```

Plugin or skill removal does not remove the external CLI or revoke its
autonomous-agent credential.
