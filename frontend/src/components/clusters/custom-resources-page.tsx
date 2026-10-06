// GATE C — dynamic custom-resource (CRD instance) explorer.
//
// ponytail: ONE shared component (mounted by both the custom-resources index
// and splat routes) handles all three views, keyed off the slug length,
// instead of three sibling routes that would each re-derive the same
// cluster/permission scaffolding:
//   []                              → CRD list   (E1)
//   [group, version, plural]        → CR list    (E2)
//   [group, version, plural, name]  → cluster-scoped CR detail
//   [group, version, plural, ns, name] → namespaced CR detail
// The group segment uses '_' as a sentinel for the (rare) empty group so the
// URL never has an empty path segment — see crListHref/crDetailHref.

import { useMemo } from "react";
import { useLocation, useNavigate, useParams } from "@tanstack/react-router";
import { Link as RouterLink } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { getCompleteResourceDiscovery } from "@/lib/api/resources";
import { queryKeys } from "@/lib/query-keys";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { ResourceDetail } from "@/components/resources/resource-detail";
import { DataTable, type Column } from "@/components/ui/data-table";
import { crResourcePath, crListHref } from "@/lib/k8s-paths";
import {
  ageColumn,
  ChipList,
} from "@/components/resources/networking-table-cells";
import { PageHeader, PageShell } from "@/components/ui/page";
import { CustomResourceList } from "./custom-resource-list";
import { QueryStates } from "@/components/ui/query-states";

// CR proxy access is gated server-side on the `custom_resources` RBAC resource
// (see internal/server/routes.go); mirror that for client gating.
const CR_PERMISSION = "custom_resources";

function decodeGroup(seg: string): string {
  return seg === "_" ? "" : seg;
}

export function CustomResourcesPage({ slug }: { slug: string[] }) {
  const params = useParams({ from: "/dashboard/clusters/$id" });
  const clusterId = params.id;
  const searchStr = useLocation({ select: (location) => location.searchStr });

  const read = usePermissionDecision("clusters", "read", {
    type: "cluster",
    id: clusterId,
  });

  if (!read.allowed) {
    return (
      <div className="flex items-center justify-center py-24 text-sm text-muted-foreground">
        {read.disabledReason || read.reason}
      </div>
    );
  }

  // CRD list (E1)
  if (slug.length === 0) {
    return <CRDList clusterId={clusterId} />;
  }

  const [groupSeg, version, plural, ...rest] = slug;
  const group = decodeGroup(groupSeg);

  // CR list (E2): [group, version, plural]
  if (rest.length === 0) {
    return (
      <CustomResourceList
        key={`${clusterId}/${group}/${version}/${plural}`}
        clusterId={clusterId}
        group={group}
        version={version}
        plural={plural}
      />
    );
  }

  // CR detail: cluster-scoped [..., name] or namespaced [..., ns, name].
  const namespace = rest.length >= 2 ? rest[0] : undefined;
  const name = rest.length >= 2 ? rest[1] : rest[0];
  const k8sPath = crResourcePath(group, version, plural, name, namespace);

  return (
    <ResourceDetail
      clusterId={clusterId}
      resourceType={plural}
      namespace={namespace}
      name={name}
      k8sPath={k8sPath}
      permissionResource={CR_PERMISSION}
      collectionHref={`${crListHref(clusterId, group, version, plural)}${searchStr}`}
    />
  );
}

// ── E1: CRD list ──

interface CRDRow {
  name: string;
  group: string;
  kind: string;
  plural: string;
  versions: string[];
  storageVersion: string;
  scope: string;
  createdAt: string;
}

const crdColumns: Column<CRDRow>[] = [
  {
    key: "kind",
    header: "Kind",
    kind: "name",
    minSize: 168,
    accessor: (row) => (
      <span className="font-medium text-foreground text-xs">{row.kind}</span>
    ),
    sortAccessor: (row) => row.kind,
  },
  {
    key: "group",
    header: "Group",
    kind: "text",
    minSize: 176,
    size: 208,
    sortAccessor: (row) => row.group,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.group || "-"}
      </span>
    ),
  },
  {
    key: "plural",
    header: "Plural",
    kind: "text",
    minSize: 128,
    size: 160,
    sortAccessor: (row) => row.plural,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.plural}
      </span>
    ),
  },
  {
    key: "versions",
    header: "Versions",
    kind: "version",
    minSize: 160,
    size: 176,
    accessor: (row) => <ChipList items={row.versions} />,
    searchAccessor: (row) => row.versions.join(" "),
    sortable: false,
  },
  {
    key: "scope",
    header: "Scope",
    kind: "badge",
    minSize: 96,
    size: 104,
    accessor: (row) => (
      <span className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground">
        {row.scope || "-"}
      </span>
    ),
    sortAccessor: (row) => row.scope,
    filter: { label: "Scope" },
  },
  {
    ...ageColumn<CRDRow>("-"),
  },
];

function CRDList({ clusterId }: { clusterId: string }) {
  const navigate = useNavigate();
  const search = useLocation({ select: (location) => location.searchStr });
  const query = useQuery({
    queryKey: queryKeys.generic.completeDiscovery(clusterId),
    queryFn: ({ signal }) => getCompleteResourceDiscovery(clusterId, signal),
    staleTime: 5 * 60_000,
    throwOnError: false,
  });
  const rows = useMemo<CRDRow[]>(
    () =>
      (query.data?.crds ?? [])
        .map((item) => ({
          name: String(item.name ?? ""),
          group: String(item.group ?? ""),
          kind: String(item.kind ?? ""),
          plural: String(item.plural ?? ""),
          versions: ((item.versions as Array<{ name: string }>) ?? []).map(
            (version) => version.name,
          ),
          storageVersion:
            (
              (item.versions as Array<{ name: string; storage: boolean }>) ?? []
            ).find((version) => version.storage)?.name ?? "",
          scope: String(item.scope ?? ""),
          createdAt: "",
        }))
        .filter((row) => row.plural && row.storageVersion),
    [query.data],
  );

  const columns = useMemo<Column<CRDRow>[]>(
    () => [
      {
        ...crdColumns[0],
        accessor: (row) => (
          <RouterLink
            to={
              crListHref(clusterId, row.group, row.storageVersion, row.plural) +
              search
            }
            onClick={(e) => e.stopPropagation()}
            className="font-medium text-foreground text-xs hover:underline"
          >
            {row.kind}
          </RouterLink>
        ),
      },
      ...crdColumns.slice(1),
    ],
    [clusterId, search],
  );

  return (
    <PageShell>
      <PageHeader title="Custom Resources" />
      <QueryStates
        query={query}
        permission="clusters:read"
        errorTitle="Custom resource definitions unavailable"
      >
        <DataTable
          data={rows}
          columns={columns}
          keyExtractor={(r) => r.name}
          onRowClick={(row) =>
            void navigate({
              to:
                crListHref(
                  clusterId,
                  row.group,
                  row.storageVersion,
                  row.plural,
                ) + search,
            })
          }
          searchPlaceholder="Search custom resource definitions..."
          emptyState={{
            title: "No custom resource definitions found",
            description:
              "Resources will appear here when they are available in this scope.",
          }}
        />
      </QueryStates>
    </PageShell>
  );
}
