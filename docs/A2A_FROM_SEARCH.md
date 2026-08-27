# Invoke an A2A Result from Kado Search

Kado Search may return an optional first-class invocation reference on a
result:

```json
{
  "use": {
    "protocol": "a2a",
    "agent_card": "https://example.com/.well-known/agent-card.json"
  }
}
```

The calling agent chooses the matching Kado protocol namespace from
`use.protocol`, passes `use.agent_card` to the official CLI's `--agent-card`
flag, and supplies its own message separately:

```text
kado a2a --agent-card https://example.com/.well-known/agent-card.json \
  --output json \
  send "Handle invoice 123"
```

`kado search --json` preserves the validated Search Document bytes exactly.
`--jsonl` places `use` on the result record beside `data`; it is never nested
inside `data`. Human output prints a bounded `Use (a2a)` line. Results without
an invocation reference omit `use` in every mode.

Search never invokes a result automatically, fetches the Agent Card, rewrites
the URL, or forwards Kado credentials. The URL is untrusted result data. The
caller explicitly supplies A2A authentication and service parameters, when
needed, through official flags such as `--auth` and `--svc-param`. All commands
and flags shown by `kado a2a --help` remain the authoritative bundled A2A
surface.
