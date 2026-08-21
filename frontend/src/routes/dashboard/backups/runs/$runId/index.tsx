import { createFileRoute, redirect } from '@tanstack/react-router';

export const Route = createFileRoute('/dashboard/backups/runs/$runId/')({
  beforeLoad: () => {
    throw redirect({ to: '/dashboard/settings/backup' });
  },
});
