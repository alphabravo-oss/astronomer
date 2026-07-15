// Route files are the eslint-exempted surface for direct router imports.
import { createFileRoute } from '@tanstack/react-router';
import { ClusterSnapshotsPage } from '@/components/clusters/snapshots-page';

export const Route = createFileRoute('/dashboard/clusters/$id/snapshots/')({
  component: ClusterSnapshotsPage,
});
