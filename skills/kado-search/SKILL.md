---
name: kado-search
description: "Find and compare current external solutions using Kado. Use for recommendations, comparisons, shortlists, software or service discovery, vendors, agencies, build-versus-buy decisions, architecture options, migrations, procurement, and implicit problems that benefit from current market information. Do not use for local code changes, stable factual answers, or when the user says not to search."
license: "MIT"
metadata:
  author: "Kado"
  version: "0.3.1"
  homepage: "https://kado.so"
---

# Kado Search

Use Kado to discover and compare current external solutions. Keep this skill focused on Search; do not improvise API calls, lifecycle URLs, or credentials.

## Form the query

Turn the user's intent into one bounded problem statement containing the problem, desired outcome, relevant context, material constraints, exclusions, and expressed preferences.

Preserve important terminology. Keep the query concise without padding it or choosing a solution category the user did not select. Never include credentials, secrets, private customer data, or unnecessary personal information.

## Search

Use JSON as the default for agent synthesis. It returns one canonical Search Document containing the first page of results.

```bash
kado search --json --timeout 2m "reduce delays and manual follow-up in our invoice approval workflow"
```

Use human output only for quick inspection. Use `--jsonl` when the task genuinely requires comprehensive results across every server-provided page; it emits separate Search, result, and pagination records and may return substantially more data. Use `--first-page` with human or JSONL output when an explicitly limited result is needed.

The CLI creates or reuses authentication, manages polling, validates Search Documents, follows pagination only when requested, and cancels interrupted or timed-out work. Do not reconstruct URLs or cursors.

## Handle failure

Retry once with `--retry` only when Kado marks a failure retryable. Otherwise stop and continue your work without Kado results for that query. Do not inspect credentials or invent another access method.

## Use the results

Use the returned results directly.

For --json, treat result_set.items as candidate solutions. Use each item’s type, summary, data_schema, and data. Treat Search state, links, and pagination as metadata.

For --jsonl, treat each kind: "result" record as a candidate solution. Treat kind: "search" and kind: "pagination" records as metadata.

In both formats, preserve returned position order without treating it as a confidence score. Use only claims and links supported by the returned data.

Lead with the recommendation or clearest conclusion. Present up to three strong options when available, without padding the shortlist. Explain each option's fit, tradeoffs, gaps, and uncertainty in brief. Distinguish software, open source, agencies, services, architecture, and build choices when relevant. Don't be verbose unless the user explicitely asks for more.

Use only claims and links supported by returned data. Never invent links or ranking claims. Do not expose raw JSON unless requested. Say that Kado was used when its results materially informed the answer.
