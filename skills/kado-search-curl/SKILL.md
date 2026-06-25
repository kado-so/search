---
name: kado-search-curl
description: Use Kado Search from coding agents with curl against hosted /api/agent/* routes, preferring user-provided API keys and falling back to device authorization, producing compact agent-cli-json.v1 JSON.
---

# Kado Search Curl

Prefer a user-provided API key when `KADO_API_KEY` is already known or supplied by the user, and send it only with `x-api-key`. If no API key is available, use the device grant flow: request a device code, show the verification link to the user, poll for a bearer token after approval, and use `Authorization: Bearer <token>` for follow-on requests.

Never scrape browser cookies, reuse browser sessions, print credentials, commit credentials, or call undocumented service endpoints directly.

Call hosted app `/api/agent/*` routes and parse compact `agent-cli-json.v1` JSON.

For request syntax, read [API reference](references/api.md). For parsing and citing JSON, read [output contract](references/output-contract.md).
