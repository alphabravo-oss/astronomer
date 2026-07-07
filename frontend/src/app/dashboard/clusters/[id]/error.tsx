'use client';

/**
 * Error boundary scoped to a single cluster context (F-04). A failure in one
 * cluster tab is caught here, so the surrounding dashboard chrome — and the
 * cluster nav — stay intact instead of the whole console going blank.
 */

import { useEffect } from 'react';
import { Link } from '@/lib/link';
import { AlertTriangle, RotateCcw, Server } from 'lucide-react';
import { StatePanel } from '@/components/ui/empty-state';

export default function ClusterError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    console.error('Cluster page render error:', error);
  }, [error]);

  return (
    <div className="flex flex-col items-center">
      <StatePanel
        icon={AlertTriangle}
        tone="danger"
        title="This cluster view failed to load"
        description={
          <>
            {error.message || 'An unexpected error occurred while rendering this cluster page.'}
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
        href="/dashboard/clusters"
        className="-mt-6 inline-flex h-9 items-center gap-2 rounded-lg border border-border px-4 text-sm font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
      >
        <Server className="h-4 w-4" />
        All clusters
      </Link>
    </div>
  );
}
