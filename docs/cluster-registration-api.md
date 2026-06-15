# Cluster registration API (sprint 22 / migration 078)

Astronomer's cluster registration uses a Rancher-style three-page
wizard plus a server-authoritative phase state machine. This document
covers the API surface so automation can drive it without going
through the dashboard.

## Phases

```
created -> awaiting_agent -> connected -> provisioning -> ready
                      |                        |
                      +------ failed <---------+
```

The current phase lives on `clusters.registration_phase`. A row is
created with phase=`created` and reaches a terminal phase
(`ready` or `failed`) via the events documented below.
The internal `provisioning` phase means Astronomer is applying the
operator-selected baseline to an existing cluster. It is not
infrastructure provisioning, and no cloud/node/RKE-style cluster
creation flow is in scope.

The `install_baseline` column on the clusters row is three-valued:
- `NULL` - operator hasn't reached step 2 of the wizard yet.
- `FALSE` - operator opted out of the platform-baseline install.
- `TRUE` - operator opted in.

## Endpoints

All routes mount under `/api/v1/clusters/{id}/registration/`. RBAC
gates writes on `clusters:update` and reads on `clusters:read`. The
cancel route additionally requires the caller's user row to have
`is_superuser = true`.

| Method | Path                              | Purpose                                                       |
|--------|-----------------------------------|---------------------------------------------------------------|
| GET    | `/status/`                        | Phase + step timeline                                         |
| GET    | `/events/`                        | Server-Sent Events filtered by cluster_id                     |
| PUT    | `/options/`                       | `{"install_baseline": bool}` - records operator's step-1 pick  |
| POST   | `/confirm/`                       | Advances `created` to `awaiting_agent`                         |
| POST   | `/retry/{step_id}/`               | Re-queues the apply task for a failed step                    |
| POST   | `/cancel/`                        | Superuser-only abort                                          |

### Status response shape

```jsonc
{
  "data": {
    "cluster_id": "uuid",
    "phase": "connected",
    "install_baseline": true,
    "started_at": "2026-05-12T18:00:00Z",
    "completed_at": null,
    "steps": [
      {
        "id": "uuid",
        "step_name": "cluster_created",
        "label": "Cluster created",
        "status": "success",
        "progress_pct": 100,
        "detail": { "name": "my-cluster" },
        "started_at": "...",
        "completed_at": "...",
        "error_message": "",
        "step_order": 1,
        "created_at": "..."
      }
    ]
  }
}
```

### SSE event types

Both events carry `cluster_id` so the consumer can multiplex multiple
streams:

| Event type                       | Trigger                                            |
|----------------------------------|----------------------------------------------------|
| `cluster.registration.step`      | A step row was inserted or updated                 |
| `cluster.registration.phase`     | `clusters.registration_phase` column transitioned  |

## Driving the flow from automation

The legacy `POST /clusters/` path is unchanged. Callers that already
POST a cluster and don't care about the wizard see the row land in
phase `created` and stop. Nothing else changes.

For full wizard parity:

```bash
# 1. Create the cluster row.
cluster_id=$(curl -fsSL -X POST .../clusters/ -d '{"name": "..."}' | jq -r .data.id)

# 2. Record options. install_baseline is required; pass false if the
#    caller doesn't want the platform baseline.
curl -fsSL -X PUT .../clusters/$cluster_id/registration/options/ \
    -d '{"install_baseline": false}'

# 3. Fetch the agent install manifest and apply it on the target.
curl -fsSL .../clusters/$cluster_id/manifest/ | kubectl apply -f -

# 4. Confirm - moves to awaiting_agent. The first heartbeat from the
#    agent advances it to connected.
curl -fsSL -X POST .../clusters/$cluster_id/registration/confirm/

# 5. Poll status or subscribe to the SSE stream.
curl -fsSL -N .../clusters/$cluster_id/registration/events/
```

## Agent privilege profile

The agent manifest supports `viewer`, `operator`, `namespace-viewer`,
`namespace-operator`, `custom`, and `admin` RBAC profiles. The selected profile
is stored on the cluster as annotation
`astronomer.io/agent-privilege-profile` before the manifest is rendered.
Missing or invalid values default to `admin` for compatibility.

Declarative operators can set `Cluster.spec.agent.privilegeProfile` directly or
set `Cluster.spec.agent.profileRef` to a same-namespace `AgentProfile`.
Referenced `AgentProfile` objects can also project `install.image`,
`install.serviceAccountName`, and `install.podLabels` into the rendered
manifest.
API automation can set the reserved annotation when creating or updating
the cluster row, then fetch `/clusters/{id}/manifest/`.

See [agent-privilege-profiles.md](agent-privilege-profiles.md) for the
feature/profile matrix.

## Backwards compatibility

Migration 078 backfills `registration_phase = 'ready'` for every
clusters row older than one minute. Existing operators with already-
registered clusters see no adoption prompt. `/dashboard/clusters/{id}/adoption`
is the canonical registration and baseline-application timeline; the historical
`/dashboard/clusters/{id}/provisioning` redirect alias has been removed so the
UI does not imply that Astronomer creates infrastructure.

## Deferred / known caveats

- **Signed manifest URL**: the "Quick install" one-liner currently
  embeds the registration token via the `cluster_registration_tokens`
  row the GET /manifest/ handler mints. A separate short-TTL signed
  URL for the manifest fetch is on the backlog; the workaround for
  ultra-cautious environments is `kubectl create -f` against a
  downloaded YAML.
- **Per-tool install steps**: the cluster_template:apply worker
  currently writes `template_applying` / `template_applied` /
  `template_failed` step rows. Per-tool `tool_installing:<slug>` rows
  are emitted opportunistically when the StepLabel helper sees the
  name; wiring the worker to write them on each EnsureInstalled call
  is straightforward but pending the tool-installer interface change.
- **Cluster-detail Adoption tab**: the wizard route at
  `/dashboard/clusters/register/{id}/progress` renders the same
  timeline the in-detail tab will show; reading status + steps from
  the same `/registration/status/` endpoint keeps the two views in
  sync. The tab itself is a follow-up.
