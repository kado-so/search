# Search API

Use this reference after [Auth API](auth-api.md) has resolved `KADO_TOKEN` on macOS/Linux or `$KadoToken` on Windows. Call only the public Search app at `/api/agent`; never call internal `/v1/agent` routes.

All requests and responses use `agent-search.v1`. Create and refinement require a unique `Idempotency-Key`. Default result pages contain 10 solutions; the maximum requested page size is 50.

## Create and wait

Build JSON with a real encoder so user text is never hand-escaped.

macOS/Linux:

```bash
QUERY='Find support ticket deflection solutions for a three-person SaaS team under $200/month with Slack integration.'
IDEMPOTENCY_KEY="kado-$(date +%s)-$$-$RANDOM"
RESPONSE="$(printf '%s' "$QUERY" | jq -Rnc '{schema_version:"agent-search.v1",query:input,wait:{timeout_ms:60000}}' | \
  "${KADO_CURL[@]}" "${KADO_RETRY[@]}" "${KADO_AUTH[@]}" -X POST "$KADO_BASE_URL/api/agent/searches" \
    -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
    -H "content-type: application/json" \
    -H "accept: application/json" \
    --data-binary @-)"
SEARCH_ID="$(printf '%s' "$RESPONSE" | jq -er '.search_id')"
printf '%s\n' "$RESPONSE" | jq
```

Windows PowerShell:

```powershell
$Query = 'Find support ticket deflection solutions for a three-person SaaS team under $200/month with Slack integration.'
$IdempotencyKey = [guid]::NewGuid().ToString()
$CreateRequest = @{
  schema_version = 'agent-search.v1'
  query = $Query
  wait = @{ timeout_ms = 60000 }
} | ConvertTo-Json -Depth 5 -Compress
$ResponseText = $CreateRequest | curl.exe @KadoCurlArgs @KadoRetryArgs @KadoAuthArgs -X POST "$KadoBaseUrl/api/agent/searches" `
  -H "Idempotency-Key: $IdempotencyKey" `
  -H 'content-type: application/json' -H 'accept: application/json' --data-binary '@-'
$Response = $ResponseText | ConvertFrom-Json
$SearchId = [string]$Response.search_id
$Response | ConvertTo-Json -Depth 10
```

A 60-second wait ending with `timed_out: true` does not cancel or fail the Search.

## Continue a bounded wait

Poll at the returned `poll_after_ms` interval. A normal agent loop should use at most five 60-second waits before yielding and retaining the Search ID for later continuation.

macOS/Linux:

```bash
for ATTEMPT in 1 2 3 4 5; do
  STATUS="$("${KADO_CURL[@]}" "${KADO_RETRY[@]}" "${KADO_AUTH[@]}" --get "$KADO_BASE_URL/api/agent/searches/$SEARCH_ID" \
    -H "accept: application/json" \
    --data-urlencode 'wait_ms=60000')"
  STATE="$(printf '%s' "$STATUS" | jq -r '.state')"
  case "$STATE" in completed|failed|canceled) break ;; esac
done
printf '%s\n' "$STATUS" | jq
```

Windows PowerShell:

```powershell
for ($Attempt = 1; $Attempt -le 5; $Attempt++) {
  $StatusText = curl.exe @KadoCurlArgs @KadoRetryArgs @KadoAuthArgs "$KadoBaseUrl/api/agent/searches/$SearchId`?wait_ms=60000" `
    -H 'accept: application/json'
  $Status = $StatusText | ConvertFrom-Json
  if ($Status.state -in @('completed', 'failed', 'canceled')) { break }
}
$Status | ConvertTo-Json -Depth 10
```

## Read results and continue with the cursor

macOS/Linux:

```bash
PAGE="$("${KADO_CURL[@]}" "${KADO_RETRY[@]}" "${KADO_AUTH[@]}" --get "$KADO_BASE_URL/api/agent/searches/$SEARCH_ID/results" \
  -H "accept: application/json" \
  --data-urlencode 'limit=10')"
printf '%s\n' "$PAGE" | jq

NEXT_CURSOR="$(printf '%s' "$PAGE" | jq -r '.next_cursor // empty')"
if [ -n "$NEXT_CURSOR" ]; then
  NEXT_PAGE="$("${KADO_CURL[@]}" "${KADO_RETRY[@]}" "${KADO_AUTH[@]}" --get "$KADO_BASE_URL/api/agent/searches/$SEARCH_ID/results" \
    -H "accept: application/json" \
    --data-urlencode "cursor=$NEXT_CURSOR")"
  printf '%s\n' "$NEXT_PAGE" | jq
fi
```

Windows PowerShell:

```powershell
$PageText = curl.exe @KadoCurlArgs @KadoRetryArgs @KadoAuthArgs "$KadoBaseUrl/api/agent/searches/$SearchId/results?limit=10" `
  -H 'accept: application/json'
$Page = $PageText | ConvertFrom-Json
$Page | ConvertTo-Json -Depth 10

if ($Page.next_cursor) {
  $Cursor = [uri]::EscapeDataString([string]$Page.next_cursor)
  $NextPage = curl.exe @KadoCurlArgs @KadoRetryArgs @KadoAuthArgs "$KadoBaseUrl/api/agent/searches/$SearchId/results?cursor=$Cursor" `
    -H 'accept: application/json' | ConvertFrom-Json
  $NextPage | ConvertTo-Json -Depth 10
}
```

Do not add `limit` when following a cursor; its page size is already bound into the opaque cursor.

## Read one result or lineage

URL-encode a returned `variant_id` before using it in the result-detail path. `solution_id` identifies only the parent solution and is not a detail-route key.

```bash
VARIANT_ID="$(printf '%s' "$PAGE" | jq -er '.results[0].variant_id')"
ENCODED_VARIANT_ID="$(printf '%s' "$VARIANT_ID" | jq -sRr @uri)"
"${KADO_CURL[@]}" "${KADO_RETRY[@]}" "${KADO_AUTH[@]}" "$KADO_BASE_URL/api/agent/searches/$SEARCH_ID/results/$ENCODED_VARIANT_ID" \
  -H "accept: application/json" | jq
"${KADO_CURL[@]}" "${KADO_RETRY[@]}" "${KADO_AUTH[@]}" "$KADO_BASE_URL/api/agent/searches/$SEARCH_ID/lineage" \
  -H "accept: application/json" | jq
```

```powershell
$VariantId = [uri]::EscapeDataString([string]$Page.results[0].variant_id)
curl.exe @KadoCurlArgs @KadoRetryArgs @KadoAuthArgs "$KadoBaseUrl/api/agent/searches/$SearchId/results/$VariantId" `
  -H 'accept: application/json'
curl.exe @KadoCurlArgs @KadoRetryArgs @KadoAuthArgs "$KadoBaseUrl/api/agent/searches/$SearchId/lineage" `
  -H 'accept: application/json'
```

## Refine with any answered subset

Submit only asked question IDs. Answer some or all useful questions; omit unanswered questions. A refinement creates a new Search with lineage back to its parent.

macOS/Linux:

```bash
LATEST_RESPONSE="${STATUS:-$RESPONSE}"
QUESTION_ID="$(printf '%s' "$LATEST_RESPONSE" | jq -er '.questions[0].id')"
ANSWER='About 750 tickets per month'
REFINE_KEY="kado-refine-$(date +%s)-$$-$RANDOM"
REFINEMENT="$(printf '%s\0%s' "$QUESTION_ID" "$ANSWER" | \
  jq -Rsc 'split("\u0000") | {schema_version:"agent-search.v1",answers:[{question_id:.[0],values:[.[1]]}],wait:{timeout_ms:60000}}' | \
  "${KADO_CURL[@]}" "${KADO_RETRY[@]}" "${KADO_AUTH[@]}" -X POST "$KADO_BASE_URL/api/agent/searches/$SEARCH_ID/refinements" \
    -H "Idempotency-Key: $REFINE_KEY" \
    -H "content-type: application/json" -H "accept: application/json" --data-binary @-)"
CHILD_SEARCH_ID="$(printf '%s' "$REFINEMENT" | jq -er '.search_id')"
printf '%s\n' "$REFINEMENT" | jq
```

Windows PowerShell:

```powershell
$LatestResponse = if ($Status) { $Status } else { $Response }
$QuestionId = [string]$LatestResponse.questions[0].id
$RefineRequest = @{
  schema_version = 'agent-search.v1'
  answers = @(@{ question_id = $QuestionId; values = @('About 750 tickets per month') })
  wait = @{ timeout_ms = 60000 }
} | ConvertTo-Json -Depth 6 -Compress
$RefinementText = $RefineRequest | curl.exe @KadoCurlArgs @KadoRetryArgs @KadoAuthArgs -X POST "$KadoBaseUrl/api/agent/searches/$SearchId/refinements" `
  -H "Idempotency-Key: $([guid]::NewGuid())" `
  -H 'content-type: application/json' -H 'accept: application/json' --data-binary '@-'
$Refinement = $RefinementText | ConvertFrom-Json
$ChildSearchId = [string]$Refinement.search_id
$Refinement | ConvertTo-Json -Depth 10
```

## Cancel an active Search

```bash
"${KADO_CURL[@]}" "${KADO_AUTH[@]}" -X POST "$KADO_BASE_URL/api/agent/searches/$SEARCH_ID/cancel" \
  -H "accept: application/json" | jq
```

```powershell
curl.exe @KadoCurlArgs @KadoAuthArgs -X POST "$KadoBaseUrl/api/agent/searches/$SearchId/cancel" `
  -H 'accept: application/json'
```

Cancellation is valid only while the Search is active. The agent contract intentionally has no retry or delete operation.
