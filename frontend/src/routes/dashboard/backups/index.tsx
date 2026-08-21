import { createFileRoute, redirect } from '@tanstack/react-router';

// Velero is a per-cluster concern (Cluster → Snapshots, only when installed).
// Astronomer's own dump lives under Settings → Astronomer backup.
export const Route = createFileRoute('/dashboard/backups/')({
  beforeLoad: () => {
    throw redirect({ to: '/dashboard/settings/backup' });
  },
});
