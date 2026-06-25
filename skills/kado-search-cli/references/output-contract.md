# Output Contract

CLI JSON is compact `agent-cli-json.v1` from the hosted `/api/agent/*` contract.

Required top-level fields:

- `schema_version`
- `search_id`
- `search_url`
- `state`
- `best_matches`
- `stretch_matches`
- `questions`
- `dimensions`
- `error`
- `continuation`

States are `accepted`, `structuring`, `hydrating_hyde`, `retrieving`, `ranking`, `completed`, `failed`, or `canceled`.

Use `search_url` when citing or opening the Kado UI for the search. Use `best_matches` first. Each match may include `rank`, `solution_id`, `name`, `summary`, `solution_url`, `why`, `source_url`, `score`, `constraints`, and `required_integrations`; prefer `solution_url` when opening a specific result.

If `questions` are present, answer useful blockers with `kado search answer`. If constraints need adjustment, use `kado search refine --dimension name=value`.

If `error` is non-null, inspect `error.code`, `message`, `retryable`, and `http_status`. Use `continuation.status_url`, `poll_after_ms`, `can_refine`, `can_cancel`, and `next_command` to continue safely.
