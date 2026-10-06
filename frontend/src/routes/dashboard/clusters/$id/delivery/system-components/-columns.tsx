import { Link } from "@tanstack/react-router";
import { DeliveryPhaseBadge } from "@/components/delivery/shared";
import type { Column } from "@/components/ui/data-table";
import type { DeliverySystemComponent } from "@/lib/api/delivery-system";
import { formatBytes } from "@/lib/utils";
import { replicaRedundancy } from "@/lib/system-component-availability";
import { EntityCell, TimestampCell } from "@/components/tables/cells";

function ownerLabel(owner: string) {
  if (owner === "flux") return "Flux";
  if (owner === "astronomer") return "Astronomer";
  if (owner === "external") return "External";
  return "Cluster";
}

export function systemComponentColumns(
  clusterId: string,
): Column<DeliverySystemComponent>[] {
  return [
    {
      key: "name",
      header: "Component",
      kind: "name",
      minSize: 240,
      pin: "start",
      accessor: (row) => {
        const href =
          "/dashboard/clusters/" +
          clusterId +
          "/delivery/system-components/" +
          encodeURIComponent(row.id);
        return (
          <EntityCell
            primary={
              <Link to={href} className="text-link hover:underline">
                {row.name}
              </Link>
            }
            secondary={`${row.kind} · ${row.managementMethod}`}
          />
        );
      },
      sortAccessor: (row) => row.name,
    },
    {
      key: "category",
      header: "Category",
      kind: "text",
      size: 120,
      minSize: 100,
      maxSize: 160,
      accessor: (row) => <span className="capitalize">{row.category}</span>,
      sortAccessor: (row) => row.category,
      filter: { label: "Categories" },
    },
    {
      key: "owner",
      header: "Owner",
      kind: "badge",
      size: 110,
      accessor: (row) => (
        <span className="rounded-full border border-border px-2 py-1 text-xs font-medium">
          {ownerLabel(row.owner)}
        </span>
      ),
      sortAccessor: (row) => row.owner,
      filter: { label: "Owners" },
    },
    {
      key: "managementMethod",
      header: "Managed by",
      kind: "text",
      size: 130,
      minSize: 100,
      maxSize: 160,
      accessor: (row) => (
        <span className="capitalize">{row.managementMethod}</span>
      ),
      sortAccessor: (row) => row.managementMethod,
      filter: { label: "Management methods" },
    },
    {
      key: "health",
      header: "Health",
      kind: "status",
      accessor: (row) => <DeliveryPhaseBadge value={row.health} />,
      sortAccessor: (row) => row.health,
      filter: { label: "Health" },
    },
    {
      key: "compatibility",
      header: "Compatibility",
      kind: "status",
      size: 150,
      accessor: (row) => (
        <DeliveryPhaseBadge value={row.compatibility || "unknown"} />
      ),
      sortAccessor: (row) => row.compatibility || "unknown",
      filter: { label: "Compatibility" },
    },
    {
      key: "updateState",
      header: "Update",
      kind: "status",
      accessor: (row) => (
        <DeliveryPhaseBadge value={row.updateState || "unknown"} />
      ),
      sortAccessor: (row) => row.updateState || "unknown",
      filter: { label: "Update state" },
    },
    {
      key: "replicas",
      header: "Ready",
      kind: "count",
      size: 110,
      accessor: (row) =>
        row.desiredReplicas ? (
          <div>
            <span className="tabular-nums">
              {row.readyReplicas}/{row.desiredReplicas}
            </span>
            <p className="text-xs text-muted-foreground">
              {replicaRedundancy(row)}
            </p>
          </div>
        ) : (
          "—"
        ),
      sortAccessor: (row) => row.readyReplicas ?? 0,
    },
    {
      key: "resources",
      header: "Requests / limits",
      kind: "text",
      size: 200,
      minSize: 190,
      maxSize: 240,
      accessor: (row) => (
        <div className="whitespace-nowrap text-xs">
          <p>
            CPU {row.cpuRequest || "—"} / {row.cpuLimit || "—"}
          </p>
          <p>
            Memory {row.memoryRequest || "—"} / {row.memoryLimit || "—"}
          </p>
        </div>
      ),
      sortable: false,
    },
    {
      key: "storage",
      header: "Storage",
      kind: "text",
      wrap: true,
      size: 220,
      minSize: 190,
      maxSize: 280,
      accessor: (row) =>
        row.storageClass ? (
          <div className="text-xs">
            <p className="font-medium text-foreground">{row.storageClass}</p>
            <p className="text-muted-foreground">
              {row.defaultStorage ? "Default · " : ""}
              {row.storageDriver}
            </p>
            {row.storageProvisionedBytes ? (
              <p className="text-muted-foreground">
                {formatBytes(row.storageUsedBytes || 0)} /{" "}
                {formatBytes(row.storageProvisionedBytes)}
                {row.storageReplicaCount
                  ? " · " + row.storageReplicaCount + " replicas"
                  : ""}
              </p>
            ) : null}
          </div>
        ) : (
          "—"
        ),
      sortAccessor: (row) => row.storageClass || "",
    },
    {
      key: "namespace",
      header: "Namespace",
      kind: "text",
      size: 170,
      minSize: 140,
      accessor: (row) =>
        row.namespace ? (
          <Link
            to={
              "/dashboard/clusters/" +
              clusterId +
              "/namespaces/" +
              row.namespace
            }
            className="font-mono text-xs text-link hover:underline"
          >
            {row.namespace}
          </Link>
        ) : (
          "Cluster scoped"
        ),
      sortAccessor: (row) => row.namespace || "",
    },
    {
      key: "version",
      header: "Version",
      kind: "version",
      maxSize: 220,
      accessor: (row) => (
        <span className="font-mono text-xs">{row.version || "—"}</span>
      ),
      sortAccessor: (row) => row.version || "",
    },
    {
      key: "age",
      header: "Age",
      kind: "age",
      accessor: (row) => (
        <TimestampCell value={row.createdAt} fallback="Unknown" />
      ),
      sortAccessor: (row) => row.createdAt || "",
    },
  ];
}
