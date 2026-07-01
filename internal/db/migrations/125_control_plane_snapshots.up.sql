-- Control-plane (etcd) snapshot registry for DISASTER RECOVERY of the
-- Kubernetes control plane itself — distinct from migration 052's Velero
-- workload snapshots.
--
-- Scope: SELF-MANAGED distributions only (k3s / RKE2 / kubeadm). Those
-- run their own etcd on control-plane nodes we can reach through the
-- tunnel and drive a one-shot snapshot Job against. Managed control
-- planes (EKS / GKE / AKS) hide etcd behind the cloud provider — there
-- is nothing to snapshot from a running cluster — so the handler refuses
-- a trigger for them and no row is ever written here for a managed
-- distribution.
--
-- Lifecycle (status column):
--   pending    — row created; snapshot Job not yet applied.
--   running    — Job applied to the member cluster; etcd snapshot in flight.
--   succeeded  — Job reported the snapshot was written (size_bytes stamped).
--   failed     — Job could not be applied / snapshot errored (error stamped).
--
-- location tells the operator WHERE the snapshot bytes landed:
--   local — on the control-plane node's snapshot dir (k3s default:
--           /var/lib/rancher/k3s/server/db/snapshots).
--   s3    — pushed to an object store by the distro's native s3 uploader.
--
-- Restore is deliberately NOT modeled here: restoring an etcd snapshot is
-- an OFFLINE node operation (stop the server, `k3s server
-- --cluster-reset --cluster-reset-restore-path=...`, restart) that cannot
-- be safely automated from a running cluster. The handler returns a
-- runbook instead of a Restore row.

CREATE TABLE control_plane_snapshots (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    -- Snapshot name; also the name the distro's snapshot command records
    -- (k3s: `--name <name>`). RFC-1123-ish, validated at the handler edge.
    name            VARCHAR(253) NOT NULL,
    -- pending | running | succeeded | failed
    status          VARCHAR(16) NOT NULL DEFAULT 'pending',
    -- local | s3
    location        VARCHAR(16) NOT NULL DEFAULT 'local',
    -- Size reported by the snapshot Job on success. NULL until then.
    size_bytes      BIGINT,
    -- Operator who triggered the snapshot (NULL for worker-scheduled rows
    -- or when the actor is a service token).
    requested_by_id UUID REFERENCES users(id) ON DELETE SET NULL,
    -- Failure detail surfaced to the operator; empty on the happy path.
    error           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ
);

CREATE INDEX idx_control_plane_snapshots_cluster
    ON control_plane_snapshots (cluster_id, created_at DESC);
CREATE INDEX idx_control_plane_snapshots_status
    ON control_plane_snapshots (status) WHERE status IN ('pending', 'running');
