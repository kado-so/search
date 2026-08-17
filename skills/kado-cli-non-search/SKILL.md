---
name: kado-cli-non-search
description: Use this for extra info on how to use the Kado CLI - account linking, authentication status, agent identity management, diagnostics, updates, and other non-search Kado operations. Use when the user asks to set up, connect, inspect, repair, update, or remove Kado. Do not use this skill for solution discovery or search; use kado-search for that.
license: "MIT"
metadata:
  author: "Kado"
  version: "0.1.0"
  homepage: "https://kado.so"
---

# Kado CLI

Use the installed `kado` CLI for Kado account and installation operations.

You can use `kado -h` for help and commands.

## Account linking

When the user asks to link their agents or CLI to their Kado account, run:

```bash
kado auth link
```

This links every locally configured agent identity through one browser approval.
Only when the user explicitly asks to link one identity, run:

```bash
kado --agent <identity> auth link
```

Tell the user to approve the request in the browser page opened by the CLI. If
the browser cannot open, give them the verification URL and short code printed
by the command. Never attempt human sign-in yourself or expose device codes,
agent credentials, or browser sessions.

Use `kado auth status` to inspect authentication and `kado agent list` to list
configured identities. Prefer CLI help for the exact syntax of less common
operations. Do not reconstruct Kado credentials, authentication requests,
release URLs, or lifecycle operations manually.

Keep search work in the separate `kado-search` skill.
