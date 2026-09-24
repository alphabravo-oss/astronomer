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
import { useNavigate, useParams } from "@tanstack/react-router";
import { Link as RouterLink } from "@tanstack/react-router";
import { useK8sResource } from "@/lib/hooks/kubernetes-proxy";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { ResourceDetail } from "@/components/resources/resource-detail";
import { DataTable, type Column } from "@/components/ui/data-table";
import { crResourcePath, crListHref } from "@/lib/k8s-paths";
import { formatRelativeTime } from "@/lib/utils";
import { PageHeader } from "@/components/ui/page";
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

  const read = usePermissionDecision(CR_PERMISSION, "read", {
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
    />
  );
}

// ── E1: CRD list ──

interface CRDItem {
  metadata?: { name?: string; creationTimestamp?: string };
  spec?: {
    group?: string;
    scope?: string;
    names?: { kind?: string; plural?: string };
    versions?: Array<{ name?: string; served?: boolean; storage?: boolean }>;
  };
}

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

function toCRDRow(item: CRDItem): CRDRow {
  const spec = item.spec ?? {};
  const versions = (spec.versions ?? [])
    .map((v) => v.name ?? "")
    .filter(Boolean);
  const storage = (spec.versions ?? []).find((v) => v.storage)?.name;
  return {
    name: item.metadata?.name ?? "",
    group: spec.group ?? "",
    kind: spec.names?.kind ?? "",
    plural: spec.names?.plural ?? "",
    versions,
    storageVersion: storage ?? versions[0] ?? "",
    scope: spec.scope ?? "",
    createdAt: item.metadata?.creationTimestamp ?? "",
  };
}

const crdColumns: Column<CRDRow>[] = [
  {
    key: "kind",
    header: "Kind",
    accessor: (row) => (
      <span className="font-medium text-foreground text-xs">{row.kind}</span>
    ),
    sortAccessor: (row) => row.kind,
  },
  {
    key: "group",
    header: "Group",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.group || "-"}
      </span>
    ),
  },
  {
    key: "plural",
    header: "Plural",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.plural}
      </span>
    ),
  },
  {
    key: "versions",
    header: "Versions",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.versions.join(", ") || "-"}
      </span>
    ),
    sortable: false,
  },
  {
    key: "scope",
    header: "Scope",
    accessor: (row) => (
      <span className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground">
        {row.scope || "-"}
      </span>
    ),
    sortAccessor: (row) => row.scope,
    filter: { label: "Scope" },
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
];

function CRDList({ clusterId }: { clusterId: string }) {
  const navigate = useNavigate();
  // E1: a SINGLE proxy GET to the CRD list endpoint — no /apis discovery walk.
  const query = useK8sResource(
    clusterId,
    "apis/apiextensions.k8s.io/v1/customresourcedefinitions",
  );

  const rows = useMemo<CRDRow[]>(() => {
    const items =
      (query.data as { items?: CRDItem[] } | undefined)?.items ?? [];
    return items.map(toCRDRow).filter((r) => r.plural && r.storageVersion);
  }, [query.data]);

  const columns = useMemo<Column<CRDRow>[]>(
    () => [
      {
        ...crdColumns[0],
        accessor: (row) => (
          <RouterLink
            to={crListHref(
              clusterId,
              row.group,
              row.storageVersion,
              row.plural,
            )}
            onClick={(e) => e.stopPropagation()}
            className="font-medium text-foreground text-xs hover:underline"
          >
            {row.kind}
          </RouterLink>
        ),
      },
      ...crdColumns.slice(1),
    ],
    [clusterId],
  );

  return (
    <div className="space-y-4">
      <PageHeader title="Custom Resources" />
      <QueryStates
        query={query}
        permission="custom_resources:read"
        errorTitle="Custom resource definitions unavailable"
      >
        <DataTable
          data={rows}
          columns={columns}
          keyExtractor={(r) => r.name}
          onRowClick={(row) =>
            void navigate({
              to: crListHref(
                clusterId,
                row.group,
                row.storageVersion,
                row.plural,
              ),
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
    </div>
  );
}
