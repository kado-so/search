# Query Guide

Read this guide when user intent or constraints need synthesis before invoking
`kado search`.

Describe the problem, desired outcome, and only the context that materially
changes the answer. Do not prematurely narrow the search to a guessed solution
category unless the user explicitly chose one.

Keep most queries to 2–4 sentences or 50–100 words. The CLI accepts UTF-8
queries up to its documented bound, but brevity produces clearer comparisons.
Never include credentials, secrets, private keys, tokens, cookies, or private
customer data.

Use this shape:

```text
We need to solve [problem] and achieve [outcome]. Context: [relevant product,
team, or workflow]. Constraints: [known budget, timeline, stack, integration,
deployment, compliance, or must-not-have details]. Preferences: [real
preferences or exclusions only].
```

Consider a constraint only when known or decisive:

- budget, open-source or hosted preference, and build-vs-buy tolerance;
- company size, industry, geography, compliance, and risk;
- current stack, data sources, integrations, deployment, and authentication;
- team capacity, implementation timeline, and maintenance tolerance; and
- success criteria, failure modes, and exclusions.

For vague pain points, synthesize a concrete outcome:

- "Fed up with inbounds" becomes a request to qualify, prioritize, route, and
  respond to inbound leads, including CRM or source constraints when known.
- "Onboarding is too slow" becomes a request to shorten time-to-value and
  improve completion, including product type and known drop-off points.
- "Support is exploding" becomes a request to reduce repetitive tickets,
  improve routing, and preserve response quality.
- "Deploys keep breaking" becomes a request for safer releases, including the
  stack and known failure modes.
- "We need SOC 2 help" becomes a request for the target audit outcome,
  readiness stage, timeline, and desired level of hands-on support.

If several distinct constraint profiles matter, run separate bounded searches
and compare them. If a missing constraint would materially change the decision,
ask one concise user question before searching. Otherwise state a reasonable
assumption and proceed. Handle a question returned by Kado through the
[CLI Lifecycle Guide](cli-guide.md), not an improvised API flow.
