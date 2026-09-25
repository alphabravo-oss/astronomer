import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  getClusterApp,
  getClusterAppHistory,
  getClusterAppValues,
} from "@/lib/api/cluster-apps";
import { queryKeys } from "@/lib/query-keys";
import { QueryStates } from "@/components/ui/query-states";
import { PageSection } from "@/components/ui/page";
import { StatusBadge } from "@/components/ui/status-badge";
import { DataTable } from "@/components/ui/data-table";

export function InstalledReleaseDetail({
  id,
  clusterId,
}: {
  id: string;
  clusterId: string;
}) {
  const query = useQuery({
    queryKey: queryKeys.catalog.release(id),
    queryFn: ({ signal }) => getClusterApp(id, signal),
    throwOnError: false,
  });
  return (
    <PageSection title="Installed release">
      <QueryStates
        query={query}
        permission="catalog:read"
        errorTitle="Release unavailable"
      >
        {(release) =>
          release.cluster_id !== clusterId ? (
            <p role="alert">This release belongs to a different cluster.</p>
          ) : (
            <div className="space-y-4">
              <p className="font-semibold">{release.release_name}</p>
              <StatusBadge status={release.status || "Unknown"} />
              <dl className="grid grid-cols-2 gap-2 text-sm">
                <dt>Namespace</dt>
                <dd>{release.namespace}</dd>
                <dt>Chart</dt>
                <dd>{release.chart_name || "Unavailable"}</dd>
                <dt>Version</dt>
                <dd>{release.chart_version || "Unavailable"}</dd>
                <dt>Owner</dt>
                <dd>{release.source_kind === "tool" ? "Tools" : "Catalog"}</dd>
              </dl>
              {release.source_kind === "tool" ? (
                <Link to={String(`/dashboard/clusters/${clusterId}/tools`)}>
                  Manage in Tools
                </Link>
              ) : (
                <ReleaseDiagnostics id={id} />
              )}
              <Link
                to={String(
                  `/dashboard/clusters/${clusterId}/pods?namespaces=${encodeURIComponent(release.namespace || "")}`,
                )}
              >
                Inspect namespace pods
              </Link>
            </div>
          )
        }
      </QueryStates>
    </PageSection>
  );
}
function ReleaseDiagnostics({ id }: { id: string }) {
  const values = useQuery({
    queryKey: queryKeys.catalog.releaseValues(id),
    queryFn: ({ signal }) => getClusterAppValues(id, signal),
    throwOnError: false,
  });
  const history = useQuery({
    queryKey: queryKeys.catalog.releaseHistory(id),
    queryFn: ({ signal }) => getClusterAppHistory(id, signal),
    throwOnError: false,
  });
  return (
    <>
      <QueryStates
        query={values}
        permission="catalog:read"
        errorTitle="Release values unavailable"
      >
        {(yaml) => (
          <details>
            <summary>Saved release values</summary>
            <pre className="overflow-auto whitespace-pre-wrap text-xs">
              {yaml || "No value overrides"}
            </pre>
          </details>
        )}
      </QueryStates>
      <QueryStates
        query={history}
        permission="catalog:read"
        errorTitle="Release history unavailable"
      >
        {(result) => (
          <DataTable
            data={result.revisions}
            columns={[
              {
                key: "revision",
                header: "Revision",
                accessor: (row) => row.revision,
              },
              {
                key: "status",
                header: "Status",
                accessor: (row) => row.status || "Unknown",
              },
              {
                key: "description",
                header: "Description",
                accessor: (row) => row.description || "—",
              },
            ]}
            keyExtractor={(row) => String(row.revision)}
            searchable={false}
            emptyState={{
              title: "No revision history returned",
              description:
                "No revision records are available from the release.",
            }}
          />
        )}
      </QueryStates>
    </>
  );
}
