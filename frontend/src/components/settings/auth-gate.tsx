'use client';

/**
 * Admin gate for the Settings hub. Wraps the page body and shows a 403
 * placeholder for non-superusers. The backend already returns 403 for the
 * underlying endpoints, but rendering the gate up-front avoids the
 * loading-then-error flash and keeps the page header informative when an
 * operator browses to a URL they can't actually use.
 */
import { ArrowLeft, Lock } from 'lucide-react';
import { useIsSuperuser } from '@/components/settings/hooks';
import { EmptyState } from '@/components/ui/empty-state';

export function SettingsAuthGate({ children }: { children: React.ReactNode }) {
  const { isSuperuser, ready } = useIsSuperuser();

  if (!ready) {
    // Auth state still hydrating from the persisted store — render nothing
    // rather than flashing a 403.
    return null;
  }

  if (!isSuperuser) {
    return (
      <EmptyState
        icon={Lock}
        title="Admins only"
        description="This settings surface is gated to platform administrators."
        actionLabel="Back to dashboard"
        actionHref="/dashboard"
        actionIcon={ArrowLeft}
        className="mx-auto mt-12 max-w-md rounded-lg border border-border bg-card p-8"
      />
    );
  }

  return <>{children}</>;
}
