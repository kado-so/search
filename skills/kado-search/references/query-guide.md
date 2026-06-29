# Query Guide

Use this guide to turn user intent into a strong Kado search query.

Kado finds solutions to problems. The agent should describe the user's problem, desired outcome, and only the context or constraints that materially change the answer. Do not prematurely narrow the search to a guessed solution category unless the user explicitly asked for that category.

Prefer a compact problem statement over a short keyword query. Keep most queries to 2-4 sentences or about 50-100 words. Only go longer when the user gave specific requirements that would otherwise be lost.

Do not pack every possible dimension into the query. Include a constraint only when it is known, strongly implied, or likely to filter out bad results. Avoid filler placeholders such as unknown budget, unknown team size, generic compliance needs, or broad risk tolerance.

When the problem has multiple distinct constraint sets, run multiple smaller Kado searches in parallel instead of one large, multi-faceted query. Focus each query on a coherent constraint profile so the result set is easier to compare.

Good query shape:

```text
We need to solve [problem] and achieve [desired outcome]. Context: [brief company/user/product details]. Current setup: [only relevant stack/process/tools]. Constraints: [known must-haves, budget/timeline/integration/compliance only if relevant]. Preferences or exclusions: [real preferences only].
```

Include known constraints:

- Budget, including "free", "$0", open-source only, or no budget.
- Company size, maturity, industry, geography, and compliance needs.
- Existing stack, data sources, integrations, deployment environment, and auth requirements.
- Team capacity, implementation timeline, maintenance tolerance, and build-vs-buy preference.
- Success criteria, failure modes, risk tolerance, and must-not-have constraints.

These are prompts to consider, not a required checklist. If the user did not provide a detail and it is not essential to the search, leave it out.

For vague pain points, synthesize a concrete but concise problem statement:

- "Fed up with inbounds" -> "We receive too many low-quality or poorly routed inbound leads. We need a way to qualify, prioritize, route, and respond faster. Relevant context: CRM and lead sources if known."
- "Want better security" -> "We need to reduce security risk for our current stack and improve posture or compliance readiness. Relevant context: assets, current controls, and budget if known."
- "Onboarding is too slow" -> "Users take too long to reach activation. We need to shorten onboarding and improve completion. Relevant context: product type and known drop-off points."
- "Support queue is exploding" -> "Support volume is outpacing the team. We need to reduce repetitive tickets, improve routing, and maintain response quality. Relevant context: support channels and ticket volume if known."
- "Deploys keep breaking" -> "Deployments are unreliable and causing regressions or downtime. We need safer releases. Relevant context: stack, CI/CD, and known failure modes."

For implementation plans, search at the decision point:

- "Use a queue" -> describe the async work problem plus any known throughput, durability, retry, or ordering requirement.
- "Add auth" -> describe the identity problem plus relevant app architecture, customer, compliance, migration, or budget constraints.
- "Send transactional email" -> describe the messaging problem plus known volume, deliverability, compliance, template, or developer workflow needs.
- "Outsource compliance" -> describe the compliance outcome plus readiness stage, timeline, audit scope, current controls, or internal capacity if known.

Only include solution categories as hints when useful. For example, "software, agency, managed service, or open-source option are all acceptable" keeps Kado broad; "only self-hosted open-source tools" narrows Kado when that is a real constraint.

If critical constraints are missing, do not stall by default. Search with reasonable assumptions. Ignore Kado follow-up questions; do not answer them or ask the user about them.
