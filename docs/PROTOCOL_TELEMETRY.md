# MCP and A2A conversion telemetry

The Kado CLI emits two best-effort events around non-completion `kado mcp` and
`kado a2a` invocations when an existing Kado agent credential is available:

- `protocol_command_started`
- `protocol_command_finished`

The authenticated ingestion route is
`POST /api/product-analytics/protocol-command`. The request body uses
`schema_version: kado.protocol-command.v1` and carries only:

- `attempt_id`, `phase`, `protocol`, and a bounded command classification;
- a normalized HTTP(S) endpoint or Agent Card URL when explicitly supplied;
- `auth_supplied`, which says only whether an authentication option was
  present;
- start time and, for completions, duration, exit code, and success.

URL user information is rejected. Query strings and fragments are removed.
Messages, tool arguments and results, headers, tokens, profile names, session
names, task IDs, and service-parameter values are never sent. Named MCP
sessions are reported as the constant `@session`, so their endpoint cannot be
correlated in this first version.

Reporting is fail-open and bounded. It cannot alter delegated stdout, stderr,
or exit status.

## Server mapping

The server should authenticate the agent principal, validate the closed event
schema, and map `phase=started` and `phase=finished` to the two PostHog event
names above. It should add `use_protocol` and an identically normalized
`use_target` to `search_result_impression`. A target match for the same
principal within the selected attribution window gives the minimal funnel:

1. result returned;
2. endpoint attempted;
3. MCP `login` attempted and, when exit code is zero, authenticated;
4. MCP `tools-call` or A2A `send` attempted and, when exit code is zero, used.

A zero exit code means the protocol command succeeded; it does not prove that
the provider completed the user's ultimate task.
