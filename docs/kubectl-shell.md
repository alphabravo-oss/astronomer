# In-browser kubectl shell

Sprint 17 / migration 065 adds an operator-facing "Open Shell" affordance on
the cluster-detail page. Clicking it spins up an ephemeral debug pod in the
target cluster and streams `kubectl exec -it` stdin/stdout into an xterm.js
terminal inside the browser. No kubeconfig juggling, no port-forward.

## How it works

1. Operator clicks **Open Shell** on `/dashboard/clusters/{id}/`.
2. Frontend POSTs `/api/v1/clusters/{cluster_id}/shell/sessions/`.
3. The backend:
   - Inserts a `kubectl_sessions` row (`status=starting`).
   - Creates a `ServiceAccount` + `ClusterRole` + `ClusterRoleBinding` in
     `kube-system`. Names are `astro-shell-<base32-uuid>` so they are
     DNS-1123 safe and unique across runs.
   - Creates a debug pod named `astro-shell-<base32-uuid>` running
     `bitnami/kubectl:1.31` with `sleep 14400` as a placeholder
     (`activeDeadlineSeconds=14400` so the pod auto-suicides at the hard cap).
   - Waits up to 60s for the pod to report `Ready=True`.
   - Flips the row to `status=active`.
4. Frontend opens a WebSocket to `/api/v1/ws/clusters/{cluster_id}/shell/sessions/{id}/`.
   The handler issues a 307 redirect onto the existing sprint-14
   `/api/v1/ws/exec/{cluster_id}/{ns}/{pod}/{container}/` relay, so the
   wire protocol between the browser and the cluster agent is unchanged.
5. On modal close (X, browser nav, idle / hard-cap reaper), the backend
   tears down Pod → Binding → Role → ServiceAccount and flips the row to
   `status=closed` / `expired`.

## RBAC mirroring (v1, coarse)

The operator's effective verbs against `clusters/{id}` translate to the
in-cluster Role's verbs as follows:

| Astronomer verb     | In-cluster verbs                       | Scope     |
| ------------------- | -------------------------------------- | --------- |
| `clusters:read`     | `get, list, watch`                     | all NS    |
| `clusters:update`   | + `create, update, patch`              | all NS    |
| `clusters:delete`   | + `delete`                             | all NS    |
| Superuser           | `cluster-admin` (built-in ClusterRole) | all NS    |

This is intentionally coarse for v1. The use case is operator break-glass
debugging across an entire managed cluster — the same persona who can
`kubectl edit` resources via the existing K8s proxy can drive them
through the in-browser shell. Operators who need **per-namespace
fine-grained control** should fall back to the existing `kubectl proxy`
flow with their own kubeconfig (`/api/v1/clusters/{id}/generate-kubeconfig/`).

A v2 follow-on can mirror per-namespace project memberships into namespaced
`RoleBindings` instead of the cluster-wide grant. The migration-065 schema
already accommodates this (the `sa_namespace` column is `kube-system` for
v1 but writable, and the manifest builder is parameterized).

## Lifecycle caps

| Cap                                | Default | Chart value                              |
| ---------------------------------- | ------- | ---------------------------------------- |
| Idle timeout (no input)            | 30 min  | `kubectlShell.idleTimeoutMinutes`        |
| Hard cap (since session opened)    | 4 hr    | `kubectlShell.sessionHardCapHours`       |
| Debug pod image                    | `bitnami/kubectl:1.31` | `kubectlShell.image`           |
| Feature toggle                     | OFF     | `kubectlShell.enabled`                   |

When disabled (the default), the HTTP routes are not registered and the
frontend Shell tab hides itself.

## Audit log: commands, NOT output

The `kubectl_session_commands` table records one row per inbound line
ended by `\r` or `\n`. The row carries the literal command line (capped
at 1 KB; longer lines get `...<truncated>` appended).

**Output bytes are never recorded.** A legitimate operator session might
include `kubectl get secret -o yaml`, whose output contains base64-encoded
secret material; storing the response stream would turn an audit table
into a secrets leak. The compliance story we want is "who ran what,
against which cluster, when" — that question is answerable from input
alone.

## Endpoints

### Cluster-scoped (clusters:update)

| Method | Path                                                              | Notes                              |
| ------ | ----------------------------------------------------------------- | ---------------------------------- |
| POST   | `/api/v1/clusters/{cluster_id}/shell/sessions/`                   | Open a new session                 |
| GET    | `/api/v1/clusters/{cluster_id}/shell/sessions/`                   | List active sessions in cluster    |
| GET    | `/api/v1/clusters/{cluster_id}/shell/sessions/{id}/`              | Session info + command count       |
| POST   | `/api/v1/clusters/{cluster_id}/shell/sessions/{id}/close/`        | Close (idempotent)                 |
| GET    | `/api/v1/clusters/{cluster_id}/shell/sessions/{id}/commands/`     | Recorded input lines               |
| GET    | `/api/v1/ws/clusters/{cluster_id}/shell/sessions/{id}/`           | WS handshake (redirects to /ws/exec) |

### Admin (superuser)

| Method | Path                                                | Notes                       |
| ------ | --------------------------------------------------- | --------------------------- |
| GET    | `/api/v1/admin/shell-sessions/`                     | Fleet-wide active sessions  |
| GET    | `/api/v1/admin/shell-sessions/{id}/commands/`       | Any session's audit log     |

## Worker reaper

`kubectl:session_reap` runs every 60 seconds. On each fire:

1. **Idle sweep** — rows where `last_input_at + idle_timeout < now()`
   flip to `status=expired` and the pod + RBAC tuple is torn down.
2. **Hard-cap sweep** — rows where `expires_at < now()` flip to
   `status=expired` (same teardown).
3. **Orphan pod sweep** — for every cluster with active sessions, list
   pods labelled `astronomer.io/component=kubectl-shell` and delete
   any whose name isn't in the active DB row set. Defends against a
   Close() crash that left a pod orphaned.

## Constraints

- **Tunnel-mediated only.** The cluster's apiserver is never exposed
  directly to the operator's browser. The existing tunnel exec channel
  (sprint 14) carries the WS frames.
- **No shell injection in manifests.** SA / Role / Binding / Pod names
  use `astro-shell-<base32-uuid>` derived from `crypto/rand`. No
  user-controlled string flows into a k8s object name.
- **Output bytes never recorded.** Only input lines ending in `\r` /
  `\n`, capped at 1 KB.
- **No clipboard / file transfer**, intentionally. v1 is a shell, not
  a remote desktop.
