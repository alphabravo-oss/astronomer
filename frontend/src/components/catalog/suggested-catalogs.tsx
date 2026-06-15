'use client';

import { useMemo, useState } from 'react';
import { Package, Check, Plus, Loader2, AlertTriangle } from 'lucide-react';
import { ModalShell } from '@/components/ui/modal-shell';
import { useCreateHelmRepository } from '@/lib/hooks';
import { SUGGESTED_CATALOGS, normalizeRepoUrl, type SuggestedCatalog } from '@/lib/catalogs/suggested';
import type { HelmRepository } from '@/types';
import { cn } from '@/lib/utils';

interface SuggestedCatalogsProps {
  /** Existing helm_repositories rows — used to determine "Added" state. */
  existing: HelmRepository[] | undefined;
  /** Callback when user clicks an existing-row badge so the parent can scroll/highlight. */
  onJumpToExisting?: (repo: HelmRepository) => void;
}

export function SuggestedCatalogs({ existing, onJumpToExisting }: SuggestedCatalogsProps) {
  const createRepo = useCreateHelmRepository();
  const [pendingName, setPendingName] = useState<string | null>(null);
  const [dhiConfirm, setDhiConfirm] = useState<SuggestedCatalog | null>(null);

  const existingByUrl = useMemo(() => {
    const map = new Map<string, HelmRepository>();
    for (const repo of existing || []) {
      map.set(normalizeRepoUrl(repo.url), repo);
    }
    return map;
  }, [existing]);

  const addCatalog = async (catalog: SuggestedCatalog) => {
    setPendingName(catalog.name);
    try {
      await createRepo.mutateAsync({
        name: catalog.name,
        url: catalog.url,
        repoType: catalog.repoType,
        description: catalog.description,
      });
    } catch {
      // Hook already surfaces a toast.
    } finally {
      setPendingName(null);
    }
  };

  const handleAddClick = (catalog: SuggestedCatalog) => {
    if (catalog.subscriptionRequired) {
      setDhiConfirm(catalog);
      return;
    }
    void addCatalog(catalog);
  };

  return (
    <div className="space-y-3">
      <div className="flex items-baseline justify-between">
        <div>
          <h2 className="text-sm font-semibold text-foreground">Suggested catalogs</h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            One-click add for well-known chart repositories.
          </p>
        </div>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-3">
        {SUGGESTED_CATALOGS.map((catalog) => {
          const existingRepo = existingByUrl.get(normalizeRepoUrl(catalog.url));
          const isAdded = Boolean(existingRepo);
          const isPending = pendingName === catalog.name;

          return (
            <div
              key={catalog.name}
              className="rounded-lg border border-border p-4 flex flex-col gap-3 bg-card"
            >
              <div className="flex items-start gap-3">
                <div className="flex-shrink-0 h-10 w-10 rounded-lg bg-muted/60 flex items-center justify-center">
                  <Package className="h-5 w-5 text-muted-foreground" />
                </div>
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 flex-wrap">
                    <p className="font-medium text-foreground text-sm truncate">
                      {catalog.displayName}
                    </p>
                    <span className="text-2xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground uppercase font-mono">
                      {catalog.repoType}
                    </span>
                  </div>
                  <p className="text-xs text-muted-foreground mt-1 line-clamp-2 min-h-[2rem]">
                    {catalog.description}
                  </p>
                </div>
              </div>

              {catalog.subscriptionRequired && (
                <div className="inline-flex items-center gap-1.5 self-start rounded-md border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-2xs font-medium text-amber-600 dark:text-amber-400">
                  <AlertTriangle className="h-3 w-3" />
                  Subscription required
                </div>
              )}

              <div className="mt-auto pt-1 flex items-center justify-between">
                <span className="font-mono text-2xs text-muted-foreground truncate max-w-[60%]">
                  {catalog.url}
                </span>
                {isAdded ? (
                  <button
                    type="button"
                    onClick={() => existingRepo && onJumpToExisting?.(existingRepo)}
                    className="inline-flex items-center gap-1 px-2 py-1 rounded-md text-2xs font-medium
                      bg-status-success/10 text-status-success hover:bg-status-success/20 transition-colors"
                    title="View in Your repositories"
                  >
                    <Check className="h-3 w-3" />
                    Added
                  </button>
                ) : (
                  <button
                    type="button"
                    onClick={() => handleAddClick(catalog)}
                    disabled={isPending}
                    className="inline-flex items-center gap-1 px-2.5 py-1 rounded-md text-2xs font-medium
                      bg-primary text-primary-foreground hover:opacity-90 transition-opacity disabled:opacity-50"
                  >
                    {isPending ? (
                      <Loader2 className="h-3 w-3 animate-spin" />
                    ) : (
                      <Plus className="h-3 w-3" />
                    )}
                    Add to catalog
                  </button>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {dhiConfirm && (
        <DhiConfirmModal
          catalog={dhiConfirm}
          onCancel={() => setDhiConfirm(null)}
          onConfirm={() => {
            const target = dhiConfirm;
            setDhiConfirm(null);
            void addCatalog(target);
          }}
          pending={pendingName === dhiConfirm.name}
        />
      )}
    </div>
  );
}

function DhiConfirmModal({
  catalog,
  onCancel,
  onConfirm,
  pending,
}: {
  catalog: SuggestedCatalog;
  onCancel: () => void;
  onConfirm: () => void;
  pending: boolean;
}) {
  return (
    <ModalShell
      title="Subscription required"
      onClose={onCancel}
      size="sm"
      panelClassName="max-w-md bg-popover"
      bodyClassName="space-y-3 py-4"
      footerClassName="bg-muted/30"
      titleIcon={<AlertTriangle className="h-5 w-5 text-amber-500" />}
      footer={(
        <div className="flex items-center justify-end gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={pending}
            className={cn(
              'inline-flex items-center gap-2 h-9 px-4 rounded-lg text-sm font-medium transition-opacity',
              'bg-amber-500 text-white hover:opacity-90 disabled:opacity-50',
            )}
          >
            {pending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            I have a subscription, add anyway
          </button>
        </div>
      )}
    >
          <p className="text-sm text-foreground">
            Add <span className="font-medium">{catalog.displayName}</span>?
          </p>
          <p className="text-sm text-muted-foreground">
            This catalog requires a paid Docker Hardened Images subscription.
            Pulls will fail without credentials configured in{' '}
            <code className="font-mono text-xs px-1 py-0.5 rounded bg-muted">auth_config</code>.
          </p>
    </ModalShell>
  );
}
