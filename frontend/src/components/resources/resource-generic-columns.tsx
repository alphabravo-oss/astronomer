import { StatusBadge } from "@/components/ui/status-badge";
import type { Column } from "@/components/ui/data-table";
import { formatRelativeTime } from "@/lib/utils";
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
    key: "data",
    header: "Data",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.dataCount ?? 0}</span>
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

const secretColumns: Column<GenericK8sResource>[] = [
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
    key: "type",
    header: "Type",
    accessor: (row) => (
      <span className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground">
        {row.type || "Opaque"}
      </span>
    ),
  },
  {
    key: "data",
    header: "Data",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.dataCount ?? 0}</span>
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
  {
    key: "name",
    header: "Name",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs truncate max-w-[300px] block">
        {row.name}
      </span>
    ),
  },
  {
    key: "group",
    header: "Group",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.group}
      </span>
    ),
  },
  {
    key: "kind",
    header: "Kind",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">{row.kind}</span>
    ),
  },
  {
    key: "version",
    header: "Version",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">{row.version}</span>
    ),
  },
  {
    key: "scope",
    header: "Scope",
    accessor: (row) => (
      <span className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground">
        {row.scope}
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

const serviceAccountColumns: Column<GenericK8sResource>[] = [
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
    key: "secrets",
    header: "Secrets",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.secretsCount ?? 0}</span>
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

const k8sRoleColumns: Column<GenericK8sResource>[] = [
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
        {row.namespace || "-"}
      </span>
    ),
  },
  {
    key: "rules",
    header: "Rules",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.rulesCount ?? 0}</span>
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

const k8sRoleBindingColumns: Column<GenericK8sResource>[] = [
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
        {row.namespace || "-"}
      </span>
    ),
  },
  {
    key: "role",
    header: "Role",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.roleKind}/{row.roleName}
      </span>
    ),
  },
  {
    key: "subjects",
    header: "Subjects",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.subjectsCount ?? 0}</span>
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

const endpointColumns: Column<GenericK8sResource>[] = [
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
    key: "endpoints",
    header: "Endpoints",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.addressesCount ?? 0}</span>
    ),
    align: "center",
  },
  {
    key: "ports",
    header: "Ports",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground tabular-nums">
        {row.ports || "-"}
      </span>
    ),
    sortable: false,
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
export const genericColumnMap: Record<
  string,
  Column<GenericK8sResource>[]
> = {
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
