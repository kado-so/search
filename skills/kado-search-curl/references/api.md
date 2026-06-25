# API Reference

Use a hosted Kado app base URL supplied by the user or environment. Prefer `KADO_API_KEY` when it is already known or supplied by the user, and send that key only in `x-api-key`.

If no API key is available, use device authorization:

1. Request a device code.
2. Show `verification_uri_complete` to the user and ask them to approve it.
3. Poll for a token using `device_code`.
4. Store the returned bearer token in user-local credential storage outside the current project directory and use `Authorization: Bearer <access_token>` for follow-on requests.

Do not print or log `access_token`, `device_code`, API keys, cookies, or full request headers.

Request a device code:

```bash
curl -sS "$KADO_SEARCH_APP_URL/api/auth/device/code" \
  -H "content-type: application/json" \
  -H "accept: application/json" \
  --data '{"client_id":"kado-cli"}'
```

Poll for a device token after the user approves the verification link:

```bash
curl -sS "$KADO_SEARCH_APP_URL/api/auth/device/token" \
  -H "content-type: application/json" \
  -H "accept: application/json" \
  --data '{"client_id":"kado-cli","device_code":"<device-code>","grant_type":"urn:ietf:params:oauth:grant-type:device_code"}'
```

Handle polling errors as follows: retry `authorization_pending`, increase the poll interval for `slow_down`, restart authorization for `expired_token`, and stop on `access_denied` or `invalid_grant`.

Use exactly one auth header for agent API requests:

```bash
if [ -n "${KADO_API_KEY:-}" ]; then
  KADO_AUTH_HEADER_NAME="x-api-key"
  KADO_AUTH_HEADER_VALUE="$KADO_API_KEY"
else
  KADO_AUTH_HEADER_NAME="authorization"
  KADO_AUTH_HEADER_VALUE="Bearer $KADO_DEVICE_ACCESS_TOKEN"
fi
```

Start a search:

```bash
curl -sS "$KADO_SEARCH_APP_URL/api/agent/searches" \
  -H "content-type: application/json" \
  -H "accept: application/json" \
  -H "$KADO_AUTH_HEADER_NAME: $KADO_AUTH_HEADER_VALUE" \
  --data '{"schema_version":"agent-api.v1","query":"find invoice approval automation","mode":"compact","wait":{"until":"completed_or_terminal","timeout_ms":120000,"poll_interval_ms":2000}}'
```

Read status:

```bash
curl -sS "$KADO_SEARCH_APP_URL/api/agent/searches/<search-id>" \
  -H "accept: application/json" \
  -H "$KADO_AUTH_HEADER_NAME: $KADO_AUTH_HEADER_VALUE"
```

Refine dimensions:

```bash
curl -sS "$KADO_SEARCH_APP_URL/api/agent/searches/<search-id>/dimensions" \
  -H "content-type: application/json" \
  -H "accept: application/json" \
  -H "$KADO_AUTH_HEADER_NAME: $KADO_AUTH_HEADER_VALUE" \
  --data '{"schema_version":"agent-api.v1","mode":"compact","dimensions":[{"id":"budget_monthly_usd","value":"200"}]}'
```

Answer a question:

```bash
curl -sS "$KADO_SEARCH_APP_URL/api/agent/searches/<search-id>/answers" \
  -H "content-type: application/json" \
  -H "accept: application/json" \
  -H "$KADO_AUTH_HEADER_NAME: $KADO_AUTH_HEADER_VALUE" \
  --data '{"schema_version":"agent-api.v1","mode":"compact","answers":[{"question_id":"lead_volume","answer":"About 750 leads per month"}]}'
```

Cancel:

```bash
curl -sS "$KADO_SEARCH_APP_URL/api/agent/searches/<search-id>/cancel" \
  -H "content-type: application/json" \
  -H "accept: application/json" \
  -H "$KADO_AUTH_HEADER_NAME: $KADO_AUTH_HEADER_VALUE" \
  --data '{"schema_version":"agent-api.v1","reason":"agent_no_longer_needs_result"}'
```

Use the `result` object from the API envelope as the compact agent output.
