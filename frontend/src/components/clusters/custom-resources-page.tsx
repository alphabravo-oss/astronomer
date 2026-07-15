'use client';

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

import { useMemo } from 'react';
import { useParams, useRouter } from '@/lib/navigation';
import { Link } from '@/lib/link';
import { useK8sResource } from '@/lib/hooks';
import { usePermissionDecision } from '@/lib/permission-hooks';
import { ResourceDetail } from '@/components/resources/resource-detail';
import { DataTable, type Column } from '@/components/ui/data-table';
import {
  crListPath,
  crResourcePath,
  crListHref,
  crdListHref,
  crDetailHref,
} from '@/lib/k8s-paths';
import { formatRelativeTime } from '@/lib/utils';
import { ArrowLeft } from 'lucide-react';

// CR proxy access is gated server-side on the `custom_resources` RBAC resource
// (see internal/server/routes.go); mirror that for client gating.
const CR_PERMISSION = 'custom_resources';

function decodeGroup(seg: string): string {
  return seg === '_' ? '' : seg;
}

export function CustomResourcesPage({ slug }: { slug: string[] }) {
  const params = useParams();
  const clusterId = params.id as string;

  const read = usePermissionDecision(CR_PERMISSION, 'read', { type: 'cluster', id: clusterId });

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
    return <CRList clusterId={clusterId} group={group} version={version} plural={plural} />;
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
  const versions = (spec.versions ?? []).map((v) => v.name ?? '').filter(Boolean);
  const storage = (spec.versions ?? []).find((v) => v.storage)?.name;
  return {
    name: item.metadata?.name ?? '',
    group: spec.group ?? '',
    kind: spec.names?.kind ?? '',
    plural: spec.names?.plural ?? '',
    versions,
    storageVersion: storage ?? versions[0] ?? '',
    scope: spec.scope ?? '',
    createdAt: item.metadata?.creationTimestamp ?? '',
  };
}

const crdColumns: Column<CRDRow>[] = [
  {
    key: 'kind',
    header: 'Kind',
    accessor: (row) => <span className="font-medium text-foreground text-xs">{row.kind}</span>,
    sortAccessor: (row) => row.kind,
  },
  {
    key: 'group',
    header: 'Group',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.group || '-'}</span>,
  },
  {
    key: 'plural',
    header: 'Plural',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.plural}</span>,
  },
  {
    key: 'versions',
    header: 'Versions',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.versions.join(', ') || '-'}</span>,
    sortable: false,
  },
  {
    key: 'scope',
    header: 'Scope',
    accessor: (row) => (
      <span className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground">{row.scope || '-'}</span>
    ),
    sortAccessor: (row) => row.scope,
    filter: { label: 'Scope' },
  },
  {
    key: 'age',
    header: 'Age',
    accessor: (row) => <span className="text-xs text-muted-foreground">{row.createdAt ? formatRelativeTime(row.createdAt) : '-'}</span>,
  },
];

function CRDList({ clusterId }: { clusterId: string }) {
  const router = useRouter();
  // E1: a SINGLE proxy GET to the CRD list endpoint — no /apis discovery walk.
  const { data, isLoading } = useK8sResource(
    clusterId,
    'apis/apiextensions.k8s.io/v1/customresourcedefinitions',
  );

  const rows = useMemo<CRDRow[]>(() => {
    const items = ((data as { items?: CRDItem[] } | undefined)?.items ?? []);
    return items.map(toCRDRow).filter((r) => r.plural && r.storageVersion);
  }, [data]);

  const columns = useMemo<Column<CRDRow>[]>(() => [
    {
      ...crdColumns[0],
      accessor: (row) => (
        <Link
          href={crListHref(clusterId, row.group, row.storageVersion, row.plural)}
          onClick={(e) => e.stopPropagation()}
          className="font-medium text-foreground text-xs hover:underline"
        >
          {row.kind}
        </Link>
      ),
    },
    ...crdColumns.slice(1),
  ], [clusterId]);

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold text-foreground tracking-tight">Custom Resources</h1>
      <DataTable
        data={rows}
        columns={columns}
        keyExtractor={(r) => r.name}
        onRowClick={(row) => router.push(crListHref(clusterId, row.group, row.storageVersion, row.plural))}
        searchPlaceholder="Search custom resource definitions..."
        loading={isLoading}
        emptyMessage="No custom resource definitions found"
      />
    </div>
  );
}

// ── E2: CR instance list ──

interface CRListItem {
  metadata?: { name?: string; namespace?: string; creationTimestamp?: string };
}

interface CRRow {
  name: string;
  namespace?: string;
  createdAt: string;
}

function CRList({ clusterId, group, version, plural }: {
  clusterId: string; group: string; version: string; plural: string;
}) {
  const router = useRouter();
  // CR lists can be large → virtualized DataTable.
  const { data, isLoading } = useK8sResource(clusterId, crListPath(group, version, plural));

  const rows = useMemo<CRRow[]>(() => {
    const items = ((data as { items?: CRListItem[] } | undefined)?.items ?? []);
    return items.map((it) => ({
      name: it.metadata?.name ?? '',
      namespace: it.metadata?.namespace,
      createdAt: it.metadata?.creationTimestamp ?? '',
    }));
  }, [data]);

  // Namespaced CRs carry metadata.namespace; show the column only if any row has one.
  const namespaced = useMemo(() => rows.some((r) => !!r.namespace), [rows]);

  const columns = useMemo<Column<CRRow>[]>(() => {
    const cols: Column<CRRow>[] = [
      {
        key: 'name',
        header: 'Name',
        accessor: (row) => (
          <Link
            href={crDetailHref(clusterId, group, version, plural, row.name, row.namespace)}
            onClick={(e) => e.stopPropagation()}
            // min-w-0 + truncate: without them the <a> refuses to shrink below
            // its content width inside the virtualized grid's flex cell, spills
            // under the neighboring gridcell on narrow viewports, and becomes
            // unclickable (the neighbor intercepts pointer events).
            className="min-w-0 truncate font-medium text-foreground font-mono text-xs hover:underline"
          >
            {row.name}
          </Link>
        ),
        sortAccessor: (row) => row.name,
      },
    ];
    if (namespaced) {
      cols.push({
        key: 'namespace',
        header: 'Namespace',
        accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace || '-'}</span>,
      });
    }
    cols.push({
      key: 'age',
      header: 'Age',
      accessor: (row) => <span className="text-xs text-muted-foreground">{row.createdAt ? formatRelativeTime(row.createdAt) : '-'}</span>,
    });
    return cols;
  }, [clusterId, group, version, plural, namespaced]);

  return (
    <div className="space-y-4">
      <div className="flex items-start gap-3">
        <Link
          href={crdListHref(clusterId)}
          className="mt-1 p-1 rounded-md hover:bg-accent transition-colors text-muted-foreground hover:text-foreground"
          aria-label="Back"
        >
          <ArrowLeft className="h-5 w-5" />
        </Link>
        <div>
          <h1 className="text-xl font-semibold text-foreground tracking-tight font-mono">{plural}</h1>
          <p className="mt-1 text-xs text-muted-foreground font-mono">
            {group ? `${group}/${version}` : version}
          </p>
        </div>
      </div>
      <DataTable
        data={rows}
        columns={columns}
        keyExtractor={(r) => (r.namespace ? `${r.namespace}/${r.name}` : r.name)}
        onRowClick={(row) => router.push(crDetailHref(clusterId, group, version, plural, row.name, row.namespace))}
        searchPlaceholder={`Search ${plural}...`}
        loading={isLoading}
        emptyMessage={`No ${plural} found`}
        virtualized
      />
    </div>
  );
}
