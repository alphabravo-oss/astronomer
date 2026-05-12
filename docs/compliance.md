# Compliance baselines

Sprint 17 / migration 064 introduces the four-preset compliance baselines
feature. An operator opens **Settings → Compliance → Baselines** and
applies one of:

| Slug              | Standard           | Audit retention | PSS profile | TOTP | SMTP |
| ----------------- | ------------------ | --------------- | ----------- | ---- | ---- |
| `pci_dss_4_0`     | PCI-DSS 4.0        | 365 days        | restricted  | yes  | yes  |
| `hipaa`           | HIPAA              | 2190 days (6 y) | restricted  | yes  | no   |
| `fedramp_moderate`| FedRAMP Moderate   | 1095 days (3 y) | restricted  | yes  | yes  |
| `soc2`            | SOC 2 (Type II)    | 365 days        | baseline    | no   | yes  |

The canonical spec for each baseline lives in
`internal/compliance/baselines.go`. The DB rows seeded by migration 064
carry only the slug + display fields; the GET handler joins them with
the in-code `Registry()` to surface the full spec.

## What "apply" does

Inside a single Postgres transaction:

1. **Audit-retention downgrade guard** — refuses if the baseline's
   `audit_retention_days` is *lower* than the current value.
2. **Snapshot** — reads the current value of every field the spec
   touches, encodes it as JSON into `compliance_baseline_applications.previous_state`.
3. **Write** — upserts platform_settings, quota_plans, and
   maintenance/alert templates per the spec.
4. **Record** — inserts the application row.

A partial apply is impossible because every write is inside the same
transaction. If any single write fails, the entire apply rolls back.

## Revert

`POST /admin/compliance-baseline-applications/{id}/revert/` decodes the
captured `previous_state` and re-writes those fields. Refuses (HTTP 409)
if a newer applied row exists — v1 policy is "revert from latest
backwards." Operators who really need to revert an older application
must first revert the newer ones, or edit the row in psql.

## Required webhooks — deferred enforcement (v2)

The `required_webhooks` field on a baseline is **recorded only** in v1.
It surfaces in the diff and in the audit detail, and the UI nudges
the operator if a required webhook is missing. **Deletion of a
required webhook is NOT yet blocked at the API layer.** Tracking issue:

- v2: webhook handler should consult the active baseline and refuse
  to delete a subscription whose name is in `required_webhooks`.

The same caveat applies to `required_smtp` — the flag is recorded
(operators see a warning if SMTP is disabled while a baseline that
requires it is active), but the SMTP delete handler doesn't yet block.

## Read-audit policies (sprint 063 dependency)

The `read_audit_policies` field is currently a no-op at apply time.
Sprint 063 (`read_audit_policies` table) hasn't merged in the current
worktree; when it does, the engine's `writeSpec` should add a clause
that toggles `enabled=true` on the named rows. The current behaviour
logs a warn ("read_audit_policies field present but engine doesn't
yet wire to sprint-063 table; skipping") and continues with the rest
of the apply.

## Quota plan safety

Apply **never overwrites** an existing quota plan named `default`. The
operator's `default` plan is part of their day-to-day tenant on-boarding
and silently rewriting it would be a foot-gun. Baselines that ship
quota plans give them distinct names (`pci-prod`, etc.) that operators
can attach to specific projects.

## Audit + metrics

- Audit events: `compliance.baseline.viewed`, `compliance.baseline.applied`,
  `compliance.baseline.reverted`. Apply records the previous active slug
  in the detail so auditors can trace the chain.
- Metric: `astronomer_compliance_baseline_active{slug}` — 1 for the
  currently-active slug, 0 for the others.

## Operator UI

`/dashboard/settings/compliance/baselines` — four preset cards with a
"View diff" drawer and Apply / Revert actions. Active card is badged.
