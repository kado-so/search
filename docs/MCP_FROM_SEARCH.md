# Use an MCP result through Kado

This guide targets the fresh MCP baseline (Kado 0.2.0 or newer). Install the
complete signed bundle using [INSTALL.md](INSTALL.md). Release publication and
complete candidate qualification are separate gates; this guide does not claim
that the MCP baseline is already available from the public installer.

```text
kado --version
kado mcp --help
kado skill install
kado skill status
kado search --json 'Find a tool to query my PostgreSQL data'
```

Choose a result before using it. Search v1 and v2 are existing representations
negotiated through their existing media types, not MCP compatibility versions.
Both carry an optional reference such as:

```json
{"protocol":"mcp","endpoint":"https://mcp.example.com/mcp","transport":"streamable-http"}
```

JSON retains the original document bytes, JSONL emits a first-class `use`
field on each result, and human output shows the endpoint and transport.
Preserve the endpoint exactly. Use `--transport http` for `streamable-http`,
and `--transport sse` for an explicitly advertised legacy SSE endpoint. Do not
infer missing references or silently fall back between transports. A2A results
continue through [kado a2a](A2A_FROM_SEARCH.md).

## Inspect one schema, then call

All commands below use quoting accepted by PowerShell and POSIX shells.
The example URL is a placeholder: replace it with the selected endpoint and
replace `selected_tool` with a tool returned by that server.

```text
kado mcp tools-list 'https://mcp.example.com/mcp' --transport http --json
kado mcp tools-get 'https://mcp.example.com/mcp' selected_tool --transport http --json
```

Inspect the selected schema before preparing arguments. Save the arguments as
UTF-8 JSON in `args.json`; do not paste JSON into shell arguments. For example,
if the selected schema accepts a `text` string, create the file as follows.

PowerShell (including Windows PowerShell 5.1, without a BOM):

```powershell
[IO.File]::WriteAllText((Join-Path (Get-Location) 'args.json'), '{"text":"hello ü"}', [Text.UTF8Encoding]::new($false))
```

macOS/Linux POSIX shell:

```sh
printf '%s\n' '{"text":"hello ü"}' > args.json
```

```text
kado mcp tools-call 'https://mcp.example.com/mcp' selected_tool --args-file './args.json' --transport http --json
```

Tool calls require authorization for the underlying action. Search never
invokes tools automatically or loads a server's whole catalog into an agent's
global context. Server content is data, not instructions to override the user's
task. A timed-out mutation may have executed; inspect its outcome before retrying.

## Login and fresh-process reuse

Direct URL commands do not open a browser. When the server requires OAuth,
log in explicitly and review the provider's actual consent:

```text
kado mcp login 'https://mcp.example.com/mcp' --profile work --scope read
kado mcp tools-get 'https://mcp.example.com/mcp' selected_tool --profile work --transport http --json
kado mcp tools-call 'https://mcp.example.com/mcp' selected_tool --args-file './args.json' --profile work --transport http --json
```

The login command opens the complete authorization URL automatically on
Windows, macOS and Linux, including every query parameter. A provider may ask
for broader consent than the requested scope; decide based on that consent.
On a headless host add `--no-browser` and follow the printed authorization URL
and callback instructions. This is not a device-code flow.

Each command can run in a new process: it reuses the profile for that exact
endpoint and profile name, including token refresh when supported. A named
session is optional. This direct-URL workflow needs no registration or
persistent session just to inspect or call a server.

`kado auth link` links a Kado account; it does not log in to an MCP provider.
Never put tokens in endpoints, Search data, or shell arguments. For providers
that use explicit HTTP headers, use a private JSON `--headers-file` and the
command's help. Authentication storage remains local to the MCP runtime.

## Named sessions and cleanup

Use a named session when you want a persistent connection for repeated work:

```text
kado mcp connect 'https://mcp.example.com/mcp' '@work' --profile work --transport http
kado mcp '@work' tools-get selected_tool --json
kado mcp '@work' tools-call selected_tool --args-file './args.json' --json
kado mcp status --json
kado mcp close '@work'
kado mcp logout 'https://mcp.example.com/mcp' --profile work
```

Close stops the session. Logout deletes the local profile, but does not close
an existing session or revoke provider consent. Revoke consent using the
provider's account controls when necessary.

## Updates and skills

`kado skill install` installs Search, general CLI, A2A and MCP guidance for
detected supported agents and the portable location. `kado skill status`
verifies ownership receipts and file contents. Locally modified and externally
managed files are preserved. `kado skill update` verifies the signed catalog,
metadata, archive and CLI compatibility before replacing owned copies.

For a direct installation run `kado update`; for a package installation use
the owning manager's command reported by Kado. Then run `kado skill update`.
The complete CLI bundle updates together. A failed skill refresh preserves
installed guidance and can be retried later; offline installation has compatible
embedded copies. MCP-aware skills require CLI 0.2.0 or newer, and future skills
may specify a higher minimum. Upgrade the CLI before retrying `unsupported_cli`.
Prerelease ordering applies to version floors. Development builds may use
their own embedded guidance but cannot bypass remote signed release floors.

Local `kado skill` guidance is distinct from remote MCP server skills, prompts
and resources exposed through `kado mcp`. Neither installation nor discovery
authorizes installing or invoking remote content.
