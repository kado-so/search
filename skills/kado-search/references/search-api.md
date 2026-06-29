# Search API

Use this reference after authentication is available. Start with:

```bash
export KADO_BASE_URL="https://kado.so"
export KADO_AUTH_HEADER="Authorization: Bearer $KADO_API_KEY"
```

On Windows PowerShell:

```powershell
$env:KADO_BASE_URL = 'https://kado.so'
$env:KADO_AUTH_HEADER = "Authorization: Bearer $env:KADO_API_KEY"
```

If no API key is available, use [Auth API](auth-api.md) for example device-login code.

Use a real JSON encoder for user text. Do not hand-escape JSON when the query includes quotes, dollar signs, or newlines.

Build a payload on macOS/Linux shells:

```bash
QUERY="find a support ticket deflection solution for a seed-stage SaaS team under $200/month"
PAYLOAD="$(jq -n --arg query "$QUERY" '{
  schema_version: "agent-api.v1",
  query: $query,
  mode: "compact",
  wait: {
    until: "completed_or_terminal",
    timeout_ms: 120000,
    poll_interval_ms: 2000
  }
}')"
```

Build a payload on Windows PowerShell:

```powershell
$Query = 'find a support ticket deflection solution for a seed-stage SaaS team under $200/month'
$Payload = @{
  schema_version = 'agent-api.v1'
  query = $Query
  mode = 'compact'
  wait = @{
    until = 'completed_or_terminal'
    timeout_ms = 120000
    poll_interval_ms = 2000
  }
} | ConvertTo-Json -Depth 6 -Compress
```

Start a search on macOS/Linux shells and save the response:

```bash
RESPONSE="$(curl -sS "$KADO_BASE_URL/api/agent/searches" \
  -H "content-type: application/json" \
  -H "accept: application/json" \
  -H "$KADO_AUTH_HEADER" \
  --data "$PAYLOAD")"
printf '%s\n' "$RESPONSE"
```

Start a search on Windows PowerShell and save the response:

```powershell
$Response = curl.exe -sS "$env:KADO_BASE_URL/api/agent/searches" `
  -H "content-type: application/json" `
  -H "accept: application/json" `
  -H "$env:KADO_AUTH_HEADER" `
  --data "$Payload"
$Response
```

Use `wait.until:"completed_or_terminal"` for normal agent interactions. If the host has short command timeouts, start the search without `wait`, then poll status.

Read status on macOS/Linux shells:

```bash
curl -sS "$KADO_BASE_URL/api/agent/searches/<search-id>" \
  -H "accept: application/json" \
  -H "$KADO_AUTH_HEADER"
```

Use the same endpoint with `curl.exe` and `$env:KADO_AUTH_HEADER` on Windows PowerShell.

Extract the useful result object when you need to inspect it locally:

```bash
printf '%s\n' "$RESPONSE" | jq '.result'
```

```powershell
($Response | ConvertFrom-Json).result
```

Refine dimensions:

```bash
curl -sS "$KADO_BASE_URL/api/agent/searches/<search-id>/dimensions" \
  -H "content-type: application/json" \
  -H "accept: application/json" \
  -H "$KADO_AUTH_HEADER" \
  --data '{"schema_version":"agent-api.v1","mode":"compact","dimensions":[{"id":"budget_monthly_usd","value":"200"}]}'
```

Answer a question:

```bash
curl -sS "$KADO_BASE_URL/api/agent/searches/<search-id>/answers" \
  -H "content-type: application/json" \
  -H "accept: application/json" \
  -H "$KADO_AUTH_HEADER" \
  --data '{"schema_version":"agent-api.v1","mode":"compact","answers":[{"question_id":"lead_volume","answer":"About 750 leads per month"}]}'
```

Cancel:

```bash
curl -sS "$KADO_BASE_URL/api/agent/searches/<search-id>/cancel" \
  -H "content-type: application/json" \
  -H "accept: application/json" \
  -H "$KADO_AUTH_HEADER" \
  --data '{"schema_version":"agent-api.v1","reason":"agent_no_longer_needs_result"}'
```

If the response includes questions, answer only the ones that materially improve the recommendation. If constraints are wrong or incomplete, refine dimensions.
