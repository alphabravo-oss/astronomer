import { Link } from "@/lib/link";
import { DeliveryPhaseBadge } from "@/components/delivery/shared";
import type { Column } from "@/components/ui/data-table";
import type {
  DeliverySystemResource,
  DeliverySystemVolume,
} from "@/lib/api/delivery";
import { crDetailHref, crListHref } from "@/lib/k8s-paths";
import { formatBytes, formatRelativeTime } from "@/lib/utils";

export function systemVolumeColumns(
  clusterId: string,
): Column<DeliverySystemVolume>[] {
  return [
    {
      key: "name",
      header: "Claim",
      accessor: (row) => (
        <div>
          <Link
            href={
              "/dashboard/clusters/" +
              clusterId +
              "/persistentvolumeclaims/" +
              row.namespace +
              "/" +
              row.name
            }
            className="font-medium text-link hover:underline"
          >
            {row.name}
          </Link>
          <p className="font-mono text-xs text-muted-foreground">
            {row.namespace}
          </p>
        </div>
      ),
      sortAccessor: (row) => row.namespace + "/" + row.name,
    },
    {
      key: "phase",
      header: "Phase",
      accessor: (row) => <DeliveryPhaseBadge value={row.phase} />,
      sortAccessor: (row) => row.phase,
      filter: { label: "Phases" },
    },
    {
      key: "storageClass",
      header: "Storage class",
      accessor: (row) => (
        <div>
          <p className="font-medium">{row.storageClass || "Default"}</p>
          <p className="text-xs text-muted-foreground">
            {row.storageDriver || "Unknown driver"}
          </p>
        </div>
      ),
      sortAccessor: (row) => row.storageClass || "",
      filter: { label: "Storage classes" },
    },
    {
      key: "capacity",
      header: "Requested / capacity",
      accessor: (row) => (
        <span className="whitespace-nowrap tabular-nums">
          {formatBytes(row.requestedBytes || 0)} /{" "}
          {formatBytes(row.capacityBytes || 0)}
        </span>
      ),
      sortAccessor: (row) => row.capacityBytes || row.requestedBytes || 0,
      numeric: true,
    },
    {
      key: "accessModes",
      header: "Access",
      accessor: (row) => (row.accessModes ?? []).join(", ") || "—",
      sortAccessor: (row) => (row.accessModes ?? []).join(","),
    },
    {
      key: "expansionAllowed",
      header: "Expandable",
      accessor: (row) => (row.expansionAllowed ? "Yes" : "No"),
      sortAccessor: (row) => (row.expansionAllowed ? 1 : 0),
      filter: { label: "Expansion" },
    },
    {
      key: "snapshots",
      header: "Snapshots",
      accessor: (row) =>
        row.snapshotCount ? (
          <Link
            href={crListHref(
              clusterId,
              "snapshot.storage.k8s.io",
              "v1",
              "volumesnapshots",
            )}
            className="font-medium text-link hover:underline"
          >
            {row.snapshotCount}
          </Link>
        ) : (
          "0"
        ),
      sortAccessor: (row) => row.snapshotCount || 0,
      numeric: true,
    },
    {
      key: "age",
      header: "Age",
      accessor: (row) =>
        row.createdAt ? formatRelativeTime(row.createdAt) : "—",
      sortAccessor: (row) => row.createdAt || "",
    },
  ];
}

export function systemResourceColumns(
  clusterId: string,
): Column<DeliverySystemResource>[] {
  return [
    {
      key: "name",
      header: "Resource",
      accessor: (row) => (
        <div>
          <Link
            href={crDetailHref(
              clusterId,
              row.group || "",
              row.version,
              row.plural,
              row.name,
              row.namespace,
            )}
            className="font-medium text-link hover:underline"
          >
            {row.name}
          </Link>
          <p className="font-mono text-xs text-muted-foreground">
            {row.namespace || "Cluster scoped"}
          </p>
        </div>
      ),
      sortAccessor: (row) => (row.namespace || "") + "/" + row.name,
    },
    {
      key: "kind",
      header: "Kind",
      accessor: (row) => row.kind,
      sortAccessor: (row) => row.kind,
      filter: { label: "Kinds" },
    },
    {
      key: "health",
      header: "Health",
      accessor: (row) =>
        row.health ? <DeliveryPhaseBadge value={row.health} /> : "Unknown",
      sortAccessor: (row) => row.health || "unknown",
      filter: { label: "Health" },
    },
    {
      key: "detail",
      header: "Observation",
      accessor: (row) => row.detail || "—",
      sortAccessor: (row) => row.detail || "",
      sortable: false,
    },
  ];
}
