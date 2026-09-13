---
name: kado-cli-non-search
description: Use this for extra info on how to use the Kado CLI - account linking, authentication status, agent identity management, diagnostics, updates, and other non-search Kado operations. Use when the user asks to set up, connect, inspect, repair, update, or remove Kado. Do not use this skill for solution discovery or search; use kado-search for that.
license: "MIT"
metadata:
  author: "Kado"
  version: "0.2.0"
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

## MCP and local skills

Use `kado-mcp` for a known MCP endpoint or named session, and `kado-a2a` for an
A2A Agent Card. The public MCP namespace is `kado mcp`; its runtime is bundled
with Kado. MCP provider login (`kado mcp login`) and profiles are separate from
Kado account linking (`kado auth link`). Never reuse Kado account credentials
as provider credentials.

`kado skill install` installs all four bundled skills for detected agents and
the portable location. Inspect ownership with `kado skill status`, refresh with
`kado skill update`, and remove owned guidance with `kado skill uninstall`.
These commands manage local guidance, not remote MCP server skills or consent.
Locally modified or externally managed copies are preserved and reported.

MCP-aware guidance requires Kado 0.2.0 or newer. Update a direct installation
with `kado update`, or use the owning package manager when Kado identifies one.
Then retry `kado skill update`. Incompatible skill releases are refused without
overwriting existing copies. Offline installation can use compatible bundled
copies; a failed signed refresh leaves installed guidance in place.
