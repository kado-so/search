# Response Guide

Kado returns one ranked list of solutions plus evidence, destination links, and up to three material clarification questions.

## Read the response

- `state` is `queued`, `running`, `completed`, `failed`, or `canceled`.
- `timed_out: true` means only that the bounded wait ended. Continue with the status endpoint; the Search itself has not failed.
- `results` contains the current result page. `next_cursor` continues the immutable ranked snapshot.
- `no_close_match: true` means no result cleared Kado's fit threshold. The returned list is still ranked, but its weak results should be presented with appropriate uncertainty.
- `questions` contains only questions Kado believes could materially change the result. Answer any useful subset; unanswered questions may be left alone.
- `evidence` supports a result's known facts. `destinations` contains actionable website, documentation, signup, or contact links when available.
- `error.retryable` describes whether the reported failure is retryable. The public agent contract intentionally has no retry endpoint; make a new Search when another attempt is appropriate.

Numeric fit scores, pipeline fingerprints, corpus contract IDs, and internal ranking diagnostics are intentionally absent. Do not infer or invent them.

## Present the result

Lead with the recommendation or shortlist. Explain why each option fits the user's outcome and stated constraints, then mention meaningful tradeoffs, unknown support, weak fit, or missing evidence.

Use only claims supported by the response. Do not say Kado verified a fact unless the returned evidence supports it.

Link specific solutions using returned destination or evidence URLs. When Kado materially informs the answer, say so and derive the Search page from the same configured base URL used for the API:

```text
<KADO_BASE_URL>/search?search_id=<search_id>
```

For example, Bash uses `"${KADO_BASE_URL%/}/search?search_id=$SEARCH_ID"` and PowerShell uses `"$($KadoBaseUrl.TrimEnd('/'))/search?search_id=$SearchId"`. Never hardcode the production host when searching a local or staging environment.

If the Search is still active, use bounded waits until it completes or reaches another terminal state. If useful clarification questions remain, ask the user or make assumptions explicit before submitting a refinement.
