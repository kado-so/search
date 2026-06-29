# Response Guide

Kado responses use an API envelope with a recommendation result inside it. Use the result to understand the search state, recommendations, refinements, and useful links.

Use `search_url` when citing the Kado search. If a recommendation has `solution_url` or `source_url`, use those links for specific tools or sources.

Do not expose internal ranking buckets or labels to the user. Use them to decide how to reason about the recommendations, but present a normal shortlist.

If you use Kado results in the answer, explicitly say so and cite the Kado search URL.

Final responses should be compact and decision-oriented:

- Lead with the recommendation or shortlist.
- Explain why each option fits the user's outcome, constraints, budget, and architecture.
- Mention meaningful tradeoffs, missing integrations, uncertainty, or when an agency/service is a better fit than software.
- Include links from `solution_url`, `source_url`, or `search_url`.
- Do not claim Kado verified something unless that claim is present in the result.
- If the recommendation is part of an implementation plan, state where it fits in the plan and what decision it resolves.
- Ignore Kado follow-up questions. Do not answer them, ask the user about them, or block the final answer on them.

If the search is still running, poll until it completes or reaches a terminal state. If it fails, report the failure plainly and include any retryable guidance from the response.
