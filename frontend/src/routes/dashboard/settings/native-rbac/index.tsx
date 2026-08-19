import { createFileRoute, redirect } from '@tanstack/react-router';

// CRD grants live on cluster/project roles (RBAC → Create Role).
// This URL is kept so old bookmarks land on the role editor surface.
export const Route = createFileRoute('/dashboard/settings/native-rbac/')({
  beforeLoad: () => {
    throw redirect({ to: '/dashboard/rbac' });
  },
});
