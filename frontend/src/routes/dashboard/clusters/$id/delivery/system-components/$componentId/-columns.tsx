import { Link } from "@tanstack/react-router";
import { DeliveryPhaseBadge } from "@/components/delivery/shared";
import type { Column } from "@/components/ui/data-table";
import type {
  DeliverySystemResource,
  DeliverySystemVolume,
} from "@/lib/api/delivery-system";
import {
  evidenceHealth,
  type ComponentFreshness,
} from "@/lib/system-component-freshness";
import { crDetailHref, crListHref } from "@/lib/k8s-paths";
import { formatBytes } from "@/lib/utils";
import { EntityCell, TimestampCell } from "@/components/tables/cells";

export function systemVolumeColumns(
  clusterId: string,
  freshness: ComponentFreshness,
): Column<DeliverySystemVolume>[] {
  return [
    {
      key: "name",
      header: "Claim",
      kind: "name",
      minSize: 240,
      pin: "start",
      accessor: (row) => (
        <EntityCell
          primary={
            <Link
              to={
                "/dashboard/clusters/" +
                clusterId +
                "/persistentvolumeclaims/" +
                row.namespace +
                "/" +
                row.name
              }
              className="text-link hover:underline"
            >
              {row.name}
            </Link>
          }
          secondary={<span className="font-mono">{row.namespace}</span>}
        />
      ),
      sortAccessor: (row) => row.namespace + "/" + row.name,
    },
    {
      key: "phase",
      header: "Phase",
      kind: "status",
      accessor: (row) => (
        <DeliveryPhaseBadge value={evidenceHealth(freshness, row.phase)} />
      ),
      sortAccessor: (row) => evidenceHealth(freshness, row.phase),
      filter: { label: "Phases" },
    },
    {
      key: "storageClass",
      header: "Storage class",
      kind: "text",
      size: 170,
      minSize: 140,
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
      header: "Req / capacity",
      ariaLabel: "Requested / capacity",
      kind: "bytes",
      size: 170,
      accessor: (row) => (
        <span className="whitespace-nowrap tabular-nums">
          {formatBytes(row.requestedBytes || 0)} /{" "}
          {formatBytes(row.capacityBytes || 0)}
        </span>
      ),
      sortAccessor: (row) => row.capacityBytes || row.requestedBytes || 0,
    },
    {
      key: "accessModes",
      header: "Access",
      kind: "text",
      size: 200,
      minSize: 150,
      accessor: (row) => (row.accessModes ?? []).join(", ") || "—",
      sortAccessor: (row) => (row.accessModes ?? []).join(","),
    },
    {
      key: "expansionAllowed",
      header: "Expandable",
      kind: "status",
      size: 120,
      accessor: (row) => (row.expansionAllowed ? "Yes" : "No"),
      sortAccessor: (row) => (row.expansionAllowed ? 1 : 0),
      filter: { label: "Expansion" },
    },
    {
      key: "snapshots",
      header: "Snapshots",
      kind: "count",
      accessor: (row) =>
        row.snapshotCount ? (
          <Link
            to={crListHref(
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
    },
    {
      key: "age",
      header: "Age",
      kind: "age",
      size: 80,
      accessor: (row) => <TimestampCell value={row.createdAt} fallback="—" />,
      sortAccessor: (row) => row.createdAt || "",
    },
  ];
}

export function systemResourceColumns(
  clusterId: string,
  freshness: ComponentFreshness,
): Column<DeliverySystemResource>[] {
  return [
    {
      key: "name",
      header: "Resource",
      kind: "name",
      grow: false,
      size: 300,
      minSize: 240,
      accessor: (row) => (
        <EntityCell
          primary={
            <Link
              to={crDetailHref(
                clusterId,
                row.group || "",
                row.version,
                row.plural,
                row.name,
                row.namespace,
              )}
              className="text-link hover:underline"
            >
              {row.name}
            </Link>
          }
          secondary={
            <span className="font-mono">
              {row.namespace || "Cluster scoped"}
            </span>
          }
        />
      ),
      sortAccessor: (row) => (row.namespace || "") + "/" + row.name,
    },
    {
      key: "kind",
      header: "Kind",
      kind: "text",
      size: 170,
      minSize: 120,
      accessor: (row) => row.kind,
      sortAccessor: (row) => row.kind,
      filter: { label: "Kinds" },
    },
    {
      key: "health",
      header: "Health",
      kind: "status",
      accessor: (row) => (
        <DeliveryPhaseBadge value={evidenceHealth(freshness, row.health)} />
      ),
      sortAccessor: (row) => evidenceHealth(freshness, row.health),
      filter: { label: "Health" },
    },
    {
      key: "detail",
      header: "Observation",
      kind: "text",
      grow: true,
      minSize: 260,
      maxSize: 900,
      accessor: (row) => row.detail || "—",
      sortAccessor: (row) => row.detail || "",
      sortable: false,
    },
  ];
}
