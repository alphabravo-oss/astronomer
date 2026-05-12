'use client';

/**
 * Admin gate for the Settings hub. Wraps the page body and shows a 403
 * placeholder for non-superusers. The backend already returns 403 for the
 * underlying endpoints, but rendering the gate up-front avoids the
 * loading-then-error flash and keeps the page header informative when an
 * operator browses to a URL they can't actually use.
 */
import Link from 'next/link';
import { Lock } from 'lucide-react';
import { useIsSuperuser } from '@/components/settings/hooks';

export function SettingsAuthGate({ children }: { children: React.ReactNode }) {
  const { isSuperuser, ready } = useIsSuperuser();

  if (!ready) {
    // Auth state still hydrating from the persisted store — render nothing
    // rather than flashing a 403.
    return null;
  }

  if (!isSuperuser) {
    return (
      <div className="max-w-md mx-auto mt-12 rounded-xl border border-border bg-card p-8 space-y-4 text-center">
        <div className="mx-auto w-12 h-12 rounded-full bg-muted flex items-center justify-center">
          <Lock className="h-5 w-5 text-muted-foreground" />
        </div>
        <div className="space-y-1">
          <h2 className="text-lg font-semibold text-foreground">Admins only</h2>
          <p className="text-sm text-muted-foreground">
            This settings surface is gated to platform administrators.
          </p>
        </div>
        <Link
          href="/dashboard"
          className="inline-flex items-center justify-center h-9 px-4 rounded-lg border border-border text-sm font-medium hover:bg-accent transition-colors"
        >
          Back to dashboard
        </Link>
      </div>
    );
  }

  return <>{children}</>;
}
