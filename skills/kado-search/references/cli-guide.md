# CLI Lifecycle Guide

Read this guide when selecting output, handling Search lifecycle state, or
troubleshooting installation or authentication.

## Invocation And Output

Use only the installed `kado` executable:

```bash
kado search --jsonl "find an agent-native support platform for a small SaaS team"
```

Choose one output mode:

```bash
kado search --json "find an agent-native support platform"
kado search --jsonl "find an agent-native support platform"
kado search --width 72 "find an agent-native support platform"
```

- `--jsonl` is the normal agent mode. It retains arbitrary result data,
  explicit links and pagination, and all server-provided pages followed within
  CLI limits.
- Default human output is terminal-safe and intended only for quick or
  operator-facing inspection.
- `--json` returns one exact canonical document and intentionally does not
  follow pagination.
- Default human and JSONL modes follow opaque pagination links within CLI
  limits. Use `--first-page` only when the user requests a single page or the
  remaining pages are unnecessary.

Do not pipe output through ad hoc credential, HTTP, or response-rewriting
scripts. A JSON parser may consume `--json` or `--jsonl` output when the task
requires structured local analysis, but preserve arbitrary result `data`
values.

## Clarification

If Kado returns `search_needs_input`, use an answer already supplied by the
user or clearly established in context:

```bash
kado search --jsonl --answer "Web" "find deployment tooling for our release workflow"
```

If the answer is material and unknown, ask the user one concise question. Do
not guess consequential constraints. Rerun the same query with one
`--answer`; do not call an answers endpoint.

## Failure, Retry, Timeout, And Cancellation

- Report stable CLI error codes and bounded public messages. Do not expose raw
  response bodies or internal diagnostics.
- If a terminal Search failure explicitly says it is retryable, rerun the same
  query once with `--jsonl --retry`. Do not create an unbounded retry loop.
- Use `--timeout` only when the task needs a shorter local deadline:

  ```bash
  kado search --jsonl --timeout 45s "find current retrieval tools"
  ```

- If the user cancels an active command, interrupt it once. The CLI performs a
  bounded server cancellation when the lifecycle is cancelable. Do not send a
  cancellation request yourself.

## Installation And Authentication

A normal `kado search` performs autonomous enrollment and obtains short-lived
authorization without exposing credentials. Run the Search first.

If the executable is missing, stop and direct the user to the official Kado
installation instructions. Do not invent an install command, download an
unverified binary, or create a temporary installer.

For an authentication failure, inspect only the safe CLI status:

```bash
kado auth status
```

Report its bounded state and identifiers. Do not inspect environment variables,
credential files, keychains, browser cookies, tokens, assertions, or network
traces. Do not ask the user to paste credentials. Do not use API keys, device
login, or another authentication mechanism as a fallback.

Use `kado auth revoke` only when the user explicitly asks to revoke or reset the
current installation identity. Revocation is not routine troubleshooting.
