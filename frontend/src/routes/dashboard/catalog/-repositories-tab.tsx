import { SuggestedCatalogs } from "@/components/catalog/suggested-catalogs";
import { PageSection } from "@/components/ui/page";
import type { HelmRepository } from "@/types";
import { useApplicationCatalogSources } from "@/lib/hooks/catalog";
import { formatRelativeTime } from "@/lib/utils";
import { AlertTriangle, ShieldCheck } from "lucide-react";
import { RepositoriesTable } from "./-repositories-table";

export function RepositoriesTab({
  repos,
  loading,
  onSync,
  onDelete,
  syncPending,
  deletePending,
}: {
  repos: HelmRepository[] | undefined;
  loading: boolean;
  onSync: (id: string) => void;
  onDelete: (id: string) => void | Promise<void>;
  syncPending: boolean;
  deletePending?: boolean;
}) {
  const applicationSources = useApplicationCatalogSources();
  return (
    <div className="space-y-6">
      <PageSection title="Verified application catalogs">
        <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
          {(applicationSources.data || []).map((source) => (
            <div
              key={source.id}
              className="rounded-lg border border-border bg-card p-4"
            >
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <p className="font-medium text-foreground">
                      {source.display_name || source.name}
                    </p>
                    <span className="inline-flex items-center gap-1 rounded-full border border-primary/30 bg-primary/10 px-2 py-0.5 text-2xs font-medium text-primary">
                      <ShieldCheck className="h-3 w-3" />
                      {source.verification_status}
                    </span>
                  </div>
                  <p className="mt-1 truncate font-mono text-2xs text-table-secondary">
                    {source.source_url}
                  </p>
                </div>
                <span className="flex-none text-xs text-table-secondary">
                  {formatRelativeTime(source.last_synced_at)}
                </span>
              </div>
              {source.last_sync_error && (
                <div className="mt-3 flex items-start gap-2 rounded-md bg-status-warning/10 p-2 text-xs text-status-warning">
                  <AlertTriangle className="mt-0.5 h-3.5 w-3.5 flex-none" />
                  <span>
                    Refresh unavailable; using verified cached revision{" "}
                    <code>{source.source_revision.slice(0, 12)}</code>.
                  </span>
                </div>
              )}
            </div>
          ))}
          {!applicationSources.isLoading &&
            (applicationSources.data?.length ?? 0) === 0 && (
              <div className="rounded-lg border border-dashed border-border p-5 text-sm text-table-secondary">
                No verified application catalog is configured. Repository charts
                and installed applications remain available.
              </div>
            )}
        </div>
      </PageSection>
      <SuggestedCatalogs existing={repos} />
      <PageSection title="Catalog sources">
        <RepositoriesTable
          repos={repos || []}
          loading={loading}
          onSync={onSync}
          onDelete={onDelete}
          syncPending={syncPending}
          deletePending={deletePending}
        />
      </PageSection>
    </div>
  );
}
