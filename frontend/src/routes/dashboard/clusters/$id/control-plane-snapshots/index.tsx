// Control-plane (etcd) DR snapshots — a distinct capability from the Velero
// workload snapshots on ../snapshots. The page component and its helpers live
// in the shared snapshots module (they share dialog/table primitives); this
// route just mounts it so etcd DR gets its own URL + sidebar entry. Route
// files must not import each other under autoCodeSplitting, hence the shared
// component module instead of a re-export from ../snapshots.
import { createFileRoute } from '@tanstack/react-router';
import { ClusterControlPlaneSnapshotsPage } from '@/components/clusters/snapshots-page';

export const Route = createFileRoute('/dashboard/clusters/$id/control-plane-snapshots/')({
  component: ClusterControlPlaneSnapshotsPage,
});
