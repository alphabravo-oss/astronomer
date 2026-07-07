/**
 * Root 404 boundary (F-04). Rendered for any unmatched route outside the
 * dashboard segment. Branded, with a link back into the app.
 */

import { Compass } from 'lucide-react';
import { StatePanel } from '@/components/ui/empty-state';

export default function NotFound() {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center px-6">
      <StatePanel
        icon={Compass}
        tone="info"
        title="404 — Page not found"
        description="The page you're looking for doesn't exist or has moved."
        actionLabel="Back to dashboard"
        actionHref="/dashboard"
      />
    </div>
  );
}
