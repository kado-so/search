# MCP and A2A telemetry

Telemetry is disabled by default and can be enabled for one invocation:

```text
kado --telemetry mcp ...
kado --telemetry a2a ...
```

When enabled, Kado records the protocol, command category, outcome, timing,
whether authentication was supplied, and a normalized endpoint when one is
explicitly provided.

Kado never records messages, tool arguments or results, credentials, headers,
tokens, or URL query strings and fragments. Telemetry is best-effort and does
not change command output or exit status.
