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
     `astronomer-shell:dev` (built from `deploy/docker/Dockerfile.shell`)
     with `sleep 14400` as a placeholder
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

The shell defaults to **read-only**. A write-capable (or cluster-admin)
shell is a *deliberate, audited* opt-in: the Open request must explicitly
ask for it (`{"elevate": true}` in the POST body, or `{"mode":"write"}`)
**and** the caller must hold the matching astronomer RBAC verb. A normal
break-glass debug session — and any request that does not opt in — gets
`get/list/watch` only, even though opening the shell already required the
`clusters:update` route gate.

The operator's *granted* verbs against `clusters/{id}` translate to the
in-cluster Role's verbs as follows:

| Request                                | In-cluster verbs                       | Scope     |
| -------------------------------------- | -------------------------------------- | --------- |
| default (no elevation)                 | `get, list, watch`                     | all NS    |
| `elevate` + `clusters:update` RBAC     | + `create, update, patch`              | all NS    |
| `elevate` + `clusters:delete` RBAC     | + `delete`                             | all NS    |
| `elevate` + Superuser                  | `cluster-admin` (built-in ClusterRole) | all NS    |

Asking to elevate without the matching RBAC fails **closed** to read-only
(the session still opens, but the rendered ClusterRole carries only
`get/list/watch`). The granted verbs and whether the request was elevated
are recorded on the `kubectl.session.opened` audit row.

This is intentionally coarse for v1. The use case is operator break-glass
debugging across an entire managed cluster. Operators who need
**per-namespace fine-grained control** should fall back to the existing
`kubectl proxy` flow with their own kubeconfig
(`/api/v1/clusters/{id}/generate-kubeconfig/`).

A v2 follow-on can mirror per-namespace project memberships into namespaced
`RoleBindings` instead of the cluster-wide grant. The migration-065 schema
already accommodates this (the `sa_namespace` column is `kube-system` for
v1 but writable, and the manifest builder is parameterized).

## Lifecycle caps

| Cap                                | Default | Chart value                              |
| ---------------------------------- | ------- | ---------------------------------------- |
| Idle timeout (no input)            | 30 min  | `kubectlShell.idleTimeoutMinutes`        |
| Hard cap (since session opened)    | 4 hr    | `kubectlShell.sessionHardCapHours`       |
| Debug pod image                    | `astronomer-shell:dev` | `kubectlShell.image`           |
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

### Recording is reliable, not best-effort

Earlier builds dropped keystrokes silently when the recorder channel
(cap 64) filled under sustained input, and only recognized
`type:"stdin"|"input"` frames. Both let an attacker defeat the audit log.
Now:

- **Back-pressure, not drop.** The input recorder hook runs synchronously
  in the exec read loop *before* the keystroke is forwarded to the agent.
  When the drain channel is full it **blocks** (throttling the session)
  instead of dropping the row. The block is bounded by the WS lifetime —
  closing the socket cancels the recorder context and releases the read
  loop. A command therefore cannot execute without first being queued for
  the audit log.
- **Closed frame contract.** The recorder's `extractStdinBytes` mirrors
  the tunnel's `translateFromFrontend` exactly. Any frame the relay
  forwards to the agent as stdin — `stdin`/`input` envelopes *and* the
  raw-bytes fallback for unrecognized/non-JSON frames — is recorded. Only
  the genuine control frames (`resize`/`auth`/`end`/`close`), which never
  carry executed keystrokes, are ignored. An attacker can't wrap a command
  in an exotic frame `type` to slip it past the recorder.

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
