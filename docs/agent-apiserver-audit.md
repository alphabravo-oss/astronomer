# Agent kube-apiserver audit forwarding

The agent can tail the kube-apiserver audit log and forward `audit.k8s.io`
events to the management plane's ingest endpoint
(`POST /api/v1/clusters/{cluster_id}/apiserver-audit/`).

This feature is **opt-in and disabled by default**. Enabling it requires a
cluster-admin prerequisite (below) because the kube-apiserver does not emit an
audit log unless explicitly configured to.

## Delivery path

The tail + batch + checkpoint core is implemented and tested
(`internal/agent/apiserver_audit.go`). Batches are delivered using the path
selected by `ASTRONOMER_AUDIT_DELIVERY`:

- **`tunnel`** (default, recommended): forward each batch over the existing
  authenticated agent WS tunnel as a `protocol.MsgApiserverAudit` frame
  (`internal/agent/tunnel.go`). The server attributes events to the session's
  cluster ID, so the agent needs **no second credential** — audit works
  out-of-the-box once the tailer is enabled. This is also the fallback used for
  any unrecognized value.
- **`http`**: direct outbound HTTPS POST to the ingest endpoint
  (`POST /api/v1/clusters/{cluster_id}/apiserver-audit/`), authenticating with a
  narrowly-scoped `clusters:write` API token the server delivers to the agent in
  the tunnel `CONNECT_ACK`. Use this when you want audit events to take a
  different path than the proxy tunnel (e.g. so they don't share its
  backpressure). If no ingest token has been delivered, this falls back to the
  tunnel path.
- **`stub`**: a no-op sender that batches and checkpoints but **drops** events
  (logging the batch size). For local development / wiring tests only.

The ingest route is mounted behind `requireAuth` (a JWT session or a
user/service API token), and additionally requires the `clusters:write` scope
and the `cluster:update` RBAC permission
(`internal/server/routes_security.go`). The default `tunnel` path satisfies
this implicitly via the tunnel session, which is why it needs no extra token.

## Enabling

Set on the agent (env vars are prefixed `ASTRONOMER_`):

| Env var                            | Default | Meaning                                            |
| ---------------------------------- | ------- | -------------------------------------------------- |
| `ASTRONOMER_AUDIT_ENABLED`         | `false` | Enable the tailer.                                 |
| `ASTRONOMER_AUDIT_LOG_PATH`        | (empty) | Path to the apiserver JSON audit log (required).   |
| `ASTRONOMER_AUDIT_CHECKPOINT_PATH` | (auto)  | Offset file; defaults to `<log_path>.checkpoint`.  |
| `ASTRONOMER_AUDIT_BATCH_SIZE`      | `100`   | Max events per forwarded batch.                    |
| `ASTRONOMER_AUDIT_POLL_INTERVAL`   | `10`    | Seconds between tail polls.                         |
| `ASTRONOMER_AUDIT_DELIVERY`        | `tunnel`| Delivery path: `tunnel` (default/recommended), `http`, or `stub`. See above. |

The checkpoint is a persisted byte offset, written atomically after each
accepted batch, so events are not re-forwarded on agent restart. The
checkpoint path should be on a persistent volume (e.g. the same hostPath as the
log) so it survives pod rescheduling.

## Cluster-admin prerequisite

The kube-apiserver must be started with an audit policy and a JSON log path,
and that log path must be mounted into the agent pod.

1. Provide an audit policy file (minimal example):

   ```yaml
   # /etc/kubernetes/audit-policy.yaml
   apiVersion: audit.k8s.io/v1
   kind: Policy
   rules:
     - level: Metadata
   ```

2. Add to the kube-apiserver flags:

   ```
   --audit-policy-file=/etc/kubernetes/audit-policy.yaml
   --audit-log-path=/var/log/kubernetes/audit/audit.log
   --audit-log-format=json
   ```

   The forwarder expects `--audit-log-format=json`: one `audit.k8s.io` Event
   JSON object per line.

3. Mount the log directory into the agent pod via a `hostPath` volume and point
   `ASTRONOMER_AUDIT_LOG_PATH` at the file:

   ```yaml
   spec:
     containers:
       - name: agent
         env:
           - name: ASTRONOMER_AUDIT_ENABLED
             value: "true"
           - name: ASTRONOMER_AUDIT_LOG_PATH
             value: /var/log/kubernetes/audit/audit.log
         volumeMounts:
           - name: apiserver-audit
             mountPath: /var/log/kubernetes/audit
             readOnly: true
     volumes:
       - name: apiserver-audit
         hostPath:
           path: /var/log/kubernetes/audit
           type: Directory
   ```

On managed control planes (EKS/GKE/AKS) the apiserver audit log is not exposed
on a node hostPath; this hostPath approach applies to self-managed /
kubeadm-style clusters where the apiserver runs as a static pod on a reachable
node.
