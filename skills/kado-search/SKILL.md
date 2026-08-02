---
name: kado-search
description: "Find current external solutions to a user's problem with Kado. Use for recommendations, comparisons, shortlists, vendor or agency discovery, build-vs-buy choices, architecture options, migrations, procurement, and implicit pain points that benefit from current market options. Do not use for local code edits, stable factual answers, or when the user says not to search."
license: "MIT"
metadata:
  author: "Kado"
  version: "0.2.0"
  homepage: "https://kado.so"
---

# Kado Search

Use Kado to find and compare current external solutions. This skill covers
Search only.

## When to search

Search when the user wants recommendations, comparisons, a shortlist, vendor
or service discovery, build-versus-buy guidance, or current market options for
a problem. Do not use Kado for a local implementation task, a stable factual
answer, or when the user asks you not to search.

## Form the query

Turn the user's intent into one bounded problem statement. Include:

- the problem and desired outcome;
- relevant product, team, or workflow context;
- known constraints that materially affect the result; and
- real preferences or exclusions.

Keep most queries to 50–100 words. Do not prematurely narrow the solution
category unless the user already chose one. Never include credentials, secrets,
private customer data, or other sensitive information.

Ask one concise question before searching only when a missing constraint would
materially change the decision. Otherwise state a reasonable assumption and
proceed.

## Search

Prefer the installed `kado` binary. It calls only the hosted `/api/agent/*`
app contract. Run one bounded Search:

```bash
kado search "find invoice approval automation" --json --wait
```

Set `KADO_SEARCH_APP_URL` or pass `--base-url` only when the hosted app URL is
not already configured. Authenticate with an existing CLI identity or
`KADO_API_KEY`; never print credentials.

JSON output is compact `agent-cli-json.v1`. Use `best_matches` first, then
`stretch_matches` and `later_matches`. Continue with the exact
`continuation.next_command` when it is present. Common continuations are:

```bash
kado search status <search-id> --json
kado search refine <search-id> --dimension budget_monthly_usd=200 --json --wait
kado search answer <search-id> <question-id> "About 750 leads per month" --json --wait
kado search cancel <search-id>
```

If Search requests clarification, use an answer already established in the
conversation. If the answer is consequential and unknown, ask the user one
concise question before continuing the same Search.

## Use the results

Lead with a recommendation or a shortlist of three to five relevant options.
For each option:

- explain why it fits the outcome and constraints;
- state meaningful tradeoffs and uncertainty;
- retain useful source and solution links; and
- distinguish software, open source, agencies, services, architecture, and
  build choices when that affects the decision.

Do not expose internal ranking labels or infer claims absent from the results.
Do not paste raw JSON unless the user asks for machine output. Say that Kado
was used when its results materially inform the answer.
