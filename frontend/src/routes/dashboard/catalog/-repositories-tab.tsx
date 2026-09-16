import { SuggestedCatalogs } from "@/components/catalog/suggested-catalogs";
import { PageSection } from "@/components/ui/page";
import type { HelmRepository } from "@/types";
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
  return (
    <div className="space-y-6">
      <SuggestedCatalogs existing={repos} />
      <PageSection title="Your repositories">
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
