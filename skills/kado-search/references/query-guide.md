# Query Guide

Use this guide to turn user intent into a strong Kado search query.

Kado finds solutions to problems. The agent should describe the user's problem, desired outcome, context, and constraints. Do not prematurely narrow the search to a guessed solution category unless the user explicitly asked for that category.

Prefer a complete problem statement over a short keyword query.

Good query shape:

```text
We need to solve [problem] and achieve [desired outcome]. Context: [company/user/team/product]. Current setup: [stack/process/tools]. Constraints: [budget, timeline, integrations, compliance, geography, team capacity]. Preferences or exclusions: [build-vs-buy, open-source, managed service, agency, self-hosted, must-not-have].
```

Include known constraints:

- Budget, including "free", "$0", open-source only, or no budget.
- Company size, maturity, industry, geography, and compliance needs.
- Existing stack, data sources, integrations, deployment environment, and auth requirements.
- Team capacity, implementation timeline, maintenance tolerance, and build-vs-buy preference.
- Success criteria, failure modes, risk tolerance, and must-not-have constraints.

For vague pain points, synthesize a concrete problem statement:

- "Fed up with inbounds" -> "We receive too many low-quality or poorly routed inbound leads. We need a way to qualify, prioritize, route, and respond faster. Context: [sales motion, CRM, lead sources, volume, budget]."
- "Want better security" -> "We need to reduce security risk for [stack/company type] and improve [compliance/posture/visibility]. Context: [assets, current controls, team capacity, budget]."
- "Onboarding is too slow" -> "Users take too long to reach activation. We need to shorten onboarding and improve completion. Context: [product, users, current flow, drop-off points, stack, budget]."
- "Support queue is exploding" -> "Support volume is outpacing the team. We need to reduce repetitive tickets, improve routing, and maintain response quality. Context: [channels, ticket volume, KB, team size, budget]."
- "Deploys keep breaking" -> "Deployments are unreliable and causing regressions or downtime. We need safer releases. Context: [stack, CI/CD, failure modes, team size, reliability needs]."

For implementation plans, search at the decision point:

- "Use a queue" -> describe the async work problem, runtime, throughput, durability, retry, ordering, and ops constraints.
- "Add auth" -> describe the identity problem, app architecture, customer requirements, compliance needs, migration constraints, and budget.
- "Send transactional email" -> describe the messaging problem, volume, deliverability needs, compliance constraints, templates, and developer workflow.
- "Outsource compliance" -> describe the compliance outcome, readiness stage, timeline, audit scope, current controls, and internal capacity.

Only include solution categories as hints when useful. For example, "software, agency, managed service, or open-source option are all acceptable" keeps Kado broad; "only self-hosted open-source tools" narrows Kado when that is a real constraint.

If critical constraints are missing, do not stall by default. Search with reasonable assumptions, then refine or ask follow-up questions if Kado returns blockers that materially affect the recommendation.
