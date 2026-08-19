import { createFileRoute, redirect } from '@tanstack/react-router';

export const Route = createFileRoute('/dashboard/backups/restores/$restoreId/')({
  beforeLoad: () => {
    throw redirect({ to: '/dashboard/settings/backup' });
  },
});
