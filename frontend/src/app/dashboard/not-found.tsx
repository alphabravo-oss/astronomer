/**
 * Dashboard 404 boundary (F-04). Keeps the dashboard chrome (sidebar/topbar
 * from `dashboard/layout.tsx`) mounted while telling the user the sub-route
 * doesn't exist.
 */

import { Compass } from 'lucide-react';
import { StatePanel } from '@/components/ui/empty-state';

export default function DashboardNotFound() {
  return (
    <StatePanel
      icon={Compass}
      tone="info"
      title="Page not found"
      description="This dashboard route doesn't exist. It may have moved or been removed."
      actionLabel="Back to dashboard"
      actionHref="/dashboard"
    />
  );
}
