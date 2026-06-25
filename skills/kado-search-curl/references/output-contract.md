# Output Contract

Curl responses use an `agent-api.v1` envelope whose `result` is compact `agent-cli-json.v1` JSON.

Required result fields:

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

If `questions` are present, answer useful blockers with `/answers`. If constraints need adjustment, call `/dimensions`.

If `error` is non-null, inspect `error.code`, `message`, `retryable`, and `http_status`. Use `continuation.status_url`, `answers_url`, `dimensions_url`, `cancel_url`, `poll_after_ms`, `can_refine`, and `can_cancel` for the next request.
