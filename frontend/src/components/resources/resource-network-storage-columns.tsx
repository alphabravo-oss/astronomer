/**
 * Column definitions for the Services, Ingresses, NetworkPolicies, PVs, PVCs
 * and StorageClasses tables. Every column declares a semantic `kind` (plan 031
 * P6b); the name column grows, long identifiers get a `minSize`.
 */
import { StatusBadge } from "@/components/ui/status-badge";
import type { Column } from "@/components/ui/data-table";
import { Tooltip } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import {
  ageColumn,
  ChipList,
  countColumn,
  monoTextColumn,
  nameStubColumn,
  namespaceColumn,
} from "@/components/resources/networking-table-cells";
import type {
  Ingress,
  K8sService,
  NetworkPolicy,
  PersistentVolume,
  PersistentVolumeClaim,
  StorageClass,
} from "@/types";

const yesNo = (on: boolean, yes: string) => (
  <span
    className={cn(
      "text-xs",
      on ? "text-status-success" : "text-muted-foreground",
    )}
  >
    {on ? yes : "No"}
  </span>
);

const formatPorts = (service: K8sService) =>
  service.ports?.map((p) => `${p.port}/${p.protocol}`).join(", ") ?? "";

export const serviceColumns: Column<K8sService>[] = [
  nameStubColumn<K8sService>(),
  namespaceColumn<K8sService>(),
  {
    key: "type",
    header: "Type",
    kind: "badge",
    minSize: 112,
    size: 112,
    accessor: (row) => (
      <span className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground">
        {row.type}
      </span>
    ),
    sortAccessor: (row) => row.type,
  },
  monoTextColumn<K8sService>("clusterIP", "Cluster IP", (r) => r.clusterIP, {
    minSize: 128,
    size: 132,
    maxSize: 200,
  }),
  monoTextColumn<K8sService>("ports", "Ports", formatPorts, {
    minSize: 144,
    size: 150,
    sortable: false,
  }),
  ageColumn<K8sService>(),
];

export const ingressColumns: Column<Ingress>[] = [
  nameStubColumn<Ingress>(),
  namespaceColumn<Ingress>(),
  {
    key: "class",
    header: "Class",
    kind: "text",
    minSize: 96,
    size: 104,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {row.ingressClass || "-"}
      </span>
    ),
    sortAccessor: (row) => row.ingressClass ?? "",
  },
  {
    key: "hosts",
    header: "Hosts",
    kind: "text",
    minSize: 200,
    size: 216,
    accessor: (row) => (
      <ChipList items={row.hosts?.length ? row.hosts : ["*"]} />
    ),
    searchAccessor: (row) => row.hosts?.join(" ") ?? "",
    sortable: false,
  },
  {
    key: "tls",
    header: "TLS",
    kind: "status",
    minSize: 80,
    size: 88,
    accessor: (row) => yesNo(row.tls, "Yes"),
    sortAccessor: (row) => (row.tls ? 1 : 0),
  },
  ageColumn<Ingress>(),
];

export const networkPolicyColumns: Column<NetworkPolicy>[] = [
  nameStubColumn<NetworkPolicy>(),
  namespaceColumn<NetworkPolicy>(),
  {
    key: "policyTypes",
    header: "Policy Types",
    kind: "badge",
    minSize: 144,
    size: 144,
    accessor: (row) => <ChipList items={row.policyTypes} mono={false} />,
    searchAccessor: (row) => row.policyTypes?.join(" ") ?? "",
    sortable: false,
  },
  countColumn<NetworkPolicy>("ingress", "Ingress Rules", (r) => r.ingressRules),
  countColumn<NetworkPolicy>("egress", "Egress Rules", (r) => r.egressRules),
  ageColumn<NetworkPolicy>(),
];

const ACCESS_MODE_ABBREVIATIONS: Record<string, string> = {
  ReadWriteOnce: "RWO",
  ReadOnlyMany: "ROX",
  ReadWriteMany: "RWX",
  ReadWriteOncePod: "RWOP",
};

function AccessModes({ modes }: { modes?: string[] }) {
  if (!modes?.length) {
    return <span className="text-xs text-muted-foreground">-</span>;
  }
  return (
    <Tooltip content={modes.join(", ")}>
      <span className="text-xs text-muted-foreground font-mono">
        {modes.map((m) => ACCESS_MODE_ABBREVIATIONS[m] ?? m).join(", ")}
      </span>
    </Tooltip>
  );
}

const quantityCell = (value: string) => (
  <span className="text-xs text-muted-foreground">{value}</span>
);

const accessModesColumn = <
  T extends { accessModes?: string[] },
>(): Column<T> => ({
  key: "accessModes",
  header: "Access",
  ariaLabel: "Access modes",
  kind: "text",
  minSize: 96,
  size: 96,
  accessor: (row) => <AccessModes modes={row.accessModes} />,
  searchAccessor: (row) => row.accessModes?.join(" ") ?? "",
  sortable: false,
});

const storageClassRef = <T extends { storageClass: string }>(): Column<T> =>
  monoTextColumn<T>("storageClass", "Class", (r) => r.storageClass, {
    ariaLabel: "Storage class",
    minSize: 96,
    size: 112,
  });

export const pvColumns: Column<PersistentVolume>[] = [
  nameStubColumn<PersistentVolume>(150),
  {
    key: "status",
    header: "Status",
    kind: "status",
    size: 104,
    minSize: 96,
    accessor: (row) => <StatusBadge status={row.status} />,
    sortAccessor: (row) => row.status,
  },
  {
    key: "capacity",
    header: "Capacity",
    kind: "bytes",
    size: 112,
    accessor: (row) => quantityCell(row.capacity),
  },
  accessModesColumn<PersistentVolume>(),
  storageClassRef<PersistentVolume>(),
  monoTextColumn<PersistentVolume>(
    "claimRef",
    "Claim",
    (r) => r.claimRef ?? "",
    {
      minSize: 152,
      size: 160,
    },
  ),
  ageColumn<PersistentVolume>(),
];

export const pvcColumns: Column<PersistentVolumeClaim>[] = [
  nameStubColumn<PersistentVolumeClaim>(150),
  { ...namespaceColumn<PersistentVolumeClaim>(), minSize: 120, size: 120 },
  {
    key: "status",
    header: "Status",
    kind: "status",
    size: 104,
    minSize: 96,
    accessor: (row) => <StatusBadge status={row.status} />,
    sortAccessor: (row) => row.status,
  },
  {
    key: "capacity",
    header: "Capacity",
    kind: "bytes",
    size: 112,
    accessor: (row) => quantityCell(row.capacity),
  },
  storageClassRef<PersistentVolumeClaim>(),
  {
    key: "volumeName",
    header: "Volume",
    kind: "id",
    minSize: 128,
    size: 136,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.volumeName || "-"}
      </span>
    ),
    sortAccessor: (row) => row.volumeName ?? "",
  },
  ageColumn<PersistentVolumeClaim>(),
];

export const storageClassColumns: Column<StorageClass>[] = [
  {
    ...nameStubColumn<StorageClass>(),
    accessor: (row) => (
      <div className="flex items-center gap-2">
        <span className="font-medium text-foreground font-mono text-xs">
          {row.name}
        </span>
        {row.isDefault && (
          <span className="px-1.5 py-0.5 rounded-sm text-2xs bg-status-info/10 text-status-info">
            default
          </span>
        )}
      </div>
    ),
  },
  monoTextColumn<StorageClass>(
    "provisioner",
    "Provisioner",
    (r) => r.provisioner,
    {
      minSize: 200,
      size: 208,
    },
  ),
  {
    key: "reclaimPolicy",
    header: "Reclaim Policy",
    kind: "text",
    minSize: 154,
    size: 154,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">{row.reclaimPolicy}</span>
    ),
    sortAccessor: (row) => row.reclaimPolicy,
  },
  {
    key: "volumeBindingMode",
    header: "Binding Mode",
    kind: "text",
    minSize: 160,
    size: 160,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {row.volumeBindingMode}
      </span>
    ),
    sortAccessor: (row) => row.volumeBindingMode,
  },
  {
    key: "expansion",
    header: "Expansion",
    kind: "status",
    size: 120,
    accessor: (row) => yesNo(row.allowVolumeExpansion, "Allowed"),
    sortAccessor: (row) => (row.allowVolumeExpansion ? 1 : 0),
  },
];
