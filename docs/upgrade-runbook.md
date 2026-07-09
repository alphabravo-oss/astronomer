# Astronomer upgrade + rollback runbook

This document is the operator-facing procedure for upgrading an Astronomer
install and rolling one back if needed. It is the counterpart to
[`management-plane-dr-runbook.md`](./management-plane-dr-runbook.md) (when
the database itself is corrupt) and
[`secret-rotation-runbook.md`](./secret-rotation-runbook.md) (when the keys
are compromised). Use this one when the install is healthy but you want to
move to a newer chart or roll back from a release that misbehaves.

It assumes the reader is comfortable with `kubectl`, `helm`, and the
shape of the chart (`deploy/chart/`).

---

## TL;DR

```bash
# 1) Capture the live values FIRST so dry-run / upgrade preserve operator pins.
helm get values astronomer -n astronomer > /tmp/astronomer-values.yaml

# 2) Render-time preflight (catches missing/invalid values before anything is
# applied). The chart also runs a pre-upgrade preflight Job on the real cluster
# (see "Pre-upgrade checklist" and step 2 below).
helm upgrade astronomer ./deploy/chart --dry-run \
  -f deploy/chart/values.yaml \
  -f deploy/chart/values-production.yaml \
  -f /tmp/astronomer-values.yaml \
  --set image.server.tag=<NEW_SHA> \
  --set image.worker.tag=<NEW_SHA> \
  --set image.agent.tag=<NEW_SHA> \
  --set image.migrate.tag=<NEW_SHA> \
  --set frontend.image.tag=<NEW_SHA>

# 3) Diff, then upgrade
helm diff upgrade astronomer ./deploy/chart \
  -f deploy/chart/values.yaml \
  -f deploy/chart/values-production.yaml \
  -f /tmp/astronomer-values.yaml \
  --set image.server.tag=<NEW_SHA> \
  --set image.worker.tag=<NEW_SHA> \
  --set image.agent.tag=<NEW_SHA> \
  --set image.migrate.tag=<NEW_SHA> \
  --set frontend.image.tag=<NEW_SHA>

helm upgrade astronomer ./deploy/chart \
  -f deploy/chart/values.yaml \
  -f deploy/chart/values-production.yaml \
  -f /tmp/astronomer-values.yaml \
  --set image.server.tag=<NEW_SHA> \
  --set image.worker.tag=<NEW_SHA> \
  --set image.agent.tag=<NEW_SHA> \
  --set image.migrate.tag=<NEW_SHA> \
  --set frontend.image.tag=<NEW_SHA>

# 4) Verify (see "Post-upgrade verification")
curl -s $URL/health/                              # 200
curl -s $URL/readyz                               # 200
curl -s -H "Authorization: Bearer $TOKEN" $URL/api/v1/platform/health-summary/

# Rollback if needed
helm history astronomer -n astronomer
helm rollback astronomer <PREVIOUS_REV> -n astronomer
```

---

## When to use this runbook

Use this procedure when:

- Bumping image tags to a new SHA / version
- Rolling out a chart change (template edits, new values, new resources)
- Recovering from a failed `helm upgrade` (rollback section)

Do **not** use this runbook for:

- DB-level corruption → see
  [`management-plane-dr-runbook.md`](./management-plane-dr-runbook.md)
- Key rotation → see
  [`secret-rotation-runbook.md`](./secret-rotation-runbook.md)
- An emergency that needs a full plane redeploy → that's a re-install,
  not an upgrade

---

## Pre-upgrade checklist

Run these checks before invoking `helm upgrade`. The chart's preflight
Job will catch most of them at install time, but spotting failures here
saves the round trip.

### 1. Postgres + schema state

Production installs use **external** Postgres (`postgres.bundled.enabled=false`
in `values-production.yaml`). Do **not** assume a pod named
`astronomer-postgres-0` exists — that StatefulSet is dev/k3d only.

```bash
# Backups: confirm last good nightly pg_dump landed
kubectl -n astronomer logs -l app.kubernetes.io/component=management-backup \
  --tail=200 | grep -i 'completed\|error' | tail -10

# Schema state: should be clean (dirty=false).
# Prefer the server's DATABASE_URL (works for external Postgres and bundled).
DB_URL="$(kubectl -n astronomer get secret astronomer-postgres-dsn \
  -o jsonpath='{.data.dsn}' 2>/dev/null | base64 -d)"
# Fallback when the chart still manages a literal secret key name:
if [[ -z "$DB_URL" ]]; then
  DB_URL="$(kubectl -n astronomer get deploy astronomer-server \
    -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="DATABASE_URL")].value}')"
fi

# External Postgres (production): run psql from a throwaway pod that can
# reach the managed DB, OR use your provider's SQL console.
kubectl -n astronomer run psql-check --rm -it --restart=Never \
  --image=postgres:16-alpine -- \
  psql "$DB_URL" -c 'SELECT version, dirty FROM schema_migrations;'

# Bundled Postgres only (dev / k3d — skip this on production installs):
# kubectl -n astronomer exec astronomer-postgres-0 -- \
#   psql -U astronomer -d astronomer -c \
#   'SELECT version, dirty FROM schema_migrations;'
```

If `dirty=true`, **stop**. A previous migration crashed midway and the
schema is in an indeterminate state. Recover with:

```bash
# Inspect the offending migration's down/up SQL
ls internal/db/migrations/<NN>_*.{up,down}.sql

# Decide: roll forward (re-run the up) or roll back (run the down)
# Then mark clean (DATABASE_URL from the same secret/env as above):
kubectl -n astronomer run migrate --rm -it \
  --image=astronomer-go-migrate:<TAG> --restart=Never -- \
  migrate -database "$DB_URL" -path /migrations force <NN-1>
```

### 2. In-flight tasks

There is no shell-side asynq CLI in the worker image. Use the real signals:

```bash
# Prometheus metrics on the worker (port 9090). >100 pending is a yellow
# flag — the upgrade will work but in-flight visibility dips mid-roll.
kubectl -n astronomer port-forward deploy/astronomer-worker 9090:9090 &
curl -s http://127.0.0.1:9090/metrics | grep '^astronomer_worker_queue_depth'
# Look at state="pending" / state="active" / state="retry".

# Or scrape the support bundle (includes asynq-queues.json):
curl -sH "Authorization: Bearer $ADMIN_TOKEN" $URL/api/v1/support-bundle/ \
  -o /tmp/bundle.zip
unzip -p /tmp/bundle.zip asynq-queues.json | jq '.'
```

See [`metrics-v1.md`](./metrics-v1.md) for the full
`astronomer_worker_queue_depth{...,state=...}` series.

### 3. Agent fleet versions

```bash
# When server bumps a major version, every agent must be re-rolled too
# (the wire protocol may have changed). Confirm current agent versions
# against the same Postgres the management plane uses (see §1 for DB_URL).
kubectl -n astronomer run psql-agents --rm -it --restart=Never \
  --image=postgres:16-alpine -- \
  psql "$DB_URL" -c \
  "SELECT cluster_id, agent_version, last_ping FROM agent_connections WHERE status='connected';"

# Bundled Postgres only (dev / k3d):
# kubectl -n astronomer exec astronomer-postgres-0 -- \
#   psql -U astronomer -d astronomer -c \
#   "SELECT cluster_id, agent_version, last_ping FROM agent_connections WHERE status='connected';"
```

### 4. Disk + node capacity

```bash
# Helm upgrade rolls deployments one pod at a time. Need ≥1 pod-worth
# of free CPU+RAM on each scheduling target.
kubectl top nodes
kubectl -n astronomer get pdb
```

### 5. Capture current values

```bash
# These are the values currently rendered; copy them so the upgrade
# preserves operator-set overrides.
helm get values astronomer -n astronomer > /tmp/astronomer-values.yaml

# Print which image tags are live:
kubectl -n astronomer get deploy -o jsonpath='{range .items[*]}{.metadata.name}={.spec.template.spec.containers[0].image}{"\n"}{end}'
```

---

## The upgrade itself

### Step 1 — preview the diff

`helm-diff` is not in core helm; install it once:

```bash
helm plugin install https://github.com/databus23/helm-diff
```

Then preview:

```bash
helm diff upgrade astronomer ./deploy/chart \
  -f deploy/chart/values.yaml \
  -f deploy/chart/values-production.yaml \
  -f /tmp/astronomer-values.yaml \
  --set image.server.tag=<NEW_SHA> \
  --set image.worker.tag=<NEW_SHA> \
  --set image.agent.tag=<NEW_SHA> \
  --set image.migrate.tag=<NEW_SHA> \
  --set frontend.image.tag=<NEW_SHA>
```

Read every change. Common surprises:

- **New required values** — production preflight may add gates (e.g.
  `tls.source` cannot be `none`, `postgres.external.dsn` must use TLS).
  The render fails with a clear list of what's missing.
- **CRD changes** — new chart versions sometimes add new CRDs as
  dependencies. Confirm the cluster has them or install separately.

### Step 2 — run the upgrade

```bash
helm upgrade astronomer ./deploy/chart \
  -f deploy/chart/values.yaml \
  -f deploy/chart/values-production.yaml \
  -f /tmp/astronomer-values.yaml \
  --set image.server.tag=<NEW_SHA> \
  --set image.worker.tag=<NEW_SHA> \
  --set image.agent.tag=<NEW_SHA> \
  --set image.migrate.tag=<NEW_SHA> \
  --set frontend.image.tag=<NEW_SHA> \
  --timeout 10m
```

What happens:

1. **Preflight Job** runs first (`pre-upgrade` hook, weight `-5`). This
   includes:
   - Gateway API CRDs present
   - cert-manager CRDs present (if `tls.source` needs them)
   - Postgres DSN enforces TLS (production only)
   - `schema_migrations` connectivity + dirty-flag check
   - Bundled-Postgres PVC absence (when externalised)
   - Any additional `tls.additionalTrustedCAs` Secret exists
2. **Migrate Job** runs next, applying any new schema migrations. Helm
   waits for completion before proceeding (init container in the server
   Deployment also runs migrate, but the Job is the canonical owner).
3. **Server / worker / frontend rolling restart**. PDBs enforce
   quorum-safe drains.
4. **Argo CD self-manage** picks up any chart values changes that affect
   it, sometime within its next sync window.

### Step 3 — verify

```bash
# All pods running, no CrashLoopBackOff
kubectl -n astronomer get pods

# Health endpoints
curl -s $URL/health/
curl -s $URL/readyz

# Authenticated API
TOKEN=$(curl -sX POST $URL/api/v1/auth/login/ -d "$LOGIN_JSON" | jq -r '.data.token')
curl -sH "Authorization: Bearer $TOKEN" $URL/api/v1/platform/health-summary/

# Confirm no DLQ growth from the upgrade
unzip -p <(curl -sH "Authorization: Bearer $TOKEN" $URL/api/v1/support-bundle/) asynq-queues.json | jq '.'

# Confirm agents reconnected after server pods rolled (same DB_URL as §1)
kubectl -n astronomer run psql-agents --rm -it --restart=Never \
  --image=postgres:16-alpine -- \
  psql "$DB_URL" -c \
  "SELECT cluster_id, agent_version, last_ping FROM agent_connections WHERE status='connected';"
```

Wait at least one heartbeat interval (default 30s) before declaring
victory — agents reconnecting on a stale tunnel re-register and bump
`last_ping`.

---

## Rollback

`helm rollback` is the right tool for most failed upgrades. It re-renders
the previous release revision and applies it. Three rules:

### Rule 1: schema migrations are best-effort on rollback

`helm rollback` does NOT auto-run a `migrate down`. If the new release
ran an `up` migration, that schema change is **still in place** after
the rollback. The old binary may or may not work against the new schema:

- **Additive migrations** (new columns, new tables) — old binary keeps
  working because it doesn't reference the new columns. Safe.
- **Renames / drops / non-null adds** — old binary likely breaks. Not
  safe; you need a `pg_restore` from the nightly dump
  (`management-plane-dr-runbook.md`).

Always test the rollback path in staging before relying on it in
production.

### Rule 2: rollback to the IMMEDIATE prior release

`helm rollback <N>` works best when N is the revision right before the
failed one. Jumping back multiple revisions in one shot occasionally
trips on intermediate schema state — do it one step at a time.

### Rule 3: agents survive a rollback if the tunnel protocol didn't change

The chart's agent image tag is part of the rollback. Agents restart with
their previous image; existing tunnel sessions reset and reconnect.

### The procedure

```bash
# Find the previous release revision
helm history astronomer -n astronomer

# Output:
# REVISION  UPDATED      STATUS      CHART          ...
# 14        ...          superseded  astronomer-0.1.0 ...   <- want this
# 15        ...          failed      astronomer-0.1.0 ...   <- the bad one

# Roll back
helm rollback astronomer 14 -n astronomer --timeout 10m

# Verify (same checks as the upgrade verification section)
kubectl -n astronomer get pods
curl -s $URL/health/
```

### When rollback ISN'T enough

If `schema_migrations.dirty=true` after the failed upgrade, or you
reached this section because the schema rolled forward but the binary
won't start against it:

1. Restore from the most recent nightly pg_dump per
   [`management-plane-dr-runbook.md`](./management-plane-dr-runbook.md).
2. Confirm the restored DB version matches the binary you want to run.
3. Re-run `helm rollback` (or `helm upgrade` to a different version).

This is **the only known case** where the upgrade runbook alone isn't
enough.

---

## Agent version skew matrix

The server and agent must speak a compatible wire protocol. We follow a
strict policy: **agents support at most N-1 server versions**.

| Server version | Agents supported |
|---------------:|:-----------------|
| `v0.1.0` (current) | `v0.1.0` |

When server adds a wire-protocol message (e.g. `MsgDecommission` in
2026-05-11), the agent must be re-rolled at the same time. The agent's
`HandleX` registrations are additive — old agents just ignore unknown
message types and log a warning — so a brief skew during a rollout is
tolerable but should be measured in minutes, not days.

To bump every connected agent in one shot:

```bash
# Re-roll every agent Deployment in every managed cluster:
kubectl --context <each-managed-cluster> -n astronomer \
  rollout restart deploy/astronomer-agent
```

---

## Worked example: bumping image tags only

The simplest upgrade path. No chart changes, no values changes — just
new image SHAs.

```bash
# Capture current state
helm history astronomer -n astronomer | head -5
kubectl -n astronomer get deploy -o jsonpath='{range .items[*]}{.metadata.name}={.spec.template.spec.containers[0].image}{"\n"}{end}'

# Bump
helm upgrade astronomer ./deploy/chart \
  --reuse-values \
  --set image.server.tag=$NEW_SHA \
  --set image.worker.tag=$NEW_SHA \
  --set image.agent.tag=$NEW_SHA \
  --set image.migrate.tag=$NEW_SHA \
  --set frontend.image.tag=$NEW_SHA \
  --timeout 10m

# Verify
kubectl -n astronomer rollout status deploy/astronomer-server --timeout=5m
kubectl -n astronomer rollout status deploy/astronomer-worker --timeout=5m
kubectl -n astronomer rollout status deploy/astronomer-frontend --timeout=5m
curl -s $URL/health/
```

---

## Worked example: rollback after a failed migration

The migrate Job failed mid-release with this error:

```
error: Dirty database version 38. Fix and force version.
```

1. Look at the offending migration:

   ```bash
   ls deploy/chart/../../internal/db/migrations/038_*.up.sql
   cat deploy/chart/../../internal/db/migrations/038_cluster_decommission.up.sql
   ```

2. Decide: roll forward or roll back?
   - **Forward**: figure out what failed (logs from the migrate Job),
     hand-apply the rest of the up SQL, then `migrate force 38`.
   - **Back**: run the down SQL by hand, then `migrate force 37`.

   For 038 specifically, the up creates `cluster_decommissions` and
   `audit_archive` plus an `ALTER TABLE clusters ADD COLUMN
   decommissioned_at`. If it failed mid-way, the down SQL is the path
   of least resistance.

3. Mark clean once schema state matches the chosen target:

   ```bash
   kubectl -n astronomer run migrate-fix --rm -it \
     --image=astronomer-go-migrate:<TAG> --restart=Never -- \
     migrate -database "$DATABASE_URL" -path /migrations force <N>
   ```

4. Now `helm rollback` can proceed cleanly to the previous good
   revision, OR re-run `helm upgrade` to retry the same release.

---

## What this runbook deliberately doesn't cover

- **Cross-major-version upgrades** (e.g. `v0.x → v1.0`). Those carry
  their own migration scripts and are version-specific. Document them as
  `docs/upgrade-vX.Y-to-vA.B.md` alongside this file when they ship.
- **Cluster rebuild** (rebuilding the management cluster from scratch
  and restoring state). That's the DR runbook, not the upgrade runbook.
- **Operator-initiated agent re-pairing** at scale. The agent registration
  token rotation procedure lives in `docs/secret-rotation-runbook.md`.

If you find yourself reaching for those, you're not upgrading — you're
recovering. Use the right runbook.
