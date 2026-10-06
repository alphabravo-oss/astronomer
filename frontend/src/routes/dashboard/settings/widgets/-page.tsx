/**
 * /dashboard/settings/widgets — admin CRUD for dashboard widgets +
 * Prometheus datasources (migration 058).
 *
 * Renders two tables:
 *   1. Widgets — list + inline-create form. The form switches
 *      spec shape based on the selected widget_type (grafana_panel /
 *      prom_sparkline / prom_stat / url_iframe). Edits open a
 *      pre-filled form in place.
 *   2. Prometheus datasources — list + create. The /test/ button
 *      validates connectivity end-to-end.
 *
 * Superuser-only at the API layer; the SPA gates the navigation
 * entry behind the same useIsSuperuser hook as the rest of the
 * settings hub.
 */

import { ResourceMasthead, PageShell } from "@/components/ui/page";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { QueryStates } from "@/components/ui/query-states";
import { useWidgetsAdmin } from "./-use-widgets-admin";
import { useDatasourcesAdmin } from "./-use-datasources-admin";
import { WidgetsSection } from "./-widgets-section";
import { DatasourcesSection } from "./-datasources-section";

export function WidgetsAdminPage() {
  const widgetsAdmin = useWidgetsAdmin();
  const datasourcesAdmin = useDatasourcesAdmin();
  const { widgetsQuery, error } = widgetsAdmin;
  const { datasourcesQuery } = datasourcesAdmin;

  return (
    <PageShell>
      <ResourceMasthead
        backTo="/dashboard/settings"
        backLabel="Settings"
        title="Dashboard widgets"
        description="Define widgets pinned to the global dashboard, per-cluster pages, or per-project pages."
      />

      {widgetsQuery.isError && (
        <QueryStates query={widgetsQuery} permission="dashboard_widgets:read">
          {() => null}
        </QueryStates>
      )}
      {datasourcesQuery.isError && (
        <QueryStates
          query={datasourcesQuery}
          permission="dashboard_widgets:read"
        >
          {() => null}
        </QueryStates>
      )}

      {error ? (
        <div className="text-sm text-status-error">
          {error ??
            (widgetsQuery.error instanceof Error
              ? widgetsQuery.error.message
              : "Failed to load widgets")}
        </div>
      ) : null}

      <WidgetsSection admin={widgetsAdmin} />

      <DatasourcesSection admin={datasourcesAdmin} />

      <ConfirmDialog
        open={!!widgetsAdmin.deleteTarget}
        onClose={() => widgetsAdmin.setDeleteTarget(null)}
        onConfirm={widgetsAdmin.confirmDelete}
        title="Delete Widget"
        description={`Delete widget "${widgetsAdmin.deleteTarget?.name}"? This action cannot be undone.`}
        confirmText="Delete"
        variant="destructive"
        loading={widgetsAdmin.deleting}
      />

      <ConfirmDialog
        open={!!datasourcesAdmin.deleteTarget}
        onClose={() => datasourcesAdmin.setDeleteTarget(null)}
        onConfirm={datasourcesAdmin.confirmDelete}
        title="Delete Datasource"
        description={`Delete datasource "${datasourcesAdmin.deleteTarget?.name}"? This action cannot be undone.`}
        confirmText="Delete"
        variant="destructive"
        loading={datasourcesAdmin.deleting}
      />
    </PageShell>
  );
}
