// Route files are the eslint-exempted surface for direct router imports.
import { useEffect } from 'react';
import { createFileRoute, Outlet, type ErrorComponentProps } from '@tanstack/react-router';
import { Link } from '@/lib/link';
import { AlertTriangle, RotateCcw, Server } from 'lucide-react';
import { StatePanel } from '@/components/ui/empty-state';

/**
 * Thin layout for the cluster subtree: its only job is scoping the error
 * boundary (F-04) to a single cluster context. A failure in one cluster tab
 * is caught here, so the surrounding dashboard chrome — and the cluster
 * nav — stay intact instead of the whole console going blank.
 */
function ClusterError({ error, reset }: ErrorComponentProps) {
  useEffect(() => {
    console.error('Cluster page render error:', error);
  }, [error]);

  // Next.js attached a `digest` ref to server-thrown errors; keep reading it
  // defensively for anything that still tags one on.
  const digest = 'digest' in error ? String((error as { digest?: string }).digest ?? '') : '';

  return (
    <div data-testid="route-error-boundary" className="flex flex-col items-center">
      <StatePanel
        icon={AlertTriangle}
        tone="danger"
        title="This cluster view failed to load"
        description={
          <>
            {error.message || 'An unexpected error occurred while rendering this cluster page.'}
            {digest && (
              <span className="mt-1 block font-mono text-xs opacity-70">ref: {digest}</span>
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

export const Route = createFileRoute('/dashboard/clusters/$id')({
  component: Outlet,
  errorComponent: ClusterError,
});
