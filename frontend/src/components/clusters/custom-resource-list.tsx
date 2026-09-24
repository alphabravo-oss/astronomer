import { useMemo, useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useK8sResource } from "@/lib/hooks/kubernetes-proxy";
import {
  crListPath,
  crResourcePath,
  crDetailHref,
  crdListHref,
} from "@/lib/k8s-paths";
import { formatRelativeTime } from "@/lib/utils";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ResourceMasthead } from "@/components/ui/page";
import { QueryStates } from "@/components/ui/query-states";
import { ActionButton } from "@/components/ui/action-button";
import { PermissionState } from "@/components/ui/empty-state";
import { ResourceActionMenu } from "@/components/resources/resource-action-menu";
import { useClusterResourcePermissions } from "@/components/resources/resource-action-policy";

interface CRListItem {
  metadata?: { name?: string; namespace?: string; creationTimestamp?: string };
}
interface CRRow {
  name: string;
  namespace?: string;
  createdAt: string;
}

/** One server page at a time; proxy continuation tokens remain opaque. */
export function CustomResourceList({
  clusterId,
  group,
  version,
  plural,
}: {
  clusterId: string;
  group: string;
  version: string;
  plural: string;
}) {
  const navigate = useNavigate();
  const [tokens, setTokens] = useState([""]);
  const permissions = useClusterResourcePermissions(
    clusterId,
    "custom_resources",
  );
  const params = new URLSearchParams({ limit: "50" });
  const token = tokens.at(-1);
  if (token) params.set("continue", token);
  const query = useK8sResource(
    clusterId,
    `${crListPath(group, version, plural)}?${params}`,
    permissions.read.allowed,
  );
  const rows = useMemo<CRRow[]>(() => {
    const items =
      (query.data as { items?: CRListItem[] } | undefined)?.items ?? [];
    return items
      .filter((item) => item.metadata?.name)
      .map((item) => ({
        name: item.metadata!.name!,
        namespace: item.metadata?.namespace,
        createdAt: item.metadata?.creationTimestamp ?? "",
      }));
  }, [query.data]);
  const nextToken = (
    query.data as { metadata?: { continue?: string } } | undefined
  )?.metadata?.continue;
  const columns = useMemo<Column<CRRow>[]>(
    () => [
      {
        key: "name",
        header: "Name",
        sortAccessor: (row) => row.name,
        accessor: (row) => (
          <Link
            to={crDetailHref(
              clusterId,
              group,
              version,
              plural,
              row.name,
              row.namespace,
            )}
            onClick={(event) => event.stopPropagation()}
            className="min-w-0 truncate font-medium text-foreground font-mono text-xs hover:underline"
          >
            {row.name}
          </Link>
        ),
      },
      {
        key: "namespace",
        header: "Namespace",
        accessor: (row) => (
          <span className="text-xs text-muted-foreground font-mono">
            {row.namespace || "Cluster scoped"}
          </span>
        ),
      },
      {
        key: "age",
        header: "Age",
        accessor: (row) => (
          <span className="text-xs text-muted-foreground">
            {row.createdAt ? formatRelativeTime(row.createdAt) : "-"}
          </span>
        ),
      },
    ],
    [clusterId, group, version, plural],
  );

  if (!permissions.read.allowed)
    return <PermissionState permission="custom_resources:read" />;

  return (
    <div className="space-y-4">
      <ResourceMasthead
        backTo={crdListHref(clusterId)}
        title={plural}
        mono
        description={
          <span className="font-mono">
            {group ? `${group}/${version}` : version}
          </span>
        }
      />
      <QueryStates
        query={query}
        permission="custom_resources:read"
        errorTitle="Custom resources unavailable"
      >
        <DataTable
          data={rows}
          columns={[
            ...columns,
            {
              key: "actions",
              header: "Actions",
              sortable: false,
              hideable: false,
              width: "60px",
              accessor: (row) => (
                <ResourceActionMenu
                  clusterId={clusterId}
                  resourceType="custom_resources"
                  row={row}
                  permissions={permissions}
                  items={[]}
                  k8sPath={crResourcePath(
                    group,
                    version,
                    plural,
                    row.name,
                    row.namespace,
                  )}
                />
              ),
            },
          ]}
          keyExtractor={(row) =>
            row.namespace ? `${row.namespace}/${row.name}` : row.name
          }
          onRowClick={(row) =>
            void navigate({
              to: crDetailHref(
                clusterId,
                group,
                version,
                plural,
                row.name,
                row.namespace,
              ),
            })
          }
          searchPlaceholder={`Search this page of ${plural}...`}
          emptyState={{
            title: `No ${plural} on this page`,
            description: "Check later pages when a continuation is available.",
          }}
          virtualized
        />
        <div className="flex items-center gap-3">
          <span className="text-xs text-muted-foreground">
            Server page {tokens.length}; up to 50 resources
          </span>
          <ActionButton
            disabled={tokens.length === 1 || query.isFetching}
            onClick={() => setTokens((current) => current.slice(0, -1))}
          >
            Previous
          </ActionButton>
          <ActionButton
            disabled={!nextToken || query.isFetching}
            onClick={() => {
              if (nextToken) setTokens((current) => [...current, nextToken]);
            }}
          >
            Next page
          </ActionButton>
        </div>
      </QueryStates>
    </div>
  );
}
