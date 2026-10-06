import { StatusBadge } from "@/components/ui/status-badge";
import type { Column } from "@/components/ui/data-table";
import { formatRelativeTime } from "@/lib/utils";
import {
  ageColumn,
  chWidth,
  chipColumn,
  countColumn,
  namespaceColumn,
  plainNameColumn,
  textColumn,
} from "@/components/resources/resource-column-kit";
import type { GenericK8sResource } from "@/types";

const jobColumns: Column<GenericK8sResource>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "status",
    header: "Status",
    accessor: (row) => <StatusBadge status={row.status || "Pending"} />,
  },
  {
    key: "completions",
    header: "Completions",
    accessor: (row) => (
      <span className="tabular-nums text-xs">
        {row.succeeded ?? 0}/{row.completions ?? 1}
      </span>
    ),
    align: "center",
  },
  {
    key: "age",
    header: "Age",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {formatRelativeTime(row.createdAt)}
      </span>
    ),
  },
];

const cronJobColumns: Column<GenericK8sResource>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "schedule",
    header: "Schedule",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.schedule}
      </span>
    ),
  },
  {
    key: "status",
    header: "Status",
    accessor: (row) => <StatusBadge status={row.status || "Active"} />,
  },
  {
    key: "lastSchedule",
    header: "Last Schedule",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {row.lastSchedule ? formatRelativeTime(row.lastSchedule) : "-"}
      </span>
    ),
  },
  {
    key: "active",
    header: "Active",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.activeCount ?? 0}</span>
    ),
    align: "center",
  },
  {
    key: "age",
    header: "Age",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {formatRelativeTime(row.createdAt)}
      </span>
    ),
  },
];

export const configMapColumns: Column<GenericK8sResource>[] = [
  plainNameColumn<GenericK8sResource>(),
  namespaceColumn<GenericK8sResource>(),
  countColumn<GenericK8sResource>("data", "Data", (row) => row.dataCount),
  ageColumn<GenericK8sResource>((row) => row.createdAt),
];

const secretColumns: Column<GenericK8sResource>[] = [
  plainNameColumn<GenericK8sResource>(),
  namespaceColumn<GenericK8sResource>(),
  {
    key: "type",
    header: "Type",
    kind: "badge",
    size: 260,
    minSize: chWidth(28),
    maxSize: 380,
    accessor: (row) => (
      <span className="inline-block max-w-full truncate rounded-sm bg-muted px-1.5 py-0.5 align-middle text-2xs text-muted-foreground">
        {row.type || "Opaque"}
      </span>
    ),
    searchAccessor: (row) => row.type || "Opaque",
  },
  countColumn<GenericK8sResource>("data", "Data", (row) => row.dataCount),
  ageColumn<GenericK8sResource>((row) => row.createdAt),
];

const hpaColumns: Column<GenericK8sResource>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "target",
    header: "Target",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.targetKind}/{row.targetName}
      </span>
    ),
  },
  {
    key: "minmax",
    header: "Min/Max",
    accessor: (row) => (
      <span className="tabular-nums text-xs">
        {row.minReplicas ?? 0}/{row.maxReplicas ?? 0}
      </span>
    ),
    align: "center",
  },
  {
    key: "replicas",
    header: "Replicas",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.currentReplicas ?? 0}</span>
    ),
    align: "center",
  },
  {
    key: "age",
    header: "Age",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {formatRelativeTime(row.createdAt)}
      </span>
    ),
  },
];

const resourceQuotaColumns: Column<GenericK8sResource>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "age",
    header: "Age",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {formatRelativeTime(row.createdAt)}
      </span>
    ),
  },
];

const limitRangeColumns: Column<GenericK8sResource>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "age",
    header: "Age",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {formatRelativeTime(row.createdAt)}
      </span>
    ),
  },
];

const pdbColumns: Column<GenericK8sResource>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "minAvailable",
    header: "Min Available",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.minAvailable || "-"}</span>
    ),
    align: "center",
  },
  {
    key: "maxUnavailable",
    header: "Max Unavailable",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.maxUnavailable || "-"}</span>
    ),
    align: "center",
  },
  {
    key: "currentHealthy",
    header: "Healthy",
    accessor: (row) => (
      <span className="tabular-nums text-xs">
        {row.currentHealthy ?? 0}/{row.desiredHealthy ?? 0}
      </span>
    ),
    align: "center",
  },
  {
    key: "age",
    header: "Age",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {formatRelativeTime(row.createdAt)}
      </span>
    ),
  },
];

const crdColumns: Column<GenericK8sResource>[] = [
  plainNameColumn<GenericK8sResource>(),
  textColumn<GenericK8sResource>("group", "Group", (row) => row.group, {
    mono: true,
    size: 240,
    minSize: chWidth(24),
  }),
  textColumn<GenericK8sResource>("kind", "Kind", (row) => row.kind, {
    size: 160,
    minSize: chWidth(14),
  }),
  {
    ...textColumn<GenericK8sResource>(
      "version",
      "Version",
      (row) => row.version,
    ),
    kind: "version",
    size: 112,
  },
  {
    key: "scope",
    header: "Scope",
    kind: "badge",
    accessor: (row) => (
      <span className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground">
        {row.scope}
      </span>
    ),
    searchAccessor: (row) => row.scope ?? "",
  },
  ageColumn<GenericK8sResource>((row) => row.createdAt),
];

const serviceAccountColumns: Column<GenericK8sResource>[] = [
  plainNameColumn<GenericK8sResource>(),
  namespaceColumn<GenericK8sResource>(),
  countColumn<GenericK8sResource>(
    "secrets",
    "Secrets",
    (row) => row.secretsCount,
  ),
  ageColumn<GenericK8sResource>((row) => row.createdAt),
];

const k8sRoleColumns: Column<GenericK8sResource>[] = [
  plainNameColumn<GenericK8sResource>(),
  namespaceColumn<GenericK8sResource>(),
  countColumn<GenericK8sResource>("rules", "Rules", (row) => row.rulesCount),
  ageColumn<GenericK8sResource>((row) => row.createdAt),
];

const k8sRoleBindingColumns: Column<GenericK8sResource>[] = [
  plainNameColumn<GenericK8sResource>(),
  namespaceColumn<GenericK8sResource>(),
  textColumn<GenericK8sResource>(
    "role",
    "Role",
    (row) => `${row.roleKind}/${row.roleName}`,
    { mono: true, size: 260, minSize: chWidth(24) },
  ),
  countColumn<GenericK8sResource>(
    "subjects",
    "Subjects",
    (row) => row.subjectsCount,
  ),
  ageColumn<GenericK8sResource>((row) => row.createdAt),
];

const endpointColumns: Column<GenericK8sResource>[] = [
  plainNameColumn<GenericK8sResource>(),
  namespaceColumn<GenericK8sResource>(),
  countColumn<GenericK8sResource>(
    "endpoints",
    "Endpoints",
    (row) => row.addressesCount,
  ),
  chipColumn<GenericK8sResource>(
    "ports",
    "Ports",
    (row) => row.ports?.split(/,\s*/).filter(Boolean),
    { size: 200, minSize: chWidth(16), maxSize: 280 },
  ),
  ageColumn<GenericK8sResource>((row) => row.createdAt),
];

const replicaSetColumns: Column<GenericK8sResource>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "desired",
    header: "Desired",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.desired ?? 0}</span>
    ),
    align: "center",
  },
  {
    key: "ready",
    header: "Ready",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.ready ?? 0}</span>
    ),
    align: "center",
  },
  {
    key: "available",
    header: "Available",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.available ?? 0}</span>
    ),
    align: "center",
  },
  {
    key: "age",
    header: "Age",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {formatRelativeTime(row.createdAt)}
      </span>
    ),
  },
];

// Map of generic resource type → columns
export const genericColumnMap: Record<string, Column<GenericK8sResource>[]> = {
  jobs: jobColumns,
  cronjobs: cronJobColumns,
  configmaps: configMapColumns,
  secrets: secretColumns,
  hpa: hpaColumns,
  resourcequotas: resourceQuotaColumns,
  limitranges: limitRangeColumns,
  poddisruptionbudgets: pdbColumns,
  crds: crdColumns,
  serviceaccounts: serviceAccountColumns,
  "k8s-clusterroles": k8sRoleColumns,
  "k8s-clusterrolebindings": k8sRoleBindingColumns,
  "k8s-roles": k8sRoleColumns,
  "k8s-rolebindings": k8sRoleBindingColumns,
  endpoints: endpointColumns,
  replicasets: replicaSetColumns,
};
