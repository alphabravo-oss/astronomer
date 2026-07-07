'use client';

/**
 * Read-only encryption/JWT key-status surface (F-05). Operators poll this
 * before and after a `keyrotate` run to confirm the new key is loaded and the
 * old one was dropped — see docs/secret-rotation-runbook.md. Superuser-gated
 * server-side; renders inside a settings page that is already admin-gated.
 */

import { useQuery } from '@tanstack/react-query';
import { KeyRound, RefreshCw, Loader2 } from 'lucide-react';
import { getKeyStatus } from '@/lib/api';
import { queryKeys } from '@/lib/query-keys';
import { formatDate } from '@/lib/utils';
import { ErrorState } from '@/components/ui/empty-state';

export function KeyStatusPanel() {
  const { data, isLoading, isError, refetch, isFetching } = useKeyStatusQuery();

  return (
    <section className="rounded-xl border border-border bg-card p-6 space-y-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h2 className="text-base font-semibold text-foreground flex items-center gap-2">
            <KeyRound className="h-4 w-4" />
            Encryption keys
          </h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Live count of loaded Fernet + JWT signing keys. Confirm a rotation landed here.
          </p>
        </div>
        <button
          type="button"
          onClick={() => refetch()}
          className="inline-flex items-center gap-1.5 h-8 px-3 rounded-lg border border-border text-xs font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
        >
          <RefreshCw className="h-3.5 w-3.5" />
          Refresh
        </button>
      </div>

      {isLoading ? (
        <div className="flex items-center gap-2 text-sm text-muted-foreground py-4">
          <Loader2 className="h-4 w-4 animate-spin" />
          Loading key status…
        </div>
      ) : isError ? (
        <ErrorState
          title="Failed to load key status"
          description="This surface requires superuser privileges."
          onRetry={() => refetch()}
        />
      ) : data ? (
        <div className="grid grid-cols-2 gap-3">
          <KeyTile label="Encryption keys" value={data.encryptionKeys} />
          <KeyTile label="JWT signing keys" value={data.jwtKeys} />
          <p className="col-span-2 text-2xs text-muted-foreground">
            As of {formatDate(data.asOf)}
            {isFetching ? ' · refreshing…' : ''}
          </p>
        </div>
      ) : null}
    </section>
  );
}

function KeyTile({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-lg border border-border bg-background px-4 py-3">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <p className="mt-1 text-2xl font-semibold tabular-nums text-foreground">{value}</p>
    </div>
  );
}

function useKeyStatusQuery() {
  return useQuery({
    queryKey: queryKeys.adminSecurity.keyStatus,
    queryFn: getKeyStatus,
    staleTime: 15_000,
  });
}
