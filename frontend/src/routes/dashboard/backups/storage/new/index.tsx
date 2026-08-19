import { createFileRoute, redirect } from '@tanstack/react-router';

export const Route = createFileRoute('/dashboard/backups/storage/new/')({
  beforeLoad: () => {
    throw redirect({ to: '/dashboard/settings/backup' });
  },
});
