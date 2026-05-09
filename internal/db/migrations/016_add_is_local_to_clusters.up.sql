-- Track whether a cluster row represents the management ("local") cluster
-- the server itself runs in. Rancher-style: server boot ensures exactly one
-- such row exists and an embedded agent connects through the normal tunnel.
ALTER TABLE clusters
    ADD COLUMN is_local BOOLEAN NOT NULL DEFAULT false;

-- Partial unique index: only one row may have is_local = true. Multiple
-- server replicas (or restarts) racing to create the local row will conflict
-- here, which the EnsureLocalCluster query relies on for idempotency.
CREATE UNIQUE INDEX clusters_one_local ON clusters (is_local) WHERE is_local = true;
