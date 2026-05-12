-- Down-migration for 071_service_mesh.
--
-- Drops only cluster_service_mesh. Do NOT drop helm_chart_tags here —
-- a sister sprint may rely on it; the up file uses CREATE TABLE IF
-- NOT EXISTS for the same reason. The service-mesh tag rows inserted
-- by the up file go with the chart_id CASCADE when their parent chart
-- is removed; we don't bother cleaning them up explicitly because the
-- tag itself ('service-mesh') is cheap leftover state and other
-- sprints may share the same scheme.

DROP TABLE IF EXISTS cluster_service_mesh;
