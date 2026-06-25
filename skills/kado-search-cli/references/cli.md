# CLI Reference

Use compact JSON for agent workflows:

```bash
kado search "find invoice approval automation" --json --wait
```

If `kado` is not installed:

```bash
npx -y @kado/agent search "find invoice approval automation" --json --wait
```

Set `KADO_SEARCH_APP_URL` or pass `--base-url` when the hosted app URL is not already configured. Authenticate with an existing stored CLI login or `KADO_API_KEY`; never print credentials.

Common commands:

```bash
kado search status <search-id> --json
kado search refine <search-id> --json --wait --dimension budget_monthly_usd=200
kado search answer <search-id> <question-id> "About 750 leads per month" --json --wait
kado search cancel <search-id>
```

When JSON includes `continuation.next_command`, prefer that exact command for the next CLI step.
