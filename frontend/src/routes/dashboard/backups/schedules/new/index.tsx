import { createFileRoute, redirect } from '@tanstack/react-router';

export const Route = createFileRoute('/dashboard/backups/schedules/new/')({
  beforeLoad: () => {
    throw redirect({ to: '/dashboard/settings/backup' });
  },
});
