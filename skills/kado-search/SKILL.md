---
name: kado-search
description: Find current external solutions to a user's problem with Kado. Use for recommendations, comparisons, shortlists, vendor or agency discovery, build-vs-buy choices, architecture options, migrations, procurement, and implicit pain points that benefit from current market options. Do not use for a local code edit, a stable factual answer, or when the user says not to search.
---

# Kado Search

Use the installed `kado` CLI as the only Kado interface. Never replace it with
`curl`, direct HTTP, API keys, device flows, browser cookies, copied tokens, or
temporary authentication scripts. The CLI owns enrollment, credentials,
authorization, lifecycle operations, and response validation.

## Workflow

1. Turn the user's intent into one bounded problem statement. Read
   [Query Guide](references/query-guide.md) when constraints or solution scope
   need synthesis.
2. Choose the representation:
   - Use `--jsonl` for normal agent synthesis. It retains arbitrary result data,
     explicit links and pagination, and all followed pages.
   - Use default human output only for quick or operator-facing inspection.
   - Use `--json` only when one exact canonical Search Document is required.
3. Invoke the executable with the query as one quoted argument:

   ```bash
   kado search --jsonl "We need to reduce repetitive support tickets while preserving human escalation. We use Zendesk and have a small support team."
   ```

4. Let the CLI follow Search status and pagination. Handle clarification,
   retry, timeout, cancellation, and authentication only as described in
   [CLI Lifecycle Guide](references/cli-guide.md).
5. Interpret results with [Response Guide](references/response-guide.md).

## Boundaries

- Keep most queries to 50–100 words. Include only known constraints that
  materially affect the result. Never put credentials, private keys, tokens,
  cookies, secrets, or private customer data in a query.
- Search broadly when the user wants an outcome and has not selected a solution
  category. Do not silently turn a local implementation request into vendor
  research.
- Ask one concise question before Search only when a missing constraint would
  materially change the decision. Otherwise state a reasonable assumption and
  proceed.
- Do not paste full JSON or JSONL into the final answer unless the user asks for
  the machine representation. Prefer a bounded shortlist with relevant links,
  fit, tradeoffs, and uncertainty.
- Never inspect credential stores, environment variables, process state,
  browser sessions, or network traces to troubleshoot Kado. Never ask the user
  to reveal authentication material.
- If `kado` is not installed, stop and direct the user to the official Kado
  installation instructions. Do not invent an installer or download an
  unverified executable.
