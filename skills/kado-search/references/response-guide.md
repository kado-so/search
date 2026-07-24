# Response Guide

Read this guide after `kado search` returns validated human, JSON, or JSONL
output.

Lead with a bounded recommendation or shortlist. For each relevant option:

- explain why it fits the user's outcome and known constraints;
- state meaningful tradeoffs, uncertainty, and missing evidence;
- retain useful solution, source, and canonical Search links from the result;
- distinguish software, open source, agencies, services, architecture, and
  build choices when that distinction affects the decision; and
- state where the choice fits when Search supports an implementation plan.

Do not expose internal ranking labels or infer claims that are absent from the
validated result. Say that Kado was used when its results materially inform the
answer.

Keep the response proportional to the request. Default to 3–5 well-supported
options rather than reproducing every page. Do not paste the canonical document
or JSONL stream unless the user requested machine output.

If Search needs clarification, fails, times out, or is canceled, follow
[CLI Lifecycle Guide](cli-guide.md). Report only the CLI's bounded public state
and safe next step.
