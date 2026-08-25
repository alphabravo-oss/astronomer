# Test flake and quarantine policy

Astronomer treats a flaky test as a product or test-infrastructure defect, not
as passing coverage. The blocking retry budget is zero, including for the
real-network Playwright `live` project. Service readiness is handled by explicit
health gates rather than by replaying an already-failed operator journey.

## Ownership and quarantine contract

`docs/test-quarantine.json` is the source of truth. Every entry must contain:

- a stable, whitespace-free quarantine `id`, plus the exact Playwright test
  identifier and tier;
- an accountable team/owner and tracked issue URL;
- the date quarantine started and an expiry no more than 14 days later;
- a specific failure signature and rationale;
- a retry allowance of zero or one.

Any `test.skip`, `test.fixme`, `it.skip`, or `describe.skip` in browser tests
must carry `QUARANTINE:<id>` on the same line and have a live registry
entry. Expired, orphaned, over-budget, or unowned entries fail the repository
check. Deleting a flaky test is not a remediation.

The exact `test_id` format emitted by the report is
`frontend/tests/<path> › <describe path> › <test title> [<project>]`. Copy it
from the failed weekly summary; do not invent or abbreviate it. The separate
slug-like `id` exists only so a short, durable marker can live beside the source
skip even when the test title contains spaces.

Quarantine is a temporary routing choice: the test moves to the scheduled
diagnostic run, retains its artifacts, and keeps an issue owner. Critical live
journeys cannot be quarantined for release qualification; they must pass with
no retry on the release candidate.

## Reporting

The weekly `test-flake-trend` workflow runs the desktop browser suite five times
with retries disabled and explicitly retains traces on failure.
`scripts/test-flake-report.mjs` groups attempts by exact test/project, reports
pass/fail/flaky/skip counts to the job summary, checks retry budgets and the
quarantine registry, and retains the raw JSON plus screenshots, video, traces,
and the Markdown summary for 90 days.

A test is flaky when the same identifier has both a passing and a failing
attempt, or Playwright reports `flaky`. Unexpected failures and unregistered
flakes fail the workflow. Owners review the weekly artifacts and link repeated
signatures to the same issue; two occurrences in seven days require either a
fix or a time-bounded quarantine entry.

Run the local policy gate with:

```bash
node scripts/test-flake-report.mjs --validate
```

Summarize a Playwright JSON report with:

```bash
node scripts/test-flake-report.mjs \
  --report test-artifacts/playwright.json \
  --output test-artifacts/flake-summary.md
```
