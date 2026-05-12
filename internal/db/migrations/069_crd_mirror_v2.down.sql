-- Migration 069 — CRD-mirror v2 (down).
--
-- The CASCADE on clusters(id) already drops dependent rows when a cluster
-- is decommissioned; the DROP TABLE here is the schema-level inverse of
-- the .up.sql. Order is irrelevant — there are no inter-mirror FKs.

DROP TABLE IF EXISTS mirrored_limit_ranges;
DROP TABLE IF EXISTS mirrored_resource_quotas;
DROP TABLE IF EXISTS mirrored_network_policies;
DROP TABLE IF EXISTS mirrored_gateway_classes;
DROP TABLE IF EXISTS mirrored_ingress_classes;
