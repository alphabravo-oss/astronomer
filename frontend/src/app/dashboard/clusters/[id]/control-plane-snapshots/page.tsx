// Control-plane (etcd) DR snapshots — a distinct capability from the Velero
// workload snapshots on ../snapshots. The page component and its helpers live
// in the snapshots module (they share dialog/table primitives); this route
// just re-exports it so etcd DR gets its own URL + sidebar entry.
export { ClusterControlPlaneSnapshotsPage as default } from '../snapshots/page';
