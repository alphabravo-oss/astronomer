# Agent kube-apiserver audit forwarding

The agent can tail the kube-apiserver audit log and forward `audit.k8s.io`
events to the management plane's ingest endpoint
(`POST /api/v1/clusters/{cluster_id}/apiserver-audit/`).

This feature is **opt-in and disabled by default**. Enabling it requires a
cluster-admin prerequisite (below) because the kube-apiserver does not emit an
audit log unless explicitly configured to.

## Status / known gap

The tail + batch + checkpoint core is implemented and tested
(`internal/agent/apiserver_audit.go`). The forwarder currently uses a **stub
sender** that batches and checkpoints but does not deliver, because the agent
has no usable outbound HTTP auth path to the ingest endpoint yet:

- The ingest route is mounted behind `requireAuth` (a JWT session **or** a
  user/service API token), and additionally requires the `clusters:write`
  scope and the `cluster:update` RBAC permission
  (`internal/server/routes_security.go`).
- The agent holds only its WebSocket **registration/durable token**, used for
  the tunnel `CONNECT` handshake (`internal/agent/tunnel.go`). That token is
  not an API token recognised by `RequireAuthWithQueries`, and the agent has
  no JWT.

Closing the gap requires either (a) issuing the agent a scoped API token at
registration, or (b) accepting batched audit events over the existing
WebSocket tunnel (a new `protocol.Message` type handled server-side and
written through the same ingest store). Once that path exists, swap
`stubAuditSender` for a real sender in `cmd/agent/main.go`.

## Enabling

Set on the agent (env vars are prefixed `ASTRONOMER_`):

| Env var                            | Default | Meaning                                            |
| ---------------------------------- | ------- | -------------------------------------------------- |
| `ASTRONOMER_AUDIT_ENABLED`         | `false` | Enable the tailer.                                 |
| `ASTRONOMER_AUDIT_LOG_PATH`        | (empty) | Path to the apiserver JSON audit log (required).   |
| `ASTRONOMER_AUDIT_CHECKPOINT_PATH` | (auto)  | Offset file; defaults to `<log_path>.checkpoint`.  |
| `ASTRONOMER_AUDIT_BATCH_SIZE`      | `100`   | Max events per forwarded batch.                    |
| `ASTRONOMER_AUDIT_POLL_INTERVAL`   | `10`    | Seconds between tail polls.                         |

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
