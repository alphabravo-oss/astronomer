import { Link } from "@/lib/link";
import { DeliveryPhaseBadge } from "@/components/delivery/shared";
import type { Column } from "@/components/ui/data-table";
import type { DeliverySystemComponent } from "@/lib/api/delivery";
import { formatBytes, formatRelativeTime } from "@/lib/utils";

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
      accessor: (row) => {
        const href =
          "/dashboard/clusters/" +
          clusterId +
          "/delivery/system-components/" +
          encodeURIComponent(row.id);
        return (
          <div className="min-w-48">
            <Link href={href} className="font-medium text-link hover:underline">
              {row.name}
            </Link>
            <p className="mt-0.5 text-xs text-muted-foreground">
              {row.kind} · {row.managementMethod}
            </p>
          </div>
        );
      },
      sortAccessor: (row) => row.name,
    },
    {
      key: "category",
      header: "Category",
      accessor: (row) => <span className="capitalize">{row.category}</span>,
      sortAccessor: (row) => row.category,
      filter: { label: "Categories" },
    },
    {
      key: "owner",
      header: "Owner",
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
      accessor: (row) => (
        <span className="capitalize">{row.managementMethod}</span>
      ),
      sortAccessor: (row) => row.managementMethod,
      filter: { label: "Management methods" },
    },
    {
      key: "health",
      header: "Health",
      accessor: (row) => <DeliveryPhaseBadge value={row.health} />,
      sortAccessor: (row) => row.health,
      filter: { label: "Health" },
    },
    {
      key: "compatibility",
      header: "Compatibility",
      accessor: (row) => (
        <DeliveryPhaseBadge value={row.compatibility || "unknown"} />
      ),
      sortAccessor: (row) => row.compatibility || "unknown",
      filter: { label: "Compatibility" },
    },
    {
      key: "updateState",
      header: "Update",
      accessor: (row) => (
        <DeliveryPhaseBadge value={row.updateState || "unknown"} />
      ),
      sortAccessor: (row) => row.updateState || "unknown",
      filter: { label: "Update state" },
    },
    {
      key: "replicas",
      header: "Replicas",
      accessor: (row) =>
        row.desiredReplicas ? (
          <div>
            <span className="tabular-nums">
              {row.readyReplicas}/{row.desiredReplicas}
            </span>
            <p className="text-xs text-muted-foreground">
              {row.highAvailability ? "HA" : "Single replica"}
            </p>
          </div>
        ) : (
          "—"
        ),
      sortAccessor: (row) => row.readyReplicas ?? 0,
      numeric: true,
    },
    {
      key: "resources",
      header: "Requests / limits",
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
      accessor: (row) =>
        row.namespace ? (
          <Link
            href={
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
      accessor: (row) => (
        <span
          className="block max-w-40 truncate font-mono text-xs"
          title={row.version}
        >
          {row.version || "—"}
        </span>
      ),
      sortAccessor: (row) => row.version || "",
      code: true,
    },
    {
      key: "age",
      header: "Age",
      accessor: (row) =>
        row.createdAt ? formatRelativeTime(row.createdAt) : "Unknown",
      sortAccessor: (row) => row.createdAt || "",
    },
  ];
}
