import { StatusBadge } from "@/components/ui/status-badge";
import type { Column } from "@/components/ui/data-table";
import { TimestampCell } from "@/components/tables/cells";
import { Tooltip } from "@/components/ui/tooltip";
import {
  ageColumn,
  countColumn,
  monoTextColumn,
  nameStubColumn,
  namespaceColumn,
} from "@/components/resources/networking-table-cells";
import type { GenericK8sResource } from "@/types";

const nameCol = () => nameStubColumn<GenericK8sResource>();

const jobColumns: Column<GenericK8sResource>[] = [
  {
    key: "name",
    header: "Name",
    kind: "name",
    minSize: 220,
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    kind: "name",
    grow: false,
    size: 130,
    minSize: 110,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "status",
    header: "Status",
    kind: "status",
    size: 120,
    accessor: (row) => <StatusBadge status={row.status || "Pending"} />,
  },
  {
    key: "completions",
    header: "Completions",
    kind: "count",
    size: 110,
    accessor: (row) => (
      <span className="tabular-nums text-xs">
        {row.succeeded ?? 0}/{row.completions ?? 1}
      </span>
    ),
  },
  {
    key: "age",
    header: "Age",
    kind: "age",
    size: 80,
    accessor: (row) => (
      <TimestampCell
        value={row.createdAt}
        className="text-xs text-muted-foreground"
      />
    ),
  },
];

const cronJobColumns: Column<GenericK8sResource>[] = [
  {
    key: "name",
    header: "Name",
    kind: "name",
    minSize: 220,
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    kind: "name",
    grow: false,
    size: 130,
    minSize: 110,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "schedule",
    header: "Schedule",
    kind: "text",
    size: 150,
    minSize: 130,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.schedule}
      </span>
    ),
  },
  {
    key: "status",
    header: "Status",
    kind: "status",
    size: 120,
    accessor: (row) => <StatusBadge status={row.status || "Active"} />,
  },
  {
    key: "lastSchedule",
    header: "Last Run",
    ariaLabel: "Last schedule",
    kind: "age",
    size: 119,
    accessor: (row) => (
      <TimestampCell
        value={row.lastSchedule}
        fallback="-"
        suffix
        className="text-xs text-muted-foreground"
      />
    ),
  },
  {
    key: "active",
    header: "Active",
    kind: "count",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.activeCount ?? 0}</span>
    ),
  },
  {
    key: "age",
    header: "Age",
    kind: "age",
    size: 80,
    accessor: (row) => (
      <TimestampCell
        value={row.createdAt}
        className="text-xs text-muted-foreground"
      />
    ),
  },
];

export const configMapColumns: Column<GenericK8sResource>[] = [
  nameCol(),
  namespaceColumn<GenericK8sResource>(),
  countColumn<GenericK8sResource>("data", "Data", (r) => r.dataCount ?? 0),
  ageColumn<GenericK8sResource>(),
];

const secretColumns: Column<GenericK8sResource>[] = [
  nameCol(),
  namespaceColumn<GenericK8sResource>(),
  {
    key: "type",
    header: "Type",
    kind: "badge",
    minSize: 180,
    size: 224,
    maxSize: 320,
    accessor: (row) => (
      <Tooltip content={row.type || "Opaque"}>
        <span
          data-cell-clip=""
          className="block min-w-0 truncate px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground"
        >
          {row.type || "Opaque"}
        </span>
      </Tooltip>
    ),
    sortAccessor: (row) => row.type || "Opaque",
  },
  countColumn<GenericK8sResource>("data", "Data", (r) => r.dataCount ?? 0),
  ageColumn<GenericK8sResource>(),
];

const hpaColumns: Column<GenericK8sResource>[] = [
  {
    key: "name",
    header: "Name",
    kind: "name",
    minSize: 220,
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    kind: "name",
    grow: false,
    size: 130,
    minSize: 110,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "target",
    header: "Target",
    kind: "text",
    size: 240,
    minSize: 200,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.targetKind}/{row.targetName}
      </span>
    ),
  },
  {
    key: "minmax",
    header: "Min/Max",
    kind: "count",
    size: 110,
    accessor: (row) => (
      <span className="tabular-nums text-xs">
        {row.minReplicas ?? 0}/{row.maxReplicas ?? 0}
      </span>
    ),
  },
  {
    key: "replicas",
    header: "Replicas",
    kind: "count",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.currentReplicas ?? 0}</span>
    ),
  },
  {
    key: "age",
    header: "Age",
    kind: "age",
    size: 80,
    accessor: (row) => (
      <TimestampCell
        value={row.createdAt}
        className="text-xs text-muted-foreground"
      />
    ),
  },
];

const resourceQuotaColumns: Column<GenericK8sResource>[] = [
  {
    key: "name",
    header: "Name",
    kind: "name",
    minSize: 220,
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    kind: "name",
    grow: false,
    size: 130,
    minSize: 110,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "age",
    header: "Age",
    kind: "age",
    size: 80,
    accessor: (row) => (
      <TimestampCell
        value={row.createdAt}
        className="text-xs text-muted-foreground"
      />
    ),
  },
];

const limitRangeColumns: Column<GenericK8sResource>[] = [
  {
    key: "name",
    header: "Name",
    kind: "name",
    minSize: 220,
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    kind: "name",
    grow: false,
    size: 130,
    minSize: 110,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "age",
    header: "Age",
    kind: "age",
    size: 80,
    accessor: (row) => (
      <TimestampCell
        value={row.createdAt}
        className="text-xs text-muted-foreground"
      />
    ),
  },
];

const pdbColumns: Column<GenericK8sResource>[] = [
  {
    key: "name",
    header: "Name",
    kind: "name",
    minSize: 220,
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    kind: "name",
    grow: false,
    size: 130,
    minSize: 110,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "minAvailable",
    header: "Min Avail",
    ariaLabel: "Min available",
    kind: "count",
    size: 130,
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.minAvailable || "-"}</span>
    ),
  },
  {
    key: "maxUnavailable",
    header: "Max Unavail",
    ariaLabel: "Max unavailable",
    kind: "count",
    size: 130,
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.maxUnavailable || "-"}</span>
    ),
  },
  {
    key: "currentHealthy",
    header: "Healthy",
    kind: "count",
    accessor: (row) => (
      <span className="tabular-nums text-xs">
        {row.currentHealthy ?? 0}/{row.desiredHealthy ?? 0}
      </span>
    ),
  },
  {
    key: "age",
    header: "Age",
    kind: "age",
    size: 80,
    accessor: (row) => (
      <TimestampCell
        value={row.createdAt}
        className="text-xs text-muted-foreground"
      />
    ),
  },
];

const crdColumns: Column<GenericK8sResource>[] = [
  { ...nameCol(), minSize: 160 },
  monoTextColumn<GenericK8sResource>("group", "Group", (r) => r.group ?? "", {
    minSize: 200,
    size: 208,
  }),
  {
    key: "kind",
    header: "Kind",
    kind: "text",
    minSize: 128,
    size: 144,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">{row.kind}</span>
    ),
    sortAccessor: (row) => row.kind ?? "",
  },
  {
    key: "version",
    header: "Version",
    kind: "version",
    size: 112,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">{row.version}</span>
    ),
    sortAccessor: (row) => row.version ?? "",
  },
  {
    key: "scope",
    header: "Scope",
    kind: "badge",
    size: 110,
    accessor: (row) => (
      <span className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground">
        {row.scope}
      </span>
    ),
    sortAccessor: (row) => row.scope ?? "",
  },
  ageColumn<GenericK8sResource>(),
];

const serviceAccountColumns: Column<GenericK8sResource>[] = [
  nameCol(),
  namespaceColumn<GenericK8sResource>(),
  countColumn<GenericK8sResource>(
    "secrets",
    "Secrets",
    (r) => r.secretsCount ?? 0,
  ),
  ageColumn<GenericK8sResource>(),
];

const k8sRoleColumns: Column<GenericK8sResource>[] = [
  nameCol(),
  namespaceColumn<GenericK8sResource>("-"),
  countColumn<GenericK8sResource>("rules", "Rules", (r) => r.rulesCount ?? 0),
  ageColumn<GenericK8sResource>(),
];

const k8sRoleBindingColumns: Column<GenericK8sResource>[] = [
  nameCol(),
  namespaceColumn<GenericK8sResource>("-"),
  monoTextColumn<GenericK8sResource>(
    "role",
    "Role",
    (r) => `${r.roleKind}/${r.roleName}`,
    { minSize: 224, size: 260 },
  ),
  countColumn<GenericK8sResource>(
    "subjects",
    "Subjects",
    (r) => r.subjectsCount ?? 0,
  ),
  ageColumn<GenericK8sResource>(),
];

const endpointColumns: Column<GenericK8sResource>[] = [
  nameCol(),
  namespaceColumn<GenericK8sResource>(),
  countColumn<GenericK8sResource>(
    "endpoints",
    "Endpoints",
    (r) => r.addressesCount ?? 0,
  ),
  monoTextColumn<GenericK8sResource>(
    "ports",
    "Ports",
    (r) => String(r.ports ?? ""),
    {
      minSize: 144,
      size: 168,
      sortable: false,
    },
  ),
  ageColumn<GenericK8sResource>(),
];

const replicaSetColumns: Column<GenericK8sResource>[] = [
  {
    key: "name",
    header: "Name",
    kind: "name",
    minSize: 220,
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    kind: "name",
    grow: false,
    size: 130,
    minSize: 110,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "desired",
    header: "Desired",
    kind: "count",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.desired ?? 0}</span>
    ),
  },
  {
    key: "ready",
    header: "Ready",
    kind: "count",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.ready ?? 0}</span>
    ),
  },
  {
    key: "available",
    header: "Available",
    kind: "count",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.available ?? 0}</span>
    ),
  },
  {
    key: "age",
    header: "Age",
    kind: "age",
    size: 80,
    accessor: (row) => (
      <TimestampCell
        value={row.createdAt}
        className="text-xs text-muted-foreground"
      />
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
