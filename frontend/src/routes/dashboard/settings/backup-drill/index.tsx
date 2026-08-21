import { createFileRoute, redirect } from '@tanstack/react-router';

// Restore-drill results now live on the Astronomer backup settings page.
export const Route = createFileRoute('/dashboard/settings/backup-drill/')({
  beforeLoad: () => {
    throw redirect({ to: '/dashboard/settings/backup' });
  },
});
