// Control-plane (etcd) DR snapshots — a distinct capability from the Velero
// workload snapshots on ../snapshots. Its module owns the etcd-specific query
// lifecycle, list surface, and guided restore runbook; this route only mounts
// that dedicated capability under its own URL and sidebar entry.
import { createFileRoute } from "@tanstack/react-router";
import { ClusterControlPlaneSnapshotsPage } from "@/components/clusters/control-plane-snapshots-page";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/control-plane-snapshots/",
)({
  component: ClusterControlPlaneSnapshotsPage,
});
