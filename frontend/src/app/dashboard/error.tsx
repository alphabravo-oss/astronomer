'use client';

/**
 * Route-level error boundary for the dashboard segment (F-04). A render error
 * in any dashboard page is caught here instead of white-screening the whole
 * console — the sidebar/topbar in `dashboard/layout.tsx` stay mounted because
 * a segment `error.tsx` only replaces the segment's children.
 */

import { useEffect } from 'react';
import { Link } from '@/lib/link';
import { AlertTriangle, RotateCcw, LayoutDashboard } from 'lucide-react';
import { StatePanel } from '@/components/ui/empty-state';

export default function DashboardError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    // Surface to the console so it still reaches any error-reporting hook.
    console.error('Dashboard render error:', error);
  }, [error]);

  return (
    <div className="flex flex-col items-center">
      <StatePanel
        icon={AlertTriangle}
        tone="danger"
        title="Something went wrong"
        description={
          <>
            {error.message || 'An unexpected error occurred while rendering this page.'}
            {error.digest && (
              <span className="mt-1 block font-mono text-xs opacity-70">ref: {error.digest}</span>
            )}
          </>
        }
        role="alert"
        actionLabel="Try again"
        actionIcon={RotateCcw}
        onAction={reset}
      />
      <Link
        href="/dashboard"
        className="-mt-6 inline-flex h-9 items-center gap-2 rounded-lg border border-border px-4 text-sm font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
      >
        <LayoutDashboard className="h-4 w-4" />
        Back to dashboard
      </Link>
    </div>
  );
}
